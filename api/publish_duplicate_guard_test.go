package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type dupSearchStub struct {
	newsTagStub
	items []foodTalksNewsListItem
	err   error
}

func (s *dupSearchStub) SearchNewsByTitle(context.Context, string) ([]foodTalksNewsListItem, error) {
	return s.items, s.err
}

func (s *dupSearchStub) SearchCompanies(context.Context, string) ([]foodTalksCompany, error) {
	return nil, nil
}

// 2026-08-14 实案：#78 的草稿（62760）与运营手工发布的 62773 同题并存，
// 流水线 source_key 幂等看不见手工稿，审核一点通过就会出现两篇重复文章。
func TestFindPublishedSiteDuplicate(t *testing.T) {
	stub := &dupSearchStub{items: []foodTalksNewsListItem{
		{ID: 62760, Title: "喜茶海外走红，大家都误解了“拙趣”", PublishStatus: 0},                     // 自己的草稿
		{ID: 62773, Title: " 喜茶海外走红，大家都误解了“拙趣” ", PublishStatus: 1, SourceName: "数英"}, // 手工发布（含空白差异）
		{ID: 62659, Title: "喜茶海外社媒设计，原来还能这样玩！", PublishStatus: 1},                     // 同关键词不同题
	}}
	p := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return stub, nil }}
	job := publishJob{ID: 78, Title: "喜茶海外走红，大家都误解了“拙趣”", FoodTalksNewsID: 62760}
	dup, found, err := p.findPublishedSiteDuplicate(context.Background(), job)
	if err != nil || !found || dup.ID != 62773 {
		t.Fatalf("应命中手工发布的 62773：dup=%+v found=%v err=%v", dup, found, err)
	}
	// 自身锚点是唯一同题已发布稿时不算重复（正常发布后的自身状态）
	self := publishJob{Title: "喜茶海外走红，大家都误解了“拙趣”", FoodTalksNewsID: 62773}
	if _, found, _ := p.findPublishedSiteDuplicate(context.Background(), self); found {
		t.Fatalf("自身锚点不得判为重复")
	}
}

func TestSitePublishDuplicateGateFailsClosed(t *testing.T) {
	hit := &dupSearchStub{items: []foodTalksNewsListItem{{ID: 62773, Title: "T", PublishStatus: 1, SourceName: "数英"}}}
	p := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return hit, nil }}
	err := p.sitePublishDuplicateGate(context.Background(), publishJob{Title: "T", FoodTalksNewsID: 62760})
	if err == nil || !strings.Contains(err.Error(), "62773") || !strings.Contains(err.Error(), "手工发布优先") {
		t.Fatalf("命中同题已发布稿必须拦下并说明原因：%v", err)
	}
	// 查询失败同样 fail closed（红线 26 口径：不确定不公开），错误可重试
	broken := &dupSearchStub{err: errors.New("cms down")}
	p2 := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return broken, nil }}
	if err := p2.sitePublishDuplicateGate(context.Background(), publishJob{Title: "T"}); err == nil {
		t.Fatalf("同题检查失败必须 fail closed")
	}
	// 无重复放行
	clean := &dupSearchStub{}
	p3 := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return clean, nil }}
	if err := p3.sitePublishDuplicateGate(context.Background(), publishJob{Title: "T", FoodTalksNewsID: 62760}); err != nil {
		t.Fatalf("无重复应放行：%v", err)
	}
}

func TestReviewPendingCardShowsDuplicateWarning(t *testing.T) {
	job := publishJob{ID: 78, Title: "标题", RiskLevel: "warning", SiteDuplicateNewsID: 62773}
	raw, err := json.Marshal(buildReviewPendingCard(job, ""))
	if err != nil {
		t.Fatal(err)
	}
	card := string(raw)
	if !strings.Contains(card, "同题防重") || !strings.Contains(card, "62773") || !strings.Contains(card, "手工发布优先") {
		t.Fatalf("卡片应标红同题提醒：%s", card[:400])
	}
	job.SiteDuplicateNewsID = 0
	raw, _ = json.Marshal(buildReviewPendingCard(job, ""))
	if strings.Contains(string(raw), "同题防重") {
		t.Fatalf("无重复时不得出现提醒")
	}
}

// 端到端：走真实 sendReviewPendingNotification（真实 store），断言同题标记被持久化。
func TestSendReviewPendingNotificationPersistsDuplicateFlag(t *testing.T) {
	dir := t.TempDir()
	store := newPublishJobStore(dir + "/jobs.json")
	job, _, err := store.create("site:dup", "https://example.test/dup")
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageAwaitingReview
		current.Title = "喜茶海外走红，大家都误解了“拙趣”"
		current.FoodTalksNewsID = 62760
	})
	if err != nil {
		t.Fatal(err)
	}
	stub := &dupSearchStub{items: []foodTalksNewsListItem{{ID: 62773, Title: "喜茶海外走红，大家都误解了“拙趣”", PublishStatus: 1, SourceName: "数英"}}}
	sentCards := 0
	p := &publishPipeline{
		store:     store,
		now:       time.Now,
		foodTalks: func() (foodTalksAPI, error) { return stub, nil },
		notifyCard: func(ctx context.Context, card map[string]any) error {
			sentCards++
			raw, _ := json.Marshal(card)
			if !strings.Contains(string(raw), "同题防重") {
				t.Errorf("发出的卡片缺少同题提醒")
			}
			return nil
		},
	}
	if err := p.sendReviewPendingNotification(context.Background(), job); err != nil {
		t.Fatalf("send: %v", err)
	}
	stored, err := store.byID(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SiteDuplicateNewsID != 62773 {
		t.Fatalf("同题标记未持久化：%d", stored.SiteDuplicateNewsID)
	}
	if sentCards != 1 {
		t.Fatalf("卡片应发出一次，实际 %d", sentCards)
	}
}
