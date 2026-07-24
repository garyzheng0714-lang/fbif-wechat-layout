package main

import "encoding/json"

const defaultMoteRefHeadImageURL = "https://fbif-feishu-base.oss-cn-shanghai.aliyuncs.com/fbif-attachment-to-url/2026/05/tblMQeXvSGd7Hebf_-CkOuPdpNzb-_3u9Rxc5KA_1778028720411/img_v3_0211e_8b79127d-72a5-4011-9f01-fe3b30c7548g_1778028720565.png"

func defaultRuleBehavior() map[string]any {
	return map[string]any{
		"schema_version": float64(1),
		"params": map[string]any{
			"mote_ref_image_url": map[string]any{
				"type":     "url",
				"required": true,
				"default":  defaultMoteRefHeadImageURL,
			},
			// 公众号识别正则（红线下放：规则后台可配；客户端 fbif.js 读 .default，
			// 读不到/非法时降级到代码内同值 const 离线兜底）。
			"author_line_regex": map[string]any{"type": "string", "default": `^(?:\*\*)?(?:作者|撰文|撰稿|文)(?:\*\*)?[\s]*[：:/|｜]\s*(.+)`},
			"source_line_regex": map[string]any{"type": "string", "default": `^(?:\*\*)?(?:出品|来源|出处)(?:\*\*)?[\s]*[：:/|｜]\s*(.+)`},
			"editor_line_regex": map[string]any{"type": "string", "default": `^(?:\*\*)?(?:编辑|责编)(?:\*\*)?[\s]*[：:/|｜]\s*(.+)`},
			// URL 导入开头署名扫描上限：微信原文常把「作者 | X」做成粗体/大字，
			// 通用 DOM 分类会先把它判成 h。FBIF 渲染器在此前 N 个内容块内先按
			// 署名语义识别，再接受 h/txt 等结构类型，避免正文中段同名标题被误搬。
			"url_attribution_scan_limit": map[string]any{"type": "number", "default": float64(12)},
			// URL 正文没有显式来源/出品时，用微信抓取结构的 source_name（公众号名称）
			// 作为文章来源兜底；正文显式来源始终优先。
			"url_source_name_fallback_enabled": map[string]any{"type": "boolean", "default": true},
			"ref_section_regex":                map[string]any{"type": "string", "default": `^(参考(来源|文献|资料|链接)|信息来源)[：:]?$`},
			"footer_prompt_marker":             map[string]any{"type": "string", "default": `^提示[：:]$`},
			"footer_prompt_detection_regex":    map[string]any{"type": "string", "default": `(?i)本文为.*转载|转载请|FBIF投稿|商务合作|fbifmedia1`},
			"docx_author_line_regex":           map[string]any{"type": "string", "default": `^作者[：:]\s*(.+)$`},
			// DOCX 前置扫描：编辑部结构标记（整行「正文/封面/推荐语」等）命中即重置扫描窗口，
			// 防止前置材料过长时作者行掉出上限 25（2026-07-11 生产事故：作者识别为空 →
			// 前置不剥离 + Mote 署名图/名片开关连锁消失）。
			"docx_front_matter_marker_regex": map[string]any{"type": "string", "default": `^(正文|标题|封面|推荐语|校对|引言)[：:]?$`},
			"docx_front_matter_scan_limit":   map[string]any{"type": "number", "default": float64(25)},
			"md_metadata_skip_regex":         map[string]any{"type": "string", "default": `(?i)^Author:\s|^Published:\s|^Source:\s`},
			"image_caption_max_chars":        map[string]any{"type": "number", "default": defaultImageCaptionMaxChars},
			"image_caption_center_max_chars": map[string]any{
				"type":    "number",
				"default": defaultImageCaptionCenterMaxChars,
			},
			"image_caption_strong_max_chars": map[string]any{
				"type":    "number",
				"default": defaultImageCaptionStrongMaxChars,
			},
			// footer.html 内嵌的存量原创声明（engine.js 转载改写时按此字面量定位替换）。
			"footer_stock_original_text": map[string]any{"type": "string", "default": "* 本文为FBIF原创，欢迎转发朋友圈；"},
		},
		// Mote 作者识别：作者名规范化（去空格转小写）后包含 mote 或 莫特 即命中，覆盖
		// mote / 莫特 / mote 莫特 / Mote莫特 / MOTE莫特 各种写法（2026-07-11 用户裁定放宽）。
		"conditions": []any{
			map[string]any{
				"id": "is_mote_author",
				"any": []any{
					map[string]any{"path": "article.author_normalized", "op": "includes", "value": "mote"},
					map[string]any{"path": "article.author_normalized", "op": "includes", "value": "莫特"},
				},
			},
		},
		"actions": []any{
			map[string]any{
				"when":      "is_mote_author",
				"type":      "insert_image_before",
				"target":    "first_ref_heading",
				"src_param": "mote_ref_image_url",
				"class":     "wx-mote-author",
			},
		},
		"templates": map[string]any{
			"author_line":            "作者：{{ article.author }}",
			"source_line":            "来源：{{ article.source }}",
			"footer_original_notice": "* 本文为{{ account.name }}原创，欢迎转发朋友圈；",
			"footer_repost_notice":   "* 本文为转载文章。",
		},
		"output_contracts": map[string]any{
			"author_line": map[string]any{"max_length": float64(80), "allow_html": false},
			"source_line": map[string]any{"max_length": float64(120), "allow_html": false},
		},
	}
}

func cloneRuleMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	b, err := json.Marshal(input)
	if err != nil {
		out := map[string]any{}
		for k, v := range input {
			out[k] = v
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		out := map[string]any{}
		for k, v := range input {
			out[k] = v
		}
		return out
	}
	return out
}

// baselineID 是规则文档显式声明的继承目标：网站/系列不再混入 FBIF
// Mote 条件，自建规则也不根据自身 id 猜基线。
func mergeRuleBehavior(baselineID string, behavior map[string]any) map[string]any {
	merged := cloneRuleMap(baselineRuleBehaviorForPreset(baselineID))
	if behavior == nil {
		return merged
	}
	// 对象类字段按 key 合并（外部覆盖默认）。lists 是网站识别词表/标记（红线下放），
	// 默认 behavior（公众号）无此字段，合并后即等于存储值——必须透传，否则网站
	// wechat_trailer_markers / image_caption_keywords 等永远传不到客户端。
	for _, key := range []string{"params", "templates", "output_contracts", "lists"} {
		raw, ok := behavior[key].(map[string]any)
		if !ok {
			continue
		}
		base, _ := merged[key].(map[string]any)
		if base == nil {
			base = map[string]any{}
		}
		for k, v := range raw {
			base[k] = v
		}
		merged[key] = base
	}
	// 标量/数组类字段直接覆盖。author_regex/tip_regex/ref_*_regex 是 FoodTalks 网站
	// 识别正则（红线下放）——默认 behavior 无此字段，必须透传，否则服务端配置无法生效。
	for _, key := range []string{"schema_version", "conditions", "actions",
		"author_regex", "tip_regex", "ref_header_regex", "ref_item_regex"} {
		if value, ok := behavior[key]; ok {
			// 一次性迁移（2026-07-11）：把旧的精确匹配 is_mote_author 条件放宽为
			// mote/莫特 包含匹配。只跳过「等于旧内置默认」的持久化 conditions（用 baseline
			// 新默认）；后台自定义过的条件保留不动。见 WORKLOG-mote-alias-broaden-2026-07-11.md。
			if key == "conditions" && isLegacyMoteConditions(value) {
				continue
			}
			merged[key] = value
		}
	}
	return merged
}

// isLegacyMoteConditions 判断持久化 conditions 是否恰为旧内置默认（单条 is_mote_author，
// all + eq "mote莫特"）。用于一次性迁移放宽 Mote 识别，只命中未被后台改动过的旧默认。
func isLegacyMoteConditions(value any) bool {
	arr, ok := value.([]any)
	if !ok || len(arr) != 1 {
		return false
	}
	c, ok := arr[0].(map[string]any)
	if !ok || c["id"] != "is_mote_author" {
		return false
	}
	all, ok := c["all"].([]any)
	if !ok || len(all) != 1 {
		return false
	}
	e, ok := all[0].(map[string]any)
	if !ok {
		return false
	}
	return e["path"] == "article.author_normalized" && e["op"] == "eq" && e["value"] == "mote莫特"
}
