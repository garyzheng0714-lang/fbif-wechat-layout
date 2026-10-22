package main

import (
	"bytes"
	"context"
	"encoding/json"
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

// 2026-10-22 和讯 news.hexun.com/2026-10-21/225151606.html：腾讯云 EdgeOne 反爬只回一段
// 写 cookie 再刷新的脚本，没有正文；旧提示让编辑以为是「公众号限流」，连点了 5 次。
const hexunArticleURL = "https://news.hexun.com/2026-10-21/225151606.html"

func stubEdgeOneChallenge(t *testing.T) {
	t.Helper()
	rt := &articlePageRoundTripper{body: []byte(`<script>document.cookie="__tst_status=2790889800#;";document.cookie="EO_Bot_Ssid=1915551744;";setTimeout("location.href=location.href",1200);</script>`)}
	prev := articleFetchHTTPClient
	articleFetchHTTPClient = &http.Client{Transport: rt}
	t.Cleanup(func() { articleFetchHTTPClient = prev })
}

type jinaReaderStub struct {
	calls atomic.Int32
	paths []string
}

func stubJinaReader(t *testing.T, status int, body string) *jinaReaderStub {
	t.Helper()
	stub := &jinaReaderStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.calls.Add(1)
		stub.paths = append(stub.paths, r.URL.Path)
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	prevBase, prevClient := jinaReaderBase, jinaReaderHTTPClient
	jinaReaderBase, jinaReaderHTTPClient = srv.URL+"/", srv.Client()
	t.Cleanup(func() { jinaReaderBase, jinaReaderHTTPClient = prevBase, prevClient })
	return stub
}

func postFetchArticle(t *testing.T, articleURL string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/fetch-article", strings.NewReader(`{"url":"`+articleURL+`"}`))
	handleFetchArticleWithStore(nil)(rec, req)
	return rec
}

func TestFetchArticleNonWeChatPageWithoutBodyFallsBackToJinaReader(t *testing.T) {
	stubEdgeOneChallenge(t)
	jina := stubJinaReader(t, http.StatusOK, "Title: 汇源的垮掉，始于那一场收购-新闻频道-和讯网\n\n"+
		"URL Source: "+hexunArticleURL+"\n\nPublished Time: 2026-10-21T17:51:39+08:00\n\nMarkdown Content:\n"+
		"这是一段用来模拟和讯正文的开头，长度要够，才能和登录墙、错误页这种只剩几十个字的页面区分开来。"+
		"第二句继续补足字数，说明这一页确实拿到了正文，而不是验证脚本。\n\n"+
		"**01**\n\n**第一节的小标题**\n\n"+
		"第一节正文，讲品牌早年的经历，同样是模拟文字，不取原文。\n\n"+
		"![Image 1](https://fs-cms.hexun.com/img/2026-10-21/225151607.png)\n\n"+
		"Source: 这行属于正文，不是 Jina 的头部，必须留着。\n")

	rec := postFetchArticle(t, hexunArticleURL)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want Jina markdown", rec.Code, rec.Body.String())
	}
	var got struct {
		Title   string          `json:"title"`
		Content string          `json:"content"`
		Source  string          `json:"source"`
		Blocks  json.RawMessage `json:"blocks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != "汇源的垮掉，始于那一场收购-新闻频道-和讯网" || got.Source != hexunArticleURL || got.Blocks != nil {
		t.Fatalf("title=%q source=%q blocks=%s", got.Title, got.Source, got.Blocks)
	}
	for _, want := range []string{"**01**", "**第一节的小标题**", "![Image 1](https://fs-cms.hexun.com/img/2026-10-21/225151607.png)", "Source: 这行属于正文"} {
		if !strings.Contains(got.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, got.Content)
		}
	}
	for _, header := range []string{"Title:", "URL Source:", "Published Time:", "Markdown Content:"} {
		if strings.Contains(got.Content, header) {
			t.Fatalf("content kept Jina header %q:\n%s", header, got.Content)
		}
	}
	if len(jina.paths) != 1 || jina.paths[0] != "/"+hexunArticleURL {
		t.Fatalf("jina paths=%v, want one request for the article", jina.paths)
	}
}

func TestFetchArticleNonWeChatPageWithoutBodyTellsEditorToImportViaFeishuDoc(t *testing.T) {
	stubEdgeOneChallenge(t)
	stubJinaReader(t, http.StatusOK, "Title: 安全验证\n\nURL Source: "+hexunArticleURL+"\n\nMarkdown Content:\n请完成验证后继续访问。\n")

	rec := postFetchArticle(t, hexunArticleURL)
	body := rec.Body.String()
	if rec.Code != 502 || strings.Contains(body, "公众号") || !strings.Contains(body, "飞书云文档") {
		t.Fatalf("status=%d body=%s, want non-WeChat message pointing to the Feishu doc import", rec.Code, body)
	}
}

func TestFetchArticleWeChatPageWithoutBodyNeverCallsJinaReader(t *testing.T) {
	stubEdgeOneChallenge(t)
	jina := stubJinaReader(t, http.StatusOK, "Markdown Content:\n"+strings.Repeat("正文", 200))

	rec := postFetchArticle(t, "https://mp.weixin.qq.com/s/hAjWhCtac9CLCgpeLx84-g")
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "限流") || jina.calls.Load() != 0 {
		t.Fatalf("status=%d body=%s jina calls=%d, WeChat keeps the direct path only", rec.Code, rec.Body.String(), jina.calls.Load())
	}
}
