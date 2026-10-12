package main

// 外部平台上的文章链接（2026-10-12 用户裁定：多平台分发折叠区里「显示它的标题那些东西」）。
// 云梭的推送与状态回执都不带文章链接，只能事后找：
//   - 头条号：头条搜索按标题拿候选（文章 id 高 32 位是发文时间，先按推送时间筛），再用
//     TikHub 读文章详情，作者是自家号、标题对得上才认；
//   - 百家号：百度搜索有验证码，不绕过，暂不自动找。
// 找不到只降级：卡片照样显示状态和推送标题，不影响任何发布状态。人工发过的平台
// （2026-03～08 头条号是人工发的）由管理员登记成「人工已发」lane，自动外发不会再推。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	externalLinkLookupToutiao = "toutiao_search"

	defaultToutiaoSearchEndpoint = "https://so.toutiao.com/search"
	tikhubToutiaoArticlePath     = "/api/v1/toutiao/app/get_article_info"
	toutiaoSearchMaxBytes        = 4 << 20
	externalLinkMaxCandidates    = 5
	externalLinkMaxAttempts      = 6
	externalLinkLookupWithin     = 7 * 24 * time.Hour
	externalLinkMaxPerSweep      = 3
	// 系统推送的稿：平台发文时间只会在推送之后；留 10 分钟容差。
	externalLinkPushedSlack = 10 * time.Minute
	// 人工登记时按网站发布时间前后找（人工稿可能早发几天，也可能晚很久）。
	externalLinkManualBefore = 3 * 24 * time.Hour
	externalLinkManualAfter  = 60 * 24 * time.Hour

	publishExternalManualActor = "admin:manual_register"
)

// toutiaoSearchEndpoint 测试可替换成本地桩。
var toutiaoSearchEndpoint = defaultToutiaoSearchEndpoint

// externalLinkBackoff 第 n 次查找距上次（或推送成功）至少隔多久；平台收录要时间。
var externalLinkBackoff = []time.Duration{20 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour}

var errExternalArticleNotFound = errors.New("没有找到作者与标题都对得上的文章")

type externalArticleRef struct {
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Author      string    `json:"author"`
	PublishedAt time.Time `json:"published_at"`
}

// lookupToutiaoArticle 测试可替换；生产走头条搜索 + TikHub 核对。
var lookupToutiaoArticle = defaultLookupToutiaoArticle

// lookupExternalArticle 按平台规则找文章；平台不支持自动找时报错。
func lookupExternalArticle(ctx context.Context, profile yunsuoPlatformProfile, title string, from, to time.Time) (externalArticleRef, error) {
	switch profile.LinkLookup {
	case externalLinkLookupToutiao:
		return lookupToutiaoArticle(ctx, title, from, to)
	default:
		return externalArticleRef{}, fmt.Errorf("%s暂不支持自动查找文章链接，请直接提供链接", profile.Name)
	}
}

var toutiaoGroupIDRe = regexp.MustCompile(`toutiao\.com/(?:a|article/|group/)(\d{16,20})`)

func defaultLookupToutiaoArticle(ctx context.Context, title string, from, to time.Time) (externalArticleRef, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return externalArticleRef{}, errors.New("缺少标题，无法查找")
	}
	if tikhubAPIKey() == "" {
		return externalArticleRef{}, errors.New("未配置 TIKHUB_API_KEY，无法核对头条文章")
	}
	ids, err := searchToutiaoGroupIDs(ctx, title)
	if err != nil {
		return externalArticleRef{}, err
	}
	own := siteOwnAccounts()
	checked := 0
	var lastErr error
	for _, id := range ids {
		at, ok := toutiaoGroupIDTime(id)
		if !ok || at.Before(from) || at.After(to) {
			continue
		}
		if checked >= externalLinkMaxCandidates {
			break
		}
		checked++
		ref, err := fetchToutiaoArticleRef(ctx, id)
		if err != nil {
			lastErr = err
			continue
		}
		if own[ref.Author] && externalTitlesMatch(title, ref.Title) {
			return ref, nil
		}
	}
	if checked > 0 && lastErr != nil {
		return externalArticleRef{}, fmt.Errorf("%w（最后一次核对失败：%v）", errExternalArticleNotFound, lastErr)
	}
	return externalArticleRef{}, errExternalArticleNotFound
}

// searchToutiaoGroupIDs 头条搜索结果页里出现的文章 id，按出现顺序去重。
func searchToutiaoGroupIDs(ctx context.Context, keyword string) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, tikhubRequestTimeout)
	defer cancel()
	query := url.Values{"dvpf": {"pc"}, "source": {"input"}, "keyword": {keyword}}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, toutiaoSearchEndpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Safari/537.36")
	req.Header.Set("Accept", "text/html")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("头条搜索失败：%w", err)
	}
	defer resp.Body.Close()
	body, err := readBoundedBody(resp.Body, toutiaoSearchMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("头条搜索失败：%w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("头条搜索失败：HTTP %d", resp.StatusCode)
	}
	return toutiaoGroupIDs(string(body)), nil
}

func toutiaoGroupIDs(page string) []string {
	seen := map[string]bool{}
	var ids []string
	for _, match := range toutiaoGroupIDRe.FindAllStringSubmatch(page, -1) {
		if id := match[1]; !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// toutiaoGroupIDTime 头条文章 id 的高 32 位是发文的 Unix 秒。
func toutiaoGroupIDTime(id string) (time.Time, bool) {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n>>32 == 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(n>>32), 0).UTC(), true
}

func fetchToutiaoArticleRef(ctx context.Context, id string) (externalArticleRef, error) {
	data, err := tikhubGET(ctx, tikhubToutiaoArticlePath, url.Values{"group_id": {id}})
	if err != nil {
		return externalArticleRef{}, fmt.Errorf("TikHub 读头条文章失败：%w", err)
	}
	root, _ := data.(map[string]any)
	if inner, ok := root["data"].(map[string]any); ok {
		root = inner
	}
	if root == nil {
		return externalArticleRef{}, errors.New("TikHub 未返回头条文章详情")
	}
	author := strings.TrimSpace(tikhubString(root, "source"))
	for _, key := range []string{"user_info", "media_info"} {
		if m, ok := nestedMap(root, key); ok {
			if name := strings.TrimSpace(tikhubString(m, "name")); name != "" {
				author = name
				break
			}
		}
	}
	title := strings.TrimSpace(tikhubString(root, "title"))
	if title == "" {
		if share, ok := nestedMap(root, "share_info"); ok {
			title = strings.TrimSpace(strings.TrimSuffix(tikhubString(share, "title"), " - 今日头条"))
		}
	}
	at, _ := toutiaoGroupIDTime(id)
	return externalArticleRef{
		URL: "https://www.toutiao.com/article/" + id + "/", Title: title, Author: author, PublishedAt: at,
	}, nil
}

// externalTitlesMatch 去掉空白和标点后相同；平台截过的长标题（≥12 字）互相包含也算。
func externalTitlesMatch(want, got string) bool {
	a, b := normalizeOriginalTitle(want), normalizeOriginalTitle(got)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	shorter := a
	if len([]rune(b)) < len([]rune(a)) {
		shorter = b
	}
	return len([]rune(shorter)) >= 12 && (strings.Contains(a, b) || strings.Contains(b, a))
}

// externalLaneNeedsLink 系统推送成功、还没有链接、平台支持自动找、没超次数和时限，且
// 距上次查找已过退避间隔。
func externalLaneNeedsLink(lane publishExternalLane, profile yunsuoPlatformProfile, now time.Time) bool {
	if lane.State != publishExternalDistributionSucceeded || strings.TrimSpace(lane.URL) != "" ||
		profile.LinkLookup == "" || lane.URLAttempts >= externalLinkMaxAttempts {
		return false
	}
	completedAt, err := time.Parse(time.RFC3339, lane.CompletedAt)
	if err != nil || now.Sub(completedAt) > externalLinkLookupWithin {
		return false
	}
	last := completedAt
	if checked, err := time.Parse(time.RFC3339, lane.URLCheckedAt); err == nil && checked.After(last) {
		last = checked
	}
	return now.Sub(last) >= externalLinkBackoff[min(lane.URLAttempts, len(externalLinkBackoff)-1)]
}

// externalLaneLookupWindow 系统推送的按推送时间往后找，否则按网站发布时间前后找。
func externalLaneLookupWindow(job publishJob, lane publishExternalLane, now time.Time) (time.Time, time.Time) {
	if requested, err := time.Parse(time.RFC3339, lane.RequestedAt); err == nil && lane.State != publishExternalLaneManual && lane.ContentPK != "" {
		return requested.Add(-externalLinkPushedSlack), now
	}
	base := now
	if published, err := time.Parse(time.RFC3339, job.PublishedAt); err == nil {
		base = published
	}
	return base.Add(-externalLinkManualBefore), base.Add(externalLinkManualAfter)
}

// refreshExternalDistributionLinks 兜底循环里给推送成功的平台补文章链接；每轮最多查
// 几篇，TikHub 按次计费。找到就刷卡。
func (p *publishPipeline) refreshExternalDistributionLinks() {
	if p == nil || p.store == nil || publishWorkersPaused() {
		return
	}
	cfg, err := loadYunsuoConfig()
	if err != nil {
		return
	}
	jobs, err := p.store.all()
	if err != nil {
		log.Printf("publish: 外发链接补查读取任务失败：%v", err)
		return
	}
	now := pipelineNow(p)
	stamp := now.Format(time.RFC3339)
	budget := externalLinkMaxPerSweep
	for _, job := range jobs {
		for _, lane := range job.ExternalDistributionLanes {
			if budget <= 0 {
				return
			}
			target, ok := cfg.target(lane.AccountID)
			if !ok {
				continue
			}
			profile := yunsuoProfileForTarget(target)
			if !externalLaneNeedsLink(lane, profile, now) {
				continue
			}
			budget--
			from, to := externalLaneLookupWindow(job, lane, now)
			ref, lookupErr := lookupExternalArticle(context.Background(), profile, firstNonEmpty(lane.Title, job.Title), from, to)
			found := lookupErr == nil && strings.TrimSpace(ref.URL) != ""
			_, swapped, updateErr := p.updateExternalLane(job.ID, lane.AccountID, func(current publishExternalLane) bool {
				return current.State == lane.State && strings.TrimSpace(current.URL) == ""
			}, func(current *publishJob, l *publishExternalLane) {
				l.URLAttempts++
				l.URLCheckedAt = stamp
				if found {
					l.URL = ref.URL
					if strings.TrimSpace(ref.Title) != "" {
						l.Title = ref.Title
					}
					enqueueExternalDistributionCardUpdate(current)
				}
			})
			switch {
			case updateErr != nil:
				log.Printf("publish: 外发链接补查落盘失败（任务 #%d %s）：%v", job.ID, lane.Name, updateErr)
			case !found:
				log.Printf("publish: 外发链接补查未找到（任务 #%d %s，第 %d 次）：%v", job.ID, lane.Name, lane.URLAttempts+1, lookupErr)
			case swapped:
				log.Printf("publish: 外发链接补查找到（任务 #%d %s）：%s", job.ID, lane.Name, ref.URL)
				p.scheduleExternalDistributionCardUpdate(job.ID)
			}
		}
	}
}

// externalManualRequest 管理员登记某平台「人工已发」或补文章链接。
type externalManualRequest struct {
	AccountID string `json:"account_id"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	// Lookup 不给链接时按平台规则自动找（目前只有头条号）；找不到不写。
	Lookup bool `json:"lookup"`
	DryRun bool `json:"dry_run"`
}

type externalManualResult struct {
	JobID     int64               `json:"job_id"`
	Title     string              `json:"title"`
	AccountID string              `json:"account_id"`
	Platform  string              `json:"platform"`
	Action    string              `json:"action"` // register_manual / set_link
	Found     *externalArticleRef `json:"found,omitempty"`
	Lane      publishExternalLane `json:"lane"`
	DryRun    bool                `json:"dry_run"`
}

// validateExternalArticleURL 只认 https 和平台规则里登记的域名，防止把任意网址写进审核卡。
func validateExternalArticleURL(raw string, profile yunsuoPlatformProfile) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("文章链接必须是 https 网址")
	}
	if len(profile.URLHosts) == 0 {
		return "", fmt.Errorf("%s没有登记文章域名，不能登记链接", profile.Name)
	}
	host := strings.ToLower(parsed.Hostname())
	for _, allowed := range profile.URLHosts {
		if host == allowed {
			parsed.Fragment = ""
			return parsed.String(), nil
		}
	}
	return "", fmt.Errorf("%s的文章链接只认 %s", profile.Name, strings.Join(profile.URLHosts, "、"))
}

// registerExternalDistributionManual 把某平台记为「人工已发」（这个平台以后不会再自动推），
// 或给已发布的平台补链接。正在同步的平台不能改。
func (p *publishPipeline) registerExternalDistributionManual(ctx context.Context, jobID int64, req externalManualRequest) (externalManualResult, error) {
	result := externalManualResult{JobID: jobID, AccountID: strings.TrimSpace(req.AccountID), DryRun: req.DryRun}
	cfg, err := loadYunsuoConfig()
	if err != nil {
		return result, err
	}
	target, ok := cfg.target(result.AccountID)
	if !ok {
		return result, errors.New("account_id 不是已配置的外发账号")
	}
	profile := yunsuoProfileForTarget(target)
	result.Platform = target.Name
	job, err := p.store.byID(jobID)
	if err != nil {
		return result, err
	}
	result.Title = job.Title
	if job.Stage != publishStagePublished && job.Stage != publishStageOffline {
		return result, fmt.Errorf("只有已在 FoodTalks 发布过的文章才能登记外部平台，当前阶段为 %s", job.Stage)
	}
	existing, exists := externalLaneFor(job, target.ID)
	if exists && publishExternalDistributionProcessing(existing.State) {
		return result, fmt.Errorf("%s正在同步，等结果出来再登记", target.Name)
	}
	articleURL, title := "", strings.TrimSpace(req.Title)
	switch {
	case strings.TrimSpace(req.URL) != "":
		if articleURL, err = validateExternalArticleURL(req.URL, profile); err != nil {
			return result, err
		}
	case req.Lookup:
		lane := existing
		if !exists {
			lane = publishExternalLane{State: publishExternalLaneManual}
		}
		from, to := externalLaneLookupWindow(job, lane, pipelineNow(p))
		ref, err := lookupExternalArticle(ctx, profile, firstNonEmpty(title, existing.Title, job.Title), from, to)
		if err != nil {
			return result, err
		}
		result.Found = &ref
		articleURL, title = ref.URL, firstNonEmpty(ref.Title, title)
	}
	settled := exists && externalLaneSettled(existing)
	if settled && articleURL == "" {
		return result, fmt.Errorf("%s已是发布状态，补链接时必须提供 url 或 lookup", target.Name)
	}
	result.Action = "register_manual"
	if settled {
		result.Action = "set_link"
	}
	stamp := pipelineNow(p).Format(time.RFC3339)
	apply := func(lane *publishExternalLane) {
		if !settled {
			lane.State = publishExternalLaneManual
			lane.RequestedBy, lane.RequestedAt, lane.CompletedAt = publishExternalManualActor, stamp, stamp
			lane.Error = ""
		}
		if articleURL != "" {
			lane.URL, lane.URLCheckedAt = articleURL, stamp
		}
		if title != "" {
			lane.Title = title
		}
	}
	if req.DryRun {
		preview := existing
		if !exists {
			preview = publishExternalLane{AccountID: target.ID, Name: target.Name}
		}
		apply(&preview)
		result.Lane = preview
		return result, nil
	}
	updated, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		lane, ok := externalLaneFor(current, target.ID)
		if ok != exists {
			return false
		}
		return !ok || (lane.State == existing.State && lane.ContentPK == existing.ContentPK)
	}, func(current *publishJob) {
		i := externalLaneIndex(*current, target.ID)
		if i < 0 {
			current.ExternalDistributionLanes = append(current.ExternalDistributionLanes, publishExternalLane{AccountID: target.ID, Name: target.Name})
			i = len(current.ExternalDistributionLanes) - 1
		}
		apply(&current.ExternalDistributionLanes[i])
		if current.ExternalDistributionAutoPending != "" && len(externalDistributionMissingAutoTargets(*current, cfg)) == 0 {
			current.ExternalDistributionAutoPending = ""
			current.ExternalDistributionAutoError = ""
		}
		enqueueExternalDistributionCardUpdate(current)
	})
	if err != nil {
		return result, err
	}
	if !swapped {
		return result, fmt.Errorf("%s的状态刚刚变化，请重新读取后再登记", target.Name)
	}
	result.Lane, _ = externalLaneFor(updated, target.ID)
	result.Lane.PayloadJSON = ""
	p.scheduleExternalDistributionCardUpdate(updated.ID)
	return result, nil
}
