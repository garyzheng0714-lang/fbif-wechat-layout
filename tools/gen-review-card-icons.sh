#!/usr/bin/env bash
# 生成飞书审核卡按钮图标：Lucide（https://lucide.dev，ISC 许可）SVG → 按按钮配色上色 → 48×48 PNG，
# 输出到 api/cardicons/，由 api/feishu_review_card_icons.go 以 go:embed 打包。
#
# 飞书卡片按钮不能直接用 SVG，也不会给自定义图标着色：每个图标按它所在按钮的文字颜色
# 预先出一张 PNG，运行时由各审核应用自行上传拿 img_key（img_key 与上传应用绑定）。
#
# 「公众号原文」按钮用微信公众平台自己的网站图标（2026-10-10 用户要求），品牌色、不随按钮
# 配色变化：wechat-favicon.png，脚本末尾一并重新抓取。
#
# 用法：tools/gen-review-card-icons.sh   依赖 curl、rsvg-convert（brew install librsvg）、sips（macOS）
set -euo pipefail

LUCIDE_VERSION="1.53.0"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/api/cardicons"
SRC="$(mktemp -d)"
trap 'rm -rf "$SRC"' EXIT

# 配色：default 普通按钮（中灰，深色模式下仍可见）、danger 红色描边按钮、
# inverse 蓝底主按钮上的白色、disabled 不可点按钮、wathet 浅蓝卡片头部（与飞书 wathet 头部标题同色）。
color_of() {
  case "$1" in
    default) echo "#646A73" ;;
    danger) echo "#F54A45" ;;
    inverse) echo "#FFFFFF" ;;
    disabled) echo "#BBBFC4" ;;
    wathet) echo "#0F7FA8" ;;
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
  share-2:default
  circle-alert:disabled
  network:wathet
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

# 多平台分发行前的平台图标（2026-10-12）：各平台自己的网站图标，品牌色、不随按钮配色变化。
# 头条号 favicon 是 256px，缩到 48；百家号 favicon 最大只有 32px，原样用（卡里显示 16px）。
curl -fsS -o "$SRC/toutiao.ico" "https://www.toutiao.com/favicon.ico"
sips -s format png -z 48 48 "$SRC/toutiao.ico" --out "$OUT/platform-toutiao.png" >/dev/null
echo "$OUT/platform-toutiao.png"
curl -fsSL -o "$SRC/baijiahao.ico" "https://baijiahao.baidu.com/favicon.ico"
sips -s format png "$SRC/baijiahao.ico" --out "$OUT/platform-baijiahao.png" >/dev/null
echo "$OUT/platform-baijiahao.png"
# 知乎 favicon 也只有 32px，原样用（2026-11-09 接知乎号）。
curl -fsSL -o "$SRC/zhihu.ico" "https://static.zhihu.com/heifetz/favicon.ico"
sips -s format png "$SRC/zhihu.ico" --out "$OUT/platform-zhihu.png" >/dev/null
echo "$OUT/platform-zhihu.png"
# 腾讯号（腾讯内容开放平台 om.qq.com）首页声明的图标是 om.gtimg.cn 上这个，也只有 32px，原样用（2026-11-10 接腾讯号）。
curl -fsSL -o "$SRC/tencent.ico" "https://om.gtimg.cn/om/om_2.0/images/favicon_om.ico"
sips -s format png "$SRC/tencent.ico" --out "$OUT/platform-tencent.png" >/dev/null
echo "$OUT/platform-tencent.png"

# 网站图标：mp.weixin.qq.com 首页声明的 shortcut icon。
curl -fsS -o "$SRC/wechat.ico" "https://res.wx.qq.com/a/wx_fed/assets/res/NTI4MWU5.ico"
sips -s format png -z 48 48 "$SRC/wechat.ico" --out "$OUT/wechat-favicon.png" >/dev/null
echo "$OUT/wechat-favicon.png"

