package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestReviewThreadImageKeyParsesImageAndPost 话题里发的图：纯图片消息，或带图的富文本。
func TestReviewThreadImageKeyParsesImageAndPost(t *testing.T) {
	if got := reviewThreadImageKey("image", `{"image_key":"img_v3_a"}`); got != "img_v3_a" {
		t.Fatalf("图片消息: %q", got)
	}
	if got := reviewThreadImageKey("post", `{"title":"","content":[[{"tag":"text","text":"换这张"}],[{"tag":"img","image_key":"img_v3_b"}]]}`); got != "img_v3_b" {
		t.Fatalf("富文本: %q", got)
	}
	if got := reviewThreadImageKey("post", `{"zh_cn":{"title":"","content":[[{"tag":"img","image_key":"img_v3_c"}]]}}`); got != "img_v3_c" {
		t.Fatalf("带语言层的富文本: %q", got)
	}
	if got := reviewThreadImageKey("text", `{"text":"好的"}`); got != "" {
		t.Fatalf("文字消息不是图: %q", got)
	}
}

type threadReply struct{ parent, text string }

type reviewThreadHarness struct {
	env     *publishTestEnv
	fake    *fakeYunsuoAPI
	sizes   map[string][2]int
	images  map[string][]byte
	mu      sync.Mutex
	replies []threadReply
	sent    []sentReminder
}

func newReviewThreadHarness(t *testing.T) *reviewThreadHarness {
	t.Helper()
	setTestYunsuoConfig(t)
	t.Setenv(publishNotificationAppsEnv, "")
	t.Setenv("AUTH_SESSION_SECRET", "test-session-secret")
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1,ou_owner_1")
	t.Setenv("FEISHU_REVIEWER_ALLOW_CHAT_MEMBERS", "")
	h := &reviewThreadHarness{sizes: map[string][2]int{"https://cdn.example.com/a.jpg": {300, 200}}, images: map[string][]byte{}}
	stubYunsuoImageSize(t, h.sizes)
	h.env = newPublishTestEnv(t)
	h.env.pipeline.deliverySettings = newPublishDeliverySettingsStore(filepath.Join(t.TempDir(), "delivery.json"))
	if err := h.env.pipeline.deliverySettings.write(publishDeliverySettings{
		AppProfileID: "review-app", ChatIDs: []string{"oc_review"},
		DistributionOwners:    []publishDistributionOwner{{OpenID: "ou_owner_1", Name: "王编辑"}},
		DistributionOwnersApp: "review-app",
	}); err != nil {
		t.Fatal(err)
	}
	h.fake = newBothAccountsYunsuo()
	h.env.pipeline.yunsuo = func() (yunsuoAPI, error) { return h.fake, nil }
	h.env.pipeline.externalReminderSender = func(_ context.Context, _ publishJob, chatID, kind string, post map[string]any) (string, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.sent = append(h.sent, sentReminder{chatID: chatID, kind: kind, post: panelJSON(t, post)})
		return fmt.Sprintf("om_reminder_%d", len(h.sent)), nil
	}
	h.env.pipeline.reviewThreadImageFetcher = func(_ context.Context, chatID, messageID, imageKey string) ([]byte, error) {
		data, ok := h.images[imageKey]
		if !ok {
			return nil, fmt.Errorf("no image %s", imageKey)
		}
		return data, nil
	}
	h.env.pipeline.reviewThreadReplier = func(_ context.Context, chatID, parentID string, post map[string]any) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.replies = append(h.replies, threadReply{parent: parentID, text: panelJSON(t, post)})
		return nil
	}
	h.env.pipeline.externalCoverStore = func(_ context.Context, name string, body []byte) (string, error) {
		url := "https://cdn.example.com/uploaded/" + name
		h.sizes[url] = [2]int{1200, 800}
		return url, nil
	}
	return h
}

// publishedWaitingCover 一篇已发布原创，头条号卡在封面（可选也卡标题），审核卡在 om_card。
func (h *reviewThreadHarness) publishedWaitingCover(t *testing.T, key, title, cardMsg string) publishJob {
	t.Helper()
	job := createPublishedDistributionJobWithKey(t, h.env, key, "FBIF食品饮料创新")
	job, err := h.env.store.update(job.ID, func(current *publishJob) {
		current.Title = title
		current.ReviewDeliveryAppID = "review-app"
		current.ReviewDeliveryChatIDs = []string{"oc_review"}
		current.ReviewCardMsgIDs = map[string]string{"oc_review": cardMsg}
		current.ApprovedRiskVersion = publishReviewCardVersion(*current)
	})
	if err != nil {
		t.Fatal(err)
	}
	seedLivePublishedNews(t, h.env, foodTalksNewsDetail{
		ID: testDistributionNewsID, Title: title, CoverImg: job.CoverURL, Content: testDistributionLiveHTML,
		Summary: job.Summary, SourceName: "FBIF食品饮料创新", PublishStatus: foodTalksPublishStatusPublished,
	})
	if _, err := h.env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	got := mustJob(t, h.env, job.ID)
	if len(externalPendingCoverSlots(got, mustYunsuoConfig(t))) != 1 {
		t.Fatalf("头条号应在等封面：%+v", got.ExternalDistributionDrafts)
	}
	return got
}

func mustYunsuoConfig(t *testing.T) yunsuoConfig {
	t.Helper()
	cfg, err := loadYunsuoConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func (h *reviewThreadHarness) post(msg reviewThreadMessage) {
	if msg.ChatID == "" {
		msg.ChatID = "oc_review"
	}
	if msg.SenderType == "" {
		msg.SenderType = "user"
	}
	if msg.SenderOpenID == "" {
		msg.SenderOpenID = "ou_owner_1"
	}
	h.env.pipeline.handleReviewThreadMessage(context.Background(), "review-app", msg)
}

func (h *reviewThreadHarness) lastReply(t *testing.T) string {
	t.Helper()
	if len(h.replies) == 0 {
		t.Fatalf("没有话题回复")
	}
	return h.replies[len(h.replies)-1].text
}

// TestReviewThreadImageReplacesCover 负责人把图发到审核卡的话题里就换上封面（2026-10-12 用户：「传上去
// 之后直接绑定、自动同步过去了……就没必要再打开我们文章排版工具的链接了」）：图太小说清差多少；换上后
// 还差标题就说还差什么；同一条消息重投不重复处理；别的群、不在话题里的消息不理。
func TestReviewThreadImageReplacesCover(t *testing.T) {
	h := newReviewThreadHarness(t)
	job := h.publishedWaitingCover(t, "site:thread-title-cover", testLongToutiaoTitle, "om_card")
	h.images["img_small"] = testPNG(t, 300, 200)
	h.images["img_big"] = testPNG(t, 1200, 800)

	// 别的群：不理，也不算开通。
	h.post(reviewThreadMessage{ChatID: "oc_other", MessageID: "om_x", RootID: "om_card", MessageType: "image", Content: `{"image_key":"img_big"}`})
	if len(h.replies) != 0 || h.env.pipeline.reviewThreadImagesReady("review-app") {
		t.Fatalf("别的群的消息不处理：%+v", h.replies)
	}
	// 审核群里一条普通消息：说明事件已开通，之后提醒里才写「发图到话题」。
	h.post(reviewThreadMessage{MessageID: "om_chat", MessageType: "text", Content: `{"text":"收到"}`})
	if len(h.replies) != 0 || !h.env.pipeline.reviewThreadImagesReady("review-app") {
		t.Fatalf("收到审核群消息后应记为已开通：%+v", h.replies)
	}

	// 不是审核人：和卡片按钮一样没有权限，说清楚，不换。
	h.post(reviewThreadMessage{MessageID: "om_stranger", RootID: "om_card", SenderOpenID: "ou_stranger", MessageType: "image", Content: `{"image_key":"img_big"}`})
	if got := h.lastReply(t); !strings.Contains(got, "你没有发布操作权限，换不了封面") {
		t.Fatalf("非审核人应被拒：%+v", h.replies)
	}
	if draft, _ := externalDraftFor(mustJob(t, h.env, job.ID), testYunsuoToutiaoID); draft.CoverURL != "" {
		t.Fatalf("非审核人发的图不换：%+v", draft)
	}
	h.replies = nil

	h.post(reviewThreadMessage{MessageID: "om_small", RootID: "om_card", MessageType: "image", Content: `{"image_key":"img_small"}`})
	if got := h.lastReply(t); !strings.Contains(got, "这张图 300×200，头条号封面至少 452×352，请换一张大一点的。") || h.replies[0].parent != "om_small" {
		t.Fatalf("图太小应说清：%+v", h.replies)
	}

	h.post(reviewThreadMessage{MessageID: "om_big", RootID: "om_card", MessageType: "post",
		Content: `{"title":"","content":[[{"tag":"img","image_key":"img_big"}]]}`})
	got := h.lastReply(t)
	if !strings.Contains(got, "头条号封面已换成这张。") || !strings.Contains(got, "还差：头条号标题超出") || !strings.Contains(got, "「保存并同步至头条号」") {
		t.Fatalf("换上封面后应说还差标题：%s", got)
	}
	updated := mustJob(t, h.env, job.ID)
	draft, _ := externalDraftFor(updated, testYunsuoToutiaoID)
	if !strings.HasPrefix(draft.CoverURL, "https://cdn.example.com/uploaded/distribution-cover-") || draft.EditedBy != "ou_owner_1" {
		t.Fatalf("封面应换成转存后的图，并记下是谁换的：%+v", draft)
	}
	if _, ok := externalIssueFields(draft.Issues)[publishExternalIssueCover]; ok {
		t.Fatalf("封面问题应已解决：%+v", draft.Issues)
	}
	if len(h.fake.pushed) != 1 {
		t.Fatalf("标题还没改，头条号不推：%v", h.fake.pushed)
	}

	// 飞书重投同一条消息：不再处理、不再回复。
	before := len(h.replies)
	h.post(reviewThreadMessage{MessageID: "om_big", RootID: "om_card", MessageType: "post",
		Content: `{"title":"","content":[[{"tag":"img","image_key":"img_big"}]]}`})
	if len(h.replies) != before {
		t.Fatalf("重投不重复处理：%+v", h.replies)
	}
	// 不在话题里（直接发群里）的图不处理。
	h.post(reviewThreadMessage{MessageID: "om_loose", MessageType: "image", Content: `{"image_key":"img_big"}`})
	if len(h.replies) != before {
		t.Fatalf("不在话题里的图不处理：%+v", h.replies)
	}
}

// TestReviewThreadImageFinishesAndSyncs 只差封面：发图后直接推送，话题里由提醒收尾一句（不说两遍）。
func TestReviewThreadImageFinishesAndSyncs(t *testing.T) {
	h := newReviewThreadHarness(t)
	job := h.publishedWaitingCover(t, "site:thread-cover-only", "中国食品品牌如何用产品创新打开新市场", "om_card")
	if len(h.sent) != 1 || h.sent[0].kind != externalReminderKindOpen {
		t.Fatalf("应先在话题里 @ 负责人：%+v", h.sent)
	}
	h.images["img_big"] = testPNG(t, 1200, 800)
	h.post(reviewThreadMessage{MessageID: "om_big", RootID: "om_card", MessageType: "image", Content: `{"image_key":"img_big"}`})
	if len(h.replies) != 0 {
		t.Fatalf("都改好时由提醒收尾，不重复回复：%+v", h.replies)
	}
	last := h.sent[len(h.sent)-1]
	if last.kind != externalReminderKindClose || !strings.Contains(last.post, "都改好了，正在同步至头条号。") {
		t.Fatalf("应收尾一句：%+v", h.sent)
	}
	if fmt.Sprint(h.fake.pushed) != fmt.Sprint([][]string{{testYunsuoBaijiaID}, {testYunsuoToutiaoID}}) ||
		!strings.HasPrefix(h.fake.payload.Cover, "https://cdn.example.com/uploaded/distribution-cover-") {
		t.Fatalf("应马上推头条号，封面是发来的图：%v %+v", h.fake.pushed, h.fake.payload.Cover)
	}
	if lane := mustLane(t, mustJob(t, h.env, job.ID), testYunsuoToutiaoID); lane.RequestedBy != "ou_owner_1" {
		t.Fatalf("推送应记发图的人：%+v", lane)
	}
}

// TestReviewThreadImageAsksWhichArticle 聚合卡里两篇都在等封面：分不清就请人回复到对应提醒下面；
// 回复到某篇的提醒下面就是那篇。
func TestReviewThreadImageAsksWhichArticle(t *testing.T) {
	h := newReviewThreadHarness(t)
	first := h.publishedWaitingCover(t, "site:thread-a", "中国食品品牌如何用产品创新打开新市场", "om_card")
	second := h.publishedWaitingCover(t, "site:thread-b", "另一篇等封面的原创文章标题", "om_card")
	h.images["img_big"] = testPNG(t, 1200, 800)
	h.post(reviewThreadMessage{MessageID: "om_1", RootID: "om_card", MessageType: "image", Content: `{"image_key":"img_big"}`})
	if got := h.lastReply(t); !strings.Contains(got, fmt.Sprintf("这个话题里有 2 篇在等封面（#%d、#%d）", first.ID, second.ID)) {
		t.Fatalf("分不清时应请人回复到对应提醒：%s", got)
	}
	opening := mustJob(t, h.env, second.ID).ExternalDistributionReminder.MessageIDs["oc_review"]
	h.post(reviewThreadMessage{MessageID: "om_2", RootID: "om_card", ParentID: opening, MessageType: "image", Content: `{"image_key":"img_big"}`})
	if draft, _ := externalDraftFor(mustJob(t, h.env, second.ID), testYunsuoToutiaoID); draft.CoverURL == "" {
		t.Fatalf("回复到第二篇提醒下的图应换到第二篇：%+v", draft)
	}
	if draft, _ := externalDraftFor(mustJob(t, h.env, first.ID), testYunsuoToutiaoID); draft.CoverURL != "" {
		t.Fatalf("第一篇不受影响：%+v", draft)
	}
}
