package main

// 多平台分发的样稿考卷（红线 43 金标准）分两半：编辑手发在各平台的真稿是标准答案。
// 前一半在这里：testdata/distribution-golden/<编号>.json 存一篇稿的 FoodTalks 已发布正文（外发的输入）
// 与正文里每张图的尺寸，用现在的通用规则走一遍真实的外发组装，写出的标题、封面、外发通用版正文块
// 存在 <编号>.blocks.json；块一变测试就失败，看过确认后用 GOLDEN_UPDATE=1 重写。
// 后一半在分发服务（test/golden.test.mjs）：拿这份块按各平台的写法写一遍，和编辑稿按读者看到的样子逐块比。
// 两边各存一份 <编号>.blocks.json，DISTRIBUTION_REPO=… 时 TestDistributionFixturesMatchService 核对。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const distributionGoldenDir = "testdata/distribution-golden"

type distributionGoldenSample struct {
	Job        int64               `json:"job"`
	News       foodTalksNewsDetail `json:"news"`
	ImageSizes map[string][2]int   `json:"image_sizes"`
}

// distributionGoldenBlocks 考卷前一半的答案，也是后一半的输入（分发服务 test/golden/<编号>.blocks.json）。
type distributionGoldenBlocks struct {
	Job    int64                   `json:"job"`
	Title  string                  `json:"title"`
	Cover  distributionGoldenCover `json:"cover"`
	Blocks []Block                 `json:"blocks"`
}

type distributionGoldenCover struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func TestDistributionBlocksGolden(t *testing.T) {
	paths := distributionGoldenSamplePaths(t)
	update := os.Getenv("GOLDEN_UPDATE") != ""
	for _, path := range paths {
		sample := loadDistributionGolden(t, path)
		t.Run(fmt.Sprint(sample.Job), func(t *testing.T) {
			got, err := distributionGoldenOutput(t, sample)
			if err != nil {
				t.Fatalf("#%d 组装失败：%v", sample.Job, err)
			}
			encoded := encodeDistributionGolden(t, got)
			recordPath := strings.TrimSuffix(path, ".json") + ".blocks.json"
			if update {
				if err := os.WriteFile(recordPath, encoded, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			recorded, err := os.ReadFile(recordPath)
			if err != nil {
				t.Fatalf("没有 %s：GOLDEN_UPDATE=1 go test -run TestDistributionBlocksGolden 生成后逐块看过再提交", recordPath)
			}
			if !bytes.Equal(recorded, encoded) {
				var before distributionGoldenBlocks
				_ = json.Unmarshal(recorded, &before)
				t.Errorf("#%d 的外发正文块变了（%s）：\n%s\n看过确认后用 GOLDEN_UPDATE=1 go test -run TestDistributionBlocksGolden 重写，再拷到分发服务 test/golden/ 跑它那一半。",
					sample.Job, recordPath, distributionGoldenChange(before, got))
			}
		})
	}
}

func distributionGoldenSamplePaths(t *testing.T) []string {
	t.Helper()
	all, err := filepath.Glob(filepath.Join(distributionGoldenDir, "*.json"))
	var paths []string
	for _, path := range all {
		if !strings.HasSuffix(path, ".blocks.json") {
			paths = append(paths, path)
		}
	}
	if err != nil || len(paths) == 0 {
		t.Fatalf("没有样稿：%v", err)
	}
	sort.Strings(paths)
	return paths
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

// distributionGoldenOutput 走真实外发组装：先用 FoodTalks 已发布正文盖到任务上，再按各平台一样的规则
// 出标题、封面与正文块（交给分发服务的那一份）。封面够不够平台下限归分发服务那一半看，这里不按平台挑。
func distributionGoldenOutput(t *testing.T, sample distributionGoldenSample) (distributionGoldenBlocks, error) {
	t.Helper()
	previous := distributionImageSize
	distributionImageSize = func(src string) (int, int, bool) {
		size, ok := sample.ImageSizes[src]
		if !ok {
			t.Errorf("样稿 #%d 缺这张图的尺寸，重新生成样稿：%s", sample.Job, src)
		}
		return size[0], size[1], size[0] > 0 && size[1] > 0
	}
	defer func() { distributionImageSize = previous }()

	job, err := overlayPublishJobWithPublishedNews(publishJob{ID: sample.Job, FoodTalksNewsID: sample.News.ID}, sample.News)
	if err != nil {
		return distributionGoldenBlocks{}, err
	}
	content, err := buildDistributionContent(job, distributionPlatform{}, publishExternalDraft{})
	if err != nil {
		return distributionGoldenBlocks{}, err
	}
	size := sample.ImageSizes[content.Cover]
	return distributionGoldenBlocks{
		Job:    sample.Job,
		Title:  externalEffectiveTitle(job, publishExternalDraft{}),
		Cover:  distributionGoldenCover{URL: content.Cover, Width: size[0], Height: size[1]},
		Blocks: content.Blocks,
	}, nil
}

func encodeDistributionGolden(t *testing.T, value any) []byte {
	t.Helper()
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// distributionGoldenChange 记录与这次结果比，哪几块没了、哪几块是新的（按块读起来的样子）。
func distributionGoldenChange(before, now distributionGoldenBlocks) string {
	var out strings.Builder
	if before.Title != now.Title {
		fmt.Fprintf(&out, "标题：「%s」→「%s」\n", before.Title, now.Title)
	}
	if before.Cover != now.Cover {
		fmt.Fprintf(&out, "封面：%+v → %+v\n", before.Cover, now.Cover)
	}
	lines := func(blocks []Block) map[string]int {
		set := map[string]int{}
		for _, line := range strings.Split(distributionOutline(blocks), "\n") {
			set[line]++
		}
		return set
	}
	was, is := lines(before.Blocks), lines(now.Blocks)
	for _, line := range strings.Split(distributionOutline(now.Blocks), "\n") {
		if was[line] > 0 {
			was[line]--
			continue
		}
		out.WriteString("新出现：" + line + "\n")
	}
	for _, line := range strings.Split(distributionOutline(before.Blocks), "\n") {
		if is[line] > 0 {
			is[line]--
			continue
		}
		out.WriteString("没了：" + line + "\n")
	}
	if out.Len() == 0 {
		return "块的内容没变，顺序变了"
	}
	return out.String()
}

// GOLDEN_BUILD_RAW=<目录> DISTRIBUTION_REPO=<分发工具仓库> go test -run TestDistributionGoldenBuild：
// 把抓来的原始数据生成样稿，要联网量图片尺寸。FoodTalks 正文与图片尺寸写进这里的 <编号>.json，
// 编辑在各平台手发的正文写进分发服务 test/golden/<编号>.<平台>.json。目录里的文件见 testdata/distribution-golden/README.md。
func TestDistributionGoldenBuild(t *testing.T) {
	raw := os.Getenv("GOLDEN_BUILD_RAW")
	repo := os.Getenv("DISTRIBUTION_REPO")
	if raw == "" || repo == "" {
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
		sample := distributionGoldenSample{News: news, ImageSizes: map[string][2]int{}}
		fmt.Sscan(id, &sample.Job)
		manuals := map[string]distributionGoldenManual{}
		if data, err := os.ReadFile(filepath.Join(raw, "tt-"+id+".json")); err == nil {
			var tt struct {
				GroupID string `json:"group_id"`
				Title   string `json:"title"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(data, &tt); err != nil {
				t.Fatal(err)
			}
			manuals["toutiao"] = distributionGoldenManual{
				URL: "https://www.toutiao.com/article/" + tt.GroupID + "/", Title: tt.Title, HTML: cleanDistributionGoldenHTML(tt.Content),
			}
		}
		if manual, ok := baijiahao[id]; ok {
			manual.HTML = cleanDistributionGoldenHTML(manual.HTML)
			manuals["baijiahao"] = manual
		}
		if len(manuals) == 0 {
			t.Fatalf("#%s 没有任何平台的编辑稿", id)
		}

		// 正文里的图与封面都量一遍，再组装一次，把组装时问到尺寸的图（换过地址的）也记下。
		measure := func(src string) {
			if _, done := sample.ImageSizes[src]; done || src == "" {
				return
			}
			width, height, _ := fetchImageSize(src)
			sample.ImageSizes[src] = [2]int{width, height}
		}
		root := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
		nodes, _ := html.ParseFragment(strings.NewReader(news.Content), root)
		for _, n := range nodes {
			root.AppendChild(n)
		}
		for _, img := range distributionGoldenFind(root, "img") {
			for _, a := range img.Attr {
				if a.Key == "src" {
					measure(publicMediaURL(a.Val))
				}
			}
		}
		measure(publicMediaURL(news.CoverImg))
		previous := distributionImageSize
		distributionImageSize = func(src string) (int, int, bool) {
			measure(src)
			size := sample.ImageSizes[src]
			return size[0], size[1], size[0] > 0 && size[1] > 0
		}
		job, err := overlayPublishJobWithPublishedNews(publishJob{ID: sample.Job, FoodTalksNewsID: news.ID}, news)
		if err == nil {
			content, buildErr := buildDistributionContent(job, distributionPlatform{}, publishExternalDraft{})
			measure(content.Cover)
			err = buildErr
		}
		distributionImageSize = previous
		if err != nil {
			t.Errorf("#%s 组装失败：%v", id, err)
		}

		if err := os.WriteFile(filepath.Join(distributionGoldenDir, id+".json"), encodeDistributionGolden(t, distributionGoldenFile(sample)), 0o644); err != nil {
			t.Fatal(err)
		}
		for key, manual := range manuals {
			if err := os.WriteFile(filepath.Join(repo, "test", "golden", id+"."+key+".json"), encodeDistributionGolden(t, manual), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("#%s：%d 张图，平台 %d 个", id, len(sample.ImageSizes), len(manuals))
	}
}

// distributionGoldenManual 编辑在一个平台上手发的那一篇（分发服务 test/golden/<编号>.<平台>.json）。
type distributionGoldenManual struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	HTML  string `json:"html"`
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
		Job        int64             `json:"job"`
		News       news              `json:"news"`
		ImageSizes map[string][2]int `json:"image_sizes"`
	}{sample.Job, news{n.ID, n.Title, n.CoverImg, n.SourceType, n.SourceName, n.Author, n.Summary, n.PublishStatus, n.Content}, sample.ImageSizes}
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
		for _, a := range n.Attr {
			if a.Key == "class" && strings.Contains(a.Val, "pgc-img-caption") {
				out.Attr = []html.Attribute{{Key: "class", Val: "pgc-img-caption"}}
			}
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
