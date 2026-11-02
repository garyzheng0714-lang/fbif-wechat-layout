package main

// 运营已在 FoodTalks 后台把文章公开时，流水线只对账、不覆盖正文（红线 26）。
// 本文件把任务记为 published，并把群里已有审核卡原位改成「已发布」，
// 备注「这篇文章是人工手动后台发布」。禁止走 UpdateNews / review approve。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// 定时对账只看最近有动静的稿：生产上 24 次后台手工发布全在建单/发卡后 51 小时内，
// 而 7、8 月积压的待审稿每 2 分钟各调一次网站标题检索（/news/admin/news/page），
// 2026-10-12 网站侧反馈分页查询过多。更老的稿手工发布后用 ack-cms-published 补记。
const cmsSyncLookback = 7 * 24 * time.Hour

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
		markExternalAutoDistributionPending(j)
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
	p.kickExternalAutoDistribution()
	refreshed, refreshErr := p.refreshReviewCard(ctx, updated.ID, false)
	if refreshErr != nil {
		return updated, fmt.Errorf("已记为后台发布，但刷新群卡片失败: %w", refreshErr)
	}
	p.recordPublishedRepost(ctx, refreshed)
	return refreshed, nil
}

func (p *publishPipeline) cmsPublicationSyncInterval() time.Duration {
	if p != nil && p.cmsSyncInterval > 0 {
		return p.cmsSyncInterval
	}
	return intervals.cmsWait(pipelineNow(p))
}

// startCMSPublicationSync 后台把草稿公开后，群卡不会自己变。时段里（CMS_RECONCILE_PEAK_WINDOW，默认北京时间
// 8:30–18:00）每 CMS_RECONCILE_PEAK_SECONDS 对账一次，其余时间每 CMS_RECONCILE_QUIET_SECONDS（默认 2、30 分钟）；
// 临近时段切换时提前醒来，避免睡过上班点。只对账最近 cmsSyncLookback 内建单或发过卡的稿。
//
// 这个死循环自己起 goroutine，不进发布 worker 池：放进池里会一直占一个 worker，
// 退出时池子永远等不到它结束，每次部署重启都干等满 30 秒才退出（exit 1），
// 这 30 秒端口已关，监控探针和 Feed 回调全是 502（2026-11-01 查实）。
func (p *publishPipeline) startCMSPublicationSync() {
	if p == nil {
		return
	}
	go func() {
		for {
			p.syncManualCMSPublications(context.Background())
			time.Sleep(p.cmsPublicationSyncInterval())
		}
	}()
}

// cmsPublicationSyncRecent 建单或最近一次发卡不早于 since。重新同步会重发审核卡，
// 稿子随之回到对账范围。时间读不出来时照旧对账，宁多查不漏改卡。
func cmsPublicationSyncRecent(job publishJob, since time.Time) bool {
	parsed := false
	for _, stamp := range []string{job.CreatedAt, job.NotificationSentAt} {
		at, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			continue
		}
		parsed = true
		if !at.Before(since) {
			return true
		}
	}
	return !parsed
}

func (p *publishPipeline) syncManualCMSPublications(ctx context.Context) {
	if p == nil || p.store == nil || publishWorkersPaused() {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	jobs, err := p.store.all()
	if err != nil {
		log.Printf("publish: 读取后台发布对账任务失败：%v", err)
		return
	}
	since := pipelineNow(p).Add(-cmsSyncLookback)
	for _, job := range jobs {
		if job.Stage == publishStagePublished || !publishJobCanAckManualCMS(job) ||
			!cmsPublicationSyncRecent(job, since) {
			continue
		}
		if _, published, checkErr := p.findOwnPublishedCMSNews(ctx, job); checkErr != nil {
			if errors.Is(checkErr, errFoodTalksUnauthorized) {
				// 凭证失效对每个任务都一样，逐个再查只会按任务数刷屏；本轮停下等下一轮。
				log.Printf("publish: 后台发布对账本轮中止（任务 #%d 起）：%v", job.ID, checkErr)
				return
			}
			log.Printf("publish: 后台发布对账失败（任务 #%d）：%v", job.ID, checkErr)
			continue
		} else if !published {
			continue
		}
		if _, ackErr := p.ackManualCMSPublication(ctx, job.ID); ackErr != nil {
			log.Printf("publish: 后台发布后更新群卡失败（任务 #%d）：%v", job.ID, ackErr)
		}
	}
}
