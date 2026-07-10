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
	Ready         bool `json:"ready"`
	WorkersPaused bool `json:"workers_paused"`
	StoreValid    bool `json:"store_valid"`
	FoodTalks     struct {
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
		NotifyConfigured            bool `json:"notify_configured"`
		WSConfigured                bool `json:"ws_configured"`
		WebhookConfigured           bool `json:"webhook_configured"`
		ReviewerAllowlistConfigured bool `json:"reviewer_allowlist_configured"`
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
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

func foodTalksURLTarget(raw string) string {
	if !configuredHTTPBaseURL(raw) {
		return "unconfigured"
	}
	u, _ := url.Parse(strings.TrimSpace(raw))
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, "test") {
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
	out.WorkersPaused = publishWorkersPaused()
	out.StoreValid = p != nil && p.store != nil
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
	out.Feishu.WSConfigured = appID != "" && appSecret != ""
	out.Feishu.NotifyConfigured = out.Feishu.WSConfigured && chatID != ""
	out.Feishu.WebhookConfigured = strings.TrimSpace(os.Getenv("FEISHU_VERIFICATION_TOKEN")) != ""
	out.Feishu.ReviewerAllowlistConfigured = feishuReviewerAllowlistConfigured()
	out.EnrichMode = publishEnrichMode()
	out.JudgeConfigured = strings.TrimSpace(os.Getenv(arkJudgeModelIDEnv)) != ""
	out.Ready = adminPasswordConfigured() && out.StoreValid && out.FoodTalks.Configured &&
		out.FoodTalks.Categories && out.Render.Configured && out.Feishu.NotifyConfigured &&
		out.Feishu.WSConfigured && out.EnrichMode == "dual" && out.JudgeConfigured
	return out
}
