package main

// article_blocks.go — 文章正文「块」的格式说明书，块长什么样只在这里定。
//
// 公众号网址、供应商网页、FoodTalks 已发布稿抽出来都是一串块（extractArticleBlocks），公众号、
// FoodTalks、头条号、百家号的排版都读它；浏览器里粘贴纯文本也产出同样的块（parser.js
// buildClipboardArticleBlocks）。Word、Markdown 在浏览器里各自分类，不经块。
//
// 字段的意思写在字段的 doc 标签里，块的种类在 articleBlockKinds。给人和模型读的
// docs/article-blocks.md、给程序校验的 docs/article-blocks.schema.json 都由这里生成
// （cd api && SPEC_UPDATE=1 go test -run TestArticleBlockSpec .），和这里对不上测试就失败。

import "strings"

type Block struct {
	K           string        `json:"k" doc:"块的种类，见「块的种类」"`
	Level       int           `json:"level,omitempty" doc:"小标题级别。2 是标题1（一级），3 是标题2（二级，含「（一）」「1、」这类序号句），4 是更低一级；0 是原文没写标题标签、按字号颜色认出来的，由各渠道按一级处理或自己定级"`
	Text        string        `json:"text,omitempty" doc:"纯文字。小标题、图注只用它；段落一般用 runs，没有 runs 时才看它"`
	Runs        []Run         `json:"runs,omitempty" doc:"行内片段，按顺序拼起来就是这一段；加粗、链接都在片段上"`
	Src         string        `json:"src,omitempty" doc:"图片或视频的原地址（公众号图床等），排版时换成转存后的地址"`
	DataW       string        `json:"dataW,omitempty" doc:"图片原始宽度（像素，字符串），原文写了才有"`
	DataH       string        `json:"dataH,omitempty" doc:"图片原始高度（像素，字符串），原文写了才有"`
	DataRatio   string        `json:"dataRatio,omitempty" doc:"图片高宽比（字符串），原文写了才有"`
	Rows        [][]TableCell `json:"rows,omitempty" doc:"表格的行，每行是一串单元格"`
	Poster      string        `json:"poster,omitempty" doc:"视频封面图地址"`
	BlankBefore bool          `json:"blankBefore,omitempty" doc:"原文在这块前面有一个空段。公众号据此留出段距，FoodTalks 写成空段，头条号、百家号不管"`
	Compact     bool          `json:"compact,omitempty" doc:"只在浏览器粘贴纯文本时出现：每行一段，段间不留公众号段距（除非 blankBefore）；整篇长段之间基本不空行（飞书、Word 复制）时每段都不排紧"`
}

type TableCell struct {
	Runs   []Run `json:"runs,omitempty" doc:"单元格里的文字片段"`
	Header bool  `json:"header,omitempty" doc:"表头格（原文 th 或表头行）"`
}

type Run struct {
	Type string `json:"type" doc:"txt 是普通文字，link 是链接"`
	Text string `json:"text,omitempty" doc:"这一片的文字"`
	Bold bool   `json:"bold,omitempty" doc:"加粗。一段里所有有字的片段都加粗，这段就是「整段加粗」"`
	Href string `json:"href,omitempty" doc:"链接地址，只有 link 才有"`
}

var articleRunTypes = []string{"txt", "link"}

// articleBlocksResponse 抓取接口 POST /api/fetch-article 抓到正文时的返回：块和块外面的几样。
type articleBlocksResponse struct {
	Title             string             `json:"title" doc:"文章标题"`
	Blocks            []Block            `json:"blocks" doc:"正文块，从上到下就是读者看到的顺序"`
	Source            string             `json:"source" doc:"抓取的网址"`
	SourceName        string             `json:"source_name" doc:"来源公众号或网站的名字"`
	Byline            string             `json:"byline" doc:"页面上写的作者（公众号作者栏）。只作参考，不补成正文署名"`
	OriginalSourceURL string             `json:"original_source_url" doc:"公众号「阅读原文」的地址；转载稿的转载声明优先链到它"`
	Provenance        *articleProvenance `json:"provenance,omitempty" doc:"原创还是转载、转载自谁：和发布流程定 FoodTalks 来源类型同一个判定，网站开头署名和文末声明照它写"`
}

// articleProvenance 文章是谁的：和 CMS 原创/转载同一个判定（publishArticleProvenance）。
// 转载但来源是自家号（自家号发的稿子明写「本文为转载文章」、说不出原作者，#294）时不给来源，
// 网站写「本文为转载文章」。
type articleProvenance struct {
	Original bool   `json:"original" doc:"是不是自家原创"`
	Source   string `json:"source,omitempty" doc:"转载自谁；原创或说不出原作者时不给"`
}

type articleBlockKind struct {
	K       string
	Name    string
	Meaning string
	// Fields 这种块会用到的字段（json 名，不含 k），只能是 Block 上有的。
	Fields []string
}

var articleBlockKinds = []articleBlockKind{
	{K: "txt", Name: "段落", Fields: []string{"runs", "text", "blankBefore", "compact"},
		Meaning: "正文段落。所有字都加粗的段落是「整段加粗」，各渠道按加粗段写，不当小标题"},
	{K: "h", Name: "小标题", Fields: []string{"text", "level", "blankBefore"},
		Meaning: "文中的小标题，级别看 level。文章标题不在块里，在块外面的 title"},
	{K: "img", Name: "图片", Fields: []string{"src", "dataW", "dataH", "dataRatio", "blankBefore"},
		Meaning: "一张图。紧跟在它后面的 cap 是这张图的说明"},
	{K: "cap", Name: "图注", Fields: []string{"text", "blankBefore"},
		Meaning: "图片下面的说明文字，或「图片来源：」「图源：」这类出处行，紧跟在 img 后面，可以连着好几条。两种不另设字段，靠文字开头区分。前面没有图时按普通段落"},
	{K: "bq", Name: "引用", Fields: []string{"runs", "blankBefore"},
		Meaning: "原文的引用块（blockquote）"},
	{K: "li", Name: "列表项", Fields: []string{"runs", "blankBefore"},
		Meaning: "列表里的一项，连着的几项是同一个列表。原文是有序还是无序列表不区分"},
	{K: "table", Name: "表格", Fields: []string{"rows", "blankBefore"},
		Meaning: "一张表格，保留行列和表头格"},
	{K: "video", Name: "视频", Fields: []string{"src", "poster", "blankBefore"},
		Meaning: "一段视频。拿不到视频地址时各渠道用封面图代替"},
}

// articleBlockLegacyKinds 2026-07 以前的稿子里块种类的旧写法，读的时候换成现在的，新代码不许再产出。
var articleBlockLegacyKinds = map[string]string{"p": "txt", "image": "img", "blockquote": "bq"}

// canonicalBlockKind 块种类的现在写法（大小写、旧写法都归一）。
func canonicalBlockKind(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	if current, ok := articleBlockLegacyKinds[k]; ok {
		return current
	}
	return k
}
