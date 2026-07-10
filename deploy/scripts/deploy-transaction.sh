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
[[ "$RELEASE_DIR" == /var/www/.fbif-layout-releases/* ]] || die "unsafe release dir"
[[ "$PUBLIC_TARGET" == /var/www/* && "$PUBLIC_TARGET" != /var/www/ ]] || die "unsafe public target"

API_DIR=$(dirname "$PUBLIC_TARGET")/api
API_TARGET=$API_DIR/fbif-api
ENV_TARGET=$API_DIR/.env
RENDER_TARGET=/var/www/render-service
CADDY_TARGET=/etc/caddy/sites/fbifmp-layout.caddy
BACKUP_ROOT=/var/www/.fbif-layout-backups/$DEPLOY_ID

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

validate_release() {
  local required
  for required in \
    "$RELEASE_DIR/public/version.json" \
    "$RELEASE_DIR/api/fbif-api" \
    "$RELEASE_DIR/api.env" \
    "$RELEASE_DIR/render/server.mjs" \
    "$RELEASE_DIR/render/node_modules" \
    "$RELEASE_DIR/caddy/fbifmp-layout.caddy"; do
    [[ -e "$required" ]] || die "missing staged artifact: $required"
  done
  [[ -x "$RELEASE_DIR/api/fbif-api" ]] || die "staged API binary is not executable"

  local command_name
  for command_name in caddy curl install mv cp rm systemctl; do
    command -v "$command_name" >/dev/null || die "missing remote command: $command_name"
  done

  systemctl is-active --quiet fbif-api || die "fbif-api is not active before deploy"
  systemctl is-active --quiet fbif-render || die "fbif-render is not active before deploy"
  systemctl is-active --quiet caddy || die "caddy is not active before deploy"
  caddy validate --config /etc/caddy/Caddyfile >/dev/null
  caddy validate --adapter caddyfile --config "$RELEASE_DIR/caddy/fbifmp-layout.caddy" >/dev/null
  wait_http_ok http://127.0.0.1:9001/api/health 5
  wait_http_ok http://127.0.0.1:19096/healthz 5
}

restore_path() {
  local name=$1
  local target=$2
  if [[ -e "$(marker "$name.activated")" ]]; then
    rm -rf -- "$target"
  fi
  if [[ -e "$(marker "$name.had")" && -e "$BACKUP_ROOT/$name" ]]; then
    mv -- "$BACKUP_ROOT/$name" "$target"
  fi
}

rollback_impl() {
  set +e
  local failed=0

  restore_path public "$PUBLIC_TARGET" || failed=1
  restore_path render "$RENDER_TARGET" || failed=1
  restore_path api "$API_TARGET" || failed=1
  restore_path env "$ENV_TARGET" || failed=1
  restore_path caddy "$CADDY_TARGET" || failed=1

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
  install -D -m "$mode" -- "$staged" "$target.new.$DEPLOY_ID"
  mv -f -- "$target.new.$DEPLOY_ID" "$target"
  touch "$(marker "$name.activated")"
}

swap_directory() {
  local name=$1
  local staged=$2
  local target=$3
  if [[ -e "$target" || -L "$target" ]]; then
    mv -- "$target" "$BACKUP_ROOT/$name"
    touch "$(marker "$name.had")"
  fi
  mv -- "$staged" "$target"
  touch "$(marker "$name.activated")"
}

activate_release() {
  validate_release
  [[ ! -e "$BACKUP_ROOT" ]] || die "backup already exists: $BACKUP_ROOT"
  install -d -m 700 "$BACKUP_ROOT"

  local activated=0
  on_activate_error() {
    local rc=$?
    trap - ERR
    if [[ $activated -eq 1 ]]; then
      rollback_impl || true
    fi
    exit "$rc"
  }
  trap on_activate_error ERR
  activated=1

  # Caddy 候选先进入事务并做全局 validate，但在服务和静态资源全部就绪前不 reload。
  install_file caddy "$RELEASE_DIR/caddy/fbifmp-layout.caddy" "$CADDY_TARGET" 0644
  caddy validate --config /etc/caddy/Caddyfile >/dev/null

  install_file api "$RELEASE_DIR/api/fbif-api" "$API_TARGET" 0755
  install_file env "$RELEASE_DIR/api.env" "$ENV_TARGET" 0600
  swap_directory render "$RELEASE_DIR/render" "$RENDER_TARGET"
  swap_directory public "$RELEASE_DIR/public" "$PUBLIC_TARGET"
  ln -snf "$PUBLIC_TARGET" /var/www/public

  systemctl restart fbif-render
  wait_http_ok http://127.0.0.1:19096/healthz
  systemctl restart fbif-api
  wait_http_ok http://127.0.0.1:9001/api/health
  systemctl reload caddy

  trap - ERR
}

case "$ACTION" in
  preflight)
    validate_release
    ;;
  activate)
    activate_release
    ;;
  rollback)
    if [[ -d "$BACKUP_ROOT" ]]; then
      rollback_impl
    fi
    ;;
  finalize)
    rm -rf -- "$BACKUP_ROOT" "$RELEASE_DIR"
    ;;
  *)
    die "usage: $0 {preflight|activate|rollback|finalize} RELEASE_DIR PUBLIC_TARGET DEPLOY_ID"
    ;;
esac
