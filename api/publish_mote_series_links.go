package main

// publish_mote_series_links.go — Mote 系列文里的公众号互推链，在网站发布组装时
// 换成已发布的 FoodTalks 站内文章（2026-08-24 用户裁定；金标准 news/62285 的
// Tazo → /news/62236）。
//
// 只动网站发布 payload，不动 JS 渲染器：交互复制仍可保留公众号链。
// 找不到已发布站内稿、查询失败、命中不唯一：保留原链，不 fail job。

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 每篇最多查 60 个公众号链接的标题：Mote 系列文末常列出前面几十篇（2026-10-08，20 不够）。
const publishMoteSeriesMaxTitleFetches = 60

// 只改 <a href> 的属性值，不 round-trip 整篇 HTML。Go regexp 没有反向引用，引号拆两条。
// href 必须是独立属性（前面是空白），避免误吃 data-href。
var htmlAnchorHrefDoubleRe = regexp.MustCompile(`(?is)<a\s(?:[^>]*?\s)?href\s*=\s*"([^"]*)"`)
var htmlAnchorHrefSingleRe = regexp.MustCompile(`(?is)<a\s(?:[^>]*?\s)?href\s*=\s*'([^']*)'`)

// rewriteMoteSeriesLinks 把 Mote 正文里的微信文章链接改成站内 /news/{id}。
// 非 Mote、非公众号文章链、本稿自链、未发布/找不到：原文返回。
func (p *publishPipeline) rewriteMoteSeriesLinks(ctx context.Context, job publishJob, contentHTML string) string {
	if !publishSiteIsMoteArticle(job) || strings.TrimSpace(contentHTML) == "" {
		return contentHTML
	}
	if ctx == nil {
		ctx = context.Background()
	}
	index := p.moteSeriesPublishedIndex()
	cache := map[string]int64{}
	fetches := 0
	return rewriteHTMLAnchorHrefs(contentHTML, func(href string) (string, bool) {
		newsID := p.resolveMoteSeriesNewsID(ctx, job, href, index, cache, &fetches)
		if newsID <= 0 {
			return "", false
		}
		if job.FoodTalksNewsID > 0 && newsID == job.FoodTalksNewsID {
			return "", false
		}
		return siteFrontURL() + "/news/" + strconv.FormatInt(newsID, 10), true
	})
}

func rewriteHTMLAnchorHrefs(contentHTML string, replace func(href string) (string, bool)) string {
	out := rewriteQuotedAnchorHrefs(contentHTML, htmlAnchorHrefDoubleRe, replace)
	return rewriteQuotedAnchorHrefs(out, htmlAnchorHrefSingleRe, replace)
}

func rewriteQuotedAnchorHrefs(contentHTML string, re *regexp.Regexp, replace func(href string) (string, bool)) string {
	matches := re.FindAllStringSubmatchIndex(contentHTML, -1)
	if len(matches) == 0 {
		return contentHTML
	}
	var b strings.Builder
	b.Grow(len(contentHTML) + 64)
	prev := 0
	for _, m := range matches {
		hrefStart, hrefEnd := m[2], m[3]
		hrefRaw := contentHTML[hrefStart:hrefEnd]
		href := strings.TrimSpace(html.UnescapeString(hrefRaw))
		next, ok := replace(href)
		b.WriteString(contentHTML[prev:hrefStart])
		if ok && strings.TrimSpace(next) != "" && next != href {
			b.WriteString(escapeHTMLAttr(next))
		} else {
			b.WriteString(hrefRaw)
		}
		prev = hrefEnd
	}
	b.WriteString(contentHTML[prev:])
	return b.String()
}

func escapeHTMLAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return s
}

func (p *publishPipeline) resolveMoteSeriesNewsID(
	ctx context.Context,
	job publishJob,
	href string,
	index map[string]int64,
	cache map[string]int64,
	fetches *int,
) int64 {
	key, err := normalizeSiteSourceKey(href)
	if err != nil {
		return 0
	}
	if jobOwnsSourceKey(job, key) {
		return 0
	}
	if id := index[key]; id > 0 {
		log.Printf("publish: Mote 系列链接任务库 %s → news/%d（任务 #%d）", key, id, job.ID)
		return id
	}
	if id, seen := cache[key]; seen {
		return id
	}
	id := p.lookupMoteSeriesNewsIDByTitle(ctx, job, href, fetches)
	cache[key] = id
	return id
}

func jobOwnsSourceKey(job publishJob, key string) bool {
	if key == "" {
		return false
	}
	if job.SourceKey == key {
		return true
	}
	for _, alias := range job.SourceAliases {
		if alias == key {
			return true
		}
	}
	if srcKey, err := normalizeSiteSourceKey(job.SourceURL); err == nil && srcKey == key {
		return true
	}
	return false
}

// moteSeriesPublishedIndex 已发布任务的公众号身份 → 站内资讯 id。
// 同一身份对上两个不同资讯 id 时丢掉该键（宁缺勿错）。
func (p *publishPipeline) moteSeriesPublishedIndex() map[string]int64 {
	out := map[string]int64{}
	if p == nil || p.store == nil {
		return out
	}
	jobs, err := p.store.all()
	if err != nil {
		log.Printf("publish: Mote 系列链接任务库不可用：%v", err)
		return out
	}
	ambiguous := map[string]bool{}
	add := func(key string, newsID int64) {
		key = strings.TrimSpace(key)
		if key == "" || newsID <= 0 || ambiguous[key] {
			return
		}
		if prev, ok := out[key]; ok && prev != newsID {
			delete(out, key)
			ambiguous[key] = true
			return
		}
		out[key] = newsID
	}
	for _, j := range jobs {
		if j.Stage != publishStagePublished || j.FoodTalksNewsID <= 0 {
			continue
		}
		add(j.SourceKey, j.FoodTalksNewsID)
		for _, alias := range j.SourceAliases {
			add(alias, j.FoodTalksNewsID)
		}
		if key, err := normalizeSiteSourceKey(j.SourceURL); err == nil {
			add(key, j.FoodTalksNewsID)
		}
	}
	return out
}

func (p *publishPipeline) lookupMoteSeriesNewsIDByTitle(ctx context.Context, job publishJob, href string, fetches *int) int64 {
	if p == nil || p.foodTalks == nil {
		return 0
	}
	if fetches != nil && *fetches >= publishMoteSeriesMaxTitleFetches {
		return 0
	}
	select {
	case <-ctx.Done():
		return 0
	default:
	}
	ft, err := p.foodTalks()
	if err != nil {
		log.Printf("publish: Mote 系列链接标题检索不可用（任务 #%d）：%v", job.ID, err)
		return 0
	}
	ref, err := p.wechatArticleTitle(ctx, href)
	if fetches != nil {
		*fetches++
	}
	title := strings.TrimSpace(ref.Title)
	if err != nil || title == "" {
		if err != nil {
			log.Printf("publish: Mote 系列链接读公众号标题失败（任务 #%d，%s）：%v", job.ID, href, err)
		}
		return 0
	}
	// 先整题检索（CMS title 参数是包含匹配）；编辑改过标题时整题搜不到，再用最长的一个分句检索。
	queries := []string{title}
	if clause := longestTitleClause(title); clause != "" && clause != title && !ref.PublishedAt.IsZero() {
		queries = append(queries, clause)
	}
	for _, q := range queries {
		items, err := ft.SearchNewsByTitle(ctx, q)
		if err != nil {
			log.Printf("publish: Mote 系列链接站内搜题失败（任务 #%d，%q）：%v", job.ID, q, err)
			return 0
		}
		if hit := pickMoteSeriesNews(ref, items, job.FoodTalksNewsID); hit > 0 {
			log.Printf("publish: Mote 系列链接按标题 %q → news/%d（任务 #%d）", title, hit, job.ID)
			return hit
		}
	}
	return 0
}

const publishMoteSeriesDateWindow = 7 * 24 * time.Hour

var publishSiteTimeZone = time.FixedZone("CST", 8*3600)

// pickMoteSeriesNews 从站内检索结果里挑公众号文章对应的已发布稿（不含本稿）：
//   - 标题相同且只有一篇：就是它（老文可能很晚才搬上网站，不看日期）；
//   - 标题相同但有多篇（网站重复稿，如「FBIF 航海书」发了 11 篇）：取发布时间离公众号发文最近、
//     相差 7 天内的一篇；
//   - 标题不同但编辑只往里加了字（公众号标题是站内标题的子序列，63149 的 Tea Forté）：同样要求
//     7 天内，取最近的一篇。
//
// 不知道公众号发文时间时只认第一种。
func pickMoteSeriesNews(ref wechatArticleRef, items []foodTalksNewsListItem, selfID int64) int64 {
	want := normalizeSiteTitle(ref.Title)
	var exact []foodTalksNewsListItem
	var near []foodTalksNewsListItem
	for _, item := range items {
		if item.PublishStatus != foodTalksPublishStatusPublished || item.ID <= 0 || item.ID == selfID {
			continue
		}
		got := normalizeSiteTitle(item.Title)
		switch {
		case got == want:
			exact = append(exact, item)
		case len([]rune(want)) >= 8 && isRuneSubsequence(want, got):
			near = append(near, item)
		}
	}
	if len(exact) == 1 {
		return exact[0].ID
	}
	if ref.PublishedAt.IsZero() {
		return 0
	}
	if len(exact) == 0 {
		exact = near
	}
	var best int64
	var bestGap time.Duration
	for _, item := range exact {
		at, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(item.PublishTime), publishSiteTimeZone)
		if err != nil {
			continue
		}
		gap := at.Sub(ref.PublishedAt)
		if gap < 0 {
			gap = -gap
		}
		if gap > publishMoteSeriesDateWindow {
			continue
		}
		if best == 0 || gap < bestGap || (gap == bestGap && item.ID < best) {
			best, bestGap = item.ID, gap
		}
	}
	return best
}

func isRuneSubsequence(short, long string) bool {
	rest := []rune(long)
	for _, r := range short {
		i := 0
		for i < len(rest) && rest[i] != r {
			i++
		}
		if i == len(rest) {
			return false
		}
		rest = rest[i+1:]
	}
	return true
}

var titleClauseSplitRe = regexp.MustCompile(`[，,：:！!？?｜|；;。 　]+`)

// longestTitleClause 标题按标点拆开后最长的一段（至少 4 个字），整题搜不到时拿它检索。
func longestTitleClause(title string) string {
	best := ""
	for _, part := range titleClauseSplitRe.Split(title, -1) {
		if len([]rune(part)) > len([]rune(best)) {
			best = part
		}
	}
	if len([]rune(best)) < 4 {
		return ""
	}
	return best
}

// wechatArticleRef 公众号文章的标题与发文时间（时间可能为零值：读不到）。
type wechatArticleRef struct {
	Title       string
	PublishedAt time.Time
}

func (p *publishPipeline) wechatArticleTitle(ctx context.Context, articleURL string) (wechatArticleRef, error) {
	if p != nil && p.fetchWechatTitle != nil {
		return p.fetchWechatTitle(ctx, articleURL)
	}
	return wechatArticleRef{}, errMoteSeriesTitleFetcherMissing
}

// wechatTitleCache 公众号文章身份 → wechatArticleRef。TikHub 按次计费，同一链接进程内只查一次。
var wechatTitleCache sync.Map

const tikhubArticleDetailPath = "/api/v1/wechat_mp/v2/fetch_article_detail"

var wechatCreateTimeRe = regexp.MustCompile(`create_time\s*[:=]\s*['"](\d{10})['"]`)

// defaultFetchWechatTitle 先直连公众号页；读不到标题再走 TikHub。Mote 正文里的互推链多是
// 带 __biz/mid/idx/sn、不带 chksm 的长链，微信对服务器和浏览器都只回验证页（2026-10-08 实测，
// 63149 的 TWG/玛黑兄弟/Tea Forté 因此一直没换成站内链）；短链和带 chksm 的链直连照常。
func defaultFetchWechatTitle(ctx context.Context, articleURL string) (wechatArticleRef, error) {
	key, keyErr := normalizeSiteSourceKey(articleURL)
	if keyErr == nil {
		if v, ok := wechatTitleCache.Load(key); ok {
			return v.(wechatArticleRef), nil
		}
	}
	ref, err := fetchWechatTitleDirect(ctx, articleURL)
	if err != nil && tikhubAPIKey() != "" {
		ref, err = fetchWechatTitleTikhub(ctx, articleURL)
	}
	if err == nil && keyErr == nil {
		wechatTitleCache.Store(key, ref)
	}
	return ref, err
}

func fetchWechatTitleDirect(ctx context.Context, articleURL string) (wechatArticleRef, error) {
	raw, _, err := fetchArticleHTML(ctx, articleURL)
	if err != nil {
		return wechatArticleRef{}, err
	}
	title, _ := extractArticleMeta(raw)
	title = strings.TrimSpace(title)
	if title == "" {
		return wechatArticleRef{}, errors.New("公众号页未解析到标题")
	}
	ref := wechatArticleRef{Title: title}
	if m := wechatCreateTimeRe.FindStringSubmatch(raw); m != nil {
		if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			ref.PublishedAt = time.Unix(sec, 0)
		}
	}
	return ref, nil
}

func fetchWechatTitleTikhub(ctx context.Context, articleURL string) (wechatArticleRef, error) {
	data, err := tikhubPOST(ctx, tikhubArticleDetailPath, map[string]any{"url": articleURL, "raw": false})
	if err != nil {
		return wechatArticleRef{}, fmt.Errorf("TikHub 读公众号标题失败: %w", err)
	}
	m, _ := data.(map[string]any)
	content, _ := m["content"].(map[string]any)
	title := strings.TrimSpace(tikhubString(content, "title"))
	if title == "" {
		return wechatArticleRef{}, errors.New("TikHub 未返回公众号标题")
	}
	ref := wechatArticleRef{Title: title}
	if sec, ok := content["create_timestamp"].(float64); ok && sec > 0 {
		ref.PublishedAt = time.Unix(int64(sec), 0)
	} else if at, err := time.ParseInLocation("2006-01-02 15:04", tikhubString(content, "create_time"), publishSiteTimeZone); err == nil {
		ref.PublishedAt = at
	}
	return ref, nil
}

var errMoteSeriesTitleFetcherMissing = errors.New("未配置公众号标题抓取，跳过系列链接标题回退")
