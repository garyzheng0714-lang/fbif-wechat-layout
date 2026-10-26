package main

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// FoodTalks 网站文章导入（2026-10-27）：「FoodTalks食品资讯」公众号按 FoodTalks 网站上的稿子排版
// （《FoodTalks公众号排版规范（新）》）。网站是前端渲染、公开接口有反爬，直接抓网页拿不到正文，
// 这里改用发布已在用的管理接口取后台存的正文 HTML，再走与公众号同一套块提取。
// 只认当前环境前台（FOODTALKS_FRONT_URL）的 /news/{id}：测试站的 id 与正式站对不上，别的链接照旧抓网页。

var foodTalksNewsPathRe = regexp.MustCompile(`^/news/(\d{1,12})/?$`)

// fetchFoodTalksNews 取一篇 FoodTalks 资讯；测试替换。
var fetchFoodTalksNews = func(ctx context.Context, id int64) (foodTalksNewsDetail, error) {
	c, err := newFoodTalksClientFromEnv()
	if err != nil {
		return foodTalksNewsDetail{}, err
	}
	return c.GetNews(ctx, id)
}

func foodTalksHostKey(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, prefix := range []string{"www.", "m."} {
		host = strings.TrimPrefix(host, prefix)
	}
	return host
}

// foodTalksNewsIDFromURL 认出当前环境 FoodTalks 前台的文章链接，返回资讯 id。
func foodTalksNewsIDFromURL(raw string) (int64, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return 0, false
	}
	front, err := url.Parse(siteFrontURL())
	if err != nil || front.Hostname() == "" || foodTalksHostKey(u.Hostname()) != foodTalksHostKey(front.Hostname()) {
		return 0, false
	}
	m := foodTalksNewsPathRe.FindStringSubmatch(u.Path)
	if m == nil {
		return 0, false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// foodTalksNewsArticleBlocks 不是 FoodTalks 文章链接时 handled=false，交回网页抓取。
func foodTalksNewsArticleBlocks(ctx context.Context, rawURL string, opts articleBlockOptions) (resp articleBlocksResponse, handled bool, err error) {
	id, ok := foodTalksNewsIDFromURL(rawURL)
	if !ok {
		return articleBlocksResponse{}, false, nil
	}
	news, err := fetchFoodTalksNews(ctx, id)
	if err != nil {
		return articleBlocksResponse{}, true, err
	}
	_, blocks, _ := extractArticleBlocksWithOptions(news.Content, normalizeArticleBlockOptions(opts))
	return articleBlocksResponse{
		Title:      strings.TrimSpace(news.Title),
		Blocks:     blocks,
		Source:     rawURL,
		SourceName: strings.TrimSpace(news.SourceName),
		Byline:     strings.TrimSpace(news.Author),
	}, true, nil
}
