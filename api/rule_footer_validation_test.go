package main

import "testing"

// 红线 M1：网站规则带 fbif_footer 页脚库 + 识别正则，应能通过保存校验（往返）。
func TestValidateFooterLibraryAndRegex(t *testing.T) {
	cfg := defaultFoodTalksSiteRuleConfig() // 含 image + modules.mctoc + modules.fbif_footer
	if err := validateRuleConfig(cfg); err != nil {
		t.Fatalf("默认网站规则 config（含页脚库）应通过校验，却报错: %v", err)
	}
	beh := defaultFoodTalksSiteRuleBehavior() // 含 4 个正则
	if err := validateRuleBehaviorPayload(beh); err != nil {
		t.Fatalf("默认网站 behavior（含识别正则）应通过校验，却报错: %v", err)
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
	if err := validateRuleBehaviorPayload(beh); err == nil {
		t.Fatal("非法正则应被拒绝")
	}
}
