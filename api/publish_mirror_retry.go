package main

import (
	"errors"
	"fmt"
	"log"
	"time"
)

// errPublishMirrorTransient 搬图里可能过一会儿就好的失败：转存上游不可达、微信图这一下没取到。
// 原文没有封面这类重试也没用的失败不带它，照旧当场落 failed。
var errPublishMirrorTransient = errors.New("图片转存暂时失败")

// publishMirrorTransientError 给原因打上 errPublishMirrorTransient 记号，报出来的字照旧只有原因本身。
type publishMirrorTransientError struct{ cause error }

func (e publishMirrorTransientError) Error() string { return e.cause.Error() }
func (e publishMirrorTransientError) Is(target error) bool {
	return target == errPublishMirrorTransient
}
func (e publishMirrorTransientError) Unwrap() error { return e.cause }

func publishMirrorTransient(format string, args ...any) error {
	return publishMirrorTransientError{fmt.Errorf(format, args...)}
}

// publishMirrorRetryDelays 搬图失败后第几次自动再试前等多久。微信图偶尔打不开很正常
// （2026-10-18 #296：3 张 mmbiz 图当场转存失败，事后重下都是 200），一次判死只会让任务挂着
// 等人。最长一次等 15 分钟，任务停在 mirroring 不超过监控的 20 分钟「处理卡住」线；
// 五次都不行（约 46 分钟）才落 failed 交人。
var publishMirrorRetryDelays = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	10 * time.Minute,
	15 * time.Minute,
	15 * time.Minute,
}

// scheduleMirrorRetry 把这次搬图失败记成「等会儿再试」，任务留在 mirroring；
// 返回 false 表示次数用完或没装调度器，调用方照常落 failed。
func (p *publishPipeline) scheduleMirrorRetry(job publishJob, cause error) bool {
	if p.retryStageLater == nil {
		return false
	}
	var delay time.Duration
	updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStageMirroring && current.ContentVersion == job.ContentVersion &&
			current.PendingContentHash == "" && current.MirrorAttempts < len(publishMirrorRetryDelays)
	}, func(j *publishJob) {
		delay = publishMirrorRetryDelays[j.MirrorAttempts]
		j.MirrorAttempts++
		j.Error = fmt.Sprintf("搬图没成功，%s后自动再试（第 %d/%d 次）：%s",
			publishRetryDelayText(delay), j.MirrorAttempts, len(publishMirrorRetryDelays), cause.Error())
	})
	if err != nil {
		log.Printf("publish: job %d 记录搬图自动重试失败: %v", job.ID, err)
		return false
	}
	if !swapped {
		return false
	}
	log.Printf("publish: job %d 搬图失败，%s 后第 %d 次自动重试: %v", job.ID, delay, updated.MirrorAttempts, cause)
	jobID, version, attempt := job.ID, updated.ContentVersion, updated.MirrorAttempts
	p.retryStageLater(delay, func() {
		if publishWorkersPaused() {
			return
		}
		// 期间人工处理过、内容换过版本或已被别的 worker 续跑，就不再插手。
		current, err := p.store.byID(jobID)
		if err != nil || current.Stage != publishStageMirroring || current.ContentVersion != version || current.MirrorAttempts != attempt {
			return
		}
		p.spawn(func() { p.runJob(jobID) })
	})
	return true
}

func publishRetryDelayText(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%d 分钟", int(d/time.Minute))
	}
	return d.String()
}
