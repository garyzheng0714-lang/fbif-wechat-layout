package main

// foodtalks_banner.go — FoodTalks 官网轮播图的后台接口（群硕 2026-11-23 文档，2026-11-27 测试环境实测）。
// 用法和坑见 docs/foodtalks-home-banner.md，要点：
//   - 新建 POST /basic/admin/advertisement，建好立刻上线，没有草稿，也没有幂等键；
//   - 上下架 PUT /basic/admin/advertisement/banned?id=&bannedStatus=（1 下架），参数只认网址里的；
//   - 排序 PUT /basic/admin/advertisement/sort?id=&sort=，sort 大的排前面（文档写反了），默认 sort 等于 id；
//   - 没有删除接口（DELETE 回 500），不要的只能下架；
//   - 后台列表 GET /basic/admin/advertisement/page 的 title 是模糊查，position、isBanned 是精确筛。
// 请求头、凭证、出错分类与发资讯同一个客户端（foodtalks.go）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	foodTalksBannerPath       = "/basic/admin/advertisement"
	foodTalksBannerPagePath   = "/basic/admin/advertisement/page"
	foodTalksBannerBannedPath = "/basic/admin/advertisement/banned"
	foodTalksBannerSortPath   = "/basic/admin/advertisement/sort"
)

// foodTalksBanner 后台列表、详情里的一张轮播图（2026-11-28 正式环境 5874 的字段）。
type foodTalksBanner struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Img         string `json:"img"`
	ImgMobile   string `json:"imgMobile"`
	URL         string `json:"url"`
	Position    string `json:"position"`
	Sort        int64  `json:"sort"`
	IsBanned    int    `json:"isBanned"`
	IsActive    int    `json:"isActive"`
	CreatedTime string `json:"createdTime"` // 2026-11-25 08:55:02，北京时间
}

// live 在首页上看得见：没下架、启用着。
func (b foodTalksBanner) live() bool {
	return b.IsBanned == 0 && b.IsActive == 1
}

// foodTalksNewBanner 新建一张轮播图要填的。人工放的那几张 tagId 都是空，这里不传。
type foodTalksNewBanner struct {
	Img       string `json:"img"`
	ImgMobile string `json:"imgMobile"`
	Position  string `json:"position"`
	Title     string `json:"title"`
	URL       string `json:"url"`
}

// foodTalksBannerQuery 后台列表的筛选。Title 是模糊查；OnlyLive 只要没下架的。
type foodTalksBannerQuery struct {
	Position string
	Title    string
	OnlyLive bool
	Size     int
}

// foodTalksBannerAPI 首页轮播图要用的四件事；生产是 *foodTalksClient，测试换成内存里的假官网。
type foodTalksBannerAPI interface {
	CreateBanner(ctx context.Context, b foodTalksNewBanner) (int64, error)
	ListBanners(ctx context.Context, q foodTalksBannerQuery) ([]foodTalksBanner, error)
	BanBanner(ctx context.Context, id int64) error
	SortBanner(ctx context.Context, id, sort int64) error
}

func (c *foodTalksClient) CreateBanner(ctx context.Context, b foodTalksNewBanner) (int64, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return 0, fmt.Errorf("foodtalks: 序列化轮播图: %w", err)
	}
	data, err := c.bannerCall(ctx, http.MethodPost, foodTalksBannerPath, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	var id int64
	if err := json.Unmarshal(data, &id); err != nil || id <= 0 {
		return 0, fmt.Errorf("FoodTalks 新建轮播图成功但没回编号（响应 data：%s）", c.snippet(data))
	}
	return id, nil
}

func (c *foodTalksClient) ListBanners(ctx context.Context, q foodTalksBannerQuery) ([]foodTalksBanner, error) {
	query := url.Values{}
	query.Set("current", "1")
	size := q.Size
	if size <= 0 {
		size = 50
	}
	query.Set("size", strconv.Itoa(size))
	if q.Position != "" {
		query.Set("position", q.Position)
	}
	if strings.TrimSpace(q.Title) != "" {
		query.Set("title", strings.TrimSpace(q.Title))
	}
	if q.OnlyLive {
		query.Set("isBanned", "0")
	}
	data, err := c.bannerCall(ctx, http.MethodGet, foodTalksBannerPagePath+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var page struct {
		Records []foodTalksBanner `json:"records"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("FoodTalks 轮播图列表读不懂（响应片段：%s）: %w", c.snippet(data), err)
	}
	// 网址里的筛选万一被后台忽略，这里再筛一遍，免得把不相干的图当成首页上的。
	out := make([]foodTalksBanner, 0, len(page.Records))
	for _, b := range page.Records {
		if q.Position != "" && b.Position != q.Position {
			continue
		}
		if q.OnlyLive && !b.live() {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

func (c *foodTalksClient) BanBanner(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("下架轮播图要编号，收到 %d", id)
	}
	query := url.Values{"id": {strconv.FormatInt(id, 10)}, "bannedStatus": {"1"}}
	_, err := c.bannerCall(ctx, http.MethodPut, foodTalksBannerBannedPath+"?"+query.Encode(), nil)
	return err
}

func (c *foodTalksClient) SortBanner(ctx context.Context, id, sort int64) error {
	if id <= 0 {
		return fmt.Errorf("轮播图排序要编号，收到 %d", id)
	}
	query := url.Values{"id": {strconv.FormatInt(id, 10)}, "sort": {strconv.FormatInt(sort, 10)}}
	_, err := c.bannerCall(ctx, http.MethodPut, foodTalksBannerSortPath+"?"+query.Encode(), nil)
	return err
}

// bannerCall 发一次轮播图请求，按 FoodTalks 的老规矩判结果：HTTP 200 也要看 code，成功时交回 data。
func (c *foodTalksClient) bannerCall(ctx context.Context, method, path string, body io.Reader) (json.RawMessage, error) {
	return c.adminCall(ctx, "轮播图", method, path, body)
}

// adminCall 发一次 FoodTalks 后台请求（轮播图、快讯共用）：HTTP 200 也要看 code，成功时交回 data；what 只用在出错的说法里。
func (c *foodTalksClient) adminCall(ctx context.Context, what, method, path string, body io.Reader) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("foodtalks: 构造%s请求: %w", what, err)
	}
	c.applyHeaders(req)
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("FoodTalks 接口不可达（%s %s）: %w", method, strings.SplitN(path, "?", 2)[0], err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("foodtalks: 读%s响应: %w", what, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.statusError(what+"接口", resp.StatusCode, respBody)
	}
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("FoodTalks %s响应不是预期 JSON（响应片段：%s）: %w", what, c.snippet(respBody), err)
	}
	if env.Code != 0 {
		return nil, &foodTalksAPIError{Code: env.Code, Msg: env.Msg}
	}
	if len(env.Data) == 0 {
		return nil, fmt.Errorf("FoodTalks %s响应缺 data", what)
	}
	return env.Data, nil
}
