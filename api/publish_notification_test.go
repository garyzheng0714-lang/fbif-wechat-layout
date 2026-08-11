package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func seedPendingReviewNotification(t *testing.T, env *publishTestEnv, state string) publishJob {
	t.Helper()
	job, _, err := env.store.create("site:notification:"+state, testPublishSourceURL)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	job, err = env.store.update(job.ID, func(j *publishJob) {
		j.Stage = publishStageAwaitingReview
		j.Title = "通知恢复测试"
		j.SourceName = "测试公众号"
		j.ContentHTML = "<p>正文</p>"
		j.CoverURL = "https://oss.example.com/a.jpg"
		j.FoodTalksNewsID = 7240
		j.RiskLevel = publishRiskWarning
		enqueuePublishNotification(j, publishNotificationReviewPending)
		j.NotificationState = state
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return job
}

func TestPublishTerminalNotificationsPreferDoneCardAndFallbackText(t *testing.T) {
	for _, tc := range []struct {
		name, kind, stage, label string
	}{
		{name: "published", kind: publishNotificationPublished, stage: publishStagePublished, label: "已发布"},
		{name: "rejected", kind: publishNotificationRejected, stage: publishStageRejected, label: "已驳回"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newPublishTestEnv(t)
			job, _, err := env.store.create("site:terminal-notification:"+tc.name, testPublishSourceURL)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			job, err = env.store.update(job.ID, func(j *publishJob) {
				j.Stage = tc.stage
				j.NotificationKind = tc.kind
				j.Title = "终态通知测试"
				j.FoodTalksNewsID = 7240
				j.ArticleURL = "https://test.foodtalks.cn/news/7240"
				j.ReviewAction = publishReviewApprove
				j.ReviewedBy = "ou_reviewer_1"
				if tc.stage == publishStageRejected {
					j.ReviewAction = publishReviewReject
				}
			})
			if err != nil {
				t.Fatalf("seed: %v", err)
			}
			var card map[string]any
			env.pipeline.notifyCard = func(_ context.Context, mapCard map[string]any) error {
				card = mapCard
				return nil
			}
			env.notified = nil
			if err := env.pipeline.sendPublishNotification(context.Background(), job); err != nil {
				t.Fatalf("send terminal card: %v", err)
			}
			raw, _ := json.Marshal(card)
			if card == nil || !strings.Contains(string(raw), tc.label) {
				t.Fatalf("终态通知应优先发 %s 卡，实际 %s", tc.label, raw)
			}
			if strings.Contains(string(raw), `"action":"approve"`) || strings.Contains(string(raw), `"action":"reject"`) {
				t.Fatalf("终态卡不得残留审核动作：%s", raw)
			}
			if len(env.notified) != 0 {
				t.Fatalf("终态卡成功后不应重复发文本：%v", env.notified)
			}

			env.pipeline.notifyCard = func(context.Context, map[string]any) error {
				return errors.New("card unavailable")
			}
			if err := env.pipeline.sendPublishNotification(context.Background(), job); err != nil {
				t.Fatalf("card failure should fallback text: %v", err)
			}
			if len(env.notified) != 1 || !strings.Contains(env.notified[0], tc.label) {
				t.Fatalf("卡片失败应降级 %s 文本，实际 %v", tc.label, env.notified)
			}
		})
	}
}

func TestPublishTerminalNotificationsUpdateExistingCardInPlace(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.store.create("site:terminal-notification:update-in-place", testPublishSourceURL)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	job, err = env.store.update(job.ID, func(j *publishJob) {
		j.Stage = publishStagePublished
		j.NotificationKind = publishNotificationPublished
		j.ReviewCardMsgID = "om_review_8"
		j.Title = "原位更新测试"
		j.FoodTalksNewsID = 7240
		j.ArticleURL = "https://test.foodtalks.cn/news/7240"
		j.ReviewAction = publishReviewApprove
		j.ReviewedBy = "ou_reviewer_1"
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	var updatedID string
	var updatedCard map[string]any
	env.pipeline.updateCard = func(_ context.Context, messageID string, card map[string]any) error {
		updatedID = messageID
		updatedCard = card
		return nil
	}
	env.pipeline.notifyCard = func(context.Context, map[string]any) error {
		t.Fatal("原位更新成功后不应再发送第二张终态卡")
		return nil
	}
	env.notified = nil
	if err := env.pipeline.sendPublishNotification(context.Background(), job); err != nil {
		t.Fatalf("send terminal card in place: %v", err)
	}
	raw, _ := json.Marshal(updatedCard)
	if updatedID != job.ReviewCardMsgID || !strings.Contains(string(raw), "已发布") {
		t.Fatalf("应使用原 message_id 更新已发布卡：id=%q card=%s", updatedID, raw)
	}
	if len(env.notified) != 0 {
		t.Fatalf("原位更新成功后不应再发文本：%v", env.notified)
	}
}

func TestPublishPendingNotificationStoresAndReusesCardMessageID(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.store.create("site:notification:same-card", testPublishSourceURL)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	job, err = env.store.update(job.ID, func(j *publishJob) {
		j.Stage = publishStageAwaitingReview
		j.Title = "审核卡原位更新测试"
		j.SourceName = "测试公众号"
		j.FoodTalksNewsID = 7240
		j.RiskLevel = publishRiskPass
		enqueuePublishNotification(j, publishNotificationReviewPending)
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	var sends int
	env.pipeline.notifyCardWithID = func(_ context.Context, _ map[string]any) (string, error) {
		sends++
		return "om_review_same_card", nil
	}
	if err := env.pipeline.sendReviewPendingNotification(context.Background(), job); err != nil {
		t.Fatalf("send pending card: %v", err)
	}
	stored, err := env.store.byID(job.ID)
	if err != nil || stored.ReviewCardMsgID != "om_review_same_card" {
		t.Fatalf("审核卡 message_id 必须持久化：job=%+v err=%v", stored, err)
	}

	var updatedID string
	env.pipeline.updateCard = func(_ context.Context, messageID string, mapCard map[string]any) error {
		updatedID = messageID
		return nil
	}
	current, _ := env.store.byID(job.ID)
	if err := env.pipeline.sendReviewPendingNotification(context.Background(), current); err != nil {
		t.Fatalf("reuse pending card: %v", err)
	}
	if sends != 1 || updatedID != "om_review_same_card" {
		t.Fatalf("已有审核卡应直接复用：sends=%d updatedID=%q", sends, updatedID)
	}
}

func TestPublishNotificationRecoverySendsPendingAndAbandonedSending(t *testing.T) {
	for _, state := range []string{publishNotificationStatePending, publishNotificationStateSending} {
		t.Run(state, func(t *testing.T) {
			env := newPublishTestEnv(t)
			job := seedPendingReviewNotification(t, env, state)
			env.notified = nil
			env.pipeline.retryNotification = func(time.Duration, func()) {}
			env.pipeline.recoverJobs()
			got, _ := env.store.byID(job.ID)
			if got.NotificationKind != "" || got.NotificationState != "" {
				t.Fatalf("恢复发送成功后 outbox 应清空：kind=%q state=%q error=%q", got.NotificationKind, got.NotificationState, got.NotificationError)
			}
			if len(env.notified) != 1 {
				t.Fatalf("恢复应恰发送一次，实际 %v", env.notified)
			}
		})
	}
}

func TestPublishNotificationClaimPreventsConcurrentDuplicateSend(t *testing.T) {
	env := newPublishTestEnv(t)
	job := seedPendingReviewNotification(t, env, publishNotificationStatePending)
	env.notified = nil
	env.pipeline.retryNotification = func(time.Duration, func()) {}
	var sends atomic.Int32
	release := make(chan struct{})
	env.pipeline.notify = func(context.Context, string) error {
		sends.Add(1)
		<-release
		return nil
	}
	const n = 24
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			env.pipeline.dispatchPublishNotification(job.ID)
		}()
	}
	for sends.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()
	if sends.Load() != 1 {
		t.Fatalf("并发 drain 只能一个 claim 成功，实际发送 %d 次", sends.Load())
	}
}

func TestPublishNotificationFailurePersistsAndRetries(t *testing.T) {
	env := newPublishTestEnv(t)
	job := seedPendingReviewNotification(t, env, publishNotificationStatePending)
	var calls atomic.Int32
	env.pipeline.notify = func(context.Context, string) error {
		if calls.Add(1) == 1 {
			return errors.New("feishu unavailable")
		}
		return nil
	}
	var retry func()
	env.pipeline.retryNotification = func(_ time.Duration, f func()) { retry = f }
	env.pipeline.dispatchPublishNotification(job.ID)
	failed, _ := env.store.byID(job.ID)
	if failed.NotificationState != publishNotificationStatePending || failed.NotificationError == "" || retry == nil {
		t.Fatalf("失败后必须持久 pending+error 并排队重试：%+v retry=%v", failed, retry != nil)
	}
	retry()
	got, _ := env.store.byID(job.ID)
	if calls.Load() != 2 || got.NotificationKind != "" || got.NotificationState != "" {
		t.Fatalf("重试成功应清 outbox：calls=%d kind=%q state=%q", calls.Load(), got.NotificationKind, got.NotificationState)
	}
}

func TestPublishNotificationMovesToDeadLetterAfterMaxAttempts(t *testing.T) {
	env := newPublishTestEnv(t)
	job := seedPendingReviewNotification(t, env, publishNotificationStatePending)
	if _, err := env.store.update(job.ID, func(j *publishJob) {
		j.NotificationAttempts = publishNotificationMaxAttempts - 1
	}); err != nil {
		t.Fatal(err)
	}
	env.pipeline.notify = func(context.Context, string) error { return errors.New("feishu remains unavailable") }
	retried := false
	env.pipeline.retryNotification = func(time.Duration, func()) { retried = true }

	env.pipeline.dispatchPublishNotification(job.ID)

	got, _ := env.store.byID(job.ID)
	if got.NotificationState != publishNotificationStateDeadLetter || got.NotificationAttempts != publishNotificationMaxAttempts {
		t.Fatalf("dead letter state = %+v", got)
	}
	if retried {
		t.Fatal("dead-letter notification must not schedule another automatic retry")
	}
}

func TestPublishNotificationDeadLetterRequiresExplicitReplay(t *testing.T) {
	env := newPublishTestEnv(t)
	job := seedPendingReviewNotification(t, env, publishNotificationStateDeadLetter)
	if _, err := env.store.update(job.ID, func(j *publishJob) {
		j.NotificationAttempts = publishNotificationMaxAttempts
		j.NotificationError = "configuration missing"
	}); err != nil {
		t.Fatal(err)
	}
	env.notified = nil
	env.pipeline.recoverJobs()
	if len(env.notified) != 0 {
		t.Fatalf("startup must not replay dead letter automatically: %v", env.notified)
	}

	replayed, err := env.pipeline.replayPublishNotification(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.NotificationState != publishNotificationStatePending || replayed.NotificationAttempts != 0 {
		t.Fatalf("replayed notification = %+v", replayed)
	}
	got, _ := env.store.byID(job.ID)
	if got.NotificationKind != "" || len(env.notified) != 1 {
		t.Fatalf("explicit replay should deliver and clear outbox: job=%+v notified=%v", got, env.notified)
	}
}

func TestPublishNotificationRetryAddsBoundedJitter(t *testing.T) {
	base := publishNotificationRetryDelay(4)
	if base != 16*time.Second {
		t.Fatalf("base delay = %s", base)
	}
	if got := publishNotificationRetryDelay(100); got > publishNotificationMaxDelay {
		t.Fatalf("capped delay = %s", got)
	}
	for i := 0; i < 20; i++ {
		jitter := randomNotificationJitter(base / 4)
		if jitter < 0 || jitter > base/4 {
			t.Fatalf("jitter = %s, want [0,%s]", jitter, base/4)
		}
	}
}

func TestPublishTerminalStateAndNotificationAreAtomic(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()
	env.pipeline.notify = func(context.Context, string) error { return errors.New("feishu down") }
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, "admin"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStagePublished || got.NotificationKind != publishNotificationPublished || got.NotificationState != publishNotificationStatePending {
		t.Fatalf("published 与终态通知必须同存：stage=%q kind=%q state=%q", got.Stage, got.NotificationKind, got.NotificationState)
	}
	env.pipeline.notify = func(context.Context, string) error { return nil }
	env.pipeline.dispatchPublishNotification(job.ID)
	got, _ = env.store.byID(job.ID)
	if got.Stage != publishStagePublished || got.NotificationKind != "" {
		t.Fatalf("补发只清 outbox，不得回退终态：stage=%q kind=%q", got.Stage, got.NotificationKind)
	}
}
