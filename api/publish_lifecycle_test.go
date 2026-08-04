package main

import (
	"context"
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
