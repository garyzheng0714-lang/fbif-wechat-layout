package main

// foodtalks_flash.go — FoodTalks 快讯后台接口（从后台网页「发布快讯」页的代码里查到，2026-11-29 测试环境实测）。
// 用法和坑见 docs/foodtalks-flash.md，要点：
//   - 新建 POST /news/admin/short/news，回 data 是新编号；没有幂等键。publishStatus 1 建好就公开（发布时间是那一刻），
//     0 是草稿，前台看不到；
//   - 修改 PUT /news/admin/short/news，整份带 id；
//   - 详情 GET /news/admin/short/news/{id}；后台列表 GET /news/admin/short/news/page，title 是模糊查；
//   - 上下架 PUT /news/admin/short/news/banned?id=&bannedStatus=（1 下架），参数只认网址里的；
//   - 正文是富文本，后台编辑器最多 1000 字；人工发的快讯段与段之间隔一个空段，来源写在正文里，「原文链接」填了前台标题下会出一个链接。
// 请求头、凭证、出错分类与发资讯同一个客户端（foodtalks.go）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	foodTalksFlashPath       = "/news/admin/short/news"
	foodTalksFlashPagePath   = "/news/admin/short/news/page"
	foodTalksFlashBannedPath = "/news/admin/short/news/banned"
	// foodTalksFlashSEOSuffix 后台「发布快讯」页标题一变就把 SEO 标题填成「标题-FoodTalks全球食品资讯」，照它填。
	foodTalksFlashSEOSuffix = "-FoodTalks全球食品资讯"
)

// foodTalksFlash 后台列表、详情里的一条快讯（2026-11-29 正式环境 20281 的字段）。
type foodTalksFlash struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	Language      string `json:"language"`
	Content       string `json:"content"`
	SourceURL     string `json:"sourceUrl"`
	PublishStatus int    `json:"publishStatus"`
	IsBanned      int    `json:"isBanned"`
	PublishTime   string `json:"publishTime"` // 2026-11-28 10:03:25，北京时间
}

// live 前台看得见：发布了、没下架。
func (f foodTalksFlash) live() bool { return f.PublishStatus == 1 && f.IsBanned == 0 }

// foodTalksNewFlash 新建一条要填的。栏目、标签、封面人工发的也都空着，这里不填。
type foodTalksNewFlash struct {
	Title     string
	Content   string // 富文本
	SourceURL string
	Publish   bool // true 建好就公开；false 存草稿
}

// foodTalksFlashAPI 快讯要用的几件事；生产是 *foodTalksClient，测试换成内存里的假后台。
type foodTalksFlashAPI interface {
	CreateFlash(ctx context.Context, f foodTalksNewFlash) (int64, error)
	GetFlash(ctx context.Context, id int64) (foodTalksFlash, error)
	SearchFlashes(ctx context.Context, title string, size int) ([]foodTalksFlash, error)
	BanFlash(ctx context.Context, id int64) error
	RecentNewsTitles(ctx context.Context, size int) ([]foodTalksNewsListItem, error)
}

func (c *foodTalksClient) CreateFlash(ctx context.Context, f foodTalksNewFlash) (int64, error) {
	status := 0
	if f.Publish {
		status = 1
	}
	// 字段和后台「发布快讯」页提交的一样；id 传空串、发布时间空着由后台填。
	body := map[string]any{
		"id": "", "title": f.Title, "language": "ZH", "coverImg": "", "content": f.Content,
		"sourceUrl": f.SourceURL, "sort": 0, "publishStatus": status, "publishTime": "",
		"seoTitle": f.Title + foodTalksFlashSEOSuffix, "seoKeywords": "", "seoDescription": "",
		"categoryTagId": "", "shortNewsTagList": []any{}, "relatedCompanyIds": []any{},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("foodtalks: 序列化快讯: %w", err)
	}
	data, err := c.adminCall(ctx, "快讯", http.MethodPost, foodTalksFlashPath, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	var id int64
	if err := json.Unmarshal(data, &id); err != nil || id <= 0 {
		return 0, fmt.Errorf("FoodTalks 新建快讯成功但没回编号（响应 data：%s）", c.snippet(data))
	}
	return id, nil
}

func (c *foodTalksClient) GetFlash(ctx context.Context, id int64) (foodTalksFlash, error) {
	if id <= 0 {
		return foodTalksFlash{}, fmt.Errorf("读快讯要编号，收到 %d", id)
	}
	data, err := c.adminCall(ctx, "快讯", http.MethodGet, foodTalksFlashPath+"/"+strconv.FormatInt(id, 10), nil)
	if err != nil {
		return foodTalksFlash{}, err
	}
	var f foodTalksFlash
	if err := json.Unmarshal(data, &f); err != nil {
		return foodTalksFlash{}, fmt.Errorf("FoodTalks 快讯详情读不懂（响应片段：%s）: %w", c.snippet(data), err)
	}
	return f, nil
}

// SearchFlashes 后台列表按标题模糊查（title 空就是最新的几条），按发布时间从新到旧。
func (c *foodTalksClient) SearchFlashes(ctx context.Context, title string, size int) ([]foodTalksFlash, error) {
	if size <= 0 {
		size = 20
	}
	query := url.Values{"current": {"1"}, "size": {strconv.Itoa(size)}}
	if title = strings.TrimSpace(title); title != "" {
		query.Set("title", title)
	}
	data, err := c.adminCall(ctx, "快讯", http.MethodGet, foodTalksFlashPagePath+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var page struct {
		Records []foodTalksFlash `json:"records"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("FoodTalks 快讯列表读不懂（响应片段：%s）: %w", c.snippet(data), err)
	}
	return page.Records, nil
}

func (c *foodTalksClient) BanFlash(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("下架快讯要编号，收到 %d", id)
	}
	query := url.Values{"id": {strconv.FormatInt(id, 10)}, "bannedStatus": {"1"}}
	_, err := c.adminCall(ctx, "快讯", http.MethodPut, foodTalksFlashBannedPath+"?"+query.Encode(), nil)
	return err
}

// RecentNewsTitles 资讯后台列表最新的几条（标题、发布时间），查网站上是不是已经发过同一件事。
func (c *foodTalksClient) RecentNewsTitles(ctx context.Context, size int) ([]foodTalksNewsListItem, error) {
	if size <= 0 {
		size = 50
	}
	query := url.Values{"current": {"1"}, "size": {strconv.Itoa(size)}}
	data, err := c.adminCall(ctx, "资讯列表", http.MethodGet, foodTalksNewsPath+"/page?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var page struct {
		Records []foodTalksNewsListItem `json:"records"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("FoodTalks 资讯列表读不懂（响应片段：%s）: %w", c.snippet(data), err)
	}
	return page.Records, nil
}

// foodTalksFlashHTML 快讯正文段落 → 后台富文本：照人工发的样子，一段一个 <p>，段与段之间隔一个空段。
func foodTalksFlashHTML(paragraphs []string) string {
	parts := make([]string, 0, len(paragraphs))
	for _, p := range paragraphs {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, "<p>"+html.EscapeString(p)+"</p>")
		}
	}
	return strings.Join(parts, "\n<p>&nbsp;</p>\n")
}
