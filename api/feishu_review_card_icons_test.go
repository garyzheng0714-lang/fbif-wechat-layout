package main

// feishu_review_card_icons_test.go — 审核聚合卡按钮图标与缩略图裁切守护。

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
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
	// 只表示状态的按钮已去掉（2026-10-10），这里覆盖发布、强制发布、检查未完成、下线、
	// 重新同步（含已分发置灰）、重新发布这几类可见按钮。
	if len(buttons) < 6 {
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
	if !strings.Contains(body, "img_send-inverse") {
		t.Fatalf("蓝底「发布」的图标应为白色: %s", body)
	}
	pending.RiskLevel = publishRiskBlock
	if body := cardJSON(t, buildReviewBatchCardWithIcons([]publishJob{pending}, icons)); !strings.Contains(body, "img_send-danger") {
		t.Fatalf("强制发布是红色按钮，图标应为红色: %s", body)
	}
	pending.RiskLevel = ""
	if body := cardJSON(t, buildReviewBatchCardWithIcons([]publishJob{pending}, icons)); !strings.Contains(body, "img_circle-alert-disabled") {
		t.Fatalf("「检查未完成」是不可点按钮，图标应为浅灰: %s", body)
	}
	published := reviewCardTestJob(publishStagePublished)
	published.FoodTalksNewsID = 7318
	published.PublicationAction = publishPublicationActionPublish
	published.PublishedAt = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	published.UnpublishPolicySet = true
	published.UnpublishWindowHours = 8
	published.UnpublishWindowUntil = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	body = cardJSON(t, buildReviewBatchCardWithIcons([]publishJob{published}, icons))
	for _, want := range []string{"img_refresh-cw-default", "img_eye-off-danger"} {
		if !strings.Contains(body, want) {
			t.Fatalf("已发布卡应有 %s（普通按钮中灰、下线红色）: %s", want, body)
		}
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

// TestReviewBatchCoverPrefersUnpaddedThumb 封面是 88×88 方图，优先用 16:9 无补边图
// （ReviewThumbImgKey）居中裁成正方形。还没有这张图的旧稿退回补成正方形的 ReviewCoverImgKey：
// 方图进方框不裁切，照片完整可见（只是上下带浅灰补边）。
func TestReviewBatchCoverPrefersUnpaddedThumb(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	if cover := reviewBatchCover(job); cover["img_key"] != job.ReviewCoverImgKey || cover["size"] != reviewBatchCoverSize {
		t.Fatalf("无小图时整张显示方形封面: %#v", cover)
	}
	job.ReviewThumbImgKey = "img_v3_thumb"
	if cover := reviewBatchCover(job); cover["img_key"] != "img_v3_thumb" || cover["scale_type"] != "crop_center" || cover["size"] != reviewBatchCoverSize {
		t.Fatalf("有 16:9 小图时用小图裁成方形: %#v", cover)
	}
	job.ReviewThumbImgKey, job.ReviewCoverImgKey = "", ""
	if cover := reviewBatchCover(job); cover != nil {
		t.Fatalf("没有任何封面时不出图: %#v", cover)
	}
}

// TestThumbFeishuCoverIsSixteenByNine 小图居中裁成 16:9、480×270，横图竖图都一样。
func TestThumbFeishuCoverIsSixteenByNine(t *testing.T) {
	for _, size := range [][2]int{{874, 372}, {600, 900}, {1280, 720}} {
		src := testCoverPNG(t, size[0], size[1], color.RGBA{R: 30, G: 120, B: 200, A: 255})
		out, err := thumbFeishuCoverJPEG(src)
		if err != nil {
			t.Fatalf("%v 生成小图失败: %v", size, err)
		}
		img, format, err := image.Decode(bytes.NewReader(out))
		if err != nil || format != "jpeg" || img.Bounds().Dx() != feishuThumbWidth || img.Bounds().Dy() != feishuThumbHeight {
			t.Fatalf("%v 小图应为 %dx%d JPEG，实际 %v %v %v", size, feishuThumbWidth, feishuThumbHeight, format, img.Bounds(), err)
		}
	}
}

// TestPrepareReviewThumbsUploadsOnceAndBacksOff 建聚合卡前给缺小图的稿子补传并落库；
// 已有小图、没有封面的跳过；上传失败的稿子短时间内不重试，卡片照常发出。
func TestPrepareReviewThumbsUploadsOnceAndBacksOff(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	job, err = env.store.update(job.ID, func(current *publishJob) { current.CoverURL = "https://img.example.com/cover.jpg" })
	if err != nil {
		t.Fatalf("set cover: %v", err)
	}
	var calls int
	fail := true
	env.pipeline.reviewThumbUploader = func(ctx context.Context, job publishJob) (string, error) {
		calls++
		if fail {
			return "", fmt.Errorf("飞书图片接口不可用")
		}
		return "img_v3_thumb", nil
	}
	got := env.pipeline.prepareReviewThumbs(context.Background(), []publishJob{job})
	if calls != 1 || got[0].ReviewThumbImgKey != "" {
		t.Fatalf("上传失败时不落 key，calls=%d key=%q", calls, got[0].ReviewThumbImgKey)
	}
	fail = false
	env.pipeline.prepareReviewThumbs(context.Background(), []publishJob{job})
	if calls != 1 {
		t.Fatalf("失败后 %s 内不应重试，calls=%d", reviewThumbRetryAfter, calls)
	}
	env.pipeline.reviewThumbFailedAt = nil
	got = env.pipeline.prepareReviewThumbs(context.Background(), []publishJob{job})
	stored, _ := env.store.byID(job.ID)
	if calls != 2 || got[0].ReviewThumbImgKey != "img_v3_thumb" || stored.ReviewThumbImgKey != "img_v3_thumb" {
		t.Fatalf("应上传并落库小图，calls=%d got=%q stored=%q", calls, got[0].ReviewThumbImgKey, stored.ReviewThumbImgKey)
	}
	env.pipeline.prepareReviewThumbs(context.Background(), got)
	if calls != 2 {
		t.Fatalf("已有小图不应重传，calls=%d", calls)
	}
}
