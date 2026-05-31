package main

import (
	"strings"
	"testing"
)

func TestCleanXReaderMarkdownStripsManualMetadata(t *testing.T) {
	raw := strings.Join([]string{
		"[✅][manual] Title:  以选品筑壁垒：价值是选品的起点，Olé如何定义“值得”–FoodTalks全球食品资讯",
		"",
		"https://www.foodtalks.cn/news/62243",
		"",
		"URL Source: https://www.foodtalks.cn/news/62243",
		"",
		"Published Time: Thu, 28 May 2026 01:47:36 GMT",
		"",
		"Markdown Content:",
		"",
		"很多人说，逛Olé就像是在进行一场关于生活美好的“策展旅行”。",
	}, "\n")

	content, title := cleanXReaderMarkdown(raw)

	wantTitle := "以选品筑壁垒：价值是选品的起点，Olé如何定义“值得”"
	if title != wantTitle {
		t.Fatalf("title = %q, want %q", title, wantTitle)
	}
	for _, bad := range []string{"[manual] Title:", "URL Source:", "Published Time:", "Markdown Content:", "https://www.foodtalks.cn/news/62243"} {
		if strings.Contains(content, bad) {
			t.Fatalf("content still contains metadata %q: %q", bad, content)
		}
	}
	if strings.Contains(content, `\_`) || strings.Contains(content, "__本文") {
		t.Fatalf("content still contains escaped emphasis markers: %q", content)
	}
	if !strings.Contains(content, "很多人说") {
		t.Fatalf("content lost article body: %q", content)
	}
}
