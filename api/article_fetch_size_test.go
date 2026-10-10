package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type articlePageRoundTripper struct {
	body  []byte
	calls atomic.Int32
}

func (rt *articlePageRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.calls.Add(1)
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       io.NopCloser(bytes.NewReader(rt.body)),
		Request:    req,
	}, nil
}

func stubArticlePage(t *testing.T, scriptBytes int) *articlePageRoundTripper {
	t.Helper()
	var page bytes.Buffer
	page.WriteString(`<html><head><script>`)
	page.WriteString(strings.Repeat("x", scriptBytes))
	page.WriteString(`</script></head><body><div class="rich_media_content" id="js_content">`)
	page.WriteString(`<p>重投入换来的不只是速度，更是选择权。</p><p>全球 5000 家店的海外路径。</p>`)
	page.WriteString(`</div></body></html>`)
	rt := &articlePageRoundTripper{body: page.Bytes()}
	prev := articleFetchHTTPClient
	articleFetchHTTPClient = &http.Client{Transport: rt}
	t.Cleanup(func() { articleFetchHTTPClient = prev })
	return rt
}

// 2026-10-10 hAjWhCtac9CLCgpeLx84-g：公众号页内联脚本约 4.8 MB，整页 5.9 MB，
// 旧 5 MB 上限三次重试全失败，却报「公众号可能临时限流」。
func TestFetchDirectBlocksAcceptsWeChatPageOver5MB(t *testing.T) {
	rt := stubArticlePage(t, 6<<20)
	_, blocks, _, _, _, _, _, _, err := fetchDirectBlocksWithOptions("https://mp.weixin.qq.com/s/hAjWhCtac9CLCgpeLx84-g", defaultArticleBlockOptions())
	if err != nil {
		t.Fatalf("fetch 6 MB page: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("blocks=0, want parsed article")
	}
	if got := rt.calls.Load(); got != 1 {
		t.Fatalf("calls=%d, want 1", got)
	}

	raw, _, err := fetchArticleHTML(context.Background(), "https://mp.weixin.qq.com/s/hAjWhCtac9CLCgpeLx84-g")
	if err != nil || !strings.Contains(raw, "js_content") {
		t.Fatalf("more-articles meta fetch of 6 MB page: err=%v", err)
	}
}

func TestFetchDirectBlocksOversizePageFailsOnceWithAccurateMessage(t *testing.T) {
	rt := stubArticlePage(t, maxArticlePageBytes+1)
	_, _, _, _, _, _, _, _, err := fetchDirectBlocksWithOptions("https://mp.weixin.qq.com/s/huge", defaultArticleBlockOptions())
	if !errors.Is(err, errArticlePageTooLarge) {
		t.Fatalf("err=%v, want errArticlePageTooLarge", err)
	}
	if got := rt.calls.Load(); got != 1 {
		t.Fatalf("calls=%d, oversize page must not be retried", got)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/fetch-article", strings.NewReader(`{"url":"https://mp.weixin.qq.com/s/huge"}`))
	handleFetchArticleWithStore(nil)(rec, req)
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "页面超过 16 MB") || strings.Contains(rec.Body.String(), "限流") {
		t.Fatalf("status=%d body=%s, want accurate oversize message", rec.Code, rec.Body.String())
	}
}
