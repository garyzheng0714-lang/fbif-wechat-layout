package main

// publish_lifecycle.go — 已发布文章的下线 / 重新发布生命周期。
//
// FoodTalks 没有删除接口；publishStatus=2 是可逆的下线态，之后可以用同一
// news id 把 publishStatus 改回 1。这里把两次外部 PUT 都当作独立的不可确定
// 窗口：先持久化 intent，再调用网站，结果不确定就进入人工对账，绝不盲重试。

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPublishUnpublishWindowHours = 8
	maxPublishUnpublishWindowHours     = 720

	publishPublicationActionPublish   = "publish"
	publishPublicationActionUnpublish = "unpublish"
	publishPublicationActionRepublish = "republish"
)

var (
	errUnpublishOutcomeUncertain = errors.New("FoodTalks UpdateNews 下线结果不确定")
	errRepublishOutcomeUncertain = errors.New("FoodTalks UpdateNews 重新发布结果不确定")
)

func pipelineNow(p *publishPipeline) time.Time {
	if p != nil && p.now != nil {
		return p.now().UTC()
	}
	return time.Now().UTC()
}

func publishUnpublishWindowHoursFromConfig(config map[string]any) int {
	hours := defaultPublishUnpublishWindowHours
	if raw, ok := config["unpublish_window_hours"]; ok {
		switch value := raw.(type) {
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				hours = n
			}
		case json.Number:
			if n, err := strconv.Atoi(string(value)); err == nil {
				hours = n
			}
		case float64:
			if math.Trunc(value) == value {
				hours = int(value)
			}
		case float32:
			if math.Trunc(float64(value)) == float64(value) {
				hours = int(value)
			}
		case int:
			hours = value
		case int64:
			hours = int(value)
		}
	}
	if hours < 0 {
		return 0
	}
	if hours > maxPublishUnpublishWindowHours {
		return maxPublishUnpublishWindowHours
	}
	return hours
}

// snapshotPublishLifecyclePolicy 与本次渲染实际使用的服务端规则做一致性校验。
// 规则在渲染请求期间发生变化时，让任务失败并按最新规则重试，不能把排版版本
// 与发布窗口版本拆开保存。
func (p *publishPipeline) snapshotPublishLifecyclePolicy(rulePackID string, resolved publishResolvedRule) (int, error) {
	if p.rules == nil {
		// 测试装配和历史离线调用没有规则 store；生产 newPublishPipeline 始终有它。
		return defaultPublishUnpublishWindowHours, nil
	}
	preset, err := p.rules.activeForAccount(rulePackID)
	if err != nil {
		return 0, fmt.Errorf("读取网站发布生命周期规则失败: %w", err)
	}
	if preset.ID != resolved.PresetID || preset.Version != resolved.Revision || preset.ConfigHash != resolved.ConfigHash {
		return 0, errors.New("渲染期间网站规则发生变化，请按最新规则重试")
	}
	return publishUnpublishWindowHoursFromConfig(preset.Config), nil
}

func publishUnpublishWindowDeadline(job publishJob) (time.Time, bool) {
	hours := job.UnpublishWindowHours
	if !job.UnpublishPolicySet {
		// 兼容功能上线前的已发布任务：只使用原审核时间推算，过期后不会重新打开窗口。
		hours = defaultPublishUnpublishWindowHours
	}
	if hours <= 0 {
		return time.Time{}, false
	}
	if raw := strings.TrimSpace(job.UnpublishWindowUntil); raw != "" {
		if deadline, err := time.Parse(time.RFC3339, raw); err == nil {
			return deadline, true
		}
	}
	baseRaw := strings.TrimSpace(job.PublishedAt)
	if baseRaw == "" {
		baseRaw = strings.TrimSpace(job.ReviewedAt)
	}
	if baseRaw == "" {
		return time.Time{}, false
	}
	base, err := time.Parse(time.RFC3339, baseRaw)
	if err != nil {
		return time.Time{}, false
	}
	return base.Add(time.Duration(hours) * time.Hour), true
}

func publishUnpublishWindowOpen(job publishJob, now time.Time) bool {
	deadline, ok := publishUnpublishWindowDeadline(job)
	return ok && now.Before(deadline)
}

func publishUnpublishWindowLabel(job publishJob, now time.Time) string {
	deadline, ok := publishUnpublishWindowDeadline(job)
	if !ok {
		return "发布后下线能力未开启"
	}
	if now.Before(deadline) {
		return fmt.Sprintf("可下线至 %s", formatReviewCardTime(deadline.Format(time.RFC3339)))
	}
	return fmt.Sprintf("下线窗口已结束（截至 %s）", formatReviewCardTime(deadline.Format(time.RFC3339)))
}

func publishWindowUntil(now time.Time, hours int) string {
	if hours <= 0 {
		return ""
	}
	return now.Add(time.Duration(hours) * time.Hour).UTC().Format(time.RFC3339)
}

func publishLifecycleCardVersion(job publishJob) string {
	payload, _ := json.Marshal(struct {
		ID          int64  `json:"id"`
		Stage       string `json:"stage"`
		NewsID      int64  `json:"news_id"`
		ContentHash string `json:"content_hash"`
		ContentVer  int64  `json:"content_version"`
		PublishedAt string `json:"published_at"`
		WindowUntil string `json:"window_until"`
		Action      string `json:"action"`
	}{
		ID: job.ID, Stage: job.Stage, NewsID: job.FoodTalksNewsID,
		ContentHash: job.ContentHash, ContentVer: job.ContentFrozenVersion,
		PublishedAt: job.PublishedAt, WindowUntil: job.UnpublishWindowUntil,
		Action: job.PublicationAction,
	})
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("lifecycle-v1:%d:%x", job.ID, digest[:8])
}

func lifecycleVersionMatches(expected string, job publishJob) bool {
	expected = strings.TrimSpace(expected)
	return expected != "" && expected == publishLifecycleCardVersion(job)
}

func (p *publishPipeline) requestUnpublish(jobID int64, actor, expectedVersion string) (publishJob, error) {
	if publishWorkersPaused() {
		job, _ := p.store.byID(jobID)
		return job, errPublishWorkersPaused
	}
	now := pipelineNow(p)
	versionMismatch := false
	job, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		if expectedVersion != "" && !lifecycleVersionMatches(expectedVersion, current) {
			versionMismatch = true
			return false
		}
		return current.Stage == publishStagePublished && current.FoodTalksNewsID > 0 &&
			publishStagingTargetMismatch(current) == nil &&
			publishUnpublishWindowOpen(current, now) && current.UnpublishIntentKey == "" && current.RepublishIntentKey == ""
	}, func(current *publishJob) {
		current.Stage = publishStageUnpublishing
		current.FailedStage = ""
		current.Error = ""
		current.UnpublishIntentKey = ""
		current.UnpublishIntentAt = ""
		current.PublicationAction = publishPublicationActionUnpublish
		current.PublicationActor = strings.TrimSpace(actor)
		current.PublicationActionAt = now.Format(time.RFC3339)
		cancelPublishNotification(current)
	})
	if err != nil {
		return publishJob{}, err
	}
	if !swapped {
		if versionMismatch {
			return job, errors.New("审核卡已过期，请使用最新一张卡片")
		}
		if guardErr := publishStagingTargetMismatch(job); guardErr != nil {
			return job, guardErr
		}
		if job.Stage == publishStagePublished && !publishUnpublishWindowOpen(job, now) {
			return job, fmt.Errorf("文章已超过下线窗口：%s", publishUnpublishWindowLabel(job, now))
		}
		return job, fmt.Errorf("任务当前阶段（%s）无法下线", job.Stage)
	}
	if p.spawn != nil {
		p.spawn(func() { p.runJob(jobID) })
	}
	return job, nil
}

func (p *publishPipeline) requestRepublish(jobID int64, actor, expectedVersion string) (publishJob, error) {
	if publishWorkersPaused() {
		job, _ := p.store.byID(jobID)
		return job, errPublishWorkersPaused
	}
	now := pipelineNow(p)
	versionMismatch := false
	job, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		if expectedVersion != "" && !lifecycleVersionMatches(expectedVersion, current) {
			versionMismatch = true
			return false
		}
		return current.Stage == publishStageOffline && current.FoodTalksNewsID > 0 &&
			publishStagingTargetMismatch(current) == nil &&
			current.RepublishIntentKey == "" && current.UnpublishIntentKey == "" &&
			validPersistedPublishApproval(current)
	}, func(current *publishJob) {
		current.Stage = publishStageRepublishing
		current.FailedStage = ""
		current.Error = ""
		current.RepublishIntentKey = ""
		current.RepublishIntentAt = ""
		current.PublicationAction = publishPublicationActionRepublish
		current.PublicationActor = strings.TrimSpace(actor)
		current.PublicationActionAt = now.Format(time.RFC3339)
		cancelPublishNotification(current)
	})
	if err != nil {
		return publishJob{}, err
	}
	if !swapped {
		if versionMismatch {
			return job, errors.New("审核卡已过期，请使用最新一张卡片")
		}
		if guardErr := publishStagingTargetMismatch(job); guardErr != nil {
			return job, guardErr
		}
		if job.Stage == publishStageOffline && !validPersistedPublishApproval(job) {
			return job, errors.New("原审核风险结论已失效，不能直接重新发布；请重新生成审核任务")
		}
		return job, fmt.Errorf("任务当前阶段（%s）无法重新发布", job.Stage)
	}
	if p.spawn != nil {
		p.spawn(func() { p.runJob(jobID) })
	}
	return job, nil
}

func (p *publishPipeline) stageUnpublish(ctx context.Context, job publishJob) error {
	if job.FoodTalksNewsID <= 0 {
		return errors.New("缺少 FoodTalks 资讯 id，无法下线")
	}
	now := pipelineNow(p)
	if !publishUnpublishWindowOpen(job, now) {
		return fmt.Errorf("下线窗口已结束：%s", publishUnpublishWindowLabel(job, now))
	}
	ft, err := p.foodTalks()
	if err != nil {
		return err
	}
	news, err := p.buildSiteNews(job, foodTalksPublishStatusOffline)
	if err != nil {
		return err
	}
	news.ID = job.FoodTalksNewsID
	intentKey := fmt.Sprintf("foodtalks-unpublish:job-%d:%s", job.ID, job.PublicationActionAt)
	claimed, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStageUnpublishing && current.UnpublishIntentKey == "" &&
			current.RepublishIntentKey == "" && current.FoodTalksNewsID > 0 &&
			publishStagingTargetMismatch(current) == nil &&
			publishUnpublishWindowOpen(current, now)
	}, func(current *publishJob) {
		current.Stage = publishStageUnpublishingUpdating
		current.UnpublishIntentKey = intentKey
		current.UnpublishIntentAt = now.Format(time.RFC3339)
		current.Error = ""
	})
	if err != nil {
		return fmt.Errorf("下线意图持久化失败：%w", err)
	}
	if !swapped {
		if claimed.Stage == publishStageUnpublishingUpdating && claimed.UnpublishIntentKey != "" {
			return fmt.Errorf("%w：任务已存在下线 intent %q", errUnpublishOutcomeUncertain, claimed.UnpublishIntentKey)
		}
		return errors.New("下线任务已被其他操作更新")
	}
	if err := ft.UpdateNews(ctx, news); err != nil {
		msg := fmt.Sprintf("UpdateNews 下线结果不确定，请在 FoodTalks 前台/后台核对文章是否已下线（intent=%s）：%v", intentKey, err)
		p.markUnpublishReconciliation(job.ID, intentKey, msg)
		return fmt.Errorf("%w：%s", errUnpublishOutcomeUncertain, msg)
	}
	updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStageUnpublishingUpdating && current.UnpublishIntentKey == intentKey
	}, func(current *publishJob) {
		current.Stage = publishStageOffline
		current.FailedStage = ""
		current.UnpublishIntentKey = ""
		current.UnpublishIntentAt = ""
		current.UnpublishedAt = now.Format(time.RFC3339)
		current.Error = ""
		enqueuePublishNotification(current, publishNotificationOffline)
	})
	if err != nil || !swapped {
		if latest, loadErr := p.store.byID(job.ID); loadErr == nil && latest.Stage == publishStageOffline {
			p.schedulePublishNotification(latest.ID)
			return nil
		}
		msg := fmt.Sprintf("FoodTalks 已返回下线成功，但本地终态落盘不确定（intent=%s）", intentKey)
		if err != nil {
			msg += "：" + err.Error()
		}
		p.markUnpublishReconciliation(job.ID, intentKey, msg)
		return fmt.Errorf("%w：%s", errUnpublishOutcomeUncertain, msg)
	}
	p.schedulePublishNotification(updated.ID)
	return nil
}

func (p *publishPipeline) stageRepublish(ctx context.Context, job publishJob) error {
	if job.FoodTalksNewsID <= 0 {
		return errors.New("缺少 FoodTalks 资讯 id，无法重新发布")
	}
	if !validPersistedPublishApproval(job) {
		return errors.New("原审核授权或风险版本已失效，不能重新发布")
	}
	ft, err := p.foodTalks()
	if err != nil {
		return err
	}
	news, err := p.buildSiteNews(job, foodTalksPublishStatusPublished)
	if err != nil {
		return err
	}
	news.ID = job.FoodTalksNewsID
	now := pipelineNow(p)
	intentKey := fmt.Sprintf("foodtalks-republish:job-%d:%s", job.ID, job.PublicationActionAt)
	claimed, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStageRepublishing && current.RepublishIntentKey == "" &&
			current.UnpublishIntentKey == "" && current.FoodTalksNewsID > 0 &&
			publishStagingTargetMismatch(current) == nil && validPersistedPublishApproval(current)
	}, func(current *publishJob) {
		current.Stage = publishStageRepublishingUpdating
		current.RepublishIntentKey = intentKey
		current.RepublishIntentAt = now.Format(time.RFC3339)
		current.Error = ""
	})
	if err != nil {
		return fmt.Errorf("重新发布意图持久化失败：%w", err)
	}
	if !swapped {
		if claimed.Stage == publishStageRepublishingUpdating && claimed.RepublishIntentKey != "" {
			return fmt.Errorf("%w：任务已存在重新发布 intent %q", errRepublishOutcomeUncertain, claimed.RepublishIntentKey)
		}
		return errors.New("重新发布任务已被其他操作更新")
	}
	if err := ft.UpdateNews(ctx, news); err != nil {
		msg := fmt.Sprintf("UpdateNews 重新发布结果不确定，请在 FoodTalks 前台/后台核对文章是否已公开（intent=%s）：%v", intentKey, err)
		p.markRepublishReconciliation(job.ID, intentKey, msg)
		return fmt.Errorf("%w：%s", errRepublishOutcomeUncertain, msg)
	}
	updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStageRepublishingUpdating && current.RepublishIntentKey == intentKey
	}, func(current *publishJob) {
		current.Stage = publishStagePublished
		current.FailedStage = ""
		current.RepublishIntentKey = ""
		current.RepublishIntentAt = ""
		current.PublishedAt = now.Format(time.RFC3339)
		current.UnpublishWindowUntil = publishWindowUntil(now, current.UnpublishWindowHours)
		current.Error = ""
		enqueuePublishNotification(current, publishNotificationRepublished)
	})
	if err != nil || !swapped {
		if latest, loadErr := p.store.byID(job.ID); loadErr == nil && latest.Stage == publishStagePublished {
			p.schedulePublishNotification(latest.ID)
			return nil
		}
		msg := fmt.Sprintf("FoodTalks 已返回重新发布成功，但本地终态落盘不确定（intent=%s）", intentKey)
		if err != nil {
			msg += "：" + err.Error()
		}
		p.markRepublishReconciliation(job.ID, intentKey, msg)
		return fmt.Errorf("%w：%s", errRepublishOutcomeUncertain, msg)
	}
	p.schedulePublishNotification(updated.ID)
	return nil
}

func (p *publishPipeline) markUnpublishReconciliation(jobID int64, intentKey, reason string) {
	updated, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.Stage == publishStageUnpublishingUpdating && current.UnpublishIntentKey == intentKey
	}, func(current *publishJob) {
		current.Stage = publishStageUnpublishReconciliationRequired
		current.Error = reason
		enqueuePublishNotification(current, publishNotificationUnpublishUncertain)
	})
	if err != nil {
		return
	}
	if swapped {
		p.schedulePublishNotification(updated.ID)
	}
}

func (p *publishPipeline) markRepublishReconciliation(jobID int64, intentKey, reason string) {
	updated, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.Stage == publishStageRepublishingUpdating && current.RepublishIntentKey == intentKey
	}, func(current *publishJob) {
		current.Stage = publishStageRepublishReconciliationRequired
		current.Error = reason
		enqueuePublishNotification(current, publishNotificationRepublishUncertain)
	})
	if err != nil {
		return
	}
	if swapped {
		p.schedulePublishNotification(updated.ID)
	}
}

func (p *publishPipeline) reconcileUnpublish(jobID int64, confirmOffline, confirmStillPublished bool) (publishJob, error) {
	if confirmOffline == confirmStillPublished {
		job, _ := p.store.byID(jobID)
		return job, errors.New("下线对账必须且只能确认一个结果")
	}
	now := pipelineNow(p)
	job, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.Stage == publishStageUnpublishReconciliationRequired && current.UnpublishIntentKey != ""
	}, func(current *publishJob) {
		current.UnpublishIntentKey = ""
		current.UnpublishIntentAt = ""
		current.Error = ""
		if confirmOffline {
			current.Stage = publishStageOffline
			current.UnpublishedAt = now.Format(time.RFC3339)
			current.PublicationAction = publishPublicationActionUnpublish
			enqueuePublishNotification(current, publishNotificationOffline)
		} else {
			current.Stage = publishStagePublished
			current.PublicationAction = publishPublicationActionPublish
			enqueuePublishNotification(current, publishNotificationPublished)
		}
	})
	if err != nil {
		return publishJob{}, err
	}
	if !swapped {
		return job, fmt.Errorf("任务当前阶段（%s）无法进行下线对账", job.Stage)
	}
	p.schedulePublishNotification(job.ID)
	return job, nil
}

func (p *publishPipeline) reconcileRepublish(jobID int64, confirmPublished, confirmStillOffline bool) (publishJob, error) {
	if confirmPublished == confirmStillOffline {
		job, _ := p.store.byID(jobID)
		return job, errors.New("重新发布对账必须且只能确认一个结果")
	}
	now := pipelineNow(p)
	job, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.Stage == publishStageRepublishReconciliationRequired && current.RepublishIntentKey != ""
	}, func(current *publishJob) {
		current.RepublishIntentKey = ""
		current.RepublishIntentAt = ""
		current.Error = ""
		if confirmPublished {
			current.Stage = publishStagePublished
			current.PublishedAt = now.Format(time.RFC3339)
			current.UnpublishWindowUntil = publishWindowUntil(now, current.UnpublishWindowHours)
			current.PublicationAction = publishPublicationActionRepublish
			enqueuePublishNotification(current, publishNotificationRepublished)
		} else {
			current.Stage = publishStageOffline
			current.PublicationAction = publishPublicationActionUnpublish
			enqueuePublishNotification(current, publishNotificationOffline)
		}
	})
	if err != nil {
		return publishJob{}, err
	}
	if !swapped {
		return job, fmt.Errorf("任务当前阶段（%s）无法进行重新发布对账", job.Stage)
	}
	p.schedulePublishNotification(job.ID)
	return job, nil
}
