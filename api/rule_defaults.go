package main

// rule_defaults.go — 规则默认值生成（从 rule_presets.go 拆出，保持单文件 ≤900 行）。
// 仅含 default* 默认值构造函数；存储读写/校验/HTTP 逻辑仍在 rule_presets.go。

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func defaultRuleConfig() map[string]any {
	return map[string]any{
		"font_size":                   "15",
		"heading_size":                "18",
		"text_color":                  "#544545",
		"link_color":                  "#0070C0",
		"muted_color":                 "#888888",
		"line_height":                 "1.75",
		"letter_spacing":              "0.034",
		"font_family":                 "PingFang SC",
		"footer_enabled":              true,
		"skip_upload":                 true,
		"paragraph_margin_x":          "8",
		"paragraph_gap":               "20",
		"caption_font_size":           "12",
		"attribution_font_size":       "15",
		"blockquote_padding_left":     "12",
		"blockquote_border_width":     "3",
		"md_heading_max_chars":        "60",
		"decorative_image_max_px":     "640",
		"more_articles_enabled":       true,
		"more_articles_slots":         "3",
		"more_articles_default_cards": defaultMoreArticleCards(),
		// 统一表格样式（公众号与网站共享这一套默认；各 preset 可在规则后台独立覆盖）。
		// 默认复刻 FBIF/FoodTalks 既有公众号表格：FoodTalks 蓝表头 + 白字 + 浅灰网格线。
		"table_header_bg":          "#01396a",
		"table_header_text_color":  "#ffffff",
		"table_header_bold":        true,
		"table_border_color":       "#e5e7eb",
		"table_font_size":          "15",
		"table_cell_padding":       "8",
		"banner_overlay_alpha":     "0.47058823529411764",
		"banner_title_x":           "61",
		"banner_title_y":           "92",
		"banner_title_width":       "878",
		"banner_title_box_height":  "116",
		"banner_title_font_size":   "48",
		"banner_title_line_height": "70",
		"banner_title_max_lines":   "2",
		"modules": map[string]any{
			"asset_library": defaultAssetLibraryModule(),
			// 公众号(FBIF)底部区块：4 原子 V2 双预设（原创/转载），enabled=true，经 template.renderBottomFooter
			// 接管底部渲染。本地浏览器验证 V2 视觉 1:1 == footer.html（v6→v7 切生产）。复用此基线的账号若不要
			// FBIF 底部需显式覆盖。
			"bottom_blocks": defaultMpBottomBlocksModuleV2(),
		},
	}
}

func defaultMoreArticleCards() []any {
	return []any{
		map[string]any{
			"href":   "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=2652909589&idx=1&sn=d0bdeacee2b1ba7f03e48b38f91bbefa&scene=21",
			"imgurl": "https://mmbiz.qpic.cn/sz_mmbiz_jpg/6JHhlvvjTRHibhuMnzJTuvhqr6Neq7ib00icPLXRyof5H9ARUnw9eWIshgyHibyfVXtgSRkWmOP2MWZjHLLbFec9VAFSYH6ZsarKf2LUC4ONEkE/0?wx_fmt=jpeg",
			"title":  "没想到！英国人把豆腐做到英国第一，还像卖衣服一样出XXL超大号！",
		},
		map[string]any{
			"href":   "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=2652916388&idx=1&sn=b5605734c9c27ce2f8d54a6534b6543d&scene=21",
			"imgurl": "https://mmbiz.qpic.cn/sz_mmbiz_jpg/6JHhlvvjTREtc1dUPY9VokWQTtQsdevBIcfoMMavrQNSVFoce8gDOtTqlWhciadMP6VJwzErl7HnzECBECMMILEXt653GCeub3nDZ8VrvTsM/0?wx_fmt=jpeg",
			"title":  "做了20年糖，她把豆腐棒放在鸡胸肉旁边，5年卖出1亿根",
		},
		map[string]any{
			"href":   "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=2652906407&idx=1&sn=7ede62b74d3a5750c2c95ca40c216290&scene=21",
			"imgurl": "https://mmbiz.qpic.cn/sz_mmbiz_jpg/6JHhlvvjTRGEHDwCvfSmEXO9VBXpibZHE3ZqLYicsIyhfiajfq9MNduKq03KbDeqU0iaj3WEQeBEY2j0mSCetzD1Jdic1iciaL7M1KZjHu8x5TLdmM/0?wx_fmt=jpeg",
			"title":  "日本最牛包子铺，单店年销2329万，怎么做到的？",
		},
	}
}

func defaultRuleDocument() rulePresetDocument {
	now := time.Now().UTC().Format(time.RFC3339)
	return rulePresetDocument{
		ActiveID: defaultRulePresetID,
		Presets: []rulePreset{{
			ID:          defaultRulePresetID,
			Name:        "FBIF默认规则",
			Description: "当前 FBIF 公众号排版默认参数",
			Config:      defaultRuleConfig(),
			Behavior:    defaultRuleBehavior(),
			UpdatedAt:   now,
		}},
		UpdatedAt: now,
	}
}

// defaultRuleAccounts 从 accountCatalog（单一真相源）派生账号绑定种子。
// 账号 Name / 默认 ActiveRuleID 不再写死在此，新增账号只改 accountCatalog()。
func defaultRuleAccounts(now string) map[string]ruleAccount {
	out := make(map[string]ruleAccount, len(accountCatalog()))
	for _, m := range accountCatalog() {
		out[m.ID] = ruleAccount{
			Name:              m.AccountName,
			ActiveRuleID:      m.DefaultPresetID,
			ActiveRuleVersion: 1,
			UpdatedAt:         now,
		}
	}
	return out
}

// currentBuiltinRuleVersion：内置默认 preset 的代码版本。修改任一 default*RuleConfig/Behavior
// 且想把新默认推到已部署环境（store 已持久化旧 preset）时 +1——store 加载会用代码默认覆盖
// BuiltinVersion 落后的内置 preset，绕过「已持久化则代码默认永不覆盖」的旧行为。
// v1→v2（2026-06-02）：FoodTalks「什么是」系列改为独立默认（深蓝 #02396b、表格边框 #888888、
// 18 群 ft_series_materials、BD 名片 bd_card），修复 5/26 持久化的 FBIF 派生旧版。
// v2→v3（2026-06-02）：新增 asset_library 素材库种子，并把 FoodTalks 网站页脚图片 URL
// 从 html_lines 内联迁为 {{asset#id}} 引用。
// v3→v4（2026-06-03）：「什么是」系列文末区块对齐成品——文末署名/图源说明 12px #7f7f7f 斜体
// （footer_note_color/footer_note_font_size + footer_note_regex），「提示：/* 声明」块（tip_*_regex），
// 联系小节公司信息居中（contact_section_regex/contact_field_regex）。
// v4→v5（2026-06-05）：bottom_blocks 区块实例新增 display_name，修正系列尾部区块在后台/前台
// 退到“什么是系列Eileen名片”这类素材文件名的问题。
// v5→v6（2026-06-05）：bottom_blocks 模块新增 gap=blank_line，区块类型增加 kind/text 文字模块。
// v6→v7（2026-06-06）：FBIF 公众号底部默认切 4 原子 V2 双预设（原创/转载），替换旧语义类型
// declaration/name_card/titled_media；本地浏览器验证 FBIF V2 视觉 1:1 == footer.html，微信复制最终由公众号后台确认。
// 系列【保持 v1，不切 V2】：spacer 间距需 FoodTalks 后台确认且系列无 spacer renderer，切 V2 零收益（理由见 series bottom_blocks 覆盖处注释）。
// v7→v8（2026-06-08）：新增网站渠道「什么是系列」包 ft-series（ft-series-v1）——复用 foodtalks-site
// 渲染器，覆盖系列原生口径(正文#333333/灰#7f7f7f/框线#7f7f7f/表头#01396a/参考16px)，依据 8 篇真实成品。
// v8→v9（2026-06-08）：ft-series 补 justify_paragraphs=false + 视频块 + 商务合作版块（fbif_footer 覆盖
// + 清空继承的 bottom_blocks）；覆盖已持久化的 v8 旧版 ft-series-v1（无商务合作）。
// v9→v10（2026-06-09）：FoodTalks「什么是」系列公众号底部 BD 名片 / 群二维码开放前台单篇换图入口。
// v10→v11（2026-06-09）：ft-series 网站「什么是系列」URL 导入结构归一化 + 参考来源/文末署名
// + 商务合作表格/二维码尺寸修复，覆盖已持久化的 v10 旧版 ft-series-v1。
// v11→v12（2026-06-09）：ft-series 网站页脚按成品顺序拆为「商务合作文案 → BD名片 → 相关合作表」，
// 并触发前端清理旧 ft.footer.* 本地覆盖，避免 tip=true / 缺 bd_card 的旧状态继续污染新文章。
// v12→v13（2026-06-09）：FoodTalks「什么是」系列公众号 URL 导入把「图片/数据来源 + 本文作者」
// 合并成同一文本块时，仍识别为文末尾注并自动匹配作者名片（Ian→Lan）。
// v13→v14（2026-06-11）：网站渠道按飞书反馈批次修订——图宽规则改纵图(含1:1)长边375/二维码150
// （image.portrait_px=375 + qr_px/qr_max_natural_px），footer_note_regex 增收「本文未标注来源的
// 图片均来自」「*注：」变体，新增 biz_coop_line_regex（商务合作导联行 14px 斜体灰）。
const currentBuiltinRuleVersion = 14

func defaultRuleDefinitions(now string) map[string]ruleDefinition {
	defs := map[string]ruleDefinition{
		defaultRulePresetID: {
			ID:          defaultRulePresetID,
			Name:        "FBIF默认规则",
			Description: "当前 FBIF 公众号排版默认参数",
			Versions: []rulePresetVersion{{
				Version:   1,
				Config:    defaultRuleConfig(),
				Behavior:  defaultRuleBehavior(),
				CreatedAt: now,
			}},
			UpdatedAt: now,
		},
		foodTalksDefaultRulePresetID: {
			ID:          foodTalksDefaultRulePresetID,
			Name:        "FoodTalks什么是系列默认规则",
			Description: "FoodTalks什么是系列的独立默认参数（序号图标题/深蓝主色/目录/尾部名片）",
			Versions: []rulePresetVersion{{
				Version:   1,
				Config:    defaultFoodTalksSeriesRuleConfig(),
				Behavior:  defaultFoodTalksSeriesRuleBehavior(),
				CreatedAt: now,
			}},
			UpdatedAt: now,
		},
		foodTalksSiteDefaultRulePresetID: {
			ID:          foodTalksSiteDefaultRulePresetID,
			Name:        "FoodTalks 网站默认规则",
			Description: "FoodTalks 网站排版的独立默认参数（基于 8 篇真实文章采样）",
			Versions: []rulePresetVersion{{
				Version:   1,
				Config:    defaultFoodTalksSiteRuleConfig(),
				Behavior:  defaultFoodTalksSiteRuleBehavior(),
				CreatedAt: now,
			}},
			UpdatedAt: now,
		},
		foodTalksSeriesSiteDefaultRulePresetID: {
			ID:          foodTalksSeriesSiteDefaultRulePresetID,
			Name:        "网站·什么是系列默认规则",
			Description: "FoodTalks 网站「什么是系列」排版默认（正文#333333/灰#7f7f7f/表头#01396a/框线#7f7f7f/参考16px + 商务合作版块）",
			Versions: []rulePresetVersion{{
				Version:   1,
				Config:    defaultFoodTalksSeriesSiteRuleConfig(),
				Behavior:  defaultFoodTalksSeriesSiteRuleBehavior(),
				CreatedAt: now,
			}},
			UpdatedAt: now,
		},
		wowFoodDefaultRulePresetID: {
			ID:          wowFoodDefaultRulePresetID,
			Name:        "WowFood爆品超市默认规则",
			Description: "WowFood爆品超市公众号的独立默认参数（复用公众号渲染器，独立尾部规则）",
			Versions: []rulePresetVersion{{
				Version:   1,
				Config:    defaultWowFoodRuleConfig(),
				Behavior:  defaultWowFoodRuleBehavior(),
				CreatedAt: now,
			}},
			UpdatedAt: now,
		},
	}
	for id, d := range defs {
		d.BuiltinVersion = currentBuiltinRuleVersion
		defs[id] = d
	}
	return defs
}

// defaultWowFoodRuleConfig 返回 WowFood爆品超市 的默认参数。复用公众号(FBIF)基线，
// 只覆盖尾部/页脚差异：WowFood 是独立品牌，不带 FBIF 的「更多文章」推荐位（默认关）。
// 其余视觉键经 mergeRuleDefaults 继承 FBIF 基线；用户可在规则后台进一步定制 WowFood 尾部。
func defaultWowFoodRuleConfig() map[string]any {
	cfg := defaultRuleConfig()
	// WowFood 底部默认「无任何排版规则」（用户 2026-06-02 裁定）：
	// 关更多文章、清空继承自 FBIF 的默认推荐卡、关页脚开关。
	// 公众号端底部追加本由 registry.js 的 footerUrl='' 阻断，这里把 config 一并设空，
	// 让规则后台与离线降级都呈现「底部无内容」的一致语义；表格等正文规则不受影响。
	cfg["more_articles_enabled"] = false
	cfg["more_articles_default_cards"] = []any{}
	cfg["footer_enabled"] = false
	// 关掉继承自 FBIF 基线的 bottom_blocks：WowFood 复用 templateId=fbif，其 renderBottomFooter
	// 会读 bottom_blocks；不清空则 WowFood 文末会错渲 FBIF 的提示/名片/更多文章/1000案例，且
	// fbif-scope 素材在 wowfood-scope 解析为破图。清空 = 与 footerUrl='' 一致的「底部无内容」。
	if mods, ok := cfg["modules"].(map[string]any); ok {
		mods["bottom_blocks"] = map[string]any{"schema_version": 2, "enabled": false, "presets": []any{}}
	}
	return cfg
}

// defaultWowFoodRuleBehavior 复用 FBIF 公众号行为基线（作者/来源/Mote 署名规则一致）；
// 若 WowFood 需独立尾部文案/正则，可在此覆盖差异项或在规则后台配置。
func defaultWowFoodRuleBehavior() map[string]any {
	return defaultRuleBehavior()
}

// defaultFoodTalksSiteRuleConfig 返回 FoodTalks 网站排版的默认参数配置。
// 规则细节见 docs/superpowers/specs/2026-05-27-foodtalks-site-layout-design.md
func defaultFoodTalksSiteRuleConfig() map[string]any {
	return map[string]any{
		// 网站视觉规则（红线下放：规则后台可配；客户端读不到时降级到代码内同值兜底）。
		// muted_color/caption_font_size 由 defaultRuleConfig 经 mergeRuleDefaults 继承，
		// 这里只补网站独有的：提示标题色/提示正文色、作者/提示字号。
		"tip_title_color":  "#01396a",
		"tip_body_color":   "#7f7f7f",
		"author_font_size": "14",
		"tip_font_size":    "14",
		"image": map[string]any{
			"width":             600,
			"portrait_px":       375, // 纵图（高≥宽，含1:1）长边（高）目标值；宽按比例（2026-06-11 反馈问题十一）
			"landscape_px":      600, // 横图宽度
			"qr_px":             150, // 二维码（近正方形小图）输出宽（2026-06-11 反馈问题十五）
			"qr_max_natural_px": 500, // 近正方形图原始宽 ≤ 此值才按二维码处理
		},
		"modules": map[string]any{
			"mctoc":         map[string]any{"enabled": true},
			"fbif_footer":   defaultFBIFFooterModule(),
			"asset_library": defaultAssetLibraryModule(),
			// 区块模块：由页脚库无损迁移而来（统一底部模型）。P3 渲染器接入前仅为数据，不改输出。
			"bottom_blocks": defaultBottomBlocksModule(),
		},
	}
}

// defaultFBIFFooterModule 返回页脚库的服务端默认种子（规则后台可编辑、可增删预设）。
// 与前端 public/js/footer-segments-defaults.js 的降级常量保持一致；真相以 /api/rules 为准。
func defaultFBIFFooterModule() map[string]any {
	seg := func(lines ...string) map[string]any {
		arr := make([]any, len(lines))
		for i, l := range lines {
			arr[i] = l
		}
		return map[string]any{"html_lines": arr}
	}
	return map[string]any{
		"enabled":          false,
		"active_preset_id": "fbif-original",
		"segment_order":    []any{"biz_coop", "join_group", "follow_account", "logo", "more_info"},
		"segments": map[string]any{
			"biz_coop": seg(
				`<p style="text-align: left;"><em><span style="color: #7f7f7f; font-size: 14px;">*资讯投稿与商务合作，请查看&ldquo;<a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/news/30752" target="_blank" rel="noopener">FoodTalks商务合作方案</a>&rdquo;，并联系Bobo。</span></em></p>`,
			),
			"join_group": seg(
				`<p style="text-align: center;"><img style="display: block; margin-left: auto; margin-right: auto;" src="`+assetRefID("ft_footer_banner")+`" alt="" width="600" /></p>`,
				`<p style="color: #01396a; font-size: 20px;"><strong>加入微信群</strong></p>`,
				`<p><br /></p>`,
				`<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="`+assetRefID("ft_footer_group_qr")+`" alt="00" width="600" height="267" /></p>`,
				`<p style="text-align: left;"><em><span style="color: #7f7f7f; font-size: 14px;">*如遇二维码失效无法加群，请联系工作人员Lemon（微信号：FBIF_Lemon）。</span></em></p>`,
			),
			"follow_account": seg(
				`<p style="color: #01396a; font-size: 20px;"><strong>关注公众号</strong></p>`,
				`<p><br /></p>`,
				`<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="`+assetRefID("ft_footer_account_qr")+`" alt="" width="600" height="221" /></p>`,
			),
			"logo": seg(
				`<p><img style="max-width: 100%; height: 27px; width: 126px;" src="` + assetRefID("ft_footer_logo") + `" alt="FoodTalks logo" width="130" height="27" border="0" /></p>`,
			),
			"more_info": seg(
				`<p>点击获取更多<a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/" target="_blank" rel="noopener">全球食品资讯</a></p>`,
			),
		},
		"presets": []any{
			map[string]any{"id": "fbif-original", "name": "FBIF原创完整页脚", "segments": map[string]any{"tip": true, "biz_coop": true, "join_group": true, "follow_account": true, "logo": true, "more_info": true}},
			map[string]any{"id": "repost-simple", "name": "转载简版（提示+商务）", "segments": map[string]any{"tip": true, "biz_coop": true, "join_group": false, "follow_account": false, "logo": false, "more_info": false}},
			map[string]any{"id": "tip-only", "name": "仅提示声明", "segments": map[string]any{"tip": true, "biz_coop": false, "join_group": false, "follow_account": false, "logo": false, "more_info": false}},
			map[string]any{"id": "none", "name": "无页脚", "segments": map[string]any{"tip": false, "biz_coop": false, "join_group": false, "follow_account": false, "logo": false, "more_info": false}},
		},
	}
}

// defaultFoodTalksSiteRuleBehavior 返回 FoodTalks 网站行为模板的默认配置。
// 识别正则：作者署名、提示行、参考来源段、文末图源/作者说明。
func defaultFoodTalksSiteRuleBehavior() map[string]any {
	return map[string]any{
		"author_regex":     `^\s*作者[：:]\s*`,
		"tip_regex":        `^\s*提示[：:]`,
		"ref_header_regex": `^\s*参考(?:来源|资料)[：:]?\s*$`,
		"ref_item_regex":   `^\s*\[\d+\]`,
		// 网站句式模板（红线下放：规则后台可配；客户端读 templates.<key>，缺省降级同值 const）。
		"templates": map[string]any{
			"site_tip_prefix_fbif":      "*本文为FBIF食品饮料创新（ID：FoodInnovation）原创",
			"site_tip_suffix":           "，不代表FoodTalks立场，转载请联系出处。",
			"author_line_repost_prefix": "本文为{source}原创",
			"author_line_repost_suffix": "，转载请联系出处。",
			"tip_title_default":         "提示：",
			"ref_header_label":          "参考来源：",
		},
		// 网站识别正则（红线下放；客户端读 params.<key>.default，非法/缺省降级同值 const）。
		"params": map[string]any{
			"fbif_source_pattern":        map[string]any{"type": "string", "default": `(?i)FBIF|食品饮料创新|FoodInnovation`},
			"image_caption_prefix_regex": map[string]any{"type": "string", "default": `(?i)^(图|图注|图片|上图|下图|表|资料来源|数据来源|图片来源|来源|注|Source|Photo|Image|Credit)\s*[：:\s\d]`},
			"docx_preamble_author_regex": map[string]any{"type": "string", "default": `(?i)^[\s　]*(作者|Author)\s*[:：]`},
			"caption_lead_regex":         map[string]any{"type": "string", "default": `(?i)^(图|图注|图片|上图|下图|表|资料来源|数据来源|图片来源|来源|制图|截图|拍摄|摄影|注|Source|Photo|Image|Credit)\s*[：:]`},
			"footer_note_regex":          map[string]any{"type": "string", "default": `^(?:本文(?:未标注来源|作者\s*[：:]|图片来源)|图片均来自|[*＊]\s*注[：:])`},
			"repost_source_regex":        map[string]any{"type": "string", "default": `^来源[：:]\s*(.+)$`},
			// 商务合作导联行（62139/62303 成品：14px 斜体、文本 #7f7f7f、链接保留）——2026-06-11 反馈问题九。
			"biz_coop_line_regex":        map[string]any{"type": "string", "default": `^\s*(?:商务合作对接请点击|如有合作意向)`},
		},
		// 网站识别词表/标记列表（红线下放；客户端读 lists.<key>，缺省降级同值 const）。
		"lists": map[string]any{
			"wechat_trailer_markers": []any{
				`/\s*更多文章\s*/`,
				`/\s*1000\s*个食品创新案例\s*/`,
				`预览时标签不可点`,
				`加入微信群`,
				`关注公众号`,
				`点击获取更多`,
				`长按.{0,4}识别.{0,2}二维码`,
				`扫码.{0,4}(?:加群|关注|进群)`,
			},
			"wechat_tip_signatures": []any{
				`欢迎转发朋友圈`,
				`后台回复.{0,4}转载`,
				`投稿与商务合作`,
				`(?i)fbifmedia`,
				`(?i)微信[：:]\s*FBIF_`,
			},
			"image_caption_keywords": []any{
				"图片来源", "资料来源", "数据来源", "来源",
				"制图", "截图", "图注", "注：",
				"Source", "Photo", "Image", "Credit",
			},
			"title_keywords": []any{
				"背景", "简介", "总结", "分析", "案例", "介绍", "概述", "结论", "展望",
			},
		},
	}
}

// defaultFoodTalksSeriesSiteRuleConfig 网站渠道「什么是系列」默认参数。
// 复用网站基线(defaultFoodTalksSiteRuleConfig)，仅覆盖系列原生口径——颜色/字号经 8 篇真实
// static.foodtalks.cn「什么是」成品逐元素核对(2026-06-08，依据《Foodtalks【什么是系列】排版要求》)：
// 正文 #333333、灰字(图注/参考/署名) #7f7f7f、表格框线 #7f7f7f、表头 #01396a、参考来源 16px。
// muted #7f7f7f 是系列原生色，区别于 ft-default 的 #888888(红线#19 针对搬运文)；物理隔离=不动 ft-default。
// 商务合作底部版块走 modules.fbif_footer/bottom_blocks（后续块覆盖），此处仅定样式真值。
func defaultFoodTalksSeriesSiteRuleConfig() map[string]any {
	cfg := defaultFoodTalksSiteRuleConfig()
	cfg["text_color"] = "#333333"
	cfg["muted_color"] = "#7f7f7f"
	cfg["footer_note_color"] = "#7f7f7f"
	cfg["caption_font_size"] = "12"
	cfg["footer_note_font_size"] = "14"
	cfg["table_header_bg"] = "#01396a"
	cfg["table_header_text_color"] = "#ffffff"
	cfg["table_border_color"] = "#7f7f7f"
	cfg["table_font_size"] = "15"
	cfg["justify_paragraphs"] = false // 系列正文/标题/参考裸标签（8 篇成品 justify=0，样式靠后台 CSS）
	// 底部页脚库覆盖为「什么是系列商务合作」版块（默认开启）：公司内容策划文案/
	// 相关合作表、BD名片(默认Soso)、群二维码、公众号二维码、logo/更多。素材见 defaultFtSeriesSiteCoopAssets。
	if mods, ok := cfg["modules"].(map[string]any); ok {
		mods["fbif_footer"] = defaultFtSeriesBizCoopFooterModule()
		// 清空继承自网站基线的 bottom_blocks（rich_html site footer）：foodtalks-site.js render 底部
		// 优先走 bottom_blocks 分支、会忽略 fbif_footer。清空 → 回退到 fbif_footer 商务合作版块
		// （与 WowFood 同手法）。
		mods["bottom_blocks"] = map[string]any{"schema_version": 2, "enabled": false, "presets": []any{}}
	}
	return cfg
}

// defaultFoodTalksSeriesSiteRuleBehavior 暂复用网站基线行为；系列专属 tip/商务合作文案在后续块按成品调。
func defaultFoodTalksSeriesSiteRuleBehavior() map[string]any {
	return defaultFoodTalksSiteRuleBehavior()
}

// defaultFoodTalksSeriesRuleConfig 返回 FoodTalks「什么是」系列的默认参数配置。
// 规则来源：飞书《策划文公众号排版标准》。与前端降级常量 foodtalks-series-defaults.js 对齐；
// 真相以 /api/rules 为准（红线：不硬编码，规则后台可改）。
func defaultFoodTalksSeriesRuleConfig() map[string]any {
	return map[string]any{
		// 视觉：深蓝主色 + 棕灰正文 + 灰辅助（继承未列项由 mergeRuleDefaults 取 FBIF 基线）
		"heading_color":         "#02396b", // 一级/二级标题、目录标题、超链接
		"text_color":            "#544545", // 正文
		"muted_color":           "#888888", // 推荐阅读/参考来源/图注/原创声明
		"link_color":            "#02396b", // 超链接（下划线）
		"footer_note_color":     "#7f7f7f", // 文末署名 / 图源说明小字（成品实测口径）
		"font_size":             "15",
		"h1_font_size":          "20", // 一级标题文字（序号图下方居中）
		"h2_font_size":          "16", // 二级标题
		"toc_title_size":        "16", // 「目录」二字
		"caption_font_size":     "12", // 图片说明 & 图片来源
		"attribution_font_size": "14", // 推荐阅读块说明（斜体）
		"footer_note_font_size": "12", // 文末署名 / 图源说明小字（斜体）
		"paragraph_margin_x":    "8",  // 两端缩进 8
		"paragraph_gap":         "20",
		// 统一表格样式：表头深蓝
		"table_header_bg":         "#02396b",
		"table_header_text_color": "#ffffff",
		"table_header_bold":       true,
		"table_border_color":      "#888888", // 正文格框线 = 灰 RGB 136 136 136（成品口径）
		"table_font_size":         "15",
		"table_cell_padding":      "8",
		// 关闭 FBIF 专属模块：系列尾部用模板自渲的名片/群二维码，不要更多文章/案例库页脚
		"footer_enabled":        false,
		"more_articles_enabled": false,
		"more_articles_slots":   "0",
		// 素材库（序号图 / 群二维码 / 名片 + 尾部默认组合）—— 结构化模块，规则后台可改 URL
		"modules": map[string]any{
			"ft_series_materials": defaultFtSeriesMaterialsModule(),
			// 显式覆盖：系列与 FBIF 共用 bottom_blocks 结构但默认不同（名片/群二维码满宽图 + 可显隐更多文章）。
			// 不覆盖则经 mergeRuleDefaults 继承 FBIF 基线 → renderBottomFooter 会错渲 FBIF 提示/波波/1000。
			// 系列保持 v1（不切 V2）：系列 V2 的 spacer 间距是占位值、需 FoodTalks 后台视觉确认（无权限做），
			// 且 foodtalks-series.js 无 spacer renderer（spacer 会被 block-render 静默跳过，间距由 v1 的
			// gap=blank_line separator 承载，已 series-blocks-fidelity 验证）；系列原创/转载同内容、双预设零收益。
			// v1 已是统一 image/more_articles 原子区块、走 mountBlockPanel，满足红线#22（统一结构≠强制 schema_version 2）。
			"bottom_blocks": defaultSeriesBottomBlocksModule(),
		},
	}
}

// defaultFtSeriesMaterialsModule 返回「什么是」系列素材库默认（站内相对路径；
// 前端渲染时按 origin 解析为绝对地址，复制时自动上传微信 CDN；规则后台可改成 CDN/mmbiz URL）。
func defaultFtSeriesMaterialsModule() map[string]any {
	sections := map[string]any{}
	for i := 1; i <= 12; i++ {
		sections[strconv.Itoa(i)] = fmt.Sprintf("/assets/foodtalks-series/sections/%02d.png", i)
	}
	groups := map[string]any{
		"调味品":    "/assets/foodtalks-series/groups/seasoning.png",
		"乳品":     "/assets/foodtalks-series/groups/dairy.png",
		"零食":     "/assets/foodtalks-series/groups/snack.png",
		"饮料":     "/assets/foodtalks-series/groups/beverage.png",
		"包装":     "/assets/foodtalks-series/groups/packaging.png",
		"奶酪":     "/assets/foodtalks-series/groups/cheese.png",
		"食品供需对接": "/assets/foodtalks-series/groups/supply-demand.png",
		"烘焙":     "/assets/foodtalks-series/groups/bakery.png",
		"功能性食品":  "/assets/foodtalks-series/groups/functional.png",
		"新茶饮":    "/assets/foodtalks-series/groups/tea.png",
		"冰淇淋":    "/assets/foodtalks-series/groups/icecream.png",
		"配料":     "/assets/foodtalks-series/groups/ingredient.png",
		"营销":     "/assets/foodtalks-series/groups/marketing.png",
		"渠道":     "/assets/foodtalks-series/groups/channel.png",
		"代工":     "/assets/foodtalks-series/groups/oem.png",
		"酒类":     "/assets/foodtalks-series/groups/alcohol.png",
		"食品资讯":   "/assets/foodtalks-series/groups/news.png",
		"方便速食":   "/assets/foodtalks-series/groups/convenience.png",
	}
	cardNames := []string{"Aimee", "Charlie", "Eileen", "Erica", "Estelle", "Isaac", "Justine", "Kee", "Lan", "Rain", "Renee", "Soso", "Tiki", "Tina", "Yisa"}
	cards := map[string]any{}
	for _, n := range cardNames {
		cards[n] = "/assets/foodtalks-series/cards/" + strings.ToLower(n) + ".jpg"
	}
	return map[string]any{
		"sections": sections,
		"groups":   groups,
		"cards":    cards,
		"author_card_aliases": map[string]any{
			"Ian": "Lan",
		},
		"footer_default": map[string]any{
			"author_card": "",
			"group":       "包装",
			"bd_card":     "Eileen", // BD 名片复用 cards 库，默认 Eileen（规则后台可改）
		},
	}
}

// defaultFoodTalksSeriesRuleBehavior 返回「什么是」系列识别正则 / 文案默认（红线下放：规则后台可配）。
// 系列专属正则放 params（顶层只允许 author/tip/ref_header/ref_item 4 个正则字段）。
func defaultFoodTalksSeriesRuleBehavior() map[string]any {
	return map[string]any{
		"templates": map[string]any{
			"recommended_reading_note": "查看更多推荐阅读，请拉至文末点击“阅读原文”访问FoodTalks网站",
			"ref_header_label":         "参考来源：",
			// 商务合作导联行替换文案；{contact_section} 自动取文中「联系XX」标题（2026-06-11 飞书反馈）。
			"biz_coop_replacement": "如有合作意向，请移至“{contact_section}”获取联系方式",
		},
		"params": map[string]any{
			"level1_regex":              map[string]any{"type": "string", "default": `^[一二三四五六七八九十百零]+\s*[、.．]\s*`},
			"level2_regex":              map[string]any{"type": "string", "default": `^[（(]\s*[一二三四五六七八九十百零]+\s*[)）]`},
			"toc_marker_regex":          map[string]any{"type": "string", "default": `^目录\s*$`},
			"recommended_reading_regex": map[string]any{"type": "string", "default": `^\s*(相关阅读|推荐阅读)\s*[：:]\s*$`},
			"ref_section_regex":         map[string]any{"type": "string", "default": `^(参考(来源|文献|资料|链接)|信息来源)\s*[：:]?\s*$`},
			"ref_item_regex":            map[string]any{"type": "string", "default": `^\s*\[\d+\]`},
			// 联系小节公司信息居中（倒数第二个一级标题含「联系」→ 其下公司信息块居中，描述正文左对齐）
			"contact_section_regex": map[string]any{"type": "string", "default": `联系`},
			"contact_field_regex":   map[string]any{"type": "string", "default": `^(地址|地點|地点|官网|官網|网址|網址|网站|網站|电话|電話|联系电话|联系方式|邮箱|郵箱|邮件|手机|手機|微信|公众号|公眾號|传真|傳真|Tel|Phone|Email|E-?mail|Add|Address|Web|Website)\s*[：:]`},
			// 文末区块识别（成品实测口径）：「提示：」标签 / 「* …」声明条目 / 文末署名·图源说明
			"tip_label_regex":   map[string]any{"type": "string", "default": `^提示\s*[：:]\s*$`},
			"tip_item_regex":    map[string]any{"type": "string", "default": `^\s*[*＊·]`},
			"footer_note_regex": map[string]any{"type": "string", "default": `^本文(?:未标注来源图片/数据均来自|作者\s*[：:]|图片来源)|图片均来自`},
			// 商务合作导联行（检测到该句式整行替换为 biz_coop_replacement 文案，2026-06-11 飞书反馈）
			"biz_coop_replace_regex": map[string]any{"type": "string", "default": `^\s*商务合作对接请点击`},
			// 前置物料剥离：系列文章无「作者：」行，正文以头图为锚点。'firstImage' = 头图之前的
			// 标题段 + 单格副标题表格都是编辑前置物料（进微信后台标题/摘要），不进正文。
			"content_start_signal":    map[string]any{"type": "string", "default": "firstImage"},
			"front_matter_scan_limit": map[string]any{"type": "number", "default": 25},
		},
		"lists": map[string]any{
			"image_caption_keywords": []any{
				"图片来源", "资料来源", "数据来源", "图源", "来源",
				"制图", "截图", "图注", "注：", "Source", "Photo", "Image", "Credit",
			},
		},
	}
}

func defaultRuleStoreDocumentV2() ruleStoreDocumentV2 {
	now := time.Now().UTC().Format(time.RFC3339)
	return ruleStoreDocumentV2{
		SchemaVersion: 2,
		Accounts:      defaultRuleAccounts(now),
		Rules:         defaultRuleDefinitions(now),
		UpdatedAt:     now,
	}
}
