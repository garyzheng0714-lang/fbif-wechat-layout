#!/usr/bin/env bash
# 生成飞书审核卡按钮图标：Lucide（https://lucide.dev，ISC 许可）SVG → 按按钮配色上色 → 48×48 PNG，
# 输出到 api/cardicons/，由 api/feishu_review_card_icons.go 以 go:embed 打包。
#
# 飞书卡片按钮不能直接用 SVG，也不会给自定义图标着色：每个图标按它所在按钮的文字颜色
# 预先出一张 PNG，运行时由各审核应用自行上传拿 img_key（img_key 与上传应用绑定）。
#
# 「公众号」「网站 / 预览」按钮用两个网站自己的网站图标（2026-10-10 用户要求），品牌色、
# 不随按钮配色变化：wechat-favicon.png、foodtalks-favicon.png，脚本末尾一并重新抓取。
#
# 用法：tools/gen-review-card-icons.sh   依赖 curl、rsvg-convert（brew install librsvg）、sips（macOS）
set -euo pipefail

LUCIDE_VERSION="1.53.0"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/api/cardicons"
SRC="$(mktemp -d)"
trap 'rm -rf "$SRC"' EXIT

# 配色：default 普通按钮（中灰，深色模式下仍可见）、danger 红色描边按钮、
# inverse 蓝底主按钮上的白色、disabled 不可点按钮。
color_of() {
  case "$1" in
    default) echo "#646A73" ;;
    danger) echo "#F54A45" ;;
    inverse) echo "#FFFFFF" ;;
    disabled) echo "#BBBFC4" ;;
    *) echo "unknown variant $1" >&2; exit 1 ;;
  esac
}

# 输出文件名 = <lucide 图标名>-<配色>.png，与 feishu_review_card_icons.go 中的登记一致。
ICONS=(
  external-link:default
  refresh-cw:default
  eye-off:danger
  send:danger
  send:inverse
  share-2:inverse
  lock:disabled
  circle-alert:disabled
)

mkdir -p "$OUT"
for entry in "${ICONS[@]}"; do
  name="${entry%%:*}"
  variant="${entry##*:}"
  svg="$SRC/$name.svg"
  if [[ ! -f "$svg" ]]; then
    curl -fsS -o "$svg" "https://unpkg.com/lucide-static@${LUCIDE_VERSION}/icons/${name}.svg"
  fi
  sed "s/currentColor/$(color_of "$variant")/g" "$svg" > "$SRC/$name-$variant.svg"
  rsvg-convert -w 48 -h 48 -o "$OUT/$name-$variant.png" "$SRC/$name-$variant.svg"
  echo "$OUT/$name-$variant.png"
done

# 网站图标：mp.weixin.qq.com 首页声明的 shortcut icon，www.foodtalks.cn 的 /favicon.ico。
curl -fsS -o "$SRC/wechat.ico" "https://res.wx.qq.com/a/wx_fed/assets/res/NTI4MWU5.ico"
curl -fsS -o "$SRC/foodtalks.ico" "https://www.foodtalks.cn/favicon.ico"
sips -s format png -z 48 48 "$SRC/wechat.ico" --out "$OUT/wechat-favicon.png" >/dev/null
sips -s format png -z 48 48 "$SRC/foodtalks.ico" --out "$OUT/foodtalks-favicon.png" >/dev/null
echo "$OUT/wechat-favicon.png"
echo "$OUT/foodtalks-favicon.png"
