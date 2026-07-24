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
	// 小字号/灰色的图注允许使用更高的最终字数上限。
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
	// 小字/灰色图注的放宽上限不应比基础上限还严。
	if opts.CaptionStrongMaxChars < opts.CaptionMaxChars {
		opts.CaptionStrongMaxChars = opts.CaptionMaxChars
	}
	return opts
}

// 公众号文章页的结构化元数据锚点（2026-07-10 真实页面实测，M7）：
// 公众号名在 `var nickname = htmlDecode("...")`（htmlDecode 包裹可有可无），
// 作者署名在 `<meta name="author" content="...">` 与 `var author = "..."` 双写。
// 这是发布者自己填的结构化字段——确定性规则直取，比模型从正文里猜准确（规则五）。
var (
	wxNicknameRe   = regexp.MustCompile(`var nickname = (?:htmlDecode\()?"([^"]*)"`)
	wxAuthorMetaRe = regexp.MustCompile(`<meta\s+name="author"\s+content="([^"]*)"`)
	wxAuthorVarRe  = regexp.MustCompile(`var author = "([^"]*)"`)
)

// extractWechatArticleMeta 公众号页面原始 HTML → (公众号名, 作者署名)。
// 没署名的文章两个作者锚点都是空串——空值原样返回（空也是有效信号，宁空勿造）。
func extractWechatArticleMeta(rawHTML string) (nickname, byline string) {
	if m := wxNicknameRe.FindStringSubmatch(rawHTML); len(m) > 1 {
		nickname = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if m := wxAuthorMetaRe.FindStringSubmatch(rawHTML); len(m) > 1 {
		byline = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if byline == "" {
		if m := wxAuthorVarRe.FindStringSubmatch(rawHTML); len(m) > 1 {
			byline = strings.TrimSpace(html.UnescapeString(m[1]))
		}
	}
	return nickname, byline
}

// extractArticleBlocks walks WeChat article HTML into a sequence of Blocks.
// Unlike extractArticle (html2md.go), it preserves the visual signals
// (font-size, color, align, bold) needed to distinguish heading vs pullquote
// vs caption vs body — classification that Markdown can no longer represent.
func extractArticleBlocks(rawHTML string) (title string, blocks []Block, author string) {
	return extractArticleBlocksWithOptions(rawHTML, defaultArticleBlockOptions())
}

func extractArticleBlocksWithOptions(rawHTML string, opts articleBlockOptions) (title string, blocks []Block, author string) {
	title, decisions, author := extractArticleRoleDecisionsWithOptions(rawHTML, opts)
	return title, blocksFromRoleDecisions(decisions), author
}

func extractArticleRoleDecisionsForTest(rawHTML string) []RoleDecision {
	_, decisions, _ := extractArticleRoleDecisionsWithOptions(rawHTML, defaultArticleBlockOptions())
	return decisions
}

func extractObservedBlocksForTest(rawHTML string) []ObservedBlock {
	return observeArticleHTML(rawHTML)
}

func extractArticleRoleDecisionsWithOptions(rawHTML string, opts articleBlockOptions) (title string, decisions []RoleDecision, author string) {
	opts = normalizeArticleBlockOptions(opts)
	title = extractMeta(rawHTML, "og:title")
	if title == "" {
		if m := regexp.MustCompile(`<title[^>]*>([^<]+)</title>`).FindStringSubmatch(rawHTML); len(m) > 1 {
			title = strings.TrimSpace(html.UnescapeString(m[1]))
		}
	}

	observed := observeArticleHTML(rawHTML)
	profile := buildArticleProfile(observed)
	explicitBindings := proveCaptionBindings(projectCaptionStructure(observed))
	bindings := inferCompatibleCaptionBindings(observed, profile, explicitBindings, opts)
	bindings = applyCaptionLengthLimits(observed, bindings, opts)
	decisions = resolveRoleDecisions(observed, profile, bindings)
	return title, decisions, ""
}

func observeArticleHTML(rawHTML string) []ObservedBlock {
	contentHTML := resolveContentHTML(rawHTML)

	contentHTML = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`).ReplaceAllString(contentHTML, "")
	contentHTML = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`).ReplaceAllString(contentHTML, "")
	contentHTML = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(contentHTML, "")

	doc, err := html.Parse(strings.NewReader(contentHTML))
	if err != nil {
		return nil
	}

	w := newBlockWalker(doc)
	w.walk(doc, nil, computedStyle{FontSize: 16})
	return w.observed
}

// ---- Walker ----

type CaptionProof uint8

const (
	CaptionProofNone CaptionProof = iota
	CaptionProofFigcaption
	CaptionProofAriaDescribedBy
	CaptionProofLegacyAssociation
)

type captionDOMIndex struct {
	nodeIDs          map[*html.Node]int
	mediaIDs         map[*html.Node]int
	figureMediaIDs   map[*html.Node][]int
	describedByMedia map[string][]int
	elementIDCounts  map[string]int
}

func newBlockWalker(root *html.Node) *blockWalker {
	return &blockWalker{captionIndex: buildCaptionDOMIndex(root)}
}

func buildCaptionDOMIndex(root *html.Node) *captionDOMIndex {
	idx := &captionDOMIndex{
		nodeIDs:          map[*html.Node]int{},
		mediaIDs:         map[*html.Node]int{},
		figureMediaIDs:   map[*html.Node][]int{},
		describedByMedia: map[string][]int{},
		elementIDCounts:  map[string]int{},
	}
	nextNodeID := 1
	nextMediaID := 1
	var indexNodes func(*html.Node, *html.Node)
	indexNodes = func(n *html.Node, figure *html.Node) {
		if n.Type == html.ElementNode {
			idx.nodeIDs[n] = nextNodeID
			nextNodeID++
			if elementID := strings.TrimSpace(getAttr(n, "id")); elementID != "" {
				idx.elementIDCounts[elementID]++
			}
			if strings.EqualFold(n.Data, "figure") {
				figure = n
			}
			if _, src := mediaSourceForNode(n); src != "" {
				idx.mediaIDs[n] = nextMediaID
				if figure != nil {
					idx.figureMediaIDs[figure] = append(idx.figureMediaIDs[figure], nextMediaID)
				}
				nextMediaID++
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			indexNodes(c, figure)
		}
	}
	indexNodes(root, nil)

	for node, mediaID := range idx.mediaIDs {
		for _, targetID := range strings.Fields(getAttr(node, "aria-describedby")) {
			idx.describedByMedia[targetID] = appendUniqueInt(idx.describedByMedia[targetID], mediaID)
		}
	}
	return idx
}

func mediaSourceForNode(n *html.Node) (kind, src string) {
	if n == nil || n.Type != html.ElementNode {
		return "", ""
	}
	switch strings.ToLower(n.Data) {
	case "img":
		src = getAttr(n, "data-src")
		if src == "" {
			src = getAttr(n, "src")
		}
		if src == "" || strings.Contains(src, "data:image") {
			return "", ""
		}
		return "img", src
	case "video":
		src = getAttr(n, "src")
		if src == "" {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && strings.EqualFold(c.Data, "source") {
					if candidate := getAttr(c, "src"); candidate != "" {
						src = candidate
						break
					}
				}
			}
		}
		if src != "" {
			return "video", src
		}
	}
	return "", ""
}

func appendUniqueInt(values []int, value int) []int {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func nearestElement(n *html.Node, tag string) *html.Node {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Type == html.ElementNode && strings.EqualFold(cur.Data, tag) {
			return cur
		}
	}
	return nil
}

func singleMediaID(ids []int) (int, bool) {
	if len(ids) != 1 || ids[0] <= 0 {
		return 0, false
	}
	return ids[0], true
}

func (idx *captionDOMIndex) captionEvidenceForNode(n *html.Node) (proof CaptionProof, scopeID, mediaID int) {
	if idx == nil || n == nil {
		return CaptionProofNone, 0, 0
	}
	if figcaption := nearestElement(n, "figcaption"); figcaption != nil {
		if figure := nearestElement(figcaption.Parent, "figure"); figure != nil {
			if boundMediaID, ok := singleMediaID(idx.figureMediaIDs[figure]); ok {
				return CaptionProofFigcaption, idx.nodeIDs[figcaption], boundMediaID
			}
		}
	}
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Type != html.ElementNode {
			continue
		}
		targetID := strings.TrimSpace(getAttr(cur, "id"))
		if targetID == "" || idx.elementIDCounts[targetID] != 1 {
			continue
		}
		if boundMediaID, ok := singleMediaID(idx.describedByMedia[targetID]); ok {
			return CaptionProofAriaDescribedBy, idx.nodeIDs[cur], boundMediaID
		}
	}
	return CaptionProofNone, 0, 0
}

type blockWalker struct {
	blocks             []Block
	observed           []ObservedBlock
	pendingBlankBefore bool
	captionIndex       *captionDOMIndex
}

type blockCtx struct {
	node      *html.Node
	tag       string
	classes   []string
	style     computedStyle
	runs      []Run
	hadBreak  bool
	hrefStack []string
	// computed style at each text node; WeChat commonly puts visual styling on
	// an inner span rather than the enclosing block.
	textStyles []computedStyle
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
			_, src := mediaSourceForNode(n)
			if src == "" {
				return
			}
			if cur != nil && hasContent(cur.runs) {
				w.emit(cur)
				cur.runs = nil
				cur.textStyles = nil
			}
			w.appendImageBlock(n, Block{
				K:         "img",
				Src:       src,
				DataW:     firstNonEmpty(getAttr(n, "data-w"), getAttr(n, "width")),
				DataH:     firstNonEmpty(getAttr(n, "data-h"), getAttr(n, "height")),
				DataRatio: getAttr(n, "data-ratio"),
			})
			return
		case "video":
			// FoodTalks 网站文章视频 = <video src=mp4 poster=jpg>（成品口径）。
			_, vsrc := mediaSourceForNode(n)
			if vsrc == "" {
				return
			}
			if cur != nil && hasContent(cur.runs) {
				w.emit(cur)
				cur.runs = nil
				cur.textStyles = nil
			}
			w.appendImageBlock(n, Block{K: "video", Src: vsrc, Poster: getAttr(n, "poster")})
			return
		case "table":
			// 表格保结构：拦在通用 block 拍平之前。先冲掉未结块，再整表走 walkTable。
			if cur != nil && hasContent(cur.runs) {
				w.emit(cur)
				cur.runs = nil
				cur.textStyles = nil
			}
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
				node:    n,
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
		}
		return
	}

	switch cx.tag {
	case "blockquote":
		w.appendTextBlock(cx, Block{K: "bq", Runs: runs})
	case "li":
		w.appendTextBlock(cx, Block{K: "li", Runs: runs})
	default:
		w.appendTextBlock(cx, Block{K: "txt", Runs: runs})
	}
}

// isCenteredLargeHeadingObservation: 兜底识别「没有任何加粗痕迹」的章节小标题。
// 判据用微信排版的第一性信号，不猜作者是否加了 <b>、是否带冒号：
//
//	居中 + 字号 ≥18px（外层 hasHeadingFontSize 已保证，严格大于正文 15–17px）+ 短
//	（≤40 字）+ 不是图片来源行 / 参考来源头 / 小字灰色内容。
//
// 历史教训：早期版本额外要求「文本含冒号」或「run 全在 <b> 下」，这两条都是某一篇
// 样例文章的巧合属性（作者恰好那样写），换一篇没冒号、没加粗的真标题就漏判——
// 「四大品牌差异化竞争策略拆解」正是被那条冒号闸门挡在门外。根因与防再犯协议见
// openspec/specs/wechat-heading-detection/spec.md。
func isCenteredLargeHeadingObservation(obs ObservedBlock) bool {
	if obs.TextLen == 0 || obs.TextLen > 40 {
		return false
	}
	// 章节标题是「短语标签」，不是完整句子：以句号/逗号/分号收尾的是居中金句/导语/强调整句，
	// 不升级为标题（与 DOCX 侧 isBoldHeading 判据一致）。问号/感叹号放行——疑问/感叹式章节
	// 标题真实存在（如「毕业20年的老粉凭什么"回坑"？」）。
	if sentenceEndsLikeBody(obs.Text) {
		return false
	}
	if obs.ImageSourceLine || obs.ReferenceHead || obs.MutedOrSmallStyle {
		return false
	}
	return obs.Centered
}

// sentenceEndsLikeBody 报告文本是否以「正文句子」的收尾标点结束（句号/逗号/顿号/分号）。
// 标题不这样收尾；居中金句、导语、强调整句会。放行 ？！——疑问/感叹标题真实存在。
func sentenceEndsLikeBody(trimmed string) bool {
	return sentenceEndRe.MatchString(trimmed)
}

var sentenceEndRe = regexp.MustCompile(`[。.．，,、；;]["'”’）)】]*$`)

// sentenceEndsLikeProse is deliberately broader than sentenceEndsLikeBody for
// article-local body-flow profiling. Question/exclamation marks remain valid
// section-title endings, so the heading-specific predicate stays narrower.
func sentenceEndsLikeProse(trimmed string) bool {
	return proseSentenceEndRe.MatchString(strings.TrimSpace(trimmed))
}

var proseSentenceEndRe = regexp.MustCompile(`[。.．，,、；;？！!?]["'”’）)】]*$`)

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

// ObservedBlock is the internal fact layer for URL imports. It records what
// the DOM said in order, but it is not the final public h/txt/cap decision.
type ObservedBlock struct {
	Index             int
	BaseBlock         Block
	RawKind           string
	NodeID            int
	MediaID           int
	CaptionScopeID    int
	CaptionProof      CaptionProof
	Tag               string
	Classes           []string
	Style             computedStyle
	TextStyles        []computedStyle
	Text              string
	TextLen           int
	Centered          bool
	HeadingFontSize   bool
	AllBold           bool
	AllUnderBoldTag   bool
	MutedOrSmallStyle bool
	ImageSourceLine   bool
	ReferenceHead     bool
	ReferenceItem     bool
	ReferenceContinue bool
	BodyLikeEnding    bool
	ImageRatio        float64
	// ContinuesVisualHeading records an explicit source-DOM line break inside
	// one visual heading. WeChat editors commonly split a two-line heading into
	// sibling <section> nodes and set margin-bottom:0 on every line except the
	// last. The resolver can then join only those lines without collapsing
	// genuine adjacent h2/h3 headings.
	ContinuesVisualHeading bool
}

type articleProfile struct {
	bodyFlow      map[int]bool
	refFlow       map[int]bool
	headingSignal map[int]bool
	bodyVisuals   map[textVisualSignature]bool
}

// textVisualSignature captures the article-local body typography that matters
// to caption compatibility. Caption-looking values such as 14px or #555 are
// not treated as special when the article uses the same values for its body.
type textVisualSignature struct {
	FontSize float64
	Color    string
	Align    string
}

type CaptionBinding struct {
	TextIndex int
	MediaID   int
	ScopeID   int
	Proof     CaptionProof
	Reason    string
}

// captionStructureObservation is a deliberate type barrier: the proof stage
// cannot access text, classes, color, font size, alignment or adjacency.
type captionStructureObservation struct {
	TextIndex int
	MediaID   int
	ScopeID   int
	Proof     CaptionProof
}

type RoleDecision struct {
	Index        int
	Role         string
	Group        string
	Reason       string
	MediaID      int
	CaptionScope int
	CaptionProof CaptionProof
	Block        Block
}

func (w *blockWalker) appendBlock(b Block) {
	w.appendObservedBlock(b, ObservedBlock{})
}

func (w *blockWalker) appendTextBlock(cx *blockCtx, b Block) {
	text := b.Text
	if text == "" {
		text = textFromRuns(b.Runs)
	}
	trimmed := strings.TrimSpace(text)
	proof, scopeID, mediaID := w.captionIndex.captionEvidenceForNode(cx.node)
	obs := ObservedBlock{
		RawKind:                "text",
		NodeID:                 w.captionIndex.nodeIDs[cx.node],
		MediaID:                mediaID,
		CaptionScopeID:         scopeID,
		CaptionProof:           proof,
		Tag:                    cx.tag,
		Classes:                append([]string(nil), cx.classes...),
		Style:                  cx.style,
		TextStyles:             append([]computedStyle(nil), cx.textStyles...),
		Text:                   trimmed,
		TextLen:                utf8.RuneCountInString(trimmed),
		Centered:               isTextCentered(cx),
		HeadingFontSize:        hasHeadingFontSize(cx),
		AllBold:                allRunsBold(b.Runs),
		AllUnderBoldTag:        allRunsUnderBoldTag(cx),
		MutedOrSmallStyle:      hasMutedOrSmallStyle(cx),
		ImageSourceLine:        isImageSourceLine(trimmed),
		ReferenceHead:          refSectionHeadRe.MatchString(trimmed),
		ReferenceItem:          refItemRe.MatchString(trimmed),
		ReferenceContinue:      refContinuationRe.MatchString(trimmed),
		BodyLikeEnding:         sentenceEndsLikeBody(trimmed),
		ContinuesVisualHeading: hasExplicitZeroMarginBottom(cx.node),
	}
	w.appendObservedBlock(b, obs)
}

func (w *blockWalker) appendImageBlock(node *html.Node, b Block) {
	w.appendObservedBlock(b, ObservedBlock{
		RawKind:    "media",
		NodeID:     w.captionIndex.nodeIDs[node],
		MediaID:    w.captionIndex.mediaIDs[node],
		ImageRatio: imageRatioFromBlock(b),
	})
}

func (w *blockWalker) appendObservedBlock(b Block, obs ObservedBlock) {
	if w.pendingBlankBefore && len(w.blocks) > 0 {
		b.BlankBefore = true
	}
	w.pendingBlankBefore = false
	w.blocks = append(w.blocks, b)
	obs.Index = len(w.observed)
	obs.BaseBlock = b
	if obs.RawKind == "" {
		switch b.K {
		case "img", "video":
			obs.RawKind = "media"
			obs.ImageRatio = imageRatioFromBlock(b)
		case "table":
			obs.RawKind = "table"
		default:
			obs.RawKind = "text"
			if obs.Text == "" {
				obs.Text = b.Text
				if obs.Text == "" {
					obs.Text = textFromRuns(b.Runs)
				}
				obs.Text = strings.TrimSpace(obs.Text)
				obs.TextLen = utf8.RuneCountInString(obs.Text)
			}
		}
	}
	w.observed = append(w.observed, obs)
}

func imageRatioFromBlock(b Block) float64 {
	if r := parsePositiveFloat(b.DataRatio); r > 0 {
		return r
	}
	w := parsePositiveFloat(b.DataW)
	h := parsePositiveFloat(b.DataH)
	if w > 0 && h > 0 {
		return h / w
	}
	return 0
}

func parsePositiveFloat(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v <= 0 {
		return 0
	}
	return v
}

func standaloneHeadingReason(obs ObservedBlock) (string, bool) {
	if isHeadingTag(obs.Tag) {
		return "heading-tag", true
	}
	for _, className := range obs.Classes {
		lc := strings.ToLower(className)
		if lc == "rich_media_title" || lc == "js_article_title" {
			return "heading-class", true
		}
	}
	if !obs.HeadingFontSize || obs.TextLen == 0 || obs.TextLen > 80 {
		return "", false
	}
	if obs.AllBold || obs.AllUnderBoldTag || isCenteredLargeHeadingObservation(obs) {
		return "visual-heading", true
	}
	return "", false
}

func isStandaloneHeadingObservation(obs ObservedBlock) bool {
	_, ok := standaloneHeadingReason(obs)
	return ok
}

func buildArticleProfile(observed []ObservedBlock) articleProfile {
	profile := articleProfile{
		bodyFlow:      map[int]bool{},
		refFlow:       map[int]bool{},
		headingSignal: map[int]bool{},
		bodyVisuals:   map[textVisualSignature]bool{},
	}
	inRefSection := false
	for _, obs := range observed {
		if obs.RawKind != "text" {
			continue
		}
		if hasSectionHeaderSignal(obs) {
			profile.headingSignal[obs.Index] = true
		}
		if obs.ReferenceHead {
			inRefSection = true
			profile.refFlow[obs.Index] = true
			continue
		}
		if inRefSection {
			if obs.ReferenceItem || obs.ReferenceContinue {
				profile.refFlow[obs.Index] = true
				continue
			}
			inRefSection = false
		}
		if isBodyFlowObservation(obs) {
			profile.bodyFlow[obs.Index] = true
		}
	}
	for i, obs := range observed {
		if !profile.bodyFlow[obs.Index] || obs.CaptionProof != CaptionProofNone ||
			!sentenceEndsLikeProse(obs.Text) {
			continue
		}
		// A long, styled caption can itself look like prose. Do not let the
		// first text immediately following a media item define the body style;
		// learn it from the surrounding article instead.
		if previous := previousMeaningfulIndex(observed, i); previous >= 0 && observed[previous].RawKind == "media" {
			continue
		}
		profile.bodyVisuals[visualSignatureForObservation(obs)] = true
	}
	return profile
}

func visualSignatureForObservation(obs ObservedBlock) textVisualSignature {
	style := obs.Style
	for _, textStyle := range obs.TextStyles {
		if textStyle.FontSizeSet {
			style.FontSize = textStyle.FontSize
			style.FontSizeSet = true
		}
		if textStyle.ColorSet {
			style.Color = textStyle.Color
			style.ColorSet = true
		}
		if textStyle.Align != "" {
			style.Align = textStyle.Align
		}
	}
	align := strings.ToLower(strings.TrimSpace(style.Align))
	if align == "" || align == "start" {
		align = "left"
	}
	return textVisualSignature{
		FontSize: style.FontSize,
		Color:    strings.ToLower(strings.TrimSpace(style.Color)),
		Align:    align,
	}
}

func matchesArticleBodyVisual(obs ObservedBlock, profile articleProfile) bool {
	return profile.bodyVisuals[visualSignatureForObservation(obs)]
}

func projectCaptionStructure(observed []ObservedBlock) []captionStructureObservation {
	structure := make([]captionStructureObservation, 0, len(observed))
	for _, obs := range observed {
		if obs.RawKind != "text" {
			continue
		}
		structure = append(structure, captionStructureObservation{
			TextIndex: obs.Index,
			MediaID:   obs.MediaID,
			ScopeID:   obs.CaptionScopeID,
			Proof:     obs.CaptionProof,
		})
	}
	return structure
}

// proveCaptionBindings is intentionally restricted by its input type to DOM
// relationships. Visual style, text content, adjacency and class names cannot
// be consulted here and therefore cannot create cap.
func proveCaptionBindings(structure []captionStructureObservation) map[int]CaptionBinding {
	bindings := map[int]CaptionBinding{}
	for _, obs := range structure {
		if obs.Proof == CaptionProofNone || obs.ScopeID <= 0 || obs.MediaID <= 0 {
			continue
		}
		bindings[obs.TextIndex] = CaptionBinding{
			TextIndex: obs.TextIndex,
			MediaID:   obs.MediaID,
			ScopeID:   obs.ScopeID,
			Proof:     obs.Proof,
			Reason:    captionProofReason(obs.Proof),
		}
	}
	return bindings
}

// inferCompatibleCaptionBindings preserves the caption forms supported before
// explicit DOM bindings were introduced. Unlike the old global caption flag,
// every inferred group is attached to one concrete media block, is stopped by
// article-local body typography, and cannot cross another media boundary.
func inferCompatibleCaptionBindings(
	observed []ObservedBlock,
	profile articleProfile,
	explicit map[int]CaptionBinding,
	opts articleBlockOptions,
) map[int]CaptionBinding {
	bindings := make(map[int]CaptionBinding, len(explicit))
	explicitMedia := map[int]bool{}
	for index, binding := range explicit {
		bindings[index] = binding
		explicitMedia[binding.MediaID] = true
	}

	for mediaIndex, media := range observed {
		if media.RawKind != "media" || media.MediaID <= 0 || explicitMedia[media.MediaID] ||
			isConfirmedSectionMarkerMedia(observed, profile, mediaIndex) {
			continue
		}
		textIndex := nextMeaningfulIndex(observed, mediaIndex)
		if textIndex < 0 {
			continue
		}
		if _, alreadyBound := bindings[textIndex]; alreadyBound {
			continue
		}
		if observed[textIndex].RawKind != "text" ||
			!isCompatibleCaptionFirstLine(media, observed[textIndex], profile, opts) {
			continue
		}

		scopeID := media.NodeID
		if scopeID <= 0 {
			scopeID = media.Index + 1
		}
		first := observed[textIndex]
		bindings[textIndex] = CaptionBinding{
			TextIndex: textIndex,
			MediaID:   media.MediaID,
			ScopeID:   scopeID,
			Proof:     CaptionProofLegacyAssociation,
			Reason:    compatibleCaptionReason(first, false),
		}
		// A source line is a natural terminal for a caption group. This keeps
		// the following body from inheriting caption state.
		if first.ImageSourceLine {
			continue
		}

		for next := nextMeaningfulIndex(observed, textIndex); next >= 0; next = nextMeaningfulIndex(observed, next) {
			if _, alreadyBound := bindings[next]; alreadyBound {
				break
			}
			candidate := observed[next]
			if candidate.RawKind != "text" || !isCompatibleCaptionContinuation(candidate, profile, opts) {
				break
			}
			bindings[next] = CaptionBinding{
				TextIndex: next,
				MediaID:   media.MediaID,
				ScopeID:   scopeID,
				Proof:     CaptionProofLegacyAssociation,
				Reason:    compatibleCaptionReason(candidate, true),
			}
			if candidate.ImageSourceLine {
				break
			}
		}
	}
	return bindings
}

func isCompatibleCaptionFirstLine(media ObservedBlock, obs ObservedBlock, profile articleProfile, opts articleBlockOptions) bool {
	if obs.RawKind != "text" || obs.TextLen == 0 || profile.refFlow[obs.Index] ||
		blocksCaptionAssociationAsArticleCredit(obs.Text) || isStandaloneHeadingObservation(obs) {
		return false
	}
	if obs.ImageSourceLine || classListSignalsCaption(obs.Classes) {
		return true
	}
	if obs.BodyLikeEnding {
		return false
	}
	if matchesArticleBodyVisual(obs, profile) {
		return false
	}
	if obs.MutedOrSmallStyle {
		return obs.TextLen <= opts.CaptionStrongMaxChars
	}
	if obs.HeadingFontSize || obs.TextLen > opts.CaptionMaxChars {
		return false
	}
	return media.ImageRatio > 0 && obs.Centered && obs.TextLen <= opts.CaptionCenterMaxChars
}

func isCompatibleCaptionContinuation(obs ObservedBlock, profile articleProfile, opts articleBlockOptions) bool {
	if obs.RawKind != "text" || obs.TextLen == 0 || profile.refFlow[obs.Index] ||
		blocksCaptionAssociationAsArticleCredit(obs.Text) || isStandaloneHeadingObservation(obs) {
		return false
	}
	if obs.ImageSourceLine || classListSignalsCaption(obs.Classes) {
		return true
	}
	if matchesArticleBodyVisual(obs, profile) {
		return false
	}
	return obs.MutedOrSmallStyle && obs.TextLen <= opts.CaptionStrongMaxChars
}

func compatibleCaptionReason(obs ObservedBlock, continuation bool) string {
	if continuation {
		return "media-caption-continuation"
	}
	if obs.ImageSourceLine {
		return "source-prefix-after-content-image"
	}
	return "media-caption"
}

func blocksCaptionAssociationAsArticleCredit(text string) bool {
	t := strings.TrimLeft(strings.TrimSpace(text), "（([【 ")
	for _, prefix := range []string{"作者", "撰文", "文", "编辑", "出品", "声明"} {
		if strings.HasPrefix(t, prefix+"：") || strings.HasPrefix(t, prefix+":") ||
			strings.HasPrefix(t, prefix+" |") || strings.HasPrefix(t, prefix+"｜") {
			return true
		}
	}
	return false
}

type captionGroupKey struct {
	MediaID int
	ScopeID int
}

type captionLimitState struct {
	Started bool
	Closed  bool
	Chars   int
	Limit   int
}

// applyCaptionLengthLimits runs after a caption has been attached to one media
// item. Once that scope exceeds either its per-block or cumulative limit, the
// rest of the scope stays txt.
func applyCaptionLengthLimits(observed []ObservedBlock, proven map[int]CaptionBinding, opts articleBlockOptions) map[int]CaptionBinding {
	bounded := map[int]CaptionBinding{}
	states := map[captionGroupKey]captionLimitState{}
	for _, obs := range observed {
		binding, ok := proven[obs.Index]
		if !ok {
			continue
		}
		key := captionGroupKey{MediaID: binding.MediaID, ScopeID: binding.ScopeID}
		state := states[key]
		if state.Closed {
			continue
		}
		blockLimit := captionTextCharLimit(obs, opts)
		if !state.Started {
			state.Started = true
			state.Limit = blockLimit
		}
		if obs.TextLen == 0 || obs.TextLen > blockLimit || state.Chars+obs.TextLen > state.Limit {
			state.Closed = true
			states[key] = state
			continue
		}
		bounded[obs.Index] = binding
		state.Chars += obs.TextLen
		states[key] = state
	}
	return bounded
}

func captionProofReason(proof CaptionProof) string {
	switch proof {
	case CaptionProofFigcaption:
		return "figcaption"
	case CaptionProofAriaDescribedBy:
		return "aria-describedby"
	case CaptionProofLegacyAssociation:
		return "media-caption"
	default:
		return ""
	}
}

func resolveRoleDecisions(observed []ObservedBlock, profile articleProfile, captions map[int]CaptionBinding) []RoleDecision {
	sectionTitles := map[int]bool{}
	for i, obs := range observed {
		if obs.RawKind != "media" || !isDecorativeMarkerCandidate(obs) {
			continue
		}
		for _, idx := range collectSectionHeaderGroup(observed, profile, i) {
			sectionTitles[idx] = true
		}
	}

	decisions := make([]RoleDecision, 0, len(observed))
	firstBody := firstBodyFlowIndex(profile)
	for _, obs := range observed {
		if obs.RawKind != "text" {
			decisions = append(decisions, RoleDecision{
				Index:  obs.Index,
				Role:   obs.BaseBlock.K,
				Group:  "Media",
				Reason: obs.RawKind,
				Block:  obs.BaseBlock,
			})
			continue
		}

		role := obs.BaseBlock.K
		reason := "default-text"
		group := defaultRoleDecisionGroup(obs, role)
		binding, isCaption := captions[obs.Index]
		if isCaption {
			role = "cap"
			group = "MediaGroup"
			reason = binding.Reason
			if reason == "" {
				reason = captionProofReason(binding.Proof)
			}
		} else if profile.refFlow[obs.Index] {
			role = "txt"
			group = "RefGroup"
			reason = "ref-section"
		} else if sectionTitles[obs.Index] {
			role = "h"
			group = "SectionHeaderGroup"
			reason = "marker-title-body"
		} else if isPrefaceMetadataLine(obs.Text) && (firstBody == -1 || obs.Index < firstBody) {
			role = "txt"
			group = "MetadataPreface"
			reason = "preface-metadata"
		} else if headingReason, ok := standaloneHeadingReason(obs); ok {
			role = "h"
			group = "StandaloneHeading"
			reason = headingReason
		} else if profile.bodyFlow[obs.Index] {
			role = "txt"
			group = "BodyFlow"
			reason = "body-flow"
		}

		decisions = append(decisions, RoleDecision{
			Index:        obs.Index,
			Role:         role,
			Group:        group,
			Reason:       reason,
			MediaID:      binding.MediaID,
			CaptionScope: binding.ScopeID,
			CaptionProof: binding.Proof,
			Block:        blockFromRoleDecision(obs, role),
		})
	}
	return mergeVisualHeadingContinuations(decisions, observed)
}

func mergeVisualHeadingContinuations(decisions []RoleDecision, observed []ObservedBlock) []RoleDecision {
	if len(decisions) < 2 {
		return decisions
	}
	out := make([]RoleDecision, 0, len(decisions))
	for i := 0; i < len(decisions); i++ {
		merged := decisions[i]
		last := i
		for last+1 < len(decisions) &&
			canMergeVisualHeadingContinuation(merged, decisions[last+1], observed, decisions[last].Index) {
			next := decisions[last+1]
			merged.Block.Text = joinVisualHeadingLines(merged.Block.Text, next.Block.Text)
			merged.Block.Runs = nil
			merged.Reason = "visual-heading-lines"
			last++
		}
		out = append(out, merged)
		i = last
	}
	return out
}

func canMergeVisualHeadingContinuation(current, next RoleDecision, observed []ObservedBlock, currentObservedIndex int) bool {
	if current.Role != "h" || next.Role != "h" ||
		current.Group != next.Group ||
		!mergeableVisualHeadingDecision(current) || !mergeableVisualHeadingDecision(next) ||
		currentObservedIndex < 0 || currentObservedIndex >= len(observed) ||
		next.Index != currentObservedIndex+1 || next.Block.BlankBefore {
		return false
	}
	curObs := observed[currentObservedIndex]
	nextObs := observed[next.Index]
	if !curObs.ContinuesVisualHeading || !curObs.Centered || !nextObs.Centered ||
		!curObs.HeadingFontSize || !nextObs.HeadingFontSize ||
		!curObs.AllBold || !nextObs.AllBold {
		return false
	}
	return utf8.RuneCountInString(strings.TrimSpace(current.Block.Text)+strings.TrimSpace(next.Block.Text)) <= 80
}

func mergeableVisualHeadingDecision(decision RoleDecision) bool {
	if decision.Group == "SectionHeaderGroup" &&
		(decision.Reason == "marker-title-body" || decision.Reason == "visual-heading-lines") {
		return true
	}
	return decision.Group == "StandaloneHeading" &&
		(decision.Reason == "visual-heading" || decision.Reason == "visual-heading-lines")
}

func joinVisualHeadingLines(first, second string) string {
	first = strings.TrimSpace(first)
	second = strings.TrimSpace(second)
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	if strings.HasPrefix(second, "与") || strings.HasPrefix(second, "和") ||
		strings.HasPrefix(second, "及") || strings.HasPrefix(second, "暨") ||
		strings.HasPrefix(second, "以及") || strings.HasPrefix(second, "或") ||
		hasHeadingJoinPunctuation(first) {
		return first + second
	}
	return first + "，" + second
}

func hasHeadingJoinPunctuation(text string) bool {
	for _, suffix := range []string{"，", ",", "、", "：", ":", "；", ";", "—", "-"} {
		if strings.HasSuffix(text, suffix) {
			return true
		}
	}
	return false
}

func hasExplicitZeroMarginBottom(n *html.Node) bool {
	if n == nil {
		return false
	}
	for _, decl := range strings.Split(getAttr(n, "style"), ";") {
		kv := strings.SplitN(decl, ":", 2)
		if len(kv) != 2 {
			continue
		}
		prop := strings.ToLower(strings.TrimSpace(kv[0]))
		value := strings.TrimSpace(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(kv[1])), "!important"))
		switch prop {
		case "margin-bottom":
			return cssLengthIsZero(value)
		case "margin":
			parts := strings.Fields(value)
			switch len(parts) {
			case 1, 2:
				return cssLengthIsZero(parts[0])
			case 3, 4:
				return cssLengthIsZero(parts[2])
			}
		}
	}
	return false
}

func cssLengthIsZero(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, suffix := range []string{"rem", "px", "pt", "em", "%"} {
		value = strings.TrimSuffix(value, suffix)
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return err == nil && number == 0
}

func defaultRoleDecisionGroup(obs ObservedBlock, role string) string {
	switch role {
	case "h":
		return "StandaloneHeading"
	case "txt", "bq", "li":
		if obs.RawKind == "text" {
			return "Unknown"
		}
	case "cap":
		return "Unknown"
	}
	if obs.RawKind != "" {
		return obs.RawKind
	}
	return "Unknown"
}

func blocksFromRoleDecisions(decisions []RoleDecision) []Block {
	out := make([]Block, 0, len(decisions))
	for _, d := range decisions {
		out = append(out, d.Block)
	}
	return out
}

func firstBodyFlowIndex(profile articleProfile) int {
	first := -1
	for idx := range profile.bodyFlow {
		if first == -1 || idx < first {
			first = idx
		}
	}
	return first
}

func isBodyFlowObservation(obs ObservedBlock) bool {
	if obs.RawKind != "text" || obs.TextLen == 0 || obs.CaptionProof != CaptionProofNone ||
		obs.ImageSourceLine || obs.ReferenceHead {
		return false
	}
	if obs.BaseBlock.K == "bq" || obs.BaseBlock.K == "li" || isStandaloneHeadingObservation(obs) {
		return false
	}
	if obs.TextLen > 40 || sentenceEndsLikeProse(obs.Text) {
		return true
	}
	return obs.BaseBlock.K == "txt" && !obs.Centered && !obs.MutedOrSmallStyle && obs.TextLen >= 18
}

func collectSectionHeaderGroup(observed []ObservedBlock, profile articleProfile, markerIdx int) []int {
	var titles []int
	next := nextMeaningfulIndex(observed, markerIdx)
	for next >= 0 && len(titles) < 2 {
		obs := observed[next]
		if obs.RawKind != "text" {
			return nil
		}
		if profile.bodyFlow[obs.Index] {
			if len(titles) > 0 {
				if sectionHeaderGroupHasEvidence(observed[markerIdx], observed, titles) {
					return titles
				}
			}
			return nil
		}
		if !isSectionHeaderCandidate(obs) {
			return nil
		}
		titles = append(titles, obs.Index)
		following := nextMeaningfulIndex(observed, next)
		if following < 0 {
			return nil
		}
		if observed[following].RawKind == "text" && profile.bodyFlow[observed[following].Index] {
			if sectionHeaderGroupHasEvidence(observed[markerIdx], observed, titles) {
				return titles
			}
			return nil
		}
		next = following
	}
	return nil
}

func isSectionHeaderCandidate(obs ObservedBlock) bool {
	if obs.RawKind != "text" || obs.TextLen == 0 || obs.TextLen > 40 {
		return false
	}
	if obs.ImageSourceLine || obs.ReferenceHead || obs.ReferenceItem || obs.ReferenceContinue {
		return false
	}
	if obs.BodyLikeEnding {
		return false
	}
	return true
}

func sectionHeaderGroupHasEvidence(marker ObservedBlock, observed []ObservedBlock, titles []int) bool {
	if isFlatSectionMarkerCandidate(marker) && len(titles) > 0 &&
		titles[0] >= 0 && titles[0] < len(observed) &&
		!observed[titles[0]].MutedOrSmallStyle {
		return true
	}
	for _, idx := range titles {
		if idx >= 0 && idx < len(observed) && hasSectionHeaderSignal(observed[idx]) {
			return true
		}
	}
	return false
}

func isDecorativeMarkerCandidate(obs ObservedBlock) bool {
	if obs.RawKind != "media" {
		return false
	}
	return obs.ImageRatio == 0 || obs.ImageRatio <= 0.33
}

func isFlatSectionMarkerCandidate(obs ObservedBlock) bool {
	return obs.ImageRatio > 0 && isDecorativeMarkerCandidate(obs) && obs.ImageRatio <= 0.13
}

func isConfirmedSectionMarkerMedia(observed []ObservedBlock, profile articleProfile, markerIdx int) bool {
	return markerIdx >= 0 && markerIdx < len(observed) &&
		isDecorativeMarkerCandidate(observed[markerIdx]) &&
		len(collectSectionHeaderGroup(observed, profile, markerIdx)) > 0
}

func hasSectionHeaderSignal(obs ObservedBlock) bool {
	if obs.HeadingFontSize {
		return true
	}
	for _, s := range obs.TextStyles {
		if s.ColorSet && !isGreyHex(s.Color) {
			return true
		}
		if s.FontWeightBold || s.BoldTag {
			return true
		}
	}
	if obs.Style.ColorSet && !isGreyHex(obs.Style.Color) {
		return true
	}
	return obs.Style.FontWeightBold || obs.Style.BoldTag
}

// captionTextCharLimit applies only after a caption has been attached to one
// media item. Muted/small visual styling selects the documented relaxed ceiling.
func captionTextCharLimit(obs ObservedBlock, opts articleBlockOptions) int {
	if obs.MutedOrSmallStyle {
		return opts.CaptionStrongMaxChars
	}
	return opts.CaptionMaxChars
}

func previousMeaningfulIndex(observed []ObservedBlock, i int) int {
	for j := i - 1; j >= 0; j-- {
		if observed[j].RawKind != "" {
			return j
		}
	}
	return -1
}

func nextMeaningfulIndex(observed []ObservedBlock, i int) int {
	for j := i + 1; j < len(observed); j++ {
		if observed[j].RawKind != "" {
			return j
		}
	}
	return -1
}

func isPrefaceMetadataLine(text string) bool {
	t := strings.TrimSpace(text)
	t = strings.TrimLeft(t, "（([【 ")
	if t == "" {
		return false
	}
	prefixes := []string{"作者", "撰文", "文", "来源", "编辑", "出品"}
	for _, p := range prefixes {
		if strings.HasPrefix(t, p+"：") || strings.HasPrefix(t, p+":") || strings.HasPrefix(t, p+" |") || strings.HasPrefix(t, p+"｜") {
			return true
		}
	}
	return false
}

func blockFromRoleDecision(obs ObservedBlock, role string) Block {
	b := obs.BaseBlock
	switch role {
	case "h":
		b.K = "h"
		b.Text = obs.Text
		b.Runs = nil
	case "cap":
		b.K = "cap"
		b.Text = obs.Text
		b.Runs = nil
	case "txt":
		b.K = "txt"
		b.Text = ""
		if len(b.Runs) == 0 && obs.Text != "" {
			b.Runs = []Run{{Type: "txt", Text: obs.Text}}
		}
	}
	return b
}

// hasMutedOrSmallStyle only records visual facts. It is used after a
// structural caption proof to select a length ceiling and by heading guards;
// it must never create cap.
func hasMutedOrSmallStyle(cx *blockCtx) bool {
	if mutedOrSmallStyle(cx.style) {
		return true
	}
	for _, s := range cx.textStyles {
		if mutedOrSmallStyle(s) {
			return true
		}
	}
	return false
}

func isImageSourceLine(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.TrimLeft(t, "（([【 ")
	return strings.HasPrefix(t, "图片来源") ||
		strings.HasPrefix(t, "图源") ||
		strings.HasPrefix(t, "图为") ||
		strings.HasPrefix(t, "图片源") ||
		strings.HasPrefix(t, "image source") ||
		strings.HasPrefix(t, "photo source") ||
		strings.HasPrefix(t, "source:")
}

func classListSignalsCaption(classes []string) bool {
	for _, className := range classes {
		if classSignalsCaption(className) {
			return true
		}
	}
	return false
}

func classSignalsCaption(className string) bool {
	lower := strings.ToLower(className)
	return strings.Contains(lower, "caption") ||
		strings.Contains(lower, "img_des") ||
		strings.Contains(lower, "imgdesc")
}

func mutedOrSmallStyle(s computedStyle) bool {
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
