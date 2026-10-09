package main

// feishu_review_card_icons_test.go — 审核聚合卡按钮图标与缩略图裁切守护。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// reviewCardTestIconKeys 每个内嵌图标都给一个假 img_key，用于检查按钮是否都能拿到图标。
func reviewCardTestIconKeys(t *testing.T) reviewCardIcons {
	t.Helper()
	entries, err := reviewCardIconFS.ReadDir("cardicons")
	if err != nil || len(entries) == 0 {
		t.Fatalf("按钮图标未打包: %v", err)
	}
	icons := reviewCardIcons{}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".png")
		icons[name] = "img_" + name
	}
	return icons
}

func reviewCardButtons(node any, out *[]map[string]any) {
	switch value := node.(type) {
	case map[string]any:
		if value["tag"] == "button" {
			*out = append(*out, value)
		}
		for _, child := range value {
			reviewCardButtons(child, out)
		}
	case []any:
		for _, child := range value {
			reviewCardButtons(child, out)
		}
	}
}

// TestReviewBatchCardEveryButtonHasLucideIcon 用户要求按钮带 Lucide 图标（2026-10-09）。
// 覆盖聚合卡所有状态分支：每个按钮用到的「图标-配色」都必须有对应的内嵌 PNG，
// 内部标记不得留在卡里（飞书 2.0 对未知字段报错）。
func TestReviewBatchCardEveryButtonHasLucideIcon(t *testing.T) {
	t.Setenv("AUTH_SESSION_SECRET", "test-session-preview-secret")
	icons := reviewCardTestIconKeys(t)
	now := time.Now().UTC()
	open := func(job publishJob) publishJob {
		job.PublishedAt = now.Add(-time.Hour).Format(time.RFC3339)
		job.UnpublishPolicySet = true
		job.UnpublishWindowHours = 8
		job.UnpublishWindowUntil = now.Add(time.Hour).Format(time.RFC3339)
		return job
	}
	variant := func(stage string, edit func(*publishJob)) publishJob {
		job := reviewCardTestJob(stage)
		job.FoodTalksNewsID = 7318
		job.PublicationAction = publishPublicationActionPublish
		if edit != nil {
			edit(&job)
		}
		return job
	}
	jobs := []publishJob{
		variant(publishStageAwaitingReview, func(j *publishJob) { j.RiskLevel = publishRiskPass }),
		variant(publishStageAwaitingReview, func(j *publishJob) { j.RiskLevel = publishRiskBlock }),
		variant(publishStageAwaitingReview, func(j *publishJob) { j.RiskLevel = "" }),
		open(variant(publishStagePublished, nil)),
		variant(publishStagePublished, nil),
		variant(publishStagePublished, func(j *publishJob) { j.SourceResyncState = publishSourceResyncQueued }),
		variant(publishStagePublished, func(j *publishJob) { j.SourceResyncState = publishSourceResyncReconciliationRequired }),
		variant(publishStagePublished, func(j *publishJob) { j.SourceURL = "https://www.example.com/a" }),
		variant(publishStagePublished, func(j *publishJob) { j.SourceURL = "" }),
		variant(publishStageOffline, nil),
		variant(publishStagePublicizing, nil),
		variant(publishStageUnpublishing, nil),
		variant(publishStageRepublishing, nil),
		variant(publishStageRejected, nil),
		variant(publishStageReconciliationRequired, nil),
	}
	for i := range jobs {
		jobs[i].ID = int64(300 + i)
	}
	card := buildReviewBatchCardWithIcons(jobs, icons)
	body := cardJSON(t, card)
	if strings.Contains(body, reviewCardIconField) {
		t.Fatalf("内部图标标记不得留在卡里: %s", body)
	}
	var buttons []map[string]any
	reviewCardButtons(card, &buttons)
	if len(buttons) < len(jobs) {
		t.Fatalf("按钮数异常：%d", len(buttons))
	}
	for _, button := range buttons {
		icon, _ := button["icon"].(map[string]any)
		if icon["tag"] != "custom_icon" || !strings.HasPrefix(fmt.Sprint(icon["img_key"]), "img_") {
			t.Fatalf("按钮缺少 Lucide 图标（缺对应的 cardicons PNG？）: %#v", button)
		}
	}

	plain := cardJSON(t, buildReviewBatchCard(jobs))
	if strings.Contains(plain, reviewCardIconField) || strings.Contains(plain, "custom_icon") {
		t.Fatalf("没有图标 img_key 时按钮应照常无图标、且不留标记: %s", plain)
	}
}

// TestReviewBatchCardIconColorFollowsButton 图标是预先上色的 PNG，配色必须跟按钮走：
// 蓝底主按钮白色、红色按钮红色、不可点按钮浅灰、其余中灰。
func TestReviewBatchCardIconColorFollowsButton(t *testing.T) {
	icons := reviewCardTestIconKeys(t)
	pending := reviewCardTestJob(publishStageAwaitingReview)
	pending.RiskLevel = publishRiskPass
	body := cardJSON(t, buildReviewBatchCardWithIcons([]publishJob{pending}, icons))
	for _, want := range []string{"img_send-inverse", "img_external-link-default"} {
		if !strings.Contains(body, want) {
			t.Fatalf("待审核卡应有 %s: %s", want, body)
		}
	}
	pending.RiskLevel = publishRiskBlock
	if body := cardJSON(t, buildReviewBatchCardWithIcons([]publishJob{pending}, icons)); !strings.Contains(body, "img_send-danger") {
		t.Fatalf("强制发布是红色按钮，图标应为红色: %s", body)
	}
	rejected := reviewCardTestJob(publishStageRejected)
	if body := cardJSON(t, buildReviewBatchCardWithIcons([]publishJob{rejected}, icons)); !strings.Contains(body, "img_circle-x-disabled") {
		t.Fatalf("已驳回是不可点按钮，图标应为浅灰: %s", body)
	}
}

// TestReviewCardIconCacheUploadsOncePerApp 图标由发卡应用上传一次后按应用缓存；上传失败
// 时本卡不带图标，且短时间内不反复重试。
func TestReviewCardIconCacheUploadsOncePerApp(t *testing.T) {
	var uploads, failing atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"msg":"ok","tenant_access_token":"tok-icon","expire":7200}`)
		case "/open-apis/im/v1/images":
			if failing.Load() == 1 {
				fmt.Fprint(w, `{"code":99991400,"msg":"rate limited"}`)
				return
			}
			n := uploads.Add(1)
			fmt.Fprintf(w, `{"code":0,"msg":"ok","data":{"image_key":"img_%d"}}`, n)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	entries, _ := reviewCardIconFS.ReadDir("cardicons")

	var cache reviewCardIconCache
	notifier := &feishuNotifier{baseURL: srv.URL, appID: "cli_a", appSecret: "s", httpClient: srv.Client()}
	icons := cache.forNotifier(context.Background(), notifier)
	if len(icons) != len(entries) || int(uploads.Load()) != len(entries) {
		t.Fatalf("首次应上传全部 %d 个图标，实际 icons=%d uploads=%d", len(entries), len(icons), uploads.Load())
	}
	if again := cache.forNotifier(context.Background(), notifier); len(again) != len(entries) || int(uploads.Load()) != len(entries) {
		t.Fatalf("同一应用第二张卡不应重复上传，uploads=%d", uploads.Load())
	}

	failing.Store(1)
	other := &feishuNotifier{baseURL: srv.URL, appID: "cli_b", appSecret: "s", httpClient: srv.Client()}
	if got := cache.forNotifier(context.Background(), other); len(got) != 0 {
		t.Fatalf("上传失败时本卡不带图标，实际 %v", got)
	}
	failing.Store(0)
	before := uploads.Load()
	if got := cache.forNotifier(context.Background(), other); len(got) != 0 || uploads.Load() != before {
		t.Fatalf("失败后 %s 内不应重试，uploads %d→%d", reviewCardIconRetryAfter, before, uploads.Load())
	}
}

// TestReviewBatchCoverCropNeverShowsSquarePadding 上传给飞书的封面是补成正方形的
// 512×512（squareFeishuCoverPNG 上下浅灰补边）。缩略图裁切比例必须不窄于公众号封面
// 的 2.35:1，否则会把补边裁进来，图片上下各露一条灰边。
func TestReviewBatchCoverCropNeverShowsSquarePadding(t *testing.T) {
	body := cardJSON(t, buildReviewBatchCard([]publishJob{reviewCardTestJob(publishStagePublished)}))
	start := strings.Index(body, `"size":"`)
	if start < 0 {
		t.Fatalf("缩略图缺少裁切尺寸: %s", body)
	}
	size := body[start+len(`"size":"`):]
	size = size[:strings.Index(size, `"`)]
	var width, height int
	if _, err := fmt.Sscanf(size, "%dpx %dpx", &width, &height); err != nil || height == 0 {
		t.Fatalf("无法解析缩略图尺寸 %q: %v", size, err)
	}
	if ratio := float64(width) / float64(height); ratio < 2.35 {
		t.Fatalf("缩略图 %s 比例 %s 窄于 2.35:1，会露出正方形补边", size, strconv.FormatFloat(ratio, 'f', 2, 64))
	}
}
