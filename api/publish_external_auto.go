package main

// publish_external_auto.go — 原创稿公开后自动外发（2026-10-10 用户裁定：原创自动同步头条号）。
//
// 只对 YUNSUO_TARGET_ACCOUNTS_JSON 里标 "auto":true 的账号生效；其余账号仍只走审核卡按钮。
// 任务落为已发布的同一次写入里记下待办（external_distribution_auto_pending），再由
// runExternalAutoDistribution 消化：进程在两步之间重启也不会漏发，每 10 分钟兜底扫一次。
//
// 头条号文章每天约 5 篇上限，超出会被平台拒绝，还会挤掉当天新稿，所以按配额推：
//   - 新公开稿（publish）优先，所有提交给 auto 账号的外发每天（北京时间）合计不超过
//     publishExternalAutoDailyLimit，人工按钮发的也计入；
//   - 存量补发（backfill）每天最多 publishExternalAutoBackfillDailyLimit 篇，只在白天推，
//     与上一次外发至少隔 publishExternalAutoBackfillSpacing，避免集中刷屏被风控。

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

	publishExternalAutoDailyLimit         = 5
	publishExternalAutoBackfillDailyLimit = 3
	publishExternalAutoBackfillSpacing    = time.Hour
	publishExternalAutoBackfillStartHour  = 9
	publishExternalAutoBackfillEndHour    = 20
	publishExternalAutoSweepInterval      = 10 * time.Minute
)

var publishExternalAutoZone = time.FixedZone("CST", 8*3600)

// markExternalAutoDistributionPending 在任务落为已发布的同一次写入里调用：配置了 auto
// 账号、从未外发过、按外发口径是原创，才记待办。转载与待核原创一律不记。
func markExternalAutoDistributionPending(j *publishJob) {
	if j == nil || strings.TrimSpace(j.ExternalDistributionState) != "" || j.ExternalDistributionAutoPending != "" {
		return
	}
	cfg, err := loadYunsuoConfig()
	if err != nil || len(cfg.autoTargets()) == 0 {
		return
	}
	if provenance, _, _ := publishExternalDistributionProvenance(*j); provenance != publishExternalProvenanceOriginal {
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
// 重试都靠它。独立 goroutine，不占发布 worker。
func (p *publishPipeline) startExternalAutoDistribution() {
	if p == nil {
		return
	}
	go func() {
		for {
			p.runExternalAutoDistribution()
			time.Sleep(publishExternalAutoSweepInterval)
		}
	}()
}

// externalAutoQuota 今天（北京时间）已提交给 auto 账号的外发数，以及最近一次提交时间。
type externalAutoQuota struct {
	today    int
	backfill int
	last     time.Time
}

func externalAutoQuotaFor(jobs []publishJob, autoTargets []yunsuoAccountTarget, now time.Time) externalAutoQuota {
	autoIDs := map[string]bool{}
	for _, target := range autoTargets {
		autoIDs[target.ID] = true
	}
	today := now.In(publishExternalAutoZone).Format("2006-01-02")
	var quota externalAutoQuota
	for _, job := range jobs {
		// 云梭受理前就失败的不占平台名额。
		if job.ExternalDistributionState == "" || publishExternalDistributionCanSafeRetry(job) {
			continue
		}
		requestedAt, err := time.Parse(time.RFC3339, job.ExternalDistributionRequestedAt)
		if err != nil {
			continue
		}
		hitsAuto := false
		for _, target := range job.ExternalDistributionTargets {
			if autoIDs[strings.TrimSpace(target.AccountID)] {
				hitsAuto = true
				break
			}
		}
		if !hitsAuto {
			continue
		}
		if requestedAt.After(quota.last) {
			quota.last = requestedAt
		}
		if requestedAt.In(publishExternalAutoZone).Format("2006-01-02") != today {
			continue
		}
		quota.today++
		if job.ExternalDistributionRequestedBy == publishExternalAutoBackfillActor {
			quota.backfill++
		}
	}
	return quota
}

func (q externalAutoQuota) allows(kind string, now time.Time) bool {
	if q.today >= publishExternalAutoDailyLimit {
		return false
	}
	if kind != publishExternalAutoKindBackfill {
		return true
	}
	if q.backfill >= publishExternalAutoBackfillDailyLimit {
		return false
	}
	hour := now.In(publishExternalAutoZone).Hour()
	if hour < publishExternalAutoBackfillStartHour || hour >= publishExternalAutoBackfillEndHour {
		return false
	}
	return q.last.IsZero() || now.Sub(q.last) >= publishExternalAutoBackfillSpacing
}

func (q *externalAutoQuota) record(kind string, now time.Time) {
	q.today++
	if kind == publishExternalAutoKindBackfill {
		q.backfill++
	}
	q.last = now
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
	cfg, err := loadYunsuoConfig()
	if err != nil {
		log.Printf("publish: 自动外发暂停，云梭配置不可用：%v", err)
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
		if strings.TrimSpace(job.ExternalDistributionState) != "" {
			p.finishExternalAutoPending(job.ID, "")
			continue
		}
		if !quota.allows(kind, now) {
			continue
		}
		actor := publishExternalAutoActor
		if kind == publishExternalAutoKindBackfill {
			actor = publishExternalAutoBackfillActor
		}
		_, queueErr := p.queueAutoExternalDistribution(job.ID, actor)
		switch {
		case queueErr == nil:
			quota.record(kind, now)
			log.Printf("publish: 自动外发已提交（任务 #%d，%s）", job.ID, kind)
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

// externalAutoBackfillCandidate 存量补发只收：已发布、从未外发、没有待办、按外发口径
// 是原创且有审核记录、公开时间不早于 since。网站稿状态在真正推送前还会再核一次。
func externalAutoBackfillCandidate(job publishJob, since time.Time) bool {
	if job.Stage != publishStagePublished || strings.TrimSpace(job.ExternalDistributionState) != "" ||
		job.ExternalDistributionAutoPending != "" {
		return false
	}
	publishedAt, err := time.Parse(time.RFC3339, job.PublishedAt)
	if err != nil || publishedAt.Before(since) {
		return false
	}
	return externalDistributionEligibility(job) == nil
}

// markExternalAutoBackfill 管理员补发：把 since 之后公开、从未外发的原创稿记为补发待办，
// 由 runExternalAutoDistribution 按补发配额逐日推送。dryRun 只返回名单。
func (p *publishPipeline) markExternalAutoBackfill(since time.Time, dryRun bool) ([]publishJob, error) {
	cfg, err := loadYunsuoConfig()
	if err != nil {
		return nil, err
	}
	if len(cfg.autoTargets()) == 0 {
		return nil, errors.New("没有配置自动外发账号（YUNSUO_TARGET_ACCOUNTS_JSON 里标 \"auto\":true）")
	}
	jobs, err := p.store.all()
	if err != nil {
		return nil, err
	}
	var marked []publishJob
	for _, job := range jobs {
		if !externalAutoBackfillCandidate(job, since) {
			continue
		}
		if dryRun {
			marked = append(marked, job)
			continue
		}
		updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
			return externalAutoBackfillCandidate(current, since)
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
