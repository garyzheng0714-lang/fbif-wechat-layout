#!/usr/bin/env bash
set -Eeuo pipefail

# 远端部署事务：所有候选文件必须先放进 RELEASE_DIR。本脚本只在显式 activate 时
# 改写 live 路径，并把旧版本保留到公网验收完成；任一步失败都会恢复旧静态、API、
# render、env 和 Caddy 配置，再重启旧服务。

ACTION=${1:-}
RELEASE_DIR=${2:-}
PUBLIC_TARGET=${3:-}
DEPLOY_ID=${4:-}

die() {
  echo "deploy transaction: $*" >&2
  exit 1
}

[[ ${EUID:-$(id -u)} -eq 0 ]] || die "must run as root"
[[ "$DEPLOY_ID" =~ ^[A-Za-z0-9._-]+$ ]] || die "invalid deploy id"
command -v realpath >/dev/null || die "missing remote command: realpath"
[[ "$RELEASE_DIR" != *"/../"* && "$RELEASE_DIR" != */.. ]] || die "release dir contains parent traversal"
[[ "$PUBLIC_TARGET" != *"/../"* && "$PUBLIC_TARGET" != */.. ]] || die "public target contains parent traversal"
RELEASE_DIR=$(realpath -m -- "$RELEASE_DIR")
PUBLIC_TARGET=$(realpath -m -- "$PUBLIC_TARGET")
[[ "$RELEASE_DIR" == "/var/www/.fbif-layout-releases/$DEPLOY_ID" ]] || die "unsafe or symlinked release dir"
[[ "$PUBLIC_TARGET" == "/var/www/wechat-layout" ]] || die "public target must be /var/www/wechat-layout and must not resolve through a symlink"

API_DIR=$(dirname "$PUBLIC_TARGET")/api
API_TARGET=$API_DIR/fbif-api
ENV_TARGET=$API_DIR/.env
RENDER_TARGET=/var/www/render-service
RENDER_ENV_TARGET=/var/www/render-service.env
CADDY_TARGET=/etc/caddy/sites/fbifmp-layout.caddy
API_SERVICE_TARGET=/etc/systemd/system/fbif-api.service
RENDER_SERVICE_TARGET=/etc/systemd/system/fbif-render.service
CONFIG_SCHEMA_TARGET=/etc/fbif-layout/env-schema.json
WATCHDOG_DIR=/opt/fbif-monitor-watchdog
WATCHDOG_TARGET=$WATCHDOG_DIR/watchdog.py
WATCHDOG_SERVICE_TARGET=/etc/systemd/system/fbif-monitor-watchdog.service
WATCHDOG_TIMER_TARGET=/etc/systemd/system/fbif-monitor-watchdog.timer
BACKUP_ROOT=/var/www/.fbif-layout-backups/$DEPLOY_ID
HISTORY_ROOT=/var/www/.fbif-layout-history
HISTORY_RETENTION=5

marker() {
  printf '%s/%s' "$BACKUP_ROOT" "$1"
}

wait_http_ok() {
  local url=$1
  local attempts=${2:-30}
  local i
  for ((i = 1; i <= attempts; i++)); do
    if curl --silent --show-error --fail --max-time 3 "$url" >/dev/null; then
      return 0
    fi
    sleep 1
  done
  echo "deploy transaction: health check failed: $url" >&2
  return 1
}

wait_runtime_config_ok() {
  local url=$1
  local field=$2
  local attempts=${3:-30}
  local i payload
  for ((i = 1; i <= attempts; i++)); do
    if payload=$(curl --silent --show-error --fail --max-time 3 "$url") &&
      printf '%s' "$payload" | python3 -c \
        'import json,sys; value=json.load(sys.stdin); raise SystemExit(0 if str(value.get(sys.argv[1], "")) == "1" else 1)' \
        "$field"; then
      return 0
    fi
    sleep 1
  done
  echo "deploy transaction: config schema health check failed: $url" >&2
  return 1
}

validate_release() {
  local required
  for required in \
    "$RELEASE_DIR/public/version.json" \
    "$RELEASE_DIR/api/fbif-api" \
    "$RELEASE_DIR/api.env" \
    "$RELEASE_DIR/render.env" \
    "$RELEASE_DIR/render/server.mjs" \
    "$RELEASE_DIR/render/node_modules" \
    "$RELEASE_DIR/caddy/fbifmp-layout.caddy" \
    "$RELEASE_DIR/systemd/fbif-api.service" \
    "$RELEASE_DIR/systemd/fbif-render.service" \
    "$RELEASE_DIR/config/env-schema.json" \
    "$RELEASE_DIR/config/api.env.example" \
    "$RELEASE_DIR/config/publish.env.example" \
    "$RELEASE_DIR/config/render.env.example" \
    "$RELEASE_DIR/validate-env.py" \
    "$RELEASE_DIR/monitoring/watchdog.py" \
    "$RELEASE_DIR/monitoring/fbif-monitor-watchdog.service" \
    "$RELEASE_DIR/monitoring/fbif-monitor-watchdog.timer"; do
    [[ -e "$required" ]] || die "missing staged artifact: $required"
  done
  [[ -x "$RELEASE_DIR/api/fbif-api" ]] || die "staged API binary is not executable"

  local command_name
  for command_name in caddy curl install mv cp rm sed systemctl systemd-analyze python3 realpath \
    chown chmod date find sort grep; do
    command -v "$command_name" >/dev/null || die "missing remote command: $command_name"
  done

  systemctl is-active --quiet fbif-api || die "fbif-api is not active before deploy"
  systemctl is-active --quiet fbif-render || die "fbif-render is not active before deploy"
  systemctl is-active --quiet caddy || die "caddy is not active before deploy"
  systemctl is-enabled --quiet fbif-api || die "fbif-api is not enabled before deploy"
  systemctl is-enabled --quiet fbif-render || die "fbif-render is not enabled before deploy"
  [[ -x /usr/bin/node ]] || die "missing /usr/bin/node required by fbif-render.service"
  caddy validate --config /etc/caddy/Caddyfile >/dev/null
  caddy validate --adapter caddyfile --config "$RELEASE_DIR/caddy/fbifmp-layout.caddy" >/dev/null
  systemd-analyze verify \
    "$RELEASE_DIR/systemd/fbif-api.service" \
    "$RELEASE_DIR/systemd/fbif-render.service"
  python3 "$RELEASE_DIR/validate-env.py" "$RELEASE_DIR/config/env-schema.json" api_managed "$RELEASE_DIR/api.env"
  python3 "$RELEASE_DIR/validate-env.py" "$RELEASE_DIR/config/env-schema.json" api_publish /var/www/api/publish.env
  python3 "$RELEASE_DIR/validate-env.py" "$RELEASE_DIR/config/env-schema.json" render "$RELEASE_DIR/render.env"
  grep -qx 'ENV=production' "$RELEASE_DIR/api.env" ||
    die "staged API env must declare ENV=production"
  grep -qx 'DEPLOYMENT_WRITES_PAUSED=1' "$RELEASE_DIR/api.env" ||
    die "staged API env must start with DEPLOYMENT_WRITES_PAUSED=1"
  wait_http_ok http://127.0.0.1:9001/api/health 5
  wait_http_ok http://127.0.0.1:19096/healthz 5
}

prepare_monitoring() {
  [[ -d "$BACKUP_ROOT" ]] || die "active transaction backup not found: $BACKUP_ROOT"
  grep -qx 'DEPLOYMENT_WRITES_PAUSED=1' "$ENV_TARGET" ||
    die "monitoring must be prepared while deployment writes remain paused"
  [[ -r /var/www/api/publish.env ]] || die "publish.env is required by monitoring watchdog"
  [[ ! -e "$(marker monitoring.prepared)" ]] || die "monitoring is already prepared"

  # 先记录旧 timer 状态。候选单元在整个可回滚验收窗口内保持停用，
  # 避免把预期的 worker paused 状态误报为生产事故。
  if systemctl is-enabled --quiet fbif-monitor-watchdog.timer; then
    touch "$(marker watchdog_timer.was_enabled)"
  fi
  if systemctl is-active --quiet fbif-monitor-watchdog.timer; then
    touch "$(marker watchdog_timer.was_active)"
    systemctl stop fbif-monitor-watchdog.timer
  fi
  if systemctl is-enabled --quiet fbif-monitor-watchdog.timer; then
    systemctl disable fbif-monitor-watchdog.timer
  fi
  if systemctl is-active --quiet fbif-monitor-watchdog.service; then
    systemctl stop fbif-monitor-watchdog.service
  fi

  install_file watchdog "$RELEASE_DIR/monitoring/watchdog.py" "$WATCHDOG_TARGET" 0755
  install_file watchdog_service "$RELEASE_DIR/monitoring/fbif-monitor-watchdog.service" "$WATCHDOG_SERVICE_TARGET" 0644
  install_file watchdog_timer "$RELEASE_DIR/monitoring/fbif-monitor-watchdog.timer" "$WATCHDOG_TIMER_TARGET" 0644

  # compile() 只读候选脚本，不在 release 目录留 __pycache__。
  python3 -c 'import pathlib,sys; path=pathlib.Path(sys.argv[1]); compile(path.read_bytes(), str(path), "exec")' "$WATCHDOG_TARGET"
  systemctl daemon-reload
  systemd-analyze verify "$WATCHDOG_SERVICE_TARGET" "$WATCHDOG_TIMER_TARGET"
  systemctl disable fbif-monitor-watchdog.timer >/dev/null
  systemctl stop fbif-monitor-watchdog.timer
  ! systemctl is-enabled --quiet fbif-monitor-watchdog.timer || die "candidate watchdog timer must remain disabled"
  ! systemctl is-active --quiet fbif-monitor-watchdog.timer || die "candidate watchdog timer must remain inactive"
  rm -f -- "$WATCHDOG_DIR/deployment-check"
  touch "$(marker monitoring.prepared)"
}

start_monitoring() {
  [[ -d "$BACKUP_ROOT" ]] || die "active transaction backup not found: $BACKUP_ROOT"
  [[ -e "$(marker monitoring.prepared)" ]] || die "monitoring has not been prepared"
  grep -qx 'DEPLOYMENT_WRITES_PAUSED=0' "$ENV_TARGET" ||
    die "monitoring may only start after deployment writes are unpaused"

  # 先执行一次真实探测并发送明确的飞书部署自检消息；配置、网络、
  # 探针健康或飞书上报任一失败，都不会 enable 定时器。
  touch "$WATCHDOG_DIR/deployment-check"
  systemctl reset-failed fbif-monitor-watchdog.service >/dev/null 2>&1 || true
  systemctl start fbif-monitor-watchdog.service
  local probe_status
  probe_status=$(systemctl show --property=ExecMainStatus --value fbif-monitor-watchdog.service)
  [[ "$probe_status" == "0" ]] || die "initial watchdog probe was not healthy: exit $probe_status"
  rm -f -- "$WATCHDOG_DIR/deployment-check"
  systemctl enable --now fbif-monitor-watchdog.timer
  systemctl is-enabled --quiet fbif-monitor-watchdog.timer
  systemctl is-active --quiet fbif-monitor-watchdog.timer
  touch "$(marker monitoring.started)"
}

restore_path() {
  local name=$1
  local target=$2
  local failed=0
  rm -rf -- "$target.new.$DEPLOY_ID" || failed=1
  if [[ -e "$(marker "$name.activated")" ]]; then
    rm -rf -- "$target" || failed=1
  fi
  if [[ -e "$(marker "$name.had")" ]]; then
    if [[ -e "$BACKUP_ROOT/$name" || -L "$BACKUP_ROOT/$name" ]]; then
      # 保留 backup 到整个 rollback 验收成功后再统一删除。这样回滚被再次取消、
      # marker 清理失败或 Actions 重试 rollback 时，仍可幂等地删除半成品并重拷。
      cp -a -- "$BACKUP_ROOT/$name" "$target" || failed=1
    else
      echo "deploy transaction: missing backup for $name" >&2
      failed=1
    fi
  fi
  if [[ $failed -eq 0 ]]; then
    rm -f -- "$(marker "$name.activated")" "$(marker "$name.had")" || failed=1
  fi
  return "$failed"
}

rollback_impl() {
  set +e
  local failed=0

  # 候选监控也是 live surface。先停掉它，再恢复脚本和单元文件。
  systemctl stop fbif-monitor-watchdog.timer fbif-monitor-watchdog.service >/dev/null 2>&1 || true
  systemctl disable fbif-monitor-watchdog.timer >/dev/null 2>&1 || true
  rm -f -- "$WATCHDOG_DIR/deployment-check" || failed=1
  restore_path watchdog "$WATCHDOG_TARGET" || failed=1
  restore_path watchdog_service "$WATCHDOG_SERVICE_TARGET" || failed=1
  restore_path watchdog_timer "$WATCHDOG_TIMER_TARGET" || failed=1
  restore_path api_service "$API_SERVICE_TARGET" || failed=1
  restore_path render_service "$RENDER_SERVICE_TARGET" || failed=1
  restore_path render_env "$RENDER_ENV_TARGET" || failed=1
  restore_path config_schema "$CONFIG_SCHEMA_TARGET" || failed=1
  restore_path public "$PUBLIC_TARGET" || failed=1
  restore_path render "$RENDER_TARGET" || failed=1
  restore_path api "$API_TARGET" || failed=1
  restore_path env "$ENV_TARGET" || failed=1
  restore_path caddy "$CADDY_TARGET" || failed=1

  systemctl daemon-reload || failed=1
  if [[ -e "$(marker watchdog_timer.was_enabled)" ]]; then
    systemctl enable fbif-monitor-watchdog.timer || failed=1
  elif [[ -e "$WATCHDOG_TIMER_TARGET" ]]; then
    systemctl disable fbif-monitor-watchdog.timer >/dev/null || failed=1
  fi
  if [[ -e "$(marker watchdog_timer.was_active)" ]]; then
    systemctl start fbif-monitor-watchdog.timer || failed=1
  elif [[ -e "$WATCHDOG_TIMER_TARGET" ]]; then
    systemctl stop fbif-monitor-watchdog.timer || failed=1
  fi

  # 首次部署原本没有 vhost 时，restore_path 会删掉失败候选而不恢复任何文件。
  caddy validate --config /etc/caddy/Caddyfile >/dev/null || failed=1
  systemctl restart fbif-render || failed=1
  systemctl restart fbif-api || failed=1
  systemctl reload caddy || failed=1
  wait_http_ok http://127.0.0.1:19096/healthz 20 || failed=1
  wait_http_ok http://127.0.0.1:9001/api/health 20 || failed=1

  if [[ $failed -ne 0 ]]; then
    echo "deploy transaction: rollback incomplete; backup kept at $BACKUP_ROOT" >&2
    return 1
  fi
  rm -rf -- "$BACKUP_ROOT"
  return 0
}

backup_file() {
  local name=$1
  local target=$2
  if [[ -e "$target" || -L "$target" ]]; then
    cp -a -- "$target" "$BACKUP_ROOT/$name"
    touch "$(marker "$name.had")"
  fi
}

install_file() {
  local name=$1
  local staged=$2
  local target=$3
  local mode=$4
  backup_file "$name" "$target"
  touch "$(marker "$name.activated")"
  install -D -m "$mode" -- "$staged" "$target.new.$DEPLOY_ID"
  mv -f -- "$target.new.$DEPLOY_ID" "$target"
}

swap_directory() {
  local name=$1
  local staged=$2
  local target=$3
  if [[ -e "$target" || -L "$target" ]]; then
    touch "$(marker "$name.had")"
    mv -- "$target" "$BACKUP_ROOT/$name"
  fi
  touch "$(marker "$name.activated")"
  mv -- "$staged" "$target"
}

activate_release() {
  validate_release
  [[ ! -e "$BACKUP_ROOT" ]] || die "backup already exists: $BACKUP_ROOT"
  install -d -m 700 "$BACKUP_ROOT"

  local activated=0
  on_activate_error() {
    local rc=${1:-$?}
    trap - ERR HUP INT TERM
    if [[ $activated -eq 1 ]]; then
      rollback_impl || true
    fi
    exit "$rc"
  }
  trap 'on_activate_error $?' ERR
  trap 'on_activate_error 129' HUP
  trap 'on_activate_error 130' INT
  trap 'on_activate_error 143' TERM
  activated=1

  # Caddy 候选先进入事务并做全局 validate，但在服务和静态资源全部就绪前不 reload。
  install_file caddy "$RELEASE_DIR/caddy/fbifmp-layout.caddy" "$CADDY_TARGET" 0644
  caddy validate --config /etc/caddy/Caddyfile >/dev/null

  install_file api_service "$RELEASE_DIR/systemd/fbif-api.service" "$API_SERVICE_TARGET" 0644
  install_file render_service "$RELEASE_DIR/systemd/fbif-render.service" "$RENDER_SERVICE_TARGET" 0644
  install_file render_env "$RELEASE_DIR/render.env" "$RENDER_ENV_TARGET" 0600
  install_file config_schema "$RELEASE_DIR/config/env-schema.json" "$CONFIG_SCHEMA_TARGET" 0644
  install_file api "$RELEASE_DIR/api/fbif-api" "$API_TARGET" 0755
  install_file env "$RELEASE_DIR/api.env" "$ENV_TARGET" 0600
  swap_directory render "$RELEASE_DIR/render" "$RENDER_TARGET"
  swap_directory public "$RELEASE_DIR/public" "$PUBLIC_TARGET"
  ln -snf "$PUBLIC_TARGET" /var/www/public

  systemctl daemon-reload
  [[ "$(systemctl show --property=FragmentPath --value fbif-api)" == "$API_SERVICE_TARGET" ]] ||
    die "fbif-api is not using the versioned unit"
  [[ "$(systemctl show --property=FragmentPath --value fbif-render)" == "$RENDER_SERVICE_TARGET" ]] ||
    die "fbif-render is not using the versioned unit"
  systemctl restart fbif-render
  wait_runtime_config_ok http://127.0.0.1:19096/healthz config_schema_version
  systemctl restart fbif-api
  wait_runtime_config_ok http://127.0.0.1:9001/api/health config_schema_version
  systemctl reload caddy

  trap - ERR HUP INT TERM
}

unpause_release() {
  [[ -d "$BACKUP_ROOT" ]] || die "active transaction backup not found: $BACKUP_ROOT"
  grep -qx 'DEPLOYMENT_WRITES_PAUSED=1' "$ENV_TARGET" ||
    die "live API env is not in deployment pause mode"
  local next_env="$ENV_TARGET.unpause.$DEPLOY_ID"
  install -m 0600 -- "$ENV_TARGET" "$next_env"
  sed -i 's/^DEPLOYMENT_WRITES_PAUSED=1$/DEPLOYMENT_WRITES_PAUSED=0/' "$next_env"
  grep -qx 'DEPLOYMENT_WRITES_PAUSED=0' "$next_env" || die "failed to clear deployment pause"
  mv -f -- "$next_env" "$ENV_TARGET"
  # 从这里开始任务恢复和外部副作用可能发生；调用方必须先完成候选 release 验收，
  # 且此后不得自动 rollback 到旧二进制/旧 env。
  systemctl restart fbif-api
  wait_runtime_config_ok http://127.0.0.1:9001/api/health config_schema_version
}

finalize_release() {
  [[ -d "$BACKUP_ROOT" ]] || die "active transaction backup not found: $BACKUP_ROOT"
  [[ -e "$(marker monitoring.started)" ]] || die "monitoring did not complete its initial probe"
  grep -qx 'DEPLOYMENT_WRITES_PAUSED=0' "$ENV_TARGET" ||
    die "release cannot be finalized while deployment writes are paused"
  systemctl is-enabled --quiet fbif-monitor-watchdog.timer ||
    die "watchdog timer is not enabled"
  systemctl is-active --quiet fbif-monitor-watchdog.timer ||
    die "watchdog timer is not active"

  # 历史目录是 root 操作的固定路径；不接受软链接或外部参数。
  [[ ! -L "$HISTORY_ROOT" ]] || die "history root must not be a symlink"
  install -d -m 0700 "$HISTORY_ROOT"
  [[ "$(realpath -m -- "$HISTORY_ROOT")" == "$HISTORY_ROOT" ]] || die "unsafe history root"

  local history_target="$HISTORY_ROOT/$DEPLOY_ID"
  local history_pending="$HISTORY_ROOT/.pending-$DEPLOY_ID"
  [[ "$(realpath -m -- "$history_target")" == "$HISTORY_ROOT/$DEPLOY_ID" ]] ||
    die "unsafe history target"
  [[ "$(realpath -m -- "$history_pending")" == "$HISTORY_ROOT/.pending-$DEPLOY_ID" ]] ||
    die "unsafe pending history target"

  if [[ -e "$history_target" ]]; then
    grep -Fqx "deploy_id=$DEPLOY_ID" "$history_target/release.meta" ||
      die "existing history entry does not match deploy id"
  else
    rm -rf -- "$history_pending"
    install -d -m 0700 \
      "$history_pending/api" \
      "$history_pending/caddy" \
      "$history_pending/config" \
      "$history_pending/systemd" \
      "$history_pending/monitoring"

    # 只复制已验收的当前 live 产物。.env、publish.env 和任何其他
    # 明文密钥都不进历史目录。reflink 可用时使用 CoW，不可用时正常复制。
    cp -a --reflink=auto -- "$PUBLIC_TARGET" "$history_pending/public"
    cp -a --reflink=auto -- "$RENDER_TARGET" "$history_pending/render"
    cp -a --reflink=auto -- "$API_TARGET" "$history_pending/api/fbif-api"
    cp -a --reflink=auto -- "$CADDY_TARGET" "$history_pending/caddy/fbifmp-layout.caddy"
    cp -a --reflink=auto -- "$CONFIG_SCHEMA_TARGET" "$history_pending/config/env-schema.json"
    cp -a --reflink=auto -- "$API_SERVICE_TARGET" "$history_pending/systemd/fbif-api.service"
    cp -a --reflink=auto -- "$RENDER_SERVICE_TARGET" "$history_pending/systemd/fbif-render.service"
    cp -a --reflink=auto -- "$WATCHDOG_TARGET" "$history_pending/monitoring/watchdog.py"
    cp -a --reflink=auto -- "$WATCHDOG_SERVICE_TARGET" "$history_pending/systemd/fbif-monitor-watchdog.service"
    cp -a --reflink=auto -- "$WATCHDOG_TIMER_TARGET" "$history_pending/systemd/fbif-monitor-watchdog.timer"
    printf 'deploy_id=%s\nfinalized_at=%s\n' "$DEPLOY_ID" "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
      > "$history_pending/release.meta"

    chown -hR root:root "$history_pending"
    chmod -R go-w "$history_pending"
    mv -- "$history_pending" "$history_target"
  fi

  # 仅保留最新 5 个完整快照。find 只枚举单层目录，名称再次按
  # DEPLOY_ID allowlist 检查，rm 之前还会重新确认 canonical path。
  local kept=0 entry_name entry_path
  while read -r _ entry_name; do
    [[ "$entry_name" =~ ^[A-Za-z0-9._-]+$ ]] || die "unsafe history entry: $entry_name"
    entry_path=$(realpath -m -- "$HISTORY_ROOT/$entry_name")
    [[ "$entry_path" == "$HISTORY_ROOT/$entry_name" ]] || die "unsafe history entry path"
    kept=$((kept + 1))
    if ((kept > HISTORY_RETENTION)); then
      rm -rf -- "$entry_path"
    fi
  done < <(find "$HISTORY_ROOT" -mindepth 1 -maxdepth 1 -type d ! -name '.pending-*' \
    -printf '%T@ %f\n' | sort -nr)

  # 保留快照完成后才销毁含旧 env 的短期事务备份与 staging。
  rm -rf -- "$BACKUP_ROOT" "$RELEASE_DIR"
}

case "$ACTION" in
  preflight)
    validate_release
    ;;
  activate)
    activate_release
    ;;
  unpause)
    unpause_release
    ;;
  prepare-monitoring)
    prepare_monitoring
    ;;
  start-monitoring)
    start_monitoring
    ;;
  rollback)
    if [[ -d "$BACKUP_ROOT" ]]; then
      rollback_impl
    fi
    ;;
  finalize)
    finalize_release
    ;;
  *)
    die "usage: $0 {preflight|activate|prepare-monitoring|unpause|start-monitoring|rollback|finalize} RELEASE_DIR PUBLIC_TARGET DEPLOY_ID"
    ;;
esac
