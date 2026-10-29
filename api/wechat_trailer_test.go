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

// #294 重新同步（2026-10-30）：重抓回来的尾巴少了联系 Bobo 那行，只剩「本文为转载文章」也要认出是公众号页脚。
func TestStripWechatTrailerBlocksCutsRepostTipWithoutContactLine(t *testing.T) {
	blocks := []Block{
		{K: "txt", Text: "正文。"},
		{K: "txt", Text: "参考来源："},
		{K: "txt", Text: "[1] 某规划纲要"},
		{K: "txt", Text: "提示："},
		{K: "txt", Text: "* 本文为转载文章。"},
		{K: "txt", Text: "/ 更多文章 /"},
		{K: "img", Src: "https://mmbiz.qpic.cn/a.png"},
	}
	if got := stripWechatTrailerBlocks(blocks); len(got) != 3 {
		t.Fatalf("应切在「提示：」之前，实际 %d 块", len(got))
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

// #128 Vista看天下：文末「点击下方按钮立刻关注」与其后的账号口号是来源号尾巴；
// 开头的「点击上方蓝字关注」不靠近文末，不能当切点。
func TestStripWechatTrailerBlocksCutsSourceFollowPrompt(t *testing.T) {
	blocks := []Block{{K: "txt", Text: "点击上方蓝字关注我们"}}
	for i := 0; i < 12; i++ {
		blocks = append(blocks, Block{K: "txt", Text: "正文段落。"})
	}
	blocks = append(blocks,
		Block{K: "txt", Text: "谁就能在无糖红海之外，切出一块新大陆。"},
		Block{K: "txt", Text: "本文经授权转载自Vista看天下，点击下方按钮立刻关注。"},
		Block{K: "txt", Text: "不苛求正确，只好好说话。"},
	)
	got := stripWechatTrailerBlocks(blocks)
	if len(got) != len(blocks)-2 {
		t.Fatalf("应切在关注引导处，保留 %d 块，实际 %d 块", len(blocks)-2, len(got))
	}
	if wechatTrailerBlockText(got[0]) != "点击上方蓝字关注我们" || wechatTrailerBlockText(got[len(got)-1]) != "谁就能在无糖红海之外，切出一块新大陆。" {
		t.Fatalf("开头与正文结尾应保留：%+v", got)
	}
}

// 灵兽：文末「喜欢文章点击"在看、点赞、分享"」是来源号尾巴；正文里提到「点赞」不切。
func TestStripWechatTrailerBlocksCutsLikePrompt(t *testing.T) {
	var blocks []Block
	for i := 0; i < 12; i++ {
		blocks = append(blocks, Block{K: "txt", Text: "正文段落，读者纷纷点赞。"})
	}
	blocks = append(blocks,
		Block{K: "txt", Text: "这些都在提醒后面想入局的后来者，得谨慎一点。"},
		Block{K: "txt", Runs: []Run{{Type: "txt", Text: `喜欢文章点击"在看、点赞、分享"`, Bold: true}}},
	)
	got := stripWechatTrailerBlocks(blocks)
	if len(got) != len(blocks)-1 || wechatTrailerBlockText(got[len(got)-1]) != "这些都在提醒后面想入局的后来者，得谨慎一点。" {
		t.Fatalf("应只切掉文末的在看引导：%+v", got)
	}
}
