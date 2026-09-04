package main

import (
	"context"
	"strings"
	"testing"
)

func TestNewArticleFetchRequestNormalizesWeChatChallengeURL(t *testing.T) {
	req, err := newArticleFetchRequest(context.Background(), "https://mp.weixin.qq.com/s/article?poc_token=expired&scene=1")
	if err != nil {
		t.Fatal(err)
	}
	if got := req.URL.Query().Get("poc_token"); got != "" {
		t.Fatalf("poc_token = %q, want removed", got)
	}
	if got := req.URL.Query().Get("scene"); got != "1" {
		t.Fatalf("scene = %q, want preserved", got)
	}
	if ua := req.Header.Get("User-Agent"); !strings.Contains(ua, "MicroMessenger/") {
		t.Fatalf("User-Agent = %q, want WeChat mobile UA", ua)
	}
}

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

func TestExtractArticleImageURLsUsesJsContentAndFiltersPlaceholders(t *testing.T) {
	raw := `<html><body>
		<img src="https://mmbiz.qlogo.cn/avatar.png">
		<div id="js_content" class="rich_media_content">
			<p><img data-src="//mmbiz.qpic.cn/first.jpg?wx_fmt=jpeg&amp;from=appmsg"></p>
			<p><img src="https://res.wx.qq.com/mmbizappmsg/zh_CN/images/pic/pic_blank.gif"></p>
			<p><img data-original='https:\/\/mmbiz.qpic.cn\/second.png?wx_fmt=png&amp;x=1'></p>
			<p><img data-src="//mmbiz.qpic.cn/first.jpg?wx_fmt=jpeg&amp;from=appmsg"></p>
		</div><div class="rich_media_tool"></div>
	</body></html>`
	got := extractArticleImageURLs(raw)
	want := []string{
		"https://mmbiz.qpic.cn/first.jpg?wx_fmt=jpeg&from=appmsg",
		"https://mmbiz.qpic.cn/second.png?wx_fmt=png&x=1",
	}
	if len(got) != len(want) {
		t.Fatalf("extractArticleImageURLs() len = %d (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("extractArticleImageURLs()[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
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
