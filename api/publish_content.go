package main

import (
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
