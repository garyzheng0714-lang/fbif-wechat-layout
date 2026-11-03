package main

import (
	"os"
	"strings"
	"testing"
)

// 世界茶叶简史（FoodTalks news/63305）的公众号原文切片。
// 温馨提示是 14px 灰斜体整段，斗茶旁注是句内灰斜体，十大名茶是 margin-bottom:0 的短行。
func TestTeaHistoryKeepsInlineEmphasisAndTightLines(t *testing.T) {
	raw, err := os.ReadFile("testdata/wechat/tea-history-63305.html")
	if err != nil {
		t.Fatal(err)
	}
	_, blocks, _ := extractArticleBlocks(string(raw))

	note := findBlockByText(blocks, "温馨提示")
	if note == nil || note.K != "txt" {
		t.Fatalf("温馨提示应是正文段，got %#v", note)
	}
	noteRun := runContaining(t, note, "温馨提示")
	if noteRun.Text != "温馨提示：这篇简史不简，大约3.2万字。" || !noteRun.Italic || noteRun.Color != "8f959e" || noteRun.FontSize != 14 {
		t.Fatalf("温馨提示应保留 14px 灰斜体，got %+v", noteRun)
	}

	quote := findBlockByText(blocks, "杜长风")
	if quote == nil || quote.K != "txt" {
		t.Fatalf("斗茶句应是正文段，got %#v", quote)
	}
	bold := runContaining(t, quote, "谁白谁赢")
	if !bold.Bold || strings.Contains(bold.Text, "杜长风") {
		t.Fatalf("加粗引文应单独成片，got %+v", bold)
	}
	aside := runContaining(t, quote, "杜长风")
	if aside.Text != "（杜长风教的小学生孙理说的，儿童演员陈尚均饰）" || !aside.Italic || aside.Color != "8f959e" || aside.FontSize != 0 || aside.Bold {
		t.Fatalf("句内旁注应只保留灰斜体，字号跟正文，got %+v", aside)
	}
	closer := runContaining(t, quote, "”")
	if closer.Italic || closer.Bold || closer.Color != "" || strings.Contains(closer.Text, "杜长风") {
		t.Fatalf("引号应是普通正文，不跟旁注粘在一起，got %+v", closer)
	}

	body := runContaining(t, quote, "梦华录")
	if body.Italic || body.Color != "" || body.FontSize != 0 || body.Bold {
		t.Fatalf("正文颜色和字号不应写进片段，got %+v", body)
	}

	var italics int
	for _, b := range blocks {
		for _, r := range b.Runs {
			if r.Italic {
				italics++
			}
		}
	}
	if italics != 2 {
		t.Fatalf("这篇切片里只有两处斜体，got %d", italics)
	}

	teas := []string{
		"西湖龙井（浙江杭州，绿茶）",
		"洞庭碧螺春（江苏苏州，绿茶）",
		"信阳毛尖（河南信阳，绿茶）",
		"都匀毛尖（贵州都匀，绿茶）",
		"黄山毛峰（安徽黄山，绿茶）",
		"六安瓜片（安徽六安，绿茶）",
		"君山银针（湖南岳阳，黄茶）",
		"安溪铁观音（福建安溪，乌龙茶）",
		"武夷岩茶（福建武夷山，乌龙茶）",
		"祁门红茶（安徽祁门，红茶）",
	}
	for i, name := range teas {
		b := findBlockByText(blocks, name)
		if b == nil || b.K != "txt" {
			t.Fatalf("%s 应是正文段，got %#v", name, b)
		}
		if i == 0 && b.TightBefore {
			t.Fatalf("%s 上面是普通段距，不应排紧", name)
		}
		if i > 0 && !b.TightBefore {
			t.Fatalf("%s 应紧贴上一行", name)
		}
	}
	follow := findBlockByText(blocks, "这十个名字")
	if follow == nil || !follow.TightBefore {
		t.Fatalf("名单最后一行的下一段原文没有段距，got %#v", follow)
	}
}

func TestSoftBreakStaysInsideTheParagraph(t *testing.T) {
	html := jsContentWrap(`<p><span style="font-size:15px;color:#544545">甲</span><br><span style="font-size:15px;color:#544545">乙</span></p>`)
	_, blocks, _ := extractArticleBlocks(html)
	if len(blocks) != 1 || blocks[0].K != "txt" {
		t.Fatalf("段内换行不应拆成两段，got %#v", blocks)
	}
	got := make([]string, 0, len(blocks[0].Runs))
	for _, r := range blocks[0].Runs {
		got = append(got, r.Text)
	}
	if strings.Join(got, "|") != "甲|\n|乙" {
		t.Fatalf("段内换行应单独成一片，got %q", got)
	}
}

func runContaining(t *testing.T, b *Block, needle string) Run {
	t.Helper()
	for _, r := range b.Runs {
		if strings.Contains(r.Text, needle) {
			return r
		}
	}
	t.Fatalf("找不到含 %q 的片段：%+v", needle, b.Runs)
	return Run{}
}
