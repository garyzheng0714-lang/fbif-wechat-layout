package main

// publish_preview_test.go — 未上架预览页安全模型与渲染契约测试。
// 锁死：签名门（HMAC，错误/缺失/不存在/阶段太早一律 404 不区分成因）、
// 无 AUTH_SESSION_SECRET 时 503、页面含警示横幅 + 转义标题 + 原样正文 + AI 参数页尾。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

func newPreviewMux(t *testing.T, env *publishTestEnv) *http.ServeMux {
	t.Helper()
	t.Setenv("AUTH_SESSION_SECRET", "test-session-preview-secret")
	mux := http.NewServeMux()
	registerPublishPreviewRoute(mux, env.store)
	return mux
}

func getPreview(mux *http.ServeMux, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestPublishPreviewSignatureGate 签名是预览页唯一的门：正确签名 200；签名错、缺签名、
// 任务不存在（即使签名按该 id 算对）一律 404——同一响应不给探测面。
func TestPublishPreviewSignatureGate(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPreviewMux(t, env)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	base := "/preview/publish/" + itoa64(job.ID)

	if rec := getPreview(mux, base+"?t="+publishPreviewSig(job.ID)+"&local=1"); rec.Code != http.StatusOK {
		t.Fatalf("正确签名应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	for name, target := range map[string]string{
		"签名错误":  base + "?t=deadbeef",
		"缺签名":   base,
		"空签名":   base + "?t=",
		"不存在任务": "/preview/publish/9999?t=" + publishPreviewSig(9999),
	} {
		if rec := getPreview(mux, target); rec.Code != http.StatusNotFound {
			t.Fatalf("%s 应 404，实际 %d", name, rec.Code)
		}
	}
}

// TestPublishPreviewRendersArticle 页面契约：警示横幅在场、标题 HTML 转义（防注入）、
// content_html 原样嵌入（就是将要发布的字节）、AI 预填参数页尾在场。
func TestPublishPreviewRendersArticle(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPreviewMux(t, env)
	env.pipeline.fetch = func(ctx context.Context, articleURL string) (string, string, string, string, string, error) {
		return `恶意<script>标题 & "引号"`, "测试公众号", "", articleURL, testPublishBlocksJSON, nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	rec := getPreview(mux, "/preview/publish/"+itoa64(job.ID)+"?t="+publishPreviewSig(job.ID)+"&local=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，实际 %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
	}
	for header, want := range map[string]string{
		"Content-Security-Policy": "default-src 'none'",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
	} {
		if got := rec.Header().Get(header); !strings.Contains(got, want) {
			t.Fatalf("%s 缺安全策略 %q，实际 %q", header, want, got)
		}
	}
	body := rec.Body.String()
	if !strings.Contains(body, "预览 · 未发布——本页仅供审核，实际上线以 FoodTalks 为准") {
		t.Fatalf("缺警示横幅：%s", body)
	}
	// 标题必须转义：转义形态在场、原始 <script> 不得出现。
	if !strings.Contains(body, "恶意&lt;script&gt;标题 &amp; &#34;引号&#34;") {
		t.Fatalf("标题未按预期转义：%s", body)
	}
	if strings.Contains(body, "<script>") {
		t.Fatalf("原始 <script> 泄漏进页面（转义失效）：%s", body)
	}
	// 正文原样嵌入：mirroring 后的 OSS 图 src 逐字在场。
	if !strings.Contains(body, `src="`+ossURLFor("https://mmbiz.qpic.cn/a.jpg")+`"`) {
		t.Fatalf("content_html 未原样嵌入：%s", body)
	}
	// AI 预填参数页尾（本测试 enrich 未装配 → 规则兜底值：author 宁空勿造显示「-」，
	// 来源行是 M7 确定性判定——外部公众号=转载）。
	for _, want := range []string{"AI 预填参数", "摘要：", "作者：-", "来源：转载 · 测试公众号", "AI 预填未执行"} {
		if !strings.Contains(body, want) {
			t.Fatalf("页尾缺 %q：%s", want, body)
		}
	}
}

// TestPublishPreviewStageGate 仅 awaiting_review 及之后可预览：更早阶段（content_html
// 未定型）即使签名正确也 404；无 AUTH_SESSION_SECRET 配置时端点 503（签名无密钥可算）。
func TestPublishPreviewStageGate(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPreviewMux(t, env)
	// 手工建一单停在 created（不推进）。
	early, _, err := env.store.create("site:early", "https://mp.weixin.qq.com/s/early")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rec := getPreview(mux, "/preview/publish/"+itoa64(early.ID)+"?t="+publishPreviewSig(early.ID)); rec.Code != http.StatusNotFound {
		t.Fatalf("早期阶段应 404，实际 %d", rec.Code)
	}
	// 终态（rejected）仍可预览：审核后回看依据不消失。
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewReject, ""); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rec := getPreview(mux, "/preview/publish/"+itoa64(job.ID)+"?t="+publishPreviewSig(job.ID)+"&local=1"); rec.Code != http.StatusOK {
		t.Fatalf("rejected 终态应仍可预览，实际 %d", rec.Code)
	}
	// 无 AUTH_SESSION_SECRET → 503。
	t.Setenv("AUTH_SESSION_SECRET", "")
	if rec := getPreview(mux, "/preview/publish/"+itoa64(job.ID)+"?t=whatever"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("无 AUTH_SESSION_SECRET 应 503，实际 %d", rec.Code)
	}
}

// TestPublishPreviewRedirectsToOfficial 官方预览优先（2026-07-10 用户裁定）：草稿已
// 进站（有 FoodTalksNewsID）且能现铸预览 token 时，302 到 FoodTalks 官方预览页
// {front}/preview/news/{id}?token=...；?local=1 强制本地页（AI 参数兜底视图）。
func TestPublishPreviewRedirectsToOfficial(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPreviewMux(t, env)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	ft := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/preview/token") {
			if got := r.Header.Get("Authorization"); got != "Bearer svc-token" {
				t.Errorf("官方预览 token 请求鉴权头=%q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{"id":7249,"token":"tok+fresh&123"}}`)
			return
		}
		t.Errorf("FoodTalks 桩收到意外请求 %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(ft.Close)
	t.Setenv("FOODTALKS_BASE_URL", ft.URL)
	t.Setenv("FOODTALKS_TOKEN", "svc-token")
	t.Setenv("FOODTALKS_FRONT_URL", "https://test.foodtalks.cn")

	rec := getPreview(mux, "/preview/publish/"+itoa64(job.ID)+"?t="+publishPreviewSig(job.ID))
	if rec.Code != http.StatusFound {
		t.Fatalf("应 302 到官方预览，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	got, _ := env.store.byID(job.ID)
	wantLoc := "https://test.foodtalks.cn/preview/news/" + itoa64(got.FoodTalksNewsID) + "?token=tok%2Bfresh%26123"
	if loc := rec.Header().Get("Location"); loc != wantLoc {
		t.Fatalf("Location=%q，应为 %q", loc, wantLoc)
	}
	for header, want := range map[string]string{
		"Referrer-Policy": "no-referrer",
		"Cache-Control":   "no-store",
		"X-Robots-Tag":    "noindex, nofollow",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Fatalf("官方预览重定向 %s=%q，应为 %q", header, got, want)
		}
	}
	// ?local=1 强制本地预览页（仍走签名门）。
	rec = getPreview(mux, "/preview/publish/"+itoa64(job.ID)+"?t="+publishPreviewSig(job.ID)+"&local=1")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "AI 预填参数") {
		t.Fatalf("local=1 应渲染本地页，实际 %d", rec.Code)
	}
}

func TestPublishPreviewOfficialTokenFailureFallsBackLocally(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPreviewMux(t, env)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	ft := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusBadGateway)
	}))
	t.Cleanup(ft.Close)
	t.Setenv("FOODTALKS_BASE_URL", ft.URL)
	t.Setenv("FOODTALKS_TOKEN", "svc-token")

	rec := getPreview(mux, "/preview/publish/"+itoa64(job.ID)+"?t="+publishPreviewSig(job.ID))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "AI 预填参数") {
		t.Fatalf("官方 token 失败应回落本地页，实际 %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestOfficialPublishPreviewURLRejectsUnsafeBase(t *testing.T) {
	for _, base := range []string{"javascript:alert(1)", "https://user@example.com", "//example.com"} {
		if _, err := officialPublishPreviewURL(base, 7249, "token"); err == nil {
			t.Fatalf("不安全前台 URL 应拒绝：%q", base)
		}
	}
}
