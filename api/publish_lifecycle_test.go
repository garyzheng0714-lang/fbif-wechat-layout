package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublishLifecycleCanUnpublishAndRepublishTheSameNews(t *testing.T) {
	env := newPublishTestEnv(t)
	clock := time.Date(2026, 8, 4, 4, 0, 0, 0, time.UTC)
	env.pipeline.now = func() time.Time { return clock }
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()
	approved, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, "ou_reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if approved.Stage != publishStagePublicizing {
		t.Fatalf("approve should return publicizing, got %s", approved.Stage)
	}
	published, err := env.store.byID(job.ID)
	if err != nil || published.Stage != publishStagePublished {
		t.Fatalf("published job=%+v err=%v", published, err)
	}
	if published.PublishedAt == "" || published.UnpublishWindowUntil == "" || !published.UnpublishPolicySet {
		t.Fatalf("publish lifecycle policy was not frozen: %+v", published)
	}

	clock = clock.Add(time.Hour)
	offlineAccepted, err := env.pipeline.requestUnpublish(job.ID, "ou_reviewer", publishLifecycleCardVersion(published))
	if err != nil {
		t.Fatal(err)
	}
	if offlineAccepted.Stage != publishStageUnpublishing {
		t.Fatalf("unpublish should return accepted stage, got %s", offlineAccepted.Stage)
	}
	offline, err := env.store.byID(job.ID)
	if err != nil || offline.Stage != publishStageOffline {
		t.Fatalf("offline job=%+v err=%v", offline, err)
	}

	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()
	clock = clock.Add(time.Hour)
	republishedAccepted, err := env.pipeline.requestRepublish(job.ID, "ou_reviewer", publishLifecycleCardVersion(offline))
	if err != nil {
		t.Fatal(err)
	}
	if republishedAccepted.Stage != publishStageRepublishing {
		t.Fatalf("republish should return accepted stage, got %s", republishedAccepted.Stage)
	}
	republished, err := env.store.byID(job.ID)
	if err != nil || republished.Stage != publishStagePublished {
		t.Fatalf("republished job=%+v err=%v", republished, err)
	}
	if republished.PublishedAt == published.PublishedAt || republished.UnpublishWindowUntil == published.UnpublishWindowUntil {
		t.Fatalf("republish should start a fresh window: before=%+v after=%+v", published, republished)
	}
	if len(env.ft.updateStatuses) != 3 || env.ft.updateStatuses[0] != foodTalksPublishStatusPublished ||
		env.ft.updateStatuses[1] != foodTalksPublishStatusOffline || env.ft.updateStatuses[2] != foodTalksPublishStatusPublished {
		t.Fatalf("same news id did not follow publish/offline/republish statuses: %v", env.ft.updateStatuses)
	}
	for _, id := range env.ft.updateIDs {
		if id != published.FoodTalksNewsID {
			t.Fatalf("lifecycle changed FoodTalks news id: %v", env.ft.updateIDs)
		}
	}
}

func TestPublishLifecycleRejectsExpiredUnpublishWindow(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, "ou_reviewer"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-9 * time.Hour)
	job, err = env.store.update(job.ID, func(current *publishJob) {
		current.PublishedAt = old.Format(time.RFC3339)
		current.UnpublishWindowUntil = old.Add(8 * time.Hour).Format(time.RFC3339)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pipeline.requestUnpublish(job.ID, "ou_reviewer", publishLifecycleCardVersion(job)); err == nil {
		t.Fatal("expired unpublish window should be rejected")
	}
	current, _ := env.store.byID(job.ID)
	if current.Stage != publishStagePublished || len(env.ft.updateStatuses) != 1 {
		t.Fatalf("expired request changed lifecycle: job=%+v updates=%v", current, env.ft.updateStatuses)
	}
}

func TestPublishLifecyclePolicyHelpersAndReconciliation(t *testing.T) {
	for _, tc := range []struct {
		raw  any
		want int
	}{
		{raw: "", want: 8}, {raw: "12", want: 12}, {raw: json.Number("24"), want: 24}, {raw: float64(3), want: 3},
		{raw: float32(4), want: 4}, {raw: int(-1), want: 0}, {raw: int64(9999), want: 720}, {raw: float64(2.5), want: 8},
	} {
		config := map[string]any{}
		if tc.raw != "" {
			config["unpublish_window_hours"] = tc.raw
		}
		if got := publishUnpublishWindowHoursFromConfig(config); got != tc.want {
			t.Fatalf("window config %#v=%d want %d", tc.raw, got, tc.want)
		}
	}
	job := publishJob{PublishedAt: "2026-08-04T04:00:00Z", UnpublishWindowHours: 2, UnpublishPolicySet: true}
	deadline, ok := publishUnpublishWindowDeadline(job)
	if !ok || !deadline.Equal(time.Date(2026, 8, 4, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("deadline=%v ok=%v", deadline, ok)
	}
	if publishUnpublishWindowOpen(job, deadline) || !strings.Contains(publishUnpublishWindowLabel(job, deadline), "窗口已结束") {
		t.Fatalf("deadline boundary did not close window")
	}
	for _, invalid := range []publishJob{
		{UnpublishWindowHours: 0, UnpublishPolicySet: true, PublishedAt: job.PublishedAt},
		{UnpublishWindowHours: 2, PublishedAt: "not-a-time"},
		{UnpublishWindowHours: 2},
	} {
		if _, ok := publishUnpublishWindowDeadline(invalid); ok || publishUnpublishWindowOpen(invalid, time.Now()) {
			t.Fatalf("invalid deadline opened: %+v", invalid)
		}
		if !strings.Contains(publishUnpublishWindowLabel(invalid, time.Now()), "未开启") {
			t.Fatalf("invalid deadline label=%q", publishUnpublishWindowLabel(invalid, time.Now()))
		}
	}
	legacy := publishJob{ReviewedAt: "2026-08-04T04:00:00Z", UnpublishWindowHours: 2}
	if deadline, ok := publishUnpublishWindowDeadline(legacy); !ok || !deadline.Equal(time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("legacy reviewed deadline=%v ok=%v", deadline, ok)
	}
	if got := publishWindowUntil(time.Date(2026, 8, 4, 4, 0, 0, 0, time.UTC), 0); got != "" {
		t.Fatalf("zero-hour window=%q", got)
	}

	env := newPublishTestEnv(t)
	clock := time.Date(2026, 8, 4, 4, 0, 0, 0, time.UTC)
	env.pipeline.now = func() time.Time { return clock }
	job, _, err := env.store.create("site:reconcile", "https://example.test/reconcile")
	if err != nil {
		t.Fatal(err)
	}
	job, err = env.store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageUnpublishReconciliationRequired
		current.UnpublishIntentKey = "unpublish-intent"
		current.FoodTalksNewsID = 7001
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pipeline.reconcileUnpublish(job.ID, true, true); err == nil {
		t.Fatal("ambiguous unpublish reconciliation accepted")
	}
	resolved, err := env.pipeline.reconcileUnpublish(job.ID, true, false)
	if err != nil || resolved.Stage != publishStageOffline || resolved.UnpublishIntentKey != "" {
		t.Fatalf("offline reconciliation=%+v err=%v", resolved, err)
	}

	job, err = env.store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageRepublishReconciliationRequired
		current.RepublishIntentKey = "republish-intent"
		current.FoodTalksNewsID = 7001
		current.UnpublishPolicySet = true
		current.UnpublishWindowHours = 2
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pipeline.reconcileRepublish(job.ID, false, false); err == nil {
		t.Fatal("empty republish reconciliation accepted")
	}
	resolved, err = env.pipeline.reconcileRepublish(job.ID, true, false)
	if err != nil || resolved.Stage != publishStagePublished || resolved.RepublishIntentKey != "" || resolved.UnpublishWindowUntil == "" {
		t.Fatalf("published reconciliation=%+v err=%v", resolved, err)
	}

	job, err = env.store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageUnpublishingUpdating
		current.UnpublishIntentKey = "mark-unpublish"
	})
	if err != nil {
		t.Fatal(err)
	}
	env.pipeline.markUnpublishReconciliation(job.ID, "mark-unpublish", "核对下线")
	job, err = env.store.byID(job.ID)
	if err != nil || job.Stage != publishStageUnpublishReconciliationRequired {
		t.Fatalf("marked unpublish reconciliation=%+v err=%v", job, err)
	}
	job, err = env.store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageRepublishingUpdating
		current.RepublishIntentKey = "mark-republish"
	})
	if err != nil {
		t.Fatal(err)
	}
	env.pipeline.markRepublishReconciliation(job.ID, "mark-republish", "核对重新发布")
	job, err = env.store.byID(job.ID)
	if err != nil || job.Stage != publishStageRepublishReconciliationRequired {
		t.Fatalf("marked republish reconciliation=%+v err=%v", job, err)
	}

	t.Setenv("DEPLOYMENT_WRITES_PAUSED", "1")
	if _, err := env.pipeline.requestUnpublish(job.ID, "actor", ""); !errors.Is(err, errPublishWorkersPaused) {
		t.Fatalf("paused unpublish err=%v", err)
	}
	if _, err := env.pipeline.requestRepublish(job.ID, "actor", ""); !errors.Is(err, errPublishWorkersPaused) {
		t.Fatalf("paused republish err=%v", err)
	}
}

func TestPublishLifecycleRequestGuards(t *testing.T) {
	if got := pipelineNow(nil); got.IsZero() {
		t.Fatal("pipelineNow(nil) returned zero time")
	}
	if got := pipelineNow(&publishPipeline{}); got.IsZero() {
		t.Fatal("pipelineNow without clock returned zero time")
	}

	env := newPublishTestEnv(t)
	job, _, err := env.store.create("site:lifecycle-guards", "https://example.test/guards")
	if err != nil {
		t.Fatal(err)
	}
	job, err = env.store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStagePublished
		current.FoodTalksNewsID = 7002
		current.PublishedAt = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
		current.UnpublishWindowHours = 8
		current.UnpublishPolicySet = true
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pipeline.requestUnpublish(job.ID, "actor", "stale-card-version"); err == nil || !strings.Contains(err.Error(), "过期") {
		t.Fatalf("stale unpublish card accepted: %v", err)
	}
	job, err = env.store.update(job.ID, func(current *publishJob) { current.Stage = publishStageAwaitingReview })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pipeline.requestUnpublish(job.ID, "actor", ""); err == nil || !strings.Contains(err.Error(), "当前阶段") {
		t.Fatalf("wrong-stage unpublish accepted: %v", err)
	}

	job, err = env.store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageOffline
		current.FoodTalksNewsID = 7002
		current.ReviewAction = ""
		current.ReviewedAt = ""
		current.ApprovedRiskVersion = ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pipeline.requestRepublish(job.ID, "actor", ""); err == nil || !strings.Contains(err.Error(), "风险结论") {
		t.Fatalf("republish without approval accepted: %v", err)
	}
	job, err = env.store.update(job.ID, func(current *publishJob) {
		current.ReviewAction = publishReviewApprove
		current.ReviewedAt = time.Now().UTC().Format(time.RFC3339)
		current.RiskLevel = publishRiskPass
		current.ContentVersion = 1
		current.ContentFrozenVersion = 1
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err = env.store.update(job.ID, func(current *publishJob) {
		current.ApprovedRiskVersion = publishReviewCardVersion(*current)
		current.Stage = publishStageOffline
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pipeline.requestRepublish(job.ID, "actor", "stale-card-version"); err == nil || !strings.Contains(err.Error(), "过期") {
		t.Fatalf("stale republish card accepted: %v", err)
	}
	job, err = env.store.update(job.ID, func(current *publishJob) { current.Stage = publishStagePublished })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pipeline.requestRepublish(job.ID, "actor", ""); err == nil || !strings.Contains(err.Error(), "当前阶段") {
		t.Fatalf("wrong-stage republish accepted: %v", err)
	}
}

func TestPublishLifecyclePolicySnapshotChecksServerRuleVersion(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	active, err := store.activeForAccount(foodTalksSiteRuleAccountID)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := &publishPipeline{rules: store}
	resolved := publishResolvedRule{
		AccountID:  foodTalksSiteRuleAccountID,
		PresetID:   active.ID,
		Revision:   active.Version,
		ConfigHash: active.ConfigHash,
	}
	if hours, err := pipeline.snapshotPublishLifecyclePolicy(foodTalksSiteRuleAccountID, resolved); err != nil || hours <= 0 {
		t.Fatalf("matching server rule rejected: hours=%d err=%v", hours, err)
	}
	resolved.ConfigHash = "sha256:stale"
	if _, err := pipeline.snapshotPublishLifecyclePolicy(foodTalksSiteRuleAccountID, resolved); err == nil || !strings.Contains(err.Error(), "规则发生变化") {
		t.Fatalf("stale server rule accepted: %v", err)
	}
	if _, err := pipeline.snapshotPublishLifecyclePolicy("unknown-account", publishResolvedRule{}); err == nil {
		t.Fatal("unknown rule account accepted")
	}
}
