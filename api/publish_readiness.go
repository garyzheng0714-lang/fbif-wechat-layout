package main

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const (
	deploymentWritesPausedEnv  = "DEPLOYMENT_WRITES_PAUSED"
	foodTalksExpectedTargetEnv = "FOODTALKS_EXPECTED_TARGET"
)

var errPublishWorkersPaused = errors.New("发布流水线正在部署验收，写操作与后台 worker 暂停，请稍后重试")

type publishReadinessStatus struct {
	Ready          bool                      `json:"ready"`
	ConfigSchema   runtimeConfigSchemaStatus `json:"config_schema"`
	WorkersPaused  bool                      `json:"workers_paused"`
	StoreValid     bool                      `json:"store_valid"`
	GeneratedFiles generatedArtifactsStatus  `json:"generated_files"`
	ServiceAuth    struct {
		Configured bool `json:"configured"`
	} `json:"service_auth"`
	HTTPReviewEnabled bool `json:"http_review_enabled"`
	FoodTalks         struct {
		Configured            bool   `json:"configured"`
		Target                string `json:"target"`
		ExpectedTarget        string `json:"expected_target"`
		TargetMatchesExpected bool   `json:"target_matches_expected"`
		FrontExplicit         bool   `json:"front_explicit"`
		AdminExplicit         bool   `json:"admin_explicit"`
		CategoriesExplicit    bool   `json:"categories_explicit"`
		Categories            bool   `json:"categories_valid"`
	} `json:"foodtalks"`
	Render struct {
		Configured bool `json:"configured"`
	} `json:"render"`
	Feishu struct {
		NotifyConfigured            bool   `json:"notify_configured"`
		MonitorNotifyConfigured     bool   `json:"monitor_notify_configured"`
		WSConfigured                bool   `json:"ws_configured"`
		WebhookConfigured           bool   `json:"webhook_configured"`
		CallbackMode                string `json:"callback_mode"`
		CallbackModeExplicit        bool   `json:"callback_mode_explicit"`
		CallbackConfigured          bool   `json:"callback_configured"`
		ReviewerAllowlistConfigured bool   `json:"reviewer_allowlist_configured"`
	} `json:"feishu"`
	EnrichMode      string `json:"enrich_mode"`
	JudgeConfigured bool   `json:"judge_configured"`
}

func publishWorkersPaused() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(deploymentWritesPausedEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func requireDeploymentWritesActive(w http.ResponseWriter) bool {
	if publishWorkersPaused() {
		writeError(w, http.StatusServiceUnavailable, errPublishWorkersPaused.Error())
		return false
	}
	return true
}

func publishEnrichMode() string {
	if strings.TrimSpace(os.Getenv(arkAPIKeyEnv)) == "" {
		return "fallback"
	}
	modelB := strings.TrimSpace(os.Getenv(arkModelIDBEnv))
	judge := strings.TrimSpace(os.Getenv(arkJudgeModelIDEnv))
	if modelB != "" && modelB != arkModelID() && judge != "" {
		return "dual"
	}
	return "single"
}

func configuredHTTPBaseURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return u.Scheme == "https" || host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func foodTalksTestHost(host string) bool {
	for _, label := range strings.Split(strings.ToLower(strings.TrimSpace(host)), ".") {
		for _, token := range strings.Split(label, "-") {
			if token == "test" {
				return true
			}
		}
	}
	return false
}

func foodTalksURLTarget(raw string) string {
	if !configuredHTTPBaseURL(raw) {
		return "unconfigured"
	}
	u, _ := url.Parse(strings.TrimSpace(raw))
	host := strings.ToLower(u.Hostname())
	if foodTalksTestHost(host) {
		return "test"
	}
	if host == "foodtalks.cn" || strings.HasSuffix(host, ".foodtalks.cn") {
		return "production"
	}
	return "custom"
}

func classifyFoodTalksTarget(base, front, admin string) string {
	targets := []string{foodTalksURLTarget(base), foodTalksURLTarget(front), foodTalksURLTarget(admin)}
	if targets[0] == targets[1] && targets[1] == targets[2] {
		return targets[0]
	}
	for _, target := range targets {
		if target == "unconfigured" {
			return "unconfigured"
		}
	}
	return "mixed"
}

func currentPublishReadiness(p *publishPipeline) publishReadinessStatus {
	var out publishReadinessStatus
	out.ConfigSchema = currentRuntimeConfigSchemaStatus()
	out.WorkersPaused = publishWorkersPaused()
	out.StoreValid = p != nil && p.store != nil
	if p != nil {
		out.GeneratedFiles = currentGeneratedArtifactsStatus(p.publicDir)
	}
	if out.StoreValid {
		_, storeErr := p.store.all()
		out.StoreValid = storeErr == nil
	}
	baseURL := strings.TrimSpace(os.Getenv(foodTalksBaseURLEnv))
	frontURL := strings.TrimSpace(os.Getenv(siteFrontURLEnv))
	adminURL := strings.TrimSpace(os.Getenv(siteAdminURLEnv))
	categoryRaw := strings.TrimSpace(os.Getenv(siteCategoryIDsEnv))
	out.FoodTalks.FrontExplicit = configuredHTTPBaseURL(frontURL)
	out.FoodTalks.AdminExplicit = configuredHTTPBaseURL(adminURL)
	out.FoodTalks.CategoriesExplicit = categoryRaw != ""
	out.FoodTalks.Target = classifyFoodTalksTarget(baseURL, frontURL, adminURL)
	out.FoodTalks.ExpectedTarget = strings.ToLower(strings.TrimSpace(os.Getenv(foodTalksExpectedTargetEnv)))
	out.FoodTalks.TargetMatchesExpected = (out.FoodTalks.ExpectedTarget == "test" || out.FoodTalks.ExpectedTarget == "production") &&
		out.FoodTalks.Target == out.FoodTalks.ExpectedTarget
	_, _, categoryErr := siteCategoryIDs()
	out.FoodTalks.Categories = out.FoodTalks.CategoriesExplicit && categoryErr == nil
	out.FoodTalks.Configured = configuredHTTPBaseURL(baseURL) && strings.TrimSpace(os.Getenv(foodTalksTokenEnv)) != "" &&
		out.FoodTalks.FrontExplicit && out.FoodTalks.AdminExplicit && out.FoodTalks.Categories && out.FoodTalks.TargetMatchesExpected
	out.Render.Configured = configuredHTTPBaseURL(os.Getenv("RENDER_SERVICE_URL")) && renderToken() != ""
	appID := strings.TrimSpace(os.Getenv("FEISHU_APP_ID"))
	appSecret := strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET"))
	chatID := strings.TrimSpace(os.Getenv("FEISHU_REVIEW_CHAT_ID"))
	monitorUserID := strings.TrimSpace(os.Getenv(publishMonitorFeishuUserIDEnv))
	out.Feishu.WSConfigured = appID != "" && appSecret != ""
	out.Feishu.NotifyConfigured = out.Feishu.WSConfigured && chatID != ""
	out.Feishu.MonitorNotifyConfigured = out.Feishu.WSConfigured && monitorUserID != ""
	out.Feishu.WebhookConfigured = strings.TrimSpace(os.Getenv("FEISHU_VERIFICATION_TOKEN")) != ""
	out.Feishu.CallbackMode, out.Feishu.CallbackModeExplicit = configuredFeishuCallbackMode()
	if out.Feishu.CallbackMode == feishuCallbackModeWebhook {
		out.Feishu.CallbackConfigured = out.Feishu.WebhookConfigured
	} else {
		out.Feishu.CallbackConfigured = out.Feishu.WSConfigured
	}
	out.Feishu.ReviewerAllowlistConfigured = feishuReviewerAllowlistConfigured()
	out.ServiceAuth.Configured = publishSyncServiceTokenConfigured()
	out.HTTPReviewEnabled = publishHTTPReviewEnabled()
	out.EnrichMode = publishEnrichMode()
	out.JudgeConfigured = strings.TrimSpace(os.Getenv(arkJudgeModelIDEnv)) != ""
	out.Ready = out.ConfigSchema.Matches && adminPasswordConfigured() && out.ServiceAuth.Configured && !out.HTTPReviewEnabled &&
		out.GeneratedFiles.ready() &&
		out.StoreValid && out.FoodTalks.Configured && out.Feishu.ReviewerAllowlistConfigured &&
		out.FoodTalks.Categories && out.Render.Configured && out.Feishu.NotifyConfigured &&
		out.Feishu.MonitorNotifyConfigured && out.Feishu.CallbackModeExplicit && out.Feishu.CallbackConfigured &&
		out.EnrichMode == "dual" && out.JudgeConfigured
	return out
}
