/**
 * 原型用的账号目录。正式实现读 public/js/rule-packs/registry.js（由 api/account_catalog.go 生成），
 * 这里只取名字、渠道和头像，不另立真相源。
 */
export type Channel = "mp" | "ft"

export type Account = { id: string; name: string; channel: Channel; avatar: string }

// 头像与 registry.js 的 avatarUrl 一致（公众号三个号共用 FBIF 标志，网站用 FoodTalks 标志）
const FBIF = "/prototype-media/fbif-avatar.webp"
const FOODTALKS = "/prototype-media/foodtalks-logo.svg"

export const ACCOUNTS: Account[] = [
  { id: "fbif", name: "FBIF食品饮料创新", channel: "mp", avatar: FBIF },
  { id: "wowfood", name: "WowFood爆品超市", channel: "mp", avatar: FBIF },
  { id: "foodtalks-series", name: "FoodTalks什么是系列", channel: "mp", avatar: FBIF },
  { id: "ft-default", name: "网站默认", channel: "ft", avatar: FOODTALKS },
  { id: "ft-series", name: "网站·什么是系列", channel: "ft", avatar: FOODTALKS },
]

export const CHANNEL_LABEL: Record<Channel, string> = { mp: "公众号", ft: "网站" }
