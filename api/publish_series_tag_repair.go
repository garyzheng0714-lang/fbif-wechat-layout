package main

// publish_series_tag_repair.go — 已发布网站稿补链接（2026-10-08 用户反馈 63149）。按资讯 id
// 直接改网站现稿，流水线稿和编辑手工稿都适用，只动下面几处，其余正文与字段按 CMS 现稿原样写回：
//   - 系列声明段的系列名 → 同名标签页（publishSiteLinkSeriesTags）；
//   - Mote 文章里的公众号文章链 → 已发布的站内 /news/{id}，找不到保留原链（rewriteMoteSeriesLinks）；
//   - 流水线转载稿的转载声明《本稿标题》→ 链到核验过的原文、换成原文真标题（repairRepostNoticeTitle，2026-10-14）；
//   - 旧排版改成「。」的英文缩写点（`Salt Co。和`）改回半角（repairAbbreviationPeriods，2026-10-23）。

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"
)

type newsLinkRepairResult struct {
	FoodTalksNewsID   int64  `json:"foodtalks_news_id"`
	Mote              bool   `json:"mote"`
	Changed           bool   `json:"changed"`
	DryRun            bool   `json:"dry_run"`
	RepostNoticeTitle string `json:"repost_notice_title,omitempty"`
	// AbbreviationPeriods 改回半角的英文缩写点数。
	AbbreviationPeriods int `json:"abbreviation_periods,omitempty"`
}

// siteAbbreviationWords 与 public/js/punctuation.js 规则 2d 同一张表（TestSiteAbbreviationWordsMatchRenderer 核对）。
var siteAbbreviationWords = []string{"Co", "Corp", "Inc", "Ltd", "Ltda", "Bros", "Mfg", "Intl", "Pty",
	"Mr", "Mrs", "Ms", "Dr", "Prof", "Jr", "Sr", "St", "Mt", "vs", "etc", "approx"}

var siteAbbreviationPeriodRe = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9.])((?:` + strings.Join(siteAbbreviationWords, "|") +
	`)|(?:[A-Za-z]\.)+[A-Za-z])。`)

// repairAbbreviationPeriods 2026-10-23 前的排版把英文缩写后的点当句末改成了「。」，改回半角。
func repairAbbreviationPeriods(content string) (string, int) {
	n := len(siteAbbreviationPeriodRe.FindAllStringIndex(content, -1))
	if n == 0 {
		return content, 0
	}
	return siteAbbreviationPeriodRe.ReplaceAllString(content, "${1}${2}."), n
}

// repostNoticeTitleRe 网站转载声明「*本文为{来源}原创《标题》」，《》可能已包在链接里。
var repostNoticeTitleRe = regexp.MustCompile(`(\*本文为([^<《*]{1,60}?)原创)(?:<a\b[^>]*>)?《([^》<]{1,200})》(?:</a>)?`)

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
	// 存量稿只链到已有标签，不新建、不改文章标签。
	content := publishSiteLinkSeriesTags(live.Content, newSiteSeriesTags(ctx, ft, live.NewsTags, false).id)
	job := p.newsLinkRepairJob(newsID, live)
	if res.Mote = publishSiteIsMoteArticle(job); res.Mote {
		content = p.rewriteMoteSeriesLinks(ctx, job, content)
	}
	content, res.RepostNoticeTitle = p.repairRepostNoticeTitle(ctx, job, content, dryRun)
	content, res.AbbreviationPeriods = repairAbbreviationPeriods(content)
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

// repairRepostNoticeTitle 流水线转载稿的转载声明还是本稿（FBIF 改过的）标题时，换成核验过的原文真标题
// 并链到原文；编辑已改过这句、找不到原文、自家原创都不动。dry_run 只查不记。
func (p *publishPipeline) repairRepostNoticeTitle(ctx context.Context, job publishJob, content string, dryRun bool) (string, string) {
	if job.ID <= 0 {
		return content, "" // 编辑手工稿没有任务库记录，不知道本稿标题与正文
	}
	if dryRun {
		job, _ = repostOriginalFor(ctx, job)
	} else {
		p.ensureRepostOriginalURL(ctx, &job)
	}
	title := publishOriginalTitleForRender(job)
	m := repostNoticeTitleRe.FindStringSubmatchIndex(content)
	if title == "" || m == nil || publishExternalSourceIsOwn(html.UnescapeString(content[m[4]:m[5]]), siteOwnAccounts()) {
		return content, ""
	}
	if normalizeOriginalTitle(html.UnescapeString(content[m[6]:m[7]])) != normalizeOriginalTitle(job.Title) {
		return content, ""
	}
	notice := content[m[2]:m[3]] + `<a href="` + html.EscapeString(job.OriginalSourceURL) + `" target="_blank" rel="noopener">《` +
		html.EscapeString(title) + `》</a>`
	return content[:m[0]] + notice + content[m[1]:], title
}
