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

// ---- Bug B: image caption must classify as cap ----

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
		t.Errorf("grey caption must be cap, got %s", b.K)
	}
}

func TestImageCaptionBySmallFont(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" /></p>` +
			`<p style="font-size: 12px;">图片说明：来源不详</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "图片说明")
	if b == nil || b.K != "cap" {
		t.Errorf("small-font post-img → cap, got %v", b)
	}
}

func TestImageCaptionByCenterShort(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" /></p>` +
			`<p style="text-align: center;">来源：财新网</p>`)
	_, blocks, _ := extractArticleBlocks(html)
	b := findBlockByText(blocks, "来源：财新网")
	if b == nil || b.K != "cap" {
		t.Errorf("center+short post-img → cap, got %v", b)
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

func TestCaptionContinuationRequiresCaptionStyle(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" /></p>` +
			`<p style="text-align: center;">图注</p>` +
			`<p style="text-align: center;">短正文</p>`)
	_, blocks, _ := extractArticleBlocks(html)

	caption := findBlockByText(blocks, "图注")
	if caption == nil || caption.K != "cap" {
		t.Fatalf("first center-short post-image line must be cap, got %v", caption)
	}
	body := findBlockByText(blocks, "短正文")
	if body == nil || body.K != "txt" {
		t.Fatalf("center-short text after a caption needs stronger caption styling, got %v; kinds=%v", body, findKinds(blocks))
	}
}

func TestCaptionCenterThresholdCanBeConfigured(t *testing.T) {
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" /></p>` +
			`<p style="text-align: center;">abcdef</p>`)
	_, defaultBlocks, _ := extractArticleBlocks(html)
	if b := findBlockByText(defaultBlocks, "abcdef"); b == nil || b.K != "cap" {
		t.Fatalf("default center-short threshold should classify caption, got %v; kinds=%v", b, findKinds(defaultBlocks))
	}

	_, strictBlocks, _ := extractArticleBlocksWithOptions(html, articleBlockOptions{
		CaptionMaxChars:       80,
		CaptionCenterMaxChars: 3,
	})
	if b := findBlockByText(strictBlocks, "abcdef"); b == nil || b.K != "txt" {
		t.Fatalf("configured center threshold should keep longer centered text as body, got %v; kinds=%v", b, findKinds(strictBlocks))
	}
}

// 说明性长图注（>80 字，中英混排 + 多个产品名/括号注释）带小字/灰色强信号时，
// 必须识别为图注，而不是被 80 字基础上限一刀切成正文。复刻真实样例：
// mp.weixin.qq.com/s/8xVXVFar3fbJEEQKHSwy7w 那段「左前方为…后排三款为…」(~123 字)。
func TestLongStyledCaptionStaysCaption(t *testing.T) {
	longCaption := "左前方为Classic Collection系列的Paris（巴黎红茶），后排三款为HT系列：" +
		"Vanilla Comoro（香草科摩罗）、Green Tea with Coconut（椰子绿茶）和Peppermint Herbal（薄荷草本茶）"
	if n := len([]rune(longCaption)); n <= defaultImageCaptionMaxChars {
		t.Fatalf("样例图注应超过基础上限以验证回归，实际 %d 字", n)
	}
	// WeChat 把图注的字号/颜色放在内层 <span> 上（灰色强信号）。
	html := jsContentWrap(
		`<p><img data-src="https://mmbiz.qpic.cn/a.jpg" /></p>` +
			`<p style="text-align: center;"><span style="font-size: 14px; color: #888888;">` +
			longCaption + `</span></p>`)
	_, blocks, _ := extractArticleBlocks(html)

	cap := findBlockByText(blocks, "Peppermint Herbal")
	if cap == nil || cap.K != "cap" {
		t.Fatalf("带灰色强信号的长图注必须识别为 cap，got %v; kinds=%v", cap, findKinds(blocks))
	}
}

// 防过度放宽：图片后的长【普通正文】段（非小字、非灰色）即使超过基础上限，
// 也必须保持正文——强信号高上限只对小字/灰色图注生效。
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
		ID:     "site-tight-caption",
		Name:   "网站短图注阈值",
		Config: defaultFoodTalksSiteRuleConfig(),
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

// Real WeChat pattern: <p> holds both the <img> and a trailing styled
// <span> with the caption. The block's own style is empty; the caption
// signal lives on the inner span. Must still classify as cap.
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
		t.Errorf("caption inside same <p> as <img> must be cap, got %s", b.K)
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
		{"font-size:14pt", 14 * 1.333, true},
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

// ---- 参考来源块不被图注启发式误吞为 cap（基石 news/62285 结构）----
// 真实文章里「参考来源：」前常有图（封面/配图），caption context 处于开启状态，
// 参考来源行字号小、灰色、短，会被误标 cap 拆散。期望：整块走 txt。
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
