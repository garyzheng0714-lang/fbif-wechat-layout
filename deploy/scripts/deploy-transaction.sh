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
# FBIF 中台下发的变量（2026-10-26 起）：本机代理（hub-agent）把它们整份写成下面两个文件
# （root:root 0600），systemd drop-in zz-fbif-hub.conf 让服务最后读、同名为准。CI 和 publish.env
# 不再放这些值；本脚本只读不写。换值在内部工具服务器上 hubctl put <编号>，
# 见 FBIF-One-Api 的 docs/hub/公共变量和密钥.md。
HUB_LAYOUT_ENV=/etc/fbif-hub/env/layout.env
HUB_RENDER_ENV=/etc/fbif-hub/env/render.env
API_READINESS_URL=http://127.0.0.1:9001/api/publish/readiness
CADDY_TARGET=/etc/caddy/sites/fbifmp-layout.caddy
API_SERVICE_TARGET=/etc/systemd/system/fbif-api.service
RENDER_SERVICE_TARGET=/etc/systemd/system/fbif-render.service
# 排版接口（/api/layout/v1）的排版程序：同一份 render-service 代码的另一个进程（layout-server.mjs，19098），
# PartOf=fbif-render，跟着它重启。第一次部署时还没有这个单元，所以部署前不要求它在跑。
RENDER_LAYOUT_SERVICE_TARGET=/etc/systemd/system/fbif-render-layout.service
CONFIG_SCHEMA_TARGET=/etc/fbif-layout/env-schema.json
# 监控 watchdog 已于 2026-08-06 迁往监控端 FoodTalks_Feed（服务名 fbif-link-watchdog）。
# 本项目只提供被探的健康端点 GET /api/publish/monitoring，不再安装、验收或回滚任何探针
# ——它探的三个服务里有两个是上游，配错了也没人看得见（曾连续失败 715 次）。见 CLAUDE.md
# 「三系统边界」第 3 条。旧的 /opt/fbif-monitor-watchdog 与两个 systemd 单元由本次部署的
# cleanup_legacy_watchdog 从生产摘除。
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

# hub_env_value 从中台文件取一个变量的值（同名取最后一行，和 systemd 一样）。中台写的值不带
# 引号、没有空白。值只经标准输出交给调用方的 $(...)，调用方不得打印、不得放进命令行参数。
hub_env_value() {
  local file=$1 name=$2 line value=''
  while IFS= read -r line || [[ -n "$line" ]]; do
    if [[ "$line" == "$name="* ]]; then
      value=${line#"$name="}
    fi
  done <"$file"
  printf '%s' "$value"
}

# require_hub_env 确认服务要读的中台文件在、归 root、权限 600，而且是这个服务最后读的
# EnvironmentFile（drop-in 没装或没 daemon-reload 时，服务会悄悄拿不到中台的值）。
require_hub_env() {
  local unit=$1 file=$2 owner_mode last_env
  [[ -f "$file" && ! -L "$file" ]] ||
    die "缺中台文件 $file：它由 FBIF 中台的本机代理（hub-agent）写，先在中台给 $unit 配好变量、等代理写出来再部署"
  owner_mode=$(stat -c '%u %a' -- "$file")
  [[ "$owner_mode" == "0 600" ]] ||
    die "中台文件 $file 必须归 root、权限 600（现在 uid 和权限是 $owner_mode）；它由中台代理写，别手改，查 hub-agent"
  last_env=$(systemctl show --property=EnvironmentFiles --value "$unit" | tail -n 1)
  [[ "$last_env" == "$file (ignore_errors="* ]] ||
    die "$unit 最后读的不是中台文件 $file：检查 /etc/systemd/system/$unit.service.d/zz-fbif-hub.conf，改完 systemctl daemon-reload"
}

# publish_readiness 在服务器本机取管理员 readiness，把响应 JSON 原样写到标准输出，部署工作流经
# SSH 拿去做 jq 断言。管理员服务令牌由中台下发、CI 不再持有：从中台文件读进变量，经标准输入交给
# curl 当请求头（printf 是内建命令，令牌不进任何进程的命令行），不打印；标准输出只有响应体。
publish_readiness() {
  local token
  require_hub_env fbif-api "$HUB_LAYOUT_ENV"
  token=$(hub_env_value "$HUB_LAYOUT_ENV" PUBLISH_ADMIN_SERVICE_TOKEN)
  [[ -n "$token" ]] || die "中台文件 $HUB_LAYOUT_ENV 里没有 PUBLISH_ADMIN_SERVICE_TOKEN"
  printf 'X-Publish-Admin-Token: %s\n' "$token" |
    curl --silent --show-error --fail --max-time 15 --header @- "$API_READINESS_URL"
}

validate_release() {
  local required
  for required in \
    "$RELEASE_DIR/public/version.json" \
    "$RELEASE_DIR/api/fbif-api" \
    "$RELEASE_DIR/api.env" \
    "$RELEASE_DIR/render.env" \
    "$RELEASE_DIR/render/server.mjs" \
    "$RELEASE_DIR/render/layout-server.mjs" \
    "$RELEASE_DIR/render/node_modules" \
    "$RELEASE_DIR/caddy/fbifmp-layout.caddy" \
    "$RELEASE_DIR/systemd/fbif-api.service" \
    "$RELEASE_DIR/systemd/fbif-render.service" \
    "$RELEASE_DIR/systemd/fbif-render-layout.service" \
    "$RELEASE_DIR/config/env-schema.json" \
    "$RELEASE_DIR/config/api.env.example" \
    "$RELEASE_DIR/config/publish.env.example" \
    "$RELEASE_DIR/config/render.env.example" \
    "$RELEASE_DIR/validate-env.py"; do
    [[ -e "$required" ]] || die "missing staged artifact: $required"
  done
  [[ -x "$RELEASE_DIR/api/fbif-api" ]] || die "staged API binary is not executable"

  local command_name
  for command_name in caddy curl install mv cp rm sed systemctl systemd-analyze python3 realpath \
    chown chmod date find sort grep stat tail; do
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
    "$RELEASE_DIR/systemd/fbif-render.service" \
    "$RELEASE_DIR/systemd/fbif-render-layout.service"
  # 中台下发的变量不在 api.env、render.env 里：校验时把中台文件叠在后面，和服务实际读到的一样。
  require_hub_env fbif-api "$HUB_LAYOUT_ENV"
  require_hub_env fbif-render "$HUB_RENDER_ENV"
  # 以前 CI 用同一个 Secret 写两边的 RENDER_TOKEN；现在两边各读一份中台文件，对不上每次渲染都 401。
  [[ "$(hub_env_value "$HUB_LAYOUT_ENV" RENDER_TOKEN)" == "$(hub_env_value "$HUB_RENDER_ENV" RENDER_TOKEN)" ]] ||
    die "中台文件 $HUB_LAYOUT_ENV 与 $HUB_RENDER_ENV 的 RENDER_TOKEN 不一致：API 调渲染服务会 401，先在中台对齐"
  python3 "$RELEASE_DIR/validate-env.py" --overlay "$HUB_LAYOUT_ENV" \
    "$RELEASE_DIR/config/env-schema.json" api_managed "$RELEASE_DIR/api.env"
  python3 "$RELEASE_DIR/validate-env.py" --overlay "$HUB_LAYOUT_ENV" \
    "$RELEASE_DIR/config/env-schema.json" api_publish /var/www/api/publish.env
  python3 "$RELEASE_DIR/validate-env.py" --overlay "$HUB_RENDER_ENV" \
    "$RELEASE_DIR/config/env-schema.json" render "$RELEASE_DIR/render.env"
  grep -qx 'ENV=production' "$RELEASE_DIR/api.env" ||
    die "staged API env must declare ENV=production"
  grep -qx 'DEPLOYMENT_WRITES_PAUSED=1' "$RELEASE_DIR/api.env" ||
    die "staged API env must start with DEPLOYMENT_WRITES_PAUSED=1"
  wait_http_ok http://127.0.0.1:9001/api/health 5
  wait_http_ok http://127.0.0.1:19096/healthz 5
}

# cleanup_legacy_watchdog 把已迁出的 fbif-monitor-watchdog 从生产彻底摘除。
#
# 幂等：单元不存在就什么都不做，因此重复部署安全。它替代了原来的
# prepare_monitoring/start_monitoring——那两步会安装探针、验收「timer 必须
# enabled 且 active」，等于每次部署都把已经停用的 watchdog 重新拉起来。
#
# 只摘除，不恢复：回滚不会把它装回来。这是有意的——监控职责已归监控端，
# 本项目回滚到旧产物也不该重新长出一个探别人死活的探针。
cleanup_legacy_watchdog() {
  [[ -d "$BACKUP_ROOT" ]] || die "active transaction backup not found: $BACKUP_ROOT"

  systemctl stop fbif-monitor-watchdog.timer >/dev/null 2>&1 || true
  systemctl stop fbif-monitor-watchdog.service >/dev/null 2>&1 || true
  systemctl disable fbif-monitor-watchdog.timer >/dev/null 2>&1 || true
  systemctl reset-failed fbif-monitor-watchdog.service >/dev/null 2>&1 || true
  rm -f -- /etc/systemd/system/fbif-monitor-watchdog.service
  rm -f -- /etc/systemd/system/fbif-monitor-watchdog.timer
  rm -rf -- /opt/fbif-monitor-watchdog
  systemctl daemon-reload

  # 摘干净才算数：残留的 unit 会让后来者以为探针还在守着。
  ! systemctl is-enabled --quiet fbif-monitor-watchdog.timer ||
    die "legacy watchdog timer is still enabled after cleanup"
  ! systemctl is-active --quiet fbif-monitor-watchdog.timer ||
    die "legacy watchdog timer is still active after cleanup"

  touch "$(marker monitoring.prepared)"
}

# cleanup_group_broadcast 把每天 08:30 的审核群监控播报从生产摘除。
#
# 2026-08-09 用户裁定：「这种播报不要发群里面，群里面只发卡片消息。因为大部分的
# 运营他们不需要知道这些东西，会给他们带来误解。」审核群是运营的操作台，只该有
# 「这篇要不要发」这一件事；链路状态、积压条数、失败计数是负责人关心的运维指标，
# 混进去只会稀释卡片、还容易被误读成待办。
#
# 监控能力不受影响：GET /api/publish/monitoring 仍如实上报，由监控端主动来探。
# 幂等；只摘除不恢复——回滚也不该让播报重新长回群里。
cleanup_group_broadcast() {
  [[ -d "$BACKUP_ROOT" ]] || die "active transaction backup not found: $BACKUP_ROOT"

  systemctl stop fbif-monitoring-report.timer >/dev/null 2>&1 || true
  systemctl stop fbif-monitoring-report.service >/dev/null 2>&1 || true
  systemctl disable fbif-monitoring-report.timer >/dev/null 2>&1 || true
  systemctl reset-failed fbif-monitoring-report.service >/dev/null 2>&1 || true
  rm -f -- /etc/systemd/system/fbif-monitoring-report.service
  rm -f -- /etc/systemd/system/fbif-monitoring-report.timer
  systemctl daemon-reload

  ! systemctl is-enabled --quiet fbif-monitoring-report.timer ||
    die "group broadcast timer is still enabled after cleanup"
  ! systemctl is-active --quiet fbif-monitoring-report.timer ||
    die "group broadcast timer is still active after cleanup"
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

  # 监控已迁往监控端，回滚不恢复 watchdog（见 cleanup_legacy_watchdog）。
  # 这次部署才第一次装上排版程序单元：回滚前先停掉、取消开机启动，再删单元文件。
  if [[ -e "$(marker render_layout_service.activated)" && ! -e "$(marker render_layout_service.had)" ]]; then
    systemctl disable --now fbif-render-layout >/dev/null 2>&1 || failed=1
  fi
  restore_path api_service "$API_SERVICE_TARGET" || failed=1
  restore_path render_service "$RENDER_SERVICE_TARGET" || failed=1
  restore_path render_layout_service "$RENDER_LAYOUT_SERVICE_TARGET" || failed=1
  restore_path render_env "$RENDER_ENV_TARGET" || failed=1
  restore_path config_schema "$CONFIG_SCHEMA_TARGET" || failed=1
  restore_path public "$PUBLIC_TARGET" || failed=1
  restore_path render "$RENDER_TARGET" || failed=1
  restore_path api "$API_TARGET" || failed=1
  restore_path env "$ENV_TARGET" || failed=1
  restore_path caddy "$CADDY_TARGET" || failed=1

  systemctl daemon-reload || failed=1

  # 首次部署原本没有 vhost 时，restore_path 会删掉失败候选而不恢复任何文件。
  caddy validate --config /etc/caddy/Caddyfile >/dev/null || failed=1
  systemctl restart fbif-render || failed=1
  if [[ -e "$RENDER_LAYOUT_SERVICE_TARGET" ]]; then
    systemctl restart fbif-render-layout || failed=1
  fi
  systemctl restart fbif-api || failed=1
  systemctl reload caddy || failed=1
  wait_http_ok http://127.0.0.1:19096/healthz 20 || failed=1
  if [[ -e "$RENDER_LAYOUT_SERVICE_TARGET" ]]; then
    wait_http_ok http://127.0.0.1:19098/healthz 20 || failed=1
  fi
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
  install_file render_layout_service "$RELEASE_DIR/systemd/fbif-render-layout.service" "$RENDER_LAYOUT_SERVICE_TARGET" 0644
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
  [[ "$(systemctl show --property=FragmentPath --value fbif-render-layout)" == "$RENDER_LAYOUT_SERVICE_TARGET" ]] ||
    die "fbif-render-layout is not using the versioned unit"
  require_hub_env fbif-render-layout "$HUB_RENDER_ENV"
  systemctl restart fbif-render
  wait_runtime_config_ok http://127.0.0.1:19096/healthz config_schema_version
  systemctl enable fbif-render-layout >/dev/null
  systemctl restart fbif-render-layout
  wait_runtime_config_ok http://127.0.0.1:19098/healthz config_schema_version
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
  [[ -e "$(marker monitoring.prepared)" ]] || die "legacy watchdog cleanup did not run"
  grep -qx 'DEPLOYMENT_WRITES_PAUSED=0' "$ENV_TARGET" ||
    die "release cannot be finalized while deployment writes are paused"

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
      "$history_pending/systemd"

    # 只复制已验收的当前 live 产物。.env、publish.env 和任何其他
    # 明文密钥都不进历史目录。reflink 可用时使用 CoW，不可用时正常复制。
    cp -a --reflink=auto -- "$PUBLIC_TARGET" "$history_pending/public"
    cp -a --reflink=auto -- "$RENDER_TARGET" "$history_pending/render"
    cp -a --reflink=auto -- "$API_TARGET" "$history_pending/api/fbif-api"
    cp -a --reflink=auto -- "$CADDY_TARGET" "$history_pending/caddy/fbifmp-layout.caddy"
    cp -a --reflink=auto -- "$CONFIG_SCHEMA_TARGET" "$history_pending/config/env-schema.json"
    cp -a --reflink=auto -- "$API_SERVICE_TARGET" "$history_pending/systemd/fbif-api.service"
    cp -a --reflink=auto -- "$RENDER_SERVICE_TARGET" "$history_pending/systemd/fbif-render.service"
    cp -a --reflink=auto -- "$RENDER_LAYOUT_SERVICE_TARGET" "$history_pending/systemd/fbif-render-layout.service"
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
  readiness)
    publish_readiness
    ;;
  prepare-monitoring)
    cleanup_legacy_watchdog
    cleanup_group_broadcast
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
    die "usage: $0 {preflight|activate|readiness|prepare-monitoring|unpause|rollback|finalize} RELEASE_DIR PUBLIC_TARGET DEPLOY_ID"
    ;;
esac
