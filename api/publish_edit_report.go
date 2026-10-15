package main

// publish_edit_report.go — 每周手改清单：比对与归类。
//
// 2026-10-15 用户裁定（「每周一份短清单，私发给我，只列编辑手改 3 次以上的，不发审核群」）：
// 编辑在 FoodTalks 后台反复手改流水线发出的稿，说明排版规则有漏洞。每周一上午把上一周已发布的
// 流水线稿逐篇拿「我们发出去的正文」（job.ContentHTML）和 FoodTalks 上现在的正文按段对齐，
// 把差异归成几类（开头署名、转载声明、小标题、删行、改字、图片……）；同一类有 3 篇以上才列出，
// 每类附几个这篇自己的真实例子。投递、调度与接口在 publish_edit_report_delivery.go。
//
// 只比文字与结构：组装层给正文加的公司链接、系列链接、目录锚点只动标签不动字，不会被当成手改；
// 编辑重新上传的同一张图（文件名前 12 位相同）也不算换图。

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// 同一类问题至少手改了几篇才列进清单（用户裁定：3 次以上）。
const publishEditReportMinArticles = 3

// 每类最多举几个例子；其余只列编号。
const publishEditReportMaxExamples = 3

// 整篇对得上的段落不到这个比例，算编辑整篇重新排过，不再逐处归类。
const publishEditReportRewriteRatio = 0.5

// 手改类别。顺序即同篇数时清单里的先后。
const (
	editReportByline        = "byline"
	editReportRepostNotice  = "repost_notice"
	editReportHeadingDemote = "heading_removed"
	editReportHeadingAdd    = "heading_added"
	editReportHeadingLevel  = "heading_level"
	editReportHeadingMerge  = "heading_merged"
	editReportHeadingText   = "heading_text"
	editReportCaptionSwap   = "caption_changed"
	editReportNoteSwap      = "note_changed"
	editReportCaptionText   = "caption_text"
	editReportHeadDeleted   = "head_deleted"
	editReportTailDeleted   = "tail_deleted"
	editReportBodyDeleted   = "body_deleted"
	editReportAdded         = "added"
	editReportMerged        = "merged"
	editReportBold          = "bold_changed"
	editReportTextEdit      = "text_edit"
	editReportImageReplaced = "image_replaced"
	editReportImageRemoved  = "image_removed"
	editReportImageAdded    = "image_added"
	editReportRewritten     = "rewritten"
)

var publishEditReportCategoryOrder = []string{
	editReportByline, editReportRepostNotice,
	editReportHeadingDemote, editReportHeadingAdd, editReportHeadingLevel, editReportHeadingMerge, editReportHeadingText,
	editReportCaptionSwap, editReportNoteSwap, editReportCaptionText,
	editReportHeadDeleted, editReportTailDeleted, editReportBodyDeleted, editReportAdded, editReportMerged,
	editReportBold, editReportTextEdit,
	editReportImageReplaced, editReportImageRemoved, editReportImageAdded,
	editReportRewritten,
}

// 给不懂编程的人看的类名（「标题1/标题2」是 FoodTalks 后台的叫法）。
var publishEditReportCategoryLabels = map[string]string{
	editReportByline:        "开头署名",
	editReportRepostNotice:  "文末转载声明",
	editReportHeadingDemote: "小标题改回正文",
	editReportHeadingAdd:    "正文改成小标题",
	editReportHeadingLevel:  "小标题级别（标题1/标题2）",
	editReportHeadingMerge:  "小标题合并或拆开",
	editReportHeadingText:   "小标题改字",
	editReportCaptionSwap:   "图注和正文互换",
	editReportNoteSwap:      "灰色小字和正文互换",
	editReportCaptionText:   "图注改字",
	editReportHeadDeleted:   "删掉开头几行",
	editReportTailDeleted:   "删掉文末几行",
	editReportBodyDeleted:   "删掉正文中间的段落",
	editReportAdded:         "加了段落",
	editReportMerged:        "段落合并或拆开",
	editReportBold:          "加粗",
	editReportTextEdit:      "正文改字",
	editReportImageReplaced: "换了图片",
	editReportImageRemoved:  "删了图片",
	editReportImageAdded:    "加了图片",
	editReportRewritten:     "整篇重新排过",
}

// editReportBlock 正文里的一段：小标题、正文段、图注、灰色小字或一张图。
type editReportBlock struct {
	Kind string // h1…h6 / p / cap（图注）/ note（灰色小字）/ img
	Text string // 显示用（空白折叠）；图片是文件名
	Key  string // 对齐用：去掉全部空白；图片是文件名前 12 位
	// BoldMask 与 Key 逐字对应：1 是加粗的字。局部加粗、拆开的 <strong> 都按字比，不按标签比。
	BoldMask string
}

func (b editReportBlock) heading() bool {
	return len(b.Kind) == 2 && b.Kind[0] == 'h' && b.Kind[1] >= '1' && b.Kind[1] <= '6'
}

func (b editReportBlock) image() bool { return b.Kind == "img" }

// editReportChange 一篇里的一处手改。Detail 是给人看的一句话（含原文片段）。
type editReportChange struct {
	Category string `json:"category"`
	Detail   string `json:"detail"`
}

var editReportLeafTags = map[string]bool{
	"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "figcaption": true, "td": true, "th": true, "blockquote": true, "dt": true, "dd": true, "pre": true,
}

var editReportContainerTags = map[string]bool{
	"div": true, "section": true, "figure": true, "ul": true, "ol": true, "dl": true, "table": true,
	"thead": true, "tbody": true, "tfoot": true, "tr": true, "article": true, "header": true, "footer": true,
	"main": true, "aside": true, "nav": true, "body": true, "html": true,
}

// editReportBlocks 把正文 HTML 拆成段序列。
func editReportBlocks(raw string) []editReportBlock {
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(raw), ctx)
	if err != nil {
		return nil
	}
	var out []editReportBlock
	for _, n := range nodes {
		editReportWalk(n, false, &out)
	}
	return out
}

func editReportStructural(n *html.Node) bool {
	return n.Type == html.ElementNode && (editReportLeafTags[n.Data] || editReportContainerTags[n.Data])
}

func editReportHasStructuralDescendant(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if editReportStructural(c) || editReportHasStructuralDescendant(c) {
			return true
		}
	}
	return false
}

func editReportWalk(n *html.Node, inCaption bool, out *[]editReportBlock) {
	switch n.Type {
	case html.TextNode:
		if text := editReportCollapse(n.Data); text != "" {
			block := editReportTextBlock("p", text)
			block.BoldMask = strings.Repeat("0", utf8.RuneCountInString(block.Key))
			*out = append(*out, block)
		}
		return
	case html.ElementNode:
	default:
		return
	}
	switch n.Data {
	case "script", "style", "noscript", "template":
		return
	case "img":
		*out = append(*out, editReportImageBlock(n))
		return
	}
	caption := inCaption || n.Data == "figcaption"
	if editReportLeafTags[n.Data] && !editReportHasStructuralDescendant(n) {
		editReportLeaf([]*html.Node{n}, n, caption, out)
		return
	}
	// 容器（或里面还套着段落的块）：结构子节点各自成段，夹在中间的零散文字并成一段。
	var run []*html.Node
	flush := func() {
		if len(run) > 0 {
			editReportLeaf(run, n, caption, out)
			run = nil
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if editReportStructural(c) || (c.Type == html.ElementNode && editReportHasStructuralDescendant(c)) {
			flush()
			editReportWalk(c, caption, out)
			continue
		}
		run = append(run, c)
	}
	flush()
}

// editReportLeaf 一段的文字、样式与加粗；段里的图片按出现顺序单独成段。
func editReportLeaf(nodes []*html.Node, owner *html.Node, caption bool, out *[]editReportBlock) {
	var text strings.Builder
	var styles strings.Builder
	var mask strings.Builder
	styles.WriteString(editReportAttr(owner, "style"))
	emit := func() {
		collapsed := editReportCollapse(text.String())
		text.Reset()
		boldMask := mask.String()
		mask.Reset()
		if collapsed == "" {
			return
		}
		kind := editReportKind(owner, caption, styles.String())
		block := editReportTextBlock(kind, collapsed)
		block.BoldMask = boldMask
		*out = append(*out, block)
	}
	var walk func(n *html.Node, bold bool)
	walk = func(n *html.Node, bold bool) {
		switch n.Type {
		case html.TextNode:
			text.WriteString(n.Data)
			for _, r := range n.Data {
				if !editReportSpace(r) {
					if bold {
						mask.WriteByte('1')
					} else {
						mask.WriteByte('0')
					}
				}
			}
			return
		case html.ElementNode:
		default:
			return
		}
		switch n.Data {
		case "script", "style", "noscript", "template":
			return
		case "br":
			text.WriteString(" ")
			return
		case "img":
			emit()
			*out = append(*out, editReportImageBlock(n))
			return
		}
		style := editReportAttr(n, "style")
		styles.WriteString(";" + style)
		childBold := bold || n.Data == "strong" || n.Data == "b" ||
			strings.Contains(style, "font-weight:bold") || strings.Contains(style, "font-weight:700")
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, childBold)
		}
	}
	ownerStyle := editReportAttr(owner, "style")
	ownerBold := strings.Contains(ownerStyle, "font-weight:bold") || strings.Contains(ownerStyle, "font-weight:700")
	for _, n := range nodes {
		walk(n, ownerBold)
	}
	emit()
}

// editReportKind 段的种类：标题看标签；图注是 figcaption 或居中的灰色小字；其余灰色小字是署名/注释。
func editReportKind(owner *html.Node, caption bool, styles string) string {
	if owner != nil && owner.Type == html.ElementNode {
		switch owner.Data {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			return owner.Data
		}
	}
	if caption {
		return "cap"
	}
	small := strings.Contains(styles, "font-size:12px") || strings.Contains(styles, "font-size:13px") ||
		strings.Contains(styles, "font-size:14px")
	gray := strings.Contains(styles, "#888") || strings.Contains(styles, "#999") ||
		strings.Contains(styles, "rgb(136,136,136)") || strings.Contains(styles, "rgb(153,153,153)")
	if small && gray {
		if strings.Contains(styles, "text-align:center") {
			return "cap"
		}
		return "note"
	}
	return "p"
}

func editReportAttr(n *html.Node, name string) string {
	if n == nil || n.Type != html.ElementNode {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == name {
			return strings.Map(func(r rune) rune {
				if editReportSpace(r) {
					return -1
				}
				return unicode.ToLower(r)
			}, a.Val)
		}
	}
	return ""
}

func editReportTextBlock(kind, text string) editReportBlock {
	return editReportBlock{Kind: kind, Text: text, Key: editReportKey(text)}
}

func editReportImageBlock(n *html.Node) editReportBlock {
	src := ""
	for _, a := range n.Attr {
		if a.Key == "src" || (src == "" && a.Key == "data-src") {
			src = strings.TrimSpace(a.Val)
		}
	}
	name := src
	if i := strings.IndexAny(name, "?#"); i >= 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	key := strings.ToLower(name)
	if i := strings.LastIndex(key, "."); i > 0 {
		key = key[:i]
	}
	if utf8.RuneCountInString(key) > 12 {
		key = string([]rune(key)[:12])
	}
	if strings.HasPrefix(src, "data:") {
		key = "data"
	}
	return editReportBlock{Kind: "img", Text: name, Key: key}
}

func editReportSpace(r rune) bool {
	return unicode.IsSpace(r) || r == '\u00a0' || r == '\u3000' || r == '\u200b' || r == '\ufeff'
}

func editReportCollapse(s string) string {
	return strings.Join(strings.FieldsFunc(s, editReportSpace), " ")
}

func editReportKey(s string) string {
	return strings.Map(func(r rune) rune {
		if editReportSpace(r) {
			return -1
		}
		return r
	}, s)
}

// editReportLoose 合并判定用：去掉标点符号（「A」+「B」合成「A，B」也算合并）。
func editReportLoose(s string) string {
	return strings.Map(func(r rune) rune {
		if editReportSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return -1
		}
		return r
	}, s)
}

// 开头署名行：「本文为X原创…」或「作者/来源/编辑…：」。
var editReportBylineRE = regexp.MustCompile(`^(?:\*?本文(?:为|来自)|(?:作者|来源|出处|编辑|责编|主编|撰文|撰稿|文|翻译|编译|采访|整理|口述|策划|出品|统筹|审校)\s*[：:｜|/])`)

// 文末转载声明：「*本文为X原创《标题》，不代表FoodTalks立场…」。
var editReportRepostNoticeRE = regexp.MustCompile(`^\*?本文为.{1,60}原创《|不代表FoodTalks立场`)

func editReportBylineLine(text string) bool { return editReportBylineRE.MatchString(text) }

func editReportRepostNoticeLine(text string) bool {
	return editReportRepostNoticeRE.MatchString(text)
}

// editReportRegion 段在稿里的位置：开头 5 段、FoodTalks「提示：」之前 10 段起到文末，其余是中间。
type editReportRegion struct {
	textIndex []int // 每个块在文字段里的序号（图片取前一段的序号）
	tailFrom  int
}

const (
	editReportRegionHead = "head"
	editReportRegionBody = "body"
	editReportRegionTail = "tail"
)

func newEditReportRegion(blocks []editReportBlock) editReportRegion {
	r := editReportRegion{textIndex: make([]int, len(blocks))}
	n, tip := 0, -1
	for i, b := range blocks {
		r.textIndex[i] = n
		if b.image() {
			continue
		}
		if tip < 0 && (b.Key == "提示：" || b.Key == "提示:") {
			tip = n
		}
		n++
	}
	if tip < 0 {
		tip = n
	}
	r.tailFrom = tip - 10
	return r
}

func (r editReportRegion) at(i int) string {
	if i < 0 || i >= len(r.textIndex) {
		return editReportRegionBody
	}
	idx := r.textIndex[i]
	switch {
	case idx < 5:
		return editReportRegionHead
	case idx >= r.tailFrom:
		return editReportRegionTail
	default:
		return editReportRegionBody
	}
}

// compareEditReport 我们发出的正文（ours）与 FoodTalks 上现在的正文（live）之间的手改，按出现顺序。
// 一字未动返回 nil。
func compareEditReport(ours, live string) []editReportChange {
	a, b := editReportBlocks(ours), editReportBlocks(live)
	keysA, keysB := editReportAlignKeys(a), editReportAlignKeys(b)
	matches := editReportLCS(keysA, keysB)
	region := newEditReportRegion(a)
	var changes []editReportChange
	add := func(category, detail string) {
		changes = append(changes, editReportChange{Category: category, Detail: editReportClip(detail, 160)})
	}
	var images editReportImageCounts
	covered := 2 * len(matches) // 对上的段（原样或改了字、合并拆开）占两边总段数的比例
	i, j := 0, 0
	for _, m := range append(matches, [2]int{len(a), len(b)}) {
		if m[0] > i || m[1] > j {
			covered += editReportClassifyGap(a[i:m[0]], b[j:m[1]], i, region, &images, add)
		}
		if m[0] < len(a) && m[1] < len(b) {
			editReportStructureChange(a[m[0]], b[m[1]], add)
			editReportBoldChange(a[m[0]], b[m[1]], add)
		}
		i, j = m[0]+1, m[1]+1
	}
	// 配完对还有一半以上的段对不上，算编辑整篇重新排过，不再逐处列。
	if len(a)+len(b) > 0 && float64(covered)/float64(len(a)+len(b)) < publishEditReportRewriteRatio {
		return []editReportChange{{Category: editReportRewritten, Detail: fmt.Sprintf("原来 %d 段，现在 %d 段，对得上的不到一半", len(a), len(b))}}
	}
	// 一篇里的图片改动合成一条：换/删/加了几张。
	if images.replaced > 0 {
		add(editReportImageReplaced, fmt.Sprintf("换了 %d 张图", images.replaced))
	}
	if images.removed > 0 {
		add(editReportImageRemoved, fmt.Sprintf("删了 %d 张图", images.removed))
	}
	if images.added > 0 {
		add(editReportImageAdded, fmt.Sprintf("加了 %d 张图", images.added))
	}
	return changes
}

// editReportImageCounts 一篇里换、删、加的图片张数。
type editReportImageCounts struct {
	replaced, removed, added int
}

func editReportAlignKeys(blocks []editReportBlock) []string {
	keys := make([]string, len(blocks))
	for i, b := range blocks {
		if b.image() {
			keys[i] = "i:" + b.Key
		} else {
			keys[i] = "t:" + b.Key
		}
	}
	return keys
}

// editReportLCS 两串段落的最长公共子序列（下标对）。
func editReportLCS(a, b []string) [][2]int {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return nil
	}
	if n*m > 4_000_000 {
		// 超长稿不建整张表（避免一篇占几十兆内存），改按「下一次出现」贪心对齐，中间对得上的照样对上。
		return editReportGreedyMatches(a, b)
	}
	w := m + 1
	dp := make([]uint16, (n+1)*w)
	for x := n - 1; x >= 0; x-- {
		for y := m - 1; y >= 0; y-- {
			if a[x] == b[y] {
				dp[x*w+y] = dp[(x+1)*w+y+1] + 1
			} else if dp[(x+1)*w+y] >= dp[x*w+y+1] {
				dp[x*w+y] = dp[(x+1)*w+y]
			} else {
				dp[x*w+y] = dp[x*w+y+1]
			}
		}
	}
	var out [][2]int
	x, y := 0, 0
	for x < n && y < m {
		switch {
		case a[x] == b[y]:
			out = append(out, [2]int{x, y})
			x++
			y++
		case dp[(x+1)*w+y] >= dp[x*w+y+1]:
			x++
		default:
			y++
		}
	}
	return out
}

// editReportGreedyMatches 内存与段数成正比的对齐：两边当前段不同时，跳到离得更近的那一处再次出现。
func editReportGreedyMatches(a, b []string) [][2]int {
	positions := func(keys []string) map[string][]int {
		out := map[string][]int{}
		for i, k := range keys {
			out[k] = append(out[k], i)
		}
		return out
	}
	inA, inB := positions(a), positions(b)
	next := func(list []int, from int) int {
		k := sort.SearchInts(list, from)
		if k < len(list) {
			return list[k]
		}
		return -1
	}
	var out [][2]int
	x, y := 0, 0
	for x < len(a) && y < len(b) {
		if a[x] == b[y] {
			out = append(out, [2]int{x, y})
			x, y = x+1, y+1
			continue
		}
		nb, na := next(inB[a[x]], y), next(inA[b[y]], x)
		switch {
		case nb < 0 && na < 0:
			x, y = x+1, y+1
		case nb < 0:
			x++
		case na < 0:
			y++
		case nb-y <= na-x:
			y = nb
		default:
			x = na
		}
	}
	return out
}

// editReportStructureChange 同一段换了种类：小标题/正文/图注/灰色小字（文字改没改都看）。
func editReportStructureChange(a, b editReportBlock, add func(string, string)) {
	if a.image() || b.image() {
		return
	}
	quote := "「" + editReportClip(b.Text, 40) + "」"
	switch {
	case a.heading() && b.heading():
		if a.Kind != b.Kind {
			add(editReportHeadingLevel, fmt.Sprintf("%s从%s改成%s", quote, editReportHeadingName(a.Kind), editReportHeadingName(b.Kind)))
		}
	case a.heading():
		add(editReportHeadingDemote, quote+"从小标题改成正文")
	case b.heading():
		add(editReportHeadingAdd, quote+"从正文改成小标题")
	case a.Kind == "cap" && b.Kind != "cap":
		add(editReportCaptionSwap, quote+"从图注改成正文")
	case a.Kind != "cap" && b.Kind == "cap":
		add(editReportCaptionSwap, quote+"从正文改成图注")
	case a.Kind == "note" && b.Kind != "note":
		add(editReportNoteSwap, quote+"从灰色小字改成正文")
	case a.Kind != "note" && b.Kind == "note":
		add(editReportNoteSwap, quote+"从正文改成灰色小字")
	}
}

// editReportBoldChange 文字一样的两段逐字比加粗；小标题本身就是粗体，不比。
func editReportBoldChange(a, b editReportBlock, add func(string, string)) {
	if a.image() || b.image() || a.heading() || b.heading() || a.BoldMask == b.BoldMask {
		return
	}
	runes := []rune(b.Key)
	if len(a.BoldMask) != len(runes) || len(b.BoldMask) != len(runes) {
		return
	}
	span := func(want, other string) string {
		from := strings.IndexFunc(want, func(r rune) bool { return r == '1' })
		for from >= 0 && from < len(runes) {
			if want[from] == '1' && other[from] == '0' {
				to := from
				for to < len(runes) && want[to] == '1' && other[to] == '0' {
					to++
				}
				return string(runes[from:to])
			}
			from++
		}
		return ""
	}
	if added := span(b.BoldMask, a.BoldMask); added != "" {
		add(editReportBold, "「"+editReportClip(added, 30)+"」加了粗")
		return
	}
	if removed := span(a.BoldMask, b.BoldMask); removed != "" {
		add(editReportBold, "「"+editReportClip(removed, 30)+"」去掉了加粗")
	}
}

// editReportHeadingName FoodTalks 后台的叫法：h2=标题1，h3=标题2……
func editReportHeadingName(kind string) string {
	if len(kind) == 2 && kind[1] >= '2' && kind[1] <= '6' {
		return fmt.Sprintf("标题%d", kind[1]-'1')
	}
	return "大标题"
}

// editReportIndexed 一段与它在我们正文里的下标（判断在开头、中间还是文末）。
type editReportIndexed struct {
	block editReportBlock
	pos   int
}

// editReportClassifyGap 对不上的一段：先数图片，再把文字段配成合并/拆开/改字/删/加。
// 返回这一段里配上对的块数（两边合计：改字 2、合并拆开 k+1、换图 2），算整篇重排用。
func editReportClassifyGap(gapA, gapB []editReportBlock, offsetA int, region editReportRegion, images *editReportImageCounts, add func(string, string)) int {
	var textA []editReportIndexed
	var textB []editReportBlock
	imagesA, imagesB := 0, 0
	for k, block := range gapA {
		if block.image() {
			imagesA++
			continue
		}
		textA = append(textA, editReportIndexed{block, offsetA + k})
	}
	for _, block := range gapB {
		if block.image() {
			imagesB++
			continue
		}
		textB = append(textB, block)
	}
	covered := 2 * min(imagesA, imagesB)
	images.replaced += min(imagesA, imagesB)
	images.removed += max(0, imagesA-imagesB)
	images.added += max(0, imagesB-imagesA)
	// 新增段落的位置按这段对不上之前的那段算。
	insertRegion := region.at(offsetA)
	if offsetA > 0 && insertRegion != editReportRegionHead {
		insertRegion = region.at(offsetA - 1)
	}
	deleted := func(x editReportIndexed) {
		quote := "删了「" + editReportClip(x.block.Text, 50) + "」"
		if x.block.heading() {
			quote = "删了小标题「" + editReportClip(x.block.Text, 50) + "」"
		}
		where := region.at(x.pos)
		switch {
		case editReportRepostNoticeLine(x.block.Text):
			add(editReportRepostNotice, quote)
		case where == editReportRegionHead && (editReportBylineLine(x.block.Text) || x.block.Kind == "note"):
			add(editReportByline, quote)
		case where == editReportRegionHead:
			add(editReportHeadDeleted, quote)
		case where == editReportRegionTail:
			add(editReportTailDeleted, quote)
		default:
			add(editReportBodyDeleted, quote)
		}
	}
	inserted := func(b editReportBlock) {
		quote := "加了「" + editReportClip(b.Text, 50) + "」"
		switch {
		case editReportRepostNoticeLine(b.Text):
			add(editReportRepostNotice, quote)
		case insertRegion == editReportRegionHead && editReportBylineLine(b.Text):
			add(editReportByline, quote)
		case b.heading():
			add(editReportHeadingAdd, "加了小标题「"+editReportClip(b.Text, 50)+"」")
		default:
			add(editReportAdded, quote)
		}
	}
	x, y := 0, 0
	for x < len(textA) && y < len(textB) {
		if k := editReportMergeSpan(textA[x:], textB[y]); k > 1 {
			parts := make([]editReportBlock, k)
			for n := 0; n < k; n++ {
				parts[n] = textA[x+n].block
			}
			editReportMergeChange(parts, []editReportBlock{textB[y]}, region.at(textA[x].pos), add)
			covered += k + 1
			x, y = x+k, y+1
			continue
		}
		if k := editReportSplitSpan(textA[x].block, textB[y:]); k > 1 {
			editReportMergeChange([]editReportBlock{textA[x].block}, textB[y:y+k], region.at(textA[x].pos), add)
			covered += 1 + k
			x, y = x+1, y+k
			continue
		}
		// 同一位置上小标题换了一句话，也算小标题改字。
		if editReportSimilar(textA[x].block.Key, textB[y].Key) || (textA[x].block.heading() && textB[y].heading()) {
			editReportEditChange(textA[x].block, textB[y], region.at(textA[x].pos), add)
			covered += 2
			x, y = x+1, y+1
			continue
		}
		if y+1 < len(textB) && editReportSimilar(textA[x].block.Key, textB[y+1].Key) {
			inserted(textB[y])
			y++
			continue
		}
		deleted(textA[x])
		x++
	}
	for ; x < len(textA); x++ {
		deleted(textA[x])
	}
	for ; y < len(textB); y++ {
		inserted(textB[y])
	}
	return covered
}

// editReportMergeSpan 从 parts 开头起几段（≥2）连起来正好是 whole；不是返回 0。
func editReportMergeSpan(parts []editReportIndexed, whole editReportBlock) int {
	blocks := make([]editReportBlock, 0, min(len(parts), 6))
	for k := 0; k < len(parts) && k < 6; k++ {
		blocks = append(blocks, parts[k].block)
	}
	return editReportSplitSpan(whole, blocks)
}

// editReportSplitSpan parts 开头起几段（≥2）连起来（不计标点）正好是 whole；不是返回 0。
func editReportSplitSpan(whole editReportBlock, parts []editReportBlock) int {
	target := editReportLoose(whole.Key)
	if target == "" {
		return 0
	}
	joined := ""
	for k := 0; k < len(parts) && k < 6; k++ {
		joined += editReportLoose(parts[k].Key)
		if joined == target {
			return k + 1
		}
		if !strings.HasPrefix(target, joined) {
			return 0
		}
	}
	return 0
}

func editReportMergeChange(from, to []editReportBlock, where string, add func(string, string)) {
	quote := func(blocks []editReportBlock) string {
		parts := make([]string, len(blocks))
		for i, b := range blocks {
			parts[i] = "「" + editReportClip(b.Text, 30) + "」"
		}
		return strings.Join(parts, "")
	}
	verb := "合成"
	if len(from) == 1 {
		verb = "拆成"
	}
	detail := quote(from) + verb + quote(to)
	heading := false
	for _, b := range append(append([]editReportBlock{}, from...), to...) {
		heading = heading || b.heading()
	}
	switch {
	case heading:
		add(editReportHeadingMerge, detail)
	case where == editReportRegionHead && (editReportBylineLine(from[0].Text) || from[0].Kind == "note"):
		add(editReportByline, detail)
	default:
		add(editReportMerged, detail)
	}
}

func editReportEditChange(a, b editReportBlock, where string, add func(string, string)) {
	before, after := editReportSnippet(a.Text, b.Text)
	detail := "「" + before + "」改成「" + after + "」"
	switch {
	case editReportRepostNoticeLine(a.Text) || editReportRepostNoticeLine(b.Text):
		add(editReportRepostNotice, detail)
		return
	case where == editReportRegionHead && (editReportBylineLine(a.Text) || editReportBylineLine(b.Text) || a.Kind == "note"):
		// 署名改写常连带换成灰色小字，算在署名里，不另记一类。
		add(editReportByline, detail)
		return
	case a.heading() && b.heading():
		add(editReportHeadingText, detail)
	case a.heading() || b.heading():
		// 改了字又换了种类（如小标题改回正文并补了句号）：种类变化另记一类。
	case a.Kind == "cap" && b.Kind == "cap":
		add(editReportCaptionText, detail)
	case a.Kind == "cap" || b.Kind == "cap":
	default:
		add(editReportTextEdit, detail)
	}
	editReportStructureChange(a, b, add)
}

// editReportSimilar 两段是不是同一段改了字：字符级最长公共子序列占两段总长的一半以上，
// 或者短的那段几乎原样留在长的那段里。
func editReportSimilar(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return false
	}
	if len(ra) > 400 || len(rb) > 400 {
		// 长段只看首尾相同的部分，够判断是不是同一段。
		p := 0
		for p < len(ra) && p < len(rb) && ra[p] == rb[p] {
			p++
		}
		s := 0
		for s < len(ra)-p && s < len(rb)-p && ra[len(ra)-1-s] == rb[len(rb)-1-s] {
			s++
		}
		return float64(2*(p+s))/float64(len(ra)+len(rb)) >= 0.5
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for x := 1; x <= len(ra); x++ {
		for y := 1; y <= len(rb); y++ {
			if ra[x-1] == rb[y-1] {
				cur[y] = prev[y-1] + 1
			} else {
				cur[y] = max(prev[y], cur[y-1])
			}
		}
		prev, cur = cur, prev
	}
	common := prev[len(rb)]
	// 「作者：X」扩写成「本文为…原创，作者：X，转载请联系出处。」也算同一段：短的那段八成以上都还在。
	return float64(2*common)/float64(len(ra)+len(rb)) >= 0.5 ||
		(common >= 4 && common*5 >= min(len(ra), len(rb))*4)
}

// editReportSnippet 只截改动的那一处，前后各留几个字；短段整段给出。
func editReportSnippet(a, b string) (string, string) {
	ra, rb := []rune(a), []rune(b)
	if len(ra) <= 60 && len(rb) <= 60 {
		return a, b
	}
	p := 0
	for p < len(ra) && p < len(rb) && ra[p] == rb[p] {
		p++
	}
	s := 0
	for s < len(ra)-p && s < len(rb)-p && ra[len(ra)-1-s] == rb[len(rb)-1-s] {
		s++
	}
	cut := func(r []rune) string {
		from := max(0, p-10)
		to := min(len(r), len(r)-s+10)
		if to-from > 60 {
			to = from + 60
		}
		out := string(r[from:to])
		if from > 0 {
			out = "…" + out
		}
		if to < len(r) {
			out += "…"
		}
		return out
	}
	return cut(ra), cut(rb)
}

// publishEditReportArticle 一篇的比对结果。
type publishEditReportArticle struct {
	JobID   int64              `json:"job_id"`
	NewsID  int64              `json:"news_id"`
	Title   string             `json:"title"`
	URL     string             `json:"url"`
	Changes []editReportChange `json:"changes,omitempty"`
}

type publishEditReportExample struct {
	JobID  int64  `json:"job_id"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Detail string `json:"detail"`
}

type publishEditReportCategory struct {
	Key      string                     `json:"key"`
	Label    string                     `json:"label"`
	Articles int                        `json:"articles"`
	JobIDs   []int64                    `json:"job_ids"`
	Examples []publishEditReportExample `json:"examples"`
}

// publishEditReport 一周的清单。Categories 是全部类别（含不到 3 篇的），Listed 是要发出去的。
type publishEditReport struct {
	WeekStart  string                      `json:"week_start"` // 周一，北京时间 YYYY-MM-DD
	WeekEnd    string                      `json:"week_end"`   // 周日
	Compared   int                         `json:"compared"`   // 比对了几篇
	Edited     int                         `json:"edited"`     // 其中编辑手改过的
	Unreadable int                         `json:"unreadable"` // 读不到 FoodTalks 正文的
	Categories []publishEditReportCategory `json:"categories"`
	Listed     []publishEditReportCategory `json:"listed"`
	Articles   []publishEditReportArticle  `json:"articles,omitempty"`
}

// summarizePublishEditReport 按类数篇数（一篇同类多处只算一篇），每类按稿件编号取前几个例子。
func summarizePublishEditReport(report *publishEditReport) {
	byKey := map[string]*publishEditReportCategory{}
	report.Edited = 0
	for _, article := range report.Articles {
		if len(article.Changes) == 0 {
			continue
		}
		report.Edited++
		seen := map[string]bool{}
		for _, change := range article.Changes {
			if seen[change.Category] {
				continue
			}
			seen[change.Category] = true
			cat := byKey[change.Category]
			if cat == nil {
				cat = &publishEditReportCategory{Key: change.Category, Label: publishEditReportCategoryLabels[change.Category]}
				byKey[change.Category] = cat
			}
			cat.Articles++
			cat.JobIDs = append(cat.JobIDs, article.JobID)
			if len(cat.Examples) < publishEditReportMaxExamples {
				cat.Examples = append(cat.Examples, publishEditReportExample{
					JobID: article.JobID, Title: article.Title, URL: article.URL, Detail: change.Detail,
				})
			}
		}
	}
	order := map[string]int{}
	for i, key := range publishEditReportCategoryOrder {
		order[key] = i
	}
	report.Categories = report.Categories[:0]
	for _, cat := range byKey {
		report.Categories = append(report.Categories, *cat)
	}
	sort.SliceStable(report.Categories, func(i, j int) bool {
		if report.Categories[i].Articles != report.Categories[j].Articles {
			return report.Categories[i].Articles > report.Categories[j].Articles
		}
		return order[report.Categories[i].Key] < order[report.Categories[j].Key]
	})
	report.Listed = nil
	for _, cat := range report.Categories {
		if cat.Articles >= publishEditReportMinArticles {
			report.Listed = append(report.Listed, cat)
		}
	}
}

// buildPublishEditReportPost 私信正文（飞书富文本）：一句总数，再逐类列篇数与例子。
func buildPublishEditReportPost(report publishEditReport) map[string]any {
	title := fmt.Sprintf("FoodTalks 手改周报（%s–%s）", editReportDateLabel(report.WeekStart), editReportDateLabel(report.WeekEnd))
	var lines [][]any
	summary := fmt.Sprintf("上周流水线发到 FoodTalks 的 %d 篇里，编辑在后台手改了 %d 篇。", report.Compared, report.Edited)
	if report.Compared == 0 {
		summary = "上周没有流水线发到 FoodTalks 的文章。"
	}
	if report.Unreadable > 0 {
		summary += fmt.Sprintf("另有 %d 篇读不到 FoodTalks 上的正文，没算进去。", report.Unreadable)
	}
	lines = append(lines, []any{postText(summary, false)})
	if report.Compared > 0 && len(report.Listed) == 0 {
		lines = append(lines, []any{postText(fmt.Sprintf("没有哪一类改了 %d 篇以上，不用处理。", publishEditReportMinArticles), false)})
	}
	if len(report.Listed) > 0 {
		lines = append(lines, []any{postText(fmt.Sprintf("同一类改了 %d 篇以上的：", publishEditReportMinArticles), false)})
	}
	for i, cat := range report.Listed {
		lines = append(lines, []any{postText(fmt.Sprintf("%d. %s（%d 篇）", i+1, cat.Label, cat.Articles), true)})
		shown := map[int64]bool{}
		for _, example := range cat.Examples {
			shown[example.JobID] = true
			label := fmt.Sprintf("#%d《%s》", example.JobID, reviewCardSafeOrDash(example.Title, 30))
			line := []any{postText("· ", false)}
			if strings.TrimSpace(example.URL) != "" {
				line = append(line, postLink(label, example.URL))
			} else {
				line = append(line, postText(label, false))
			}
			lines = append(lines, append(line, postText("："+example.Detail, false)))
		}
		var rest []string
		for _, id := range cat.JobIDs {
			if !shown[id] {
				rest = append(rest, fmt.Sprintf("#%d", id))
			}
		}
		if len(rest) > 10 {
			rest = append(rest[:10], fmt.Sprintf("等 %d 篇", len(rest)))
		}
		if len(rest) > 0 {
			lines = append(lines, []any{postText("· 还有 "+strings.Join(rest, " "), false)})
		}
	}
	if len(report.Listed) > 0 {
		lines = append(lines, []any{postText(fmt.Sprintf("只改了 1–%d 篇的零星情况不列。", publishEditReportMinArticles-1), false)})
	}
	post := postMessage(lines)
	post["zh_cn"].(map[string]any)["title"] = title
	return post
}

// editReportClip 截到 n 个字，截了就补「…」。
func editReportClip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// editReportDateLabel 2026-10-05 → 10月5日。
func editReportDateLabel(date string) string {
	var y, m, d int
	if _, err := fmt.Sscanf(date, "%d-%d-%d", &y, &m, &d); err != nil {
		return date
	}
	return fmt.Sprintf("%d月%d日", m, d)
}
