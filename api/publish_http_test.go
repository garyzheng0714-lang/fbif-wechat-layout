package main

// publish_http_test.go — 发布流水线 HTTP 端点、JSON 存储并发安全、以及
// rendering / mirroring 默认实现对既有内核的进程内复用（httptest 桩，施工图 S6）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testAdminPassword = "test-admin-pw"

func newPublishTestMux(t *testing.T, env *publishTestEnv) *http.ServeMux {
	t.Helper()
	t.Setenv("ADMIN_PASSWORD", testAdminPassword)
	mux := http.NewServeMux()
	registerPublishRoutes(mux, env.pipeline)
	return mux
}

func doPublishReq(mux *http.ServeMux, method, target, body string, withAuth bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	if withAuth {
		req.Header.Set("X-Admin-Password", testAdminPassword)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestPublishEndpointsRequireAdminPassword 发布管理面全部端点都在 ADMIN_PASSWORD 门后：
// 提交/查看/审核/重试任何一个裸奔，翻公开闸门就等于没锁。
func TestPublishEndpointsRequireAdminPassword(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	cases := []struct{ method, target, body string }{
		{http.MethodPost, "/api/publish/site-sync", `{"url":"https://mp.weixin.qq.com/s/x"}`},
		{http.MethodPost, "/api/publish/official-sync", `{"url":"https://mp.weixin.qq.com/s/x"}`},
		{http.MethodGet, "/api/publish/readiness", ""},
		{http.MethodGet, "/api/publish/jobs", ""},
		{http.MethodGet, "/api/publish/jobs/1", ""},
		{http.MethodPost, "/api/publish/jobs/1/review", `{"action":"approve"}`},
		{http.MethodPost, "/api/publish/jobs/1/retry", ""},
		{http.MethodPost, "/api/publish/jobs/1/reconcile", `{"foodtalks_news_id":7240}`},
	}
	for _, c := range cases {
		if rec := doPublishReq(mux, c.method, c.target, c.body, false); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s 无密码应 401，实际 %d", c.method, c.target, rec.Code)
		}
	}
	// 红线连带断言：以上未过门的请求不得触发任何 FoodTalks 调用。
	if createN, updateN := env.ft.counts(); createN != 0 || updateN != 0 {
		t.Fatalf("未过鉴权门的请求触发了 FoodTalks 调用（create=%d update=%d）", createN, updateN)
	}
}

// TestPublishOfficialSyncUsesOfficialContent 官方 API 正文直入必须跳过网页 fetch，
// 同时复用原有排版、图片转存、草稿进站与永久幂等链路。
func TestPublishOfficialSyncUsesOfficialContent(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	body, err := json.Marshal(map[string]string{
		"url":          testPublishSourceURL,
		"title":        "官方接口标题",
		"source_name":  "FBIF食品饮料创新",
		"author":       "官方作者",
		"content_html": `<p>官方 API 正文</p><img src="https://mmbiz.qpic.cn/a.jpg"><img src="https://mmbiz.qpic.cn/b.jpg">`,
		"cover_url":    "https://mmbiz.qpic.cn/a.jpg",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	first := doPublishReq(mux, http.MethodPost, "/api/publish/official-sync", string(body), true)
	if first.Code != http.StatusCreated {
		t.Fatalf("首次官方正文提交应 201，实际 %d body=%s", first.Code, first.Body.String())
	}
	var response struct {
		Job      publishJob `json:"job"`
		Existing bool       `json:"existing"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.fetchCalls != 0 {
		t.Fatalf("官方正文直入不得抓公众号网页，实际 fetch=%d", env.fetchCalls)
	}
	job, err := env.store.byID(response.Job.ID)
	if err != nil {
		t.Fatalf("byID: %v", err)
	}
	if job.Stage != publishStageAwaitingReview || job.Title != "官方接口标题" || job.SourceName != "FBIF食品饮料创新" || job.Author != "官方作者" {
		t.Fatalf("官方正文任务字段/阶段不符: stage=%q title=%q source=%q author=%q", job.Stage, job.Title, job.SourceName, job.Author)
	}
	if strings.TrimSpace(job.BlocksJSON) == "" {
		t.Fatalf("官方 HTML 应先持久化为 blocks")
	}
	if createN, updateN := env.ft.counts(); createN != 1 || updateN != 0 {
		t.Fatalf("自动流程只能创建一份待审草稿，实际 create=%d update=%d", createN, updateN)
	}

	second := doPublishReq(mux, http.MethodPost, "/api/publish/official-sync", string(body), true)
	if second.Code != http.StatusOK {
		t.Fatalf("重复官方正文提交应 200，实际 %d body=%s", second.Code, second.Body.String())
	}
	if createN, _ := env.ft.counts(); createN != 1 {
		t.Fatalf("同文重复提交不得重复建稿，实际 create=%d", createN)
	}
}

func TestPublishOfficialSyncAcceptsOfficialPlainText(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	body, err := json.Marshal(map[string]string{
		"url":          "https://mp.weixin.qq.com/s/official-plain-text",
		"title":        "官方纯文本快讯",
		"source_name":  "FBIF食品饮料创新",
		"content_html": "第一段官方正文。\n\n第二段官方正文。\n\n参考来源：官方公告",
		"cover_url":    "https://mmbiz.qpic.cn/a.jpg",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := doPublishReq(mux, http.MethodPost, "/api/publish/official-sync", string(body), true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("官方纯文本提交应 201，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Job publishJob `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	job, err := env.store.byID(response.Job.ID)
	if err != nil {
		t.Fatalf("byID: %v", err)
	}
	if env.fetchCalls != 0 || job.Stage != publishStageAwaitingReview {
		t.Fatalf("纯文本官方正文应跳过网页抓取并进入待审：fetch=%d stage=%q", env.fetchCalls, job.Stage)
	}
	var blocks []Block
	if err := json.Unmarshal([]byte(job.BlocksJSON), &blocks); err != nil {
		t.Fatalf("decode blocks: %v", err)
	}
	if len(blocks) != 3 || blocks[0].Text != "第一段官方正文。" || !blocks[1].BlankBefore {
		t.Fatalf("纯文本段落转换不符：%+v", blocks)
	}
}

func TestPublishOfficialSyncAcceptsPlainTextWithInlineLinks(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	body, err := json.Marshal(map[string]string{
		"url":          "https://mp.weixin.qq.com/s/official-plain-text-links",
		"title":        "官方纯文本带链接快讯",
		"source_name":  "FBIF食品饮料创新",
		"content_html": "第一段官方正文。\n\n更多阅读：\n<a href=\"https://mp.weixin.qq.com/s/related\">相关历史文章</a>",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := doPublishReq(mux, http.MethodPost, "/api/publish/official-sync", string(body), true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("带行内链接的官方纯文本提交应 201，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Job publishJob `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	job, err := env.store.byID(response.Job.ID)
	if err != nil {
		t.Fatalf("byID: %v", err)
	}
	var blocks []Block
	if err := json.Unmarshal([]byte(job.BlocksJSON), &blocks); err != nil {
		t.Fatalf("decode blocks: %v", err)
	}
	if len(blocks) != 3 || blocks[2].Runs[0].Type != "link" || blocks[2].Runs[0].Text != "相关历史文章" {
		t.Fatalf("带行内链接的纯文本段落转换不符：%+v", blocks)
	}
}

// TestPublishSiteSyncIdempotentHTTP 同一篇文章的不同链接形态（chksm 等易变参数）
// 幂等收敛到同一个任务：第二次提交返回既有单，不重复建单、不重复推进。
func TestPublishSiteSyncIdempotentHTTP(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)

	first := doPublishReq(mux, http.MethodPost, "/api/publish/site-sync",
		`{"url":"https://mp.weixin.qq.com/s?__biz=MzA1&mid=2&idx=1&sn=abc&chksm=aaa"}`, true)
	if first.Code != http.StatusCreated {
		t.Fatalf("首次提交应 201，实际 %d body=%s", first.Code, first.Body.String())
	}
	var resp1 struct {
		Job      publishJob `json:"job"`
		Existing bool       `json:"existing"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp1.Existing {
		t.Fatalf("首次提交 existing 应为 false")
	}

	second := doPublishReq(mux, http.MethodPost, "/api/publish/site-sync",
		`{"url":"https://mp.weixin.qq.com/s?sn=abc&idx=1&mid=2&__biz=MzA1&chksm=bbb"}`, true)
	if second.Code != http.StatusOK {
		t.Fatalf("重复提交应 200，实际 %d", second.Code)
	}
	var resp2 struct {
		Job      publishJob `json:"job"`
		Existing bool       `json:"existing"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp2.Existing || resp2.Job.ID != resp1.Job.ID {
		t.Fatalf("同 source_key 应返回既有任务 #%d，实际 #%d existing=%v", resp1.Job.ID, resp2.Job.ID, resp2.Existing)
	}
	// 推进只发生一轮：CreateNews 恰一次。
	if createN, _ := env.ft.counts(); createN != 1 {
		t.Fatalf("CreateNews 应恰一次，实际 %d", createN)
	}
	// 非公众号链接显式拒绝。
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/site-sync", `{"url":"https://example.com/a"}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("非公众号链接应 400，实际 %d", rec.Code)
	}
}

// TestPublishReviewAndDetailHTTP 审核端点走通 approve 全链路，详情端点回读全量字段。
func TestPublishReviewAndDetailHTTP(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	rec := doPublishReq(mux, http.MethodPost, "/api/publish/site-sync",
		fmt.Sprintf(`{"url":%q}`, testPublishSourceURL), true)
	var created struct {
		Job publishJob `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()

	target := fmt.Sprintf("/api/publish/jobs/%d/review", created.Job.ID)
	if rec := doPublishReq(mux, http.MethodPost, target, `{"action":"approve"}`, true); rec.Code != http.StatusOK {
		t.Fatalf("approve 应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	detail := doPublishReq(mux, http.MethodGet, fmt.Sprintf("/api/publish/jobs/%d", created.Job.ID), "", true)
	var got struct {
		Job publishJob `json:"job"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if got.Job.Stage != publishStagePublished || got.Job.ArticleURL == "" {
		t.Fatalf("approve 后详情应为 published + article_url，实际 %q %q", got.Job.Stage, got.Job.ArticleURL)
	}
	// 列表端点：摘要视图不携带大字段。
	list := doPublishReq(mux, http.MethodGet, "/api/publish/jobs?limit=5", "", true)
	var jobs struct {
		Jobs []publishJob `json:"jobs"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(jobs.Jobs) != 1 || jobs.Jobs[0].ContentHTML != "" || jobs.Jobs[0].BlocksJSON != "" {
		t.Fatalf("列表应为不含大字段的摘要视图，实际 %+v", jobs.Jobs)
	}
	// 非待审核阶段重复 approve：显式 400。
	if rec := doPublishReq(mux, http.MethodPost, target, `{"action":"approve"}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("published 后再 approve 应 400，实际 %d", rec.Code)
	}
}

// TestPublishJobStoreConcurrentCreate JSON 存储并发安全（施工图 S6）：并行建单 id 不撞、
// 一单不丢——mutex + 原子写是这套零依赖存储的全部并发契约，必须有测试钉住。
func TestPublishJobStoreConcurrentCreate(t *testing.T) {
	store := newPublishJobStore(filepath.Join(t.TempDir(), "publish-jobs.json"))
	const n = 40
	ids := make(chan int64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			job, created, err := store.create(fmt.Sprintf("site:key-%d", i), "https://mp.weixin.qq.com/s/x")
			if err != nil || !created {
				t.Errorf("create %d: err=%v created=%v", i, err, created)
				return
			}
			ids <- job.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	seen := map[int64]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("任务 id 撞号：%d", id)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Fatalf("应建 %d 单，实际 %d", n, len(seen))
	}
	jobs, err := store.list(n + 10)
	if err != nil || len(jobs) != n {
		t.Fatalf("落盘任务数 = %d（err=%v），应为 %d", len(jobs), err, n)
	}
}

// TestRenderSiteHTMLReusesProxyCore rendering 默认实现走 render_proxy 的转发内核：
// 请求打到 RENDER_SERVICE_URL 的 /render、带 Bearer RENDER_TOKEN、packId 固定 ft-default
// （网站视觉口径与人工排版同源，这是「不复制第二套渲染客户端」的关键断言）。
func TestRenderSiteHTMLReusesProxyCore(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"html":"<p>ok</p>"}`)
	}))
	defer srv.Close()
	t.Setenv("RENDER_SERVICE_URL", srv.URL)
	t.Setenv("RENDER_TOKEN", "render-tok")

	html, err := renderSiteHTML(context.Background(), "标题", "https://mp.weixin.qq.com/s/x", `[{"k":"p","text":"正文"}]`)
	if err != nil {
		t.Fatalf("renderSiteHTML: %v", err)
	}
	if html != "<p>ok</p>" {
		t.Fatalf("html = %q", html)
	}
	if gotPath != "/render" || gotAuth != "Bearer render-tok" {
		t.Fatalf("转发内核请求形态不符：path=%q auth=%q", gotPath, gotAuth)
	}
	if gotBody["packId"] != sitePublishRulePackID || gotBody["isRepost"] != true {
		t.Fatalf("渲染参数不符：packId=%v isRepost=%v", gotBody["packId"], gotBody["isRepost"])
	}
	// 缺 RENDER_TOKEN 必须 fail loud，不得无鉴权裸调。
	t.Setenv("RENDER_TOKEN", "")
	if _, err := renderSiteHTML(context.Background(), "标题", "src", "[]"); err == nil {
		t.Fatalf("RENDER_TOKEN 缺失应报错")
	}
}

// TestOSSUploadByURLsReusesUploadProxyCore mirroring 默认实现走 handleImageUpload 的
// 上游代理内核（urls 模式）：同一个 uploadProxyURL 路径推导、同一套 Origin/Referer 约定。
func TestOSSUploadByURLsReusesUploadProxyCore(t *testing.T) {
	var gotPath, gotOrigin, gotReferer string
	var gotBody struct {
		URLs []string `json:"urls"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotOrigin = r.Header.Get("Origin")
		gotReferer = r.Header.Get("Referer")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":{"https://mmbiz.qpic.cn/a.jpg":"https://oss.example.com/a.jpg"}}`)
	}))
	defer srv.Close()
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", srv.URL)
	t.Setenv("WECHAT_UPLOAD_ORIGIN", "https://layout.test")

	results, err := ossUploadByURLs(context.Background(), []string{"https://mmbiz.qpic.cn/a.jpg"})
	if err != nil {
		t.Fatalf("ossUploadByURLs: %v", err)
	}
	if results["https://mmbiz.qpic.cn/a.jpg"] != "https://oss.example.com/a.jpg" {
		t.Fatalf("results = %v", results)
	}
	if gotPath != "/api/oss-upload" {
		t.Fatalf("上游路径 = %q，应复用 /api/oss-upload 推导", gotPath)
	}
	if gotOrigin != "https://layout.test" || gotReferer != "https://layout.test/" {
		t.Fatalf("Origin/Referer 约定不符：%q %q", gotOrigin, gotReferer)
	}
	if len(gotBody.URLs) != 1 {
		t.Fatalf("urls 模式请求体不符：%+v", gotBody)
	}
	// 缺 WECHAT_UPLOAD_ENDPOINT 必须 fail loud——图片转存是防丢图的硬前提。
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", "")
	if _, err := ossUploadByURLs(context.Background(), []string{"x"}); err == nil {
		t.Fatalf("WECHAT_UPLOAD_ENDPOINT 缺失应报错")
	}
}
