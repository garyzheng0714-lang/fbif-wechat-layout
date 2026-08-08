package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParsePublishRiskAssessmentNormalizesHighestLevel(t *testing.T) {
	raw := `{
		"level":"warning",
		"items":[
			{"category":"data_source","level":"warning","evidence":"销量增长 80%","reason":"正文未注明数据出处"},
			{"category":"efficacy","level":"block","evidence":"三天改善肠胃","reason":"食品功效承诺需要人工确认"}
		],
		"notes":"请核对原始材料"
	}`
	got, err := parsePublishRiskAssessment(raw)
	if err != nil {
		t.Fatalf("parse risk: %v", err)
	}
	if got.Level != publishRiskBlock {
		t.Fatalf("level=%q，应按最高条目提升为 block", got.Level)
	}
	if len(got.Items) != 2 || got.Items[1].Category != publishRiskCategoryEfficacy {
		t.Fatalf("items 未保真：%+v", got.Items)
	}
}

func TestParsePublishRiskAssessmentRejectsUnknownShape(t *testing.T) {
	for _, raw := range []string{
		`{"level":"safe","items":[]}`,
		`{"level":"warning","items":[{"category":"marketing","level":"warning","evidence":"x","reason":"y"}]}`,
		`{"level":"block","items":[{"category":"efficacy","level":"pass","evidence":"x","reason":"y"}]}`,
	} {
		if _, err := parsePublishRiskAssessment(raw); err == nil {
			t.Fatalf("非法风险输出应拒绝：%s", raw)
		}
	}
}

func TestFallbackPublishRiskIsWarningAndKeepsRawEvidence(t *testing.T) {
	got := fallbackPublishRisk("模型返回的原始片段", errors.New("invalid JSON"))
	if got.Level != publishRiskWarning {
		t.Fatalf("fallback level=%q，应为 warning", got.Level)
	}
	if !strings.Contains(got.Raw, "模型返回的原始片段") || !strings.Contains(got.Notes, "invalid JSON") {
		t.Fatalf("fallback 必须保留原始返回和失败原因：%+v", got)
	}
	if len(got.Items) != 1 || got.Items[0].Category != publishRiskCategorySystem {
		t.Fatalf("fallback 应有系统风险条目：%+v", got.Items)
	}
}

func TestPublishRiskPromptCoversFourEditorialRedLines(t *testing.T) {
	prompt := publishRiskSystemPrompt()
	for _, want := range []string{"功效表达", "转载授权", "竞品评价", "数据来源", "只做提示"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("风险提示词缺 %q", want)
		}
	}
}

func TestPublishRiskStagePersistsAssessmentWithoutChangingContent(t *testing.T) {
	env := newPublishTestEnv(t)
	env.pipeline.assessRisk = func(context.Context, publishJob) (publishRiskAssessment, error) {
		return publishRiskAssessment{
			Level: publishRiskBlock,
			Items: []publishRiskItem{{
				Category: publishRiskCategoryEfficacy,
				Level:    publishRiskBlock,
				Evidence: "三天改善肠胃",
				Reason:   "食品功效承诺缺少证据",
			}},
			Notes: "必须人工处理",
		}, nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("风险检查后应正常待审，实际 stage=%q error=%q", got.Stage, got.Error)
	}
	if got.RiskLevel != publishRiskBlock || len(got.RiskItems) != 1 || got.RiskNotes != "必须人工处理" {
		t.Fatalf("风险结果未完整持久化：level=%q items=%+v notes=%q", got.RiskLevel, got.RiskItems, got.RiskNotes)
	}
	wantHTML := `<p>正文</p><img src="https://oss.example.com/a.jpg"><img src="https://oss.example.com/b.jpg">`
	if got.ContentHTML != wantHTML {
		t.Fatalf("风险审核不得改正文\nwant=%s\n got=%s", wantHTML, got.ContentHTML)
	}
}

func TestPublishRiskFailureFallsBackToWarningAndPersistsRaw(t *testing.T) {
	env := newPublishTestEnv(t)
	env.pipeline.assessRisk = func(context.Context, publishJob) (publishRiskAssessment, error) {
		return publishRiskAssessment{Raw: "模型坏输出"}, errors.New("invalid JSON")
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview || got.RiskLevel != publishRiskWarning {
		t.Fatalf("模型失败必须降级 warning 且继续待审，实际 stage=%q risk=%q", got.Stage, got.RiskLevel)
	}
	if !strings.Contains(got.RiskRaw, "模型坏输出") || len(got.RiskItems) == 0 {
		t.Fatalf("fallback 证据未持久化：raw=%q items=%+v", got.RiskRaw, got.RiskItems)
	}
}

func TestPublishRiskBlockCannotApproveAndCardHasNoApproveAction(t *testing.T) {
	env := newPublishTestEnv(t)
	env.pipeline.assessRisk = func(context.Context, publishJob) (publishRiskAssessment, error) {
		return publishRiskAssessment{
			Level: publishRiskBlock,
			Items: []publishRiskItem{{
				Category: publishRiskCategoryRepublication,
				Level:    publishRiskBlock,
				Evidence: "转载自某媒体",
				Reason:   "未确认转载授权",
			}},
			Notes: "禁止一键发布",
		}, nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, "admin"); err == nil || !strings.Contains(err.Error(), "风险") {
		t.Fatalf("block 风险 approve 必须被核心状态机拒绝，实际 err=%v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("拒绝 approve 后仍应待审，实际 %q", got.Stage)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("block 风险不得触发公开 PUT，实际 %d", updateN)
	}
	if len(env.notified) != 1 || !strings.Contains(env.notified[0], "风险审核：阻断") || !strings.Contains(env.notified[0], "转载授权") {
		t.Fatalf("纯文本降级通知必须带风险结论与证据，实际 %v", env.notified)
	}
	raw, err := json.Marshal(buildReviewPendingCard(got, ""))
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	card := string(raw)
	for _, want := range []string{"检查结论", "阻断", "转载授权", "转载自某媒体", "未确认转载授权", "禁止一键发布"} {
		if !strings.Contains(card, want) {
			t.Fatalf("block 卡片缺少 %q：%s", want, card)
		}
	}
	if strings.Contains(card, `"action":"approve"`) || strings.Contains(card, "通过并发布") {
		t.Fatalf("block 卡片不得提供 approve 按钮：%s", card)
	}
	if !strings.Contains(card, `"action":"reject"`) || !strings.Contains(card, "预览效果") {
		t.Fatalf("block 卡片仍应保留驳回与预览：%s", card)
	}
}

func TestPublishMissingRiskAssessmentCannotApprove(t *testing.T) {
	for _, level := range []string{"", "future_level"} {
		t.Run("level="+level, func(t *testing.T) {
			env := newPublishTestEnv(t)
			job, _, err := env.store.create("site:missing-risk:"+level, testPublishSourceURL)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			_, err = env.store.update(job.ID, func(j *publishJob) {
				j.Stage = publishStageAwaitingReview
				j.FoodTalksNewsID = 7240
				j.Title = "历史待审任务"
				j.ContentHTML = "<p>正文</p>"
				j.CoverURL = "https://oss.example.com/a.jpg"
				j.RiskLevel = level
			})
			if err != nil {
				t.Fatalf("seed: %v", err)
			}
			if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, "admin"); err == nil || !strings.Contains(err.Error(), "风险") {
				t.Fatalf("缺失/未知风险结论必须 fail-closed，实际 err=%v", err)
			}
			got, _ := env.store.byID(job.ID)
			card, _ := json.Marshal(buildReviewPendingCard(got, ""))
			if strings.Contains(string(card), `"action":"approve"`) {
				t.Fatalf("缺失/未知风险卡片不得提供 approve：%s", card)
			}
			if _, updateN := env.ft.counts(); updateN != 0 {
				t.Fatalf("缺失/未知风险结论不得公开，实际 PUT=%d", updateN)
			}
		})
	}
}

func TestPublishStartupBackfillsRiskForLegacyStages(t *testing.T) {
	for _, stage := range []string{publishStageStaging, publishStageAwaitingReview} {
		t.Run(stage, func(t *testing.T) {
			env := newPublishTestEnv(t)
			t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_legacy_reviewer")
			env.pipeline.assessRisk = func(context.Context, publishJob) (publishRiskAssessment, error) {
				return publishRiskAssessment{Level: publishRiskPass, Notes: "历史任务补检"}, nil
			}
			job, _, err := env.store.create("site:legacy-risk:"+stage, testPublishSourceURL)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			_, err = env.store.update(job.ID, func(j *publishJob) {
				j.Stage = stage
				j.Title = "历史任务"
				j.SourceName = "测试公众号"
				j.ContentHTML = "<p>正文</p>"
				j.CoverURL = "https://oss.example.com/a.jpg"
				if stage == publishStageAwaitingReview {
					j.FoodTalksNewsID = 7240
				}
			})
			if err != nil {
				t.Fatalf("seed: %v", err)
			}
			env.pipeline.recoverJobs()
			got, _ := env.store.byID(job.ID)
			if got.Stage != publishStageAwaitingReview || got.RiskLevel != publishRiskPass || got.RiskNotes != "历史任务补检" {
				t.Fatalf("历史 %s 应先补风险再待审，实际 stage=%q risk=%q notes=%q", stage, got.Stage, got.RiskLevel, got.RiskNotes)
			}
			if _, updateN := env.ft.counts(); updateN != 0 {
				t.Fatalf("历史补检只写风险，不应 PUT 草稿或公开：%v", env.ft.updateStatuses)
			}
			if len(env.notified) != 1 || !strings.Contains(env.notified[0], "风险审核：通过") {
				t.Fatalf("补检完成应发恰一条带风险结论的新通知，实际 %v", env.notified)
			}
			if stage == publishStageAwaitingReview {
				oldValue := json.RawMessage(`{"action":"approve","job_id":1}`)
				out := env.pipeline.reviewCardAction(context.Background(), "ou_legacy_reviewer", "", oldValue)
				if out.toastType != "error" || !strings.Contains(out.toastContent, "过期") {
					t.Fatalf("补检前旧卡必须失效，实际 %+v", out)
				}
			}
		})
	}
}
