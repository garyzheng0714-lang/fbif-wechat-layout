package main

import (
	"fmt"
	"strings"
	"testing"
)

// 公众号原文的滑动图（2026-11-18 Dudu，So Acai 原文 9 组）：同一个横滑外框里的图记同一个组号，
// 图注照旧跟在最后一张后面；只有一张图、带字的、上下滑的、能换行的框都不算。
func TestWechatSwipeBoxesMarkTheirImages(t *testing.T) {
	img := func(name string) string {
		return `<section style="display: inline-block;width: 33.3333%;vertical-align: top;"><section style="text-align: center;line-height: 0;"><section style="max-width: 100%;display: inline-block;width: 95%;" nodeleaf=""><img data-src="https://mmbiz.qpic.cn/` + name + `/640" data-ratio="1.33" data-w="1080" style="width: 100%;"></section></section></section>`
	}
	box135 := func(names ...string) string {
		var b strings.Builder
		fmt.Fprintf(&b, `<section style="display: inline-block;width: 100%%;vertical-align: top;overflow-x: auto;box-sizing: border-box;"><section style="overflow: hidden;width: %d00%%;max-width: %d00%% !important;">`, len(names), len(names))
		for _, n := range names {
			b.WriteString(img(n))
		}
		b.WriteString(`</section></section>`)
		return b.String()
	}
	page := `<html><body><div class="rich_media_content" id="js_content">
<p>正文第一段，说一说品牌的视觉。</p>` +
		box135("a1", "a2", "a3") + `
<p style="text-align: center;"><span style="font-size: 12px;color: rgb(136, 136, 136);">So Acai品牌视觉</span></p>
<p style="text-align: center;"><span style="font-size: 12px;color: rgb(136, 136, 136);">←向左滑动查看更多</span></p>
<p>正文第二段。</p>` +
		box135("single") +
		box135("b1", "b2") + `
<p style="text-align: center;"><span style="font-size: 12px;color: rgb(136, 136, 136);">产品包装</span></p>
<p>正文第三段。</p>
<section style="overflow-x: scroll;"><section style="display: flex;"><img data-src="https://mmbiz.qpic.cn/c1/640" data-w="1080"><img data-src="https://mmbiz.qpic.cn/c2/640" data-w="1080"></section></section>
<p>正文第四段。</p>
<section style="overflow-x: auto;"><section style="width: 200%;"><section style="display: inline-block;width: 50%;"><img data-src="https://mmbiz.qpic.cn/card1/640" data-w="1080"><p>卡片一的说明</p></section><section style="display: inline-block;width: 50%;"><img data-src="https://mmbiz.qpic.cn/card2/640" data-w="1080"><p>卡片二的说明</p></section></section></section>
<section style="overflow: auto;height: 400px;"><section style="width: 300%;"><img data-src="https://mmbiz.qpic.cn/tall1/640" data-w="1080"><img data-src="https://mmbiz.qpic.cn/tall2/640" data-w="1080"></section></section>
<section style="overflow-x: auto;"><p><img data-src="https://mmbiz.qpic.cn/stack1/640" data-w="1080"></p><p><img data-src="https://mmbiz.qpic.cn/stack2/640" data-w="1080"></p></section>
<p><img data-src="https://mmbiz.qpic.cn/plain/640" data-w="1080"></p>
</div></body></html>`
	_, blocks, _ := extractArticleBlocks(page)
	want := map[string]int{
		"a1": 1, "a2": 1, "a3": 1,
		"single": 0,
		"b1":     2, "b2": 2,
		"c1": 3, "c2": 3,
		"card1": 0, "card2": 0,
		"tall1": 0, "tall2": 0,
		"stack1": 0, "stack2": 0,
		"plain": 0,
	}
	seen := map[string]bool{}
	for _, b := range blocks {
		if b.K != "img" {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(b.Src, "https://mmbiz.qpic.cn/"), "/640")
		seen[name] = true
		if got, ok := want[name]; !ok || got != b.Swipe {
			t.Errorf("图 %s 的组号是 %d，应是 %d", name, b.Swipe, want[name])
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("图 %s 丢了：%v", name, findKinds(blocks))
		}
	}
	for i, b := range blocks {
		if b.K == "img" && strings.HasSuffix(b.Src, "/a3/640") {
			if i+2 >= len(blocks) || blocks[i+1].K != "cap" || blocks[i+2].K != "cap" || blocks[i+2].Text != "←向左滑动查看更多" {
				t.Fatalf("一组滑动图后面的两行图注该跟在最后一张后面：%v", findKinds(blocks))
			}
		}
	}
}
