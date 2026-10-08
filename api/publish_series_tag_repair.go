package main

// publish_series_tag_repair.go — 已发布稿补系列标签链（2026-10-08 用户反馈 63149：文末
// 「这是我茶叶系列的第38篇」仍链公众号合集）。按资讯 id 直接改网站现稿，流水线稿和编辑
// 手工稿都适用：只替换系列声明段里的系列名链接，其余正文与字段按 CMS 现稿原样写回。

import (
	"context"
	"fmt"
)

type seriesTagRepairResult struct {
	FoodTalksNewsID int64 `json:"foodtalks_news_id"`
	Changed         bool  `json:"changed"`
	DryRun          bool  `json:"dry_run"`
}

func (p *publishPipeline) repairNewsSeriesTagLink(ctx context.Context, newsID int64, dryRun bool) (seriesTagRepairResult, error) {
	res := seriesTagRepairResult{FoodTalksNewsID: newsID, DryRun: dryRun}
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
