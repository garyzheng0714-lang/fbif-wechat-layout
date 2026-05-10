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

func TestExtractWeChatCaptchaTargetURLHandlesHexEscapes(t *testing.T) {
	raw := `<script>window.cgiData = {
		target_url : "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==\x26amp;mid=2652903792\x26amp;idx=1\x26amp;sn=01016d83a8b08bcecfba26c57cb89198\x26amp;scene=21",
	}</script>`
	got := extractWeChatCaptchaTargetURL(raw)
	want := "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=2652903792&idx=1&sn=01016d83a8b08bcecfba26c57cb89198&scene=21"
	if got != want {
		t.Fatalf("extractWeChatCaptchaTargetURL() = %q, want %q", got, want)
	}
}

func TestWeChatMetaFallbackURLStripsPOCTokenAndAddsChksm(t *testing.T) {
	raw := `<script>window.cgiData = {
		target_url : "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==\x26amp;mid=2652903792\x26amp;idx=1\x26amp;sn=01016d83a8b08bcecfba26c57cb89198\x26amp;scene=21\x26amp;poc_token=expired",
	}</script>`
	got := weChatMetaFallbackURL(
		"https://mp.weixin.qq.com/mp/wappoc_appmsgcaptcha?poc_token=expired",
		"https://mp.weixin.qq.com/mp/wappoc_appmsgcaptcha?poc_token=expired",
		raw,
	)
	want := "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg%3D%3D&chksm=00000000000000000000000000000000&idx=1&mid=2652903792&scene=21&sn=01016d83a8b08bcecfba26c57cb89198"
	if got != want {
		t.Fatalf("weChatMetaFallbackURL() = %q, want %q", got, want)
	}
}
