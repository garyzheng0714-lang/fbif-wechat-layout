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
const testPublishSyncServiceToken = "test-publish-sync-service-token"

func newPublishTestMux(t *testing.T, env *publishTestEnv) *http.ServeMux {
	t.Helper()
	t.Setenv("ADMIN_PASSWORD", testAdminPassword)
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", testPublishSyncServiceToken)
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

func doPublishServiceReq(mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("X-Publish-Sync-Token", testPublishSyncServiceToken)
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

func TestPublishSyncServiceTokenHasLeastPrivilege(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)

	created := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync",
		fmt.Sprintf(`{"url":%q,"title":"普通文章","content_html":"<p>这是足够长的普通公众号文章正文，用于验证最小权限提交。</p>","content_kind":"article","classification":"ordinary_confirmed","classifier_version":"test-v1"}`, testPublishSourceURL))
	if created.Code != http.StatusCreated {
		t.Fatalf("同步服务凭证应能提交任务，实际 %d body=%s", created.Code, created.Body.String())
	}
	var response struct {
		Job publishSyncJobSummary `json:"job"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil || response.Job.ID <= 0 {
		t.Fatalf("提交响应应为紧凑任务摘要：err=%v body=%s", err, created.Body.String())
	}
	status := doPublishServiceReq(mux, http.MethodPost, "/api/publish/sync-status",
		fmt.Sprintf(`{"job_ids":[%d]}`, response.Job.ID))
	if status.Code != http.StatusOK || strings.Contains(status.Body.String(), "content_html") ||
		strings.Contains(status.Body.String(), "blocks_json") || strings.Contains(status.Body.String(), "risk_raw") {
		t.Fatalf("同步状态应可读且不含大字段/模型原文，实际 %d body=%s", status.Code, status.Body.String())
	}
	monitoring := doPublishServiceReq(mux, http.MethodGet, "/api/publish/monitoring", "")
	if monitoring.Code != http.StatusOK || strings.Contains(monitoring.Body.String(), "content_html") ||
		strings.Contains(monitoring.Body.String(), "blocks_json") || strings.Contains(monitoring.Body.String(), "risk_raw") {
		t.Fatalf("同步服务凭证应能读取无正文的运行指标，实际 %d body=%s", monitoring.Code, monitoring.Body.String())
	}

	for _, tc := range []struct {
		method, target, body string
	}{
		{http.MethodGet, "/api/publish/readiness", ""},
		{http.MethodGet, "/api/publish/jobs", ""},
		{http.MethodGet, fmt.Sprintf("/api/publish/jobs/%d", response.Job.ID), ""},
		{http.MethodPost, fmt.Sprintf("/api/publish/jobs/%d/review", response.Job.ID), `{"action":"approve"}`},
		{http.MethodPost, fmt.Sprintf("/api/publish/jobs/%d/retry", response.Job.ID), ""},
		{http.MethodPost, fmt.Sprintf("/api/publish/jobs/%d/reconcile", response.Job.ID), `{"confirm_not_created":true}`},
	} {
		rec := doPublishServiceReq(mux, tc.method, tc.target, tc.body)
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusNotFound {
			t.Fatalf("同步服务凭证不得调用管理端点 %s %s，实际 %d body=%s", tc.method, tc.target, rec.Code, rec.Body.String())
		}
	}
}

func TestPublishMachineSiteSyncAcceptsURLOnlyAndFetchesInsideLayoutPipeline(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync",
		fmt.Sprintf(`{"url":%q,"content_kind":"article","classification":"ordinary_confirmed","classifier_version":"monitor-v1"}`, testPublishSourceURL))
	if rec.Code != http.StatusCreated {
		t.Fatalf("机器只传原文链接应能建单，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Job publishSyncJobSummary `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	job, err := env.store.byID(response.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.ContentSource != publishContentWebFetch || job.ContentHTML == "" || job.Stage != publishStageAwaitingReview {
		t.Fatalf("URL-only 入口必须由排版工具回源抓取并停在人审前：%+v", job)
	}
	if job.RulePackID != sitePublishRulePackID || response.Job.RulePackID != sitePublishRulePackID {
		t.Fatalf("未指定规则包时应落默认 ft-default：job=%q summary=%q", job.RulePackID, response.Job.RulePackID)
	}
}

func TestPublishSiteSyncPersistsAndUsesSelectedRulePack(t *testing.T) {
	env := newPublishTestEnv(t)
	env.pipeline.render = func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
		if got := publishRulePackIDFromContext(ctx); got != foodTalksSeriesSiteRuleAccountID {
			return publishRenderResult{}, fmt.Errorf("render received rule pack %q", got)
		}
		rule := testPublishResolvedRule()
		rule.AccountID = foodTalksSeriesSiteRuleAccountID
		rule.PresetID = foodTalksSeriesSiteDefaultRulePresetID
		return successfulPublishRenderResult(testPublishRenderedHTML, rule), nil
	}
	mux := newPublishTestMux(t, env)
	rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync",
		fmt.Sprintf(`{"url":%q,"rule_pack_id":"ft-series","content_kind":"article","classification":"ordinary_confirmed","classifier_version":"monitor-v1"}`, testPublishSourceURL))
	if rec.Code != http.StatusCreated {
		t.Fatalf("选择 ft-series 应能建单，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Job publishSyncJobSummary `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	job, err := env.store.byID(response.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.RulePackID != foodTalksSeriesSiteRuleAccountID || response.Job.RulePackID != foodTalksSeriesSiteRuleAccountID ||
		job.RulePresetID != foodTalksSeriesSiteDefaultRulePresetID {
		t.Fatalf("规则包选择没有贯穿任务与渲染证据：job=%+v summary=%+v", job, response.Job)
	}
}

func TestPublishSiteSyncRejectsUnknownRulePack(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync",
		fmt.Sprintf(`{"url":%q,"rule_pack_id":"fbif","content_kind":"article","classification":"ordinary_confirmed","classifier_version":"monitor-v1"}`, testPublishSourceURL))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "规则包") {
		t.Fatalf("非网站规则包必须在建单前拒绝：status=%d body=%s", rec.Code, rec.Body.String())
	}
	if jobs, err := env.store.all(); err != nil || len(jobs) != 0 {
		t.Fatalf("非法规则包不得创建任务：jobs=%d err=%v", len(jobs), err)
	}
}

func TestPublishSiteSyncRerenderCanSwitchExplicitRulePack(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	first := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync",
		fmt.Sprintf(`{"url":%q,"content_kind":"article","classification":"ordinary_confirmed","classifier_version":"monitor-v1"}`, testPublishSourceURL))
	if first.Code != http.StatusCreated {
		t.Fatalf("首次 URL-only 提交失败：%d %s", first.Code, first.Body.String())
	}
	var firstResponse struct {
		Job publishSyncJobSummary `json:"job"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResponse); err != nil {
		t.Fatal(err)
	}
	env.pipeline.render = func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
		if got := publishRulePackIDFromContext(ctx); got != foodTalksSeriesSiteRuleAccountID {
			return publishRenderResult{}, fmt.Errorf("rerender received rule pack %q", got)
		}
		rule := testPublishResolvedRule()
		rule.AccountID = foodTalksSeriesSiteRuleAccountID
		rule.PresetID = foodTalksSeriesSiteDefaultRulePresetID
		return successfulPublishRenderResult(testPublishRenderedHTML, rule), nil
	}
	second := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync",
		fmt.Sprintf(`{"url":%q,"rule_pack_id":"ft-series","rerender_latest":true,"content_kind":"article","classification":"ordinary_confirmed","classifier_version":"monitor-v1"}`, testPublishSourceURL))
	if second.Code != http.StatusOK {
		t.Fatalf("切换规则重排失败：%d %s", second.Code, second.Body.String())
	}
	job, err := env.store.byID(firstResponse.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.RulePackID != foodTalksSeriesSiteRuleAccountID || job.RulePresetID != foodTalksSeriesSiteDefaultRulePresetID ||
		job.Stage != publishStageAwaitingReview {
		t.Fatalf("重排后规则包/阶段不符：%+v", job)
	}
}

func TestPublishMachineSiteSyncFailsClosedWithoutOrdinaryEvidence(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync",
		fmt.Sprintf(`{"url":%q}`, testPublishSourceURL))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "ordinary") {
		t.Fatalf("missing positive type evidence must be held: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if jobs, err := env.store.all(); err != nil || len(jobs) != 0 {
		t.Fatalf("fail-closed request must not create a job: jobs=%d err=%v", len(jobs), err)
	}
}

func TestPublishMachineSiteSyncFailsClosedWithEmptyBody(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	body := fmt.Sprintf(`{"url":%q,"content_html":"<p> </p>","content_kind":"article","classification":"ordinary_confirmed","classifier_version":"test-v1"}`, testPublishSourceURL)
	rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync", body)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "non-empty") {
		t.Fatalf("empty machine body must be held: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if jobs, err := env.store.all(); err != nil || len(jobs) != 0 {
		t.Fatalf("empty body request must not create a job: jobs=%d err=%v", len(jobs), err)
	}
}

func TestPublishMachineOfficialSyncFailsClosedWithoutOrdinaryEvidence(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	body := fmt.Sprintf(`{"url":%q,"title":"官方标题","content_html":"<p>这是足够长的官方普通文章正文内容。</p>"}`, testPublishSourceURL)
	rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/official-sync", body)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "ordinary") {
		t.Fatalf("official machine request without type evidence must be held: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if jobs, err := env.store.all(); err != nil || len(jobs) != 0 {
		t.Fatalf("fail-closed official request must not create a job: jobs=%d err=%v", len(jobs), err)
	}
}

func TestPublishMachineSyncRejectsUnsafeActiveHTMLBeforeJobCreation(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	cases := []struct {
		path string
		html string
	}{
		{"/api/publish/site-sync", `<p>普通正文</p><iframe src="https://example.com/embed"></iframe>`},
		{"/api/publish/official-sync", `<p onclick="publish()">普通正文</p>`},
		{"/api/publish/official-sync", `<a href="javascript:alert(1)">普通正文链接</a>`},
	}
	for index, tc := range cases {
		body, err := json.Marshal(map[string]string{
			"url":                fmt.Sprintf("https://mp.weixin.qq.com/s?__biz=MzUnsafe&mid=%d&idx=1", index+1),
			"title":              "不安全正文",
			"content_html":       tc.html,
			"content_kind":       "article",
			"classification":     "ordinary_confirmed",
			"classifier_version": "test-v1",
		})
		if err != nil {
			t.Fatal(err)
		}
		rec := doPublishServiceReq(mux, http.MethodPost, tc.path, string(body))
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "unsafe") {
			t.Fatalf("unsafe machine HTML should be rejected: path=%s status=%d body=%s", tc.path, rec.Code, rec.Body.String())
		}
	}
	if jobs, err := env.store.all(); err != nil || len(jobs) != 0 {
		t.Fatalf("unsafe machine HTML must not create jobs: jobs=%d err=%v", len(jobs), err)
	}
}

func TestPublishHTTPReviewDisabledByDefault(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	target := fmt.Sprintf("/api/publish/jobs/%d/review", job.ID)
	rec := doPublishReq(mux, http.MethodPost, target, `{"action":"approve"}`, true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("HTTP 审核默认应关闭并返回 404，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview || got.ReviewAction != "" {
		t.Fatalf("关闭的 HTTP 审核不得改变任务：stage=%q action=%q", got.Stage, got.ReviewAction)
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
		"url":          "https://mp.weixin.qq.com/s?__biz=MzPlainText&mid=101&idx=1&sn=plain",
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
		"url":          "https://mp.weixin.qq.com/s?__biz=MzPlainText&mid=102&idx=1&sn=links",
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

func TestPublishSiteSyncRerenderLatestReusesExistingDraft(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	renderCalls := 0
	env.pipeline.render = func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
		renderCalls++
		rule := testPublishResolvedRule()
		rule.Revision = 6 + renderCalls
		html := fmt.Sprintf(
			`<p>最新规则第 %d 版</p><img src="https://mmbiz.qpic.cn/rerender-cover.jpg">`,
			rule.Revision,
		)
		return successfulPublishRenderResult(html, rule), nil
	}

	body := fmt.Sprintf(
		`{"url":%q,"title":"重新排版测试","content_html":"<p>这是足够长的结构化正文，用于验证同一草稿按最新规则重排。</p><p><img src=\"https://mmbiz.qpic.cn/rerender-cover.jpg\"></p>"}`,
		testPublishSourceURL,
	)
	first := doPublishReq(mux, http.MethodPost, "/api/publish/site-sync", body, true)
	if first.Code != http.StatusCreated {
		t.Fatalf("首次提交应 201，实际 %d body=%s", first.Code, first.Body.String())
	}
	var firstResp struct {
		Job publishSyncJobSummary `json:"job"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResp); err != nil {
		t.Fatal(err)
	}
	before, err := env.store.byID(firstResp.Job.ID)
	if err != nil {
		t.Fatal(err)
	}

	rerenderBody := strings.TrimSuffix(body, "}") + `,"rerender_latest":true}`
	second := doPublishReq(mux, http.MethodPost, "/api/publish/site-sync", rerenderBody, true)
	if second.Code != http.StatusOK {
		t.Fatalf("重新排版应 200，实际 %d body=%s", second.Code, second.Body.String())
	}
	var secondResp struct {
		Job        publishSyncJobSummary `json:"job"`
		Existing   bool                  `json:"existing"`
		Rerendered bool                  `json:"rerendered"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResp); err != nil {
		t.Fatal(err)
	}
	after, err := env.store.byID(firstResp.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !secondResp.Existing || !secondResp.Rerendered || secondResp.Job.ID != firstResp.Job.ID {
		t.Fatalf("必须复用同一任务并确认已重排：%+v", secondResp)
	}
	if after.FoodTalksNewsID != before.FoodTalksNewsID || after.ContentVersion != before.ContentVersion+1 ||
		after.RuleRevision != 8 || after.ReviewAction != "" {
		t.Fatalf("同一草稿最新规则重排结果不符：before=%+v after=%+v", before, after)
	}
	if createN, updateN := env.ft.counts(); createN != 1 || updateN != 1 {
		t.Fatalf("必须 Create 一次、更新同一草稿一次，实际 create=%d update=%d", createN, updateN)
	}

	if _, err := env.store.update(after.ID, func(current *publishJob) {
		current.Stage = publishStageRendering
	}); err != nil {
		t.Fatal(err)
	}
	inProgress := doPublishReq(mux, http.MethodPost, "/api/publish/site-sync", rerenderBody, true)
	if inProgress.Code != http.StatusConflict {
		t.Fatalf("相同内容仍在处理中时不得伪报已重排，实际 %d body=%s", inProgress.Code, inProgress.Body.String())
	}
	if createN, updateN := env.ft.counts(); createN != 1 || updateN != 1 {
		t.Fatalf("冲突请求不得新增或更新网站草稿，实际 create=%d update=%d", createN, updateN)
	}
}

func TestPublishSiteSyncRefetchLatestReplacesSupplierContentFromOriginalURL(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	first := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync",
		fmt.Sprintf(`{"url":%q,"content_kind":"article","classification":"ordinary_confirmed","classifier_version":"monitor-v1"}`, testPublishSourceURL))
	if first.Code != http.StatusCreated {
		t.Fatalf("首次 URL-only 提交失败：%d %s", first.Code, first.Body.String())
	}
	var firstResp struct {
		Job publishSyncJobSummary `json:"job"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResp); err != nil {
		t.Fatal(err)
	}
	before, err := env.store.byID(firstResp.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	refetchedBlocks := `[{"k":"p","text":"原文回源后的第一段"},{"k":"h","text":"原文回源后的标题"}]`
	env.pipeline.fetch = func(ctx context.Context, articleURL string) (string, string, string, string, string, error) {
		if got := publishRulePackIDFromContext(ctx); got != sitePublishRulePackID {
			t.Fatalf("回源抓取必须沿用网站默认规则包，实际 %q", got)
		}
		return "原文标题", "原文公众号", "原文作者", articleURL, refetchedBlocks, nil
	}
	second := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync",
		fmt.Sprintf(`{"url":%q,"refetch_latest":true,"rule_pack_id":"ft-default","content_kind":"article","classification":"ordinary_confirmed","classifier_version":"monitor-v1"}`, testPublishSourceURL))
	if second.Code != http.StatusOK {
		t.Fatalf("从原文回源重排失败：%d %s", second.Code, second.Body.String())
	}
	var secondResp struct {
		Job        publishSyncJobSummary `json:"job"`
		Rerendered bool                  `json:"rerendered"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResp); err != nil {
		t.Fatal(err)
	}
	after, err := env.store.byID(firstResp.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !secondResp.Rerendered || secondResp.Job.ID != firstResp.Job.ID ||
		after.ContentSource != publishContentWebFetch || after.Title != "原文标题" || after.Author != "原文作者" ||
		after.BlocksJSON != refetchedBlocks || after.FoodTalksNewsID != before.FoodTalksNewsID ||
		after.ContentVersion != before.ContentVersion+1 || after.Stage != publishStageAwaitingReview {
		t.Fatalf("回源重排必须替换 supplier 内容并复用同一隐藏草稿：before=%+v after=%+v response=%+v", before, after, secondResp)
	}
}

// TestPublishReviewAndDetailHTTP 审核端点走通 approve 全链路，详情端点回读全量字段。
func TestPublishReviewAndDetailHTTP(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("PUBLISH_HTTP_REVIEW_ENABLED", "1")
	t.Setenv("PUBLISH_HTTP_REVIEWER_OPEN_ID", "ou_breakglass_reviewer")
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_breakglass_reviewer")
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
		json.NewEncoder(w).Encode(successfulPublishRenderResult("<p>ok</p>", testPublishResolvedRule()))
	}))
	defer srv.Close()
	t.Setenv("RENDER_SERVICE_URL", srv.URL)
	t.Setenv("RENDER_TOKEN", "render-tok")

	result, err := renderSiteHTML(context.Background(), "标题", "https://mp.weixin.qq.com/s/x", `[{"k":"p","text":"正文"}]`, true)
	if err != nil {
		t.Fatalf("renderSiteHTML: %v", err)
	}
	if result.HTML != "<p>ok</p>" {
		t.Fatalf("html = %q", result.HTML)
	}
	if gotPath != "/render" || gotAuth != "Bearer render-tok" {
		t.Fatalf("转发内核请求形态不符：path=%q auth=%q", gotPath, gotAuth)
	}
	if gotBody["packId"] != sitePublishRulePackID || gotBody["isRepost"] != true {
		t.Fatalf("渲染参数不符：packId=%v isRepost=%v", gotBody["packId"], gotBody["isRepost"])
	}
	if gotBody["ruleMode"] != "latest" || gotBody["ruleSnapshot"] != nil {
		t.Fatalf("自动发布必须现场读取最新规则，不能携带旧快照：%v", gotBody)
	}
	// 缺 RENDER_TOKEN 必须 fail loud，不得无鉴权裸调。
	t.Setenv("RENDER_TOKEN", "")
	if _, err := renderSiteHTML(context.Background(), "标题", "src", "[]", true); err == nil {
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
