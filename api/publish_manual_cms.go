package main

// 运营已在 FoodTalks 后台把文章公开时，流水线只对账、不覆盖正文（红线 26）。
// 本文件把任务记为 published，并把群里已有审核卡原位改成「已发布」，
// 备注「这篇文章是人工手动后台发布」。禁止走 UpdateNews / review approve。

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func foodTalksAlreadyPublishedDraftError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *foodTalksAPIError
	if errors.As(err, &apiErr) {
		return strings.Contains(apiErr.Msg, "不允许将已发布的资讯改为草稿状态")
	}
	return strings.Contains(err.Error(), "不允许将已发布的资讯改为草稿状态")
}

func publishJobCanAckManualCMS(job publishJob) bool {
	if job.FoodTalksNewsID <= 0 || publishContentHasUncertainIntent(job) {
		return false
	}
	switch job.Stage {
	case publishStagePublicizingUpdating, publishStageStagingCreating,
		publishStageReconciliationRequired, publishStagePublicizeReconciliationRequired,
		publishStageUnpublishReconciliationRequired, publishStageRepublishReconciliationRequired,
		publishStageUnpublishing, publishStageUnpublishingUpdating,
		publishStageRepublishing, publishStageRepublishingUpdating,
		publishStageOffline:
		return false
	default:
		return true
	}
}

func (p *publishPipeline) findOwnPublishedCMSNews(ctx context.Context, job publishJob) (foodTalksNewsListItem, bool, error) {
	if job.FoodTalksNewsID <= 0 || p == nil || p.foodTalks == nil {
		return foodTalksNewsListItem{}, false, nil
	}
	ft, err := p.foodTalks()
	if err != nil {
		return foodTalksNewsListItem{}, false, err
	}
	items, err := ft.SearchNewsByTitle(ctx, job.Title)
	if err != nil {
		return foodTalksNewsListItem{}, false, err
	}
	for _, item := range items {
		if item.ID == job.FoodTalksNewsID && item.PublishStatus == foodTalksPublishStatusPublished {
			return item, true, nil
		}
	}
	return foodTalksNewsListItem{}, false, nil
}

func (p *publishPipeline) ackManualCMSPublication(ctx context.Context, jobID int64) (publishJob, error) {
	if publishWorkersPaused() {
		job, _ := p.store.byID(jobID)
		return job, errPublishWorkersPaused
	}
	job, err := p.store.byID(jobID)
	if err != nil {
		return publishJob{}, err
	}
	if job.FoodTalksNewsID <= 0 {
		return job, errors.New("任务还没有对应的网站文章，不能记为后台已发布")
	}
	if !publishJobCanAckManualCMS(job) {
		return job, fmt.Errorf("任务当前阶段（%s）不能记为后台已发布", job.Stage)
	}
	if _, published, err := p.findOwnPublishedCMSNews(ctx, job); err != nil {
		return job, fmt.Errorf("核对网站是否已发布失败，未改任务状态: %w", err)
	} else if !published {
		return job, errors.New("网站上这篇还不是已发布状态，不能改卡片")
	}
	if job.Stage == publishStagePublished {
		if job.PublicationAction != publishPublicationActionManualCMS {
			return job, errors.New("任务已经是审核通过发布，无需再标后台发布")
		}
		refreshed, refreshErr := p.refreshReviewCard(ctx, job.ID, false)
		if refreshErr != nil {
			return job, fmt.Errorf("任务已是已发布，但刷新群卡片失败: %w", refreshErr)
		}
		return refreshed, nil
	}

	hours := defaultPublishUnpublishWindowHours
	if pack, packErr := publishRulePackIDForJob(job); packErr == nil {
		if h, policyErr := p.snapshotPublishLifecyclePolicy(pack, publishResolvedRule{
			AccountID:  pack,
			PresetID:   job.RulePresetID,
			Revision:   job.RuleRevision,
			ConfigHash: job.RuleConfigHash,
			Source:     "server",
		}); policyErr == nil {
			hours = h
		}
	}
	publishedAt := pipelineNow(p)
	articleURL := siteFrontURL() + "/news/" + strconv.FormatInt(job.FoodTalksNewsID, 10)
	updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.FoodTalksNewsID == job.FoodTalksNewsID &&
			publishJobCanAckManualCMS(current) &&
			current.Stage != publishStagePublished
	}, func(j *publishJob) {
		j.Stage = publishStagePublished
		j.FailedStage = ""
		j.Error = ""
		j.ArticleURL = articleURL
		j.PublishedAt = publishedAt.Format(time.RFC3339)
		j.UnpublishWindowHours = hours
		j.UnpublishPolicySet = true
		j.UnpublishWindowUntil = publishWindowUntil(publishedAt, hours)
		j.PublicationAction = publishPublicationActionManualCMS
		j.PublicationActor = ""
		j.PublicationActionAt = publishedAt.Format(time.RFC3339)
		cancelPublishNotification(j)
	})
	if err != nil {
		return job, err
	}
	if !swapped {
		latest, loadErr := p.store.byID(job.ID)
		if loadErr != nil {
			return job, fmt.Errorf("任务状态刚刚变化，请刷新后重试")
		}
		if latest.Stage == publishStagePublished && latest.PublicationAction == publishPublicationActionManualCMS {
			refreshed, refreshErr := p.refreshReviewCard(ctx, latest.ID, false)
			if refreshErr != nil {
				return latest, fmt.Errorf("任务已是已发布，但刷新群卡片失败: %w", refreshErr)
			}
			return refreshed, nil
		}
		return latest, fmt.Errorf("任务当前阶段（%s）不能记为后台已发布", latest.Stage)
	}
	refreshed, refreshErr := p.refreshReviewCard(ctx, updated.ID, false)
	if refreshErr != nil {
		return updated, fmt.Errorf("已记为后台发布，但刷新群卡片失败: %w", refreshErr)
	}
	return refreshed, nil
}
