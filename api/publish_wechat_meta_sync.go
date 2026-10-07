package main

// publish_wechat_meta_sync.go — 已发布稿的封面/摘要与公众号对齐（2026-10-07 用户裁定：
// 网站摘要要跟公众号文章保持一致；#263 页脚名片封面事故后补修存量）。
//
// 只改两个字段，且只改没人手工动过的：网站现值仍等于流水线当初写的值（或现值本身就是
// 页脚模板图）才替换；正文、标题、标签等全部按 CMS 现稿原样写回（CMS 手工稿为准）。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
)

type wechatMetaSyncResult struct {
	JobID           int64  `json:"job_id"`
	FoodTalksNewsID int64  `json:"foodtalks_news_id"`
	CoverChanged    bool   `json:"cover_changed"`
	SummaryChanged  bool   `json:"summary_changed"`
	CoverKept       string `json:"cover_kept,omitempty"`   // 未替换原因
	SummaryKept     string `json:"summary_kept,omitempty"` // 未替换原因
	Cover           string `json:"cover,omitempty"`
	Summary         string `json:"summary,omitempty"`
}

func (p *publishPipeline) syncPublishedWechatMeta(ctx context.Context, jobID int64) (wechatMetaSyncResult, error) {
	res := wechatMetaSyncResult{JobID: jobID}
	if publishWorkersPaused() {
		return res, errPublishWorkersPaused
	}
	job, err := p.store.byID(jobID)
	if err != nil {
		return res, err
	}
	res.FoodTalksNewsID = job.FoodTalksNewsID
	if job.Stage != publishStagePublished || job.FoodTalksNewsID <= 0 {
		return res, fmt.Errorf("任务 #%d 不是已发布稿（%s），待审核稿请用 refetch_latest 重排", jobID, job.Stage)
	}
	if err := publishStagingTargetMismatch(job); err != nil {
		return res, err
	}
	if p.fetch == nil {
		return res, errors.New("排版工具缺少公众号原文抓取内核")
	}
	rulePackID, err := publishRulePackIDForJob(job)
	if err != nil {
		return res, err
	}
	fetched, err := p.fetch(withPublishRulePackID(ctx, rulePackID), job.SourceURL)
	if err != nil {
		return res, fmt.Errorf("抓取公众号原文失败: %w", err)
	}
	if t := strings.TrimSpace(fetched.Title); t != "" && normalizeSiteTitle(t) != normalizeSiteTitle(job.Title) {
		return res, fmt.Errorf("公众号标题已变为“%s”，为防链接错配未同步", t)
	}

	newCover := ""
	if src := strings.TrimSpace(fetched.CoverURL); src != "" {
		mapped, upErr := p.ossUpload(ctx, []string{src})
		if upErr != nil || strings.TrimSpace(mapped[src]) == "" {
			return res, fmt.Errorf("公众号原封面转存失败: %v", upErr)
		}
		newCover = mapped[src]
	}
	digest := strings.TrimSpace(fetched.Digest)

	ft, err := p.foodTalks()
	if err != nil {
		return res, err
	}
	live, err := ft.GetNews(ctx, job.FoodTalksNewsID)
	if err != nil {
		return res, fmt.Errorf("读取网站现稿失败，未同步: %w", err)
	}
	news := foodTalksNews{ID: job.FoodTalksNewsID, Content: live.Content, TOC: live.TOC}
	preserveFoodTalksLiveMetadata(&news, live)

	coverTemplate := publishCoverIsTemplateImage(job.CoverURL, extractImgSrcs(job.ContentHTML), publishArticleImageSet(job))
	switch {
	case newCover == "":
		res.CoverKept = "公众号原文没有封面"
	case live.CoverImg == newCover:
		res.Cover = newCover
	case live.CoverImg == job.CoverURL || coverTemplate:
		news.CoverImg, res.Cover, res.CoverChanged = newCover, newCover, true
	default:
		res.CoverKept = "网站封面已被人工修改"
	}
	switch {
	case digest == "":
		res.SummaryKept = "公众号原文没有摘要"
	case live.Summary == digest:
		res.Summary = digest
	case live.Summary == job.Summary:
		news.Summary, res.Summary, res.SummaryChanged = digest, digest, true
	default:
		res.SummaryKept = "网站摘要已被人工修改"
	}
	if res.CoverChanged || res.SummaryChanged {
		if err := ft.UpdateNews(ctx, news); err != nil {
			return res, fmt.Errorf("写回网站失败: %w", err)
		}
	}

	storeCoverChanged := res.Cover != "" && res.Cover != job.CoverURL
	_, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.Stage == publishStagePublished && current.ContentVersion == job.ContentVersion
	}, func(j *publishJob) {
		applyWechatSourceMeta(j, fetched)
		if storeCoverChanged {
			j.CoverURL = res.Cover
			j.ReviewCoverImgKey = "" // 卡片封面跟着换，refreshReviewCard 会重新上传
		}
		if res.Summary != "" {
			j.Summary = res.Summary
		}
	})
	if err != nil {
		return res, err
	}
	if !swapped {
		return res, errors.New("同步期间任务状态已变化，本地记录未更新（网站侧已写入的字段保持不变）")
	}
	if storeCoverChanged || res.SummaryChanged {
		if _, cardErr := p.refreshReviewCard(ctx, jobID, false); cardErr != nil {
			log.Printf("publish: WARN 任务 #%d 封面/摘要同步后刷新审核卡失败: %v", jobID, cardErr)
		}
	}
	return res, nil
}
