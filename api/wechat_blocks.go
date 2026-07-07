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
	K           string        `json:"k"`
	Text        string        `json:"text,omitempty"`
	Runs        []Run         `json:"runs,omitempty"`
	Src         string        `json:"src,omitempty"`
	DataW       string        `json:"dataW,omitempty"`
	DataH       string        `json:"dataH,omitempty"`
	DataRatio   string        `json:"dataRatio,omitempty"`
	Ordered     bool          `json:"ordered,omitempty"`
	Rows        [][]TableCell `json:"rows,omitempty"`        // K=="table": 二维行/列，保住表格结构
	Poster      string        `json:"poster,omitempty"`      // K=="video": 视频封面 poster
	BlankBefore bool          `json:"blankBefore,omitempty"` // 原文在该块前有显式空段（<p><br></p> 等）
}

// TableCell is one cell of a K=="table" block. Header marks heading-row /
// <th> cells so renderers apply the header style. Matches the neutral table
// elem the DOCX path produces ({runs, header}) so both import paths feed the
// same renderTable on each side.
type TableCell struct {
	Runs   []Run `json:"runs,omitempty"`
	Header bool  `json:"header,omitempty"`
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
	FontSize       float64
	FontSizeSet    bool
	Color          string
	ColorSet       bool
	Align          string
	FontWeightBold bool
	BoldTag        bool
}

const (
	defaultImageCaptionMaxChars       = 80
	defaultImageCaptionCenterMaxChars = 40
	// 带强信号（小字号/灰色）的图注放宽到的更高字数上限。说明性长图注（中英混排、
	// 多个产品名 + 括号注释，如「左前方为…（巴黎红茶），后排三款为…」）常突破 80 字，
	// 但小字/灰色是公众号图注的明确标记（正文从不用小字/灰色），故对带此强信号的图后
	// 段落放宽长度闸到 200，避免被一刀切降级成正文。
	defaultImageCaptionStrongMaxChars = 200
)

type articleBlockOptions struct {
	CaptionMaxChars       int
	CaptionCenterMaxChars int
	CaptionStrongMaxChars int
}

func defaultArticleBlockOptions() articleBlockOptions {
	return articleBlockOptions{
		CaptionMaxChars:       defaultImageCaptionMaxChars,
		CaptionCenterMaxChars: defaultImageCaptionCenterMaxChars,
		CaptionStrongMaxChars: defaultImageCaptionStrongMaxChars,
	}
}

func normalizeArticleBlockOptions(opts articleBlockOptions) articleBlockOptions {
	defaults := defaultArticleBlockOptions()
	if opts.CaptionMaxChars <= 0 || opts.CaptionMaxChars > 500 {
		opts.CaptionMaxChars = defaults.CaptionMaxChars
	}
	if opts.CaptionCenterMaxChars <= 0 || opts.CaptionCenterMaxChars > opts.CaptionMaxChars {
		opts.CaptionCenterMaxChars = defaults.CaptionCenterMaxChars
	}
	if opts.CaptionStrongMaxChars <= 0 || opts.CaptionStrongMaxChars > 1000 {
		opts.CaptionStrongMaxChars = defaults.CaptionStrongMaxChars
	}
	// 强信号上限是放宽用的，不应比基础上限还严。
	if opts.CaptionStrongMaxChars < opts.CaptionMaxChars {
		opts.CaptionStrongMaxChars = opts.CaptionMaxChars
	}
	return opts
}

// extractArticleBlocks walks WeChat article HTML into a sequence of Blocks.
// Unlike extractArticle (html2md.go), it preserves the visual signals
// (font-size, color, align, bold) needed to distinguish heading vs pullquote
// vs caption vs body — classification that Markdown can no longer represent.
func extractArticleBlocks(rawHTML string) (title string, blocks []Block, author string) {
	return extractArticleBlocksWithOptions(rawHTML, defaultArticleBlockOptions())
}

func extractArticleBlocksWithOptions(rawHTML string, opts articleBlockOptions) (title string, blocks []Block, author string) {
	opts = normalizeArticleBlockOptions(opts)
	title = extractMeta(rawHTML, "og:title")
	if title == "" {
		if m := regexp.MustCompile(`<title[^>]*>([^<]+)</title>`).FindStringSubmatch(rawHTML); len(m) > 1 {
			title = strings.TrimSpace(html.UnescapeString(m[1]))
		}
	}

	contentHTML := resolveContentHTML(rawHTML)

	contentHTML = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`).ReplaceAllString(contentHTML, "")
	contentHTML = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`).ReplaceAllString(contentHTML, "")
	contentHTML = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(contentHTML, "")

	doc, err := html.Parse(strings.NewReader(contentHTML))
	if err != nil {
		return title, nil, ""
	}

	w := &blockWalker{opts: opts}
	w.walk(doc, nil, computedStyle{FontSize: 16})
	return title, w.blocks, ""
}

// ---- Walker ----

type blockWalker struct {
	opts               articleBlockOptions
	blocks             []Block
	pendingBlankBefore bool
	captionContext     bool
	captionLines       int
	// inRefSection: 进入「参考来源：」区后置真。该区内的 [n] 条目与「—说明」延续行
	// 字号小、颜色灰，会被 caption 启发式误吞成 cap（拆散参考来源块）。置真后强制
	// 这些行走 txt，交前端 refMode 统一识别为连续的 ref-item（无空行）。
	inRefSection bool
}

type blockCtx struct {
	tag        string
	classes    []string
	style      computedStyle
	runs       []Run
	hadBreak   bool
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
		cur.runs = append(cur.runs, w.makeTextRun(cur, n.Data, inherited))
		cur.textStyles = append(cur.textStyles, inherited)
		return

	case html.ElementNode:
		tag := strings.ToLower(n.Data)
		switch tag {
		case "script", "style", "noscript", "iframe":
			return
		case "br":
			if cur != nil {
				cur.hadBreak = true
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
			// Ordering matters: emit() closes caption context, so we must
			// append the image and open caption context AFTER the flush.
			// Don't reorder without preserving this invariant, or the
			// caption immediately after the image will be misclassified.
			if cur != nil && hasContent(cur.runs) {
				w.emit(cur)
				cur.runs = nil
				cur.textStyles = nil
			}
			w.appendBlock(Block{K: "img", Src: src, DataW: getAttr(n, "data-w"), DataH: getAttr(n, "data-h"), DataRatio: getAttr(n, "data-ratio")})
			w.captionContext = true
			w.captionLines = 0
			w.inRefSection = false
			return
		case "video":
			// FoodTalks 网站文章视频 = <video src=mp4 poster=jpg>（成品口径）。src 优先取标签
			// 自身，缺省找子 <source src>。视频后紧跟的说明=来源，同图注走 captionContext。
			vsrc := getAttr(n, "src")
			if vsrc == "" {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.ElementNode && strings.ToLower(c.Data) == "source" {
						if s := getAttr(c, "src"); s != "" {
							vsrc = s
							break
						}
					}
				}
			}
			if vsrc == "" {
				return
			}
			if cur != nil && hasContent(cur.runs) {
				w.emit(cur)
				cur.runs = nil
				cur.textStyles = nil
			}
			w.appendBlock(Block{K: "video", Src: vsrc, Poster: getAttr(n, "poster")})
			w.captionContext = true
			w.captionLines = 0
			w.inRefSection = false
			return
		case "table":
			// 表格保结构：拦在通用 block 拍平之前。先冲掉未结块，再整表走 walkTable。
			if cur != nil && hasContent(cur.runs) {
				w.emit(cur)
				cur.runs = nil
				cur.textStyles = nil
			}
			w.closeCaptionContext()
			w.inRefSection = false
			w.walkTable(n, inherited)
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
			boldStyle := inherited
			boldStyle.FontWeightBold = true
			boldStyle.BoldTag = true
			w.walkChildren(n, cur, boldStyle)
			return
		}

		nodeStyle := parseInlineStyle(getAttr(n, "style"))
		merged := mergeStyle(inherited, nodeStyle)

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
			}
			w.walkChildren(n, child, merged)
			w.emit(child)
			return
		}

		w.walkChildren(n, cur, merged)
		return
	}

	w.walkChildren(n, cur, inherited)
}

func (w *blockWalker) walkChildren(n *html.Node, cur *blockCtx, style computedStyle) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.walk(c, cur, style)
	}
}

// walkTable builds a structured K=="table" Block from a <table> subtree,
// preserving the row/cell grid the generic block walker would otherwise
// flatten into one paragraph per cell. Header cells are <th> or any cell
// inside <thead>.
func (w *blockWalker) walkTable(n *html.Node, inherited computedStyle) {
	var rows [][]TableCell
	var walkRows func(node *html.Node, inThead bool)
	walkRows = func(node *html.Node, inThead bool) {
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch strings.ToLower(c.Data) {
			case "thead":
				walkRows(c, true)
			case "tbody", "tfoot", "colgroup":
				walkRows(c, false)
			case "tr":
				if row := w.buildTableRow(c, inThead, inherited); len(row) > 0 {
					rows = append(rows, row)
				}
			default:
				walkRows(c, inThead)
			}
		}
	}
	walkRows(n, false)
	if len(rows) > 0 {
		w.appendBlock(Block{K: "table", Rows: rows})
	}
}

func (w *blockWalker) buildTableRow(tr *html.Node, inThead bool, inherited computedStyle) []TableCell {
	var cells []TableCell
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		tag := strings.ToLower(c.Data)
		if tag != "td" && tag != "th" {
			continue
		}
		cell := &blockCtx{style: mergeStyle(inherited, parseInlineStyle(getAttr(c, "style")))}
		w.collectCellRuns(c, cell, cell.style)
		cells = append(cells, TableCell{Runs: normalizeRuns(cell.runs), Header: inThead || tag == "th"})
	}
	return cells
}

// collectCellRuns gathers inline runs from a table-cell subtree WITHOUT
// emitting separate blocks. Block-level descendants (section/p/div) are
// flattened into the cell's runs; <br> becomes a space (table cells are
// single-line in practice); <img> inside cells is dropped.
func (w *blockWalker) collectCellRuns(n *html.Node, cur *blockCtx, inherited computedStyle) {
	switch n.Type {
	case html.TextNode:
		if cur.runs == nil && strings.TrimSpace(n.Data) == "" {
			return
		}
		cur.runs = append(cur.runs, w.makeTextRun(cur, n.Data, inherited))
		return
	case html.ElementNode:
		switch strings.ToLower(n.Data) {
		case "script", "style", "noscript", "iframe", "img":
			return
		case "br":
			cur.runs = append(cur.runs, Run{Type: "txt", Text: " "})
			return
		case "a":
			href := getAttr(n, "href")
			if href != "" && !strings.HasPrefix(href, "javascript:") {
				cur.hrefStack = append(cur.hrefStack, href)
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					w.collectCellRuns(c, cur, inherited)
				}
				cur.hrefStack = cur.hrefStack[:len(cur.hrefStack)-1]
				return
			}
		case "strong", "b":
			boldStyle := inherited
			boldStyle.FontWeightBold = true
			boldStyle.BoldTag = true
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				w.collectCellRuns(c, cur, boldStyle)
			}
			return
		}
		nodeStyle := parseInlineStyle(getAttr(n, "style"))
		merged := mergeStyle(inherited, nodeStyle)
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			w.collectCellRuns(c, cur, merged)
		}
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.collectCellRuns(c, cur, inherited)
	}
}

func (w *blockWalker) makeTextRun(cur *blockCtx, text string, style computedStyle) Run {
	bold := style.FontWeightBold
	if len(cur.hrefStack) > 0 {
		return Run{Type: "link", Text: text, Href: cur.hrefStack[len(cur.hrefStack)-1], Bold: bold}
	}
	return Run{Type: "txt", Text: text, Bold: bold}
}

// ---- Classification ----

func (w *blockWalker) emit(cx *blockCtx) {
	runs := normalizeRuns(cx.runs)
	if !hasContent(runs) {
		if cx.hadBreak && len(w.blocks) > 0 {
			w.pendingBlankBefore = true
			w.closeCaptionContext()
			w.inRefSection = false
		}
		return
	}

	text := textFromRuns(runs)
	textLen := utf8.RuneCountInString(text)
	allBold := allRunsBold(runs)

	if isHeadingTag(cx.tag) {
		w.appendBlock(Block{K: "h", Text: text})
		w.closeCaptionContext()
		return
	}

	for _, c := range cx.classes {
		lc := strings.ToLower(c)
		if lc == "rich_media_title" || lc == "js_article_title" {
			w.appendBlock(Block{K: "h", Text: text})
			w.closeCaptionContext()
			return
		}
	}

	// Visual-signal heading: font-size >= 18px 且 <= 80 字，并且整块有明确标题信号。
	// 微信小标题常被外层 <b>/<strong> 包住，但 leaf<span> 又内联 font-weight:normal；
	// computed bold 会被 normal 覆盖为 false，所以同时保留 BoldTag 痕迹来识别这种标题。
	// 少数真实章节标题没有 <b>，但仍是「居中 + 大字号 + 冒号标题」；该兜底必须排除
	// 图片来源、参考来源和强图注样式，避免回到“所有居中大字都是标题”的宽判定。
	if hasHeadingFontSize(cx) && textLen > 0 && textLen <= 80 &&
		(allBold || allRunsUnderBoldTag(cx) || isCenteredLargeHeading(cx, text, textLen)) {
		w.appendBlock(Block{K: "h", Text: text})
		w.closeCaptionContext()
		return
	}

	// 参考来源区：「参考来源：」/「参考资料：」头开启该区；区内 [n] 条目与
	// 「—说明」延续行强制走 txt（不进 caption 分支），避免被图注启发式误吞拆散。
	trimmed := strings.TrimSpace(text)
	if refSectionHeadRe.MatchString(trimmed) {
		w.inRefSection = true
		w.closeCaptionContext()
		w.appendBlock(Block{K: "txt", Runs: runs})
		return
	}
	if w.inRefSection {
		if refItemRe.MatchString(trimmed) || refContinuationRe.MatchString(trimmed) {
			w.closeCaptionContext()
			w.appendBlock(Block{K: "txt", Runs: runs})
			return
		}
		// 既非条目也非说明行 → 参考来源区结束
		w.inRefSection = false
	}

	if w.captionContext && shouldAttachToCaptionGroup(cx, text, textLen, w.captionLines, w.opts) {
		w.appendBlock(Block{K: "cap", Text: text})
		w.captionLines++
		return
	}

	switch cx.tag {
	case "blockquote":
		w.appendBlock(Block{K: "bq", Runs: runs})
	case "li":
		w.appendBlock(Block{K: "li", Runs: runs})
	default:
		w.appendBlock(Block{K: "txt", Runs: runs})
	}
	w.closeCaptionContext()
}

// isCenteredLargeHeading: 兜底识别「没有任何加粗痕迹」的章节小标题。
// 判据用微信排版的第一性信号，不猜作者是否加了 <b>、是否带冒号：
//
//	居中 + 字号 ≥18px（外层 hasHeadingFontSize 已保证，严格大于正文 15–17px）+ 短
//	（≤40 字）+ 不是图片来源行 / 参考来源头 / 强图注样式（小字或灰色）。
//
// 历史教训：早期版本额外要求「文本含冒号」或「run 全在 <b> 下」，这两条都是某一篇
// 样例文章的巧合属性（作者恰好那样写），换一篇没冒号、没加粗的真标题就漏判——
// 「四大品牌差异化竞争策略拆解」正是被那条冒号闸门挡在门外。根因与防再犯协议见
// openspec/changes/robust-wechat-heading-detection/design.md。
func isCenteredLargeHeading(cx *blockCtx, text string, textLen int) bool {
	trimmed := strings.TrimSpace(text)
	if textLen == 0 || textLen > 40 {
		return false
	}
	// 章节标题是「短语标签」，不是完整句子：以句号/逗号/分号收尾的是居中金句/导语/强调整句，
	// 不升级为标题（与 DOCX 侧 isBoldHeading 判据一致）。问号/感叹号放行——疑问/感叹式章节
	// 标题真实存在（如「毕业20年的老粉凭什么"回坑"？」）。
	if sentenceEndsLikeBody(trimmed) {
		return false
	}
	if isImageSourceLine(trimmed) || refSectionHeadRe.MatchString(trimmed) || hasStrongCaptionStyle(cx) {
		return false
	}
	return isTextCentered(cx)
}

// sentenceEndsLikeBody 报告文本是否以「正文句子」的收尾标点结束（句号/逗号/顿号/分号）。
// 标题不这样收尾；居中金句、导语、强调整句会。放行 ？！——疑问/感叹标题真实存在。
func sentenceEndsLikeBody(trimmed string) bool {
	return sentenceEndRe.MatchString(trimmed)
}

var sentenceEndRe = regexp.MustCompile(`[。.．，,、；;]["'”’）)】]*$`)

func isTextCentered(cx *blockCtx) bool {
	if cx.style.Align == "center" {
		return true
	}
	if len(cx.runs) == 0 || len(cx.runs) != len(cx.textStyles) {
		return false
	}
	sawText := false
	for i, r := range cx.runs {
		if strings.TrimSpace(r.Text) == "" {
			continue
		}
		if cx.textStyles[i].Align != "center" {
			return false
		}
		sawText = true
	}
	return sawText
}

// hasHeadingFontSize reports whether every non-empty text run is visually larger
// than normal WeChat body text. WeChat often puts font-size on inner spans, so
// the block-level style alone is not enough.
func hasHeadingFontSize(cx *blockCtx) bool {
	if cx.style.FontSizeSet && cx.style.FontSize >= 18 {
		return true
	}
	if len(cx.runs) == 0 || len(cx.runs) != len(cx.textStyles) {
		return false
	}
	sawText := false
	for i, r := range cx.runs {
		if strings.TrimSpace(r.Text) == "" {
			continue
		}
		s := cx.textStyles[i]
		if !s.FontSizeSet || s.FontSize < 18 {
			return false
		}
		sawText = true
	}
	return sawText
}

func (w *blockWalker) appendBlock(b Block) {
	if w.pendingBlankBefore && len(w.blocks) > 0 {
		b.BlankBefore = true
	}
	w.pendingBlankBefore = false
	w.blocks = append(w.blocks, b)
}

func (w *blockWalker) closeCaptionContext() {
	w.captionContext = false
	w.captionLines = 0
}

func shouldAttachToCaptionGroup(cx *blockCtx, text string, textLen int, captionLines int, opts articleBlockOptions) bool {
	if captionLines == 0 {
		return isCaption(cx, text, textLen, opts)
	}
	return isCaptionContinuation(cx, text, textLen, opts)
}

func isCaption(cx *blockCtx, text string, textLen int, opts articleBlockOptions) bool {
	if textLen == 0 {
		return false
	}
	if isImageSourceLine(text) {
		return true
	}
	// 小字/灰色是公众号图注的明确标记（正文从不用小字/灰色），带此强信号的图后段落
	// 即便较长也是图注，放宽到 CaptionStrongMaxChars。仅居中/class 等弱信号仍守
	// CaptionMaxChars，避免吞掉较长的居中正文段。
	if hasStrongCaptionStyle(cx) {
		return textLen <= opts.CaptionStrongMaxChars
	}
	// 大字号是标题/正文强调信号，不应仅因“居中+短”被弱图注启发式吞掉。
	if hasHeadingFontSize(cx) {
		return false
	}
	if textLen > opts.CaptionMaxChars {
		return false
	}
	for _, c := range cx.classes {
		if classSignalsCaption(c) {
			return true
		}
	}
	if styleSignalsCaption(cx.style, textLen, opts) {
		return true
	}
	// Also scan the styles observed at each text node. WeChat puts the
	// caption's font-size/color on an inner <span>, not on the enclosing
	// <p>, so the block-level style often carries no signal even though
	// the visible style clearly marks the text as a caption.
	for _, s := range cx.textStyles {
		if styleSignalsCaption(s, textLen, opts) {
			return true
		}
	}
	return false
}

func isCaptionContinuation(cx *blockCtx, text string, textLen int, opts articleBlockOptions) bool {
	if textLen == 0 {
		return false
	}
	if isImageSourceLine(text) {
		return true
	}
	// 与首行一致：带小字/灰色强信号的续行图注放宽到 CaptionStrongMaxChars。
	if hasStrongCaptionStyle(cx) {
		return textLen <= opts.CaptionStrongMaxChars
	}
	if textLen > opts.CaptionMaxChars {
		return false
	}
	for _, c := range cx.classes {
		if classSignalsCaption(c) {
			return true
		}
	}
	return false
}

// hasStrongCaptionStyle 报告块级或文本节点样式是否带明确图注标记（小字号或灰色）。
// 居中被刻意排除——正文也可能居中，居中只是弱信号。
func hasStrongCaptionStyle(cx *blockCtx) bool {
	if strongStyleSignalsCaption(cx.style) {
		return true
	}
	for _, s := range cx.textStyles {
		if strongStyleSignalsCaption(s) {
			return true
		}
	}
	return false
}

func classSignalsCaption(className string) bool {
	lc := strings.ToLower(className)
	return strings.Contains(lc, "caption") ||
		strings.Contains(lc, "img_des") ||
		strings.Contains(lc, "imgdesc")
}

func isImageSourceLine(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.TrimLeft(t, "（([【 ")
	return strings.HasPrefix(t, "图片来源") ||
		strings.HasPrefix(t, "图源") ||
		strings.HasPrefix(t, "图片源") ||
		strings.HasPrefix(t, "image source") ||
		strings.HasPrefix(t, "photo source") ||
		strings.HasPrefix(t, "source:")
}

func styleSignalsCaption(s computedStyle, textLen int, opts articleBlockOptions) bool {
	if s.FontSizeSet && s.FontSize > 0 && s.FontSize <= 14 {
		return true
	}
	if s.ColorSet && isGreyHex(s.Color) {
		return true
	}
	if s.Align == "center" && textLen <= opts.CaptionCenterMaxChars {
		return true
	}
	return false
}

func strongStyleSignalsCaption(s computedStyle) bool {
	if s.FontSizeSet && s.FontSize > 0 && s.FontSize <= 14 {
		return true
	}
	if s.ColorSet && isGreyHex(s.Color) {
		return true
	}
	return false
}

// ---- Style parsing ----

type parsedStyle struct {
	computedStyle
	fontWeightSet  bool
	fontWeightBold bool
}

// parseInlineStyle walks `a: b; c: d` declarations. Splitting on `;` up
// front (instead of running regexes over the whole string) makes it
// impossible for a substring like `background-color` to accidentally
// match the `color` rule, and makes per-property logic trivial.
// 参考来源识别：头部「参考来源：/参考资料：」、条目「[n]」、说明延续行「— – ─ -」开头。
var refSectionHeadRe = regexp.MustCompile(`^参考(?:来源|资料)[：:]?\s*$`)
var refItemRe = regexp.MustCompile(`^\[\d+\]`)
var refContinuationRe = regexp.MustCompile(`^[—–─-]`)

var reFontSizeValue = regexp.MustCompile(`^\s*([0-9]*\.?[0-9]+)\s*(px|pt|em|rem|%)?`)
var reFontWeightBoldValue = regexp.MustCompile(`^(bold|bolder|[6-9]\d\d)\b`)
var reFontWeightNormalValue = regexp.MustCompile(`^(normal|lighter|[1-5]\d\d)\b`)

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
						// 点→像素按 96dpi 精确换算（96/72），不要用截断的 1.333：
						// 13.5pt 精确等于 18px，但 13.5*1.333=17.9955 会漏在 ≥18 标题阈值之外。
						out.FontSize = v * 96.0 / 72.0
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
			lower := strings.ToLower(val)
			if reFontWeightBoldValue.MatchString(lower) {
				out.fontWeightSet = true
				out.fontWeightBold = true
			} else if reFontWeightNormalValue.MatchString(lower) {
				out.fontWeightSet = true
				out.fontWeightBold = false
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
	if child.fontWeightSet {
		out.FontWeightBold = child.fontWeightBold
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

// allRunsUnderBoldTag 报告块内所有非空文本 run 是否都处在 <b>/<strong> 祖先下（BoldTag，
// 不被内联 font-weight:normal 覆盖）。用于区分「整块加粗的小标题」（即便视觉加粗被 leaf
// <span> 的 normal 抹掉、allBold=false）与「只夹了个加粗词的居中短正文」——后者并非全部
// run 在 <b> 下，不应升级为标题。runs 与 textStyles 长度不齐（含 <br> 等）时保守返回
// false，交由 allBold 主路径判断。
func allRunsUnderBoldTag(cx *blockCtx) bool {
	if len(cx.runs) == 0 || len(cx.runs) != len(cx.textStyles) {
		return false
	}
	saw := false
	for i, r := range cx.runs {
		if strings.TrimSpace(r.Text) == "" {
			continue
		}
		if !cx.textStyles[i].BoldTag {
			return false
		}
		saw = true
	}
	return saw
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
