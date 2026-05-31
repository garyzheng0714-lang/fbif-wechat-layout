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
			"author_line_regex":             map[string]any{"type": "string", "default": `^(?:\*\*)?(?:作者|撰文|撰稿|文)(?:\*\*)?[\s]*[：:/|｜]\s*(.+)`},
			"source_line_regex":             map[string]any{"type": "string", "default": `^(?:\*\*)?(?:出品|来源|出处)(?:\*\*)?[\s]*[：:/|｜]\s*(.+)`},
			"editor_line_regex":             map[string]any{"type": "string", "default": `^(?:\*\*)?(?:编辑|责编)(?:\*\*)?[\s]*[：:/|｜]\s*(.+)`},
			"ref_section_regex":             map[string]any{"type": "string", "default": `^(参考(来源|文献|资料|链接)|信息来源)[：:]?$`},
			"footer_prompt_marker":          map[string]any{"type": "string", "default": `^提示[：:]$`},
			"footer_prompt_detection_regex": map[string]any{"type": "string", "default": `(?i)本文为.*转载|转载请|FBIF投稿|商务合作|fbifmedia1`},
			"docx_author_line_regex":        map[string]any{"type": "string", "default": `^作者[：:]\s*(.+)$`},
			"md_metadata_skip_regex":        map[string]any{"type": "string", "default": `(?i)^Author:\s|^Published:\s|^Source:\s`},
			// footer.html 内嵌的存量原创声明（engine.js 转载改写时按此字面量定位替换）。
			"footer_stock_original_text": map[string]any{"type": "string", "default": "* 本文为FBIF原创，欢迎转发朋友圈；"},
		},
		"conditions": []any{
			map[string]any{
				"id": "is_mote_author",
				"all": []any{
					map[string]any{"path": "article.author_normalized", "op": "eq", "value": "mote莫特"},
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
			"footer_repost_notice":   "* 本文为转载，不代表{{ account.name }}立场。",
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

func mergeRuleBehavior(behavior map[string]any) map[string]any {
	merged := cloneRuleMap(defaultRuleBehavior())
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
			merged[key] = value
		}
	}
	return merged
}
