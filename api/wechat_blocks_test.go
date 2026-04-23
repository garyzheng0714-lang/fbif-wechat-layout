package main

import (
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
