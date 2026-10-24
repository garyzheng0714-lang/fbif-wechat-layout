package main

// 多平台分发的样稿考卷（红线 43 金标准）：编辑手发在各平台的真稿就是标准答案。
// testdata/distribution-golden/<编号>.json 存一篇稿的 FoodTalks 已发布正文（外发的输入）、
// 正文里每张图的尺寸、各平台编辑手发的正文。考的时候用现在的规则把 FoodTalks 正文走一遍真实的
// 外发组装，和编辑稿按读者在平台上看到的样子逐块比；和编辑稿还差哪几处记在 <编号>.<平台>.txt。
// 规则一改差别就变，测试失败并列出多了、少了哪几处；看过确认（变好了，或编辑稿那处是个例）后
// 用 GOLDEN_UPDATE=1 重写记录。加样稿见 testdata/distribution-golden/README.md。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const distributionGoldenDir = "testdata/distribution-golden"

type distributionGoldenSample struct {
	Job        int64                               `json:"job"`
	News       foodTalksNewsDetail                 `json:"news"`
	ImageSizes map[string][2]int                   `json:"image_sizes"`
	Platforms  map[string]distributionGoldenManual `json:"platforms"`
}

// distributionGoldenManual 编辑在这个平台上手发的那一篇。
type distributionGoldenManual struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	HTML  string `json:"html"`
}

type distributionGoldenBlock struct {
	Kind string
	Text string
}

func TestDistributionGolden(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(distributionGoldenDir, "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("没有样稿：%v", err)
	}
	update := os.Getenv("GOLDEN_UPDATE") != ""
	for _, path := range paths {
		sample := loadDistributionGolden(t, path)
		keys := make([]string, 0, len(sample.Platforms))
		for key := range sample.Platforms {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			t.Run(fmt.Sprintf("%d-%s", sample.Job, key), func(t *testing.T) {
				report := distributionGoldenReport(t, sample, key)
				recordPath := strings.TrimSuffix(path, ".json") + "." + key + ".txt"
				if update {
					if err := os.WriteFile(recordPath, []byte(report), 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				recorded, err := os.ReadFile(recordPath)
				if err != nil {
					t.Fatalf("没有 %s：GOLDEN_UPDATE=1 go test -run TestDistributionGolden 生成后逐条看过再提交", recordPath)
				}
				if string(recorded) != report {
					t.Errorf("#%d %s和编辑手发稿的差别变了（%s）：\n%s\n看过确认后用 GOLDEN_UPDATE=1 go test -run TestDistributionGolden 重写记录。",
						sample.Job, yunsuoPlatformProfiles[key].Name, recordPath, distributionGoldenChange(string(recorded), report))
				}
			})
		}
	}
}

func loadDistributionGolden(t *testing.T, path string) distributionGoldenSample {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var sample distributionGoldenSample
	if err := json.Unmarshal(raw, &sample); err != nil {
		t.Fatalf("%s：%v", path, err)
	}
	return sample
}

// distributionGoldenReport 用现在的规则组装这篇推给这个平台的内容，写出和编辑稿的差别。
func distributionGoldenReport(t *testing.T, sample distributionGoldenSample, key string) string {
	t.Helper()
	profile, ok := yunsuoPlatformProfiles[key]
	if !ok {
		t.Fatalf("样稿 #%d 里的平台 %s 没有 profile", sample.Job, key)
	}
	sizes := map[string][2]int{}
	for src, size := range sample.ImageSizes {
		sizes[src] = size
	}
	previous := yunsuoImageSize
	yunsuoImageSize = func(src string) (int, int, bool) {
		size, ok := sizes[src]
		if !ok {
			t.Errorf("样稿 #%d 缺这张图的尺寸，重新生成样稿：%s", sample.Job, src)
		}
		return size[0], size[1], size[0] > 0 && size[1] > 0
	}
	t.Cleanup(func() { yunsuoImageSize = previous })

	title, body, notes, err := distributionGoldenOutput(sample, profile, sizes)
	if err != nil {
		t.Fatalf("#%d 推%s组装失败：%v", sample.Job, profile.Name, err)
	}
	manual := sample.Platforms[key]
	editor := distributionReaderView(manual.HTML, profile)
	ours := distributionReaderView(body, profile)

	var out strings.Builder
	fmt.Fprintf(&out, "#%d %s\n%s编辑手发：%s\n", sample.Job, strings.TrimSpace(manual.Title), profile.Name, manual.URL)
	diffs := distributionGoldenDifferences(editor, ours)
	if strings.TrimSpace(manual.Title) != title {
		diffs = append([]string{fmt.Sprintf("标题不同：编辑稿「%s」，我们「%s」", strings.TrimSpace(manual.Title), title)}, diffs...)
	}
	fmt.Fprintf(&out, "编辑稿正文 %d 块，和编辑稿不同 %d 处\n", len(editor), len(diffs))
	for _, line := range append(notes, diffs...) {
		out.WriteString("- " + line + "\n")
	}
	return out.String()
}

// distributionGoldenOutput 走真实外发组装：先用 FoodTalks 已发布正文盖到任务上，再按这个平台的写法
// 出标题与正文（交给分发服务的那一份）。封面不到平台下限时系统会停下等人换封面（或自动放大），正文不受影响，这里按换好算。
func distributionGoldenOutput(sample distributionGoldenSample, profile yunsuoPlatformProfile, sizes map[string][2]int) (string, string, []string, error) {
	job, err := overlayPublishJobWithPublishedNews(publishJob{ID: sample.Job, FoodTalksNewsID: sample.News.ID}, sample.News)
	if err != nil {
		return "", "", nil, err
	}
	var notes []string
	_, rendered, err := renderExternalBody(job, profile, publishExternalDraft{})
	if err != nil {
		return "", "", nil, err
	}
	if width, height, small := profile.coverTooSmall(rendered.Cover); small {
		notes = append(notes, fmt.Sprintf("封面 %d×%d 不到%s下限 %d×%d，按换好封面算", width, height, profile.Name, profile.CoverMinWidth, profile.CoverMinHeight))
		sizes[rendered.Cover] = [2]int{profile.CoverMinWidth, profile.CoverMinHeight}
	}
	payload, err := buildRobotDraftPayload(job, profile, publishExternalDraft{})
	return payload.Title, payload.HTML, notes, err
}

var distributionGoldenHeadingTag = regexp.MustCompile(`^<(h[1-6])\b`)

// distributionReaderView 读者在这个平台上看到的样子：小标题、加粗段、段落、图片、图注、表格。
// 平台写法若把加粗行写成标题标签（到平台上是加粗段），这个标签算加粗段。
func distributionReaderView(src string, profile yunsuoPlatformProfile) []distributionGoldenBlock {
	boldTag := ""
	if m := distributionGoldenHeadingTag.FindStringSubmatch(profile.Markup.BoldLine("x")); m != nil {
		boldTag = m[1]
	}
	nodes, err := html.ParseFragment(strings.NewReader(src), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return []distributionGoldenBlock{{Kind: "解析失败", Text: err.Error()}}
	}
	var out []distributionGoldenBlock
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		switch n.Data {
		case "img":
			out = append(out, distributionGoldenBlock{Kind: "图片"})
			return
		case "table":
			out = append(out, distributionGoldenBlock{Kind: "表格", Text: distributionGoldenText(n)})
			return
		case "h1", "h2", "h3", "h4", "h5", "h6", "p", "blockquote", "li":
			for range distributionGoldenFind(n, "img") {
				out = append(out, distributionGoldenBlock{Kind: "图片"})
			}
			text := distributionGoldenText(n)
			if text == "" {
				return
			}
			kind := "段落"
			switch {
			case n.Data == boldTag:
				kind = "加粗段"
			case strings.HasPrefix(n.Data, "h"):
				kind = "小标题"
			case distributionGoldenHasClass(n, "pgc-img-caption") || distributionGoldenOnlyChild(n, "small"):
				kind = "图注"
			case distributionGoldenAllBold(n):
				kind = "加粗段"
			}
			out = append(out, distributionGoldenBlock{Kind: kind, Text: text})
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return out
}

func distributionGoldenText(n *html.Node) string {
	var sb strings.Builder
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			sb.WriteString(n.Data)
		case n.Type == html.ElementNode && (n.Data == "br" || n.Data == "td" || n.Data == "th"):
			sb.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(n)
	text := strings.NewReplacer("\u200b", "", "\ufeff", "").Replace(sb.String())
	return strings.Join(strings.Fields(text), " ")
}

func distributionGoldenFind(n *html.Node, tag string) []*html.Node {
	var found []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == tag {
			found = append(found, c)
		}
		found = append(found, distributionGoldenFind(c, tag)...)
	}
	return found
}

func distributionGoldenHasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" && strings.Contains(a.Val, class) {
			return true
		}
	}
	return false
}

func distributionGoldenOnlyChild(n *html.Node, tag string) bool {
	var element *html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == html.TextNode && strings.TrimSpace(c.Data) == "":
		case c.Type == html.ElementNode && element == nil:
			element = c
		default:
			return false
		}
	}
	return element != nil && element.Data == tag
}

func distributionGoldenAllBold(n *html.Node) bool {
	bold := false
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == html.TextNode && strings.TrimSpace(c.Data) == "":
		case c.Type == html.ElementNode && c.Data == "br":
		case c.Type == html.ElementNode && (c.Data == "strong" || c.Data == "b"):
			bold = true
		default:
			return false
		}
	}
	return bold
}

// distributionGoldenDifferences 按块对齐（最长公共子序列），对不上的地方逐处写出来。
func distributionGoldenDifferences(editor, ours []distributionGoldenBlock) []string {
	n, m := len(editor), len(ours)
	same := func(i, j int) bool { return editor[i] == ours[j] }
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case same(i, j):
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var out []string
	var onlyEditor, onlyOurs []int
	flush := func() {
		k := 0
		for ; k < len(onlyEditor) && k < len(onlyOurs); k++ {
			e, o := editor[onlyEditor[k]], ours[onlyOurs[k]]
			switch {
			case e.Text == o.Text:
				out = append(out, fmt.Sprintf("编辑稿是%s，我们是%s：%s", e.Kind, o.Kind, distributionGoldenClip(e.Text, 40)))
			case e.Kind == o.Kind:
				before, after := distributionGoldenChangedSpan(e.Text, o.Text)
				out = append(out, fmt.Sprintf("%s不同：编辑稿「%s」，我们「%s」", e.Kind, before, after))
			default:
				out = append(out, distributionGoldenOnly("编辑稿", editor, onlyEditor[k]), distributionGoldenOnly("我们", ours, onlyOurs[k]))
			}
		}
		for _, i := range onlyEditor[k:] {
			out = append(out, distributionGoldenOnly("编辑稿", editor, i))
		}
		for _, j := range onlyOurs[k:] {
			out = append(out, distributionGoldenOnly("我们", ours, j))
		}
		onlyEditor, onlyOurs = nil, nil
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && same(i, j):
			flush()
			i++
			j++
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			onlyOurs = append(onlyOurs, j)
			j++
		default:
			onlyEditor = append(onlyEditor, i)
			i++
		}
	}
	flush()
	return out
}

func distributionGoldenOnly(who string, blocks []distributionGoldenBlock, i int) string {
	where := "开头"
	for k := i - 1; k >= 0; k-- {
		if blocks[k].Text != "" {
			where = "「" + distributionGoldenClip(blocks[k].Text, 12) + "」之后"
			break
		}
	}
	line := fmt.Sprintf("只有%s有的%s（%s）", who, blocks[i].Kind, where)
	if blocks[i].Text != "" {
		line += "：" + distributionGoldenClip(blocks[i].Text, 40)
	}
	return line
}

// distributionGoldenChangedSpan 两段字只差一部分时，只截出不同的那一截，前后各带几个字。
func distributionGoldenChangedSpan(a, b string) (string, string) {
	ra, rb := []rune(a), []rune(b)
	prefix := 0
	for prefix < len(ra) && prefix < len(rb) && ra[prefix] == rb[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(ra)-prefix && suffix < len(rb)-prefix && ra[len(ra)-1-suffix] == rb[len(rb)-1-suffix] {
		suffix++
	}
	const context = 8
	start := max(prefix-context, 0)
	span := func(r []rune) string {
		end := min(len(r)-suffix+context, len(r))
		text := string(r[start:end])
		if start > 0 {
			text = "…" + text
		}
		if end < len(r) {
			text += "…"
		}
		return distributionGoldenClip(text, 60)
	}
	return span(ra), span(rb)
}

func distributionGoldenClip(text string, limit int) string {
	r := []rune(text)
	if len(r) <= limit {
		return text
	}
	return string(r[:limit]) + "…"
}

// distributionGoldenChange 记录与这次结果比，哪些差别没了、哪些是新的。
func distributionGoldenChange(recorded, now string) string {
	lines := func(s string) map[string]bool {
		set := map[string]bool{}
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(line, "- ") {
				set[line] = true
			}
		}
		return set
	}
	before, after := lines(recorded), lines(now)
	var out strings.Builder
	for _, line := range strings.Split(now, "\n") {
		if strings.HasPrefix(line, "- ") && !before[line] {
			out.WriteString("新出现（离编辑稿更远）" + line + "\n")
		}
	}
	for _, line := range strings.Split(recorded, "\n") {
		if strings.HasPrefix(line, "- ") && !after[line] {
			out.WriteString("没了（离编辑稿更近）" + line + "\n")
		}
	}
	if out.Len() == 0 {
		return "差别条目没变，块数或标题行变了：\n记录：\n" + recorded + "现在：\n" + now
	}
	return out.String()
}

// GOLDEN_BUILD_RAW=<目录> go test -run TestDistributionGoldenBuild：把抓来的原始数据生成样稿，
// 要联网量图片尺寸。目录里的文件见 testdata/distribution-golden/README.md。
func TestDistributionGoldenBuild(t *testing.T) {
	raw := os.Getenv("GOLDEN_BUILD_RAW")
	if raw == "" {
		t.Skip("生成样稿时才跑")
	}
	baijiahao := map[string]distributionGoldenManual{}
	if data, err := os.ReadFile(filepath.Join(raw, "bjh.json")); err == nil {
		if err := json.Unmarshal(data, &baijiahao); err != nil {
			t.Fatal(err)
		}
	}
	sources, _ := filepath.Glob(filepath.Join(raw, "ft-*.json"))
	if len(sources) == 0 {
		t.Fatalf("%s 里没有 ft-<编号>.json", raw)
	}
	for _, source := range sources {
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(source), "ft-"), ".json")
		var news foodTalksNewsDetail
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &news); err != nil {
			t.Fatal(err)
		}
		sample := distributionGoldenSample{News: news, ImageSizes: map[string][2]int{}, Platforms: map[string]distributionGoldenManual{}}
		fmt.Sscan(id, &sample.Job)
		if data, err := os.ReadFile(filepath.Join(raw, "tt-"+id+".json")); err == nil {
			var tt struct {
				GroupID string `json:"group_id"`
				Title   string `json:"title"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(data, &tt); err != nil {
				t.Fatal(err)
			}
			sample.Platforms["toutiao"] = distributionGoldenManual{
				URL: "https://www.toutiao.com/article/" + tt.GroupID + "/", Title: tt.Title, HTML: cleanDistributionGoldenHTML(tt.Content),
			}
		}
		if manual, ok := baijiahao[id]; ok {
			manual.HTML = cleanDistributionGoldenHTML(manual.HTML)
			sample.Platforms["baijiahao"] = manual
		}
		if len(sample.Platforms) == 0 {
			t.Fatalf("#%s 没有任何平台的编辑稿", id)
		}

		measure := func(src string) {
			if _, done := sample.ImageSizes[src]; done || src == "" {
				return
			}
			width, height, _ := fetchYunsuoImageSize(src)
			sample.ImageSizes[src] = [2]int{width, height}
		}
		// 正文里的图都量一遍，再按两个平台各组装一次，把组装时问到尺寸的图（换过地址的）也记下。
		root := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
		nodes, _ := html.ParseFragment(strings.NewReader(news.Content), root)
		for _, n := range nodes {
			root.AppendChild(n)
		}
		for _, img := range distributionGoldenFind(root, "img") {
			for _, a := range img.Attr {
				if a.Key == "src" {
					measure(publicYunsuoMediaURL(a.Val))
				}
			}
		}
		measure(publicYunsuoMediaURL(news.CoverImg))
		fixedCovers := map[string][2]int{}
		previous := yunsuoImageSize
		yunsuoImageSize = func(src string) (int, int, bool) {
			measure(src)
			size, fixed := fixedCovers[src]
			if !fixed {
				size = sample.ImageSizes[src]
			}
			return size[0], size[1], size[0] > 0 && size[1] > 0
		}
		for key := range sample.Platforms {
			if _, _, _, err := distributionGoldenOutput(sample, yunsuoPlatformProfiles[key], fixedCovers); err != nil {
				t.Errorf("#%s 推%s组装失败：%v", id, key, err)
			}
		}
		yunsuoImageSize = previous

		var out bytes.Buffer
		encoder := json.NewEncoder(&out)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(distributionGoldenFile(sample)); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(distributionGoldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(distributionGoldenDir, id+".json"), out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("#%s：%d 张图，平台 %d 个", id, len(sample.ImageSizes), len(sample.Platforms))
	}
}

// distributionGoldenFile 样稿文件只存外发会读的 FoodTalks 字段。
func distributionGoldenFile(sample distributionGoldenSample) any {
	type news struct {
		ID            int64  `json:"id"`
		Title         string `json:"title"`
		CoverImg      string `json:"coverImg"`
		SourceType    int    `json:"sourceType"`
		SourceName    string `json:"sourceName"`
		Author        string `json:"author"`
		Summary       string `json:"summary"`
		PublishStatus int    `json:"publishStatus"`
		Content       string `json:"content"`
	}
	n := sample.News
	return struct {
		Job        int64                               `json:"job"`
		News       news                                `json:"news"`
		ImageSizes map[string][2]int                   `json:"image_sizes"`
		Platforms  map[string]distributionGoldenManual `json:"platforms"`
	}{sample.Job, news{n.ID, n.Title, n.CoverImg, n.SourceType, n.SourceName, n.Author, n.Summary, n.PublishStatus, n.Content}, sample.ImageSizes, sample.Platforms}
}

// cleanDistributionGoldenHTML 编辑稿只留比对要用的：标签结构、图注的 class、文字；去掉样式、图片地址
// （平台图床地址会过期，比对只看有没有图）、注释和平台页面的「举报/反馈」。
func cleanDistributionGoldenHTML(src string) string {
	nodes, err := html.ParseFragment(strings.NewReader(src), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return src
	}
	var clean func(n *html.Node) []*html.Node
	clean = func(n *html.Node) []*html.Node {
		switch n.Type {
		case html.CommentNode:
			return nil
		case html.TextNode:
			if strings.TrimSpace(n.Data) == "举报/反馈" {
				return nil
			}
			return []*html.Node{{Type: html.TextNode, Data: n.Data}}
		case html.ElementNode:
		default:
			return nil
		}
		var children []*html.Node
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			children = append(children, clean(c)...)
		}
		switch n.Data {
		case "div", "span", "section", "font", "center":
			return children
		}
		out := &html.Node{Type: html.ElementNode, Data: n.Data, DataAtom: n.DataAtom}
		if distributionGoldenHasClass(n, "pgc-img-caption") {
			out.Attr = []html.Attribute{{Key: "class", Val: "pgc-img-caption"}}
		}
		for _, c := range children {
			out.AppendChild(c)
		}
		return []*html.Node{out}
	}
	var sb strings.Builder
	for _, n := range nodes {
		for _, c := range clean(n) {
			html.Render(&sb, c)
		}
	}
	return sb.String()
}
