package main

// publish_external_distribution.go — 已审核原创文章经云梭分发到外部平台。
//
// 这是 publishJob 上的正交子状态机，不改变 FoodTalks 的 Stage：自有站允许原创和
// 转载，外部平台只允许原创。云梭 push 没有幂等键，所以精确 payload 与 intent 必须
// 先落盘；push 结果不确定时只能人工对账。拿到 content_pk 后只做安全的状态查询。

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

const (
	publishExternalDistributionQueued                 = "queued"
	publishExternalDistributionPushing                = "pushing"
	publishExternalDistributionPolling                = "polling"
	publishExternalDistributionSucceeded              = "succeeded"
	publishExternalDistributionPartialFailed          = "partial_failed"
	publishExternalDistributionFailed                 = "failed"
	publishExternalDistributionReconciliationRequired = "reconciliation_required"

	publishExternalTargetQueued      = "queued"
	publishExternalTargetPending     = "pending"
	publishExternalTargetSucceeded   = "succeeded"
	publishExternalTargetFailed      = "failed"
	publishExternalTargetUnsupported = "unsupported"

	publishExternalCardPending    = "pending"
	publishExternalCardSending    = "sending"
	publishExternalCardDeadLetter = "dead_letter"

	publishExternalCardMaxAttempts = 10

	publishExternalProvenanceOriginal   = "original_confirmed"
	publishExternalProvenanceRepost     = "repost"
	publishExternalProvenanceUnverified = "unverified"
)

var (
	publishExternalArticleSourceLine = regexp.MustCompile(`(?i)^(?:本文)?(?:转载自|转自|原文(?:来源|出处)|文章来源|来源|出处|来自(?:微信公众号)?)[：:\s]+(.{1,100})`)
	publishExternalOriginalLine      = regexp.MustCompile(`(?i)^本文为(.{1,80}?)原创(?:[《，,。.!！]|$)`)
	publishExternalAuthorizedRepost  = regexp.MustCompile(`(?i)(?:本文)?(?:经|获).{1,50}(?:授权)?转载|reprinted\s+with\s+permission|originally\s+published\s+by|^source\s*[:：]`)
)

type publishExternalDistributionTarget struct {
	AccountID        string `json:"account_id"`
	Name             string `json:"name"`
	State            string `json:"state"`
	ProviderRecordID string `json:"provider_record_id,omitempty"`
	PlatformName     string `json:"platform_name,omitempty"`
	PublishTime      string `json:"publish_time,omitempty"`
	RawStatus        int    `json:"raw_status,omitempty"`
	Remark           string `json:"remark,omitempty"`
	FailedCount      int    `json:"failed_count,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

func publishExternalDistributionProcessing(state string) bool {
	switch strings.TrimSpace(state) {
	case publishExternalDistributionQueued, publishExternalDistributionPushing, publishExternalDistributionPolling:
		return true
	default:
		return false
	}
}

// failed 且尚未拿到 content_pk，表示 POST 之前失败或云梭明确拒绝，能够安全地
// 在修正配置/参数后重新提交。拿到 content_pk 的平台终态失败不走这里，避免重复稿。
func publishExternalDistributionCanSafeRetry(job publishJob) bool {
	return job.ExternalDistributionState == publishExternalDistributionFailed &&
		strings.TrimSpace(job.ExternalDistributionContentPK) == ""
}

// publishExternalDistributionProvenance 比 FoodTalks 的来源展示更严格：自有号只是
// 必要条件；显式“转载自/来源/某媒体原创”、风险检查里的转载证据都会否决。AI 预填
// 未执行且正文也没有自有原创声明时视为未核验，卡片不开放分发按钮。
func publishExternalDistributionProvenance(job publishJob) (status, source, evidence string) {
	original, source := publishArticleProvenance(job)
	if !original {
		return publishExternalProvenanceRepost, source, "文章来源已判定为转载"
	}
	own := siteOwnAccounts()
	explicitOriginal := false
	var blocks []Block
	if err := json.Unmarshal([]byte(job.BlocksJSON), &blocks); err == nil {
		for _, block := range blocks {
			if block.K == "cap" || block.K == "img" || block.K == "video" || block.K == "table" {
				continue
			}
			line := publishExternalDistributionBlockText(block)
			if line == "" {
				continue
			}
			compact := strings.Map(func(r rune) rune {
				if r == '*' || r == '＊' || r == '_' {
					return -1
				}
				return r
			}, line)
			compact = strings.TrimSpace(compact)
			if explicitSiteRepostDisclosure.MatchString(strings.ReplaceAll(compact, " ", "")) ||
				strings.Contains(compact, "本文为转载") || publishExternalAuthorizedRepost.MatchString(compact) {
				return publishExternalProvenanceRepost, source, compact
			}
			if match := publishExternalOriginalLine.FindStringSubmatch(compact); len(match) > 1 {
				declaredOwner := cleanPublishExternalSource(match[1])
				if publishExternalSourceIsOwn(declaredOwner, own) {
					explicitOriginal = true
					continue
				}
				return publishExternalProvenanceRepost, declaredOwner, compact
			}
			if match := publishExternalArticleSourceLine.FindStringSubmatch(compact); len(match) > 1 {
				declaredSource := cleanPublishExternalSource(match[1])
				if declaredSource != "" && !publishExternalSourceIsOwn(declaredSource, own) {
					return publishExternalProvenanceRepost, declaredSource, compact
				}
			}
		}
	}
	for _, item := range job.RiskItems {
		text := strings.TrimSpace(item.Evidence + " " + item.Reason)
		if strings.Contains(text, "转载自") || strings.Contains(text, "转自") ||
			strings.Contains(text, "原文来源") || strings.Contains(text, "转载授权") {
			return publishExternalProvenanceUnverified, source, "检查报告仍有转载来源待核提示"
		}
	}
	if publishRiskHasSystemFailure(job.RiskItems) {
		return publishExternalProvenanceUnverified, source, "风险与转载来源检查未完成"
	}
	notes := strings.TrimSpace(job.AINotes)
	if !explicitOriginal && (notes == "" || strings.Contains(notes, "AI 预填未执行")) {
		return publishExternalProvenanceUnverified, source, "自动出处核验未完成"
	}
	return publishExternalProvenanceOriginal, source, "自有账号且未发现转载出处；由操作人最终确认原创"
}

func publishExternalDistributionBlockText(block Block) string {
	if len(block.Runs) == 0 {
		return strings.Join(strings.Fields(block.Text), " ")
	}
	var value strings.Builder
	for _, run := range block.Runs {
		if run.Type != "img" && run.Type != "br" {
			value.WriteString(run.Text)
		}
	}
	return strings.Join(strings.Fields(value.String()), " ")
}

func cleanPublishExternalSource(value string) string {
	value = strings.TrimSpace(value)
	if cut := strings.IndexAny(value, "，,。；;|｜\n\r"); cut >= 0 {
		value = value[:cut]
	}
	return strings.Trim(strings.TrimSpace(value), "《》【】()（）:：")
}

func publishExternalSourceIsOwn(value string, own map[string]bool) bool {
	normalize := func(raw string) string {
		return strings.ToLower(strings.Map(func(r rune) rune {
			switch r {
			case ' ', '\t', '\r', '\n', '（', '）', '(', ')', '：', ':', '，', ',', '。', '《', '》':
				return -1
			default:
				return r
			}
		}, raw))
	}
	want := normalize(value)
	if len([]rune(want)) < 4 {
		return false
	}
	for candidate := range own {
		got := normalize(candidate)
		if want == got || strings.Contains(want, got) || strings.Contains(got, want) {
			return true
		}
	}
	return false
}

// publishExternalDistributionCardVersion 把按钮绑定到网站终态、冻结正文、最终文章来源
// 判定和当前服务端目标目录。调用方不能在 callback value 中任意塞 account_id。
func publishExternalDistributionCardVersion(job publishJob) string {
	provenance, source, _ := publishExternalDistributionProvenance(job)
	material, _ := json.Marshal(struct {
		Title    string   `json:"title"`
		Summary  string   `json:"summary"`
		Tags     []string `json:"tags"`
		CoverURL string   `json:"cover_url"`
		ImageMap string   `json:"image_map"`
	}{job.Title, job.Summary, job.Tags, job.CoverURL, job.ImageMap})
	materialDigest := sha256.Sum256(material)
	targetIDs := []string{}
	if cfg, err := loadYunsuoConfig(); err == nil {
		for _, target := range cfg.Targets {
			targetIDs = append(targetIDs, strings.TrimSpace(target.ID))
		}
	}
	payload, _ := json.Marshal(struct {
		ID            int64    `json:"id"`
		Stage         string   `json:"stage"`
		ContentHash   string   `json:"content_hash"`
		Content       int64    `json:"content_version"`
		FrozenContent int64    `json:"frozen_content_version"`
		ApprovedRisk  string   `json:"approved_risk_version"`
		Provenance    string   `json:"provenance"`
		Source        string   `json:"source"`
		MaterialHash  string   `json:"material_hash"`
		State         string   `json:"state"`
		Targets       []string `json:"targets"`
	}{
		ID: job.ID, Stage: job.Stage, ContentHash: job.ContentHash,
		Content: job.ContentVersion, FrozenContent: job.ContentFrozenVersion,
		ApprovedRisk: job.ApprovedRiskVersion, Provenance: provenance, Source: source,
		MaterialHash: fmt.Sprintf("sha256:%x", materialDigest[:]),
		State:        job.ExternalDistributionState, Targets: targetIDs,
	})
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("distribution-v1:%d:%x", job.ID, digest[:8])
}

func externalDistributionVersionMatches(expected string, job publishJob) bool {
	expected = strings.TrimSpace(expected)
	want := publishExternalDistributionCardVersion(job)
	return expected != "" && len(expected) == len(want) &&
		subtle.ConstantTimeCompare([]byte(expected), []byte(want)) == 1
}

func externalDistributionEligibility(job publishJob) error {
	if job.Stage != publishStagePublished {
		return fmt.Errorf("只有已在 FoodTalks 发布的文章才能同步外部平台，当前阶段为 %s", job.Stage)
	}
	if !validPersistedPublishApproval(job) {
		return errors.New("文章缺少与当前内容版本匹配的审核通过记录，不能同步外部平台")
	}
	provenance, _, evidence := publishExternalDistributionProvenance(job)
	if provenance == publishExternalProvenanceRepost {
		return errors.New("转载文章仅发布至 FoodTalks，不允许同步头条号、百家号等外部平台")
	}
	if provenance != publishExternalProvenanceOriginal {
		return fmt.Errorf("文章原创状态未完成核验，暂不允许同步外部平台：%s", evidence)
	}
	if strings.TrimSpace(job.BlocksJSON) == "" {
		return errors.New("文章缺少中立正文，不能生成外部分发内容")
	}
	return nil
}

// requestExternalDistribution 只受理一次人工动作：先构造并持久化精确 payload 快照，
// 再把实际 HTTP 交给 worker。卡片回调因此能在 3 秒内返回，不等待云梭。
func (p *publishPipeline) requestExternalDistribution(jobID int64, actor, expectedVersion string) (publishJob, error) {
	if publishWorkersPaused() {
		job, _ := p.store.byID(jobID)
		return job, errPublishWorkersPaused
	}
	job, err := p.store.byID(jobID)
	if err != nil {
		return publishJob{}, err
	}
	if strings.TrimSpace(job.ExternalDistributionState) != "" && !publishExternalDistributionCanSafeRetry(job) {
		return job, errors.New("这篇文章已经提交过外部分发，系统不会重复发布")
	}
	if strings.TrimSpace(actor) == "" {
		return job, errors.New("外部分发必须由审核人从飞书卡片确认原创后发起")
	}
	if !externalDistributionVersionMatches(expectedVersion, job) {
		return job, errors.New("分发卡片已过期，请使用最新卡片")
	}
	if err := externalDistributionEligibility(job); err != nil {
		return job, err
	}
	cfg, err := loadYunsuoConfig()
	if err != nil {
		return job, err
	}
	payload, err := buildYunsuoPushPayload(job, cfg.Targets, cfg.Categories)
	if err != nil {
		return job, err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return job, fmt.Errorf("保存云梭分发内容失败: %w", err)
	}
	payloadDigest := sha256.Sum256(payloadJSON)
	requestedAt := pipelineNow(p).Format(time.RFC3339)
	_, attributionSource, attributionEvidence := publishExternalDistributionProvenance(job)
	targets := make([]publishExternalDistributionTarget, 0, len(cfg.Targets))
	for _, target := range cfg.Targets {
		targets = append(targets, publishExternalDistributionTarget{
			AccountID: target.ID, Name: target.Name, State: publishExternalTargetQueued,
		})
	}
	updated, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return (strings.TrimSpace(current.ExternalDistributionState) == "" || publishExternalDistributionCanSafeRetry(current)) &&
			externalDistributionVersionMatches(expectedVersion, current) &&
			externalDistributionEligibility(current) == nil
	}, func(current *publishJob) {
		current.ExternalDistributionState = publishExternalDistributionQueued
		current.ExternalDistributionTargets = append([]publishExternalDistributionTarget(nil), targets...)
		current.ExternalDistributionRequestedBy = strings.TrimSpace(actor)
		current.ExternalDistributionRequestedAt = requestedAt
		current.ExternalDistributionContentHash = current.ContentHash
		current.ExternalDistributionContentVersion = current.ContentFrozenVersion
		if current.ExternalDistributionContentVersion == 0 {
			current.ExternalDistributionContentVersion = current.ContentVersion
		}
		current.ExternalDistributionAttribution = "original_human_confirmed: " + attributionEvidence
		current.ExternalDistributionSource = attributionSource
		current.ExternalDistributionPayloadJSON = string(payloadJSON)
		current.ExternalDistributionPayloadHash = fmt.Sprintf("sha256:%x", payloadDigest[:])
		current.ExternalDistributionIntentKey = ""
		current.ExternalDistributionIntentAt = ""
		current.ExternalDistributionContentPK = ""
		current.ExternalDistributionError = ""
		current.ExternalDistributionCompletedAt = ""
		current.ExternalDistributionPollAttempts = 0
		current.ExternalDistributionLastPolledAt = ""
		enqueueExternalDistributionCardUpdate(current)
	})
	if err != nil {
		return job, err
	}
	if !swapped {
		return updated, errors.New("文章状态、内容版本或分发目标已变化，请使用最新卡片")
	}
	// 先让同一张卡进入“正在同步”状态，再启动外调；两个 outbox 的发送入口会
	// 串行并重读最新任务，即使 worker 更快完成也不会让旧卡覆盖终态。
	p.scheduleExternalDistributionCardUpdate(updated.ID)
	p.spawnExternalWork(func() { p.runExternalDistribution(jobID) })
	return updated, nil
}

func (p *publishPipeline) runExternalDistribution(jobID int64) {
	if publishWorkersPaused() {
		return
	}
	job, err := p.store.byID(jobID)
	if err != nil {
		log.Printf("publish: load external distribution job %d failed: %v", jobID, err)
		return
	}
	if job.ExternalDistributionState == publishExternalDistributionQueued {
		p.pushExternalDistribution(job)
		return
	}
	if job.ExternalDistributionState == publishExternalDistributionPolling &&
		strings.TrimSpace(job.ExternalDistributionContentPK) != "" {
		p.pollExternalDistribution(job)
	}
}

func (p *publishPipeline) pushExternalDistribution(job publishJob) {
	var payload yunsuoPushPayload
	if err := json.Unmarshal([]byte(job.ExternalDistributionPayloadJSON), &payload); err != nil {
		p.failExternalDistributionBeforePush(job.ID, "已保存的云梭分发内容无法解析："+err.Error())
		return
	}
	clientFactory := p.yunsuo
	if clientFactory == nil {
		clientFactory = func() (yunsuoAPI, error) { return newYunsuoClientFromEnv() }
	}
	client, err := clientFactory()
	if err != nil {
		p.failExternalDistributionBeforePush(job.ID, err.Error())
		return
	}
	intentAt := pipelineNow(p).Format(time.RFC3339)
	intentKey := fmt.Sprintf("yunsuo-push:%d:%s", job.ID, strings.TrimPrefix(job.ExternalDistributionPayloadHash, "sha256:"))
	claimed, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.ExternalDistributionState == publishExternalDistributionQueued &&
			current.ExternalDistributionIntentKey == "" &&
			current.ExternalDistributionPayloadHash == job.ExternalDistributionPayloadHash
	}, func(current *publishJob) {
		current.ExternalDistributionState = publishExternalDistributionPushing
		current.ExternalDistributionIntentKey = intentKey
		current.ExternalDistributionIntentAt = intentAt
		for i := range current.ExternalDistributionTargets {
			current.ExternalDistributionTargets[i].State = publishExternalTargetPending
		}
	})
	if err != nil || !swapped {
		if err != nil {
			log.Printf("publish: persist yunsuo intent for job %d failed: %v", job.ID, err)
		}
		return
	}
	contentPK, pushErr := client.Push(context.Background(), payload)
	if pushErr != nil {
		if isYunsuoRejectedError(pushErr) {
			p.finishExternalDistributionPushFailure(claimed, publishExternalDistributionFailed, pushErr.Error())
			return
		}
		// 未分类错误也按不确定处理：进入 push 外调窗口后宁可人工核对，绝不重复 POST。
		p.finishExternalDistributionPushFailure(claimed, publishExternalDistributionReconciliationRequired, pushErr.Error())
		return
	}
	contentPK = strings.TrimSpace(contentPK)
	if contentPK == "" {
		p.finishExternalDistributionPushFailure(claimed, publishExternalDistributionReconciliationRequired, "云梭已响应但缺少 content_pk")
		return
	}
	polling, persisted, persistErr := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.ExternalDistributionState == publishExternalDistributionPushing &&
			current.ExternalDistributionIntentKey == intentKey
	}, func(current *publishJob) {
		current.ExternalDistributionState = publishExternalDistributionPolling
		current.ExternalDistributionContentPK = contentPK
		current.ExternalDistributionError = ""
	})
	if persistErr != nil || !persisted {
		// 远端已经接受而 content_pk 未可靠落盘；保留 pushing intent，启动恢复会转人工核对。
		log.Printf("publish: yunsuo accepted job %d but content_pk persistence failed: %v", job.ID, persistErr)
		return
	}
	p.pollExternalDistributionWithClient(polling, client)
}

func (p *publishPipeline) failExternalDistributionBeforePush(jobID int64, reason string) {
	updated, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.ExternalDistributionState == publishExternalDistributionQueued &&
			current.ExternalDistributionIntentKey == ""
	}, func(current *publishJob) {
		current.ExternalDistributionState = publishExternalDistributionFailed
		current.ExternalDistributionError = truncateRunes(strings.Join(strings.Fields(reason), " "), 500)
		current.ExternalDistributionCompletedAt = pipelineNow(p).Format(time.RFC3339)
		for i := range current.ExternalDistributionTargets {
			current.ExternalDistributionTargets[i].State = publishExternalTargetFailed
			current.ExternalDistributionTargets[i].Remark = current.ExternalDistributionError
		}
		enqueueExternalDistributionCardUpdate(current)
	})
	if err != nil {
		log.Printf("publish: record pre-push failure for job %d failed: %v", jobID, err)
		return
	}
	if swapped {
		p.scheduleExternalDistributionCardUpdate(updated.ID)
	}
}

func (p *publishPipeline) finishExternalDistributionPushFailure(job publishJob, state, reason string) {
	updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.ExternalDistributionState == publishExternalDistributionPushing &&
			current.ExternalDistributionIntentKey == job.ExternalDistributionIntentKey
	}, func(current *publishJob) {
		current.ExternalDistributionState = state
		current.ExternalDistributionError = truncateRunes(strings.Join(strings.Fields(reason), " "), 500)
		current.ExternalDistributionCompletedAt = pipelineNow(p).Format(time.RFC3339)
		for i := range current.ExternalDistributionTargets {
			if state == publishExternalDistributionFailed {
				current.ExternalDistributionTargets[i].State = publishExternalTargetFailed
			} else {
				current.ExternalDistributionTargets[i].State = publishExternalTargetPending
			}
			current.ExternalDistributionTargets[i].Remark = current.ExternalDistributionError
		}
		enqueueExternalDistributionCardUpdate(current)
	})
	if err != nil {
		log.Printf("publish: record yunsuo push result for job %d failed: %v", job.ID, err)
		return
	}
	if swapped {
		p.scheduleExternalDistributionCardUpdate(updated.ID)
	}
}

func (p *publishPipeline) pollExternalDistribution(job publishJob) {
	clientFactory := p.yunsuo
	if clientFactory == nil {
		clientFactory = func() (yunsuoAPI, error) { return newYunsuoClientFromEnv() }
	}
	client, err := clientFactory()
	if err != nil {
		p.finishExternalDistributionPolling(job, publishExternalDistributionReconciliationRequired, nil,
			"无法恢复云梭状态查询："+err.Error())
		return
	}
	p.pollExternalDistributionWithClient(job, client)
}

func (p *publishPipeline) pollExternalDistributionWithClient(job publishJob, client yunsuoAPI) {
	interval := p.externalDistributionPollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	timeout := p.externalDistributionPollTimeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	startedAt, err := time.Parse(time.RFC3339, job.ExternalDistributionIntentAt)
	if err != nil {
		startedAt, err = time.Parse(time.RFC3339, job.ExternalDistributionRequestedAt)
	}
	if err != nil {
		startedAt = time.Now().UTC()
	}
	statuses, statusErr := client.Status(context.Background(), job.ExternalDistributionContentPK)
	nowTime := pipelineNow(p)
	now := nowTime.Format(time.RFC3339)
	if statusErr != nil && isYunsuoRejectedError(statusErr) {
		p.finishExternalDistributionPolling(job, publishExternalDistributionReconciliationRequired,
			job.ExternalDistributionTargets, "云梭状态查询被明确拒绝，请人工核对："+statusErr.Error())
		return
	}
	if statusErr == nil {
		merged := mergeExternalDistributionStatuses(job.ExternalDistributionTargets, statuses, now)
		state, terminal := classifyExternalDistributionTargets(merged)
		if terminal {
			p.finishExternalDistributionPolling(job, state, merged, "")
			return
		}
		updated, swapped, updateErr := p.store.updateIf(job.ID, func(current publishJob) bool {
			return current.ExternalDistributionState == publishExternalDistributionPolling &&
				current.ExternalDistributionContentPK == job.ExternalDistributionContentPK
		}, func(current *publishJob) {
			current.ExternalDistributionTargets = merged
			current.ExternalDistributionPollAttempts++
			current.ExternalDistributionLastPolledAt = now
			current.ExternalDistributionError = ""
		})
		if updateErr != nil || !swapped {
			return
		}
		job = updated
	} else {
		updated, swapped, updateErr := p.store.updateIf(job.ID, func(current publishJob) bool {
			return current.ExternalDistributionState == publishExternalDistributionPolling &&
				current.ExternalDistributionContentPK == job.ExternalDistributionContentPK
		}, func(current *publishJob) {
			current.ExternalDistributionPollAttempts++
			current.ExternalDistributionLastPolledAt = now
			current.ExternalDistributionError = truncateRunes(strings.Join(strings.Fields(statusErr.Error()), " "), 500)
		})
		if updateErr != nil || !swapped {
			return
		}
		job = updated
	}
	if !nowTime.Before(startedAt.Add(timeout)) {
		p.finishExternalDistributionPolling(job, publishExternalDistributionReconciliationRequired,
			job.ExternalDistributionTargets, "云梭在轮询时限内未返回全部终态；系统不会重复发布，请人工核对")
		return
	}
	// 每次只占用 worker 做一次查询，延时器到点后再入队；多篇文章同时分发不会
	// 长时间占满四个发布 worker。状态已落盘，进程重启由 recoverJobs 继续查询。
	if p.retryNotification != nil {
		p.retryNotification(interval, func() { p.spawnExternalWork(func() { p.runExternalDistribution(job.ID) }) })
		return
	}
	time.AfterFunc(interval, func() { p.spawnExternalWork(func() { p.runExternalDistribution(job.ID) }) })
}

func mergeExternalDistributionStatuses(current []publishExternalDistributionTarget, statuses []yunsuoAccountStatus, now string) []publishExternalDistributionTarget {
	out := append([]publishExternalDistributionTarget(nil), current...)
	used := make([]bool, len(statuses))
	statusesHaveAccountIDs := false
	for _, status := range statuses {
		if strings.TrimSpace(status.AccountPool.ID) != "" {
			statusesHaveAccountIDs = true
			break
		}
	}
	for i := range out {
		match := -1
		for j, status := range statuses {
			if used[j] {
				continue
			}
			if strings.TrimSpace(status.AccountPool.ID) == out[i].AccountID ||
				(strings.TrimSpace(status.AccountPool.ID) == "" && strings.TrimSpace(status.AccountPool.Name) == out[i].Name) {
				match = j
				break
			}
		}
		// 兼容供应商省略账号对象但仍按请求顺序返回的响应。
		if match < 0 && !statusesHaveAccountIDs && i < len(statuses) && !used[i] {
			match = i
		}
		if match < 0 {
			continue
		}
		used[match] = true
		status := statuses[match]
		out[i].ProviderRecordID = strings.TrimSpace(status.PK)
		out[i].PlatformName = strings.TrimSpace(status.PlatformName)
		out[i].PublishTime = strings.TrimSpace(status.PublishTime)
		out[i].RawStatus = status.Status
		out[i].Remark = truncateRunes(strings.Join(strings.Fields(status.Remark), " "), 300)
		out[i].FailedCount = status.FailedCount
		out[i].UpdatedAt = now
		switch status.Status {
		case 0:
			out[i].State = publishExternalTargetPending
		case 1:
			out[i].State = publishExternalTargetSucceeded
		case 2:
			out[i].State = publishExternalTargetFailed
		case 3:
			out[i].State = publishExternalTargetUnsupported
		default:
			// 未知供应商状态绝不能当成功，继续查询直到时限后人工核对。
			out[i].State = publishExternalTargetPending
		}
	}
	return out
}

func classifyExternalDistributionTargets(targets []publishExternalDistributionTarget) (string, bool) {
	if len(targets) == 0 {
		return publishExternalDistributionFailed, true
	}
	succeeded, terminal := 0, 0
	for _, target := range targets {
		switch target.State {
		case publishExternalTargetSucceeded:
			succeeded++
			terminal++
		case publishExternalTargetFailed, publishExternalTargetUnsupported:
			terminal++
		}
	}
	if terminal != len(targets) {
		return publishExternalDistributionPolling, false
	}
	if succeeded == len(targets) {
		return publishExternalDistributionSucceeded, true
	}
	if succeeded > 0 {
		return publishExternalDistributionPartialFailed, true
	}
	return publishExternalDistributionFailed, true
}

func (p *publishPipeline) finishExternalDistributionPolling(job publishJob, state string, targets []publishExternalDistributionTarget, reason string) {
	updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.ExternalDistributionState == publishExternalDistributionPolling &&
			current.ExternalDistributionContentPK == job.ExternalDistributionContentPK
	}, func(current *publishJob) {
		current.ExternalDistributionState = state
		if targets != nil {
			current.ExternalDistributionTargets = append([]publishExternalDistributionTarget(nil), targets...)
		}
		current.ExternalDistributionError = truncateRunes(strings.Join(strings.Fields(reason), " "), 500)
		current.ExternalDistributionCompletedAt = pipelineNow(p).Format(time.RFC3339)
		enqueueExternalDistributionCardUpdate(current)
	})
	if err != nil {
		log.Printf("publish: record yunsuo status for job %d failed: %v", job.ID, err)
		return
	}
	if swapped {
		p.scheduleExternalDistributionCardUpdate(updated.ID)
	}
}

func enqueueExternalDistributionCardUpdate(job *publishJob) {
	job.ExternalDistributionCardSeq++
	job.ExternalDistributionCardState = publishExternalCardPending
	job.ExternalDistributionCardAttempts = 0
	job.ExternalDistributionCardError = ""
}

func (p *publishPipeline) scheduleExternalDistributionCardUpdate(jobID int64) {
	p.spawn(func() { p.dispatchExternalDistributionCardUpdate(jobID) })
}

func (p *publishPipeline) dispatchExternalDistributionCardUpdate(jobID int64) {
	claimed, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.ExternalDistributionCardState == publishExternalCardPending
	}, func(current *publishJob) {
		current.ExternalDistributionCardState = publishExternalCardSending
		current.ExternalDistributionCardAttempts++
		current.ExternalDistributionCardError = ""
	})
	if err != nil || !swapped {
		return
	}
	err = p.sendTerminalCardNotification(context.Background(), claimed, fmt.Sprintf(
		"【外部分发】任务 #%d 状态已更新\n标题：%s", claimed.ID, claimed.Title))
	if err == nil {
		_, _, ackErr := p.store.updateIf(jobID, func(current publishJob) bool {
			return current.ExternalDistributionCardSeq == claimed.ExternalDistributionCardSeq &&
				current.ExternalDistributionCardState == publishExternalCardSending
		}, func(current *publishJob) {
			current.ExternalDistributionCardState = ""
			current.ExternalDistributionCardError = ""
			current.ExternalDistributionCardUpdatedAt = pipelineNow(p).Format(time.RFC3339)
		})
		if ackErr != nil {
			log.Printf("publish: ack external distribution card for job %d failed: %v", jobID, ackErr)
		}
		return
	}
	failed, restored, persistErr := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.ExternalDistributionCardSeq == claimed.ExternalDistributionCardSeq &&
			current.ExternalDistributionCardState == publishExternalCardSending
	}, func(current *publishJob) {
		if current.ExternalDistributionCardAttempts >= publishExternalCardMaxAttempts {
			current.ExternalDistributionCardState = publishExternalCardDeadLetter
		} else {
			current.ExternalDistributionCardState = publishExternalCardPending
		}
		current.ExternalDistributionCardError = truncateRunes(strings.Join(strings.Fields(err.Error()), " "), 500)
	})
	if persistErr != nil {
		return
	}
	if restored && failed.ExternalDistributionCardState == publishExternalCardPending && p.retryNotification != nil {
		delay := publishNotificationRetryDelay(failed.ExternalDistributionCardAttempts)
		p.retryNotification(delay, func() { p.scheduleExternalDistributionCardUpdate(jobID) })
	}
}

func (p *publishPipeline) recoverExternalDistribution(job publishJob) {
	if job.ExternalDistributionCardState == publishExternalCardSending {
		reset, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
			return current.ExternalDistributionCardState == publishExternalCardSending
		}, func(current *publishJob) {
			current.ExternalDistributionCardState = publishExternalCardPending
		})
		if err == nil && swapped {
			p.scheduleExternalDistributionCardUpdate(reset.ID)
		}
	} else if job.ExternalDistributionCardState == publishExternalCardPending {
		p.scheduleExternalDistributionCardUpdate(job.ID)
	}

	switch job.ExternalDistributionState {
	case publishExternalDistributionQueued:
		p.spawnExternalWork(func() { p.runExternalDistribution(job.ID) })
	case publishExternalDistributionPushing:
		// intent 已落盘，但进程可能在 POST 前后任一点退出；结果不可判定，禁止重放。
		p.finishExternalDistributionPushFailure(job, publishExternalDistributionReconciliationRequired,
			"进程在云梭推送窗口中断，系统不会重复发布，请人工核对")
	case publishExternalDistributionPolling:
		if strings.TrimSpace(job.ExternalDistributionContentPK) == "" {
			p.finishExternalDistributionPolling(job, publishExternalDistributionReconciliationRequired,
				job.ExternalDistributionTargets,
				"云梭轮询状态缺少 content_pk，系统不会重复发布，请人工核对")
			return
		}
		p.spawnExternalWork(func() { p.runExternalDistribution(job.ID) })
	}
}

func externalDistributionCardStateOutcome(job publishJob) (reviewCardOutcome, bool) {
	switch job.ExternalDistributionState {
	case "":
		return reviewCardOutcome{}, false
	case publishExternalDistributionQueued, publishExternalDistributionPushing, publishExternalDistributionPolling:
		return reviewCardOutcome{
			toastType: "info", toastContent: "文章正在同步分发，请勿重复操作",
		}, true
	case publishExternalDistributionSucceeded:
		return reviewCardOutcome{
			toastType: "info", toastContent: "文章已同步至其他平台，无需重复操作",
		}, true
	case publishExternalDistributionFailed:
		if publishExternalDistributionCanSafeRetry(job) {
			return reviewCardOutcome{}, false
		}
		fallthrough
	case publishExternalDistributionPartialFailed:
		return reviewCardOutcome{
			toastType: "error", toastContent: "外部分发有平台未完成，请查看卡片并到云梭后台处理",
		}, true
	case publishExternalDistributionReconciliationRequired:
		return reviewCardOutcome{
			toastType: "error", toastContent: "外部分发结果待核对，系统不会重复发布",
		}, true
	default:
		return reviewCardOutcome{
			toastType: "error", toastContent: "外部分发状态异常，请到管理后台核对",
		}, true
	}
}
