package main

// publish_preview_test.go — 未上架预览页安全模型与渲染契约测试。
// 锁死：签名门（HMAC，错误/缺失/不存在/阶段太早一律 404 不区分成因）、
// 无 ADMIN_PASSWORD 时 503、页面含警示横幅 + 转义标题 + 原样正文 + AI 参数页尾。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

func newPreviewMux(t *testing.T, env *publishTestEnv) *http.ServeMux {
	t.Helper()
	t.Setenv("ADMIN_PASSWORD", testAdminPassword)
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

	if rec := getPreview(mux, base+"?t="+publishPreviewSig(job.ID)); rec.Code != http.StatusOK {
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
	env.pipeline.fetch = func(ctx context.Context, articleURL string) (string, string, string, error) {
		return `恶意<script>标题 & "引号"`, "测试公众号", testPublishBlocksJSON, nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	rec := getPreview(mux, "/preview/publish/"+itoa64(job.ID)+"?t="+publishPreviewSig(job.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，实际 %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
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
	// AI 预填参数页尾（本测试 enrich 未装配 → 规则兜底值）。
	for _, want := range []string{"AI 预填参数", "摘要：", "作者：测试公众号", "AI 预填未执行"} {
		if !strings.Contains(body, want) {
			t.Fatalf("页尾缺 %q：%s", want, body)
		}
	}
}

// TestPublishPreviewStageGate 仅 awaiting_review 及之后可预览：更早阶段（content_html
// 未定型）即使签名正确也 404；无 ADMIN_PASSWORD 配置时端点 503（签名无密钥可算）。
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
	if rec := getPreview(mux, "/preview/publish/"+itoa64(job.ID)+"?t="+publishPreviewSig(job.ID)); rec.Code != http.StatusOK {
		t.Fatalf("rejected 终态应仍可预览，实际 %d", rec.Code)
	}
	// 无 ADMIN_PASSWORD → 503。
	t.Setenv("ADMIN_PASSWORD", "")
	if rec := getPreview(mux, "/preview/publish/"+itoa64(job.ID)+"?t=whatever"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("无 ADMIN_PASSWORD 应 503，实际 %d", rec.Code)
	}
}
