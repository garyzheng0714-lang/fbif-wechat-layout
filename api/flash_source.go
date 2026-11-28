package main

// flash_source.go — 快讯从资讯订阅取原文事实：POST {FOODTALKS_FEED_BASE_URL}/api/internal/flash-source，
// 请求体 {"article_id":"..."}，口令和取原创文章同一把（X-Publish-Sync-Token / PUBLISH_SYNC_SERVICE_TOKEN）。
// 字段与报错码以资讯订阅那边的约定为准（FoodTalks-资讯订阅 internal/api/flash_source.go，2026-11-29 上线）：
// 没口令或不对 401、没配口令 503、没给编号 400、没这篇 404、库出错 503。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const flashSourcePath = "/api/internal/flash-source"

// errFlashSourceNotFound 资讯订阅库里没有这篇：重试也没用。
var errFlashSourceNotFound = errors.New("资讯订阅库里没有这篇文章")

type flashSourceArticle struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Lang        string `json:"lang,omitempty"`
	Author      string `json:"author,omitempty"`
	PublishedAt string `json:"published_at"`
	SourceID    string `json:"source_id,omitempty"`
	SourceName  string `json:"source_name"`
	Lead        string `json:"lead,omitempty"`
	Text        string `json:"text"`
}

type flashSourceTranslation struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

type flashSourceJudgment struct {
	Genre      string   `json:"genre,omitempty"`
	Flags      []string `json:"flags,omitempty"`
	HardFilter string   `json:"hard_filter,omitempty"`
	SiteTier   string   `json:"site_tier,omitempty"`
}

type flashSourceReport struct {
	ArticleID   string `json:"article_id"`
	Title       string `json:"title"`
	SourceName  string `json:"source_name"`
	URL         string `json:"url"`
	Lang        string `json:"lang,omitempty"`
	PublishedAt string `json:"published_at"`
}

type flashSourceEvent struct {
	ID          string              `json:"id"`
	Title       string              `json:"title"`
	Tier        string              `json:"tier,omitempty"`
	SourceCount int                 `json:"source_count"`
	Reports     []flashSourceReport `json:"reports"`
}

// flashSourceDoc 资讯订阅给的一篇：原文、机器译文（外文且译好才有）、价值判定、同一件事的其他报道。
type flashSourceDoc struct {
	Article     flashSourceArticle      `json:"article"`
	Translation *flashSourceTranslation `json:"translation,omitempty"`
	Judgment    *flashSourceJudgment    `json:"judgment,omitempty"`
	Event       *flashSourceEvent       `json:"event,omitempty"`
}

func (d flashSourceDoc) hasFlag(flag string) bool {
	if d.Judgment == nil {
		return false
	}
	for _, f := range d.Judgment.Flags {
		if f == flag {
			return true
		}
	}
	return false
}

// earliestOtherReport 同一件事里比这篇早、而且不是同一家来源的最早那篇（规范：多家主流媒体都报了，援引最早的）。
func (d flashSourceDoc) earliestOtherReport() (flashSourceReport, bool) {
	if d.Event == nil {
		return flashSourceReport{}, false
	}
	own, ownOK := parseFlashTime(d.Article.PublishedAt)
	for _, r := range d.Event.Reports { // 早的在前
		if r.ArticleID == "" || r.ArticleID == d.Article.ID || strings.TrimSpace(r.SourceName) == strings.TrimSpace(d.Article.SourceName) {
			continue
		}
		at, ok := parseFlashTime(r.PublishedAt)
		if ownOK && (!ok || !at.Before(own)) {
			return flashSourceReport{}, false
		}
		return r, true
	}
	return flashSourceReport{}, false
}

func parseFlashTime(raw string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	return t, err == nil
}

// fetchFlashSource 取一篇。404 回 errFlashSourceNotFound（不重试），其余失败都可以过会儿再试。
func fetchFlashSource(ctx context.Context, articleID string) (flashSourceDoc, error) {
	token := strings.TrimSpace(os.Getenv(publishSyncServiceTokenEnv))
	if token == "" {
		return flashSourceDoc{}, errors.New("没配资讯订阅的服务口令（" + publishSyncServiceTokenEnv + "）")
	}
	body, _ := json.Marshal(map[string]string{"article_id": articleID})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, foodTalksFeedBaseURL()+flashSourcePath, bytes.NewReader(body))
	if err != nil {
		return flashSourceDoc{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(publishSyncServiceHeader, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return flashSourceDoc{}, fmt.Errorf("连不上资讯订阅：%w", err)
	}
	defer resp.Body.Close()
	raw, err := readBoundedBody(resp.Body, 8<<20)
	if err != nil {
		return flashSourceDoc{}, fmt.Errorf("读资讯订阅回复失败：%w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return flashSourceDoc{}, errFlashSourceNotFound
	default:
		return flashSourceDoc{}, fmt.Errorf("资讯订阅回 HTTP %d：%s", resp.StatusCode, truncateRunes(strings.TrimSpace(string(raw)), 200))
	}
	var doc flashSourceDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return flashSourceDoc{}, fmt.Errorf("资讯订阅回复读不懂：%w", err)
	}
	if strings.TrimSpace(doc.Article.Text) == "" && strings.TrimSpace(doc.Article.Lead) == "" {
		return flashSourceDoc{}, errors.New("资讯订阅给的这篇没有正文")
	}
	return doc, nil
}
