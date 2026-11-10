#!/bin/sh
# 统一登录合规测试：编一个生产构建（不带 devlogin 标签），在临时目录里起，跑登录模板的 conformance/run.mjs，结果写 docs/sso-conformance.json。
# 改了 api/internal/auth、api/auth_login.go、api/auth_service.go 或 main.go 的登录接线后跑，全过才能上线。用法：tools/sso-conformance.sh（要有 go、node）。
# 集成平台、中台是假的，登录中心是真的；业务接口不测，受保护接口测 /api/auth/me。飞书应用不配：只测登录，不测授权飞书。
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)

if [ "${1:-}" = start ]; then
  # run.mjs 每起一次被测服务调一次：端口（PORT）、数据目录和 SSO_* 由它给。用户库、会话都放数据目录里。
  export ENV= AUTH_DEV_LOGIN= AUTH_USERS_PATH= FEISHU_LOGIN_APP_ID= FEISHU_LOGIN_APP_SECRET= FEISHU_LOGIN_REDIRECT_URI= \
    AUTH_SESSION_SECRET=conformance-preview-signing-key-not-a-secret
  exec "$LAYOUT_CONFORMANCE_BIN" -host 127.0.0.1 -port "$PORT" \
    -rules "$CONFORMANCE_DATA_DIR/rules.json" -public "$ROOT/public"
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$ROOT/api"
go build -o "$WORK/server" .
export LAYOUT_CONFORMANCE_BIN="$WORK/server"
cd "$ROOT"
node "$HOME/.claude/skills/feishu-login-guide/templates/backend/anycross-sso/conformance/run.mjs" \
  --start "$ROOT/tools/sso-conformance.sh start" --protected /api/auth/me --page /mp/fbif --report docs/sso-conformance.json
