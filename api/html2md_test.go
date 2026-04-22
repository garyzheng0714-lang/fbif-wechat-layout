package main

import (
	"os"
	"strings"
	"testing"
)

func TestExtractArticleBoldStyleSpan(t *testing.T) {
	b, err := os.ReadFile("/tmp/wx_bold.html")
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}
	md, title := extractArticle(string(b))
	if title == "" {
		t.Error("expected non-empty title")
	}
	if len(md) < 1000 {
		t.Errorf("expected substantial markdown, got %d chars", len(md))
	}
	// Key phrase is wrapped in <span style="font-weight: bold"> in the source.
	needle := "帝斯曼-芬美意风味、质构与健康旗下食品配料和食用风味两个业务板块"
	if !strings.Contains(md, needle) {
		t.Fatalf("expected key phrase missing: %q", needle)
	}
	boldWrapped := "**" + needle
	if !strings.Contains(md, boldWrapped) {
		idx := strings.Index(md, needle)
		ctx := md[max0(idx-40):minN(idx+60, len(md))]
		t.Errorf("key phrase not bold-wrapped.\nContext: %q", ctx)
	}
}

func TestIsStyleBold(t *testing.T) {
	cases := []struct {
		style string
		want  bool
	}{
		{"font-weight: bold", true},
		{"font-weight:bold;", true},
		{"font-weight: 700;color: red", true},
		{"color: red;font-weight:600", true},
		{"font-weight: bolder", true},
		{"font-weight: normal", false},
		{"font-weight: 400", false},
		{"font-weight: 500", false},
		{"color: red", false},
		{"", false},
	}
	for _, c := range cases {
		got := isStyleBold(c.style)
		if got != c.want {
			t.Errorf("isStyleBold(%q) = %v, want %v", c.style, got, c.want)
		}
	}
}

func TestContainsBlockDescendantGuardsImg(t *testing.T) {
	// Bold span containing <img> must not get ** wrapping — would break
	// the frontend's bare-image-paragraph detector.
	frag := `<div><span style="font-weight:bold;"><img src="https://mmbiz.qpic.cn/foo.jpg"/></span></div>`
	md := htmlToMarkdown(frag)
	if strings.Contains(md, "**") {
		t.Errorf("expected no ** wrapping around bold img, got: %q", md)
	}
	if !strings.Contains(md, "![](https://mmbiz.qpic.cn/foo.jpg)") {
		t.Errorf("expected image markdown preserved, got: %q", md)
	}
}

func TestBoldSpanWithLinkPreserved(t *testing.T) {
	// Walker TrimSpaces each text node, so prefix/suffix whitespace is
	// expected to collapse. What matters: ** pair spans the whole bold
	// span including the link, so the frontend renders bold+link+bold.
	frag := `<div><span style="font-weight:bold;">prefix<a href="http://x/y">click</a>suffix</span></div>`
	md := htmlToMarkdown(frag)
	if !strings.Contains(md, "**prefix[click](http://x/y)suffix**") {
		t.Errorf("expected bold-wrapped link paragraph, got: %q", md)
	}
}

func max0(a int) int {
	if a < 0 {
		return 0
	}
	return a
}
func minN(a, b int) int {
	if a < b {
		return a
	}
	return b
}
