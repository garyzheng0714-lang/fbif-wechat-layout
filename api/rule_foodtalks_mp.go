package main

import (
	"fmt"
	"sort"
)

// 「FoodTalks食品资讯」公众号默认规则（2026-10-27 用户裁定：新加账号，什么是系列不动；原稿用 FoodTalks
// 网站链接；名片卡排完在公众号后台手动插）。依据《FoodTalks公众号排版规范（新）》与编辑 2026-10 手排的
// 9 篇（不一致处按成品）。与什么是系列共用 foodtalks-series 渲染器，差异只在这里：
//   - 一级标题：饼干序号图（1820×220，/assets/foodtalks-mp/sections/）+ 18px 深蓝粗体居中；
//   - 排网站稿（behavior.params.site_article_layout）：署名挪到文末深蓝斜体，网站文末换成公众号提示；
//   - 「本文图片来源」等说明 15px #888 斜体；
//   - 图片两端缩进与正文同为 8px（什么是系列图片满列）；
//   - 贴公众号链接时，抓取按字号认出的小标题（没级别）按一级排，单独一行的「1」「01」序号并进下一行标题；
//   - 页脚：Bobo 名片 →「关注公众号」（名片卡后台手插）→「加入微信群」+ 食品资讯群码 → Lemon 说明。
// 前端降级常量：foodtalks-series-defaults.js SITE_ARTICLE_*、asset-library-defaults.js。

const foodTalksMpSectionCount = 11

func defaultFoodTalksMpRuleConfig() map[string]any {
	cfg := defaultFoodTalksSeriesRuleConfig()
	cfg["h1_font_size"] = "18"
	cfg["footer_note_color"] = "#888888"
	cfg["footer_note_font_size"] = "15"
	cfg["image_margin_x"] = "8" // 图片与正文同一两端边距（编辑手排的已发稿都对齐，2026-10-28 用户：「图片页边距没有统一」）
	mods := cfg["modules"].(map[string]any)
	sections := map[string]any{}
	for i := 1; i <= foodTalksMpSectionCount; i++ {
		sections[fmt.Sprint(i)] = foodTalksMpSectionURL(i)
	}
	mods["ft_series_materials"] = map[string]any{"sections": sections}
	mods["bottom_blocks"] = defaultFoodTalksMpBottomBlocksModule()
	mods["asset_library"] = defaultAssetLibraryModule()
	return cfg
}

func foodTalksMpSectionURL(n int) string {
	return fmt.Sprintf("/assets/foodtalks-mp/sections/%02d.png", n)
}

func defaultFoodTalksMpRuleBehavior() map[string]any {
	beh := defaultFoodTalksSeriesRuleBehavior()
	params := beh["params"].(map[string]any)
	params["site_article_layout"] = map[string]any{"type": "boolean", "default": true}
	params["english_font_size"] = map[string]any{"type": "number", "default": 14}
	params["credit_line_regex"] = map[string]any{"type": "string", "default": `^(本文来源|来源|出处|出品|作者|撰文|撰稿|文|编辑|责编|责任编辑|微信编辑|新媒体编辑|编者|审核|审校|复核|校对|翻译|编译|采访|设计|视觉|整理|统筹|策划)\s*[：:｜|]\s*\S`}
	params["site_byline_regex"] = map[string]any{"type": "string", "default": `^本文为(.{1,40}?)原创`}
	params["site_footer_start_regex"] = map[string]any{"type": "string", "default": `^(提示\s*[：:]\s*$|[*＊]\s*资讯投稿与商务合作|加入微信群\s*$|关注公众号\s*$|点击获取更多全球食品资讯)`}
	params["site_tip_source_regex"] = map[string]any{"type": "string", "default": `^[*＊]?\s*本文为(.{1,40}?)原创(?:《(.{1,120}?)》)?`}
	params["own_source_regex"] = map[string]any{"type": "string", "default": `(?i)^foodtalks`}
	templates := beh["templates"].(map[string]any)
	templates["source_credit"] = "来源：{source}{source_id}"
	templates["repost_tip"] = "*本文为{source}原创{author}，转载请联系出处。{original_title}"
	templates["original_tip"] = "*本文为FoodTalks原创，转载请底部留言。"
	templates["contact_tip"] = "*投稿与商务合作请在后台回复“商务合作”。"
	return beh
}

// defaultFoodTalksMpBottomBlocksModule 页脚照编辑手排的成品：提示（正文里生成）之后
// Bobo 名片 →「关注公众号」→（公众号名片卡，后台手插）→「点击卡片关注FoodTalks公众号」→
// 「加入微信群」→ 食品资讯群码（可换成 18 张品类群码）→ Lemon 说明。原创/转载同一套。
func defaultFoodTalksMpBottomBlocksModule() map[string]any {
	text := func(name, content string, size int, color, align string, bold, italic bool) map[string]any {
		return map[string]any{
			"type": "text", "display_name": name, "enabled": true,
			"fields": map[string]any{"content": content, "bold": bold, "italic": italic, "font_size": size, "text_color": color, "align": align},
		}
	}
	image := func(name, asset string) map[string]any {
		return map[string]any{"type": "image", "display_name": name, "enabled": true, "frontend_editable": true, "fields": map[string]any{"image": asset}}
	}
	return map[string]any{
		"schema_version":   2,
		"enabled":          true,
		"active_preset_id": "default",
		"presets": []any{
			map[string]any{
				"id":    "default",
				"name":  "FoodTalks食品资讯页脚",
				"order": []any{"bobo_card", "follow_title", "follow_hint", "follow_gap", "group_title", "group_qr", "group_note"},
				"blocks": map[string]any{
					"bobo_card":    image("Bobo 名片", "{{asset#ft_mp_bobo_card}}"),
					"follow_title": text("关注公众号", "关注公众号", 18, "#02396b", "left", true, true),
					"follow_hint":  text("名片卡提示", "点击卡片关注FoodTalks公众号", 12, "#888888", "center", false, false),
					"follow_gap":   map[string]any{"type": "spacer", "display_name": "间距", "enabled": true, "fields": map[string]any{"height": 20}},
					"group_title":  text("加入微信群", "加入微信群", 18, "#02396b", "left", true, true),
					"group_qr":     image("群二维码", "{{asset#ft_mp_group_assistant}}"),
					"group_note":   text("加群说明", "*如遇二维码失效无法加群，请联系工作人员Lemon（微信号：FBIF_Lemon）。", 15, "#888888", "left", false, true),
				},
			},
		},
	}
}

// defaultFoodTalksMpAssetLibraryAssets 「FoodTalks食品资讯」素材（scope=foodtalks-mp）：饼干序号图、
// Bobo 名片、食品资讯群码，外加什么是系列的 18 张品类群码（同一批图，换群时可选）。
func defaultFoodTalksMpAssetLibraryAssets() []any {
	out := []any{}
	for i := 1; i <= foodTalksMpSectionCount; i++ {
		out = append(out, assetLibraryImage(fmt.Sprintf("ft_mp_section_%02d", i), "custom", fmt.Sprintf("FoodTalks食品资讯序号图 %02d", i), foodTalksMpSectionURL(i), foodTalksMpRuleAccountID, 0, 0, "FoodTalks食品资讯", "序号图"))
	}
	out = append(out,
		assetLibraryImage("ft_mp_bobo_card", "avatar", "Bobo 名片（新媒体总监）", "/assets/foodtalks-mp/bobo-card.webp", foodTalksMpRuleAccountID, 0, 0, "FoodTalks食品资讯", "名片"),
		assetLibraryImage("ft_mp_group_assistant", "qr_group", "食品资讯群二维码（线上助手）", "/assets/foodtalks-mp/group-news.webp", foodTalksMpRuleAccountID, 0, 0, "FoodTalks食品资讯", "群二维码"),
	)
	groups, _ := defaultFtSeriesMaterialsModule()["groups"].(map[string]any)
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		url := fmt.Sprint(groups[name])
		out = append(out, assetLibraryImage("ft_mp_group_"+assetIDPartFromURL(url), "custom", name+"群二维码", url, foodTalksMpRuleAccountID, 0, 0, "FoodTalks食品资讯", "群二维码", name))
	}
	return out
}
