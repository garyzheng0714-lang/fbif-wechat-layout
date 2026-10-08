package main

// publish_series_tag_repair.go — 已发布稿补系列标签链（2026-10-08 用户反馈 63149：文末
// 「这是我茶叶系列的第38篇」仍链公众号合集）。直接改网站现稿：只替换系列声明段里的
// 系列名链接，其余正文与字段按 CMS 现稿原样写回（CMS 手工稿为准）。

import (
	"context"
	"fmt"
)

type seriesTagRepairResult struct {
	JobID           int64 `json:"job_id"`
	FoodTalksNewsID int64 `json:"foodtalks_news_id"`
	Changed         bool  `json:"changed"`
	DryRun          bool  `json:"dry_run"`
}

func (p *publishPipeline) repairPublishedSeriesTagLink(ctx context.Context, jobID int64, dryRun bool) (seriesTagRepairResult, error) {
	res := seriesTagRepairResult{JobID: jobID, DryRun: dryRun}
	if publishWorkersPaused() {
		return res, errPublishWorkersPaused
	}
	job, err := p.store.byID(jobID)
	if err != nil {
		return res, err
	}
	res.FoodTalksNewsID = job.FoodTalksNewsID
	if job.Stage != publishStagePublished || job.FoodTalksNewsID <= 0 {
		return res, fmt.Errorf("任务 #%d 不是已发布稿（%s）", jobID, job.Stage)
	}
	if err := publishStagingTargetMismatch(job); err != nil {
		return res, err
	}
	if publishSiteOpeningSeriesName(job.BlocksJSON) == "" {
		return res, nil
	}
	ft, err := p.foodTalks()
	if err != nil {
		return res, err
	}
	live, err := ft.GetNews(ctx, job.FoodTalksNewsID)
	if err != nil {
		return res, fmt.Errorf("读取网站现稿失败: %w", err)
	}
	content := publishSiteLinkOpeningSeriesTag(live.Content, job.BlocksJSON, p.siteNewsTagsFor(ctx, job))
	if content == live.Content {
		return res, nil
	}
	res.Changed = true
	if dryRun {
		return res, nil
	}
	news := foodTalksNews{ID: job.FoodTalksNewsID, Content: content, TOC: live.TOC}
	preserveFoodTalksLiveMetadata(&news, live)
	if err := ft.UpdateNews(ctx, news); err != nil {
		return res, fmt.Errorf("写回网站失败: %w", err)
	}
	return res, nil
}
