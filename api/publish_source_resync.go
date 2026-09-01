package main

// publish_source_resync.go — 已发布稿从原公众号重新抓取、排版并覆盖
// 同一个 FoodTalks 资讯 ID。它不改主 Stage；外调前先核对 CMS 现状，
// 发现人工改稿就停，已进入 PUT 窗口后则只能人工对账。

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"slices"
	"strings"
	"time"
)

const (
	publishSourceResyncQueued                 = "queued"
	publishSourceResyncPreparing              = "preparing"
	publishSourceResyncUpdating               = "updating"
	publishSourceResyncSucceeded              = "succeeded"
	publishSourceResyncFailed                 = "failed"
	publishSourceResyncReconciliationRequired = "reconciliation_required"
)

func publishSourceResyncProcessing(state string) bool {
	switch strings.TrimSpace(state) {
	case publishSourceResyncQueued, publishSourceResyncPreparing, publishSourceResyncUpdating:
		return true
	default:
		return false
	}
}

func publishSourceResyncBlocksOtherActions(job publishJob) bool {
	return publishSourceResyncProcessing(job.SourceResyncState) ||
		job.SourceResyncState == publishSourceResyncReconciliationRequired
}

func publishSourceResyncCardVersion(job publishJob) string {
	payload, _ := json.Marshal(struct {
		ID              int64  `json:"id"`
		Stage           string `json:"stage"`
		NewsID          int64  `json:"news_id"`
		ContentHash     string `json:"content_hash"`
		ContentVersion  int64  `json:"content_version"`
		SiteContentHash string `json:"site_content_hash"`
		State           string `json:"state"`
		CompletedAt     string `json:"completed_at"`
	}{
		ID: job.ID, Stage: job.Stage, NewsID: job.FoodTalksNewsID,
		ContentHash: job.ContentHash, ContentVersion: job.ContentVersion,
		SiteContentHash: job.SiteContentHash, State: job.SourceResyncState,
		CompletedAt: job.SourceResyncCompletedAt,
	})
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("source-resync-v1:%d:%x", job.ID, digest[:8])
}

func publishSourceResyncVersionMatches(expected string, job publishJob) bool {
	expected = strings.TrimSpace(expected)
	want := publishSourceResyncCardVersion(job)
	return expected != "" && len(expected) == len(want) &&
		subtle.ConstantTimeCompare([]byte(expected), []byte(want)) == 1
}

func validPublishedSourceResyncApproval(job publishJob) bool {
	if job.Stage != publishStagePublished ||
		strings.TrimSpace(job.SourceResyncApprovedBy) == "" ||
		strings.TrimSpace(job.SourceResyncApprovedAt) == "" ||
		strings.TrimSpace(job.SourceResyncApprovedVersion) == "" ||
		!publishContentVersionReadyForReview(job) {
		return false
	}
	want := publishReviewCardVersion(job)
	return len(want) == len(job.SourceResyncApprovedVersion) &&
		subtle.ConstantTimeCompare([]byte(want), []byte(job.SourceResyncApprovedVersion)) == 1
}

func sourceResyncEligibility(job publishJob) error {
	if job.Stage != publishStagePublished {
		return fmt.Errorf("只有已在 FoodTalks 发布的文章才能重新同步，当前阶段为 %s", job.Stage)
	}
	if job.PublicationAction == publishPublicationActionManualCMS {
		return errors.New("这篇是后台人工发布稿，流水线不能覆盖")
	}
	if job.FoodTalksNewsID <= 0 || strings.TrimSpace(job.SourceURL) == "" {
		return errors.New("任务缺少 FoodTalks 资讯 ID 或公众号原文链接")
	}
	if err := publishStagingTargetMismatch(job); err != nil {
		return err
	}
	if publishSourceResyncBlocksOtherActions(job) {
		return errors.New("这篇文章正在重新同步，或上次结果待人工核对")
	}
	if strings.TrimSpace(job.ExternalDistributionState) != "" {
		return errors.New("这篇文章已发起外部平台分发，为避免多平台版本分叉，不能再直接覆盖")
	}
	if !validPersistedPublishApproval(job) {
		return errors.New("文章缺少与当前内容版本匹配的审核通过记录")
	}
	return nil
}

func sourceResyncCardStateOutcome(job publishJob) (reviewCardOutcome, bool) {
	if publishSourceResyncProcessing(job.SourceResyncState) {
		return reviewCardOutcome{
			toastType: "info", toastContent: "文章正在重新同步，请稍候",
			card: buildReviewDoneCard(job),
		}, true
	}
	if job.SourceResyncState == publishSourceResyncReconciliationRequired {
		return reviewCardOutcome{
			toastType: "error", toastContent: "更新结果待人工核对，系统不会重复覆盖",
			card: buildReviewDoneCard(job),
		}, true
	}
	return reviewCardOutcome{}, false
}

func (p *publishPipeline) handleSourceResyncCardAction(job publishJob, actor, version string) reviewCardOutcome {
	if outcome, handled := sourceResyncCardStateOutcome(job); handled {
		return outcome
	}
	if !publishSourceResyncVersionMatches(version, job) {
		return reviewCardOutcome{toastType: "error", toastContent: "同步卡片已过期，请使用最新卡片"}
	}
	next, err := p.requestSourceResync(job.ID, actor, version)
	if err != nil {
		return reviewCardOutcome{toastType: "error", toastContent: err.Error()}
	}
	return reviewCardOutcome{
		toastType: "success", toastContent: "已受理，正在重新抓取并覆盖同一篇网站文章",
		card: buildReviewDoneCard(next),
	}
}

func (p *publishPipeline) requestSourceResync(jobID int64, actor, expectedVersion string) (publishJob, error) {
	if publishWorkersPaused() {
		job, _ := p.store.byID(jobID)
		return job, errPublishWorkersPaused
	}
	job, err := p.store.byID(jobID)
	if err != nil {
		return publishJob{}, err
	}
	if strings.TrimSpace(actor) == "" {
		return job, errors.New("重新同步必须由审核人从飞书卡片手动发起")
	}
	if err := sourceResyncEligibility(job); err != nil {
		return job, err
	}
	if !publishSourceResyncVersionMatches(expectedVersion, job) {
		return job, errors.New("同步卡片已过期，请使用最新卡片")
	}
	requestedAt := pipelineNow(p).Format(time.RFC3339Nano)
	updated, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return publishSourceResyncVersionMatches(expectedVersion, current) && sourceResyncEligibility(current) == nil
	}, func(current *publishJob) {
		current.SourceResyncState = publishSourceResyncQueued
		current.SourceResyncRequestedBy = strings.TrimSpace(actor)
		current.SourceResyncRequestedAt = requestedAt
		current.SourceResyncCompletedAt = ""
		current.SourceResyncError = ""
		current.SourceResyncIntentKey = ""
		current.SourceResyncIntentAt = ""
		cancelPublishNotification(current)
	})
	if err != nil {
		return publishJob{}, err
	}
	if !swapped {
		return updated, errors.New("文章状态刚刚变化，请刷新卡片后重试")
	}
	p.spawn(func() { p.runSourceResync(jobID) })
	if latest, loadErr := p.store.byID(jobID); loadErr == nil {
		return latest, nil
	}
	return updated, nil
}

func (p *publishPipeline) runSourceResync(jobID int64) {
	job, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.Stage == publishStagePublished && current.SourceResyncState == publishSourceResyncQueued
	}, func(current *publishJob) {
		current.SourceResyncState = publishSourceResyncPreparing
		current.SourceResyncError = ""
	})
	if err != nil {
		log.Printf("publish: job %d 认领重新同步失败: %v", jobID, err)
		return
	}
	if !swapped {
		return
	}
	ctx := context.Background()
	candidate, nextNews, err := p.prepareSourceResync(ctx, job)
	if err != nil {
		p.markSourceResyncFailed(job, err)
		return
	}
	previousNews, err := p.buildSiteNews(ctx, job, foodTalksPublishStatusPublished)
	if err != nil {
		p.markSourceResyncFailed(job, err)
		return
	}
	ft, err := p.foodTalks()
	if err != nil {
		p.markSourceResyncFailed(job, err)
		return
	}
	live, err := ft.GetNews(ctx, job.FoodTalksNewsID)
	if err != nil {
		p.markSourceResyncFailed(job, fmt.Errorf("更新前读取 FoodTalks 现稿失败，未覆盖: %w", err))
		return
	}
	if changes := foodTalksManualChangeFields(live, previousNews, job.SiteContentHash); len(changes) > 0 {
		p.markSourceResyncFailed(job, fmt.Errorf("检测到 FoodTalks 后台已有人工修改（%s），未覆盖", strings.Join(changes, "、")))
		return
	}
	if len(foodTalksManualChangeFields(live, nextNews, "")) == 0 {
		p.finishSourceResync(job, candidate, nextNews, publishSourceResyncPreparing, "")
		return
	}
	p.updateSourceResync(ctx, ft, job, candidate, nextNews)
}

func (p *publishPipeline) prepareSourceResync(ctx context.Context, job publishJob) (publishJob, foodTalksNews, error) {
	rulePackID, err := publishRulePackIDForJob(job)
	if err != nil {
		return job, foodTalksNews{}, err
	}
	blocksJSON, originalSourceURL, err := p.fetchSourceResyncBlocks(ctx, job, rulePackID)
	if err != nil {
		return job, foodTalksNews{}, err
	}
	candidate := job
	candidate.BlocksJSON = strings.TrimSpace(blocksJSON)
	candidate.OriginalSourceURL = preferOriginalSourceURL(originalSourceURL, job.OriginalSourceURL)
	p.ensureRepostOriginalURL(ctx, &candidate)
	result, err := p.render(withPublishRulePackID(ctx, rulePackID), candidate.Title, publishRenderSourceURL(candidate), candidate.BlocksJSON, true)
	if err != nil {
		return job, foodTalksNews{}, err
	}
	if err := validatePublishRenderResultForPack(result, rulePackID); err != nil {
		return job, foodTalksNews{}, err
	}
	if leak := publishHTMLWechatTrailerLeak(result.HTML); leak != "" {
		return job, foodTalksNews{}, fmt.Errorf("排版结果仍含公众号导流尾（%s），未覆盖", leak)
	}
	html, imageMap, err := p.mirrorSourceResyncImages(ctx, result.HTML, job.ImageMap, job.CoverURL)
	if err != nil {
		return job, foodTalksNews{}, err
	}
	seed := newPublishContentSeed(publishContentWebFetch, job.Title, job.SourceName, job.Author, candidate.BlocksJSON, job.CoverURL)
	candidate.ContentSource = publishContentWebFetch
	candidate.ContentHash = seed.Hash
	candidate.ContentVersion = job.ContentVersion + 1
	if candidate.ContentVersion < 1 {
		candidate.ContentVersion = 1
	}
	candidate.ContentHTML = html
	candidate.ImageMap = imageMap
	candidate.RulePresetID = result.ResolvedRule.PresetID
	candidate.RuleRevision = result.ResolvedRule.Revision
	candidate.RuleConfigHash = result.ResolvedRule.ConfigHash
	candidate.RuleSnapshotJSON = ""
	candidate.RendererBuildSHA = result.RendererBuildSHA
	candidate.RenderOutputHash = result.OutputHash
	freezePublishContent(&candidate, pipelineNow(p))
	news, err := p.buildSiteNews(ctx, candidate, foodTalksPublishStatusPublished)
	return candidate, news, err
}

func (p *publishPipeline) fetchSourceResyncBlocks(ctx context.Context, job publishJob, rulePackID string) (string, string, error) {
	if p.fetch == nil {
		return "", "", errors.New("排版工具缺少公众号原文抓取内核")
	}
	title, _, _, _, originalSourceURL, blocksJSON, err := p.fetch(withPublishRulePackID(ctx, rulePackID), job.SourceURL)
	if err != nil {
		return "", "", fmt.Errorf("从公众号原文重新抓取失败: %w", err)
	}
	blocksJSON = strings.TrimSpace(blocksJSON)
	if blocksJSON == "" {
		return "", "", errors.New("公众号原文未解析出可排版正文")
	}
	if strings.TrimSpace(title) != "" && normalizeSiteTitle(title) != normalizeSiteTitle(job.Title) {
		return "", "", fmt.Errorf("公众号标题已变为“%s”，为防止链接错配未覆盖网站稿", strings.TrimSpace(title))
	}
	return blocksJSON, originalSourceURL, nil
}

func (p *publishPipeline) mirrorSourceResyncImages(ctx context.Context, rawHTML, rawMap, cover string) (string, string, error) {
	imageMap := map[string]string{}
	if strings.TrimSpace(rawMap) != "" {
		if err := json.Unmarshal([]byte(rawMap), &imageMap); err != nil {
			return "", "", fmt.Errorf("已发布稿的图片映射无效: %w", err)
		}
	}
	known := map[string]bool{strings.TrimSpace(cover): true}
	for source, target := range imageMap {
		known[strings.TrimSpace(source)] = true
		known[strings.TrimSpace(target)] = true
	}
	pending := unpublishedImageSources(rawHTML, known)
	if err := p.uploadSourceResyncImages(ctx, pending, imageMap); err != nil {
		return "", "", err
	}
	html := rewriteImgSrcs(rawHTML, imageMap)
	if leak := publishHTMLWechatTrailerLeak(html); leak != "" {
		return "", "", fmt.Errorf("图片转存后正文仍含公众号导流尾（%s）", leak)
	}
	mapJSON, err := json.Marshal(imageMap)
	if err != nil {
		return "", "", err
	}
	return html, string(mapJSON), nil
}

func (p *publishPipeline) uploadSourceResyncImages(ctx context.Context, pending []string, imageMap map[string]string) error {
	if len(pending) > 0 && p.ossUpload == nil {
		return errors.New("最新正文含未转存图片，但当前没有 OSS 转存通道")
	}
	for attempt := 0; len(pending) > 0 && attempt < 2; attempt++ {
		results, err := p.ossUpload(ctx, pending)
		if err != nil {
			return fmt.Errorf("最新正文图片转存失败: %w", err)
		}
		missing := pending[:0]
		for _, source := range pending {
			if target := strings.TrimSpace(results[source]); target != "" {
				imageMap[source] = target
			} else {
				missing = append(missing, source)
			}
		}
		pending = missing
	}
	if len(pending) > 0 {
		return fmt.Errorf("以下图片转存失败，拒绝用缺图正文覆盖已发布稿：%s", strings.Join(pending, "、"))
	}
	return nil
}

func unpublishedImageSources(rawHTML string, known map[string]bool) []string {
	pending := []string{}
	seen := map[string]bool{}
	for _, source := range extractImgSrcs(rawHTML) {
		source = strings.TrimSpace(source)
		if source == "" || strings.HasPrefix(source, "data:") || known[source] || seen[source] {
			continue
		}
		seen[source] = true
		pending = append(pending, source)
	}
	return pending
}

func (p *publishPipeline) updateSourceResync(ctx context.Context, ft foodTalksAPI, job, candidate publishJob, news foodTalksNews) {
	intentKey := fmt.Sprintf("foodtalks-source-resync:job-%d:%s", job.ID, candidate.ContentHash)
	claimed, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStagePublished && current.SourceResyncState == publishSourceResyncPreparing &&
			current.SourceResyncRequestedAt == job.SourceResyncRequestedAt && current.ContentVersion == job.ContentVersion
	}, func(current *publishJob) {
		current.SourceResyncState = publishSourceResyncUpdating
		current.SourceResyncIntentKey = intentKey
		current.SourceResyncIntentAt = pipelineNow(p).Format(time.RFC3339Nano)
	})
	if err != nil {
		p.markSourceResyncFailed(job, fmt.Errorf("持久化网站覆盖意图失败: %w", err))
		return
	}
	if !swapped {
		return
	}
	news.ID = job.FoodTalksNewsID
	if err := ft.UpdateNews(ctx, news); err != nil {
		p.markSourceResyncReconciliation(job.ID, intentKey, fmt.Sprintf("UpdateNews 结果不确定，系统不会自动重试（intent=%s）：%v", intentKey, err))
		return
	}
	p.finishSourceResync(claimed, candidate, news, publishSourceResyncUpdating, intentKey)
}

func (p *publishPipeline) finishSourceResync(job, candidate publishJob, news foodTalksNews, expectedState, intentKey string) {
	completedAt := pipelineNow(p).Format(time.RFC3339Nano)
	updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStagePublished && current.SourceResyncState == expectedState &&
			current.SourceResyncRequestedAt == job.SourceResyncRequestedAt && current.SourceResyncIntentKey == intentKey
	}, func(current *publishJob) {
		current.OriginalSourceURL = candidate.OriginalSourceURL
		current.BlocksJSON = candidate.BlocksJSON
		current.ContentHTML = candidate.ContentHTML
		current.ImageMap = candidate.ImageMap
		current.ContentSource = candidate.ContentSource
		current.ContentHash = candidate.ContentHash
		current.ContentVersion = candidate.ContentVersion
		current.ContentFrozenVersion = candidate.ContentFrozenVersion
		current.ContentFrozenAt = candidate.ContentFrozenAt
		current.RulePresetID = candidate.RulePresetID
		current.RuleRevision = candidate.RuleRevision
		current.RuleConfigHash = candidate.RuleConfigHash
		current.RuleSnapshotJSON = ""
		current.RendererBuildSHA = candidate.RendererBuildSHA
		current.RenderOutputHash = candidate.RenderOutputHash
		current.SiteContentHash = publishRenderOutputHash(news.Content)
		current.SourceResyncState = publishSourceResyncSucceeded
		current.SourceResyncCompletedAt = completedAt
		current.SourceResyncError = ""
		current.SourceResyncIntentKey = ""
		current.SourceResyncIntentAt = ""
		current.SourceResyncApprovedBy = current.SourceResyncRequestedBy
		current.SourceResyncApprovedAt = completedAt
		current.SourceResyncApprovedVersion = publishReviewCardVersion(*current)
		enqueuePublishNotification(current, publishNotificationSourceResyncSucceeded)
	})
	if err == nil && swapped {
		p.schedulePublishNotification(updated.ID)
		return
	}
	if latest, loadErr := p.store.byID(job.ID); loadErr == nil && validPublishedSourceResyncApproval(latest) {
		p.schedulePublishNotification(latest.ID)
		return
	}
	if expectedState == publishSourceResyncUpdating {
		reason := fmt.Sprintf("FoodTalks 已返回覆盖成功，但本地终态落盘不确定（intent=%s）", intentKey)
		if err != nil {
			reason += "：" + err.Error()
		}
		p.markSourceResyncReconciliation(job.ID, intentKey, reason)
		return
	}
	p.markSourceResyncFailed(job, errors.New("最新正文已无需覆盖，但本地版本落盘失败，请重试"))
}

func (p *publishPipeline) markSourceResyncFailed(job publishJob, cause error) {
	reason := truncateRunes(strings.Join(strings.Fields(cause.Error()), " "), 500)
	updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStagePublished && current.SourceResyncState == publishSourceResyncPreparing &&
			current.SourceResyncRequestedAt == job.SourceResyncRequestedAt && current.SourceResyncIntentKey == ""
	}, func(current *publishJob) {
		current.SourceResyncState = publishSourceResyncFailed
		current.SourceResyncCompletedAt = pipelineNow(p).Format(time.RFC3339Nano)
		current.SourceResyncError = reason
		enqueuePublishNotification(current, publishNotificationSourceResyncFailed)
	})
	if err != nil {
		log.Printf("publish: job %d 记录重新同步失败状态失败: %v", job.ID, err)
		return
	}
	if swapped {
		p.schedulePublishNotification(updated.ID)
	}
}

func (p *publishPipeline) markSourceResyncReconciliation(jobID int64, intentKey, reason string) {
	updated, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.Stage == publishStagePublished && current.SourceResyncState == publishSourceResyncUpdating &&
			current.SourceResyncIntentKey == intentKey
	}, func(current *publishJob) {
		current.SourceResyncState = publishSourceResyncReconciliationRequired
		current.SourceResyncError = truncateRunes(strings.Join(strings.Fields(reason), " "), 500)
		enqueuePublishNotification(current, publishNotificationSourceResyncUncertain)
	})
	if err != nil {
		log.Printf("publish: job %d 记录重新同步待对账状态失败: %v", jobID, err)
		return
	}
	if swapped {
		p.schedulePublishNotification(updated.ID)
	}
}

func (p *publishPipeline) recoverSourceResync(job publishJob) {
	switch job.SourceResyncState {
	case publishSourceResyncQueued:
		p.spawn(func() { p.runSourceResync(job.ID) })
	case publishSourceResyncPreparing:
		reset, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
			return current.Stage == publishStagePublished && current.SourceResyncState == publishSourceResyncPreparing &&
				current.SourceResyncIntentKey == ""
		}, func(current *publishJob) {
			current.SourceResyncState = publishSourceResyncQueued
		})
		if err != nil {
			log.Printf("publish: job %d 恢复重新同步准备态失败: %v", job.ID, err)
		} else if swapped {
			p.spawn(func() { p.runSourceResync(reset.ID) })
		}
	case publishSourceResyncUpdating:
		p.markSourceResyncReconciliation(job.ID, job.SourceResyncIntentKey,
			"进程在 FoodTalks UpdateNews 调用窗口中断，结果不确定，启动时禁止自动重放")
	}
}

type foodTalksSiteMetadata struct {
	Title, CoverImg, SourceName, Author, Summary, TOC string
	Language, SEOTitle, SEOKeywords, SEODescription   string
	SourceType, PublishStatus, IsPublic, IsLatest     int
	SourceID                                          int64
	ImgPreviewEnable                                  bool
	CommentEnable                                     int
	PublishTime                                       string
	PrimaryCategoryID                                 int64
	CategoryIDs, TopicSectionIDs                      []int64
	NewsTags                                          []foodTalksNewsTag
	NewsRelationIDs, RelatedCompanyIDs                []int64
	RelatedFoodpediaIDs                               []int64
}

func foodTalksManualChangeFields(live foodTalksNewsDetail, expected foodTalksNews, siteContentHash string) []string {
	changes := []string{}
	contentMatches := live.Content == expected.Content
	if strings.TrimSpace(siteContentHash) != "" {
		contentMatches = publishRenderOutputHash(live.Content) == siteContentHash
	}
	if !contentMatches {
		changes = append(changes, "正文")
	}
	if !reflect.DeepEqual(foodTalksLiveMetadata(live), foodTalksExpectedMetadata(expected)) {
		changes = append(changes, "标题/封面/分类/标签等发布字段")
	}
	return changes
}

func foodTalksExpectedMetadata(news foodTalksNews) foodTalksSiteMetadata {
	metadata := foodTalksSiteMetadata{
		Title: news.Title, CoverImg: news.CoverImg, SourceName: news.SourceName,
		Author: news.Author, Summary: news.Summary, TOC: news.TOC, Language: news.Language,
		SEOTitle: news.SEOTitle, SEOKeywords: news.SEOKeywords, SEODescription: news.SEODescription,
		SourceType: news.SourceType, SourceID: news.SourceID, PublishStatus: news.PublishStatus,
		IsPublic: news.IsPublic, IsLatest: news.IsLatest, ImgPreviewEnable: news.ImgPreviewEnable,
		CommentEnable: news.CommentEnable, PublishTime: news.PublishTime,
		PrimaryCategoryID: news.PrimaryCategoryID,
		CategoryIDs:       append([]int64{}, news.CategoryIDs...), TopicSectionIDs: append([]int64{}, news.TopicSectionIDList...),
		NewsTags: append([]foodTalksNewsTag{}, news.NewsTags...), NewsRelationIDs: append([]int64{}, news.NewsRelationIDs...),
		RelatedCompanyIDs: append([]int64{}, news.RelatedCompanyIDs...), RelatedFoodpediaIDs: append([]int64{}, news.RelatedFoodpediaIDs...),
	}
	return normalizeFoodTalksSiteMetadata(metadata)
}

func foodTalksLiveMetadata(news foodTalksNewsDetail) foodTalksSiteMetadata {
	primary, categories := foodTalksDetailCategoryIDs(news.NewsCategories)
	metadata := foodTalksSiteMetadata{
		Title: news.Title, CoverImg: news.CoverImg, SourceName: news.SourceName,
		Author: news.Author, Summary: news.Summary, TOC: news.TOC, Language: news.Language,
		SEOTitle: news.SEOTitle, SEOKeywords: news.SEOKeywords, SEODescription: news.SEODescription,
		SourceType: news.SourceType, SourceID: news.SourceID, PublishStatus: news.PublishStatus,
		IsLatest: news.IsLatest, ImgPreviewEnable: news.ImgPreviewEnable,
		CommentEnable: news.CommentEnable, PublishTime: news.PublishTime,
		PrimaryCategoryID: primary, CategoryIDs: categories,
		TopicSectionIDs: append([]int64{}, news.TopicSectionIDList...), NewsTags: append([]foodTalksNewsTag{}, news.NewsTags...),
		NewsRelationIDs:     foodTalksDetailNewsRelationIDs(news.NewsRelation),
		RelatedCompanyIDs:   foodTalksDetailCompanyIDs(news.RelatedCompanies),
		RelatedFoodpediaIDs: foodTalksDetailFoodpediaIDs(news.RelatedFoodpedias),
	}
	if news.IsPublic {
		metadata.IsPublic = 1
	}
	return normalizeFoodTalksSiteMetadata(metadata)
}

func normalizeFoodTalksSiteMetadata(metadata foodTalksSiteMetadata) foodTalksSiteMetadata {
	slices.Sort(metadata.CategoryIDs)
	slices.Sort(metadata.TopicSectionIDs)
	slices.Sort(metadata.NewsRelationIDs)
	slices.Sort(metadata.RelatedCompanyIDs)
	slices.Sort(metadata.RelatedFoodpediaIDs)
	slices.SortFunc(metadata.NewsTags, func(a, b foodTalksNewsTag) int {
		if a.TagID < b.TagID {
			return -1
		}
		if a.TagID > b.TagID {
			return 1
		}
		return strings.Compare(a.TagName, b.TagName)
	})
	return metadata
}

func foodTalksDetailCategoryIDs(categories []foodTalksNewsCategoryDetail) (int64, []int64) {
	var primary int64
	ids := []int64{}
	var walk func([]foodTalksNewsCategoryDetail)
	walk = func(items []foodTalksNewsCategoryDetail) {
		for _, item := range items {
			if item.CategoryTagID > 0 {
				ids = append(ids, item.CategoryTagID)
			}
			if item.IsPrimary == 1 {
				primary = item.CategoryTagID
			}
			walk(item.ChildCategories)
		}
	}
	walk(categories)
	return primary, ids
}

func foodTalksDetailNewsRelationIDs(items []foodTalksNewsRelationDetail) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		id := item.RelationNewsID
		if id == 0 {
			id = item.NewsID
		}
		if id == 0 {
			id = item.ID
		}
		ids = append(ids, id)
	}
	return ids
}

func foodTalksDetailCompanyIDs(items []foodTalksRelatedCompanyDetail) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		id := item.CompanyID
		if id == 0 {
			id = item.ID
		}
		ids = append(ids, id)
	}
	return ids
}

func foodTalksDetailFoodpediaIDs(items []foodTalksRelatedFoodpedia) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		id := item.FoodpediaID
		if id == 0 {
			id = item.ID
		}
		ids = append(ids, id)
	}
	return ids
}
