package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 自家号转发外部公众号：先查 Feed 订阅源，没有再走 TikHub 广域搜。
// 查到原文只记账，不立刻订阅；同一公众号累计转发满 3 篇且有微信链接，才向 Feed 订阅（2026-11-01 用户：「5 次有点严格，3 次吧」），
// 并请 Feed 顺手开快速订阅（2026-11-02 用户：「满三次自动快速订阅」）。
// 启动恢复会把已满阈值、还没订上或快速订阅还没开成的号都排进订阅，所以改阈值、改规则后已满的号重启即补。

const (
	repostSubscribeThreshold         = 3
	feedOriginalArticleSubscribePath = "/api/internal/original-article/subscribe"
	// Feed 订上后还要查、开、再查今天看啥的快速订阅，一次要十几秒（2026-11-02 8 秒超时把全球烘焙指南打断在核对那一步）；
	// 后台异步跑，放到 50 秒，仍短于网关默认的 60 秒。
	feedOriginalArticleSubscribeTO = 50 * time.Second
	repostSubscribeIdle            = ""
	repostSubscribePending         = "pending"
	repostSubscribeSubscribing     = "subscribing"
	repostSubscribeAccepted        = "accepted"
	repostSubscribeSuccess         = "success"
	repostSubscribeFailed          = "failed"
)

// errFeedFastPending Feed 回 200 订上了，但快速订阅没开成（今天看啥积分不够、核对没过，或 Feed 没回结果）；
// 订阅记成功、快速订阅记失败，下次重启或这个号再转一篇时再交（已开的 Feed 不再扣分）。
var errFeedFastPending = errors.New("已订阅，快速订阅没开成")

// errFeedSubscribeAccepted Feed 回 202：今天看啥收下了、还没确认关注，号也还没进 Feed；
// 不算失败，下次重启或这个号再转一篇时照样再提交，确认了才回 200。
var errFeedSubscribeAccepted = errors.New("已向 Feed 提交")

type publishRepostArticle struct {
	Title string `json:"title,omitempty"`
	URL   string `json:"url"`
	JobID int64  `json:"job_id,omitempty"`
	At    string `json:"at,omitempty"`
}

type publishRepostSource struct {
	Name             string                 `json:"name"`
	Key              string                 `json:"key"`
	Articles         []publishRepostArticle `json:"articles"`
	SubscribeState   string                 `json:"subscribe_state,omitempty"`
	FastState        string                 `json:"fast_state,omitempty"`
	SubscribeError   string                 `json:"subscribe_error,omitempty"`
	SubscribeAt      string                 `json:"subscribe_at,omitempty"`
	LastSubscribeURL string                 `json:"last_subscribe_url,omitempty"`
}

type publishRepostSourcesDoc struct {
	Sources []publishRepostSource `json:"sources"`
}

type publishRepostSourceStore struct {
	mu   sync.Mutex
	path string
}

func publishRepostSourcesPath(jobsPath string) string {
	if v := strings.TrimSpace(os.Getenv("PUBLISH_REPOST_SOURCES_PATH")); v != "" {
		return v
	}
	if strings.TrimSpace(jobsPath) != "" {
		return filepath.Join(filepath.Dir(jobsPath), "publish-repost-sources.json")
	}
	return filepath.Join("data", "publish-repost-sources.json")
}

func newPublishRepostSourceStore(path string) *publishRepostSourceStore {
	return &publishRepostSourceStore{path: strings.TrimSpace(path)}
}

func (s *publishRepostSourceStore) record(name, articleURL, title string, jobID int64, at time.Time) (publishRepostSource, bool, error) {
	name = strings.TrimSpace(name)
	articleURL = strings.TrimSpace(articleURL)
	if s == nil || name == "" || !isWechatArticleURL(articleURL) {
		return publishRepostSource{}, false, nil
	}
	key := normalizeFoodTalksSourceName(name)
	if key == "" {
		return publishRepostSource{}, false, nil
	}
	urlKey := canonicalOriginalURLKey(articleURL)
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return publishRepostSource{}, false, err
	}
	idx := -1
	for i := range doc.Sources {
		if doc.Sources[i].Key == key {
			idx = i
			break
		}
	}
	if idx < 0 {
		doc.Sources = append(doc.Sources, publishRepostSource{Name: name, Key: key})
		idx = len(doc.Sources) - 1
	}
	src := doc.Sources[idx]
	for _, item := range src.Articles {
		if item.JobID == jobID && jobID > 0 {
			return src, false, nil
		}
		if canonicalOriginalURLKey(item.URL) == urlKey {
			return src, false, nil
		}
	}
	src.Articles = append(src.Articles, publishRepostArticle{
		Title: strings.TrimSpace(title),
		URL:   articleURL,
		JobID: jobID,
		At:    at.UTC().Format(time.RFC3339),
	})
	if src.Name == "" {
		src.Name = name
	}
	doc.Sources[idx] = src
	if err := s.writeLocked(doc); err != nil {
		return publishRepostSource{}, false, err
	}
	return src, true, nil
}

// markSubscribe 记订阅状态；fastState 为空表示快速订阅状态不变。
func (s *publishRepostSourceStore) markSubscribe(key, state, fastState, articleURL, errText string, at time.Time) error {
	key = normalizeFoodTalksSourceName(key)
	if s == nil || key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return err
	}
	for i := range doc.Sources {
		if doc.Sources[i].Key != key {
			continue
		}
		doc.Sources[i].SubscribeState = state
		if fastState != "" {
			doc.Sources[i].FastState = fastState
		}
		doc.Sources[i].SubscribeError = strings.TrimSpace(errText)
		if strings.TrimSpace(articleURL) != "" {
			doc.Sources[i].LastSubscribeURL = articleURL
		}
		if state == repostSubscribeSuccess || state == repostSubscribeFailed || state == repostSubscribeSubscribing {
			doc.Sources[i].SubscribeAt = at.UTC().Format(time.RFC3339)
		}
		return s.writeLocked(doc)
	}
	return nil
}

func (s *publishRepostSourceStore) byKey(name string) (publishRepostSource, bool, error) {
	key := normalizeFoodTalksSourceName(name)
	if s == nil || key == "" {
		return publishRepostSource{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return publishRepostSource{}, false, err
	}
	for _, src := range doc.Sources {
		if src.Key == key {
			return src, true, nil
		}
	}
	return publishRepostSource{}, false, nil
}

func (s *publishRepostSourceStore) pendingSubscribe() []publishRepostSource {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return nil
	}
	var out []publishRepostSource
	for _, src := range doc.Sources {
		if src.readyToSubscribe() || src.stuckSubscribing() {
			out = append(out, src)
		}
	}
	return out
}

// stuckSubscribing 订阅请求发出后进程退出，状态停在 subscribing；只在启动恢复时捞回来重交（Feed 订阅幂等）。
func (src publishRepostSource) stuckSubscribing() bool {
	return src.SubscribeState == repostSubscribeSubscribing &&
		len(src.Articles) >= repostSubscribeThreshold &&
		strings.TrimSpace(src.subscribeURL()) != ""
}

func (src publishRepostSource) readyToSubscribe() bool {
	if src.SubscribeState == repostSubscribeSubscribing {
		return false
	}
	if src.SubscribeState == repostSubscribeSuccess && src.FastState == repostSubscribeSuccess {
		return false
	}
	if len(src.Articles) < repostSubscribeThreshold {
		return false
	}
	return strings.TrimSpace(src.subscribeURL()) != ""
}

// subscribeURL 给 Feed 的那篇链接。阅读原文记下的多是 http://，Feed 只收 https://mp.weixin.qq.com
// （2026-10-19 起联商网、咖门、中国企业家杂志因此天天 400），送出前统一升成 https。
func (src publishRepostSource) subscribeURL() string {
	if u, ok := normalizeWechatWorkURL(src.LastSubscribeURL); ok {
		return u
	}
	for i := len(src.Articles) - 1; i >= 0; i-- {
		if u, ok := normalizeWechatWorkURL(src.Articles[i].URL); ok {
			return u
		}
	}
	return ""
}

func (s *publishRepostSourceStore) readLocked() (publishRepostSourcesDoc, error) {
	doc := publishRepostSourcesDoc{}
	if s.path == "" {
		return doc, nil
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return doc, err
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return publishRepostSourcesDoc{}, fmt.Errorf("转载公众号记录损坏（%s）: %w", s.path, err)
	}
	return doc, nil
}

func (s *publishRepostSourceStore) writeLocked(doc publishRepostSourcesDoc) error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".publish-repost-sources-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.path)
}

func defaultSubscribeFeedWechatArticle(ctx context.Context, sourceName, articleURL string) error {
	token := strings.TrimSpace(os.Getenv("PUBLISH_SYNC_SERVICE_TOKEN"))
	if token == "" {
		return fmt.Errorf("同步服务令牌未配置")
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("FOODTALKS_FEED_BASE_URL")), "/")
	if base == "" {
		base = defaultFoodTalksFeedBase
	}
	payload, err := json.Marshal(map[string]any{
		"source_name": strings.TrimSpace(sourceName),
		"url":         strings.TrimSpace(articleURL),
		"fast":        true,
	})
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, feedOriginalArticleSubscribeTO)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, base+feedOriginalArticleSubscribePath, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Publish-Sync-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("订阅接口不可达: %w", err)
	}
	defer resp.Body.Close()
	body, readErr := readBoundedBody(resp.Body, 1<<20)
	if readErr != nil {
		return fmt.Errorf("订阅响应读取失败: %w", readErr)
	}
	var failure struct {
		Error     string `json:"error"`
		Message   string `json:"message"`
		Fast      bool   `json:"fast"`
		FastError string `json:"fast_error"`
	}
	_ = json.Unmarshal(body, &failure)
	if resp.StatusCode == http.StatusOK {
		if failure.Fast {
			return nil
		}
		return fmt.Errorf("%w：%s", errFeedFastPending, firstNonEmpty(strings.TrimSpace(failure.FastError), "Feed 没回快速订阅结果"))
	}
	detail := firstNonEmpty(strings.TrimSpace(failure.Message), strings.TrimSpace(failure.Error))
	if resp.StatusCode == http.StatusAccepted {
		return fmt.Errorf("%w：%s", errFeedSubscribeAccepted, firstNonEmpty(detail, "等今天看啥确认"))
	}
	if detail != "" {
		return fmt.Errorf("订阅失败（HTTP %d）：%s", resp.StatusCode, detail)
	}
	return fmt.Errorf("订阅失败（HTTP %d）", resp.StatusCode)
}

func (p *publishPipeline) recordPublishedRepost(ctx context.Context, job publishJob) {
	if p == nil || p.repostSources == nil || !publishJobIsOwnAccountRepost(job) {
		return
	}
	_, sourceName := publishArticleProvenance(job)
	articleURL := strings.TrimSpace(job.OriginalSourceURL)
	if !isWechatArticleURL(articleURL) {
		return
	}
	src, added, err := p.repostSources.record(sourceName, articleURL, job.Title, job.ID, pipelineNow(p))
	if err != nil {
		log.Printf("publish: 记录转载公众号失败（任务 #%d）：%v", job.ID, err)
		return
	}
	if !added {
		return
	}
	if src.readyToSubscribe() {
		p.scheduleRepostSourceSubscribe(src.Name)
	}
}

func (p *publishPipeline) scheduleRepostSourceSubscribe(sourceName string) {
	if p == nil {
		return
	}
	p.spawnExternalWork(func() { p.completeRepostSourceSubscribe(sourceName) })
}

func (p *publishPipeline) completeRepostSourceSubscribe(sourceName string) {
	if p == nil || p.repostSources == nil {
		return
	}
	src, ok, err := p.repostSources.byKey(sourceName)
	if err != nil || !ok || !src.readyToSubscribe() {
		return
	}
	articleURL := src.subscribeURL()
	if articleURL == "" {
		return
	}
	if err := p.repostSources.markSubscribe(src.Name, repostSubscribeSubscribing, "", articleURL, "", pipelineNow(p)); err != nil {
		log.Printf("publish: 标记转载公众号订阅中失败（%s）：%v", src.Name, err)
		return
	}
	var subscribeErr error
	if p.subscribeFeedWechatArticle == nil {
		subscribeErr = fmt.Errorf("订阅接口未配置")
	} else {
		subscribeErr = p.subscribeFeedWechatArticle(context.Background(), src.Name, articleURL)
	}
	state := repostSubscribeSuccess
	fastState := repostSubscribeSuccess
	errText := ""
	if subscribeErr != nil {
		errText = truncateRunes(strings.Join(strings.Fields(subscribeErr.Error()), " "), 300)
		fastState = repostSubscribeFailed
		if errors.Is(subscribeErr, errFeedFastPending) {
			log.Printf("publish: 转载满 %d 次，已订阅 Feed（%s），快速订阅没开成：%s", repostSubscribeThreshold, src.Name, errText)
		} else if errors.Is(subscribeErr, errFeedSubscribeAccepted) {
			state = repostSubscribeAccepted
			log.Printf("publish: 转载满 %d 次，已向 Feed 提交订阅（%s），等今天看啥确认：%s", repostSubscribeThreshold, src.Name, errText)
		} else {
			state = repostSubscribeFailed
			log.Printf("publish: 转载满 %d 次后订阅 Feed 失败（%s）：%s", repostSubscribeThreshold, src.Name, errText)
		}
		if src.SubscribeState == repostSubscribeSuccess {
			// 早先已订上、这回只为补开快速订阅：订阅照旧记成功，只记快速订阅没开成。
			state = repostSubscribeSuccess
		}
	}
	if err := p.repostSources.markSubscribe(src.Name, state, fastState, articleURL, errText, pipelineNow(p)); err != nil {
		log.Printf("publish: 回写转载公众号订阅状态失败（%s）：%v", src.Name, err)
	}
}

func (p *publishPipeline) recoverRepostSourceSubscribe() {
	if p == nil || p.repostSources == nil || publishWorkersPaused() {
		return
	}
	for _, src := range p.repostSources.pendingSubscribe() {
		if src.SubscribeState == repostSubscribeSubscribing {
			_ = p.repostSources.markSubscribe(src.Name, repostSubscribePending, "", src.subscribeURL(), "", pipelineNow(p))
		}
		p.scheduleRepostSourceSubscribe(src.Name)
	}
}
