package main

// publish_external_manual.go — 「平台账号」里状态是「人工发」的号（2026-11-21 用户：腾讯号三天就要重新扫码登录，
// 交给人工发；「人工排版同步的信息，你也可以放在我们的卡片里……如果监控不到，就把这个从卡片里去掉吧」）。
//
// 系统不往这种号推：它不在 externalTargets.Targets 里，自动外发、重推、卡片按钮、改稿页、提醒、监控都碰不到它；
// 中台给分发服务的包只有在用的号，分发服务器也不再管它的登录。卡片上照样列这个号：人在平台上发了以后，
// 经分发服务读平台公开页（POST /v1/platforms/{platform}/public-articles，不登录、不开浏览器）按标题找到这篇，
// 记成「人工已发」带上链接（和管理员登记的是同一种 lane）。平台读不了公开文章、表里「主页」没填的号，卡片上不列。
//
// 什么时候查：总账本「定时任务和回调」的「人工发的号查链接」（MANUAL_LOOKUP_WINDOW 时段里每 MANUAL_LOOKUP_SECONDS 一次）。
// 只在有等人发的稿时才去问，一个号一次一页（平台给的最近那些）。等人发的：FoodTalks 已发布、能外发的原创，这个号上还没发布，
// 网站发布（或这个号上一次出结果）以来不到 7 天。人工发常常隔几天（2026-11-13 发布的两篇，腾讯号 11-18 才发）。

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

const (
	externalManualWindow = 7 * 24 * time.Hour
	// 平台上比网站早发一点也认（人可能先发了平台）。
	externalManualEarlySlack = 12 * time.Hour

	publishExternalManualLookupActor = "system:manual_lookup"

	// 问一次分发服务读公开文章最多等多久（它只发一次 GET，几秒就回）。
	externalManualLookupTimeout = time.Minute
)

// externalManualTrackable 查不查得到人在这个号上发了哪篇：平台能读公开文章（分发服务 GET /v1/platforms 的 public_articles），
// 表里填了「主页」。查不到的号卡片上不列。
func externalManualTrackable(account externalAccount) bool {
	return strings.TrimSpace(account.Homepage) != "" && platformForAccount(account).PublicArticles
}

// externalPublicArticles 这个号最近公开的文章，经分发服务读平台公开页。
func (p *publishPipeline) externalPublicArticles(ctx context.Context, account externalAccount) ([]externalArticleRef, error) {
	client, err := p.robotClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, externalManualLookupTimeout)
	defer cancel()
	return client.PublicArticles(ctx, strings.TrimSpace(account.Platform), strings.TrimSpace(account.Homepage))
}

// externalManualWaiting 这篇是不是在等人去这个号上发：网站已发布的、能外发的原创，这个号上还没发布，
// 网站发布（这个号上后来又有过结果的，按那次）以来不到 7 天。
func externalManualWaiting(job publishJob, account externalAccount, now time.Time) bool {
	if job.Stage != publishStagePublished || len(accountsForJob([]externalAccount{account}, job)) == 0 {
		return false
	}
	if original, _, _ := publishArticleProvenance(job); !original || externalDistributionExcluded(job) != "" {
		return false
	}
	since, err := time.Parse(time.RFC3339, job.PublishedAt)
	if err != nil {
		return false
	}
	if lane, ok := externalLaneFor(job, account.ID); ok {
		if externalLaneSettled(lane) || publishExternalDistributionProcessing(lane.State) {
			return false
		}
		if at, err := time.Parse(time.RFC3339, lane.CompletedAt); err == nil && at.After(since) {
			since = at
		}
	}
	return now.Sub(since) < externalManualWindow
}

// externalManualShown 卡片上列不列这个人工发的号：这篇在它上面已经发布了，或者正等人发、发了查得到。
func externalManualShown(job publishJob, account externalAccount, now time.Time) bool {
	if lane, ok := externalLaneFor(job, account.ID); ok && externalLaneSettled(lane) {
		return true
	}
	return externalManualTrackable(account) && externalManualWaiting(job, account, now)
}

// matchExternalManualArticle 这篇稿是这些文章里的哪一篇：标题对得上、发在网站发布之后（早 12 小时内也认）、没被别的稿认走。
func matchExternalManualArticle(job publishJob, accountID string, articles []externalArticleRef, taken map[string]bool) (externalArticleRef, bool) {
	published, err := time.Parse(time.RFC3339, job.PublishedAt)
	if err != nil {
		return externalArticleRef{}, false
	}
	lane, _ := externalLaneFor(job, accountID)
	for _, article := range articles {
		if taken[article.URL] || article.PublishedAt.Before(published.Add(-externalManualEarlySlack)) {
			continue
		}
		if externalTitlesMatch(job.Title, article.Title) || externalTitlesMatch(lane.Title, article.Title) {
			return article, true
		}
	}
	return externalArticleRef{}, false
}

// followExternalManualLanes 每个人工发的号：有等人发的稿就问一次平台公开页，查到的记成人工已发、刷新卡片。
// 只在 MANUAL_LOOKUP_WINDOW 时段里问。
func (p *publishPipeline) followExternalManualLanes(ctx context.Context, now time.Time) {
	if p == nil || p.store == nil || publishWorkersPaused() || !intervals.manualLookupWindow.contains(now) {
		return
	}
	cfg, err := loadExternalTargets()
	if err != nil || len(cfg.Manual) == 0 {
		return
	}
	jobs, err := p.store.all()
	if err != nil {
		log.Printf("publish: 人工发的号查链接读取任务失败：%v", err)
		return
	}
	for _, account := range cfg.Manual {
		if !externalManualTrackable(account) {
			continue
		}
		taken := map[string]bool{}
		var waiting []publishJob
		for _, job := range jobs {
			if lane, ok := externalLaneFor(job, account.ID); ok && lane.URL != "" {
				taken[lane.URL] = true
			}
			if externalManualWaiting(job, account, now) {
				waiting = append(waiting, job)
			}
		}
		if len(waiting) == 0 {
			continue
		}
		articles, err := p.externalPublicArticles(ctx, account)
		if err != nil {
			log.Printf("publish: %s公开页没查成，下次再查：%v", account.label(), err)
			continue
		}
		for _, job := range waiting {
			article, ok := matchExternalManualArticle(job, account.ID, articles, taken)
			if !ok {
				continue
			}
			taken[article.URL] = true
			existing, exists := externalLaneFor(job, account.ID)
			apply := externalManualApply(false, publishExternalManualLookupActor, now.Format(time.RFC3339), article.URL, article.Title)
			if _, err := p.saveExternalManualLane(job.ID, cfg, account, existing, exists, apply); err != nil {
				log.Printf("publish: #%d 在%s上查到了，没记上：%v", job.ID, account.label(), err)
				continue
			}
			log.Printf("publish: #%d 在%s上人工发了：%s", job.ID, account.label(), article.URL)
		}
	}
}

// externalManualApply 记成人工已发；已经是发布状态的（settled）只补链接、平台标题。
func externalManualApply(settled bool, actor, stamp, articleURL, title string) func(*publishExternalLane) {
	return func(lane *publishExternalLane) {
		if !settled {
			lane.State = publishExternalLaneManual
			lane.RequestedBy, lane.RequestedAt, lane.CompletedAt = actor, stamp, stamp
			lane.Error, lane.LoginRequired = "", false
		}
		if articleURL != "" {
			lane.URL = articleURL
		}
		if title != "" {
			lane.Title = title
		}
	}
}

// saveExternalManualLane 按 apply 改这篇在这个号上的 lane（没有就建一条），读的时候到现在 lane 变了就不写，写了就刷新卡片。
func (p *publishPipeline) saveExternalManualLane(jobID int64, cfg externalTargets, target externalAccount, existing publishExternalLane, exists bool, apply func(*publishExternalLane)) (publishJob, error) {
	updated, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		lane, ok := externalLaneFor(current, target.ID)
		if ok != exists {
			return false
		}
		return !ok || (lane.State == existing.State && lane.ContentPK == existing.ContentPK)
	}, func(current *publishJob) {
		i := externalLaneIndex(*current, target.ID)
		if i < 0 {
			current.ExternalDistributionLanes = append(current.ExternalDistributionLanes, publishExternalLane{AccountID: target.ID, Name: target.label()})
			i = len(current.ExternalDistributionLanes) - 1
		}
		apply(&current.ExternalDistributionLanes[i])
		if current.ExternalDistributionAutoPending != "" && len(externalDistributionMissingAutoTargets(*current, cfg)) == 0 {
			current.ExternalDistributionAutoPending = ""
			current.ExternalDistributionAutoError = ""
		}
		enqueueExternalDistributionCardUpdate(current)
	})
	if err != nil {
		return updated, err
	}
	if !swapped {
		return updated, fmt.Errorf("%s的状态刚刚变化，请重新读取后再登记", target.label())
	}
	p.scheduleExternalDistributionCardUpdate(updated.ID)
	return updated, nil
}
