package main

// publish_series_tag_repair.go — 已发布网站稿补链接（2026-10-08 用户反馈 63149）。按资讯 id
// 直接改网站现稿，流水线稿和编辑手工稿都适用，只动两类链接，其余正文与字段按 CMS 现稿原样写回：
//   - 系列声明段的系列名 → 同名标签页（publishSiteLinkSeriesTags）；
//   - Mote 文章里的公众号文章链 → 已发布的站内 /news/{id}，找不到保留原链（rewriteMoteSeriesLinks）。

import (
	"context"
	"fmt"
)

type newsLinkRepairResult struct {
	FoodTalksNewsID int64 `json:"foodtalks_news_id"`
	Mote            bool  `json:"mote"`
	Changed         bool  `json:"changed"`
	DryRun          bool  `json:"dry_run"`
}

func (p *publishPipeline) repairNewsLinks(ctx context.Context, newsID int64, dryRun bool) (newsLinkRepairResult, error) {
	res := newsLinkRepairResult{FoodTalksNewsID: newsID, DryRun: dryRun}
	if publishWorkersPaused() {
		return res, errPublishWorkersPaused
	}
	if newsID <= 0 {
		return res, fmt.Errorf("资讯 id 无效：%d", newsID)
	}
	ft, err := p.foodTalks()
	if err != nil {
		return res, err
	}
	live, err := ft.GetNews(ctx, newsID)
	if err != nil {
		return res, fmt.Errorf("读取网站现稿失败: %w", err)
	}
	content := publishSiteLinkSeriesTags(live.Content, siteSeriesTagResolver(ctx, ft))
	job := p.newsLinkRepairJob(newsID, live)
	if res.Mote = publishSiteIsMoteArticle(job); res.Mote {
		content = p.rewriteMoteSeriesLinks(ctx, job, content)
	}
	if content == live.Content {
		return res, nil
	}
	res.Changed = true
	if dryRun {
		return res, nil
	}
	news := foodTalksNews{ID: newsID, Content: content, TOC: live.TOC}
	preserveFoodTalksLiveMetadata(&news, live)
	if err := ft.UpdateNews(ctx, news); err != nil {
		return res, fmt.Errorf("写回网站失败: %w", err)
	}
	return res, nil
}

// newsLinkRepairJob 流水线发过的稿用任务库里那单（作者、正文块、公众号身份都在）；编辑手工稿
// 只有网站作者，Mote 判定靠它（流水线稿网站作者常是「FBIF」，所以先查任务库）。
func (p *publishPipeline) newsLinkRepairJob(newsID int64, live foodTalksNewsDetail) publishJob {
	if p.store != nil {
		if jobs, err := p.store.all(); err == nil {
			for _, j := range jobs {
				if j.FoodTalksNewsID == newsID && j.Stage == publishStagePublished {
					return j
				}
			}
		}
	}
	return publishJob{FoodTalksNewsID: newsID, Author: live.Author}
}
