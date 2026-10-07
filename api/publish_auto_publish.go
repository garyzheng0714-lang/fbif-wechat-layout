package main

import (
	"context"
	"log"
)

const publishAutoPublishActor = "system:auto_publish"

// publishFirstReviewEntry 在进入待审核前调用：从未发过审核卡、从未有审核记录，才算新同步稿
// 第一次进审核。审核卡在进审核之后才异步发送，所以新稿此刻这些字段都为空。
func publishFirstReviewEntry(j *publishJob) bool {
	return j.NotificationSentAt == "" && j.ReviewCardMsgID == "" && len(j.ReviewCardMsgIDs) == 0 &&
		j.ReviewedAt == "" && j.ReviewedBy == ""
}

func (p *publishPipeline) autoPublishEnabled() bool {
	if p == nil || p.deliverySettings == nil {
		return false
	}
	settings, err := p.deliverySettings.read()
	if err != nil {
		log.Printf("publish: 读取自动发布开关失败：%v", err)
		return false
	}
	return settings.AutoPublish
}

// maybeAutoPublish 草稿进站后，若管理后台打开了自动发布，且风险允许普通「通过」、
// 没有同题已发布稿，则按系统身份持久化 approve 并继续翻公开。失败只留在待审核，
// 不把 staging 本身打成 failed——群卡仍在，人可以点通过或下线。
func (p *publishPipeline) maybeAutoPublish(jobID int64) {
	if p == nil || !p.autoPublishEnabled() {
		return
	}
	job, err := p.store.byID(jobID)
	if err != nil {
		log.Printf("publish: 自动发布读任务 #%d 失败：%v", jobID, err)
		return
	}
	if job.Stage != publishStageAwaitingReview {
		return
	}
	if !job.AutoPublishEligible {
		// 2026-10-08 事故：7 月的待审稿 #19 被回源重排后再次暂存，被当成新稿自动公开。
		log.Printf("publish: 自动发布跳过（任务 #%d）：不是新同步稿第一次进审核，须人工处理", job.ID)
		return
	}
	job = p.refreshSiteDuplicateFlag(context.Background(), job)
	if job.SiteDuplicateNewsID > 0 {
		log.Printf("publish: 自动发布跳过（任务 #%d）：站上已有同名已发布稿 news/%d", job.ID, job.SiteDuplicateNewsID)
		return
	}
	if !publishRiskAllowsManualApprove(job.RiskLevel) {
		log.Printf("publish: 自动发布跳过（任务 #%d）：风险级别 %s 仍需人工确认", job.ID, job.RiskLevel)
		return
	}
	if _, err := p.review(context.Background(), jobID, publishReviewApprove, publishAutoPublishActor); err != nil {
		log.Printf("publish: 自动发布未执行（任务 #%d），留在待审核：%v", job.ID, err)
	}
}
