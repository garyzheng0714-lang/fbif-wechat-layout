package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type wechatTrailerRepairItem struct {
	JobID           int64  `json:"job_id"`
	FoodTalksNewsID int64  `json:"foodtalks_news_id,omitempty"`
	Title           string `json:"title,omitempty"`
	Leak            string `json:"leak,omitempty"`
	Error           string `json:"error,omitempty"`
	Repaired        bool   `json:"repaired,omitempty"`
}

type wechatTrailerRepairReport struct {
	Matched  int                       `json:"matched"`
	Repaired int                       `json:"repaired"`
	Failed   int                       `json:"failed"`
	Results  []wechatTrailerRepairItem `json:"results"`
}

// repairPublishedWechatTrailer 对已公开任务按当前规则重排并覆盖 FoodTalks。
// 不改 stage、不改 content_version、不重开审核：这是排版事故修复，不是新发布。
func (p *publishPipeline) repairPublishedWechatTrailer(ctx context.Context, jobID int64) (publishJob, error) {
	if publishWorkersPaused() {
		return publishJob{}, errPublishWorkersPaused
	}
	job, err := p.store.byID(jobID)
	if err != nil {
		return publishJob{}, err
	}
	if job.Stage != publishStagePublished {
		return job, fmt.Errorf("任务阶段 %s 不是已发布，不能覆盖线上正文", job.Stage)
	}
	if job.FoodTalksNewsID <= 0 {
		return job, fmt.Errorf("任务 #%d 没有 FoodTalks 资讯 id", job.ID)
	}
	if err := publishStagingTargetMismatch(job); err != nil {
		return job, err
	}
	if strings.TrimSpace(job.BlocksJSON) == "" {
		return job, fmt.Errorf("任务 #%d 缺少可重放的结构化正文", job.ID)
	}

	rulePackID, err := publishRulePackIDForJob(job)
	if err != nil {
		return job, err
	}
	result, err := p.render(withPublishRulePackID(ctx, rulePackID), job.Title, publishRenderSourceURL(job), job.BlocksJSON, true)
	if err != nil {
		return job, err
	}
	if err := validatePublishRenderResultForPack(result, rulePackID); err != nil {
		return job, err
	}
	if leak := publishHTMLWechatTrailerLeak(result.HTML); leak != "" {
		return job, fmt.Errorf("重排结果仍含公众号导流尾（%s）", leak)
	}

	imageMap := map[string]string{}
	if strings.TrimSpace(job.ImageMap) != "" {
		if err := json.Unmarshal([]byte(job.ImageMap), &imageMap); err != nil {
			return job, fmt.Errorf("任务 #%d 的图片映射无效：%w", job.ID, err)
		}
	}
	html := rewriteImgSrcs(result.HTML, imageMap)
	cover := strings.TrimSpace(job.CoverURL)
	var pending []string
	seen := map[string]bool{}
	for _, src := range extractImgSrcs(html) {
		if src == "" || strings.HasPrefix(src, "data:") || seen[src] {
			continue
		}
		if _, done := imageMap[src]; done {
			continue
		}
		if cover != "" && src == cover {
			continue
		}
		seen[src] = true
		pending = append(pending, src)
	}
	if len(pending) > 0 {
		if p.ossUpload == nil {
			return job, fmt.Errorf("重排后仍有未转存图片，且当前没有转存通道：%s", strings.Join(pending, "、"))
		}
		results, err := p.ossUpload(ctx, pending)
		if err != nil {
			return job, fmt.Errorf("修复转存图片失败：%w", err)
		}
		var missing []string
		for _, src := range pending {
			if oss := strings.TrimSpace(results[src]); oss != "" {
				imageMap[src] = oss
			} else {
				missing = append(missing, src)
			}
		}
		if len(missing) > 0 {
			return job, fmt.Errorf("以下图片转存失败，拒绝用缺图正文覆盖已发布稿：%s", strings.Join(missing, "、"))
		}
		html = rewriteImgSrcs(html, imageMap)
	}
	if leak := publishHTMLWechatTrailerLeak(html); leak != "" {
		return job, fmt.Errorf("转存后正文仍含公众号导流尾（%s）", leak)
	}

	mapJSON, err := json.Marshal(imageMap)
	if err != nil {
		return job, err
	}
	updated, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		return current.Stage == publishStagePublished && current.ContentVersion == job.ContentVersion &&
			current.FoodTalksNewsID == job.FoodTalksNewsID
	}, func(j *publishJob) {
		j.ContentHTML = html
		j.ImageMap = string(mapJSON)
		j.RulePresetID = result.ResolvedRule.PresetID
		j.RuleRevision = result.ResolvedRule.Revision
		j.RuleConfigHash = result.ResolvedRule.ConfigHash
		j.RendererBuildSHA = result.RendererBuildSHA
		j.RenderOutputHash = publishRenderOutputHash(html)
		j.Error = ""
		j.FailedStage = ""
	})
	if err != nil {
		return job, err
	}
	if !swapped {
		return updated, fmt.Errorf("任务状态刚刚变化，请刷新后重试修复")
	}

	news, err := p.buildSiteNews(ctx, updated, foodTalksPublishStatusPublished)
	if err != nil {
		return updated, err
	}
	news.ID = updated.FoodTalksNewsID
	ft, err := p.foodTalks()
	if err != nil {
		return updated, err
	}
	if err := ft.UpdateNews(ctx, news); err != nil {
		return updated, fmt.Errorf("本地正文已切尾，但 FoodTalks UpdateNews 失败，请重试本修复：%w", err)
	}
	return updated, nil
}

func (p *publishPipeline) repairPublishedWechatTrailerLeaks(ctx context.Context, jobID int64) (wechatTrailerRepairReport, error) {
	report := wechatTrailerRepairReport{}
	jobs, err := p.store.all()
	if err != nil {
		return report, err
	}
	for _, job := range jobs {
		if job.Stage != publishStagePublished {
			continue
		}
		if jobID > 0 && job.ID != jobID {
			continue
		}
		leak := publishHTMLWechatTrailerLeak(job.ContentHTML)
		if leak == "" && jobID == 0 {
			continue
		}
		report.Matched++
		item := wechatTrailerRepairItem{
			JobID:           job.ID,
			FoodTalksNewsID: job.FoodTalksNewsID,
			Title:           job.Title,
			Leak:            leak,
		}
		if leak == "" {
			item.Error = "正文未检出公众号导流尾，跳过"
			report.Failed++
			report.Results = append(report.Results, item)
			continue
		}
		if _, err := p.repairPublishedWechatTrailer(ctx, job.ID); err != nil {
			item.Error = err.Error()
			report.Failed++
			report.Results = append(report.Results, item)
			continue
		}
		item.Repaired = true
		report.Repaired++
		report.Results = append(report.Results, item)
	}
	return report, nil
}
