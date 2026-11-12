package main

// publish_external_retry.go — 外发没成的平台系统自己再来，不等人点「重新同步」
// （2026-11-12 #367 三个平台没发出去，都是人一个个补的；用户：「这样每次都需要人介入的流程不是好的流程，我非常讨厌这样」）。
//
// 分发服务的 lane 失败了（failed），或存好草稿却读回没对上、没读成、没发（draft_saved），按退避自己重推：
// 停在那儿 10 分钟、1 小时、4 小时各一次，之后每天一次，到最初请求后 7 天为止；只在北京时间 9–21 点动手（少碰平台，
// 也不半夜发）。前 3 次都没成才算失败、进 monitoring 告警（连续失败 3 次才找人）；之后每天那一次是给修好代码、
// 人重新扫码登录以后用的：部署了修复、号登上了，不用人再点就接着发出去。
//
// 怎么重推和卡片上点「重新同步」一样（queueExternalDistribution 的 externalQueueAutoRetry）：失败了、草稿已存好的
// 按原来的冻结内容和幂等键再交，分发服务认出那篇再发；没存成的按现在的内容重建（内容没变还是那个键，分发服务对失败过的键
// 从头做）。读回没对上的那篇草稿不能再发，按现在的内容另存一篇：键后面加 .r<第几篇>（robotIdempotencyKey），旧草稿留在
// 草稿箱、没发出去，不碍事；发布前分发服务按标题查重，有人先在平台上把旧草稿发了也不会再发一篇。
// 请求人和请求时间（RequestedBy、RequestedAt）照旧：7 天从最初请求算，发布记的还是原来点的人。
//
// 不碰：待核对（平台上可能有多篇或被人动过）、没通过、已下线、人工已发、正在做的；自动外发推的、号后来取消了「自动外发」的
// 也不碰（取消勾选就是不要系统再往这个号发）。人在卡片上点一次就是新的请求，次数从头算。

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

const (
	// externalAutoRetryWindow 从最初请求算，过了就不再自己重推（卡片上的「重新同步」照旧能点）。
	externalAutoRetryWindow = 7 * 24 * time.Hour
	// externalAutoRetryDaily 前几次退避都没成以后，每天再来一次。
	externalAutoRetryDaily = 24 * time.Hour

	externalAutoRetryStartHour = 9
	externalAutoRetryEndHour   = 21
)

// publishExternalAutoRetryActor 系统自己重推时受理用的名字；lane 上照旧记原来的请求人。
const publishExternalAutoRetryActor = "system:auto_retry"

// externalAutoRetryDelays 停在失败（或没发的草稿）多久以后重推第 1、2、3 次；用完了才告警。
var externalAutoRetryDelays = []time.Duration{10 * time.Minute, time.Hour, 4 * time.Hour}

// externalLaneAutoRetryState 系统可以自己重推的状态：失败，或存好的草稿读回没对上、没发。
// 2026-10-30 之前只存草稿的 draft_saved lane 早过了 7 天，不会被重推。
func externalLaneAutoRetryState(lane publishExternalLane) bool {
	if lane.Driver != externalDriverRobot {
		return false
	}
	switch lane.State {
	case publishExternalDistributionFailed:
		return true
	case publishExternalDistributionDraftSaved:
		return externalLaneDraftSaved(lane)
	default:
		return false
	}
}

// externalLaneNextAutoRetry 这条 lane 下一次系统自己重推的时间（可能已经过了，到时就推）；不会再自己重推的回 false。
// 7 天按此刻算：上线这段之前就停着的旧 lane（2026-10-30 之前只存草稿的、早先失败的）不会一上线就被推一遍。
func externalLaneNextAutoRetry(lane publishExternalLane, target externalAccount, now time.Time) (time.Time, bool) {
	if !externalLaneAutoRetryState(lane) {
		return time.Time{}, false
	}
	switch strings.TrimSpace(lane.RequestedBy) {
	case publishExternalAutoActor, publishExternalAutoBackfillActor:
		if !target.Auto {
			return time.Time{}, false
		}
	}
	requested, err := time.Parse(time.RFC3339, lane.RequestedAt)
	if err != nil || now.Sub(requested) > externalAutoRetryWindow {
		return time.Time{}, false
	}
	stopped, err := time.Parse(time.RFC3339, lane.CompletedAt)
	if err != nil {
		return time.Time{}, false
	}
	delay := externalAutoRetryDaily
	if lane.AutoRetries < len(externalAutoRetryDelays) {
		delay = externalAutoRetryDelays[lane.AutoRetries]
	}
	at := externalAutoRetryDaytime(stopped.Add(delay))
	if at.Sub(requested) > externalAutoRetryWindow {
		return time.Time{}, false
	}
	return at, true
}

// externalAutoRetryDaytime 落在北京时间 9–21 点以外的挪到下一个 9 点。
func externalAutoRetryDaytime(at time.Time) time.Time {
	local := at.In(publishExternalAutoZone)
	start := time.Date(local.Year(), local.Month(), local.Day(), externalAutoRetryStartHour, 0, 0, 0, publishExternalAutoZone)
	end := time.Date(local.Year(), local.Month(), local.Day(), externalAutoRetryEndHour, 0, 0, 0, publishExternalAutoZone)
	switch {
	case local.Before(start):
		return start.UTC()
	case !local.Before(end):
		return start.AddDate(0, 0, 1).UTC()
	default:
		return at.UTC()
	}
}

// externalLaneAwaitingAutoRetry 没成、但前几次退避还没用完、系统还会自己重推的：不算失败、不告警。
func externalLaneAwaitingAutoRetry(job publishJob, lane publishExternalLane, now time.Time) bool {
	if lane.AutoRetries >= len(externalAutoRetryDelays) {
		return false
	}
	cfg, err := loadExternalTargets()
	if err != nil {
		return false
	}
	target, ok := cfg.jobTarget(job, lane.AccountID)
	if !ok {
		return false
	}
	_, ok = externalLaneNextAutoRetry(lane, target, now)
	return ok
}

// externalCardNow 卡片上算还会不会自己重推用的时钟（卡片不经流水线，测试里换掉）。
var externalCardNow = func() time.Time { return time.Now().UTC() }

// noteExternalAutoRetry 卡片上那一行写下一次几点自己重推（写在原因前面，原因长了被截也看得到）。
func noteExternalAutoRetry(state *externalPlatformState, lane publishExternalLane, at time.Time) {
	local := at.In(publishExternalAutoZone)
	when := local.Format("15:04")
	if stopped, err := time.Parse(time.RFC3339, lane.CompletedAt); err != nil ||
		stopped.In(publishExternalAutoZone).Format("2006-01-02") != local.Format("2006-01-02") {
		when = local.Format("01-02 15:04")
	}
	state.Reason = strings.TrimSpace(when + " 自动重试；" + state.Reason)
}

// continueExternalLane 重推时新 lane 接着原来那条记：系统自己重推的照旧记原来的请求人、请求时间和原创确认，
// 次数加一；人点的、管理员重发的是新请求，次数从头算。原来那篇草稿不再用、按现在的内容重建的（rebuilt），换下一篇的键。
func continueExternalLane(lane *publishExternalLane, old publishExternalLane, mode externalDistributionQueueMode, rebuilt bool) {
	lane.AutoRetries = 0
	if mode == externalQueueAutoRetry {
		lane.RequestedBy, lane.RequestedAt = old.RequestedBy, old.RequestedAt
		lane.Attribution, lane.Source = old.Attribution, old.Source
		lane.AutoRetries = old.AutoRetries + 1
	}
	if old.Driver != externalDriverRobot {
		return
	}
	lane.DraftAttempt = old.DraftAttempt
	if rebuilt && strings.TrimSpace(old.ContentPK) != "" {
		lane.DraftAttempt = max(old.DraftAttempt, 1) + 1
	}
}

// retryExternalLanes 到了时间的 lane 按卡片重试的路子重推（自动外发兜底循环里每轮跑一次，只读本地任务，不碰网络）。
// 受理不了的（文章下线了、网站取不到）也记一次，按退避等下一次，不每轮去网站取稿。
func (p *publishPipeline) retryExternalLanes() {
	if p == nil || p.store == nil || publishWorkersPaused() {
		return
	}
	jobs, err := p.store.all()
	if err != nil {
		log.Printf("publish: 外发自动重试读取任务失败：%v", err)
		return
	}
	cfg, err := loadExternalTargets()
	if err != nil {
		log.Printf("publish: 外发自动重试暂停，外发账号读不到：%v", err)
		return
	}
	now := pipelineNow(p)
	for _, job := range jobs {
		// 规则上就不外发的（创新周报）、审核记录对不上的不去推，免得每次都被拒。
		if job.Stage != publishStagePublished || externalDistributionExcluded(job) != "" || !externalDistributionApprovalOK(job) {
			continue
		}
		due := map[string]bool{}
		for _, target := range cfg.forJob(job) {
			lane, ok := externalLaneFor(job, target.ID)
			if !ok {
				continue
			}
			if at, ok := externalLaneNextAutoRetry(lane, target, now); ok && !now.Before(at) {
				due[target.ID] = true
			}
		}
		if len(due) == 0 {
			continue
		}
		if _, err := p.queueExternalDistribution(job.ID, publishExternalAutoRetryActor, "", externalQueueAutoRetry, due); err != nil {
			// 各平台都没生成出推送内容的，受理那一步已经把 lane 记成这次失败了。
			if !errors.Is(err, errExternalLanesNotQueued) {
				p.noteExternalAutoRetryRefused(job.ID, due, err.Error())
			}
			log.Printf("publish: 外发自动重试没推出（任务 #%d）：%v", job.ID, err)
			continue
		}
		log.Printf("publish: 外发自动重试已提交（任务 #%d，%d 个平台）", job.ID, len(due))
	}
}

// noteExternalAutoRetryRefused 这次重推在受理前就被拒了：lane 还是原来的状态，记一次重推、把时间往后挪，原因写上。
func (p *publishPipeline) noteExternalAutoRetryRefused(jobID int64, due map[string]bool, reason string) {
	now := pipelineNow(p).Format(time.RFC3339)
	for accountID := range due {
		_, swapped, err := p.updateExternalLane(jobID, accountID, externalLaneAutoRetryState, func(job *publishJob, lane *publishExternalLane) {
			lane.AutoRetries++
			lane.CompletedAt = now
			lane.Error = robotLaneReason(fmt.Sprintf("自动重试没推出：%s", reason))
			lane.Remark = lane.Error
			enqueueExternalDistributionCardUpdate(job)
		})
		if err != nil {
			log.Printf("publish: 记外发自动重试结果失败（任务 #%d/%s）：%v", jobID, accountID, err)
			continue
		}
		if swapped {
			p.scheduleExternalDistributionCardUpdate(jobID)
		}
	}
}
