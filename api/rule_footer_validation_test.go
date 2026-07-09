package main

import (
	"strings"
	"testing"
)

func anyStringSlice(raw any) []string {
	arr, _ := raw.([]any)
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// 红线 M1：网站规则带 fbif_footer 页脚库 + 识别正则，应能通过保存校验（往返）。
func TestValidateFooterLibraryAndRegex(t *testing.T) {
	cfg := defaultFoodTalksSiteRuleConfig() // 含 image + modules.mctoc + modules.fbif_footer
	if err := validateRuleConfig(cfg); err != nil {
		t.Fatalf("默认网站规则 config（含页脚库）应通过校验，却报错: %v", err)
	}
	beh := defaultFoodTalksSiteRuleBehavior() // 含 4 个正则
	if err := validateRuleBehaviorPayload(foodTalksSiteDefaultRulePresetID, beh); err != nil {
		t.Fatalf("默认网站 behavior（含识别正则）应通过校验，却报错: %v", err)
	}

	modules := cfg["modules"].(map[string]any)
	footer := modules["fbif_footer"].(map[string]any)
	segments := footer["segments"].(map[string]any)
	biz := strings.Join(anyStringSlice(segments["biz_coop"].(map[string]any)["html_lines"]), "\n")
	group := strings.Join(anyStringSlice(segments["join_group"].(map[string]any)["html_lines"]), "\n")
	if !strings.Contains(biz, assetRefID("ft_footer_bobo_card")) {
		t.Fatalf("网站默认商务合作段勾选后应输出 Bobo 二维码，实际: %s", biz)
	}
	if strings.Contains(group, "bobo-card") || strings.Contains(group, assetRefID("ft_footer_bobo_card")) || strings.Contains(group, assetRefID("ft_footer_banner")) {
		t.Fatalf("网站默认加群段只应包含群二维码，不应混入 Bobo 二维码，实际: %s", group)
	}
}

func TestValidateFtSeriesSiteFooterDefaults(t *testing.T) {
	cfg := defaultFoodTalksSeriesSiteRuleConfig()
	if err := validateRuleConfig(cfg); err != nil {
		t.Fatalf("网站什么是系列默认规则 config 应通过校验，却报错: %v", err)
	}
	beh := defaultFoodTalksSeriesSiteRuleBehavior()
	if err := validateRuleBehaviorPayload(foodTalksSiteDefaultRulePresetID, beh); err != nil {
		t.Fatalf("网站什么是系列默认 behavior 应通过校验，却报错: %v", err)
	}

	modules := cfg["modules"].(map[string]any)
	footer := modules["fbif_footer"].(map[string]any)
	order := anyStringSlice(footer["segment_order"])
	segments := footer["segments"].(map[string]any)
	biz := strings.Join(anyStringSlice(segments["biz_coop"].(map[string]any)["html_lines"]), "\n")
	bd := strings.Join(anyStringSlice(segments["bd_card"].(map[string]any)["html_lines"]), "\n")
	coopTable := strings.Join(anyStringSlice(segments["coop_table"].(map[string]any)["html_lines"]), "\n")
	group := strings.Join(anyStringSlice(segments["join_group"].(map[string]any)["html_lines"]), "\n")
	follow := strings.Join(anyStringSlice(segments["follow_account"].(map[string]any)["html_lines"]), "\n")

	wantOrder := []string{"biz_coop", "bd_card", "coop_table", "join_group", "follow_account", "logo", "more_info"}
	if strings.Join(order, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("什么是系列网站页脚顺序应是商务合作文案→BD名片→相关合作表→后续二维码，实际: %#v", order)
	}
	if strings.Contains(biz, "相关合作") || !strings.Contains(biz, "线上名片") {
		t.Fatalf("商务合作段只应包含标题和两段文案，不能吞掉相关合作表，实际: %s", biz)
	}
	if !strings.Contains(bd, assetRefID("ftss_bd_soso")) {
		t.Fatalf("默认 BD 名片应使用 Soso 素材引用，实际: %s", bd)
	}
	if !strings.Contains(coopTable, "相关合作") || !strings.Contains(coopTable, "食品雷达") || !strings.Contains(coopTable, "更多合作，请点击") {
		t.Fatalf("相关合作表应独立成段并包含成品表格内容，实际: %s", coopTable)
	}
	if !strings.Contains(group, "加入FBIF饮料群") || !strings.Contains(group, `width="150" height="150"`) {
		t.Fatalf("加群段应是饮料群标题 + 150x150 二维码，实际: %s", group)
	}
	if strings.Contains(group, assetRefID("ftss_bd_soso")) || strings.Contains(group, assetRefID("ft_footer_bobo_card")) || strings.Contains(group, "bobo-card") {
		t.Fatalf("什么是系列加群段只应包含饮料群二维码，不能混入 BD/Bobo 名片，实际: %s", group)
	}
	if !strings.Contains(follow, "关注FoodTalks什么是系列公众号") || !strings.Contains(follow, `height="180"`) {
		t.Fatalf("关注公众号段应使用正确标题和 600x180 二维码，实际: %s", follow)
	}
}

func TestFooterModuleRejectsUnsafeHTML(t *testing.T) {
	bad := map[string]any{
		"segments": map[string]any{
			"biz_coop": map[string]any{"html_lines": []any{`<p onclick="evil()">x</p>`}},
		},
	}
	if err := validateFBIFFooterModule(bad); err == nil {
		t.Fatal("含 onclick 的页脚片段应被拒绝")
	}
}

func TestFooterModuleAcceptsEditedContent(t *testing.T) {
	good := map[string]any{
		"enabled":          true,
		"active_preset_id": "fbif-original",
		"segments": map[string]any{
			"join_group": map[string]any{"html_lines": []any{
				`<p style="color:#01396a;"><strong>加入微信群</strong></p>`,
				`<p><img src="https://static.foodtalks.cn/image/news/new-qr.png" width="600" /></p>`,
			}},
		},
		"presets": []any{
			map[string]any{"id": "podcast", "name": "播客专用", "segments": map[string]any{"tip": true}},
		},
	}
	if err := validateFBIFFooterModule(good); err != nil {
		t.Fatalf("合法的编辑后页脚（换二维码+新增预设）应通过，却报错: %v", err)
	}
}

func TestBehaviorRejectsBadRegex(t *testing.T) {
	beh := map[string]any{"author_regex": "([unclosed"}
	if err := validateRuleBehaviorPayload(foodTalksSiteDefaultRulePresetID, beh); err == nil {
		t.Fatal("非法正则应被拒绝")
	}
}

// 红线下放：公众号识别正则进 behavior.params（{type:string,default}），默认值应通过保存校验。
func TestValidatePublicAccountRegexParams(t *testing.T) {
	beh := defaultRuleBehavior() // 含 author_line_regex / source_line_regex / footer_prompt_* 等
	if err := validateRuleBehaviorPayload(foodTalksSiteDefaultRulePresetID, beh); err != nil {
		t.Fatalf("公众号默认 behavior（含识别正则 params）应通过校验，却报错: %v", err)
	}
}

// 红线下放：behavior.lists（网站识别词表/标记）应能编辑后保存（往返）。
func TestBehaviorListsRoundTrip(t *testing.T) {
	beh := map[string]any{
		"lists": map[string]any{
			"wechat_trailer_markers": []any{`/\s*更多文章\s*/`, `(?i)fbifmedia`},
			"image_caption_keywords": []any{"图片来源", "Source"},
		},
	}
	if err := validateRuleBehaviorPayload(foodTalksSiteDefaultRulePresetID, beh); err != nil {
		t.Fatalf("编辑后的 behavior.lists 应通过校验，却报错: %v", err)
	}
}

// behavior.lists 中标记/正则项若非法正则，应被拒绝（防后台误配导致整篇渲染崩）。
func TestBehaviorListsRejectsBadRegex(t *testing.T) {
	beh := map[string]any{"lists": map[string]any{"wechat_trailer_markers": []any{"([unclosed"}}}
	if err := validateRuleBehaviorPayload(foodTalksSiteDefaultRulePresetID, beh); err == nil {
		t.Fatal("behavior.lists 中非法正则应被拒绝")
	}
}

// 红线下放：mergeRuleBehavior（下发链路）必须透传网站 lists + 扁平识别正则，
// 否则服务端配置传不到客户端，规则"后台可配"名存实亡。
func TestMergeRuleBehaviorPreservesSiteFields(t *testing.T) {
	stored := defaultFoodTalksSiteRuleBehavior()
	merged := mergeRuleBehavior(foodTalksSiteDefaultRulePresetID, stored)
	lists, ok := merged["lists"].(map[string]any)
	if !ok || lists["wechat_trailer_markers"] == nil {
		t.Fatalf("下发后应保留 behavior.lists.wechat_trailer_markers，实际 lists=%v", merged["lists"])
	}
	if merged["author_regex"] == nil || merged["ref_header_regex"] == nil {
		t.Fatalf("下发后应保留网站扁平识别正则 author_regex/ref_header_regex")
	}
	// 客户端编辑 lists 后下发也应保留覆盖值。
	edited := map[string]any{"lists": map[string]any{"title_keywords": []any{"自定义标题词"}}}
	m2 := mergeRuleBehavior(foodTalksSiteDefaultRulePresetID, edited)
	got, _ := m2["lists"].(map[string]any)
	arr, _ := got["title_keywords"].([]any)
	if len(arr) != 1 || arr[0] != "自定义标题词" {
		t.Fatalf("下发应保留编辑后的 lists.title_keywords，实际=%v", got["title_keywords"])
	}
}

// behavior.lists 中含不安全 HTML 的词条应被拒绝（XSS 防护）。
func TestBehaviorListsRejectsUnsafeHTML(t *testing.T) {
	beh := map[string]any{"lists": map[string]any{"image_caption_keywords": []any{`<script>x</script>`}}}
	if err := validateRuleBehaviorPayload(foodTalksSiteDefaultRulePresetID, beh); err == nil {
		t.Fatal("behavior.lists 中含 <script> 的词条应被拒绝")
	}
}
