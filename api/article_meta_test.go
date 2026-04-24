package main

import "testing"

func TestExtractMetaHandlesNameAndSingleQuotes(t *testing.T) {
	raw := `<html><head><meta name='twitter:image' content='https://mmbiz.qpic.cn/cover.jpg?wx_fmt=jpeg&amp;tp=webp'></head></html>`
	got := extractMeta(raw, "twitter:image")
	want := "https://mmbiz.qpic.cn/cover.jpg?wx_fmt=jpeg&tp=webp"
	if got != want {
		t.Fatalf("extractMeta() = %q, want %q", got, want)
	}
}

func TestExtractArticleCoverHandlesEscapedMsgCdnURL(t *testing.T) {
	raw := `<script>var msg_cdn_url = 'https:\/\/mmbiz.qpic.cn\/sz_mmbiz_jpg\/cover.jpg?wx_fmt=jpeg';</script>`
	got := extractArticleCover(raw)
	want := "https://mmbiz.qpic.cn/sz_mmbiz_jpg/cover.jpg?wx_fmt=jpeg"
	if got != want {
		t.Fatalf("extractArticleCover() = %q, want %q", got, want)
	}
}

func TestExtractArticleCoverFallsBackToSingleQuotedDataSrc(t *testing.T) {
	raw := `<article><img data-src='//mmbiz.qpic.cn/first.jpg?x=1&amp;y=2'></article>`
	got := extractArticleCover(raw)
	want := "https://mmbiz.qpic.cn/first.jpg?x=1&y=2"
	if got != want {
		t.Fatalf("extractArticleCover() = %q, want %q", got, want)
	}
}
