package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPublishJobInCSTDay(t *testing.T) {
	day, err := parseCMSResyncDayCST("2026-08-13")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		at   string
		want bool
	}{
		{"2026-08-12T16:00:00Z", true},  // 北京时间 8/13 00:00
		{"2026-08-13T03:00:00Z", true},  // 北京时间 8/13 11:00
		{"2026-08-13T16:00:00Z", false}, // 北京时间 8/14 00:00
		{"2026-08-12T15:59:59Z", false},
	}
	for _, tc := range cases {
		job := publishJob{PublishedAt: tc.at}
		if got := publishJobInCSTDay(job, day); got != tc.want {
			t.Fatalf("%s in 2026-08-13 CST = %v，want %v", tc.at, got, tc.want)
		}
	}
}

func TestResyncPublishedCMSDayWritesHomepageAndSource(t *testing.T) {
	env := newPublishTestEnv(t)
	env.ft.forbidPublish = false
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, "ou_reviewer"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	current, err := env.store.byID(job.ID)
	if err != nil || current.Stage != publishStagePublished {
		t.Fatalf("approve 后应为 published：%+v err=%v", current, err)
	}
	env.pipeline.sourceTags = []foodTalksTag{{ID: 8801, Name: "测试公众号", TagTypeID: foodTalksTagTypeArticleSource}}
	env.pipeline.sourceTagsLoaded = time.Now()

	day, err := parseCMSResyncDayCST("2026-08-13")
	if err != nil {
		t.Fatal(err)
	}
	// 把审核/发布时间拨到「今天」，模拟生产上当天已发稿。
	_, _, err = env.store.updateIf(job.ID, func(publishJob) bool { return true }, func(j *publishJob) {
		j.PublishedAt = "2026-08-13T02:00:00Z"
		j.ReviewedAt = "2026-08-13T02:00:00Z"
	})
	if err != nil {
		t.Fatalf("stamp times: %v", err)
	}

	beforeCreate, beforeUpdate := env.ft.counts()
	report, err := env.pipeline.resyncPublishedCMSDay(context.Background(), day)
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	if report.Matched != 1 || report.Updated != 1 || report.Failed != 0 {
		t.Fatalf("report=%+v", report)
	}
	_, afterUpdate := env.ft.counts()
	if afterUpdate-beforeUpdate != 1 {
		t.Fatalf("应再 PUT 一次，create 基线=%d update 增量=%d", beforeCreate, afterUpdate-beforeUpdate)
	}
	env.ft.mu.Lock()
	gotLatest, gotSource := env.ft.lastIsLatest, env.ft.lastSourceID
	env.ft.mu.Unlock()
	if gotLatest != 1 {
		t.Fatalf("补写 isLatest=%d，应为 1", gotLatest)
	}
	if gotSource != 8801 {
		t.Fatalf("补写 sourceId=%d，应为 8801", gotSource)
	}
	after, _ := env.store.byID(job.ID)
	if after.Stage != publishStagePublished {
		t.Fatalf("补写不得改本地阶段，实际 %s", after.Stage)
	}
}

func TestResyncPublishedCMSHTTP(t *testing.T) {
	env := newPublishTestEnv(t)
	env.ft.forbidPublish = false
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, "ou_reviewer"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	_, _, _ = env.store.updateIf(job.ID, func(publishJob) bool { return true }, func(j *publishJob) {
		j.PublishedAt = "2026-08-13T02:00:00Z"
		j.ReviewedAt = "2026-08-13T02:00:00Z"
	})
	mux := newPublishTestMux(t, env)
	rec := doPublishReq(mux, "POST", "/api/publish/cms-resync", `{"day":"2026-08-13"}`, true)
	if rec.Code != 200 {
		t.Fatalf("cms-resync status=%d body=%s", rec.Code, rec.Body.String())
	}
	var report cmsResyncReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if report.Updated != 1 || report.Day != "2026-08-13" {
		t.Fatalf("report=%+v", report)
	}
}
