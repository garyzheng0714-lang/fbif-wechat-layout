package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func seedManualCMSJob(t *testing.T, env *publishTestEnv, stage string, newsID int64, title string) publishJob {
	t.Helper()
	job, created, err := env.store.createSeeded(
		fmt.Sprintf("site:https://mp.weixin.qq.com/s/manual-cms-%d", newsID),
		fmt.Sprintf("https://mp.weixin.qq.com/s/manual-cms-%d", newsID),
		func(j *publishJob) {
			j.Stage = stage
			j.Title = title
			j.FoodTalksNewsID = newsID
			j.ReviewCardMsgIDs = map[string]string{"oc_test": "om_test"}
			if stage == publishStageFailed {
				j.FailedStage = publishStageStaging
				j.Error = "FoodTalks 业务错误 code=1 msg=不允许将已发布的资讯改为草稿状态！"
			}
		},
	)
	if err != nil || !created {
		t.Fatalf("seed job: created=%v err=%v", created, err)
	}
	return job
}

func TestReviewDoneCardNotesManualCMSPublish(t *testing.T) {
	job := reviewCardTestJob(publishStagePublished)
	job.PublicationAction = publishPublicationActionManualCMS
	body := cardJSON(t, buildReviewDoneCard(job))
	if !strings.Contains(body, "已发布") {
		t.Fatalf("后台发布卡必须带已发布标签: %s", body)
	}
	if !strings.Contains(body, "这篇文章是人工手动后台发布") {
		t.Fatalf("后台发布卡必须备注人工手动后台发布: %s", body)
	}
	if !strings.Contains(body, "确认本文已由人工在后台发布") {
		t.Fatalf("后台发布卡必须写清操作留痕: %s", body)
	}
	if strings.Contains(body, "通过并发布了本文") {
		t.Fatalf("后台发布不得伪装成审核通过: %s", body)
	}
}

func TestAckManualCMSPublicationDoesNotUpdateNews(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	title := "餐饮零食化，火了猪油渣"
	job := seedManualCMSJob(t, env, publishStageFailed, 62848, title)
	env.ft.mu.Lock()
	env.ft.searchRecords = []foodTalksNewsListItem{{
		ID: 62848, Title: title, PublishStatus: foodTalksPublishStatusPublished, SourceName: "惊蛰研究所",
	}}
	env.ft.mu.Unlock()

	got, err := env.pipeline.ackManualCMSPublication(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	if got.Stage != publishStagePublished {
		t.Fatalf("应记为已发布，实际 %s", got.Stage)
	}
	if got.PublicationAction != publishPublicationActionManualCMS {
		t.Fatalf("publication_action=%q", got.PublicationAction)
	}
	if got.ArticleURL != "https://www.foodtalks.cn/news/62848" {
		t.Fatalf("article_url=%q", got.ArticleURL)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("后台对账不得调用 UpdateNews，实际 update=%d", updateN)
	}
	if len(env.notified) == 0 {
		t.Fatal("应对已有审核卡发出刷新")
	}
}

func TestAckManualCMSPublicationRejectsWhenCMSNotPublished(t *testing.T) {
	env := newPublishTestEnv(t)
	job := seedManualCMSJob(t, env, publishStageFailed, 62847, "蛋白质焦虑，带来了多少新生意")
	env.ft.mu.Lock()
	env.ft.searchRecords = []foodTalksNewsListItem{{
		ID: 62847, Title: job.Title, PublishStatus: foodTalksPublishStatusDraft,
	}}
	env.ft.mu.Unlock()

	got, err := env.pipeline.ackManualCMSPublication(context.Background(), job.ID)
	if err == nil {
		t.Fatalf("网站未发布时应失败，实际 job=%+v", got)
	}
	if got.Stage != publishStageFailed {
		t.Fatalf("失败时不得改阶段，实际 %s", got.Stage)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("核对失败也不得 UpdateNews，实际 update=%d", updateN)
	}
}

func TestStageStageAcksWhenCMSAlreadyPublished(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	title := "从山姆30年里，看见长期主义"
	job := seedManualCMSJob(t, env, publishStageStaging, 62846, title)
	if _, err := env.store.update(job.ID, func(current *publishJob) {
		current.ContentHTML = "<p>正文</p>"
		current.ContentVersion = 1
		current.StagingTarget = currentFoodTalksTarget()
	}); err != nil {
		t.Fatalf("prepare staging job: %v", err)
	}
	env.ft.mu.Lock()
	env.ft.searchRecords = []foodTalksNewsListItem{{
		ID: 62846, Title: title, PublishStatus: foodTalksPublishStatusPublished,
	}}
	env.ft.mu.Unlock()

	current, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := env.pipeline.stageStage(context.Background(), current); err != nil {
		t.Fatalf("stageStage: %v", err)
	}
	got, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Stage != publishStagePublished || got.PublicationAction != publishPublicationActionManualCMS {
		t.Fatalf("已发布稿应只对账，实际 stage=%s action=%s", got.Stage, got.PublicationAction)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("已发布稿再暂存不得 UpdateNews，实际 update=%d", updateN)
	}
}

func TestAckCMSPublishedHTTP(t *testing.T) {
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	title := "蛋白质焦虑，带来了多少新生意"
	job := seedManualCMSJob(t, env, publishStageAssessing, 62847, title)
	env.ft.mu.Lock()
	env.ft.searchRecords = []foodTalksNewsListItem{{
		ID: 62847, Title: title, PublishStatus: foodTalksPublishStatusPublished,
	}}
	env.ft.mu.Unlock()

	if rec := doPublishReq(mux, http.MethodPost, fmt.Sprintf("/api/publish/jobs/%d/ack-cms-published", job.ID), "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("无凭证应 401，实际 %d", rec.Code)
	}
	if rec := doPublishServiceReq(mux, http.MethodPost, fmt.Sprintf("/api/publish/jobs/%d/ack-cms-published", job.ID), ""); rec.Code == http.StatusOK {
		t.Fatalf("同步服务凭证不得标记后台发布，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	rec := doPublishReq(mux, http.MethodPost, fmt.Sprintf("/api/publish/jobs/%d/ack-cms-published", job.ID), "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("管理员应对账成功，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Acked bool                  `json:"acked"`
		Job   publishSyncJobSummary `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.Acked || body.Job.Stage != publishStagePublished {
		t.Fatalf("响应应是已发布: err=%v body=%s", err, rec.Body.String())
	}
	if body.Job.PublicationAction != publishPublicationActionManualCMS {
		t.Fatalf("publication_action=%q", body.Job.PublicationAction)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("HTTP 对账不得 UpdateNews，实际 update=%d", updateN)
	}
}

func TestSyncManualCMSPublicationsUpdatesAwaitingReviewCard(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	title := "餐饮零食化，火了猪油渣"
	job := seedManualCMSJob(t, env, publishStageAwaitingReview, 62848, title)
	env.ft.mu.Lock()
	env.ft.searchRecords = []foodTalksNewsListItem{{
		ID: 62848, Title: title, PublishStatus: foodTalksPublishStatusPublished, SourceName: "惊蛰研究所",
	}}
	env.ft.mu.Unlock()

	env.pipeline.syncManualCMSPublications(context.Background())
	got, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Stage != publishStagePublished || got.PublicationAction != publishPublicationActionManualCMS {
		t.Fatalf("待审卡应对账为后台已发布，实际 stage=%s action=%s", got.Stage, got.PublicationAction)
	}
	if len(env.notified) == 0 {
		t.Fatal("应对群里已有审核卡发出刷新")
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("定时对账不得 UpdateNews，实际 update=%d", updateN)
	}
}

func TestSyncManualCMSPublicationsLeavesDraftJobsAlone(t *testing.T) {
	env := newPublishTestEnv(t)
	job := seedManualCMSJob(t, env, publishStageAwaitingReview, 62847, "蛋白质焦虑，带来了多少新生意")
	env.ft.mu.Lock()
	env.ft.searchRecords = []foodTalksNewsListItem{{
		ID: 62847, Title: job.Title, PublishStatus: foodTalksPublishStatusDraft,
	}}
	env.ft.mu.Unlock()

	env.pipeline.syncManualCMSPublications(context.Background())
	got, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("网站仍是草稿时不得改阶段，实际 %s", got.Stage)
	}
	if len(env.notified) != 0 {
		t.Fatalf("草稿不得刷卡，实际 notified=%d", len(env.notified))
	}
}
