package main

// 同题防重（2026-08-14 用户裁定）：流水线的 source_key 幂等只认自己建的任务，
// 看不见运营在 CMS 后台手工另建的同题文章——实测 #78/#80 的草稿与手工发布的
// 62773/62774 同题并存，审核人一点「通过」就会出现两篇一模一样的公开文章。
// 两道防线：
//  1. 待审核卡发送/刷新时按标题查 CMS 已发布同名稿，卡上标红提醒（best effort）；
//  2. stagePublicize 翻公开前再查一次，命中即 fail closed——手工发布优先（红线 26），
//     宁可发布失败也不产生重复稿；查询本身失败同样 fail closed。

import (
	"context"
	"fmt"
	"log"
	"strings"
)

func normalizeSiteTitle(s string) string {
	s = strings.TrimSpace(s)
	return strings.Join(strings.Fields(s), "")
}

// findPublishedSiteDuplicate 在 CMS 中查找与任务同题、已发布、且不是本任务锚点
// 资讯的文章。命中返回该条列表项。
func (p *publishPipeline) findPublishedSiteDuplicate(ctx context.Context, job publishJob) (foodTalksNewsListItem, bool, error) {
	title := strings.TrimSpace(job.Title)
	if title == "" || p == nil || p.foodTalks == nil {
		return foodTalksNewsListItem{}, false, nil
	}
	ft, err := p.foodTalks()
	if err != nil {
		return foodTalksNewsListItem{}, false, err
	}
	items, err := ft.SearchNewsByTitle(ctx, title)
	if err != nil {
		return foodTalksNewsListItem{}, false, err
	}
	want := normalizeSiteTitle(title)
	for _, item := range items {
		if item.PublishStatus != foodTalksPublishStatusPublished {
			continue
		}
		if item.ID == job.FoodTalksNewsID {
			continue
		}
		if normalizeSiteTitle(item.Title) != want {
			continue
		}
		return item, true, nil
	}
	return foodTalksNewsListItem{}, false, nil
}

// refreshSiteDuplicateFlag 供待审核卡发送/刷新前调用：best effort 更新任务上的
// 同题标记（查询失败只记日志，不阻塞发卡——发布前的硬闸才是安全兜底）。
func (p *publishPipeline) refreshSiteDuplicateFlag(ctx context.Context, job publishJob) publishJob {
	dup, found, err := p.findPublishedSiteDuplicate(ctx, job)
	if err != nil {
		log.Printf("publish: WARN 同题防重查询失败（任务 #%d，卡片提醒可能缺失，发布前仍会硬校验）: %v", job.ID, err)
		return job
	}
	var id int64
	if found {
		id = dup.ID
	}
	if job.SiteDuplicateNewsID == id {
		return job
	}
	updated, updateErr := p.store.update(job.ID, func(current *publishJob) {
		current.SiteDuplicateNewsID = id
	})
	if updateErr != nil {
		log.Printf("publish: WARN 保存同题标记失败（任务 #%d）: %v", job.ID, updateErr)
		job.SiteDuplicateNewsID = id
		return job
	}
	return updated
}

// sitePublishDuplicateGate 翻公开前的硬闸：命中同题已发布稿或查询失败均返回错误。
func (p *publishPipeline) sitePublishDuplicateGate(ctx context.Context, job publishJob) error {
	dup, found, err := p.findPublishedSiteDuplicate(ctx, job)
	if err != nil {
		return fmt.Errorf("同题防重检查失败，按 fail closed 不公开（可重试）: %w", err)
	}
	if found {
		return fmt.Errorf("站上已有同名已发布文章（news/%d，来源 %s）：手工发布优先，不重复发布；请驳回本稿，确需以本稿为准时先下线该文章", dup.ID, strings.TrimSpace(dup.SourceName))
	}
	return nil
}
