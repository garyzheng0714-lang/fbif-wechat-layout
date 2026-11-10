package main

// publish_external_auto.go — 原创稿公开后自动外发（2026-10-10 用户裁定：原创自动同步头条号）。
//
// 只对多维表格「平台账号」里勾了「自动外发」的号生效；其余账号仍只走审核卡按钮。
// 各平台分开推（publish_external_lanes.go）：只推这篇还没推过的 auto 账号，推过的账号
// 不论结果都不自动再推。任务落为已发布的同一次写入里记下待办（external_distribution_auto_pending），
// 再由 runExternalAutoDistribution 消化：进程在两步之间重启也不会漏发，每 AUTO_DISTRIBUTE_SECONDS（默认 10 分钟）兜底扫一次。
//
// 平台有每天的文章上限（头条号约 5 篇），超出会被拒绝，还会挤掉当天新稿，所以按账号各自配额推：
//   - 新公开稿（publish）优先，每个账号每天（北京时间）不超过平台上限，人工按钮发的也计入；
//   - 存量补发（backfill）每个账号每天最多 publishExternalAutoBackfillDailyLimit 篇，只在白天推，
//     与这个账号上一次外发至少隔 publishExternalAutoBackfillSpacing，避免集中刷屏被风控。

import (
	"errors"
	"log"
	"sort"
	"strings"
	"time"
)

const (
	publishExternalAutoActor         = "system:auto_distribute"
	publishExternalAutoBackfillActor = "system:auto_distribute_backfill"

	publishExternalAutoKindPublish  = "publish"
	publishExternalAutoKindBackfill = "backfill"

	publishExternalAutoBackfillDailyLimit = 3
	publishExternalAutoBackfillSpacing    = time.Hour
	publishExternalAutoBackfillStartHour  = 9
	publishExternalAutoBackfillEndHour    = 20
)

var publishExternalAutoZone = time.FixedZone("CST", 8*3600)

// markExternalAutoDistributionPending 在任务落为已发布的同一次写入里调用：配置了 auto
// 账号、从未外发过、按外发口径是原创、不是只发 FoodTalks 的（创新周报），才记待办。转载与待核原创一律不记；外发过的稿
// 重新发布也不记，后加的 auto 账号只经管理员补发。
func markExternalAutoDistributionPending(j *publishJob) {
	if j == nil || externalDistributionStarted(*j) || j.ExternalDistributionAutoPending != "" {
		return
	}
	cfg, err := loadExternalTargets()
	if err != nil || len(cfg.autoTargets()) == 0 {
		return
	}
	if original, _, _ := publishArticleProvenance(*j); !original || externalDistributionExcluded(*j) != "" {
		return
	}
	j.ExternalDistributionAutoPending = publishExternalAutoKindPublish
	j.ExternalDistributionAutoError = ""
}

func (p *publishPipeline) kickExternalAutoDistribution() {
	if p == nil {
		return
	}
	p.spawnExternalWork(p.runExternalAutoDistribution)
}

// startExternalAutoDistribution 兜底循环：配额次日恢复、补发按间隔放行、读网站失败后的
// 重试都靠它；顺带按幂等键向分发服务补查结果不明的草稿。另一个循环每 DISTRIBUTION_FOLLOW_SECONDS（默认 1 分钟，
// 链接一出来卡片一两分钟内就有）跟一次发布中、平台审核中的 lane（只读分发服务的记录），把文章链接带回卡片。
// 独立 goroutine，不占发布 worker。
func (p *publishPipeline) startExternalAutoDistribution() {
	if p == nil {
		return
	}
	go func() {
		for {
			p.runExternalAutoDistribution()
			p.refreshExternalDistributionResults()
			time.Sleep(intervals.autoDistribute)
		}
	}()
	go func() {
		for {
			p.followRobotLanes()
			time.Sleep(intervals.distributionFollow)
		}
	}()
}

// externalAutoQuota 各 auto 账号今天（北京时间）已提交的外发数、补发数与最近一次提交时间。
type externalAutoQuota struct {
	limits   map[string]int
	today    map[string]int
	backfill map[string]int
	last     map[string]time.Time
}

func externalAutoQuotaFor(jobs []publishJob, autoTargets []externalAccount, now time.Time) externalAutoQuota {
	quota := externalAutoQuota{
		limits: map[string]int{}, today: map[string]int{}, backfill: map[string]int{}, last: map[string]time.Time{},
	}
	for _, target := range autoTargets {
		quota.limits[strings.TrimSpace(target.ID)] = platformForAccount(target).dailyLimit()
	}
	today := now.In(publishExternalAutoZone).Format("2006-01-02")
	for _, job := range jobs {
		for _, lane := range job.ExternalDistributionLanes {
			id := strings.TrimSpace(lane.AccountID)
			if _, auto := quota.limits[id]; !auto {
				continue
			}
			// 人工登记的已发稿、没发出去的（失败）不占平台名额。
			if lane.State == publishExternalLaneManual || externalLaneCanSafeRetry(lane) {
				continue
			}
			requestedAt, err := time.Parse(time.RFC3339, lane.RequestedAt)
			if err != nil {
				continue
			}
			if requestedAt.After(quota.last[id]) {
				quota.last[id] = requestedAt
			}
			if requestedAt.In(publishExternalAutoZone).Format("2006-01-02") != today {
				continue
			}
			quota.today[id]++
			if lane.RequestedBy == publishExternalAutoBackfillActor {
				quota.backfill[id]++
			}
		}
	}
	return quota
}

func (q externalAutoQuota) allows(kind, accountID string, now time.Time) bool {
	limit, auto := q.limits[accountID]
	if !auto || q.today[accountID] >= limit {
		return false
	}
	if kind != publishExternalAutoKindBackfill {
		return true
	}
	if q.backfill[accountID] >= publishExternalAutoBackfillDailyLimit {
		return false
	}
	hour := now.In(publishExternalAutoZone).Hour()
	if hour < publishExternalAutoBackfillStartHour || hour >= publishExternalAutoBackfillEndHour {
		return false
	}
	last := q.last[accountID]
	return last.IsZero() || now.Sub(last) >= publishExternalAutoBackfillSpacing
}

func (q *externalAutoQuota) record(kind, accountID string, now time.Time) {
	q.today[accountID]++
	if kind == publishExternalAutoKindBackfill {
		q.backfill[accountID]++
	}
	q.last[accountID] = now
}

// runExternalAutoDistribution 按配额把待办推给 auto 账号。规则上不能外发的（转载、
// 网站已下线等）清掉待办并留原因；读网站失败等暂时错误保留待办，下一轮再试。
func (p *publishPipeline) runExternalAutoDistribution() {
	if p == nil || p.store == nil || publishWorkersPaused() {
		return
	}
	p.externalAutoMu.Lock()
	defer p.externalAutoMu.Unlock()
	jobs, err := p.store.all()
	if err != nil {
		log.Printf("publish: 自动外发读取任务失败：%v", err)
		return
	}
	var pending []publishJob
	for _, job := range jobs {
		if job.ExternalDistributionAutoPending != "" {
			pending = append(pending, job)
		}
	}
	if len(pending) == 0 {
		return
	}
	cfg, err := loadExternalTargets()
	if err != nil {
		log.Printf("publish: 自动外发暂停，外发账号读不到：%v", err)
		return
	}
	autoTargets := cfg.autoTargets()
	if len(autoTargets) == 0 {
		// 去掉 auto 标记即关闭自动外发；已排队的待办一并作废，重新打开时不会突然补发。
		for _, job := range pending {
			p.finishExternalAutoPending(job.ID, "自动外发已关闭（没有 auto 账号），待办作废")
		}
		return
	}
	sort.SliceStable(pending, func(i, j int) bool {
		left, right := pending[i], pending[j]
		if (left.ExternalDistributionAutoPending == publishExternalAutoKindPublish) !=
			(right.ExternalDistributionAutoPending == publishExternalAutoKindPublish) {
			return left.ExternalDistributionAutoPending == publishExternalAutoKindPublish
		}
		if left.PublishedAt != right.PublishedAt {
			return left.PublishedAt < right.PublishedAt
		}
		return left.ID < right.ID
	})
	now := pipelineNow(p)
	quota := externalAutoQuotaFor(jobs, autoTargets, now)
	for _, job := range pending {
		kind := job.ExternalDistributionAutoPending
		missing := externalDistributionMissingAutoTargets(job, cfg)
		if len(missing) == 0 {
			// auto 账号都推过了（人工按钮或别的入口先推的），待办作废。
			p.finishExternalAutoPending(job.ID, "")
			continue
		}
		only := map[string]bool{}
		for _, target := range missing {
			// 卡在推送前检查、文章和平台规则都没变的平台等人改，不必每轮去网站取稿重查。
			if externalDraftBlocked(job, target) {
				continue
			}
			if quota.allows(kind, target.ID, now) {
				only[target.ID] = true
			}
		}
		if len(only) == 0 {
			continue
		}
		actor := publishExternalAutoActor
		if kind == publishExternalAutoKindBackfill {
			actor = publishExternalAutoBackfillActor
		}
		updated, queueErr := p.queueAutoExternalDistribution(job.ID, actor, only)
		if queueErr == nil || errors.Is(queueErr, errExternalLanesNotQueued) || errors.Is(queueErr, errExternalLanesNeedFix) {
			for id := range only {
				if lane, ok := externalLaneFor(updated, id); ok && !externalLaneCanSafeRetry(lane) {
					quota.record(kind, id, now)
				}
			}
		}
		switch {
		case queueErr == nil:
			log.Printf("publish: 自动外发已提交（任务 #%d，%s）", job.ID, kind)
		case errors.Is(queueErr, errExternalLanesNeedFix):
			// 有平台要先改标题、摘要或封面：已记待修改并提醒负责人，待办留着，改好再推。
			log.Printf("publish: 自动外发等待修改（任务 #%d）：%v", job.ID, queueErr)
		case errors.Is(queueErr, errExternalLanesNotQueued):
			// 各平台的 payload 都没生成出来，已记为受理前失败（卡片可修正后重推）；
			// 还在等配额的平台留着待办。
			log.Printf("publish: 自动外发未推出（任务 #%d）：%v", job.ID, queueErr)
		case errors.Is(queueErr, errPublishWorkersPaused):
			return
		case isExternalDistributionIneligible(queueErr):
			log.Printf("publish: 自动外发跳过（任务 #%d）：%v", job.ID, queueErr)
			p.finishExternalAutoPending(job.ID, queueErr.Error())
		default:
			log.Printf("publish: 自动外发暂未提交（任务 #%d），下一轮重试：%v", job.ID, queueErr)
			p.noteExternalAutoError(job.ID, queueErr.Error())
		}
	}
}

func (p *publishPipeline) finishExternalAutoPending(jobID int64, reason string) {
	if _, _, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.ExternalDistributionAutoPending != ""
	}, func(current *publishJob) {
		current.ExternalDistributionAutoPending = ""
		current.ExternalDistributionAutoError = truncateRunes(strings.Join(strings.Fields(reason), " "), 300)
	}); err != nil {
		log.Printf("publish: 清除自动外发待办失败（任务 #%d）：%v", jobID, err)
	}
}

func (p *publishPipeline) noteExternalAutoError(jobID int64, reason string) {
	if _, _, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.ExternalDistributionAutoPending != ""
	}, func(current *publishJob) {
		current.ExternalDistributionAutoError = truncateRunes(strings.Join(strings.Fields(reason), " "), 300)
	}); err != nil {
		log.Printf("publish: 记录自动外发错误失败（任务 #%d）：%v", jobID, err)
	}
}

// externalAutoBackfillCandidate 存量补发只收：已发布、没有待办、还有 auto 账号没推过
// （推过的账号不论结果都不补；人工发过的平台先登记为 manual 就不会再推）、按外发口径
// 是原创且有审核记录、公开时间不早于 since。网站稿状态在真正推送前还会再核一次。
func externalAutoBackfillCandidate(job publishJob, cfg externalTargets, since time.Time) bool {
	if job.Stage != publishStagePublished || job.ExternalDistributionAutoPending != "" ||
		len(externalDistributionMissingAutoTargets(job, cfg)) == 0 {
		return false
	}
	publishedAt, err := time.Parse(time.RFC3339, job.PublishedAt)
	if err != nil || publishedAt.Before(since) {
		return false
	}
	return externalDistributionEligibility(job) == nil
}

// markExternalAutoBackfill 管理员补发：把 since 之后公开、还有 auto 账号没推过的原创稿记为补发待办，
// 由 runExternalAutoDistribution 按补发配额逐日推送。dryRun 只返回名单。
func (p *publishPipeline) markExternalAutoBackfill(since time.Time, dryRun bool) ([]publishJob, error) {
	cfg, err := loadExternalTargets()
	if err != nil {
		return nil, err
	}
	if len(cfg.autoTargets()) == 0 {
		return nil, errors.New("没有勾了「自动外发」的号（多维表格「平台账号」）")
	}
	jobs, err := p.store.all()
	if err != nil {
		return nil, err
	}
	var marked []publishJob
	for _, job := range jobs {
		if !externalAutoBackfillCandidate(job, cfg, since) {
			continue
		}
		if dryRun {
			marked = append(marked, job)
			continue
		}
		updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
			return externalAutoBackfillCandidate(current, cfg, since)
		}, func(current *publishJob) {
			current.ExternalDistributionAutoPending = publishExternalAutoKindBackfill
			current.ExternalDistributionAutoError = ""
		})
		if err != nil {
			return marked, err
		}
		if swapped {
			marked = append(marked, updated)
		}
	}
	sort.SliceStable(marked, func(i, j int) bool { return marked[i].PublishedAt < marked[j].PublishedAt })
	if !dryRun && len(marked) > 0 {
		p.kickExternalAutoDistribution()
	}
	return marked, nil
}
