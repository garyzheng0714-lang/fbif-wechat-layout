package main

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Block is the frontend-facing unit emitted by extractArticleBlocks.
// Shape matches the `elems` array produced by classifyMd in fbif.js so the
// renderer can consume them without translation.
type Block struct {
	K       string `json:"k"`
	Text    string `json:"text,omitempty"`
	Runs    []Run  `json:"runs,omitempty"`
	Src     string `json:"src,omitempty"`
	DataW   string `json:"dataW,omitempty"`
	Ordered bool   `json:"ordered,omitempty"`
}

// Run is an inline fragment. Type ∈ {"txt", "link"}.
type Run struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Bold bool   `json:"bold,omitempty"`
	Href string `json:"href,omitempty"`
}

// computedStyle carries the CSS signals needed for classification as they
// inherit down the DOM tree. Every block is classified against a resolved
// snapshot of its ancestor chain's styles plus its own overrides.
type computedStyle struct {
	FontSize    float64
	FontSizeSet bool
	Color       string
	ColorSet    bool
	Align       string
}

// extractArticleBlocks walks WeChat article HTML into a sequence of Blocks.
// Unlike extractArticle (html2md.go), it preserves the visual signals
// (font-size, color, align, bold) needed to distinguish heading vs pullquote
// vs caption vs body — classification that Markdown can no longer represent.
func extractArticleBlocks(rawHTML string) (title string, blocks []Block, author string) {
	title = extractMeta(rawHTML, "og:title")
	if title == "" {
		if m := regexp.MustCompile(`<title[^>]*>([^<]+)</title>`).FindStringSubmatch(rawHTML); len(m) > 1 {
			title = strings.TrimSpace(html.UnescapeString(m[1]))
		}
	}

	contentHTML := extractJsContent(rawHTML)
	if contentHTML == "" {
		contentHTML = extractTag(rawHTML, "article")
		if contentHTML == "" {
			contentHTML = extractTag(rawHTML, "main")
		}
		if contentHTML == "" {
			contentHTML = rawHTML
		}
	}

	contentHTML = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`).ReplaceAllString(contentHTML, "")
	contentHTML = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`).ReplaceAllString(contentHTML, "")
	contentHTML = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(contentHTML, "")

	doc, err := html.Parse(strings.NewReader(contentHTML))
	if err != nil {
		return title, nil, ""
	}

	w := &blockWalker{}
	w.walk(doc, nil, computedStyle{FontSize: 16})
	return title, w.blocks, ""
}

// ---- Walker ----

type blockWalker struct {
	blocks       []Block
	lastWasImage bool
}

type blockCtx struct {
	tag        string
	classes    []string
	style      computedStyle
	runs       []Run
	boldDepth  int
	hrefStack  []string
	textStyles []computedStyle // computed style at each text node — needed
	// because WeChat emits captions as <p><img><span style="...">text</span></p>:
	// the caption style sits on an inner span, not on the block. Classifying
	// against only the block's own style would miss these.
}

func isBlockTag(tag string) bool {
	switch tag {
	case "p", "section", "div", "li", "blockquote",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"article", "main", "header", "footer",
		"ul", "ol", "table", "tr", "td", "th",
		"figure", "figcaption":
		return true
	}
	return false
}

func isHeadingTag(tag string) bool {
	return len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6'
}

func (w *blockWalker) walk(n *html.Node, cur *blockCtx, inherited computedStyle) {
	switch n.Type {
	case html.TextNode:
		if cur == nil {
			return
		}
		if cur.runs == nil && strings.TrimSpace(n.Data) == "" {
			return
		}
		cur.runs = append(cur.runs, w.makeTextRun(cur, n.Data))
		cur.textStyles = append(cur.textStyles, inherited)
		return

	case html.ElementNode:
		tag := strings.ToLower(n.Data)
		switch tag {
		case "script", "style", "noscript", "iframe":
			return
		case "br":
			if cur != nil {
				cur.runs = append(cur.runs, Run{Type: "txt", Text: " "})
			}
			return
		case "img":
			src := getAttr(n, "data-src")
			if src == "" {
				src = getAttr(n, "src")
			}
			if src == "" || strings.Contains(src, "data:image") {
				return
			}
			// Ordering matters: emit() resets lastWasImage=false, so we must
			// append the image and set lastWasImage=true AFTER the flush.
			// Don't reorder without preserving this invariant, or the
			// caption immediately after the image will be misclassified.
			if cur != nil && hasContent(cur.runs) {
				w.emit(cur)
				cur.runs = nil
				cur.textStyles = nil
			}
			w.blocks = append(w.blocks, Block{K: "img", Src: src, DataW: getAttr(n, "data-w")})
			w.lastWasImage = true
			return
		case "a":
			href := getAttr(n, "href")
			if cur != nil && href != "" && !strings.HasPrefix(href, "javascript:") {
				cur.hrefStack = append(cur.hrefStack, href)
				w.walkChildren(n, cur, inherited)
				cur.hrefStack = cur.hrefStack[:len(cur.hrefStack)-1]
				return
			}
			w.walkChildren(n, cur, inherited)
			return
		case "strong", "b":
			if cur != nil {
				cur.boldDepth++
			}
			w.walkChildren(n, cur, inherited)
			if cur != nil {
				cur.boldDepth--
			}
			return
		}

		nodeStyle := parseInlineStyle(getAttr(n, "style"))
		merged := mergeStyle(inherited, nodeStyle)
		spanBold := nodeStyle.fontWeightBold

		if isBlockTag(tag) {
			if cur != nil && hasContent(cur.runs) {
				w.emit(cur)
				cur.runs = nil
				cur.textStyles = nil
			}
			child := &blockCtx{
				tag:     tag,
				classes: strings.Fields(getAttr(n, "class")),
				style:   merged,
			}
			if cur != nil {
				child.hrefStack = append([]string(nil), cur.hrefStack...)
				child.boldDepth = cur.boldDepth
			}
			if spanBold {
				child.boldDepth++
			}
			w.walkChildren(n, child, merged)
			w.emit(child)
			return
		}

		if cur != nil && spanBold {
			cur.boldDepth++
		}
		w.walkChildren(n, cur, merged)
		if cur != nil && spanBold {
			cur.boldDepth--
		}
		return
	}

	w.walkChildren(n, cur, inherited)
}

func (w *blockWalker) walkChildren(n *html.Node, cur *blockCtx, style computedStyle) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.walk(c, cur, style)
	}
}

func (w *blockWalker) makeTextRun(cur *blockCtx, text string) Run {
	bold := cur.boldDepth > 0
	if len(cur.hrefStack) > 0 {
		return Run{Type: "link", Text: text, Href: cur.hrefStack[len(cur.hrefStack)-1], Bold: bold}
	}
	return Run{Type: "txt", Text: text, Bold: bold}
}

// ---- Classification ----

func (w *blockWalker) emit(cx *blockCtx) {
	runs := normalizeRuns(cx.runs)
	if !hasContent(runs) {
		return
	}

	text := textFromRuns(runs)
	textLen := utf8.RuneCountInString(text)
	allBold := allRunsBold(runs)

	if isHeadingTag(cx.tag) {
		w.blocks = append(w.blocks, Block{K: "h", Text: text})
		w.lastWasImage = false
		return
	}

	for _, c := range cx.classes {
		lc := strings.ToLower(c)
		if lc == "rich_media_title" || lc == "js_article_title" {
			w.blocks = append(w.blocks, Block{K: "h", Text: text})
			w.lastWasImage = false
			return
		}
	}

	// Visual-signal heading: font-size ≥ 18px AND all text runs bold AND ≤ 80 chars.
	// Pullquotes stay at body font-size → fail this gate → remain txt with bold inline.
	if cx.style.FontSizeSet && cx.style.FontSize >= 18 && allBold && textLen > 0 && textLen <= 80 {
		w.blocks = append(w.blocks, Block{K: "h", Text: text})
		w.lastWasImage = false
		return
	}

	if w.lastWasImage && isCaption(cx, textLen) {
		w.blocks = append(w.blocks, Block{K: "cap", Text: text})
		w.lastWasImage = false
		return
	}

	switch cx.tag {
	case "blockquote":
		w.blocks = append(w.blocks, Block{K: "bq", Runs: runs})
	case "li":
		w.blocks = append(w.blocks, Block{K: "li", Runs: runs})
	default:
		w.blocks = append(w.blocks, Block{K: "txt", Runs: runs})
	}
	w.lastWasImage = false
}

func isCaption(cx *blockCtx, textLen int) bool {
	if textLen == 0 || textLen > 80 {
		return false
	}
	for _, c := range cx.classes {
		lc := strings.ToLower(c)
		if strings.Contains(lc, "caption") || strings.Contains(lc, "img_des") || strings.Contains(lc, "imgdesc") {
			return true
		}
	}
	if styleSignalsCaption(cx.style, textLen) {
		return true
	}
	// Also scan the styles observed at each text node. WeChat puts the
	// caption's font-size/color on an inner <span>, not on the enclosing
	// <p>, so the block-level style often carries no signal even though
	// the visible style clearly marks the text as a caption.
	for _, s := range cx.textStyles {
		if styleSignalsCaption(s, textLen) {
			return true
		}
	}
	return false
}

func styleSignalsCaption(s computedStyle, textLen int) bool {
	if s.FontSizeSet && s.FontSize > 0 && s.FontSize <= 14 {
		return true
	}
	if s.ColorSet && isGreyHex(s.Color) {
		return true
	}
	if s.Align == "center" && textLen <= 40 {
		return true
	}
	return false
}

// ---- Style parsing ----

type parsedStyle struct {
	computedStyle
	fontWeightBold bool
}

// parseInlineStyle walks `a: b; c: d` declarations. Splitting on `;` up
// front (instead of running regexes over the whole string) makes it
// impossible for a substring like `background-color` to accidentally
// match the `color` rule, and makes per-property logic trivial.
var reFontSizeValue = regexp.MustCompile(`^\s*([0-9]*\.?[0-9]+)\s*(px|pt|em|rem|%)?`)
var reFontWeightValue = regexp.MustCompile(`^(bold|bolder|[6-9]\d\d)\b`)

func parseInlineStyle(style string) parsedStyle {
	out := parsedStyle{}
	if style == "" {
		return out
	}
	for _, decl := range strings.Split(style, ";") {
		kv := strings.SplitN(decl, ":", 2)
		if len(kv) != 2 {
			continue
		}
		prop := strings.ToLower(strings.TrimSpace(kv[0]))
		val := strings.TrimSpace(kv[1])
		if val == "" {
			continue
		}
		switch prop {
		case "font-size":
			if m := reFontSizeValue.FindStringSubmatch(val); m != nil {
				if v, err := strconv.ParseFloat(m[1], 64); err == nil {
					switch strings.ToLower(m[2]) {
					case "", "px":
						out.FontSize = v
					case "pt":
						out.FontSize = v * 1.333
					case "em", "rem":
						out.FontSize = v * 16
					case "%":
						out.FontSize = v * 16 / 100
					}
					if out.FontSize > 0 {
						out.FontSizeSet = true
					}
				}
			}
		case "color":
			if hex := normalizeColor(val); hex != "" {
				out.Color = hex
				out.ColorSet = true
			}
		case "text-align":
			out.Align = strings.ToLower(strings.Fields(val)[0])
		case "font-weight":
			if reFontWeightValue.MatchString(strings.ToLower(val)) {
				out.fontWeightBold = true
			}
		}
	}
	return out
}

func mergeStyle(parent computedStyle, child parsedStyle) computedStyle {
	out := parent
	if child.FontSizeSet {
		out.FontSize = child.FontSize
		out.FontSizeSet = true
	}
	if child.ColorSet {
		out.Color = child.Color
		out.ColorSet = true
	}
	if child.Align != "" {
		out.Align = child.Align
	}
	return out
}

var reHex3 = regexp.MustCompile(`^#([0-9a-f])([0-9a-f])([0-9a-f])$`)
var reHex6 = regexp.MustCompile(`^#([0-9a-f]{6})$`)
var reRgb = regexp.MustCompile(`^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)`)

func normalizeColor(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if m := reHex6.FindStringSubmatch(v); m != nil {
		return m[1]
	}
	if m := reHex3.FindStringSubmatch(v); m != nil {
		return m[1] + m[1] + m[2] + m[2] + m[3] + m[3]
	}
	if m := reRgb.FindStringSubmatch(v); m != nil {
		r, _ := strconv.Atoi(m[1])
		g, _ := strconv.Atoi(m[2])
		b, _ := strconv.Atoi(m[3])
		return toHex(r) + toHex(g) + toHex(b)
	}
	return ""
}

func toHex(v int) string {
	if v < 0 {
		v = 0
	} else if v > 255 {
		v = 255
	}
	s := strconv.FormatInt(int64(v), 16)
	if len(s) == 1 {
		return "0" + s
	}
	return s
}

// isGreyHex tests a 6-hex color for "neutral grey suitable for captions":
// every channel within 30 of the others AND all in the [100, 200] band.
// Rejects pure black/white, tinted greys, and dark body text (#333 etc.).
func isGreyHex(hex string) bool {
	if len(hex) != 6 {
		return false
	}
	r, err := strconv.ParseInt(hex[0:2], 16, 0)
	if err != nil {
		return false
	}
	g, err := strconv.ParseInt(hex[2:4], 16, 0)
	if err != nil {
		return false
	}
	b, err := strconv.ParseInt(hex[4:6], 16, 0)
	if err != nil {
		return false
	}
	lo, hi := r, r
	for _, v := range []int64{g, b} {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if hi-lo > 30 {
		return false
	}
	return lo >= 100 && hi <= 200
}

// ---- Run helpers ----

func hasContent(runs []Run) bool {
	for _, r := range runs {
		if strings.TrimSpace(r.Text) != "" {
			return true
		}
	}
	return false
}

func textFromRuns(runs []Run) string {
	var sb strings.Builder
	for _, r := range runs {
		sb.WriteString(r.Text)
	}
	return strings.TrimSpace(sb.String())
}

func allRunsBold(runs []Run) bool {
	saw := false
	for _, r := range runs {
		if strings.TrimSpace(r.Text) == "" {
			continue
		}
		if !r.Bold {
			return false
		}
		saw = true
	}
	return saw
}

var reWhitespace = regexp.MustCompile(`[\s]+`)

func collapseWhitespace(s string) string {
	return reWhitespace.ReplaceAllString(s, " ")
}

func normalizeRuns(runs []Run) []Run {
	if len(runs) == 0 {
		return runs
	}
	out := make([]Run, 0, len(runs))
	for _, r := range runs {
		r.Text = collapseWhitespace(r.Text)
		if r.Text == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Type == r.Type && out[n-1].Bold == r.Bold && out[n-1].Href == r.Href {
			out[n-1].Text += r.Text
			continue
		}
		out = append(out, r)
	}
	return out
}
