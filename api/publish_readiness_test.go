package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishReadinessReportsBooleanStateWithoutSecrets(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("DEPLOYMENT_WRITES_PAUSED", "1")
	t.Setenv("RENDER_SERVICE_URL", "http://127.0.0.1:19096")
	t.Setenv("RENDER_TOKEN", "secret-render-token")
	t.Setenv("FEISHU_APP_ID", "cli_secret_app")
	t.Setenv("FEISHU_APP_SECRET", "secret-feishu-app")
	t.Setenv("FEISHU_REVIEW_CHAT_ID", "oc_secret_chat")
	t.Setenv("FEISHU_VERIFICATION_TOKEN", "secret-verification")
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_secret_reviewer")
	t.Setenv("ARK_API_KEY", "secret-ark-key")
	t.Setenv("ARK_MODEL_ID", "model-a")
	t.Setenv("ARK_MODEL_ID_B", "model-b")
	t.Setenv("ARK_JUDGE_MODEL_ID", "model-judge")
	t.Setenv("FOODTALKS_BASE_URL", "https://api-we-test.foodtalks.cn")
	t.Setenv("FOODTALKS_FRONT_URL", "https://test.foodtalks.cn")
	t.Setenv("FOODTALKS_ADMIN_ARTICLE_URL", "https://admin-we-test.foodtalks.cn/#/")
	t.Setenv("FOODTALKS_CATEGORY_IDS", "52,58")
	t.Setenv("FOODTALKS_EXPECTED_TARGET", "test")
	t.Setenv("PUBLISH_HTTP_REVIEW_ENABLED", "")

	mux := newPublishTestMux(t, env)
	rec := doPublishReq(mux, http.MethodGet, "/api/publish/readiness", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("readiness 应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	for _, secret := range []string{
		"secret-render-token", "secret-feishu-app", "oc_secret_chat", "ou_secret_reviewer", "secret-ark-key", "test-token",
	} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("readiness 不得泄露配置值 %q：%s", secret, rec.Body.String())
		}
	}
	var got publishReadinessStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Ready || !got.WorkersPaused || !got.ServiceAuth.Configured || got.HTTPReviewEnabled || got.FoodTalks.Target != "test" ||
		!got.FoodTalks.TargetMatchesExpected || got.EnrichMode != "dual" {
		t.Fatalf("readiness 核心状态错误：%+v", got)
	}
	if !got.Feishu.NotifyConfigured || !got.Feishu.WSConfigured ||
		!got.Feishu.WebhookConfigured || !got.Feishu.ReviewerAllowlistConfigured {
		t.Fatalf("飞书 readiness 错误：%+v", got.Feishu)
	}
}

func TestPublishReadinessFailsClosedWithoutReviewerAllowlistOrServiceToken(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("ADMIN_PASSWORD", testAdminPassword)
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", testPublishSyncServiceToken)
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "")
	got := currentPublishReadiness(env.pipeline)
	if got.Feishu.ReviewerAllowlistConfigured || got.Ready {
		t.Fatalf("空审核白名单必须使 readiness=false：%+v", got)
	}
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer")
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", "")
	got = currentPublishReadiness(env.pipeline)
	if got.ServiceAuth.Configured || got.Ready {
		t.Fatalf("缺同步服务凭证必须使 readiness=false：%+v", got)
	}
}

func TestFoodTalksTargetClassificationIsTokenBasedAndHTTPSOnly(t *testing.T) {
	for _, raw := range []string{
		"https://api-we-test.foodtalks.cn",
		"https://test.foodtalks.cn",
		"https://admin-we-test.foodtalks.cn/#/",
	} {
		if got := foodTalksURLTarget(raw); got != "test" {
			t.Fatalf("foodTalksURLTarget(%q)=%q want test", raw, got)
		}
	}
	if got := foodTalksURLTarget("https://contest.foodtalks.cn"); got != "production" {
		t.Fatalf("substring 'test' must not spoof test target: got %q", got)
	}
	if configuredHTTPBaseURL("http://api-we-test.foodtalks.cn") {
		t.Fatal("non-loopback FoodTalks endpoints must use HTTPS")
	}
	if !configuredHTTPBaseURL("http://127.0.0.1:19090") {
		t.Fatal("local test endpoint should remain available")
	}
}

func TestPublishWorkersPausedBlocksHTTPRecoveryAndReview(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("DEPLOYMENT_WRITES_PAUSED", "true")
	mux := newPublishTestMux(t, env)

	rec := doPublishReq(mux, http.MethodPost, "/api/publish/site-sync",
		`{"url":"https://mp.weixin.qq.com/s/paused"}`, true)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "暂停") {
		t.Fatalf("暂停态提交应 503，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	rec = doPublishReq(mux, http.MethodPost, "/api/publish/official-sync",
		`{"url":"https://mp.weixin.qq.com/s/paused","title":"暂停态","content_html":"<p>正文</p>"}`, true)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "暂停") {
		t.Fatalf("暂停态官方正文提交应 503，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	if jobs, err := env.store.all(); err != nil || len(jobs) != 0 {
		t.Fatalf("暂停态不得建单：jobs=%d err=%v", len(jobs), err)
	}

	job, _, err := env.store.create("site:paused-recover", testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	env.pipeline.recoverJobs()
	after, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Stage != publishStageCreated || env.fetchCalls != 0 {
		t.Fatalf("暂停态恢复不得改状态/启动 worker：stage=%q fetch=%d", after.Stage, env.fetchCalls)
	}
	if _, err := env.pipeline.review(nil, job.ID, publishReviewApprove, ""); !errors.Is(err, errPublishWorkersPaused) {
		t.Fatalf("暂停态业务核心也必须拒绝审核，实际 err=%v", err)
	}
	if got, err := env.store.byID(job.ID); err != nil || got.Stage != publishStageCreated {
		t.Fatalf("暂停审核不得写任务：job=%+v err=%v", got, err)
	}
}

func TestDeploymentPauseBlocksRuleWritesButAllowsReadinessReads(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", testAdminPassword)
	t.Setenv("DEPLOYMENT_WRITES_PAUSED", "1")
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, store)

	body := []byte(`{"id":"paused-rule","name":"不应保存","baseline_id":"default","config":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
	req.Header.Set("X-Admin-Password", testAdminPassword)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("部署暂停态规则写应 503，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	doc, err := store.list()
	if err != nil {
		t.Fatal(err)
	}
	for _, preset := range doc.Presets {
		if preset.ID == "paused-rule" {
			t.Fatal("部署暂停态不得写入规则库")
		}
	}

	get := httptest.NewRequest(http.MethodGet, "/api/rules/admin/presets", nil)
	get.Header.Set("X-Admin-Password", testAdminPassword)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, get)
	if getRec.Code != http.StatusOK {
		t.Fatalf("部署暂停态仍应允许只读规则验收，实际 %d", getRec.Code)
	}
}

func TestPublishReadinessDistinguishesSingleAndFallbackEnrichment(t *testing.T) {
	t.Setenv("ARK_API_KEY", "key")
	t.Setenv("ARK_MODEL_ID", "same-model")
	t.Setenv("ARK_MODEL_ID_B", "same-model")
	t.Setenv("ARK_JUDGE_MODEL_ID", "judge")
	if got := publishEnrichMode(); got != "single" {
		t.Fatalf("A/B 相同不能伪报双模型，实际 %q", got)
	}
	t.Setenv("ARK_API_KEY", "")
	if got := publishEnrichMode(); got != "fallback" {
		t.Fatalf("缺 Key 应报告 fallback，实际 %q", got)
	}
}

func TestPublishReadinessOverallFlagIncludesRuntimeDependencies(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("ADMIN_PASSWORD", testAdminPassword)
	t.Setenv("FOODTALKS_BASE_URL", "https://api-we-test.foodtalks.cn")
	t.Setenv("FOODTALKS_TOKEN", "token")
	t.Setenv("FOODTALKS_FRONT_URL", "https://test.foodtalks.cn")
	t.Setenv("FOODTALKS_ADMIN_ARTICLE_URL", "https://admin-we-test.foodtalks.cn/#/")
	t.Setenv("FOODTALKS_CATEGORY_IDS", "52,58")
	t.Setenv("FOODTALKS_EXPECTED_TARGET", "test")
	t.Setenv("RENDER_SERVICE_URL", "not-a-url")
	t.Setenv("RENDER_TOKEN", "render")
	t.Setenv("FEISHU_APP_ID", "app")
	t.Setenv("FEISHU_APP_SECRET", "secret")
	t.Setenv("FEISHU_REVIEW_CHAT_ID", "chat")
	t.Setenv("ARK_API_KEY", "key")
	t.Setenv("ARK_MODEL_ID", "model-a")
	t.Setenv("ARK_MODEL_ID_B", "model-b")
	t.Setenv("ARK_JUDGE_MODEL_ID", "judge")

	got := currentPublishReadiness(env.pipeline)
	if got.Render.Configured || got.Ready {
		t.Fatalf("非法 render URL 不得使 overall readiness 假绿：%+v", got)
	}
	t.Setenv("RENDER_SERVICE_URL", "http://127.0.0.1:19096")
	t.Setenv("FEISHU_REVIEW_CHAT_ID", "")
	got = currentPublishReadiness(env.pipeline)
	if got.Feishu.NotifyConfigured || got.Ready {
		t.Fatalf("缺审核通知目标不得使 overall readiness 假绿：%+v", got)
	}
}

func TestPublishReadinessRejectsMixedTargetsAndCorruptStore(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("ADMIN_PASSWORD", testAdminPassword)
	t.Setenv("FOODTALKS_BASE_URL", "https://api-we-test.foodtalks.cn")
	t.Setenv("FOODTALKS_TOKEN", "token")
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	t.Setenv("FOODTALKS_ADMIN_ARTICLE_URL", "https://admin-we-test.foodtalks.cn/#/")
	t.Setenv("FOODTALKS_CATEGORY_IDS", "52,58")
	t.Setenv("FOODTALKS_EXPECTED_TARGET", "test")
	t.Setenv("RENDER_SERVICE_URL", "http://127.0.0.1:19096")
	t.Setenv("RENDER_TOKEN", "render")
	got := currentPublishReadiness(env.pipeline)
	if got.FoodTalks.Target != "mixed" || got.FoodTalks.TargetMatchesExpected || got.Ready {
		t.Fatalf("混用测试/正式地址必须 fail closed：%+v", got)
	}

	if err := os.WriteFile(env.store.path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	got = currentPublishReadiness(env.pipeline)
	if got.StoreValid || got.Ready {
		t.Fatalf("发布任务库损坏时 readiness 不得假绿：%+v", got)
	}
}
