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
	// 一篇一张卡：十几篇挤一张会超飞书的组件上限，那时整张不带图标（TestReviewBatchCardStaysWithinFeishuElementLimit）。
	var buttons []map[string]any
	for _, job := range jobs {
		card := buildReviewBatchCardWithIcons([]publishJob{job}, icons)
		if body := cardJSON(t, card); strings.Contains(body, reviewCardIconField) {
			t.Fatalf("内部图标标记不得留在卡里: %s", body)
		}
		reviewCardButtons(card, &buttons)
	}
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
	for _, want := range []string{"img_send-inverse", "img_wechat-favicon"} {
		if !strings.Contains(body, want) {
			t.Fatalf("待审核卡应有 %s（蓝底「发布」白色图标，原文用微信网站图标）: %s", want, body)
		}
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

// TestReviewBatchCoverPrefersSquareThumb 封面是随栏宽缩放的方图：优先用
// 审核卡专门上传的方图（ReviewThumbImgKey），没有时退回补成正方形的 ReviewCoverImgKey；两者都是
// 方图，进方框不裁切。
func TestReviewBatchCoverPrefersSquareThumb(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	if cover := reviewBatchCover(job); cover["img_key"] != job.ReviewCoverImgKey || cover["scale_type"] != "fit_horizontal" {
		t.Fatalf("无方图时显示补成方形的封面: %#v", cover)
	}
	job.ReviewThumbImgKey = "img_v3_square"
	if cover := reviewBatchCover(job); cover["img_key"] != "img_v3_square" || cover["scale_type"] != "fit_horizontal" {
		t.Fatalf("有方图时用方图: %#v", cover)
	}
	job.ReviewThumbImgKey, job.ReviewCoverImgKey = "", ""
	if cover := reviewBatchCover(job); cover != nil {
		t.Fatalf("没有任何封面时不出图: %#v", cover)
	}
}

// TestThumbFeishuCoverIsSquare 方图居中裁成正方形、480×480，横图竖图都一样；本身是方图的
// （公众号编辑选的 1:1 封面）只缩放不裁。
func TestThumbFeishuCoverIsSquare(t *testing.T) {
	for _, size := range [][2]int{{874, 372}, {600, 900}, {720, 720}} {
		src := testCoverPNG(t, size[0], size[1], color.RGBA{R: 30, G: 120, B: 200, A: 255})
		out, err := thumbFeishuCoverJPEG(src)
		if err != nil {
			t.Fatalf("%v 生成方图失败: %v", size, err)
		}
		img, format, err := image.Decode(bytes.NewReader(out))
		if err != nil || format != "jpeg" || img.Bounds().Dx() != feishuThumbSize || img.Bounds().Dy() != feishuThumbSize {
			t.Fatalf("%v 方图应为 %d² JPEG，实际 %v %v %v", size, feishuThumbSize, format, img.Bounds(), err)
		}
	}
}

// TestReviewSquareCoverPrefersEditorChoice 方图来源优先是公众号原文页的 cdn_url_1_1（编辑选的
// 1:1 封面）；http 旧链接按 https 取（http 只回「未知错误」页）；取不到退回横版封面。
func TestReviewSquareCoverPrefersEditorChoice(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	job.SourceURL = "http://mp.weixin.qq.com/s?__biz=MzA5&mid=1&idx=1&sn=abc"
	job.CoverURL = "https://oss.example.com/wide.jpg"
	var fetched string
	page := `var msg_cdn_url = "https://mmbiz.qpic.cn/wide/0?wx_fmt=jpeg";
var cdn_url_1_1 = "https://mmbiz.qpic.cn/sz_mmbiz_jpg/square/0?wx_fmt=jpeg";`
	got := reviewSquareCoverURL(context.Background(), job, func(_ context.Context, raw string) (string, string, error) {
		fetched = raw
		return page, raw, nil
	})
	if got != "https://mmbiz.qpic.cn/sz_mmbiz_jpg/square/0?wx_fmt=jpeg" || !strings.HasPrefix(fetched, "https://mp.weixin.qq.com/") {
		t.Fatalf("应按 https 取原文页并用编辑选的方图，fetched=%q got=%q", fetched, got)
	}
	got = reviewSquareCoverURL(context.Background(), job, func(context.Context, string) (string, string, error) {
		return "", "", fmt.Errorf("环境异常")
	})
	if got != job.CoverURL {
		t.Fatalf("取不到原文页时退回横版封面，实际 %q", got)
	}
	job.SourceURL = "https://www.example.com/news/1"
	got = reviewSquareCoverURL(context.Background(), job, func(context.Context, string) (string, string, error) {
		t.Fatalf("非公众号来源不应去抓页面")
		return "", "", nil
	})
	if got != job.CoverURL {
		t.Fatalf("非公众号来源直接用横版封面，实际 %q", got)
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

// TestReviewBatchCardStaysWithinFeishuElementLimit 飞书一张卡最多 200 个元素或组件（2026-11-10 #326–#328：一天两篇
// 原创、四个平台，带图标 209 个，更新被拒、卡片停在接腾讯号之前的样子）。图标是点缀：超了整张不带图标，没超照常带；
// 只有名字一行的平台行不再包竖排分栏（每行省 2 个，样子不变）。
func TestReviewBatchCardStaysWithinFeishuElementLimit(t *testing.T) {
	setTestExternalAccounts(t,
		testArticleAccount(testToutiaoAccountID, "toutiao", false), testArticleAccount(testBaijiaAccountID, "baijiahao", false),
		testArticleAccount("recTestZhihu0001", "zhihu", false), testArticleAccount("recTestTencent01", "tencent", false))
	icons := reviewCardTestIconKeys(t)
	original := func(id int64) publishJob {
		job := reviewCardTestJob(publishStagePublished)
		job.ID, job.FoodTalksNewsID, job.PublicationAction = id, 7000+id, publishPublicationActionPublish
		return job
	}

	jobs := []publishJob{original(1)}
	one := buildReviewBatchCardWithIcons(jobs, icons)
	if body := cardJSON(t, one); !strings.Contains(body, `"img_key":"img_platform-tencent"`) || !strings.Contains(body, "custom_icon") {
		t.Fatalf("没超上限时照常带平台图标和按钮图标: %s", body)
	}
	elements := one["body"].(map[string]any)["elements"].([]any)
	box := elements[len(elements)-1].(map[string]any)
	rows := 0
	for _, row := range box["elements"].([]any) {
		body := cardJSON(t, row.(map[string]any))
		if !strings.Contains(body, "同步至") {
			continue // 几篇一张卡时块里先是「#编号 标题」表头
		}
		rows++
		if strings.Count(body, `"tag":"column_set"`) != 1 {
			t.Fatalf("只有名字一行的平台行不再包竖排分栏: %s", body)
		}
	}
	if rows != 4 {
		t.Fatalf("四个平台应各一行: %s", cardJSON(t, box))
	}

	for len(jobs) < 20 && feishuCardElementCount(composeReviewBatchCard(jobs, icons, false)) <= feishuCardElementLimit {
		jobs = append(jobs, original(int64(len(jobs)+1)))
	}
	if feishuCardElementCount(composeReviewBatchCard(jobs, nil, false)) > feishuCardElementLimit {
		t.Fatalf("造的卡不带图标也超了，测不到去图标这一步（%d 篇）", len(jobs))
	}
	card := buildReviewBatchCardWithIcons(jobs, icons)
	body := cardJSON(t, card)
	if n := feishuCardElementCount(card); n > feishuCardElementLimit || strings.Contains(body, "custom_icon") ||
		strings.Contains(body, `"img_key":"img_platform-`) || strings.Contains(body, reviewCardIconField) {
		t.Fatalf("带图标超上限时整张去掉图标、不留标记（%d 篇，%d 个组件）: %s", len(jobs), n, body)
	}
	if !strings.Contains(body, "同步至腾讯号") {
		t.Fatalf("去掉图标不能去掉按钮: %s", body)
	}
}

// TestReviewBatchCardCompactsDistributionWhenStillOverLimit 不带图标还超（2026-11-14 #328：不带图标 193 个，
// 再多一个平台或一篇原创就超）：分发区每个平台收成一行字、1 个组件，结果、链接、原因都在；「同步至X」
// 「去修改」链到侧边栏改稿页，卡上不放按钮和表单；侧边栏改不了的（失败了、草稿已存进平台）照旧带按钮。
// 平常一张卡最多 4 篇（7–11 月 127 天的最多值）：4 篇原创各 6 个平台、都待修改也放得下。
func TestReviewBatchCardCompactsDistributionWhenStillOverLimit(t *testing.T) {
	t.Setenv("AUTH_SESSION_SECRET", "test-session-secret")
	accounts := []externalAccount{
		testArticleAccount(testToutiaoAccountID, "toutiao", false), testArticleAccount(testBaijiaAccountID, "baijiahao", false),
		testArticleAccount("recTestZhihu0001", "zhihu", false), testArticleAccount("recTestTencent01", "tencent", false),
		testArticleAccount("recTestDayu00001", "dayu", false), testArticleAccount("recTestLinkedin1", "linkedin", false),
	}
	setTestExternalAccounts(t, accounts...)
	icons := reviewCardTestIconKeys(t)
	long := "这是一条明显超过了头条号三十个字上限的文章标题，需要编辑缩短以后再同步"
	original := func(id int64) publishJob {
		job := reviewCardTestJob(publishStagePublished)
		job.ID, job.FoodTalksNewsID, job.PublicationAction = id, 7000+id, publishPublicationActionPublish
		for _, account := range accounts {
			job.ExternalDistributionDrafts = append(job.ExternalDistributionDrafts, publishExternalDraft{
				AccountID: account.ID,
				Issues:    []publishExternalIssue{{Field: publishExternalIssueTitle, Current: long, Length: runeLen(long), Min: 2, Max: 30}},
			})
		}
		return job
	}
	jobs := []publishJob{original(1), original(2), original(3), original(4)}
	// 一篇有一个平台失败了、草稿已存进平台：侧边栏改不了，只能按原样重推。
	jobs[0].ExternalDistributionDrafts = jobs[0].ExternalDistributionDrafts[1:]
	jobs[0].ExternalDistributionLanes = []publishExternalLane{{
		AccountID: testToutiaoAccountID, Name: "头条号", Driver: externalDriverRobot, State: publishExternalDistributionFailed,
		ContentPK: "pk-1", IntentKey: "job-1.toutiao", DraftURL: "https://mp.toutiao.com/profile_v4/graphic/publish?pgc_id=1", Error: "发布按钮没点上",
		IntentAt: "2026-11-10T01:00:00Z", CompletedAt: "2026-11-10T01:05:00Z",
	}}
	if feishuCardElementCount(composeReviewBatchCard(jobs, nil, false)) <= feishuCardElementLimit || !reviewBatchCardCompact(jobs) {
		t.Fatalf("造的卡不带图标没超，测不到收成一行字这一步")
	}
	card := buildReviewBatchCardWithIcons(jobs, icons)
	body := panelJSON(t, card)
	if n := feishuCardElementCount(card); n > feishuCardElementLimit {
		t.Fatalf("收成一行字后仍有 %d 个组件", n)
	}
	if strings.Contains(body, `"tag":"form"`) || strings.Contains(body, `"tag":"input"`) {
		t.Fatalf("收成一行字的卡不放改稿表单: %s", body)
	}
	if got := strings.Count(body, `"action":"`+feishuCardActionDistributeExternal+`"`); got != 1 {
		t.Fatalf("只有侧边栏改不了的那一行留按钮，现在 %d 个: %s", got, body)
	}
	for _, want := range []string{"**知乎号**　<font color='orange'>待修改</font>", "[去修改](https://applink.feishu.cn/", "重新同步至头条号"} {
		if !strings.Contains(body, want) {
			t.Fatalf("收成一行字也要有 %q: %s", want, body)
		}
	}

	// 只有一篇时照常是按钮和表单。
	single := buildReviewBatchCardWithIcons(jobs[1:2], icons)
	if reviewBatchCardCompact(jobs[1:2]) || !strings.Contains(cardJSON(t, single), `"tag":"form"`) {
		t.Fatalf("放得下就不收: %s", cardJSON(t, single))
	}

	// 收成一行字的卡上没有改稿表单：待修改提醒指向侧边栏改稿页。
	env := newPublishTestEnv(t)
	for _, job := range jobs {
		created, _, err := env.store.create(fmt.Sprintf("site:https://mp.weixin.qq.com/s/compact-%d", job.ID), "https://mp.weixin.qq.com/s/compact")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.store.update(created.ID, func(current *publishJob) {
			id := current.ID
			*current = job
			current.ID = id
			current.CreatedAt = jobs[0].CreatedAt
			current.ReviewCardMsgIDs = map[string]string{"oc_review": fmt.Sprintf("om_%d", id)}
		}); err != nil {
			t.Fatal(err)
		}
	}
	env.pipeline.reviewCardSentAt = func(context.Context, publishJob, string, string) (time.Time, error) { return time.Now(), nil }
	stored, _ := env.store.byID(1)
	if env.pipeline.externalReminderCardEditable(context.Background(), stored, "oc_review") {
		t.Fatal("卡上收成了一行字，提醒不能叫人在卡片里改")
	}
}
