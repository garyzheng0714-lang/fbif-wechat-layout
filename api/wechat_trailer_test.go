package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStripWechatTrailerBlocksCutsFBIFTipFooter(t *testing.T) {
	blocks := []Block{
		{K: "txt", Runs: []Run{{Type: "txt", Text: "把家乡一度被低估的食物，卖到英国天贝品类第一。"}}},
		{K: "txt", Runs: []Run{{Type: "txt", Text: "参考来源："}}},
		{K: "txt", Runs: []Run{{Type: "txt", Text: "[1]William Dodds，Better Nature raises £1.1M"}}},
		{K: "txt", Runs: []Run{{Type: "txt", Text: "提示："}}},
		{K: "txt", Runs: []Run{{Type: "txt", Text: "* 本文为FBIF原创，欢迎转发朋友圈；"}}},
		{K: "txt", Runs: []Run{{Type: "txt", Text: "* 转载请在后台回复“转载”了解规则；"}}},
		{K: "txt", Runs: []Run{{Type: "txt", Text: "* FBIF投稿与商务合作，联系Bobo（微信：fbifmedia1）"}}},
		{K: "cap", Text: "/ 更多文章 /"},
		{K: "img", Src: "https://mmbiz.qpic.cn/author-card.png"},
		{K: "txt", Runs: []Run{{Type: "txt", Text: "预览时标签不可点"}}},
	}
	got := stripWechatTrailerBlocks(blocks)
	if len(got) != 3 {
		t.Fatalf("应切在「提示：」之前，实际 %d 块", len(got))
	}
	joined, _ := json.Marshal(got)
	if leak := publishHTMLWechatTrailerLeak(string(joined)); leak != "" {
		t.Fatalf("切尾后仍含导流标记 %s：%s", leak, joined)
	}
	if !strings.Contains(string(joined), "参考来源") || !strings.Contains(string(joined), "天贝品类第一") {
		t.Fatalf("正文与参考来源应保留：%s", joined)
	}
}

func TestStripWechatTrailerBlocksSkipsMarkerBeforeReferences(t *testing.T) {
	blocks := []Block{
		{K: "p", Text: "正文联系方式。"},
		{K: "cap", Text: "（扫码关注上海艾录）"},
		{K: "p", Text: "参考来源："},
		{K: "p", Text: "[1] 艾录年报"},
		{K: "p", Text: "提示："},
		{K: "p", Text: "* 本文为FBIF原创，欢迎转发朋友圈；"},
	}
	got := stripWechatTrailerBlocks(blocks)
	joined, _ := json.Marshal(got)
	if !strings.Contains(string(joined), "扫码关注上海艾录") {
		t.Fatalf("参考来源前的扫码图注不得当切点：%s", joined)
	}
	if strings.Contains(string(joined), "欢迎转发朋友圈") {
		t.Fatalf("参考来源之后的公众号提示仍应切除：%s", joined)
	}
}

func TestStripWechatTrailerBlocksCutsHeadingOrCaptionTip(t *testing.T) {
	for _, kind := range []string{"h", "cap"} {
		t.Run(kind, func(t *testing.T) {
			blocks := []Block{
				{K: "txt", Text: "正文保留。"},
				{K: "txt", Text: "参考来源："},
				{K: "txt", Text: "[1] 乐天家庭购物"},
				{K: kind, Text: "提示："},
				{K: "txt", Text: "* 本文为FBIF原创，欢迎转发朋友圈；"},
				{K: "cap", Text: "/ 更多文章 /"},
			}
			got := stripWechatTrailerBlocks(blocks)
			if len(got) != 3 {
				t.Fatalf("%s 提示必须从尾注前切除，保留正文与参考来源，得到 %#v", kind, got)
			}
		})
	}
}

func TestStripWechatTrailerBlocksJSONRejectsEmptyAfterCut(t *testing.T) {
	raw := `[{"k":"txt","text":"提示："},{"k":"txt","text":"* 本文为FBIF原创，欢迎转发朋友圈；"}]`
	if _, err := stripWechatTrailerBlocksJSON(raw); err == nil {
		t.Fatal("切光后必须失败，不能交出空正文")
	}
}

func TestStripWechatTrailerBlocksJSONPreservesUnknownFieldsWhenUncut(t *testing.T) {
	raw := `[{"k":"list","ordered":true,"items":["第一项"]}]`
	got, err := stripWechatTrailerBlocksJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != raw {
		t.Fatalf("无导流尾时应原样返回 JSON，避免丢掉 list items：%s", got)
	}
	if _, err := stripWechatTrailerBlocksJSON("{"); err == nil {
		t.Fatal("无效 JSON 必须失败")
	}
}

func TestStripWechatTrailerBlocksCutsMoreArticlesAndExternalTail(t *testing.T) {
	more := []Block{
		{K: "p", Text: "正文保留。"},
		{K: "cap", Text: "/ 更多文章 /"},
		{K: "img", Src: "https://mmbiz.qpic.cn/card.png"},
	}
	got := stripWechatTrailerBlocks(more)
	if len(got) != 1 || wechatTrailerBlockText(got[0]) != "正文保留。" {
		t.Fatalf("应从更多文章切开：%#v", got)
	}

	external := make([]Block, 0, 12)
	for i := 0; i < 8; i++ {
		external = append(external, Block{K: "p", Text: "正文继续 " + string(rune('A'+i))})
	}
	external = append(external,
		Block{K: "h", Level: 2, Text: "延伸阅读"},
		Block{K: "p", Text: "红餐网已同步入驻"},
		Block{K: "p", Text: "来源：红餐网"},
	)
	got = stripWechatTrailerBlocks(external)
	joined, _ := json.Marshal(got)
	if strings.Contains(string(joined), "延伸阅读") || strings.Contains(string(joined), "红餐网已同步入驻") {
		t.Fatalf("外部媒体尾巴应切除：%s", joined)
	}
	if !strings.Contains(string(joined), "正文继续") {
		t.Fatalf("正文应保留：%s", joined)
	}
}
