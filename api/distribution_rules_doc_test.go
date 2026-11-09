package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// docs/distribution-blocks.md：排版工具交给分发服务的外发通用版正文块，和各平台一样的规则落进块之前、之后的样子，
// 由这里按现在的代码真跑出来；改了规则不重新生成测试就失败（SPEC_UPDATE=1 重写）。
// 每种块在各平台写成什么标签、平台的标题与封面限制只在分发服务的 docs/platforms/<平台>.md，这边不再有一份。
const distributionBlocksDocPath = "../docs/distribution-blocks.md"

func TestDistributionRulesDoc(t *testing.T) {
	stubDistributionImageSize(t, map[string][2]int{distributionDocNameCard: {1080, 304}})
	doc, err := renderDistributionRulesDoc()
	if err != nil {
		t.Fatal(err)
	}
	checkGeneratedSpecFile(t, distributionBlocksDocPath, doc)
}

const (
	distributionDocImage    = "https://img.example.com/photo.jpg"
	distributionDocNameCard = "https://img.example.com/name-card.jpg"
)

// distributionOutgoingKinds 外发通用版正文块的几种块（分发服务 service/blocks.mjs 的 FIELDS 同一份）。
var distributionOutgoingKinds = []struct{ K, Fields, Meaning string }{
	{"h", "text, level", "小标题：level 2 是标题1（原文 level 0–2），3 是标题2（原文 level 3、4）"},
	{"txt", "runs", "段落；整段加粗的段落是一个加粗片段。不认得的块、前面没有图的图注也按段落给"},
	{"bq", "runs", "引用"},
	{"li", "runs", "列表项，连着的几项是一个列表"},
	{"img", "src", "图片，src 是已转存的公网地址；没转存的图连同图注不给"},
	{"cap", "text", "紧跟在 img 后面的图注，图下面的说明与「图片来源」已并成一条"},
	{"table", "rows", "表格，每格 runs，表头格带 header"},
	{"video", "src, poster", "视频；视频地址没转存时改给 img，src 是视频封面"},
}

// distributionDocRules 各平台一样的规则（distribution_rules.go），每条配一篇最小的稿子真跑一遍。
var distributionDocRules = []struct {
	Name, Rule string
	Blocks     []Block
}{
	{"署名挪到文末声明", "开头不放署名。开头几块和正文最后的署名行（作者、编辑、编译稿的英文来源、中文编辑……）按原文顺序写进文末「本文为FBIF食品饮料创新原创，作者：X，编辑：Y，转载请联系授权。」",
		[]Block{specTxt("作者：Sage"), specTxt("编辑：Bobo"), specTxt("正文第一段。")}},
	{"小标题编号", "两个以上的标题1 编「一、二、」（结语也编）；原文已经编了号、以及参考来源，不动。",
		[]Block{{K: "h", Level: 2, Text: "市场变了"}, specTxt("正文。"), {K: "h", Level: 2, Text: "新品牌冒头"}, specTxt("正文。")}},
	{"图注与图片来源并成一条", "图下面的说明和「图片来源」用「；」并成一条（前一句以！？收尾就不加），不截短；公众号几张图左右滑着看时写的「（向左滑动查看更多）」去掉，外发平台上图是竖着排的；首图留在原位带图注。平台图注有字数上限的（`GET /v1/platforms` 的 `caption`），超了推之前只给那个平台缩写（AI 缩说明、「图片来源」原样留着，不成就在标点处截）。",
		[]Block{{K: "img", Src: distributionDocImage}, {K: "cap", Text: "▲ 苦味气泡水（向左滑动查看更多）"}, {K: "cap", Text: "图片来源：品牌官网"}, specTxt("正文。")}},
	{"正文不带链接", "链接只留字，裸网址删掉。",
		[]Block{{K: "txt", Runs: []Run{{Type: "txt", Text: "详见"}, {Type: "link", Text: "品牌官网", Href: "https://example.com/news"}, {Type: "txt", Text: "，或访问https://example.com/news。"}}}}},
	{"整段加粗并成一个加粗片段", "所有字都加粗的段落给一个加粗片段，平台按加粗段写；段内局部加粗照原样分片段。",
		[]Block{{K: "txt", Runs: []Run{{Type: "txt", Text: "整段", Bold: true}, {Type: "txt", Text: "加粗", Bold: true}}}, {K: "txt", Runs: []Run{{Type: "txt", Text: "段落里"}, {Type: "txt", Text: "局部加粗", Bold: true}, {Type: "txt", Text: "的字。"}}}}},
	{"去掉作者名片", "作者名片（参考来源上方或之后的无图注图；没有参考来源时，文末无图注的宽条图）不放：平台会提示「图片含二维码」并限制推荐。",
		[]Block{specTxt("正文最后一段。"), {K: "img", Src: distributionDocNameCard}}},
}

func specTxt(text string) Block {
	return Block{K: "txt", Runs: []Run{{Type: "txt", Text: text}}}
}

func renderDistributionRulesDoc() (string, error) {
	var b strings.Builder
	b.WriteString("# 外发通用版正文块\n\n")
	b.WriteString("> 由 `api/distribution_rules_doc_test.go` 按现在的代码生成，别手改：改了规则跑 `cd api && SPEC_UPDATE=1 go test -run TestDistributionRulesDoc .`。\n\n")
	b.WriteString("排版工具把第三方平台都一样的规则（`api/distribution_rules.go`）落进正文块，经 `POST /v1/platforms/<平台>/drafts` 的 `blocks` 交给分发服务（FBIF-多平台分发工具）。" +
		"每种块在各平台写成什么标签、平台的标题与封面限制只在分发服务的 `docs/platforms/<平台>.md`；排版工具经 `GET /v1/platforms` 取限制，推送前检查用。" +
		"原文的块是什么见 [article-blocks.md](article-blocks.md)。\n\n")

	b.WriteString("## 块\n\n| 块 | 字段 | 说明 |\n| --- | --- | --- |\n")
	for _, kind := range distributionOutgoingKinds {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", kind.K, kind.Fields, kind.Meaning)
	}
	b.WriteString("\n`runs` 里每个片段只有 `type`（固定 `txt`）、`text`、可选 `bold`。没有链接、空行、图片尺寸这些字段，分发服务收到多一个字段就 400。文末总有一段转载声明（正文里已有「转载请联系授权」就不补）。封面不在块里，单独给 `cover_url`。\n\n")

	b.WriteString("## 各平台一样的规则\n\n每条配一篇最小的稿子真跑一遍。外发块一行一块，加粗写成 `**字**`。\n\n")
	mirrored := map[string]string{distributionDocImage: distributionDocImage, distributionDocNameCard: distributionDocNameCard}
	mirroredJSON, _ := json.Marshal(mirrored)
	for _, rule := range distributionDocRules {
		raw, err := json.Marshal(rule.Blocks)
		if err != nil {
			return "", err
		}
		content, err := buildDistributionContent(publishJob{BlocksJSON: string(raw), ImageMap: string(mirroredJSON), SourceName: distributionStatementPublisher}, distributionPlatform{}, publishExternalDraft{})
		if err != nil {
			return "", fmt.Errorf("%s：%w", rule.Name, err)
		}
		fmt.Fprintf(&b, "### %s\n\n%s\n\n原文：\n\n", rule.Name, rule.Rule)
		for _, block := range rule.Blocks {
			fmt.Fprintf(&b, "- %s\n", distributionDocReaderLine(block))
		}
		fmt.Fprintf(&b, "\n外发块：\n\n```\n%s\n```\n\n", distributionOutline(content.Blocks))
	}

	b.WriteString("## 标准答案\n\n编辑在各平台手发的稿是标准答案，样稿考卷分两半：排版工具 `api/testdata/distribution-golden/` 存 FoodTalks 正文和这边写出的块（`<编号>.blocks.json`，`TestDistributionBlocksGolden`），" +
		"分发服务 `test/golden/` 拿同一份块按各平台写法写出来、和编辑稿逐块比（`test/golden.test.mjs`）。\n")
	return b.String(), nil
}

// distributionDocReaderLine 一块原文在读者眼里是什么。
func distributionDocReaderLine(block Block) string {
	name := block.K
	for _, kind := range articleBlockKinds {
		if kind.K == block.K {
			name = kind.Name
		}
	}
	switch block.K {
	case "img":
		if block.Src == distributionDocNameCard {
			return "图片：作者名片（宽条图，无图注）"
		}
		return "图片"
	case "h":
		return fmt.Sprintf("%s（level %d）：%s", name, block.Level, block.Text)
	}
	var text strings.Builder
	for _, run := range block.Runs {
		if run.Bold {
			text.WriteString("**" + run.Text + "**")
		} else {
			text.WriteString(run.Text)
		}
		if run.Type == "link" {
			fmt.Fprintf(&text, "（链接 %s）", run.Href)
		}
	}
	if text.Len() == 0 {
		text.WriteString(block.Text)
	}
	return name + "：" + text.String()
}
