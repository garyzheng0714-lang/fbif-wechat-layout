package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

type cmsResyncItem struct {
	JobID           int64  `json:"job_id"`
	FoodTalksNewsID int64  `json:"foodtalks_news_id,omitempty"`
	Title           string `json:"title,omitempty"`
	SourceName      string `json:"source_name,omitempty"`
	SourceID        int64  `json:"source_id,omitempty"`
	Error           string `json:"error,omitempty"`
}

type cmsResyncReport struct {
	Day     string          `json:"day"`
	Matched int             `json:"matched"`
	Updated int             `json:"updated"`
	Failed  int             `json:"failed"`
	Results []cmsResyncItem `json:"results"`
}

func parseCMSResyncDayCST(raw string) (time.Time, error) {
	day := strings.TrimSpace(raw)
	if day == "" {
		return time.Time{}, fmt.Errorf("请提供 CST 日期，例如 2026-08-13")
	}
	loc := time.FixedZone("CST", 8*3600)
	t, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("日期 %q 无法解析，请用 YYYY-MM-DD", day)
	}
	return t, nil
}

func publishJobInCSTDay(job publishJob, dayCST time.Time) bool {
	loc := time.FixedZone("CST", 8*3600)
	start := time.Date(dayCST.Year(), dayCST.Month(), dayCST.Day(), 0, 0, 0, 0, loc)
	end := start.Add(24 * time.Hour)
	for _, raw := range []string{job.PublishedAt, job.ReviewedAt} {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		if !t.Before(start) && t.Before(end) {
			return true
		}
	}
	return false
}

// resyncPublishedCMS 对已公开任务再发一次全量 PUT：补 isLatest/sourceId 等 CMS 字段。
// 不改本地状态机、不发飞书通知，避免把补写当成一次新的发布动作。
func (p *publishPipeline) resyncPublishedCMS(ctx context.Context, job publishJob) (int64, error) {
	if publishWorkersPaused() {
		return 0, errPublishWorkersPaused
	}
	if job.Stage != publishStagePublished {
		return 0, fmt.Errorf("任务当前阶段（%s）不是已发布，不能补写 CMS", job.Stage)
	}
	if job.FoodTalksNewsID <= 0 {
		return 0, fmt.Errorf("任务 #%d 没有 FoodTalks 资讯 id", job.ID)
	}
	if err := publishStagingTargetMismatch(job); err != nil {
		return 0, err
	}
	if strings.TrimSpace(job.ContentHTML) == "" {
		return 0, fmt.Errorf("任务 #%d 缺少正文，拒绝用空内容覆盖 CMS", job.ID)
	}
	ft, err := p.foodTalks()
	if err != nil {
		return 0, err
	}
	news, err := p.buildSiteNews(ctx, job, foodTalksPublishStatusPublished)
	if err != nil {
		return 0, err
	}
	news.ID = job.FoodTalksNewsID
	if err := ft.UpdateNews(ctx, news); err != nil {
		return news.SourceID, err
	}
	return news.SourceID, nil
}

func (p *publishPipeline) resyncPublishedCMSDay(ctx context.Context, dayCST time.Time) (cmsResyncReport, error) {
	report := cmsResyncReport{Day: dayCST.Format("2006-01-02")}
	jobs, err := p.store.all()
	if err != nil {
		return report, err
	}
	for _, job := range jobs {
		if job.Stage != publishStagePublished || !publishJobInCSTDay(job, dayCST) {
			continue
		}
		report.Matched++
		item := cmsResyncItem{
			JobID:           job.ID,
			FoodTalksNewsID: job.FoodTalksNewsID,
			Title:           job.Title,
		}
		if _, name := siteSourceFor(job); name != "" {
			item.SourceName = name
		} else {
			item.SourceName = strings.TrimSpace(job.SourceName)
		}
		sourceID, err := p.resyncPublishedCMS(ctx, job)
		item.SourceID = sourceID
		if err != nil {
			item.Error = err.Error()
			report.Failed++
			report.Results = append(report.Results, item)
			continue
		}
		report.Updated++
		report.Results = append(report.Results, item)
	}
	return report, nil
}

func runCMSResyncAndExit(dayRaw, rulesPath string) {
	day, err := parseCMSResyncDayCST(dayRaw)
	if err != nil {
		log.Fatal(err)
	}
	store := newPublishJobStore(publishJobsPath(rulesPath))
	p := newPublishPipeline(store, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	report, err := p.resyncPublishedCMSDay(ctx, day)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(report)
	if err != nil {
		log.Fatal(err)
	}
	if report.Failed > 0 {
		os.Exit(1)
	}
}
