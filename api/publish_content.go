package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	publishContentWebFetch = "web_fetch"
	publishContentSupplier = "supplier"
	publishContentOfficial = "official_api"
)

type publishContentSeed struct {
	Source     string
	Title      string
	SourceName string
	Author     string
	BlocksJSON string
	CoverURL   string
	Hash       string
}

func newPublishContentSeed(source, title, sourceName, author, blocksJSON, coverURL string) publishContentSeed {
	seed := publishContentSeed{
		Source: strings.TrimSpace(source), Title: strings.TrimSpace(title),
		SourceName: strings.TrimSpace(sourceName), Author: strings.TrimSpace(author),
		BlocksJSON: strings.TrimSpace(blocksJSON), CoverURL: strings.TrimSpace(coverURL),
	}
	payload, _ := json.Marshal(struct {
		Title      string `json:"title"`
		SourceName string `json:"source_name"`
		Author     string `json:"author"`
		BlocksJSON string `json:"blocks_json"`
		CoverURL   string `json:"cover_url"`
	}{seed.Title, seed.SourceName, seed.Author, seed.BlocksJSON, seed.CoverURL})
	digest := sha256.Sum256(payload)
	seed.Hash = hex.EncodeToString(digest[:])
	return seed
}

func publishContentRank(source string) int {
	switch source {
	case publishContentOfficial:
		return 3
	case publishContentSupplier:
		return 2
	case publishContentWebFetch:
		return 1
	default:
		return 0
	}
}

func publishContentPromotableStage(job publishJob) bool {
	switch job.Stage {
	case publishStageCreated, publishStageFetching, publishStageRendering,
		publishStageMirroring, publishStageEnriching, publishStageAssessing,
		publishStageAssessingReview, publishStageStaging, publishStageAwaitingReview:
		return true
	case publishStagePublicizing:
		return job.PublicizeIntentKey == ""
	case publishStageFailed:
		return !publishContentHasUncertainIntent(job)
	default:
		return false
	}
}

func publishContentApplyImmediately(job publishJob) bool {
	switch job.Stage {
	case publishStageCreated, publishStageFetching, publishStageAwaitingReview:
		return true
	case publishStagePublicizing:
		return job.PublicizeIntentKey == ""
	case publishStageFailed:
		return !publishContentHasUncertainIntent(job)
	default:
		return false
	}
}

func publishContentHasUncertainIntent(job publishJob) bool {
	stagingUncertain := job.StagingIntentKey != "" && job.FoodTalksNewsID == 0
	return stagingUncertain || job.PublicizeIntentKey != ""
}

func applyPublishContentSeed(job *publishJob, seed publishContentSeed) {
	job.Title = seed.Title
	job.SourceName = seed.SourceName
	job.Author = seed.Author
	job.BlocksJSON = seed.BlocksJSON
	job.CoverURL = seed.CoverURL
	job.ContentSource = seed.Source
	job.ContentHash = seed.Hash
	job.ContentVersion++
	if job.ContentVersion < 1 {
		job.ContentVersion = 1
	}
	job.ContentHTML = ""
	job.RulePresetID = ""
	job.RuleRevision = 0
	job.RuleConfigHash = ""
	job.RuleSnapshotJSON = ""
	job.RendererBuildSHA = ""
	job.RenderOutputHash = ""
	job.ImageMap = ""
	job.Summary = ""
	job.Tags = nil
	job.AISource = ""
	job.AINotes = ""
	job.RiskLevel = ""
	job.RiskItems = nil
	job.RiskHeadline = ""
	job.RiskNotes = ""
	job.RiskRaw = ""
	job.ContentFrozenVersion = 0
	job.ContentFrozenAt = ""
	clearPendingPublishContent(job)
	job.ContentConflict = false
	job.ContentConflictNote = ""
	job.Stage = publishStageRendering
	job.FailedStage = ""
	job.Error = ""
	job.ReviewedBy = ""
	job.ReviewedAt = ""
	job.ReviewAction = ""
	job.ApprovedRiskVersion = ""
	cancelPublishNotification(job)
}

func publishContentRerenderable(job publishJob) bool {
	if job.Stage == publishStageAwaitingReview {
		return true
	}
	return job.Stage == publishStageFailed && !publishContentHasUncertainIntent(job)
}

// rerenderLatest keeps the permanent source_key and existing FoodTalks draft,
// but invalidates the old render/review evidence and runs the persisted neutral
// blocks through the current active rule again. Published/rejected or uncertain
// external-call states stay fail-closed.
func (p *publishPipeline) rerenderLatest(jobID int64) (publishJob, error) {
	return p.rerenderLatestWithRulePack(jobID, "")
}

// restageCurrentAndRerender 丢掉不属于当前环境的旧草稿编号，再按已存
// blocks 重排并在当前环境新建隐藏草稿。切正式站之前的存量单，资讯 id
// 在正式站上往往是另一篇文章，必须先摘锚再 CreateNews，禁止回填
// staging_target 后去 UpdateNews。
func (p *publishPipeline) restageCurrentAndRerender(jobID int64) (publishJob, error) {
	current, err := p.store.byID(jobID)
	if err != nil {
		return publishJob{}, err
	}
	if !publishContentRerenderable(current) {
		return current, fmt.Errorf("任务阶段 %s 不允许重新暂存并排版", current.Stage)
	}
	if publishContentHasUncertainIntent(current) {
		return current, errors.New("任务仍有未确认的外部写入意图，不能重新暂存")
	}
	if current.FoodTalksNewsID > 0 && publishStagingTargetMismatch(current) == nil {
		return p.rerenderLatest(jobID)
	}
	detached, swapped, err := p.store.updateIf(jobID, func(latest publishJob) bool {
		return latest.ContentVersion == current.ContentVersion &&
			publishContentRerenderable(latest) && !publishContentHasUncertainIntent(latest)
	}, func(latest *publishJob) {
		latest.FoodTalksNewsID = 0
		latest.StagingTarget = ""
		latest.StagingIntentKey = ""
		latest.StagingIntentAt = ""
		latest.ArticleURL = ""
	})
	if err != nil {
		return publishJob{}, err
	}
	if !swapped {
		return detached, errors.New("任务状态刚刚变化，请刷新后重试重新暂存")
	}
	return p.rerenderLatest(jobID)
}

// rerenderLatestWithRulePack 与原有重排动作相同，但允许调用方在明确要求时
// 一并切换网站规则包。没有传规则包时沿用任务原绑定，避免重复提交一个 ft-series
// 任务时被可选参数的默认值误切回 ft-default。
func (p *publishPipeline) rerenderLatestWithRulePack(jobID int64, requestedRulePackID string) (publishJob, error) {
	current, err := p.store.byID(jobID)
	if err != nil {
		return publishJob{}, err
	}
	if !publishContentRerenderable(current) {
		return current, fmt.Errorf("任务阶段 %s 不允许按最新规则重新排版", current.Stage)
	}
	if strings.TrimSpace(current.BlocksJSON) == "" {
		return current, errors.New("任务缺少可重放的结构化正文，不能按最新规则重新排版")
	}
	rulePackID := strings.TrimSpace(requestedRulePackID)
	if rulePackID == "" {
		rulePackID, err = publishRulePackIDForJob(current)
	} else {
		rulePackID, err = normalizeSitePublishRulePackID(rulePackID)
	}
	if err != nil {
		return current, err
	}

	source := current.ContentSource
	if source == "" {
		source = publishContentWebFetch
	}
	seed := newPublishContentSeed(
		source,
		current.Title,
		current.SourceName,
		current.Author,
		current.BlocksJSON,
		current.CoverURL,
	)
	reset, swapped, err := p.store.updateIf(jobID, func(latest publishJob) bool {
		return latest.ContentVersion == current.ContentVersion && publishContentRerenderable(latest)
	}, func(latest *publishJob) {
		applyPublishContentSeed(latest, seed)
		latest.RulePackID = rulePackID
	})
	if err != nil {
		return publishJob{}, err
	}
	if !swapped {
		return reset, errors.New("任务状态刚刚变化，请刷新后重试重新排版")
	}

	p.spawn(func() { p.runJob(jobID) })
	if latest, loadErr := p.store.byID(jobID); loadErr == nil {
		return latest, nil
	}
	return reset, nil
}

// refetchLatestWithRulePack 重新从任务保存的公众号原文 URL 抓取 blocks，再复用同一
// FoodTalks 草稿按最新规则排版。它与普通 rerender 分开，避免把“只换规则重排”误变成
// 回源抓取；URL-only 监控重跑历史 supplier 任务时必须显式选择本动作。
func (p *publishPipeline) refetchLatestWithRulePack(ctx context.Context, jobID int64, requestedRulePackID string) (publishJob, error) {
	current, err := p.store.byID(jobID)
	if err != nil {
		return publishJob{}, err
	}
	if !publishContentRerenderable(current) {
		return current, fmt.Errorf("任务阶段 %s 不允许从原文链接重新抓取排版", current.Stage)
	}
	if strings.TrimSpace(current.SourceURL) == "" {
		return current, errors.New("任务缺少公众号原文链接，不能回源重新排版")
	}
	rulePackID := strings.TrimSpace(requestedRulePackID)
	if rulePackID == "" {
		rulePackID, err = publishRulePackIDForJob(current)
	} else {
		rulePackID, err = normalizeSitePublishRulePackID(rulePackID)
	}
	if err != nil {
		return current, err
	}
	if p.fetch == nil {
		return current, errors.New("排版工具缺少原文抓取内核，不能回源重新排版")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	title, sourceName, byline, _, originalSourceURL, blocksJSON, err := p.fetch(withPublishRulePackID(ctx, rulePackID), current.SourceURL)
	if err != nil {
		return current, fmt.Errorf("从公众号原文回源抓取失败: %w", err)
	}
	if strings.TrimSpace(title) == "" {
		title = current.Title
	}
	if strings.TrimSpace(title) == "" {
		return current, errors.New("原文回源未解析出标题，不能重新排版")
	}
	if strings.TrimSpace(blocksJSON) == "" {
		return current, errors.New("原文回源未解析出结构化内容，不能重新排版")
	}
	seed := newPublishContentSeed(publishContentWebFetch, title, sourceName, byline, blocksJSON, current.CoverURL)
	reset, swapped, err := p.store.updateIf(jobID, func(latest publishJob) bool {
		return latest.ContentVersion == current.ContentVersion && publishContentRerenderable(latest)
	}, func(latest *publishJob) {
		applyPublishContentSeed(latest, seed)
		latest.RulePackID = rulePackID
		latest.OriginalSourceURL = originalSourceURL
	})
	if err != nil {
		return publishJob{}, err
	}
	if !swapped {
		return reset, errors.New("任务状态刚刚变化，请刷新后重试从原文链接重新排版")
	}

	p.spawn(func() { p.runJob(jobID) })
	if latest, loadErr := p.store.byID(jobID); loadErr == nil {
		return latest, nil
	}
	return reset, nil
}

func setPendingPublishContent(job *publishJob, seed publishContentSeed, now time.Time) {
	job.PendingContentSource = seed.Source
	job.PendingContentHash = seed.Hash
	job.PendingTitle = seed.Title
	job.PendingSourceName = seed.SourceName
	job.PendingAuthor = seed.Author
	job.PendingBlocksJSON = seed.BlocksJSON
	job.PendingCoverURL = seed.CoverURL
	job.PendingContentAt = now.UTC().Format(time.RFC3339)
	if !publishContentPromotableStage(*job) {
		job.ContentConflict = true
		job.ContentConflictNote = fmt.Sprintf("更高权威内容在任务阶段 %s 到达，未覆盖已冻结版本 %d", job.Stage, job.ContentFrozenVersion)
	}
}

func pendingPublishContent(job publishJob) publishContentSeed {
	return publishContentSeed{
		Source: job.PendingContentSource, Hash: job.PendingContentHash,
		Title: job.PendingTitle, SourceName: job.PendingSourceName,
		Author: job.PendingAuthor, BlocksJSON: job.PendingBlocksJSON,
		CoverURL: job.PendingCoverURL,
	}
}

func clearPendingPublishContent(job *publishJob) {
	job.PendingContentSource = ""
	job.PendingContentHash = ""
	job.PendingTitle = ""
	job.PendingSourceName = ""
	job.PendingAuthor = ""
	job.PendingBlocksJSON = ""
	job.PendingCoverURL = ""
	job.PendingContentAt = ""
}

// mergeIncomingContent 实现来源优先级与晚到内容留痕。返回 restart=true 表示任务已
// 原子重置到 rendering，调用方需要确保有 worker 继续推进。
func (p *publishPipeline) mergeIncomingContent(jobID int64, seed publishContentSeed) (job publishJob, restart bool, err error) {
	if seed.Source == "" || seed.BlocksJSON == "" || seed.Hash == "" {
		return publishJob{}, false, fmt.Errorf("incoming content seed is incomplete")
	}
	now := p.now()
	job, _, err = p.store.updateIf(jobID, func(publishJob) bool { return true }, func(current *publishJob) {
		currentHash := current.ContentHash
		if currentHash == "" && strings.TrimSpace(current.BlocksJSON) != "" {
			currentSeed := newPublishContentSeed(current.ContentSource, current.Title, current.SourceName, current.Author, current.BlocksJSON, current.CoverURL)
			currentHash = currentSeed.Hash
		}
		incomingRank := publishContentRank(seed.Source)
		currentRank := publishContentRank(current.ContentSource)
		if seed.Hash == currentHash {
			if incomingRank > currentRank {
				current.ContentSource = seed.Source
			}
			return
		}
		if incomingRank < currentRank {
			return
		}
		if publishContentApplyImmediately(*current) {
			applyPublishContentSeed(current, seed)
			restart = true
			return
		}
		if publishContentRank(current.PendingContentSource) > incomingRank {
			return
		}
		setPendingPublishContent(current, seed, now)
	})
	return job, restart, err
}

func (p *publishPipeline) promotePendingContent(jobID int64) (publishJob, bool, error) {
	promoted := false
	job, _, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.PendingContentHash != "" && publishContentPromotableStage(current)
	}, func(current *publishJob) {
		applyPublishContentSeed(current, pendingPublishContent(*current))
		promoted = true
	})
	return job, promoted, err
}

func freezePublishContent(job *publishJob, now time.Time) {
	job.ContentFrozenVersion = job.ContentVersion
	job.ContentFrozenAt = now.UTC().Format(time.RFC3339)
}

func publishContentVersionReadyForReview(job publishJob) bool {
	if job.ContentConflict || job.PendingContentHash != "" {
		return false
	}
	// 兼容上线前已经待审的存量任务；新任务从抓取/官方正文入口都会写入版本。
	if job.ContentVersion == 0 && job.ContentFrozenVersion == 0 {
		return true
	}
	return job.ContentVersion > 0 && job.ContentFrozenVersion == job.ContentVersion && job.ContentHash != ""
}
