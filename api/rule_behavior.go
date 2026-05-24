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
	for _, key := range []string{"params", "templates", "output_contracts"} {
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
	for _, key := range []string{"schema_version", "conditions", "actions"} {
		if value, ok := behavior[key]; ok {
			merged[key] = value
		}
	}
	return merged
}
