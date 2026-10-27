package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// account_catalog.go — 账号元数据【单一真相源】（阶段0·务实版）。
//
// 此前账号身份分裂在前端 registry.js（UI 元数据）与后端 rule_defaults.go/
// rule_presets.go（ID 常量 + 种子 + order 切片 + delete 回退 if-chain）两处，
// 靠 id 字符串隐式绑定、无运行时校验，漏写静默降级成 FBIF（病灶①·D1~D5）。
//
// 现在以 accountCatalog() 为后端唯一真相：
//   - defaultRuleAccounts 的 Name / ActiveRuleID 由它派生；
//   - accountBindingsFromStore 的展示顺序读它（消灭写死 order 切片，D4）；
//   - delete 回退用它的 DefaultPresetID 查表（消灭 if-chain，D5）；
//   - `go run . -emit-registry` 从它生成 public/js/rule-packs/registry.js（消灭双写）；
//   - 启动期 validateRegistryConsistency 读 registry.js 与它比对，漂移即 log.Fatal（D1/D3 不再静默）。
//
// 新增账号 = 在 accountCatalog() 加一条 + 跑 `npm run gen:registry`，启动校验兜底。

type accountMeta struct {
	ID                  string
	Channel             string // mp | ft
	TemplateID          string // 复用 'fbif' 原地切换；独立渲染器走自己的 templateId
	AccountName         string // 左栏导入卡账号菜单显示名
	PreviewAccountName  string // 预览作者行显示名
	BehaviorAccountName string // 注入页脚 {{ account.name }}
	AvatarURL           string
	FooterURL           string // '' = 不追加公众号页脚
	FooterPicker        string // '' | 'ft-series'
	Location            string
	DefaultPresetID     string // 该账号默认/回退 preset（= 旧 if-chain 的 fallbackID）
	Note                string // 生成 registry.js 时写在条目前的注释（保留红线/TODO 说明）
}

// accountCatalog 是账号元数据的单一真相源。顺序即 UI 展示顺序、即各渠道默认包判定顺序。
func accountCatalog() []accountMeta {
	return []accountMeta{
		{
			ID:                  defaultRuleAccountID, // "fbif"
			Channel:             "mp",
			TemplateID:          "fbif",
			AccountName:         "FBIF食品饮料创新",
			PreviewAccountName:  "FBIF食品饮料创新",
			BehaviorAccountName: "FBIF",
			AvatarURL:           "/fbif-avatar.webp",
			FooterURL:           "/footer.html",
			Location:            "上海",
			DefaultPresetID:     defaultRulePresetID, // "default"
		},
		{
			ID:                  foodTalksRuleAccountID, // "foodtalks-series"
			Channel:             "mp",
			TemplateID:          "foodtalks-series",
			AccountName:         "FoodTalks什么是系列",
			PreviewAccountName:  "FoodTalks什么是系列",
			BehaviorAccountName: "FoodTalks什么是系列",
			AvatarURL:           "/fbif-avatar.webp",
			FooterURL:           "",          // 空串 = 不追加 FBIF 公众号页脚；「什么是」系列尾部（名片/Eileen/群二维码）由模板自身在正文末渲染
			FooterPicker:        "ft-series", // 声明侧栏用系列尾部选择器；app.html 据此显隐，取代硬编码 id 比较
			Location:            "上海",
			DefaultPresetID:     foodTalksDefaultRulePresetID, // "foodtalks-series-default"
			Note:                "独立渲染器；尾部由模板自身渲染，footerUrl 空串；footerPicker=ft-series 驱动侧栏系列尾部选择器。",
		},
		{
			ID:                  wowFoodRuleAccountID, // "wowfood"
			Channel:             "mp",
			TemplateID:          "fbif", // 复用公众号渲染器（DOCX/表格/表格内图片底层能力共用）
			AccountName:         "WowFood爆品超市",
			PreviewAccountName:  "WowFood爆品超市",
			BehaviorAccountName: "WowFood爆品超市",
			AvatarURL:           "/fbif-avatar.webp", // TODO: 待 WowFood 头像资源就位后替换
			FooterURL:           "",                  // 空串 = 不追加 FBIF 公众号页脚；WowFood 自己的尾部规则走它独立 preset
			Location:            "上海",
			DefaultPresetID:     wowFoodDefaultRulePresetID, // "wowfood-default"
			Note:                "复用公众号渲染器（templateId=fbif）；footerUrl 空串不追加 FBIF 页脚；avatar 待替换。",
		},
		{
			ID:                  foodTalksSiteRuleAccountID, // "ft-default"
			Channel:             "ft",
			TemplateID:          "foodtalks-site", // 网站渲染器，与公众号物理隔离（红线 #19）
			AccountName:         "网站默认",
			PreviewAccountName:  "FBIF食品饮料创新", // 基石 62285 作者行是 FBIF（公众号搬运到网站），非 FoodTalks 平台
			BehaviorAccountName: "FoodTalks",
			AvatarURL:           "/foodtalks-logo.svg", // 网站渠道按品牌用 FoodTalks 标（与公众号 FBIF 头像区分，用户 2026-06 裁定）
			FooterURL:           "",
			Location:            "",
			DefaultPresetID:     foodTalksSiteDefaultRulePresetID, // "ft-default-v1"
			Note:                "网站渲染器，与公众号物理隔离（红线 #19）；previewAccountName 是基石 62285 的 FBIF 署名而非 FoodTalks。",
		},
		{
			ID:                  foodTalksSeriesSiteRuleAccountID, // "ft-series"
			Channel:             "ft",
			TemplateID:          "foodtalks-site", // 复用网站渲染器；系列差异(颜色/字号/商务合作版块/视频)全走 config（红线#19：网站内部共用，不碰公众号）
			AccountName:         "网站·什么是系列",
			PreviewAccountName:  "FoodTalks什么是系列",
			BehaviorAccountName: "FoodTalks",
			AvatarURL:           "/foodtalks-logo.svg",
			FooterURL:           "",
			Location:            "",
			DefaultPresetID:     foodTalksSeriesSiteDefaultRulePresetID, // "ft-series-v1"
			Note:                "网站渠道「什么是系列」：复用 foodtalks-site 渲染器，颜色#333333/#7f7f7f/#01396a + 商务合作版块差异走 config。放 ft-default 之后保证 ft 渠道默认仍是 ft-default（红线#21）。",
		},
		{
			ID:                  foodTalksMpRuleAccountID, // "foodtalks-mp"
			Channel:             "mp",
			TemplateID:          "foodtalks-series", // 与什么是系列共用 FoodTalks 公众号渲染器，差异（排网站稿、序号图、页脚）全走账号规则
			AccountName:         "FoodTalks食品资讯",
			PreviewAccountName:  "FoodTalks食品资讯",
			BehaviorAccountName: "FoodTalks食品资讯",
			AvatarURL:           "/foodtalks-logo.svg",
			FooterURL:           "",
			Location:            "上海",
			DefaultPresetID:     foodTalksMpDefaultRulePresetID, // "foodtalks-mp-default"
			Note:                "FoodTalks食品资讯公众号：排 FoodTalks 网站稿（《FoodTalks公众号排版规范（新）》，2026-10-27）；复用 foodtalks-series 渲染器，页脚走 bottom_blocks。",
		},
		{
			ID:                  wechatPlainRuleAccountID, // "wechat-plain"
			Channel:             "mp",
			TemplateID:          "wechat-plain", // 独立渲染器：只出微信编辑器原生的 <p>，不带任何视觉样式
			AccountName:         "格式清理",
			PreviewAccountName:  "格式清理",
			BehaviorAccountName: "格式清理",
			AvatarURL:           "/fbif-avatar.webp",
			FooterURL:           "",
			Location:            "上海",
			DefaultPresetID:     wechatPlainDefaultRulePresetID, // "wechat-plain-default"
			Note:                "格式清理（2026-10-27）：粘贴的文字排成微信编辑器原生段落，空行写成 <p><br></p>，无样式、无页脚，适合任何文章或贴图文字框。",
		},
	}
}

func accountMetaByID(id string) (accountMeta, bool) {
	for _, m := range accountCatalog() {
		if m.ID == id {
			return m, true
		}
	}
	return accountMeta{}, false
}

// isProtectedDefaultPreset 报告 id 是否是某账号在 catalog 里登记的默认/回退 preset。
// 这些 preset 不可删除——删了会让该账号的回退目标悬空（ActiveRuleID 指向已不存在的规则）。
func isProtectedDefaultPreset(id string) bool {
	for _, m := range accountCatalog() {
		if m.DefaultPresetID == id {
			return true
		}
	}
	return false
}

// catalogOrderIDs 返回账号 ID 的展示顺序（即 accountCatalog 的声明顺序）。
func catalogOrderIDs() []string {
	cat := accountCatalog()
	ids := make([]string, 0, len(cat))
	for _, m := range cat {
		ids = append(ids, m.ID)
	}
	return ids
}

// channelDefaultPackIDs 派生每个渠道的默认包（该渠道在 catalog 中最先出现的账号），
// 用于生成 registry.js 的 CHANNEL_DEFAULTS（mp→fbif, ft→ft-default）。
func channelDefaultPackIDs() map[string]string {
	out := map[string]string{}
	for _, m := range accountCatalog() {
		if _, ok := out[m.Channel]; !ok {
			out[m.Channel] = m.ID
		}
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────
// registry.js 代码生成（`go run . -emit-registry`）

// jsStr 把字符串安全编码成单引号 JS 字面量：先转义反斜杠，再转义单引号与换行/回车，
// 避免任何字段值含这些字符时生成破损 JS。
func jsStr(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return "'" + s + "'"
}

var jsIdentRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// jsKey 返回对象键：合法标识符直接用，含连字符等则加引号。
func jsKey(id string) string {
	if jsIdentRe.MatchString(id) {
		return id
	}
	return "'" + id + "'"
}

// renderRegistryJS 从 accountCatalog 生成 public/js/rule-packs/registry.js 的完整内容。
// 数据（RULE_PACKS / DEFAULT_RULE_PACK_ID / CHANNEL_DEFAULTS）来自 catalog；
// 纯逻辑函数（list/get/byChannel/byPath）为稳定模板，逐字输出。
func renderRegistryJS() string {
	cat := accountCatalog()
	var b strings.Builder
	b.WriteString("// rule-packs/registry.js —公众号排版包注册表。\n")
	b.WriteString("// 【生成文件】由 `go run . -emit-registry`（npm run gen:registry）从 api/account_catalog.go 生成，请勿手改。\n")
	b.WriteString("// 账号元数据唯一真相在 accountCatalog()；改账号后跑 npm run gen:registry，启动期一致性校验兜底。\n\n")
	b.WriteString("import { parsePath } from './url.js';\n\n")
	b.WriteString(fmt.Sprintf("export const DEFAULT_RULE_PACK_ID = %s;\n\n", jsStr(cat[0].ID)))
	b.WriteString("export const RULE_PACKS = Object.freeze({\n")
	for _, m := range cat {
		if m.Note != "" {
			b.WriteString("  // " + m.Note + "\n")
		}
		b.WriteString("  " + jsKey(m.ID) + ": Object.freeze({\n")
		b.WriteString("    id: " + jsStr(m.ID) + ",\n")
		b.WriteString("    templateId: " + jsStr(m.TemplateID) + ",\n")
		b.WriteString("    defaultPresetId: " + jsStr(m.DefaultPresetID) + ",\n")
		b.WriteString("    accountName: " + jsStr(m.AccountName) + ",\n")
		b.WriteString("    previewAccountName: " + jsStr(m.PreviewAccountName) + ",\n")
		b.WriteString("    behaviorAccountName: " + jsStr(m.BehaviorAccountName) + ",\n")
		b.WriteString("    avatarUrl: " + jsStr(m.AvatarURL) + ",\n")
		b.WriteString("    footerUrl: " + jsStr(m.FooterURL) + ",\n")
		if m.FooterPicker != "" {
			b.WriteString("    footerPicker: " + jsStr(m.FooterPicker) + ",\n")
		}
		b.WriteString("    location: " + jsStr(m.Location) + ",\n")
		b.WriteString("    channel: " + jsStr(m.Channel) + ",\n")
		b.WriteString("  }),\n")
	}
	b.WriteString("});\n\n")

	b.WriteString(`export function listRulePacks() {
  return Object.values(RULE_PACKS);
}

export function getRulePacksByChannel(channel) {
  return Object.values(RULE_PACKS).filter(pack => pack.channel === channel);
}

export function getRulePack(id = DEFAULT_RULE_PACK_ID) {
  return RULE_PACKS[id] || RULE_PACKS[DEFAULT_RULE_PACK_ID];
}

export function getRulePackIDFromLocation(locationLike = globalThis.location) {
  try {
    const params = new URLSearchParams(locationLike && locationLike.search || '');
    const id = params.get('account') || params.get('rule_pack') || DEFAULT_RULE_PACK_ID;
    return getRulePack(id).id;
  } catch {
    return DEFAULT_RULE_PACK_ID;
  }
}

`)
	// CHANNEL_DEFAULTS 来自 catalog 派生（每渠道首个账号）。
	defaults := channelDefaultPackIDs()
	chOrder := []string{}
	seen := map[string]bool{}
	for _, m := range cat {
		if !seen[m.Channel] {
			chOrder = append(chOrder, m.Channel)
			seen[m.Channel] = true
		}
	}
	b.WriteString("const CHANNEL_DEFAULTS = Object.freeze({\n")
	for _, ch := range chOrder {
		b.WriteString("  " + jsKey(ch) + ": " + jsStr(defaults[ch]) + ",\n")
	}
	b.WriteString("});\n\n")

	b.WriteString(`export function getRulePackByPath(pathname) {
  const parsed = parsePath(pathname);
  if (!parsed) return getRulePack(DEFAULT_RULE_PACK_ID);
  const { channel, packId } = parsed;
  if (packId && RULE_PACKS[packId] && RULE_PACKS[packId].channel === channel) {
    return RULE_PACKS[packId];
  }
  const fallbackId = CHANNEL_DEFAULTS[channel];
  if (fallbackId && RULE_PACKS[fallbackId]) {
    return RULE_PACKS[fallbackId];
  }
  return getRulePack(DEFAULT_RULE_PACK_ID);
}
`)
	return b.String()
}

// ─────────────────────────────────────────────────────────────────────────
// 启动期一致性校验：committed 的 registry.js 必须与 accountCatalog 生成结果【逐字节一致】。
//
// registry.js 是生成文件（renderRegistryJS）。逐字节比对一举覆盖【所有】字段——不止
// id/templateId/channel，连 accountName/previewAccountName/behaviorAccountName/avatarUrl/
// location/CHANNEL_DEFAULTS 漂移都会被捕获；也防住有人手改了生成文件、或改 catalog 没跑
// gen:registry。比正则逐字段解析既更完整也无脆弱性（不怕字段值里含引号/`})` 等）。
//
// 文件缺失返回带 warn 前缀的错误：本地开发只告警，ENV=production 由统一启动策略提升为 fatal；
// 存在但不一致在所有环境都 fatal。
func validateRegistryConsistency(publicDir string) error {
	path := filepath.Join(publicDir, "js", "rule-packs", "registry.js")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("warn: 无法读取 %s 做账号一致性校验（跳过）: %w", path, err)
	}
	if string(raw) != renderRegistryJS() {
		return fmt.Errorf("registry.js 与 accountCatalog 不一致（改了 api/account_catalog.go 后请跑 `npm run gen:registry`；registry.js 是生成文件，勿手改）")
	}
	return nil
}

// 启动期账号↔preset 挂链校验：每个账号的 DefaultPresetID 必须真实存在于
// defaultRuleDefinitions。两者只靠字符串常量对齐、无编译期链接，写错则该账号的
// 默认规则悬空（2026-07-10 架构体检认定的「只靠人肉」薄弱边界，补为机器强制）。
func validateAccountPresetLinkage() error {
	defs := defaultRuleDefinitions("")
	for _, m := range accountCatalog() {
		if _, ok := defs[m.DefaultPresetID]; !ok {
			return fmt.Errorf("账号 %q 的 DefaultPresetID=%q 在 defaultRuleDefinitions 中不存在（新增账号需在 rule_defaults.go 成对登记 preset）", m.ID, m.DefaultPresetID)
		}
	}
	return nil
}
