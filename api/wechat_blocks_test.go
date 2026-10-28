package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func jsContentWrap(body string) string {
	return `<html><head><meta property="og:title" content="Test Article"></head>` +
		`<body><h2 class="rich_media_title">Test Article</h2>` +
		`<div id="js_content">` + body + `</div>` +
		`<div class="rich_media_tool"></div></body></html>`
}

func findKinds(blocks []Block) []string {
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, b.K)
	}
	return out
}

func requireKinds(t *testing.T, blocks []Block, want []string) {
	t.Helper()
	got := findKinds(blocks)
	if len(got) != len(want) {
		t.Fatalf("kinds len = %d, want %d: got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds mismatch at %d: got=%v want=%v", i, got, want)
		}
	}
}

func findBlockByText(blocks []Block, needle string) *Block {
	for i := range blocks {
		if blocks[i].Text != "" && strings.Contains(blocks[i].Text, needle) {
			return &blocks[i]
		}
		for _, r := range blocks[i].Runs {
			if strings.Contains(r.Text, needle) {
				return &blocks[i]
			}
		}
	}
	return nil
}

func findDecisionByText(decisions []RoleDecision, needle string) *RoleDecision {
	for i := range decisions {
		b := decisions[i].Block
		if b.Text != "" && strings.Contains(b.Text, needle) {
			return &decisions[i]
		}
		for _, r := range b.Runs {
			if strings.Contains(r.Text, needle) {
				return &decisions[i]
			}
		}
	}
	return nil
}

// ---- Bug A: pullquote with bold at body font-size must stay txt ----

func TestBoldPullquoteAtBodySizeIsTxt(t *testing.T) {
	html := jsContentWrap(
		`<p style="font-size: 16px; text-align: left;">` +
			`<strong>&#8220;杭州开车来只需要2小时，结果为了一杯奶茶要等4小时。&#8221;</strong>` +
			`</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	b := findBlockByText(blocks, "杭州开车来")
	if b == nil {
		t.Fatalf("pullquote text missing from blocks: kinds=%v", findKinds(blocks))
	}
	if b.K != "txt" {
		t.Errorf("pullquote must be txt (body), got %s", b.K)
	}
	if len(b.Runs) == 0 || !b.Runs[0].Bold {
		t.Errorf("pullquote bold must be preserved on runs, got %+v", b.Runs)
	}
}

func TestBoldPullquoteNoFontSizeIsTxt(t *testing.T) {
	// WeChat frequently omits inline font-size entirely on pullquotes,
	// relying on the default body size. These must NOT be mis-promoted.
	html := jsContentWrap(`<p><strong>&#8220;这是一段加粗强调，但字号正常。&#8221;</strong></p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "这是一段加粗强调")
	if b == nil {
		t.Fatalf("missing pullquote block: kinds=%v", findKinds(blocks))
	}
	if b.K != "txt" {
		t.Errorf("default-size bold must stay txt, got %s", b.K)
	}
}

func TestFontWeightNormalOverridesBoldAncestor(t *testing.T) {
	// WHY：这篇微信原文大量出现外层 span font-weight:bold、内层 textstyle
	// font-weight:normal 的结构。浏览器最终显示正常字重；抽取器也必须遵守内层覆盖，
	// 否则整篇正文都会被标成粗体。
	html := jsContentWrap(
		`<p style="font-size: 16px;">` +
			`<span style="font-weight: bold; font-size: 15px;">` +
			`<span textstyle="" style="font-weight: normal;">如果要选出2026年餐饮业的潘通口味。</span>` +
			`</span>` +
			`</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	b := findBlockByText(blocks, "潘通口味")
	if b == nil {
		t.Fatalf("missing paragraph block: kinds=%v", findKinds(blocks))
	}
	if b.K != "txt" {
		t.Fatalf("normal body paragraph should stay txt, got %s", b.K)
	}
	if len(b.Runs) == 0 {
		t.Fatalf("missing runs: %+v", b)
	}
	if b.Runs[0].Bold {
		t.Fatalf("inner font-weight:normal must override bold ancestor, got %+v", b.Runs)
	}
}

func TestStrongInsideFontWeightNormalStillBold(t *testing.T) {
	// WHY：支持 normal 覆盖不能把真正的局部强调洗掉；内层 strong 仍应生效。
	html := jsContentWrap(
		`<p><span style="font-weight: normal;">普通<strong>重点</strong></span></p>`)
	_, blocks, _ := extractArticleBlocks(html)

	b := findBlockByText(blocks, "普通")
	if b == nil {
		t.Fatalf("missing paragraph block: kinds=%v", findKinds(blocks))
	}
	if got := len(b.Runs); got != 2 {
		t.Fatalf("runs len = %d, want 2: %+v", got, b.Runs)
	}
	if b.Runs[0].Bold {
		t.Fatalf("normal text should not be bold: %+v", b.Runs)
	}
	if !b.Runs[1].Bold {
		t.Fatalf("strong text should remain bold: %+v", b.Runs)
	}
}

func TestBlankParagraphMarksNextBlockBlankBefore(t *testing.T) {
	// WHY：网站「什么是系列」联系区依赖公众号源稿里的显式空段来恢复分组间距。
	// 空段不应输出成一个无意义 txt 块，但必须挂到下一块，否则联系信息会被错误压成一团。
	html := jsContentWrap(`<p>第一段</p><p><br /></p><p>第二段</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	if len(blocks) != 2 {
		t.Fatalf("空段不应生成独立 block，got %d kinds=%v", len(blocks), findKinds(blocks))
	}
	if blocks[0].BlankBefore {
		t.Fatal("第一段前不应标 blankBefore")
	}
	if !blocks[1].BlankBefore {
		t.Fatalf("第二段应继承前置空段 blankBefore，got %+v", blocks[1])
	}
}

// 兼容旧版已经正确工作的图注写法：公众号历史文章不一定带明确的图文关系标记，
// 因此图片后的图注样式、来源文案和同段图片说明仍必须可用。
func TestLegacyCaptionSignalsRemainCompatible(t *testing.T) {
	const captionText = "这是图片的说明文字"
	variants := []struct {
		name string
		html string
	}{
		{name: "small-grey-centered", html: `<p><img data-src="https://mmbiz.qpic.cn/a.jpg" data-w="1080" data-h="720"></p><p style="font-size:12px;color:#888888;text-align:center">` + captionText + `</p>`},
		{name: "caption-class", html: `<p><img data-src="https://mmbiz.qpic.cn/a.jpg"></p><p class="caption">` + captionText + `</p>`},
		{name: "source-prefix", html: `<p><img data-src="https://mmbiz.qpic.cn/a.jpg"></p><p>图源：` + captionText + `</p>`},
		{name: "same-paragraph", html: `<p><img data-src="https://mmbiz.qpic.cn/a.jpg"><span style="font-size:12px;color:#888888">` + captionText + `</span></p>`},
	}

	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			_, blocks, _ := extractArticleBlocks(jsContentWrap(tc.html))
			b := findBlockByText(blocks, captionText)
			if b == nil || b.K != "cap" {
				t.Fatalf("legacy caption signal must remain compatible, got %v; kinds=%v", b, findKinds(blocks))
			}
		})
	}
}

func TestPlainTextDoesNotBecomeCaptionByImageAdjacencyAlone(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg"></p>` +
			`<p>这是紧跟图片、但没有图注表现的普通正文。</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "普通正文")
	if b == nil || b.K != "txt" {
		t.Fatalf("plain text after an image must stay txt, got %v; kinds=%v", b, findKinds(blocks))
	}
}

func TestExplicitFigcaptionSurvivesStyleAndWrapperMutation(t *testing.T) {
	variants := []string{
		`<figure><img data-src="https://mmbiz.qpic.cn/a.jpg"><figcaption>图源：Neuro</figcaption></figure>`,
		`<figure><section><img data-src="https://mmbiz.qpic.cn/a.jpg"></section><figcaption><p style="font-size:20px;color:#111111;text-align:left">图源：Neuro</p></figcaption></figure>`,
	}

	for _, body := range variants {
		decisions := extractArticleRoleDecisionsForTest(jsContentWrap(body))
		d := findDecisionByText(decisions, "图源：Neuro")
		if d == nil || d.Role != "cap" || d.CaptionProof != CaptionProofFigcaption ||
			d.Reason != "figcaption" || d.MediaID <= 0 || d.CaptionScope <= 0 {
			t.Fatalf("explicit figcaption must be structurally proven independent of visual style, got %+v", d)
		}
	}
}

func TestObservationLayerDoesNotEmitFinalHeadingOrCaptionRoles(t *testing.T) {
	html := jsContentWrap(
		`<figure><img data-src="https://mmbiz.qpic.cn/a.jpg"><figcaption>结构图注</figcaption></figure>` +
			`<h2>结构标题</h2><p>普通正文</p>`)
	for _, obs := range extractObservedBlocksForTest(html) {
		if obs.RawKind == "text" && (obs.BaseBlock.K == "h" || obs.BaseBlock.K == "cap") {
			t.Fatalf("observation layer emitted final role %q for %q", obs.BaseBlock.K, obs.Text)
		}
	}

	decisions := extractArticleRoleDecisionsForTest(html)
	if d := findDecisionByText(decisions, "结构图注"); d == nil || d.Role != "cap" {
		t.Fatalf("resolver must emit structurally proven caption, got %+v", d)
	}
	if d := findDecisionByText(decisions, "结构标题"); d == nil || d.Role != "h" {
		t.Fatalf("resolver must emit heading after observation, got %+v", d)
	}
}

func TestEveryCaptionDecisionCarriesBoundedMediaAssociation(t *testing.T) {
	html := jsContentWrap(
		`<figure><img data-src="https://mmbiz.qpic.cn/a.jpg"><figcaption>Figure 说明</figcaption></figure>` +
			`<img data-src="https://mmbiz.qpic.cn/b.jpg" aria-describedby="aria-cap">` +
			`<p id="aria-cap">ARIA 说明</p>` +
			`<img data-src="https://mmbiz.qpic.cn/c.jpg">` +
			`<p class="caption" style="font-size:12px;color:#888888;text-align:center">未绑定说明</p>`)
	captionCount := 0
	for _, decision := range extractArticleRoleDecisionsForTest(html) {
		if decision.Role != "cap" {
			continue
		}
		captionCount++
		if decision.CaptionProof == CaptionProofNone || decision.MediaID <= 0 || decision.CaptionScope <= 0 {
			t.Fatalf("cap decision lacks bounded media association: %+v", decision)
		}
	}
	if captionCount != 3 {
		t.Fatalf("caption count = %d, want two explicit captions plus one compatible legacy caption", captionCount)
	}
}

func TestAriaDescribedByCreatesExplicitCaptionBinding(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" aria-describedby="photo-caption"></p>` +
			`<section id="photo-caption"><p style="font-size:20px;text-align:left">图片的结构化说明</p></section>` +
			`<p style="font-size:12px;color:#888888;text-align:center">未绑定的后续正文</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	if b := findBlockByText(blocks, "结构化说明"); b == nil || b.K != "cap" {
		t.Fatalf("aria-describedby target must be cap, got %v; kinds=%v", b, findKinds(blocks))
	}
	if b := findBlockByText(blocks, "未绑定的后续正文"); b == nil || b.K != "txt" {
		t.Fatalf("caption binding must not escape the IDREF target, got %v; kinds=%v", b, findKinds(blocks))
	}
	d := findDecisionByText(extractArticleRoleDecisionsForTest(html), "结构化说明")
	if d == nil || d.CaptionProof != CaptionProofAriaDescribedBy || d.Reason != "aria-describedby" {
		t.Fatalf("aria-describedby caption must expose structural proof, got %+v", d)
	}
}

func TestLegacyCompatibilityCannotOverwriteExplicitCaptionBinding(t *testing.T) {
	html := jsContentWrap(
		`<img data-src="https://mmbiz.qpic.cn/a.jpg" aria-describedby="caption-a">` +
			`<img data-src="https://mmbiz.qpic.cn/b.jpg">` +
			`<p id="caption-a" style="font-size:12px;color:#888888;text-align:center;">图片 A 的明确说明</p>`)
	decision := findDecisionByText(extractArticleRoleDecisionsForTest(html), "明确说明")
	if decision == nil || decision.Role != "cap" || decision.CaptionProof != CaptionProofAriaDescribedBy ||
		decision.Reason != "aria-describedby" {
		t.Fatalf("legacy compatibility must not overwrite an explicit binding, got %+v", decision)
	}
}

func TestAmbiguousCaptionRelationshipFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		html string
		text string
	}{
		{
			name: "figure-with-multiple-media",
			html: `<figure><img data-src="https://mmbiz.qpic.cn/a.jpg"><img data-src="https://mmbiz.qpic.cn/b.jpg">` +
				`<figcaption>无法确定属于哪张图</figcaption></figure>`,
			text: "无法确定属于哪张图",
		},
		{
			name: "two-media-share-idref-target",
			html: `<img data-src="https://mmbiz.qpic.cn/a.jpg" aria-describedby="shared">` +
				`<img data-src="https://mmbiz.qpic.cn/b.jpg" aria-describedby="shared">` +
				`<p id="shared">共享说明目标</p>`,
			text: "共享说明目标",
		},
		{
			name: "duplicate-idref-target",
			html: `<img data-src="https://mmbiz.qpic.cn/a.jpg" aria-describedby="duplicate">` +
				`<p id="duplicate">重复目标一</p><p id="duplicate">重复目标二</p>`,
			text: "重复目标",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, blocks, _ := extractArticleBlocks(jsContentWrap(tc.html))
			for _, b := range blocks {
				if (strings.Contains(b.Text, tc.text) || strings.Contains(textFromRuns(b.Runs), tc.text)) && b.K == "cap" {
					t.Fatalf("ambiguous relationship must fail closed to txt; block=%+v kinds=%v", b, findKinds(blocks))
				}
			}
		})
	}
}

func TestCaptionBindingCannotCrossScopeOrMediaBoundary(t *testing.T) {
	html := jsContentWrap(
		`<figure><img data-src="https://mmbiz.qpic.cn/a.jpg"><figcaption><p>图片 A 的说明</p></figcaption></figure>` +
			`<p><img data-src="https://mmbiz.qpic.cn/b.jpg"></p>` +
			`<p>图片 B 后面的普通正文。</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	if b := findBlockByText(blocks, "图片 A 的说明"); b == nil || b.K != "cap" {
		t.Fatalf("explicit caption must stay bound to media A, got %v; kinds=%v", b, findKinds(blocks))
	}
	if b := findBlockByText(blocks, "图片 B 后面的普通正文"); b == nil || b.K != "txt" {
		t.Fatalf("caption binding must not cross scope or media boundary, got %v; kinds=%v", b, findKinds(blocks))
	}
}

// ---- Caption bindings: explicit proof + bounded legacy compatibility ----

func TestImageCaptionByGreyColor(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" data-w="750" /></p>` +
			`<p style="color: rgb(136, 136, 136); text-align: left;">小红书@喜太狼</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "小红书@喜太狼")
	if b == nil {
		t.Fatalf("caption text missing: kinds=%v", findKinds(blocks))
	}
	if b.K != "cap" {
		t.Errorf("grey caption must remain cap, got %s", b.K)
	}
}

func TestImageCaptionBySmallFont(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" /></p>` +
			`<p style="font-size: 12px;">图片说明：来源不详</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "图片说明")
	if b == nil || b.K != "cap" {
		t.Errorf("small-font post-image caption must remain cap, got %v", b)
	}
}

func TestImageCaptionByCenterShort(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" data-w="1080" data-h="720" data-ratio="0.667" /></p>` +
			`<p style="text-align: center;">来源：财新网</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "来源：财新网")
	if b == nil || b.K != "cap" {
		t.Errorf("center+short post-image caption must remain cap, got %v", b)
	}
}

func TestExplicitHeadingTagsPreserveFoodTalksLevels(t *testing.T) {
	html := jsContentWrap(
		`<h2>一级章节</h2>` +
			`<h3>二级章节</h3>` +
			`<h4>三级章节</h4>`)
	_, blocks, _ := extractArticleBlocks(html)

	want := map[string]int{
		"一级章节": 2,
		"二级章节": 3,
		"三级章节": 4,
	}
	for text, level := range want {
		block := findBlockByText(blocks, text)
		if block == nil || block.K != "h" || block.Level != level {
			t.Fatalf("%q heading = %+v, want level %d", text, block, level)
		}
	}
}

func TestArabicNumberedHeadingIsSiteSecondaryLevel(t *testing.T) {
	html := jsContentWrap(
		`<p style="font-size: 20px;"><strong>创业三大“地狱级”难题</strong></p>` +
			`<p style="font-size: 20px;"><strong>2、第二大难点：改变消费场景</strong></p>` +
			`<p>这是足够长的正文，用来建立正常正文流。</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	chapter := findBlockByText(blocks, "创业三大")
	if chapter == nil || chapter.K != "h" {
		t.Fatalf("无序号章节句应为标题，got %+v", chapter)
	}
	if chapter.Level != 0 && chapter.Level != 2 {
		t.Fatalf("无序号视觉标题 Level 应为 0 或 2，got %d", chapter.Level)
	}
	sub := findBlockByText(blocks, "第二大难点")
	if sub == nil || sub.K != "h" {
		t.Fatalf("阿拉伯数字小标题应为标题，got %+v; kinds=%v", sub, findKinds(blocks))
	}
	if sub.Level != 3 {
		t.Fatalf("「2、第二大难点」应为二级 Level=3，got %d", sub.Level)
	}
}

func TestExplicitH2ArabicNumberedHeadingDemotesToLevel3(t *testing.T) {
	html := jsContentWrap(`<h2>2、第二大难点：改变消费场景</h2><p>正文开始</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "第二大难点")
	if b == nil || b.K != "h" || b.Level != 3 {
		t.Fatalf("源 HTML 即使是 h2，阿拉伯数字序号也要记成 Level=3，got %+v", b)
	}
}

func TestImageCaptionGroupKeepsImageSourceAsCaption(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" /></p>` +
			`<p style="font-size: 12px; color: rgb(136, 136, 136); text-align: center;">星巴克高蛋白咖啡（美国市场）</p>` +
			`<p style="font-size: 12px; color: rgb(136, 136, 136); text-align: center;">图片来源：Facebook@Emily Velasco</p>` +
			`<p>正文开始</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	caption := findBlockByText(blocks, "星巴克高蛋白咖啡")
	if caption == nil || caption.K != "cap" {
		t.Fatalf("image caption must be cap, got %v; kinds=%v", caption, findKinds(blocks))
	}
	source := findBlockByText(blocks, "图片来源：Facebook")
	if source == nil || source.K != "cap" {
		t.Fatalf("image source following a caption must stay cap, got %v; kinds=%v", source, findKinds(blocks))
	}
	body := findBlockByText(blocks, "正文开始")
	if body == nil || body.K != "txt" {
		t.Fatalf("body after caption group must return to txt, got %v; kinds=%v", body, findKinds(blocks))
	}
}

// #280（2026-10-13）：截图下三行译文与图注同款（居中 12px 灰），其中两行以「！」结尾，
// 曾被学成正文样式，导致全文「图片→说明→图片来源」都落成正文。
func TestReported280CaptionStyledQuotesDoNotTeachBodyStyle(t *testing.T) {
	body := func(text string) string {
		return `<section style="text-align: left;margin-bottom: 20px;line-height: 1.75;"><span leaf=""><span textstyle="" style="font-size: 15px;color: rgb(84, 69, 69);">` + text + `</span></span></section>`
	}
	caption := func(text string) string {
		return `<section style="text-align: center;margin-top: 0;margin-bottom: 0;line-height: 1.75;"><span leaf=""><span textstyle="" style="font-size: 12px;color: rgb(136, 136, 136);">` + text + `</span></span></section>`
	}
	img := func(name string) string {
		return `<section style="line-height: 0;font-size: 0;text-align: center;" nodeleaf=""><img class="rich_pages wxw-img" data-src="https://mmbiz.qpic.cn/` + name + `.png" data-w="1080" data-ratio="0.75" /></section>`
	}
	html := jsContentWrap(
		body("就像鱼香肉丝里没有鱼、蟹棒里没有蟹一样，“西柚汁”也可以不用西柚了。") +
			img("a") + caption("不使用西柚的西柚味100%果汁") + caption("图片来源：可果美") +
			body("近日，日本企业可果美的Beyond系列就推出了一款“不使用西柚的西柚味100%果汁”，开发这款产品的直接原因，是受西柚供应不稳定的影响。") +
			body("比如X上一位网友就发帖表示，这款产品居然连西柚那一点点苦味都还原了，并且还让他纳闷苦味到底来自哪里。") +
			img("b") + caption("真的！是西柚味！明明没有使用西柚！") + caption("口中残留的味道也是西柚味——！！！") +
			caption("这种谜一般的还原度也太高了！清晰度简直像8K一样！") +
			body("除了“西柚汁”，可果美Beyond系列此前还推出了一款“不使用橙子的橙味100%果汁”，同样以黄胡萝卜为主要原料调配而成。") +
			img("c") + caption("不使用橙子的橙味100%果汁") + caption("图片来源：可果美") +
			body("跟“西柚汁”一样，“橙汁”同样以黄胡萝卜为主要原料，同时使用苹果、菠萝、柠檬、芒果等水果调配。"))
	_, blocks, _ := extractArticleBlocks(html)

	textOf := func(b Block) string {
		text := b.Text
		for _, r := range b.Runs {
			text += r.Text
		}
		return text
	}
	for _, want := range []string{"不使用西柚的西柚味100%果汁", "不使用橙子的橙味100%果汁", "真的！是西柚味！明明没有使用西柚！", "这种谜一般的还原度也太高了！清晰度简直像8K一样！"} {
		var got *Block
		for i := range blocks {
			if textOf(blocks[i]) == want {
				got = &blocks[i]
			}
		}
		if got == nil || got.K != "cap" {
			t.Fatalf("%q 必须是图注，got %v; kinds=%v", want, got, findKinds(blocks))
		}
	}
	sources := 0
	for i, b := range blocks {
		text := textOf(b)
		if !strings.HasPrefix(text, "图片来源") {
			continue
		}
		sources++
		if b.K != "cap" {
			t.Fatalf("第 %d 块图片来源必须是图注，got %v; kinds=%v", i, b, findKinds(blocks))
		}
	}
	if sources != 2 {
		t.Fatalf("应有 2 处图片来源，got %d; kinds=%v", sources, findKinds(blocks))
	}
	for _, needle := range []string{"就像鱼香肉丝", "近日，日本企业可果美", "比如X上一位网友", "除了“西柚汁”", "跟“西柚汁”一样"} {
		b := findBlockByText(blocks, needle)
		if b == nil || b.K != "txt" {
			t.Fatalf("%q 必须是正文，got %v; kinds=%v", needle, b, findKinds(blocks))
		}
	}
}

func TestLegacyMultiBlockCaptionDoesNotDefineBodyTypography(t *testing.T) {
	first := "新品包装组合展示"
	second := strings.Repeat("说明", 50)
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" data-w="1080" data-h="720" data-ratio="0.667" /></p>` +
			`<p style="font-size:12px;color:#888888;text-align:center;">` + first + `</p>` +
			`<p style="font-size:12px;color:#888888;text-align:center;">` + second + `</p>` +
			`<p style="font-size:16px;color:#333333;text-align:left;">正文继续介绍产品研发过程。</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	for _, needle := range []string{first, second} {
		if b := findBlockByText(blocks, needle); b == nil || b.K != "cap" {
			t.Fatalf("legacy multi-block caption must remain cap, got %v; kinds=%v", b, findKinds(blocks))
		}
	}
	if b := findBlockByText(blocks, "正文继续"); b == nil || b.K != "txt" {
		t.Fatalf("body after legacy caption must remain txt, got %v; kinds=%v", b, findKinds(blocks))
	}
}

func TestCaptionContinuationRequiresCaptionStyle(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" data-w="1080" data-h="720" data-ratio="0.667" /></p>` +
			`<p style="text-align: center;">图注</p>` +
			`<p style="text-align: center;">短正文</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	caption := findBlockByText(blocks, "图注")
	if caption == nil || caption.K != "cap" {
		t.Fatalf("first center-short post-image line must remain cap, got %v", caption)
	}
	body := findBlockByText(blocks, "短正文")
	if body == nil || body.K != "txt" {
		t.Fatalf("center-short text after a caption needs stronger caption styling, got %v; kinds=%v", body, findKinds(blocks))
	}
}

// 真实反馈文章 https://mp.weixin.qq.com/s/LmvhTcF2V7WVfY7xJTAhmQ 的页首结构：
// 横幅图后先出现作者/版权信息，再进入小字号正文。这些文本没有媒体绑定，因此无论
// 样式或邻接关系如何都必须保持 txt。
func TestReportedLmvhHeaderMetadataAndBodyRemainText(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/brandark-banner.jpg" data-w="1080" data-h="360" data-ratio="0.333"></p>` +
			`<p style="font-size:14px;color:#888888;text-align:left;">作者 | 十度</p>` +
			`<p style="font-size:14px;color:#888888;text-align:left;">声明 | 此文章版权归品牌方舟所有，未经允许不得转载</p>` +
			`<p style="font-size:14px;color:#555555;text-align:left;">如果把咖啡因加到一颗口香糖里，你会买吗？</p>` +
			`<p style="font-size:14px;color:#555555;text-align:left;">放在十年前，这听起来更像一个异想天开的点子。毕竟，提神有咖啡，补充营养有保健品，口香糖的作用似乎只有清新口气。</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	for _, needle := range []string{"作者 | 十度", "声明 | 此文章版权归", "如果把咖啡因", "放在十年前"} {
		b := findBlockByText(blocks, needle)
		if b == nil || b.K != "txt" {
			t.Fatalf("%q must remain txt after the header image, got %v; kinds=%v", needle, b, findKinds(blocks))
		}
	}
}

// 同一真实页首在上游剥离作者/声明后，横幅图会直接邻接疑问式导语。左对齐完整问句
// 仍是正文；不能因为它使用 14px 就把它当作图片说明。
func TestReportedLmvhQuestionLeadImmediatelyAfterImageIsBody(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/brandark-banner.jpg" data-w="1080" data-h="360" data-ratio="0.333"></p>` +
			`<p style="font-size:14px;color:#555555;text-align:left;">如果把咖啡因加到一颗口香糖里，你会买吗？</p>` +
			`<p style="font-size:14px;color:#555555;text-align:left;">放在十年前，这听起来更像一个异想天开的点子。</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	for _, needle := range []string{"如果把咖啡因", "放在十年前"} {
		b := findBlockByText(blocks, needle)
		if b == nil || b.K != "txt" {
			t.Fatalf("%q must remain txt immediately after the header image, got %v; kinds=%v", needle, b, findKinds(blocks))
		}
	}
}

func TestExplicitCaptionQuestionStaysCaption(t *testing.T) {
	html := jsContentWrap(
		`<figure><img data-src="https://mmbiz.qpic.cn/a.jpg" data-w="1080" data-h="720" data-ratio="0.667">` +
			`<figcaption><p style="font-size:12px;color:#888888;text-align:center;">你知道这款包装来自哪里吗？</p></figcaption></figure>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "包装来自哪里")
	if b == nil || b.K != "cap" {
		t.Fatalf("centered styled question after image must stay cap, got %v; kinds=%v", b, findKinds(blocks))
	}
}

// 同一真实文章的正文结构：旧版可识别的「图源：Neuro」仍必须是图注；正文沿用
// 14px 深灰排版时，又不能被图注状态继续拖进去。
// 第二个 case 改掉原文、包装层级、声明顺序和 px/pt 写法，守住结构关系而非样本文本。
func TestReportedLmvhStyledBodyAfterCaptionReturnsToBodyFlow(t *testing.T) {
	cases := []struct {
		name     string
		bodyHTML string
		needles  []string
	}{
		{
			name: "reported-text",
			bodyHTML: `<p style="font-size:14px;color:#555555;text-align:left;">两人不由想到，如果把咖啡因放进一片口香糖里，会怎样？</p>` +
				`<p style="font-size:14px;color:#555555;text-align:left;">Kent拥有神经科学背景，Ryan主修化学，两人开始一遍遍调试配方，将天然咖啡因与茶氨酸结合。</p>`,
			needles: []string{"两人不由想到", "Kent拥有神经科学背景"},
		},
		{
			name: "wrapper-style-and-text-variant",
			bodyHTML: `<section style="color:rgb(85,85,85);font-size:10.5pt;">` +
				`<p style="text-align:left;"><span>团队提出了另一个问题：把有效成分放进糖果里，会发生什么？</span></p>` +
				`<p style="text-align:left;"><span>他们随后反复调整配方，并记录每一次测试结果。</span></p>` +
				`</section>`,
			needles: []string{"团队提出了另一个问题", "他们随后反复调整配方"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			html := jsContentWrap(
				`<p><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/neuro.jpg" data-w="1080" data-h="720" data-ratio="0.667"></p>` +
					`<p style="font-size:12px;color:#888888;text-align:center;">图源：Neuro</p>` +
					tc.bodyHTML)
			_, blocks, _ := extractArticleBlocks(html)

			caption := findBlockByText(blocks, "图源：Neuro")
			if caption == nil || caption.K != "cap" {
				t.Fatalf("real source caption must stay cap, got %v; kinds=%v", caption, findKinds(blocks))
			}
			for _, needle := range tc.needles {
				b := findBlockByText(blocks, needle)
				if b == nil || b.K != "txt" {
					t.Fatalf("%q must return to body flow after caption, got %v; kinds=%v", needle, b, findKinds(blocks))
				}
			}
		})
	}
}

func TestCaptionBindingEndsAtExplicitScope(t *testing.T) {
	html := jsContentWrap(
		`<figure><img data-src="https://mmbiz.qpic.cn/a.jpg" data-w="1080" data-h="720" data-ratio="0.667" />` +
			`<figcaption><p style="text-align: center;">图注</p></figcaption></figure>` +
			`<p style="text-align: center;">短正文</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	caption := findBlockByText(blocks, "图注")
	if caption == nil || caption.K != "cap" {
		t.Fatalf("explicit figcaption must be cap, got %v", caption)
	}
	body := findBlockByText(blocks, "短正文")
	if body == nil || body.K != "txt" {
		t.Fatalf("text outside the explicit caption scope must be txt, got %v; kinds=%v", body, findKinds(blocks))
	}
}

func TestCaptionSingleBlockCharacterLimit(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		style string
	}{
		{
			name: "source-prefix-cannot-bypass-base-limit",
			text: "图片来源：" + strings.Repeat("源", defaultImageCaptionMaxChars),
		},
		{
			name:  "strong-style-cannot-bypass-strong-limit",
			text:  strings.Repeat("图", defaultImageCaptionStrongMaxChars+1),
			style: ` style="font-size:12px;color:#888888;text-align:center;"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			html := jsContentWrap(
				`<figure><img data-src="https://mmbiz.qpic.cn/a.jpg" data-w="1080" data-h="720" data-ratio="0.667">` +
					`<figcaption><p` + tc.style + `>` + tc.text + `</p></figcaption></figure>`)
			_, blocks, _ := extractArticleBlocks(html)
			if b := findBlockByText(blocks, tc.text); b == nil || b.K != "txt" {
				t.Fatalf("over-limit caption candidate must be txt, got %v; kinds=%v", b, findKinds(blocks))
			}
		})
	}
}

func TestCaptionGroupCumulativeCharacterLimit(t *testing.T) {
	t.Run("plain-caption-group-uses-base-limit", func(t *testing.T) {
		first := strings.Repeat("图", 30)
		second := strings.Repeat("注", 30)
		overflow := strings.Repeat("文", 30)
		afterOverflow := strings.Repeat("续", 10)
		html := jsContentWrap(
			`<figure><img data-src="https://mmbiz.qpic.cn/a.jpg" data-w="1080" data-h="720" data-ratio="0.667"><figcaption>` +
				`<p style="text-align:center;">` + first + `</p>` +
				`<p class="caption">` + second + `</p>` +
				`<p class="caption">` + overflow + `</p>` +
				`<p class="caption">` + afterOverflow + `</p></figcaption></figure>`)
		_, blocks, _ := extractArticleBlocks(html)

		for _, needle := range []string{first, second} {
			if b := findBlockByText(blocks, needle); b == nil || b.K != "cap" {
				t.Fatalf("caption text within %d-char group limit must stay cap, got %v; kinds=%v", defaultImageCaptionMaxChars, b, findKinds(blocks))
			}
		}
		for _, needle := range []string{overflow, afterOverflow} {
			if b := findBlockByText(blocks, needle); b == nil || b.K != "txt" {
				t.Fatalf("text after cumulative %d-char group limit must be txt, got %v; kinds=%v", defaultImageCaptionMaxChars, b, findKinds(blocks))
			}
		}
	})

	t.Run("strong-caption-group-uses-strong-limit", func(t *testing.T) {
		first := strings.Repeat("长", 120)
		source := "图片来源：" + strings.Repeat("源", 20)
		overflow := strings.Repeat("文", 70)
		html := jsContentWrap(
			`<figure><img data-src="https://mmbiz.qpic.cn/a.jpg"><figcaption>` +
				`<p style="font-size:12px;color:#888888;text-align:center;">` + first + `</p>` +
				`<p style="font-size:12px;color:#888888;text-align:center;">` + source + `</p>` +
				`<p class="caption" style="font-size:12px;color:#888888;text-align:center;">` + overflow + `</p></figcaption></figure>`)
		_, blocks, _ := extractArticleBlocks(html)

		for _, needle := range []string{first, source} {
			if b := findBlockByText(blocks, needle); b == nil || b.K != "cap" {
				t.Fatalf("strong caption text within %d-char group limit must stay cap, got %v; kinds=%v", defaultImageCaptionStrongMaxChars, b, findKinds(blocks))
			}
		}
		if b := findBlockByText(blocks, overflow); b == nil || b.K != "txt" {
			t.Fatalf("text after cumulative %d-char strong group limit must be txt, got %v; kinds=%v", defaultImageCaptionStrongMaxChars, b, findKinds(blocks))
		}
	})
}

func TestCaptionCenterThresholdRemainsCompatible(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" data-w="1080" data-h="720" data-ratio="0.667" /></p>` +
			`<p style="text-align: center;">abcdef</p>`)
	_, defaultBlocks, _ := extractArticleBlocks(html)
	if b := findBlockByText(defaultBlocks, "abcdef"); b == nil || b.K != "cap" {
		t.Fatalf("default center-short threshold should keep legacy caption behavior, got %v; kinds=%v", b, findKinds(defaultBlocks))
	}

	_, strictBlocks, _ := extractArticleBlocksWithOptions(html, articleBlockOptions{
		CaptionMaxChars:       80,
		CaptionCenterMaxChars: 3,
	})
	if b := findBlockByText(strictBlocks, "abcdef"); b == nil || b.K != "txt" {
		t.Fatalf("configured center threshold should keep longer centered text as body, got %v; kinds=%v", b, findKinds(strictBlocks))
	}
}

// 历史公众号中只靠图后小字/灰色排版表达的说明性长图注也必须兼容。
// 复刻真实样例：
// mp.weixin.qq.com/s/8xVXVFar3fbJEEQKHSwy7w 那段「左前方为…后排三款为…」(~123 字)。
func TestLongStyledCaptionStaysCaption(t *testing.T) {
	longCaption := "左前方为Classic Collection系列的Paris（巴黎红茶），后排三款为HT系列：" +
		"Vanilla Comoro（香草科摩罗）、Green Tea with Coconut（椰子绿茶）和Peppermint Herbal（薄荷草本茶）"
	if n := len([]rune(longCaption)); n <= defaultImageCaptionMaxChars {
		t.Fatalf("样例图注应超过基础上限以验证回归，实际 %d 字", n)
	}
	// WeChat 把图注的字号/颜色放在内层 <span> 上。
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" /></p>` +
			`<p style="text-align: center;"><span style="font-size: 14px; color: #888888;">` +
			longCaption + `</span></p>`)
	_, blocks, _ := extractArticleBlocks(html)

	cap := findBlockByText(blocks, "Peppermint Herbal")
	if cap == nil || cap.K != "cap" {
		t.Fatalf("旧版可识别的长图注必须继续是 cap，got %v; kinds=%v", cap, findKinds(blocks))
	}
}

func TestReported62915LongStyledSentenceCaptionStaysCaption(t *testing.T) {
	caption := "宇治川与两岸的山间茶园。镰仓时代明惠上人在此开辟茶园，室町将军足利义满又拓出七座茶园，" +
		"宇治茶八百年的历史就从这片山水开始。伊藤久右卫门的本店就在这条河边，紧邻国宝平等院。"
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/uji.jpg" data-w="1080" data-h="720" data-ratio="0.667" /></p>` +
			`<p style="text-align: center;"><span style="font-size: 12px; color: #7f7f7f;">` + caption + `</span></p>` +
			`<p>日本茶叶甜品三部曲写到第三篇，可以做个横向比较了。</p>`)
	decisions := extractArticleRoleDecisionsForTest(html)

	d := findDecisionByText(decisions, "宇治川与两岸")
	if d == nil || d.Role != "cap" || d.CaptionProof != CaptionProofLegacyAssociation ||
		d.MediaID <= 0 || d.CaptionScope <= 0 {
		t.Fatalf("62915 的小字灰色完整句图注必须与图片绑定，got %+v", d)
	}
	body := findDecisionByText(decisions, "日本茶叶甜品三部曲")
	if body == nil || body.Role != "txt" {
		t.Fatalf("图注后的正文不得继承图注角色，got %+v", body)
	}
}

// 防过度放宽：图片后的长【普通正文】段即使超过基础上限，也必须保持正文；
// 视觉上限选择不能为未绑定文本创建图注资格。
func TestLongPlainParagraphAfterImageStaysBody(t *testing.T) {
	longBody := "这是一段紧跟在图片后面的普通正文，它既不是小字号也不是灰色，只是正常黑色正文，" +
		"长度同样超过了八十个字符的基础上限，按规则必须保持为正文，绝不能被误判成居中灰色小字图注。"
	if n := len([]rune(longBody)); n <= defaultImageCaptionMaxChars {
		t.Fatalf("样例正文应超过基础上限以验证防回归，实际 %d 字", n)
	}
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" /></p>` +
			`<p style="font-size: 16px;">` + longBody + `</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	b := findBlockByText(blocks, "普通正文")
	if b == nil || b.K != "txt" {
		t.Fatalf("图后长普通正文必须保持 txt，got %v; kinds=%v", b, findKinds(blocks))
	}
}

func TestArticleBlockOptionsReadActiveAccountBehavior(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	preset := rulePreset{
		ID:         "site-tight-caption",
		Name:       "网站短图注阈值",
		BaselineID: foodTalksSiteDefaultRulePresetID,
		Config:     defaultFoodTalksSiteRuleConfig(),
		Behavior: map[string]any{
			"params": map[string]any{
				"image_caption_max_chars":        map[string]any{"type": "number", "default": 20},
				"image_caption_center_max_chars": map[string]any{"type": "number", "default": 6},
			},
		},
	}
	if _, err := store.upsert(preset); err != nil {
		t.Fatalf("upsert custom preset: %v", err)
	}
	if _, err := store.setActiveForAccount(foodTalksSiteRuleAccountID, preset.ID); err != nil {
		t.Fatalf("set active: %v", err)
	}

	opts := articleBlockOptionsForAccount(store, foodTalksSiteRuleAccountID)
	if opts.CaptionMaxChars != 20 || opts.CaptionCenterMaxChars != 6 {
		t.Fatalf("options = %#v, want behavior params 20/6", opts)
	}
}

// Real WeChat pattern: <p> holds both an image and trailing styled text.
func TestCaptionInSameParagraphAsImage(t *testing.T) {
	html := jsContentWrap(
		`<p>` +
			`<img data-src="https://mmbiz.qpic.cn/a.jpg" data-w="1080" />` +
			`<span style="font-size: 12px;color: rgb(136, 136, 136);">` +
			`<span leaf="">小红书@喜太狼</span>` +
			`</span></p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "小红书@喜太狼")
	if b == nil {
		t.Fatalf("missing caption; kinds=%v", findKinds(blocks))
	}
	if b.K != "cap" {
		t.Errorf("caption inside same <p> as <img> must remain cap, got %s", b.K)
	}
}

func TestNonImageAdjacentGreyIsNotCaption(t *testing.T) {
	html := jsContentWrap(`<p style="color: #888;">这是一段灰色的正文。</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "灰色的正文")
	if b == nil || b.K != "txt" {
		t.Errorf("orphan grey paragraph must stay txt, got %v", b)
	}
}

// ---- Heading paths ----

func TestExplicitH2IsHeading(t *testing.T) {
	html := jsContentWrap(`<h2>第一节 奶茶排队现象</h2><p>正文开始</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "奶茶排队现象")
	if b == nil || b.K != "h" {
		t.Errorf("<h2> must be heading, got %v", b)
	}
}

func TestLargeBoldFontIsHeading(t *testing.T) {
	html := jsContentWrap(`<p style="font-size: 20px;"><strong>01 | 奶茶排队现象</strong></p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "01")
	if b == nil || b.K != "h" {
		t.Errorf("20px + bold → heading, got %v", b)
	}
}

func TestInnerSpanLargeBoldFontIsHeading(t *testing.T) {
	html := jsContentWrap(
		`<p><span textstyle="" style="font-size: 20px; font-weight: bold;">最听劝的生鲜电商平台</span></p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "最听劝的生鲜电商平台")
	if b == nil || b.K != "h" {
		t.Errorf("inner span 20px + bold → heading, got %v", b)
	}
}

func TestWechatNumberTitleHeadingsStaySeparateAndLongBoldBodyStaysTxt(t *testing.T) {
	longBody := strings.Repeat("这是一段很长的加粗正文，", 12)
	if n := len([]rune(longBody)); n <= 80 {
		t.Fatalf("样例正文应超过标题上限以验证防回归，实际 %d 字", n)
	}
	html := jsContentWrap(
		`<p><span textstyle="" style="font-size: 20px; font-weight: bold;">01</span></p>` +
			`<p><span textstyle="" style="font-size: 20px; font-weight: bold;">最听劝的生鲜电商平台</span></p>` +
			`<p><span textstyle="" style="font-size: 20px; font-weight: bold;">` + longBody + `</span></p>`)
	_, blocks, _ := extractArticleBlocks(html)

	hTexts := []string{}
	for _, b := range blocks {
		if b.K == "h" {
			hTexts = append(hTexts, b.Text)
		}
	}
	if len(hTexts) != 2 || hTexts[0] != "01" || hTexts[1] != "最听劝的生鲜电商平台" {
		t.Fatalf("number and title should stay as two heading blocks, got %q; kinds=%v", hTexts, findKinds(blocks))
	}
	body := findBlockByText(blocks, "加粗正文")
	if body == nil || body.K != "txt" {
		t.Fatalf("long bold body must stay txt, got %v; kinds=%v", body, findKinds(blocks))
	}
}

func TestWechatVisualHeadingLinesWithZeroBottomMarginAreJoined(t *testing.T) {
	html := jsContentWrap(
		`<section style="text-align:center;margin-bottom:0;line-height:2em">` +
			`<span style="font-size:20px;font-weight:bold">六线并进</span></section>` +
			`<section style="text-align:center;margin-bottom:24px;line-height:2em">` +
			`<span style="font-size:20px;font-weight:bold">一个标准仓的收支账</span></section>` +
			`<p>这是足够长的正文内容，用来建立正常正文流并结束标题分组。</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	var headings []string
	for _, block := range blocks {
		if block.K == "h" {
			headings = append(headings, block.Text)
		}
	}
	if len(headings) != 1 || headings[0] != "六线并进，一个标准仓的收支账" {
		t.Fatalf("two visual lines should become one heading, got %q; kinds=%v", headings, findKinds(blocks))
	}
}

func TestWechatVisualHeadingContinuationStartingWithConjunctionNeedsNoComma(t *testing.T) {
	html := jsContentWrap(
		`<section style="text-align:center;margin-bottom:0"><strong><span style="font-size:20px">窗口期、点位战</span></strong></section>` +
			`<section style="text-align:center;margin-bottom:24px"><strong><span style="font-size:20px">与结构性机会</span></strong></section>` +
			`<p>这是足够长的正文内容，用来建立正常正文流并结束标题分组。</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	var headings []string
	for _, block := range blocks {
		if block.K == "h" {
			headings = append(headings, block.Text)
		}
	}
	if len(headings) != 1 || headings[0] != "窗口期、点位战与结构性机会" {
		t.Fatalf("conjunction continuation should join directly, got %q; kinds=%v", headings, findKinds(blocks))
	}
}

// #128/#146/#147（2026-10-15）：左对齐标题同样会拆成上下两个 section（上一行
// margin-bottom:0），编辑都手工合成一行。行尾已有标点、以「的」收尾的直接接上，
// 三行以上整句接上，其余两行补逗号；对齐方式不同的两行不合并。
func TestWechatLeftAlignedHeadingLinesAreJoined(t *testing.T) {
	line := func(text, marginBottom string) string {
		return `<section style="text-align: left;margin-bottom: ` + marginBottom + `;line-height: 1.75;">` +
			`<span leaf=""><span style="font-size: 18px;font-weight: bold;" textstyle="">` + text + `</span></span></section>`
	}
	body := `<p>这是足够长的正文内容，用来建立正常正文流并结束标题分组。</p>`
	html := jsContentWrap(
		line("嘴上要健康，", "0") + line("手却伸向全糖", "20px") + body +
			line("逃不出的", "0") + line("“周末限定款”？", "20px") + body +
			line("进入中国近30年", "0") + line("从合资扩张到股权变局", "20px") + body +
			line("品牌资产：用口号", "0") + line("背书、故事和视觉建立", "0") + line("高端化认知", "20px") + body +
			line("一切业务", "0") + line("都在向合伙人收敛", "20px") + body +
			line("左边一行", "0") +
			`<section style="text-align: center;margin-bottom: 20px;"><span style="font-size: 18px;font-weight: bold;">居中一行</span></section>` + body)
	_, blocks, _ := extractArticleBlocks(html)
	var headings []string
	for _, block := range blocks {
		if block.K == "h" {
			headings = append(headings, block.Text)
		}
	}
	want := []string{
		"嘴上要健康，手却伸向全糖",
		"逃不出的“周末限定款”？",
		"进入中国近30年，从合资扩张到股权变局",
		"品牌资产：用口号背书、故事和视觉建立高端化认知",
		"一切业务都在向合伙人收敛",
		"左边一行",
		"居中一行",
	}
	if strings.Join(headings, "|") != strings.Join(want, "|") {
		t.Fatalf("headings = %q, want %q", headings, want)
	}
}

func TestSiteSecondaryHeadingTextSkipsDecimals(t *testing.T) {
	for text, want := range map[string]bool{
		"1、第一大难点：生产成本":          true,
		"2. 名字好记":               true,
		"1）不追求拓展品类":             true,
		"（一）背景":                 true,
		"1.8万家店，CU靠什么让消费者反复进店？": false,
		"3.9元的成本藏在哪里？":          false,
		"3. 108种茶，太多了":          true,
		"a、站在同一起跑线上":            false,
		"一、创业者杜国楹":              false,
	} {
		if got := isSiteSecondaryHeadingText(text); got != want {
			t.Errorf("isSiteSecondaryHeadingText(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestAdjacentExplicitHeadingsAreNeverJoined(t *testing.T) {
	html := jsContentWrap(`<h2 style="margin-bottom:0">一级标题</h2><h3>二级标题</h3><p>正文内容。</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	var headings []string
	for _, block := range blocks {
		if block.K == "h" {
			headings = append(headings, block.Text)
		}
	}
	if len(headings) != 2 {
		t.Fatalf("semantic headings must stay separate, got %q; kinds=%v", headings, findKinds(blocks))
	}
}

func TestFoodTalksNativeImageDimensionsFallBackToWidthHeight(t *testing.T) {
	html := jsContentWrap(
		`<figure class="image"><img class="mc-img" src="https://static.foodtalks.cn/a.jpg" width="918" height="593">` +
			`<figcaption><p>图片说明</p></figcaption></figure>`)
	_, blocks, _ := extractArticleBlocks(html)

	var image *Block
	for i := range blocks {
		if blocks[i].K == "img" {
			image = &blocks[i]
			break
		}
	}
	if image == nil {
		t.Fatalf("native FoodTalks image missing; kinds=%v", findKinds(blocks))
	}
	if image.DataW != "918" || image.DataH != "593" {
		t.Fatalf("native width/height must survive extraction, got dataW=%q dataH=%q", image.DataW, image.DataH)
	}
}

func TestLargeNonBoldFontIsNotHeading(t *testing.T) {
	// Font-size alone doesn't promote — avoids treating every decorative
	// oversized callout as a heading.
	html := jsContentWrap(`<p style="font-size: 22px;">大字但没加粗</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "大字但没加粗")
	if b == nil {
		t.Fatalf("missing block")
	}
	if b.K == "h" {
		t.Errorf("large-font non-bold must stay txt, got heading")
	}
}

// 真实微信小标题：夹在两张图之间、20px（>正文17px）、居中，外层 <b> 被 leaf<span> 的
// font-weight:normal 覆盖 → 视觉非粗、allBold=false。靠 BoldTag（<b>/<strong> 标签
// 痕迹，不被内联 normal 抹掉）判为标题，而非靠泛化的「居中」（居中会误伤图注/导语/来源）。
// 病样：https://mp.weixin.qq.com/s/swJQCw5THd0gJVF2GOoMIQ
func TestCenteredLargeNonBoldTitleBetweenImagesIsHeading(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://x/a.png" data-w="1080"></p>` +
			`<p style="text-align:center;">` +
			`<b><font face="汉仪劲楷简">` +
			`<span leaf="" style="color:rgba(0,0,0,0.9);font-size:17px;font-weight:normal;">` +
			`<span textstyle="" style="font-size:20px;">产品美好：当地的、应季的、有良知的</span>` +
			`</span></font></b></p>` +
			`<p><img data-src="https://x/b.png" data-w="1080"></p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "产品美好")
	if b == nil {
		t.Fatalf("missing title block; kinds=%v", findKinds(blocks))
	}
	if b.K != "h" {
		t.Errorf("<b> 包裹的 20px 居中标题应为 heading，got %s; kinds=%v", b.K, findKinds(blocks))
	}
}

// 同篇真实文章的第一个章节标题没有 <b> 包裹，但仍是居中 20px 的「冒号标题」。
func TestCenteredLargeTitleLikeWithoutBoldTagIsHeading(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://x/a.png" data-w="1080"></p>` +
			`<p style="text-align:center;">` +
			`<span leaf="" style="color:rgba(0,0,0,0.9);font-size:17px;font-weight:normal;">` +
			`<span textstyle="" style="font-size:20px;">文化美好：求真，行善，分享美</span>` +
			`</span></p>` +
			`<p><img data-src="https://x/b.png" data-w="1080"></p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "文化美好")
	if b == nil {
		t.Fatalf("missing title block; kinds=%v", findKinds(blocks))
	}
	if b.K != "h" {
		t.Errorf("无 <b> 但标题式的 20px 居中章节应为 heading，got %s; kinds=%v", b.K, findKinds(blocks))
	}
}

// ---- 居中大字号章节标题：不依赖 <b> / 冒号这两个不可靠的巧合信号 ----

// 真实回归样例：https://mp.weixin.qq.com/s/nGe2BIRRWeItV1p3O6wxOQ 的「02」章节标题。
// 与「01」标题视觉完全一致（居中、20px>正文17px、深色 rgba(0,0,0,.9)、短），
// 唯一区别是作者漏写了外层 <b>，也没有冒号。旧判据要求「run 全在 <b> 下」或「含冒号」，
// 两条都是别篇样例的巧合属性，导致这个真标题被降级成正文。字号相对正文放大 + 居中 +
// 非图注/来源/参考头，才是微信章节标题的第一性信号。
func TestCenteredLargeTitleWithoutBoldNorColonIsHeading(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://x/a.png" data-w="1080"></p>` +
			`<p style="text-align: center;">` +
			`<span leaf="" style="color: rgba(0, 0, 0, 0.9);font-size: 17px;font-weight: normal;">` +
			`<span textstyle="" style="font-size: 20px;">四大品牌差异化竞争策略拆解</span>` +
			`</span></p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "四大品牌差异化竞争策略拆解")
	if b == nil {
		t.Fatalf("missing title block; kinds=%v", findKinds(blocks))
	}
	if b.K != "h" {
		t.Errorf("居中 20px 无 <b> 无冒号的真章节标题应为 heading，got %s; kinds=%v", b.K, findKinds(blocks))
	}
}

// 居中大字号的「图片来源：」行不能被抢成标题，也不能掉成正文。
func TestLargeCenteredImageSourceNotHeading(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://x/a.png" data-w="1080"></p>` +
			`<p style="text-align:center;"><span style="font-size:20px;">图片来源：Facebook</span></p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "图片来源")
	if b == nil {
		t.Fatalf("missing block; kinds=%v", findKinds(blocks))
	}
	if b.K == "h" {
		t.Errorf("图片来源行不应判 heading，got h; kinds=%v", findKinds(blocks))
	}
	if b.K != "cap" {
		t.Errorf("图片来源行应保留为 cap，got %s; kinds=%v", b.K, findKinds(blocks))
	}
}

// 居中大字号的「参考来源：」头不能被抢成标题——否则 inRefSection 不开启，后续条目错乱。
func TestLargeCenteredRefHeadNotStolenAsHeading(t *testing.T) {
	html := jsContentWrap(
		`<p style="text-align:center;"><span style="font-size:20px;">参考来源：</span></p>` +
			`<p>[1] 某条目</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	head := findBlockByText(blocks, "参考来源")
	item := findBlockByText(blocks, "某条目")
	if head == nil || item == nil {
		t.Fatalf("missing blocks; kinds=%v", findKinds(blocks))
	}
	if head.K == "h" {
		t.Errorf("参考来源头不应判 heading，got h; kinds=%v", findKinds(blocks))
	}
	if item.K != "txt" {
		t.Errorf("参考条目应在 refSection 内走 txt，got %s; kinds=%v", item.K, findKinds(blocks))
	}
}

// 左对齐正文里内嵌一个居中的空白 <span>(&nbsp;) 不应污染判定把正文升级为标题。
func TestBlankCenterSpanDoesNotPromoteToHeading(t *testing.T) {
	html := jsContentWrap(
		`<p style="font-size:18px;text-align:left;">纯左对齐正文没有任何居中意图` +
			`<span style="text-align:center;">&nbsp;</span></p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "纯左对齐正文")
	if b == nil {
		t.Fatalf("missing block; kinds=%v", findKinds(blocks))
	}
	if b.K == "h" {
		t.Errorf("含居中空白 span 的左对齐正文不应判 heading，got h; kinds=%v", findKinds(blocks))
	}
}

func TestLongBoldLineIsNotHeading(t *testing.T) {
	long := strings.Repeat("很长的一段加粗的话，", 10)
	html := jsContentWrap(`<p style="font-size: 20px;"><strong>` + long + `</strong></p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "很长的一段")
	if b == nil {
		t.Fatalf("missing block")
	}
	if b.K == "h" {
		t.Errorf("long bold paragraph must not be heading")
	}
}

// pt 精度回归守护（Codex 对抗测试挖出）：13.5pt 精确等于 18px，换算不能用截断的
// 1.333（会得 17.9955，漏在 ≥18 标题阈值外）。居中 13.5pt 章节标题必须判 h。
func TestPtFontSizeHeadingPrecision(t *testing.T) {
	html := jsContentWrap(
		`<p style="text-align:center;font-size:13.5pt;">` +
			`<span leaf="" style="color:rgba(0,0,0,0.9);font-weight:normal;">` +
			`新鲜零食四强品牌共性特征</span></p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "新鲜零食四强")
	if b == nil {
		t.Fatalf("missing title block; kinds=%v", findKinds(blocks))
	}
	if b.K != "h" {
		t.Errorf("13.5pt(=18px) 居中标题应为 h，got %s; kinds=%v", b.K, findKinds(blocks))
	}
}

// 句号结尾否决回归守护（Codex 对抗测试挖出）：居中大字若是「完整句子」（句号/逗号收尾），
// 是金句/导语/强调整句，不是章节标题，不升级为 h；同一形态但短语式收尾（无句末标点）的
// 真标题仍判 h。放行 ？！——疑问/感叹式标题真实存在。
func TestCenteredLargeSentenceEndingIsNotHeadingButLabelIs(t *testing.T) {
	// 金句：句号 + 闭引号收尾 → 不是标题
	quoteHTML := jsContentWrap(
		`<p style="text-align:center;">` +
			`<span leaf="" style="color:rgba(0,0,0,0.9);font-size:20px;font-weight:normal;">` +
			`“好吃不是结果，而是一套持续被验证的能力。”</span></p>`)
	_, qb, _ := extractArticleBlocks(quoteHTML)
	if b := findBlockByText(qb, "好吃不是结果"); b == nil || b.K == "h" {
		t.Errorf("句号收尾的居中金句不应判 h，got %v; kinds=%v", b, findKinds(qb))
	}

	// 短语式真标题：无句末标点 → 仍是标题
	labelHTML := jsContentWrap(
		`<p style="text-align:center;">` +
			`<span leaf="" style="color:rgba(0,0,0,0.9);font-size:20px;font-weight:normal;">` +
			`四大品牌差异化竞争策略拆解</span></p>`)
	_, lb, _ := extractArticleBlocks(labelHTML)
	if b := findBlockByText(lb, "四大品牌"); b == nil || b.K != "h" {
		t.Errorf("无句末标点的短语式居中标题应为 h，got %v; kinds=%v", b, findKinds(lb))
	}

	// 疑问式标题：？收尾放行 → 仍是标题（不因像句子而被否决）
	qTitleHTML := jsContentWrap(
		`<p style="text-align:center;">` +
			`<span leaf="" style="color:rgba(0,0,0,0.9);font-size:20px;font-weight:normal;">` +
			`新鲜零食还能火多久？</span></p>`)
	_, qtb, _ := extractArticleBlocks(qTitleHTML)
	if b := findBlockByText(qtb, "还能火多久"); b == nil || b.K != "h" {
		t.Errorf("问号收尾的疑问式标题应为 h，got %v; kinds=%v", b, findKinds(qtb))
	}
}

// ---- Reported WeChat URL structures: classify by role relationship, not local style only ----

func reportedGtyfMarkerFixture(titleText string) string {
	return jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/content-photo.jpg" data-w="1080" data-h="720" data-ratio="0.667"></p>` +
			`<p style="font-size:12px;color:#888888;text-align:center;">聚慧食品服务的部分餐饮客户</p>` +
			`<p>比起火锅底料供应商，他们更熟悉海底捞、老乡鸡、蒙自源这类连锁品牌。</p>` +
			`<p style="text-align:center;"><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_png/marker-01.png" data-w="312" data-h="84" data-ratio="0.269"></p>` +
			`<p style="text-align:center;"><span style="font-size:16px;color:#8fa5c6;">` + titleText + `</span></p>` +
			`<p><strong>但却是餐饮界最熟悉的“陌生人”</strong></p>` +
			`<p>事实上，聚慧食品既不是“小透明”，也不是行业无人问津的供应商。</p>`)
}

func reportedKatP22Fixture(titleText string) string {
	return jsContentWrap(
		`<p>不是做奶茶，做的还是奶茶里面属于小众品类的泰式奶茶。</p>` +
			`<p><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/thaitea-store.jpg" data-w="1080" data-h="720" data-ratio="0.667"></p>` +
			`<p style="font-size:12px;color:#888888;text-align:center;">图源大众点评</p>` +
			`<p>在一条连锁加盟九死一生已经是共识的赛道上，泰柯茶园能不能持续证明这不是赶上了某个窗口期的幸运？</p>` +
			`<section style="text-align:center;"><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_png/thaitea-divider.png" data-w="420" data-h="100" data-ratio="0.238"></section>` +
			`<section><p style="color:#3a9799;text-align:center;font-size:16px;">` + titleText + `</p></section>` +
			`<p>其实，泰式奶茶已经火过两次了。</p>`)
}

func TestReportedGtyfNumberMarkerTitleIsHeading(t *testing.T) {
	_, blocks, _ := extractArticleBlocks(reportedGtyfMarkerFixture("年收入超过11亿元"))
	requireKinds(t, blocks, []string{"img", "cap", "txt", "img", "h", "h", "txt"})

	title := findBlockByText(blocks, "年收入超过11亿元")
	if title == nil {
		t.Fatalf("missing reported marker title; kinds=%v", findKinds(blocks))
	}
	if title.K != "h" {
		t.Fatalf("number marker title must be h, got %s; kinds=%v", title.K, findKinds(blocks))
	}
	continuation := findBlockByText(blocks, "但却是餐饮界最熟悉")
	if continuation == nil || continuation.K != "h" {
		t.Fatalf("marker title continuation must remain part of heading group, got %v; kinds=%v", continuation, findKinds(blocks))
	}

	body := findBlockByText(blocks, "聚慧食品既不是")
	if body == nil || body.K != "txt" {
		t.Fatalf("body paragraph after marker title must remain txt, got %v; kinds=%v", body, findKinds(blocks))
	}
}

func TestReportedKatP22CaptionAndLaterSectionTitleCoexist(t *testing.T) {
	_, blocks, _ := extractArticleBlocks(reportedKatP22Fixture("重生之泰奶又又又火了"))
	requireKinds(t, blocks, []string{"txt", "img", "cap", "txt", "img", "h", "txt"})

	caption := findBlockByText(blocks, "图源大众点评")
	if caption == nil || caption.K != "cap" {
		t.Fatalf("content-image source must stay cap, got %v; kinds=%v", caption, findKinds(blocks))
	}
	title := findBlockByText(blocks, "重生之泰奶又又又火了")
	if title == nil {
		t.Fatalf("missing reported section title; kinds=%v", findKinds(blocks))
	}
	if title.K != "h" {
		t.Fatalf("decorative marker section title must be h, got %s; kinds=%v", title.K, findKinds(blocks))
	}
	body := findBlockByText(blocks, "泰式奶茶已经火过")
	if body == nil || body.K != "txt" {
		t.Fatalf("body after marker title must remain txt, got %v; kinds=%v", body, findKinds(blocks))
	}
}

func TestRoleDecisionReasonsExposeStructureForReportedFixtures(t *testing.T) {
	katDecisions := extractArticleRoleDecisionsForTest(reportedKatP22Fixture("重生之泰奶又又又火了"))
	caption := findDecisionByText(katDecisions, "图源大众点评")
	if caption == nil || caption.Role != "cap" || caption.Group != "MediaGroup" ||
		caption.Reason != "source-prefix-after-content-image" ||
		caption.CaptionProof != CaptionProofLegacyAssociation || caption.MediaID <= 0 || caption.CaptionScope <= 0 {
		t.Fatalf("content image source decision = %+v", caption)
	}
	title := findDecisionByText(katDecisions, "重生之泰奶又又又火了")
	if title == nil || title.Role != "h" || title.Group != "SectionHeaderGroup" || title.Reason != "marker-title-body" {
		t.Fatalf("decorative marker title decision = %+v", title)
	}
	body := findDecisionByText(katDecisions, "泰式奶茶已经火过")
	if body == nil || body.Role != "txt" || body.Group != "BodyFlow" {
		t.Fatalf("body flow decision = %+v", body)
	}

	prefaceDecisions := extractArticleRoleDecisionsForTest(jsContentWrap(
		`<h1 style="font-size:18px;">来源：FoodTalks</h1>` +
			`<p>开篇导语说明文章背景。</p>`))
	preface := findDecisionByText(prefaceDecisions, "来源：FoodTalks")
	if preface == nil || preface.Role != "txt" || preface.Group != "MetadataPreface" || preface.Reason != "preface-metadata" {
		t.Fatalf("preface source decision = %+v", preface)
	}
}

func TestPrefaceH1AuthorLineIsNotBlindBodyHeading(t *testing.T) {
	cases := []string{
		"作者 | 曾有为",
		"来源：FoodTalks",
	}
	for _, prefaceText := range cases {
		t.Run(prefaceText, func(t *testing.T) {
			html := jsContentWrap(
				`<h1 style="font-size:18px;">` + prefaceText + `</h1>` +
					`<p>开篇导语说明文章背景。</p>` +
					`<h2>真正的小节标题</h2>` +
					`<p>正文开始。</p>`)
			_, blocks, _ := extractArticleBlocks(html)

			preface := findBlockByText(blocks, prefaceText)
			if preface == nil {
				t.Fatalf("missing h1 preface; kinds=%v", findKinds(blocks))
			}
			if preface.K == "h" {
				t.Fatalf("author/source preface must not be emitted as body heading solely because it uses h1; kinds=%v", findKinds(blocks))
			}
			realHeading := findBlockByText(blocks, "真正的小节标题")
			if realHeading == nil || realHeading.K != "h" {
				t.Fatalf("real body heading should still be h, got %v; kinds=%v", realHeading, findKinds(blocks))
			}
		})
	}
}

func TestMarkerHeadingMetamorphicTextWrapperAndUnits(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/content.jpg" data-w="1080" data-h="720" data-ratio="0.667"></p>` +
			`<p>前一段正文提供正文流基准。</p>` +
			`<section><section style="text-align:center;">` +
			`<img data-src="https://mmbiz.qpic.cn/sz_mmbiz_png/marker.png" data-w="300" data-h="72" data-ratio="0.24">` +
			`</section></section>` +
			`<section><p style="text-align:center;"><span style="color:#3a9799; font-size:12pt;">咖啡店仍在下沉</span></p></section>` +
			`<p>随后出现一段正常正文，证明前文短句开启了新小节。</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	title := findBlockByText(blocks, "咖啡店仍在下沉")
	if title == nil {
		t.Fatalf("missing metamorphic marker title; kinds=%v", findKinds(blocks))
	}
	if title.K != "h" {
		t.Fatalf("marker title must survive text/style/wrapper changes, got %s; kinds=%v", title.K, findKinds(blocks))
	}
}

func TestShortCenteredTextWithoutMarkerIsNotHeadingByItself(t *testing.T) {
	html := jsContentWrap(
		`<p>前一段正文建立正文流。</p>` +
			`<p style="text-align:center;"><span style="color:#3a9799;font-size:12pt;">咖啡店仍在下沉</span></p>` +
			`<p>随后出现一段正常正文。</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	b := findBlockByText(blocks, "咖啡店仍在下沉")
	if b == nil {
		t.Fatalf("missing short centered text; kinds=%v", findKinds(blocks))
	}
	if b.K == "h" {
		t.Fatalf("short centered text without section marker/body-flow relationship must not be h; kinds=%v", findKinds(blocks))
	}
}

func TestMarkerAndContentImageNegativeRoleBoundaries(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/content.jpg" data-w="1080" data-h="720" data-ratio="0.667"></p>` +
			`<p style="text-align:center;">品牌门店实拍</p>` +
			`<p>正文继续描述门店状态。</p>` +
			`<p style="text-align:center;"><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_png/marker.png" data-w="300" data-h="72" data-ratio="0.24"></p>` +
			`<p style="text-align:center;"><span style="color:#3a9799;">品牌门店实拍</span></p>` +
			`<p>这个短语在这里是新小节标题，不是上一张图的说明。</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	var matches []Block
	for _, b := range blocks {
		if strings.Contains(b.Text, "品牌门店实拍") || strings.Contains(textFromRuns(b.Runs), "品牌门店实拍") {
			matches = append(matches, b)
		}
	}
	if len(matches) != 2 {
		t.Fatalf("expected two matching short texts, got %d; kinds=%v", len(matches), findKinds(blocks))
	}
	if matches[0].K != "cap" {
		t.Fatalf("same short text after content image should be cap, got %s", matches[0].K)
	}
	if matches[1].K != "h" {
		t.Fatalf("same short text after decorative marker should be h, got %s; kinds=%v", matches[1].K, findKinds(blocks))
	}
}

func TestWideContentImageWithShortCaptionIsNotMarkerHeading(t *testing.T) {
	html := jsContentWrap(
		`<p>前一段正文。</p>` +
			`<p><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/wide-chart.jpg" data-w="1080" data-h="320" data-ratio="0.296"></p>` +
			`<p style="text-align:center;">门店实拍</p>` +
			`<p>下一段正文继续解释这张宽幅图片。</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "门店实拍")
	if b == nil {
		t.Fatalf("missing short caption after wide content image; kinds=%v", findKinds(blocks))
	}
	if b.K == "h" {
		t.Fatalf("wide content image must not be treated as decorative marker solely by ratio; kinds=%v", findKinds(blocks))
	}
	if b.K != "cap" {
		t.Fatalf("short centered text after wide content image should remain cap, got %s; kinds=%v", b.K, findKinds(blocks))
	}
}

func TestSmallWideContentImageWithShortCaptionIsNotMarkerHeading(t *testing.T) {
	html := jsContentWrap(
		`<p>前一段正文。</p>` +
			`<p><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/wide-small-chart.jpg" data-w="640" data-h="190" data-ratio="0.297"></p>` +
			`<p style="text-align:center;">门店实拍</p>` +
			`<p>下一段正文继续解释这张宽幅图片。</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "门店实拍")
	if b == nil {
		t.Fatalf("missing short caption after small wide content image; kinds=%v", findKinds(blocks))
	}
	if b.K == "h" {
		t.Fatalf("small wide content image must not become marker solely by width+ratio; kinds=%v", findKinds(blocks))
	}
	if b.K != "cap" {
		t.Fatalf("short centered text after small wide content image should remain cap, got %s; kinds=%v", b.K, findKinds(blocks))
	}
}

func TestFlatContentImageWithShortCaptionIsNotMarkerHeading(t *testing.T) {
	html := jsContentWrap(
		`<p>前一段正文。</p>` +
			`<p><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/wide-banner-content.jpg" data-w="1080" data-h="160" data-ratio="0.148"></p>` +
			`<p style="text-align:center;">门店实拍</p>` +
			`<p>下一段正文继续解释这张横幅图片。</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "门店实拍")
	if b == nil {
		t.Fatalf("missing short caption after flat content image; kinds=%v", findKinds(blocks))
	}
	if b.K == "h" {
		t.Fatalf("flat content image must not become marker solely by extreme ratio; kinds=%v", findKinds(blocks))
	}
	if b.K != "cap" {
		t.Fatalf("short caption after flat content image should remain cap, got %s; kinds=%v", b.K, findKinds(blocks))
	}
}

func TestDimensionlessUnknownMediaDoesNotSwallowPlainShortTextAsCaption(t *testing.T) {
	html := jsContentWrap(
		`<p>前一段正文。</p>` +
			`<p style="text-align:center;"><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/unknown-content.jpg"></p>` +
			`<p style="text-align:center;">门店实拍</p>` +
			`<p>下一段正文继续解释这张图片。</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "门店实拍")
	if b == nil {
		t.Fatalf("missing plain short text after dimensionless media; kinds=%v", findKinds(blocks))
	}
	if b.K == "cap" {
		t.Fatalf("dimensionless unknown media must not swallow plain short text as caption; kinds=%v", findKinds(blocks))
	}
	if b.K == "h" {
		t.Fatalf("dimensionless unknown media without title signal must not promote plain short text to heading; kinds=%v", findKinds(blocks))
	}
}

func TestHighResolutionDecorativeMarkerStillCreatesHeadingGroup(t *testing.T) {
	html := jsContentWrap(
		`<p>前一段正文。</p>` +
			`<p style="text-align:center;"><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_png/marker-hd.png" data-w="1080" data-h="290" data-ratio="0.269"></p>` +
			`<p style="text-align:center;"><span style="color:#3a9799;font-size:16px;">新茶饮继续下沉</span></p>` +
			`<p>下一段正文开启小节。</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "新茶饮继续下沉")
	if b == nil || b.K != "h" {
		t.Fatalf("high-resolution decorative marker should still produce heading, got %v; kinds=%v", b, findKinds(blocks))
	}
}

func TestDimensionlessDecorativeMarkerWithHeadingSignalCreatesHeadingGroup(t *testing.T) {
	html := jsContentWrap(
		`<p>前一段正文。</p>` +
			`<p style="text-align:center;"><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_png/dimensionless-marker.png"></p>` +
			`<p style="text-align:center;"><span style="color:#3a9799;font-size:16px;">新茶饮继续下沉</span></p>` +
			`<p>下一段正文开启小节。</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "新茶饮继续下沉")
	if b == nil || b.K != "h" {
		t.Fatalf("dimensionless decorative marker with heading signal should produce heading, got %v; kinds=%v", b, findKinds(blocks))
	}
}

func TestFlatDividerWithPlainTitleCreatesHeadingGroup(t *testing.T) {
	html := jsContentWrap(
		`<p>前一段正文。</p>` +
			`<p><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/content.jpg" data-w="1080" data-h="720" data-ratio="0.667"></p>` +
			`<p style="font-size:12px;color:#888888;text-align:center;">图源大众点评</p>` +
			`<p>上一节正文收束。</p>` +
			`<p style="text-align:center;"><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_png/flat-divider.png" data-w="1080" data-h="133" data-ratio="0.123"></p>` +
			`<p style="text-align:center;">重生之泰奶又又又火了</p>` +
			`<p>其实，泰式奶茶已经火过两次了。</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	caption := findBlockByText(blocks, "图源大众点评")
	if caption == nil || caption.K != "cap" {
		t.Fatalf("content image caption should remain cap, got %v; kinds=%v", caption, findKinds(blocks))
	}
	title := findBlockByText(blocks, "重生之泰奶又又又火了")
	if title == nil || title.K != "h" {
		t.Fatalf("flat divider plain title should produce heading, got %v; kinds=%v", title, findKinds(blocks))
	}
}

func TestWeakKickerBeforeStrongMarkerTitleCreatesHeadingGroup(t *testing.T) {
	html := jsContentWrap(
		`<p>前一段正文。</p>` +
			`<p style="text-align:center;"><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_png/marker-hd.png" data-w="1080" data-h="290" data-ratio="0.269"></p>` +
			`<p style="font-size:13px;color:#9a9a9a;text-align:center;">年收入超过11亿元</p>` +
			`<p><strong>但却是餐饮界最熟悉的“陌生人”</strong></p>` +
			`<p>事实上，聚慧食品既不是“小透明”，也不是行业无人问津的供应商。</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	kicker := findBlockByText(blocks, "年收入超过11亿元")
	if kicker == nil || kicker.K != "h" {
		t.Fatalf("weak marker kicker should remain in heading group, got %v; kinds=%v", kicker, findKinds(blocks))
	}
	title := findBlockByText(blocks, "但却是餐饮界最熟悉")
	if title == nil || title.K != "h" {
		t.Fatalf("strong continuation should remain in heading group, got %v; kinds=%v", title, findKinds(blocks))
	}
}

func TestHeaderImageThenH1AuthorPrefaceIsNotCaption(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/header.jpg" data-w="1080" data-h="720" data-ratio="0.667"></p>` +
			`<h1 style="font-size:18px;text-align:center;color:#888888;">作者 | 曾有为</h1>` +
			`<p>开篇导语说明文章背景。</p>` +
			`<h2>真正的小节标题</h2>`)
	_, blocks, _ := extractArticleBlocks(html)
	preface := findBlockByText(blocks, "作者 | 曾有为")
	if preface == nil {
		t.Fatalf("missing h1 preface after header image; kinds=%v", findKinds(blocks))
	}
	if preface.K != "txt" {
		t.Fatalf("h1 author preface after header image should be txt, got %s; kinds=%v", preface.K, findKinds(blocks))
	}
}

func TestImageSourceLeadVariantsStayCaptionAfterContentImage(t *testing.T) {
	for _, sourceText := range []string{"图源大众点评", "图为门店实拍", "图片来源：品牌官方"} {
		t.Run(sourceText, func(t *testing.T) {
			html := jsContentWrap(
				`<p><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/content.jpg" data-w="1080" data-h="720" data-ratio="0.667"></p>` +
					`<p style="text-align:left;">` + sourceText + `</p>` +
					`<p>后续正文。</p>`)
			_, blocks, _ := extractArticleBlocks(html)
			b := findBlockByText(blocks, sourceText)
			if b == nil || b.K != "cap" {
				t.Fatalf("%q after content image should stay cap, got %v; kinds=%v", sourceText, b, findKinds(blocks))
			}
		})
	}
}

// #178（2026-09-09 发布）：「图为…」是图片说明，不是来源行，不能在它这里收尾，
// 否则后面的「图片来源」落进正文。
func TestImageDescriptionLeadKeepsFollowingSourceInCaption(t *testing.T) {
	html := jsContentWrap(
		`<p>超市内部采用宽通道、柔光氛围灯、分区场景化陈列的设计理念，将品牌内核贯穿全场，动线清晰流畅。</p>` +
			`<p><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_jpg/store.jpg" data-w="1080" data-h="720" data-ratio="0.667"></p>` +
			`<section style="text-align: center;"><span style="font-size: 12px;color: rgb(136, 136, 136);">图为Green&amp;Health钱湾店效果图</span></section>` +
			`<section style="text-align: center;"><section style="text-align: center;"><span style="font-size: 12px;color: rgb(136, 136, 136);">图片来源：Green&amp;Health</span></section></section>` +
			`<p>根据体验场景，门店“切割”为精品生鲜市集区、全球甄选好物区、自有品牌体验区，最终促成购买。</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	for _, needle := range []string{"图为Green&Health钱湾店效果图", "图片来源：Green&Health"} {
		if b := findBlockByText(blocks, needle); b == nil || b.K != "cap" {
			t.Fatalf("%q 必须是图注，got %v; kinds=%v", needle, b, findKinds(blocks))
		}
	}
	if b := findBlockByText(blocks, "根据体验场景"); b == nil || b.K != "txt" {
		t.Fatalf("图注后的正文必须回到正文，got %v; kinds=%v", b, findKinds(blocks))
	}
}

func TestFullSentenceAfterMarkerIsNotHeadingByItself(t *testing.T) {
	html := jsContentWrap(
		`<p>前一段正文。</p>` +
			`<p style="text-align:center;"><img data-src="https://mmbiz.qpic.cn/sz_mmbiz_png/marker.png" data-w="300" data-h="72" data-ratio="0.24"></p>` +
			`<p style="text-align:center;">这是一句完整的正文判断，它只是跟在装饰图后面。</p>` +
			`<p>下一段正文。</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "完整的正文判断")
	if b == nil {
		t.Fatalf("missing body-like sentence; kinds=%v", findKinds(blocks))
	}
	if b.K == "h" {
		t.Fatalf("body-like sentence after marker must not be h solely because of marker context; kinds=%v", findKinds(blocks))
	}
	if b.K != "txt" {
		t.Fatalf("body-like sentence after marker should stay txt, got %s; kinds=%v", b.K, findKinds(blocks))
	}
}

// ---- Blockquote / list ----

func TestBlockquote(t *testing.T) {
	html := jsContentWrap(`<blockquote>引言内容</blockquote>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "引言内容")
	if b == nil || b.K != "bq" {
		t.Errorf("<blockquote> → bq, got %v", b)
	}
}

func TestListItems(t *testing.T) {
	html := jsContentWrap(`<ul><li>第一条</li><li>第二条</li></ul>`)
	_, blocks, _ := extractArticleBlocks(html)
	liCount := 0
	for _, b := range blocks {
		if b.K == "li" {
			liCount++
		}
	}
	if liCount != 2 {
		t.Errorf("expected 2 li blocks, got kinds=%v", findKinds(blocks))
	}
}

// ---- Inline formatting preservation ----

func TestInlineBoldInsideTxt(t *testing.T) {
	html := jsContentWrap(`<p>前面<strong>加粗部分</strong>后面</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "加粗部分")
	if b == nil || b.K != "txt" {
		t.Fatalf("expected txt, got %v", b)
	}
	foundBold := false
	for _, r := range b.Runs {
		if r.Bold && strings.Contains(r.Text, "加粗") {
			foundBold = true
		}
	}
	if !foundBold {
		t.Errorf("inline bold run missing; runs=%+v", b.Runs)
	}
}

func TestLinkRun(t *testing.T) {
	html := jsContentWrap(`<p>点击 <a href="https://example.com">这里</a> 查看</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "这里")
	if b == nil {
		t.Fatalf("missing block")
	}
	foundLink := false
	for _, r := range b.Runs {
		if r.Type == "link" && r.Href == "https://example.com" && strings.Contains(r.Text, "这里") {
			foundLink = true
		}
	}
	if !foundLink {
		t.Errorf("link run missing; runs=%+v", b.Runs)
	}
}

// ---- Image handling ----

func TestImagePreservesDataW(t *testing.T) {
	html := jsContentWrap(`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" data-w="280" /></p>`)
	_, blocks, _ := extractArticleBlocks(html)
	var img *Block
	for i := range blocks {
		if blocks[i].K == "img" {
			img = &blocks[i]
		}
	}
	if img == nil {
		t.Fatalf("no image block; kinds=%v", findKinds(blocks))
	}
	if img.Src != "https://mmbiz.qpic.cn/a.jpg" {
		t.Errorf("src mismatch: %s", img.Src)
	}
	if img.DataW != "280" {
		t.Errorf("dataW mismatch: %s", img.DataW)
	}
}

func TestImageIgnoresDataUrl(t *testing.T) {
	html := jsContentWrap(`<p><img src="data:image/png;base64,abc" /></p>`)
	_, blocks, _ := extractArticleBlocks(html)
	for _, b := range blocks {
		if b.K == "img" {
			t.Errorf("data: URL should not emit img block, got %s", b.Src)
		}
	}
}

// ---- Title ----

func TestTitleFromOgMeta(t *testing.T) {
	html := jsContentWrap(`<p>body</p>`)
	title, _, _ := extractArticleBlocks(html)
	if title != "Test Article" {
		t.Errorf("title mismatch: %q", title)
	}
}

// ---- Style primitives ----

func TestIsGreyHex(t *testing.T) {
	cases := []struct {
		hex  string
		want bool
	}{
		{"888888", true},
		{"999999", true},
		{"777777", true},
		{"bbbbbb", true},  // lighter caption grey, still legible
		{"cccccc", false}, // too light — at/above 200 band
		{"555555", false}, // too dark — body-text grey
		{"ffffff", false},
		{"000000", false},
		{"336699", false}, // tinted (channel spread > 30)
		{"808080", true},
	}
	for _, c := range cases {
		if got := isGreyHex(c.hex); got != c.want {
			t.Errorf("isGreyHex(%q) = %v, want %v", c.hex, got, c.want)
		}
	}
}

func TestParseInlineStyleFontSize(t *testing.T) {
	cases := []struct {
		style   string
		wantPx  float64
		wantSet bool
	}{
		{"font-size: 18px", 18, true},
		{"font-size:14pt", 14 * 96.0 / 72.0, true}, // pt→px 按 96dpi 精确换算
		{"font-size: 1em", 16, true},
		{"color: red", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		p := parseInlineStyle(c.style)
		if p.FontSizeSet != c.wantSet || (c.wantSet && p.FontSize != c.wantPx) {
			t.Errorf("style %q → size=%v set=%v, want size=%v set=%v",
				c.style, p.FontSize, p.FontSizeSet, c.wantPx, c.wantSet)
		}
	}
}

func TestParseInlineStyleColor(t *testing.T) {
	cases := []struct {
		style string
		want  string
	}{
		{"color: #888", "888888"},
		{"color: #abcdef", "abcdef"},
		{"color: rgb(136, 136, 136)", "888888"},
		{"color: rgba(0, 0, 0, 0.5)", "000000"},
		{"font-size: 14px; color: #999;", "999999"},
		{"", ""},
	}
	for _, c := range cases {
		p := parseInlineStyle(c.style)
		if p.Color != c.want {
			t.Errorf("style %q → color=%q, want %q", c.style, p.Color, c.want)
		}
	}
}

// ---- Regression guards ----

func TestNestedSectionDoesNotDoubleEmit(t *testing.T) {
	html := jsContentWrap(`<section><section><section><p>真正的正文</p></section></section></section>`)
	_, blocks, _ := extractArticleBlocks(html)
	count := 0
	for _, b := range blocks {
		if b.K == "txt" && strings.Contains(textFromRuns(b.Runs), "真正的正文") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 txt block, got %d; kinds=%v", count, findKinds(blocks))
	}
}

func TestInheritedFontSizePromotesBoldChild(t *testing.T) {
	// WeChat pattern: <section style="font-size:20px"><p><strong>title</strong></p></section>
	// The child <p> has no font-size of its own but inherits 20px from the
	// ancestor <section>. Classifier must see the inherited value.
	html := jsContentWrap(
		`<section style="font-size: 20px;"><p><strong>嵌套标题</strong></p></section>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "嵌套标题")
	if b == nil {
		t.Fatalf("missing block; kinds=%v", findKinds(blocks))
	}
	if b.K != "h" {
		t.Errorf("inherited 20px + bold → h, got %s", b.K)
	}
}

// ---- 参考来源块永远不会因图片邻接和样式被误吞为 cap（基石 news/62285 结构）----
// 真实文章里「参考来源：」前常有图，且参考来源行字号小、灰色、短；没有明确
// 媒体绑定时整块必须走 txt。
func TestReferenceSectionNotCaptioned(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/x.png" data-w="1080" data-ratio="0.5"></p>` +
			`<p><span style="font-size:14px;color:#888888;">参考来源：</span></p>` +
			`<p><span style="font-size:14px;color:#888888;">[1] Starbucks tea party, 2013, Fortune</span></p>` +
			`<p><span style="font-size:14px;color:#888888;">—记者实地探访</span></p>` +
			`<p><span style="font-size:14px;color:#888888;">[2] Teavana S-1, 2011, SEC</span></p>` +
			`<p><span style="font-size:14px;color:#888888;">—IPO前财务数据</span></p>`)
	_, blocks, _ := extractArticleBlocks(html)

	// 参考来源头及之后所有 [n]/说明行都应是 txt，不能是 cap
	for _, needle := range []string{"参考来源", "[1]", "—记者实地探访", "[2]", "—IPO前财务数据"} {
		b := findBlockByText(blocks, needle)
		if b == nil {
			t.Fatalf("block %q not found", needle)
		}
		if b.K == "cap" {
			t.Errorf("block %q 被误标为 cap，应为 txt（参考来源不是图注）", needle)
		}
	}
}

func findTableBlock(blocks []Block) *Block {
	for i := range blocks {
		if blocks[i].K == "table" {
			return &blocks[i]
		}
	}
	return nil
}

// ---- 表格保结构：URL 导入不再把表格拍平成每格一段 ----
// 结构对齐参考公众号文章（thead 蓝底白字表头 + tbody 居中正文），见
// https://mp.weixin.qq.com/s/rVkNAkekkMYRc1Sw3EaNtA。
func TestTablePreservesRowsAndHeader(t *testing.T) {
	html := jsContentWrap(
		`<table style="width: 556px;"><thead><tr>` +
			`<td style="background-color: rgb(2, 57, 107);"><strong>产品名称</strong></td>` +
			`<td style="background-color: rgb(2, 57, 107);"><strong>价格</strong></td>` +
			`</tr></thead><tbody><tr>` +
			`<td><section style="text-align: center;">话梅</section></td>` +
			`<td><section style="text-align: center;">¥11.9</section></td>` +
			`</tr></tbody></table>`)
	_, blocks, _ := extractArticleBlocks(html)

	tbl := findTableBlock(blocks)
	if tbl == nil {
		t.Fatalf("表格未保留为 K=table block，实际 kinds=%v", findKinds(blocks))
	}
	if len(tbl.Rows) != 2 {
		t.Fatalf("应有 2 行，实得 %d", len(tbl.Rows))
	}
	if len(tbl.Rows[0]) != 2 || len(tbl.Rows[1]) != 2 {
		t.Fatalf("每行应 2 列，实得 %d / %d", len(tbl.Rows[0]), len(tbl.Rows[1]))
	}
	// thead 单元格必须标 header
	if !tbl.Rows[0][0].Header || !tbl.Rows[0][1].Header {
		t.Errorf("thead 单元格应标 Header=true")
	}
	if tbl.Rows[1][0].Header {
		t.Errorf("tbody 单元格不应是 Header")
	}
	// 单元格文本取出正确（section 包裹被拍进 runs）
	cellText := func(c TableCell) string {
		var sb strings.Builder
		for _, r := range c.Runs {
			sb.WriteString(r.Text)
		}
		return strings.TrimSpace(sb.String())
	}
	if got := cellText(tbl.Rows[0][0]); got != "产品名称" {
		t.Errorf("表头首格 = %q，应为 产品名称", got)
	}
	if got := cellText(tbl.Rows[1][1]); got != "¥11.9" {
		t.Errorf("正文末格 = %q，应为 ¥11.9", got)
	}
}

// <th> 单元格即使不在 thead 里也算表头。
func TestTableThCellsAreHeader(t *testing.T) {
	html := jsContentWrap(
		`<table><tr><th>列一</th><th>列二</th></tr><tr><td>a</td><td>b</td></tr></table>`)
	_, blocks, _ := extractArticleBlocks(html)
	tbl := findTableBlock(blocks)
	if tbl == nil {
		t.Fatalf("表格未保留，kinds=%v", findKinds(blocks))
	}
	if !tbl.Rows[0][0].Header || !tbl.Rows[0][1].Header {
		t.Errorf("<th> 单元格应标 Header=true")
	}
}

// TestExtractWechatArticleMeta 公众号页面结构化元数据直取（M7）：公众号名走
// var nickname（htmlDecode 包裹可有可无），作者署名 meta author 优先、var author
// 兜底；没署名的文章返回空串——这是发布者填的字段，宁空勿造（规范 §5）。
func TestExtractWechatArticleMeta(t *testing.T) {
	page := `<html><head><meta name="author" content="Mote&amp;莫特" /></head>
<script>var nickname = htmlDecode("FBIF食品饮料创新");var author = "备用名";
var msg_source_url = 'https://mp.weixin.qq.com/s/original-article';</script></html>`
	nickname, byline, sourceURL := extractWechatArticleMeta(page)
	if nickname != "FBIF食品饮料创新" {
		t.Fatalf("公众号名解析失败：%q", nickname)
	}
	if byline != "Mote&莫特" {
		t.Fatalf("meta author 应优先且解码实体：%q", byline)
	}
	if sourceURL != "https://mp.weixin.qq.com/s/original-article" {
		t.Fatalf("阅读原文链接解析失败：%q", sourceURL)
	}
	// meta 缺失 → var author 兜底；nickname 无 htmlDecode 包裹也要认；无 msg_source_url → 空。
	nickname, byline, sourceURL = extractWechatArticleMeta(`<script>var nickname = "小食代";var author = "何丹琳";</script>`)
	if nickname != "小食代" || byline != "何丹琳" || sourceURL != "" {
		t.Fatalf("var 形态解析失败：%q %q %q", nickname, byline, sourceURL)
	}
	// 未署名文章（2026-07-10 真实页面形态）：两个锚点都空；msg_source_url 为空串同样返回空。
	nickname, byline, sourceURL = extractWechatArticleMeta(`<meta name="author" content="" /><script>var nickname = htmlDecode("FBIF食品饮料创新");var author = "";var msg_source_url = '';</script>`)
	if nickname != "FBIF食品饮料创新" || byline != "" || sourceURL != "" {
		t.Fatalf("未署名文章应返回空 byline/sourceURL：%q %q %q", nickname, byline, sourceURL)
	}
	// 非 http(s) 值（javascript: 等）不透传。
	if _, _, got := extractWechatArticleMeta(`<script>var msg_source_url = 'javascript:alert(1)';</script>`); got != "" {
		t.Fatalf("非 http(s) 的 msg_source_url 应被丢弃：%q", got)
	}
	// 页面里 & 写成 JS 转义 \x26amp;（2026-10-28 零售商业财经页面），要还原成 &。
	_, _, sourceURL = extractWechatArticleMeta(`<script>var msg_source_url = 'https://mp.weixin.qq.com/mp/profile_ext?action=home\x26amp;__biz=Mzg2ODg4ODU5MA==\x26amp;scene=1#wechat_redirect';</script>`)
	if sourceURL != "https://mp.weixin.qq.com/mp/profile_ext?action=home&__biz=Mzg2ODg4ODU5MA==&scene=1#wechat_redirect" {
		t.Fatalf("阅读原文链接里的 \\x26amp; 应还原成 &：%q", sourceURL)
	}
}

// 2026-10 起公众号正文后面先是「预览时标签不可点」提示，不能混进正文末尾。
func TestWechatTagsPreviewToastIsNotBody(t *testing.T) {
	page := `<html><body><div class="rich_media_content" id="js_content"><p>正文最后一段。</p></div>
<script>var first_sceen__time = 1;</script></div>
<div id="js_tags_preview_toast" class="article-tag__error-tips" style="display: none;">预览时标签不可点</div>
<div id="content_bottom_area"></div></body></html>`
	_, blocks, _ := extractArticleBlocks(page)
	if findBlockByText(blocks, "预览时标签不可点") != nil {
		t.Fatalf("页面提示混进正文：%v", findKinds(blocks))
	}
	if findBlockByText(blocks, "正文最后一段") == nil {
		t.Fatalf("正文丢了：%v", findKinds(blocks))
	}
}

// 零售商业财经《麦德龙启动B2B2C战略》：开头引号显示 20px、署名旁竖线 1px（原图 3×120），
// 按配图排会被网站放大成大灰引号和 400 宽的大黑块。原文显示得很小的图整张不要，也不把段落拆开。
func TestWechatTinyDecorativeImagesAreDropped(t *testing.T) {
	page := `<html><body><div class="rich_media_content" id="js_content">
<p><img data-src="https://mmbiz.qpic.cn/hero/640" data-w="1080" data-ratio="0.66" style="width: 100%;"></p>
<section><img data-src="https://mmbiz.qpic.cn/quote-open/640" data-w="63" data-ratio="0.76" style="max-width: 100%;width: 20px !important;height: auto !important;"></section>
<p>物美收购五年，麦德龙用B端能力赌C端未来。</p>
<section><img data-src="https://mmbiz.qpic.cn/bar/640" data-w="3" data-ratio="40" width="1" style="width: 0.998264px !important;"></section>
<p>作者：柴元锦</p>
<p>前半句<img data-src="https://mmbiz.qpic.cn/icon/640" data-w="54" data-ratio="2">后半句</p>
<p><img data-src="https://mmbiz.qpic.cn/qr/640" data-w="258" data-ratio="1" style="width: 150px;"></p>
</div></body></html>`
	_, blocks, _ := extractArticleBlocks(page)
	var srcs []string
	for _, b := range blocks {
		if b.K == "img" {
			srcs = append(srcs, b.Src)
		}
	}
	if len(srcs) != 2 || !strings.Contains(srcs[0], "/hero/") || !strings.Contains(srcs[1], "/qr/") {
		t.Fatalf("只该留头图和二维码，得到 %v", srcs)
	}
	if findBlockByText(blocks, "前半句后半句") == nil {
		t.Fatalf("小图标把段落拆开了：%v", findKinds(blocks))
	}
}
