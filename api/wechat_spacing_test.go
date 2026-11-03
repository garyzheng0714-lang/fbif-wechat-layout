package main

import (
	"strings"
	"testing"
)

// 原文段落的写法照 #338「中国茶叶的下一个10年」：普通段自带段后距，一列短句写明 margin: 0。
const spacedParagraph = `<section style="text-align: left;margin-top: 0;margin-bottom: 20px;margin-left: 8px;margin-right: 8px;line-height: 1.75;"><span leaf="">%s</span></section>`
const flushLine = `<section style="text-align: left;margin: 0px 8px;line-height: 1.75;"><span leaf="">%s</span></section>`

func sourceSection(format, text string) string {
	return strings.Replace(format, "%s", text, 1)
}

func tightTexts(blocks []Block) []string {
	var out []string
	for _, b := range blocks {
		if b.TightBefore {
			out = append(out, strings.TrimSpace(textFromRuns(b.Runs)+b.Text))
		}
	}
	return out
}

func requireTight(t *testing.T, blocks []Block, want ...string) {
	t.Helper()
	got := tightTexts(blocks)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("紧贴上一段的块 = %q，应为 %q", got, want)
	}
}

func TestWechatFlushLinesAreTightLikeTheSource(t *testing.T) {
	body := sourceSection(spacedParagraph, "那我心目中好的茶叶品牌，是什么样的？") +
		sourceSection(flushLine, "1. 品牌名：好听、好记") +
		sourceSection(flushLine, "2. 包装：设计好看、体验好") +
		sourceSection(flushLine, "3. 讲人话") +
		`<section style="margin: 0px 8px;"><span leaf=""><br></span></section>` +
		sourceSection(spacedParagraph, "简单点，就是可口可乐怎么卖可乐、农夫山泉怎么卖水。")
	_, blocks, _ := extractArticleBlocks(jsContentWrap(body))
	requireTight(t, blocks, "2. 包装：设计好看、体验好", "3. 讲人话")
}

func TestWechatTightNeedsEverySpacingBetweenToBeZero(t *testing.T) {
	cases := map[string]string{
		"p 没写段距（浏览器自带段距）": `<p>甲：一行</p><p>乙：一行</p>`,
		"外层自带段后距": `<section style="margin-bottom: 20px"><section style="margin: 0">甲：一行</section></section>` +
			`<section style="margin: 0">乙：一行</section>`,
		"这段写了段前距":    `<section style="margin: 0">甲：一行</section><section style="margin: 0; margin-top: 12px">乙：一行</section>`,
		"上一段写了内边距":   `<section style="margin: 0; padding-bottom: 8px">甲：一行</section><section style="margin: 0">乙：一行</section>`,
		"中间是空格段":     `<section style="margin: 0">甲：一行</section><section style="margin: 0">&nbsp;</section><section style="margin: 0">乙：一行</section>`,
		"中间是固定高度的占位": `<section style="margin: 0">甲：一行</section><section style="height: 16px"></section><section style="margin: 0">乙：一行</section>`,
		"中间是分隔线":     `<section style="margin: 0">甲：一行</section><hr><section style="margin: 0">乙：一行</section>`,
		"并排的两栏":      `<section style="display: flex"><section style="margin: 0">甲：一行</section><section style="margin: 0">乙：一行</section></section>`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, blocks, _ := extractArticleBlocks(jsContentWrap(body))
			if len(blocks) != 2 {
				t.Fatalf("应抽出两段，得到 %d：%+v", len(blocks), blocks)
			}
			requireTight(t, blocks)
		})
	}
}

func TestWechatTightCountsWhenMarginsAreWrittenZero(t *testing.T) {
	body := `<p style="margin: 0px; padding: 0 0">甲：一行</p>` +
		`<section style="margin-block: 0"><span style="display: block; margin-bottom: 0">乙：一行</span></section>` +
		`<section style="margin: 20px 0; margin-top: 0">丙：一行</section>`
	_, blocks, _ := extractArticleBlocks(jsContentWrap(body))
	requireTight(t, blocks, "乙：一行", "丙：一行")
}

func TestWechatTightNeverCrossesImagesOrHeadings(t *testing.T) {
	body := sourceSection(flushLine, "第一行说明文字。") +
		`<section style="margin: 0"><img src="https://mmbiz.qpic.cn/a.jpg" data-w="1080" data-ratio="0.5"></section>` +
		sourceSection(flushLine, "第二行说明文字。") +
		`<h2 style="margin: 0">一、小标题</h2>` +
		sourceSection(flushLine, "第三行说明文字。")
	_, blocks, _ := extractArticleBlocks(jsContentWrap(body))
	requireTight(t, blocks)
}

// 长段之间也都写成紧贴的，是没认出这篇靠什么分段：一处也不认，照旧每段排开。
func TestWechatTightIgnoredWhenProseIsAlsoFlush(t *testing.T) {
	long := "这是一段足够长的正文，用来模拟作者写的普通段落，长度超过三十个字才算长段。"
	body := sourceSection(flushLine, "第一段："+long) +
		sourceSection(flushLine, "第二段："+long) +
		sourceSection(flushLine, "第三段："+long) +
		sourceSection(flushLine, "1. 一行") +
		sourceSection(flushLine, "2. 一行")
	_, blocks, _ := extractArticleBlocks(jsContentWrap(body))
	requireTight(t, blocks)
}
