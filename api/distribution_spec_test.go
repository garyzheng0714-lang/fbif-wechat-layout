package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// 每个平台一份分发说明书 docs/platforms/<平台>.md，由平台 profile 和外发排版代码真跑出来；
// 改了规则不重新生成测试就失败（SPEC_UPDATE=1 重写）。
func TestDistributionPlatformSpec(t *testing.T) {
	restore := yunsuoImageSize
	yunsuoImageSize = func(src string) (int, int, bool) {
		if src == distributionSpecNameCard {
			return 1080, 304, true
		}
		return 0, 0, false
	}
	defer func() { yunsuoImageSize = restore }()

	keys := distributionSpecPlatformKeys()
	for _, key := range keys {
		doc, err := renderDistributionPlatformSpec(yunsuoPlatformProfiles[key])
		if err != nil {
			t.Fatalf("%s：%v", key, err)
		}
		checkGeneratedSpecFile(t, filepath.Join("../docs/platforms", key+".md"), doc)
	}
	existing, _ := filepath.Glob("../docs/platforms/*.md")
	for _, path := range existing {
		if !containsString(keys, strings.TrimSuffix(filepath.Base(path), ".md")) {
			t.Errorf("%s 没有对应的平台 profile：删掉它", path)
		}
	}
}

func distributionSpecPlatformKeys() []string {
	keys := make([]string, 0, len(yunsuoPlatformProfiles))
	for key := range yunsuoPlatformProfiles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

const (
	distributionSpecImage    = "https://img.example.com/photo.jpg"
	distributionSpecNameCard = "https://img.example.com/name-card.jpg"
	distributionSpecVideo    = "https://img.example.com/clip.mp4"
)

// distributionSpecMirrored 例子里的图都当已转存：外发只用转存过的图。
var distributionSpecMirrored = map[string]string{
	distributionSpecImage: distributionSpecImage, distributionSpecNameCard: distributionSpecNameCard, distributionSpecVideo: distributionSpecVideo,
}

func specTxt(text string) Block {
	return Block{K: "txt", Runs: []Run{{Type: "txt", Text: text}}}
}

// distributionSpecMarkupRows 每种块各一个最小的例子，看这个平台写成什么。
var distributionSpecMarkupRows = []struct {
	Name   string
	Blocks []Block
}{
	{"小标题（标题1，level 0–2）", []Block{{K: "h", Level: 2, Text: "小标题"}}},
	{"二级标题（level 3、4）", []Block{{K: "h", Level: 3, Text: "二级标题"}}},
	{"整段加粗的段落", []Block{{K: "txt", Runs: []Run{{Type: "txt", Text: "整段加粗的一句话", Bold: true}}}}},
	{"段落（含局部加粗）", []Block{{K: "txt", Runs: []Run{{Type: "txt", Text: "段落里"}, {Type: "txt", Text: "局部加粗", Bold: true}, {Type: "txt", Text: "的字。"}}}}},
	{"图片带图注", []Block{{K: "img", Src: distributionSpecImage}, {K: "cap", Text: "图注"}}},
	{"图片不带图注", []Block{{K: "img", Src: distributionSpecImage}}},
	{"引用", []Block{{K: "bq", Runs: []Run{{Type: "txt", Text: "引用的话"}}}}},
	{"列表", []Block{{K: "li", Runs: []Run{{Type: "txt", Text: "第一项"}}}, {K: "li", Runs: []Run{{Type: "txt", Text: "第二项"}}}}},
	{"表格", []Block{{K: "table", Rows: [][]TableCell{{{Header: true, Runs: []Run{{Type: "txt", Text: "表头"}}}}, {{Runs: []Run{{Type: "txt", Text: "内容"}}}}}}}},
	{"视频", []Block{{K: "video", Src: distributionSpecVideo, Poster: distributionSpecImage}}},
}

// distributionSpecRules 各平台一样的规则（yunsuo_distribution_rules.go），每条配一篇最小的稿子真跑一遍。
var distributionSpecRules = []struct {
	Name, Rule string
	Blocks     []Block
}{
	{"署名挪到文末声明", "开头不放署名。开头几块和正文最后的署名行（作者、编辑、编译稿的英文来源、中文编辑……）按原文顺序写进文末「本文为FBIF食品饮料创新原创，作者：X，编辑：Y，转载请联系授权。」",
		[]Block{specTxt("作者：Sage"), specTxt("编辑：Bobo"), specTxt("正文第一段。")}},
	{"小标题编号", "两个以上的标题1 编「一、二、」（结语也编）；原文已经编了号、以及参考来源，不动。",
		[]Block{{K: "h", Level: 2, Text: "市场变了"}, specTxt("正文。"), {K: "h", Level: 2, Text: "新品牌冒头"}, specTxt("正文。")}},
	{"图注与图片来源并成一条", "图下面的说明和「图片来源」用「；」并成一条（前一句以！？收尾就不加），不截短；首图留在原位带图注。",
		[]Block{{K: "img", Src: distributionSpecImage}, {K: "cap", Text: "▲ 苦味气泡水"}, {K: "cap", Text: "图片来源：品牌官网"}, specTxt("正文。")}},
	{"正文不带链接", "链接只留字，裸网址删掉。",
		[]Block{{K: "txt", Runs: []Run{{Type: "txt", Text: "详见"}, {Type: "link", Text: "品牌官网", Href: "https://example.com/news"}, {Type: "txt", Text: "，或访问https://example.com/news。"}}}}},
	{"去掉作者名片", "作者名片（参考来源上方或之后的无图注图；没有参考来源时，文末无图注的宽条图）不放：平台会提示「图片含二维码」并限制推荐。",
		[]Block{specTxt("正文最后一段。"), {K: "img", Src: distributionSpecNameCard}}},
}

func renderDistributionPlatformSpec(p yunsuoPlatformProfile) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s（分发说明书）\n\n", p.Name)
	b.WriteString("> 由 `api/yunsuo_platform_profiles.go` 和外发排版代码生成，别手改：改了规则跑 `cd api && SPEC_UPDATE=1 go test -run TestDistributionPlatformSpec .`。块是什么见 `docs/article-blocks.md`，各平台共用的规则在 `api/yunsuo_distribution_rules.go`。\n\n")

	b.WriteString("## 稿子怎么送进平台\n\n")
	fmt.Fprintf(&b, "经自己的分发服务（FBIF-多平台分发工具，在 B 机），只存草稿、不发布。编辑器每一步怎么点、找不到按钮时模型兜底的边界，见分发工具的 `docs/platforms/%s.md`。存完草稿当场读回核对标题、小标题、加粗、图片、图注、封面。\n\n", p.Key)

	b.WriteString("## 稿子要求\n\n| 项 | 要求 |\n| --- | --- |\n")
	fmt.Fprintf(&b, "| 标题 | %s |\n", distributionSpecTitle(p))
	if p.SummaryMax > 0 {
		fmt.Fprintf(&b, "| 摘要 | 最多 %d 字 |\n", p.SummaryMax)
	} else {
		b.WriteString("| 摘要 | 不查 |\n")
	}
	fmt.Fprintf(&b, "| 封面 | %s |\n", distributionSpecCover(p))
	fmt.Fprintf(&b, "| 每天上限 | %d 篇（按账号计数，人工按钮发的也算） |\n", p.DailyLimit)
	fmt.Fprintf(&b, "| 文章链接 | 只认 `%s`；%s |\n", strings.Join(p.URLHosts, "` `"), distributionSpecLinkLookup(p))
	b.WriteString("| 核对 | 分发服务存完草稿当场读回，对不上的逐条记下、在审核卡上写灰字 |\n")
	if p.ManageURL != "" {
		fmt.Fprintf(&b, "| 后台内容管理页 | %s |\n", p.ManageURL)
	}

	b.WriteString("\n## 正文怎么写\n\n每种块写成什么（块的意思见 `docs/article-blocks.md`）。段与段之间不插空行，平台自带段距。\n\n| 原文 | 写成 |\n| --- | --- |\n")
	for _, row := range distributionSpecMarkupRows {
		html, _ := writeYunsuoBlocks(row.Blocks, distributionSpecMirrored, p.Markup)
		fmt.Fprintf(&b, "| %s | `%s` |\n", row.Name, strings.ReplaceAll(html, "|", "\\|"))
	}

	b.WriteString("\n## 各平台一样的规则，在这个平台上的样子\n\n")
	for _, rule := range distributionSpecRules {
		raw, err := json.Marshal(rule.Blocks)
		if err != nil {
			return "", err
		}
		mirrored, _ := json.Marshal(distributionSpecMirrored)
		rendered, err := renderYunsuoDistribution(string(raw), string(mirrored), yunsuoStatementPublisher, p, "", "")
		if err != nil {
			return "", fmt.Errorf("%s：%w", rule.Name, err)
		}
		fmt.Fprintf(&b, "### %s\n\n%s\n\n原文：\n\n", rule.Name, rule.Rule)
		for _, block := range rule.Blocks {
			fmt.Fprintf(&b, "- %s\n", distributionSpecReaderLine(block))
		}
		fmt.Fprintf(&b, "\n写成：\n\n```html\n%s\n```\n\n", rendered.HTML)
	}

	b.WriteString("## 标准答案\n\n")
	samples, _ := filepath.Glob(fmt.Sprintf("testdata/distribution-golden/*.%s.txt", p.Key))
	ids := make([]string, 0, len(samples))
	for _, path := range samples {
		ids = append(ids, "#"+strings.TrimSuffix(filepath.Base(path), "."+p.Key+".txt"))
	}
	fmt.Fprintf(&b, "编辑在%s手发的稿是标准答案。样稿考卷 `api/testdata/distribution-golden/` 收了 %s，每篇用现在的规则走一遍真实外发组装，和编辑稿还差哪几处记在 `<编号>.%s.txt`，差别一变 `TestDistributionGolden` 就失败。\n", p.Name, strings.Join(ids, " "), p.Key)
	return b.String(), nil
}

func distributionSpecTitle(p yunsuoPlatformProfile) string {
	min, max := p.titleLimits()
	note := ""
	if n := p.titleCountNote(); n != "" {
		note = "，" + n
	}
	return fmt.Sprintf("原样送，%d–%d 字%s；超了记为待修改，在卡片或侧边栏改", min, max, note)
}

func distributionSpecCover(p yunsuoPlatformProfile) string {
	out := "用公众号封面（和 FoodTalks 一样），分发服务在编辑器的封面弹窗里单独上传，不进正文"
	if p.CoverMinWidth > 0 {
		out += fmt.Sprintf("。最小 %d×%d；差一点（放大到 %.1f 倍以内能达标）自动放大一份转存，差得多记为待修改，在卡片里选正文大图或在侧边栏上传", p.CoverMinWidth, p.CoverMinHeight, externalCoverUpscaleMax)
	} else {
		out += "。不设尺寸下限"
	}
	return out
}

func distributionSpecLinkLookup(p yunsuoPlatformProfile) string {
	switch p.LinkLookup {
	case externalLinkLookupToutiao:
		return "草稿发出去后人工登记；登记时可让系统经头条搜索加 TikHub 找（作者是自家号、标题对上、发文时间在存稿之后）"
	case "":
		return "草稿发出去后人工登记"
	}
	return p.LinkLookup
}

// distributionSpecReaderLine 一块在读者眼里是什么。
func distributionSpecReaderLine(block Block) string {
	name := block.K
	for _, kind := range articleBlockKinds {
		if kind.K == block.K {
			name = kind.Name
		}
	}
	switch block.K {
	case "img":
		if block.Src == distributionSpecNameCard {
			return "图片：作者名片（宽条图，无图注）"
		}
		return "图片"
	case "h":
		return fmt.Sprintf("%s（level %d）：%s", name, block.Level, block.Text)
	}
	var text strings.Builder
	for _, run := range block.Runs {
		text.WriteString(run.Text)
		if run.Type == "link" {
			fmt.Fprintf(&text, "（链接 %s）", run.Href)
		}
	}
	if text.Len() == 0 {
		text.WriteString(block.Text)
	}
	return name + "：" + text.String()
}
