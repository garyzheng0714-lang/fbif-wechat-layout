package main

// publish_status_callback.go — 任务状态一变就推给资讯订阅（2026-11-08 资讯订阅提的需求：用户的
// 规矩是不轮询、有改动才同步）。对外约定只在 docs/publish-status-callback.md，改这里要同步改它。
//
// 怎么转：
//   - 资讯订阅调 site-sync / official-sync 时带 status_callback_url（只许指向 FOODTALKS_FEED_BASE_URL
//     同一个源），登记在任务上（registerStatusCallback），登记那一刻先推一次当前状态。
//   - 任务库每次写盘（updateIf）都拿 sync-status 会回的那份紧凑摘要比一次指纹（不算 updated_at）；
//     变了就在同一次原子写里 seq+1、存下这一刻的摘要（stampPublishStatusCallback），写完叫醒发送器。
//     所以不管哪段代码改了阶段、错误、链接，调用方都不用操心推送。
//   - 发送器一个 goroutine 串行发，按 Standard Webhooks 签名（密钥是两边已共用的
//     PUBLISH_SYNC_SERVICE_TOKEN，不新建、也不随请求发出去）。没发出去之前又变了，只发最新的一版。
//   - 失败按 publishStatusCallbackRetryDelays 退避，连续 8 次不成就放弃这一版（dead_letter），
//     下一次状态变化重新开始；对方回 410 就不再推（gone），重新登记才恢复。
//   - 只有送达、放弃、410 才写盘（每次送达多写一次任务库）；进程重启后，没送达的按盘上 seq 补推。

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	publishStatusCallbackEventType = "publish.job.status"
	// publishStatusCallbackDeadLetter 这一版连续失败 publishStatusCallbackMaxAttempts 次，放弃了；状态再变会重新推。
	publishStatusCallbackDeadLetter = "dead_letter"
	// publishStatusCallbackGone 对方回了 410：这个地址不要了，不再推，重新登记才恢复。
	publishStatusCallbackGone = "gone"

	publishStatusCallbackTimeout     = 15 * time.Second
	publishStatusCallbackMaxURLBytes = 2048
	publishStatusCallbackWarnFor     = 7 * 24 * time.Hour
)

// publishStatusCallbackRetryDelays 第 N 次失败后隔多久再试；用完（共 8 次）就放弃这一版。
// 只在出错时跑、成了就停，按中台「轮询和定时任务」的口径不用登记。
var publishStatusCallbackRetryDelays = []time.Duration{
	5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute,
	30 * time.Minute, time.Hour, 2 * time.Hour,
}

var publishStatusCallbackMaxAttempts = len(publishStatusCallbackRetryDelays) + 1

// publishStatusCallback 记在任务上的推送状态。Seq/Snapshot 与任务状态同一次写盘，
// DeliveredSeq 是对方回过 2xx 的最大一版。
type publishStatusCallback struct {
	URL string `json:"url,omitempty"`
	// Seq 状态每变一次加一；Fingerprint 是这一版摘要（不算 updated_at）的指纹；Snapshot 是这一版要推的摘要。
	Seq          int64           `json:"seq,omitempty"`
	Fingerprint  string          `json:"fingerprint,omitempty"`
	Snapshot     json.RawMessage `json:"snapshot,omitempty"`
	QueuedAt     string          `json:"queued_at,omitempty"`
	DeliveredSeq int64           `json:"delivered_seq,omitempty"`
	DeliveredAt  string          `json:"delivered_at,omitempty"`
	State        string          `json:"state,omitempty"` // "" / dead_letter / gone
	StateAt      string          `json:"state_at,omitempty"`
	Error        string          `json:"error,omitempty"`
}

// foodTalksFeedBaseURL 资讯订阅的地址（FOODTALKS_FEED_BASE_URL，没配就是正式域名），不带结尾斜杠。
func foodTalksFeedBaseURL() string {
	if base := strings.TrimRight(strings.TrimSpace(os.Getenv("FOODTALKS_FEED_BASE_URL")), "/"); base != "" {
		return base
	}
	return defaultFoodTalksFeedBase
}

// publishStatusCallbackTarget 校验调用方给的回调地址：只许和资讯订阅同一个源（协议 + 域名 + 端口），
// 不带用户信息和 #。空串表示没带，照旧不推。
func publishStatusCallbackTarget(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > publishStatusCallbackMaxURLBytes {
		return "", fmt.Errorf("status_callback_url 太长（最多 %d 字节）", publishStatusCallbackMaxURLBytes)
	}
	base, err := url.Parse(foodTalksFeedBaseURL())
	if err != nil || base.Host == "" {
		return "", errors.New("FOODTALKS_FEED_BASE_URL 不是有效地址，暂时不能登记 status_callback_url")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", errors.New("status_callback_url 要是完整地址，不带用户信息和 #")
	}
	if !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Host, base.Host) {
		return "", fmt.Errorf("status_callback_url 只能指向资讯订阅（%s://%s）", base.Scheme, base.Host)
	}
	if !publishSyncServiceTokenConfigured() {
		return "", errors.New("PUBLISH_SYNC_SERVICE_TOKEN 未配置，没法给回调签名")
	}
	return u.String(), nil
}

// registerStatusCallback 把回调地址登记到任务上，并让它马上推一次当前状态。同一个地址重复登记不动，
// 除非上次已放弃（dead_letter）或对方回过 410（gone）。
func (s *publishJobStore) registerStatusCallback(id int64, target string) (publishJob, error) {
	job, _, err := s.updateIf(id, func(current publishJob) bool {
		cb := current.StatusCallback
		return cb.URL != target || cb.State != ""
	}, func(j *publishJob) {
		j.StatusCallback.URL = target
		j.StatusCallback.State = ""
		j.StatusCallback.StateAt = ""
		j.StatusCallback.Error = ""
		j.StatusCallback.Fingerprint = "" // 指纹清空 → 这次写盘必定排上一版
	})
	return job, err
}

func publishStatusFingerprint(summary publishSyncJobSummary) string {
	summary.UpdatedAt = ""
	b, _ := json.Marshal(summary)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// stampPublishStatusCallback 由 updateIf 在写盘前调用：登记过回调、摘要变了，就排上新的一版。
// 返回 true 表示写完要叫醒发送器。
func stampPublishStatusCallback(job *publishJob) bool {
	cb := &job.StatusCallback
	if cb.URL == "" {
		return false
	}
	summary := newPublishSyncJobSummary(*job)
	fp := publishStatusFingerprint(summary)
	if fp == cb.Fingerprint {
		return false
	}
	snapshot, err := json.Marshal(summary)
	if err != nil {
		return false
	}
	cb.Seq++
	cb.Fingerprint = fp
	cb.Snapshot = snapshot
	cb.QueuedAt = job.UpdatedAt
	if cb.State == publishStatusCallbackDeadLetter { // 新的一版重新开始推
		cb.State, cb.StateAt, cb.Error = "", "", ""
	}
	return cb.State != publishStatusCallbackGone
}

// publishStatusCallbackBody 推送的请求体。同一版每次重试一字不差（签名里的时间戳另算）。
func publishStatusCallbackBody(jobID, seq int64, queuedAt string, snapshot json.RawMessage) ([]byte, error) {
	type data struct {
		JobID int64           `json:"job_id"`
		Seq   int64           `json:"seq"`
		Job   json.RawMessage `json:"job"`
	}
	return json.Marshal(struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Data      data   `json:"data"`
	}{publishStatusCallbackEventType, queuedAt, data{jobID, seq, snapshot}})
}

func publishStatusCallbackMessageID(jobID, seq int64) string {
	return fmt.Sprintf("layout-job-%d-%d", jobID, seq)
}

// signPublishStatusCallback Standard Webhooks 的对称签名：v1,base64(HMAC-SHA256(密钥, "id.时间戳.请求体"))。
func signPublishStatusCallback(secret, msgID string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s.%d.", msgID, timestamp)
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// ---- 发送器 ----

type publishStatusCallbackEntry struct {
	jobID    int64
	url      string
	seq      int64
	queuedAt string
	snapshot json.RawMessage
	attempts int
	nextAt   time.Time
	lastErr  string
}

type publishStatusCallbackResult int

const (
	publishStatusCallbackDelivered publishStatusCallbackResult = iota
	publishStatusCallbackFailed
	publishStatusCallbackRefused // 410
)

// publishStatusCallbacks 每个任务只留最新一版待推；一个 goroutine 串行发，失败按退避排下一次。
type publishStatusCallbacks struct {
	store  *publishJobStore
	client *http.Client
	now    func() time.Time
	jitter func(time.Duration) time.Duration

	mu    sync.Mutex
	due   map[int64]*publishStatusCallbackEntry
	wake  chan struct{}
	start sync.Once
}

// newPublishStatusCallbacks 建发送器并挂到任务库上（任务库写盘后叫醒它）。
func newPublishStatusCallbacks(store *publishJobStore) *publishStatusCallbacks {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	c := &publishStatusCallbacks{
		store: store,
		client: &http.Client{
			Timeout:   publishStatusCallbackTimeout,
			Transport: transport,
			// 3xx 一律算失败、不跟：签过名的请求不送到别处去。
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now:    time.Now,
		jitter: randomNotificationJitter,
		due:    map[int64]*publishStatusCallbackEntry{},
		wake:   make(chan struct{}, 1),
	}
	if store != nil {
		store.statusCallbacks = c
	}
	return c
}

// queued 任务库写盘后（或启动恢复时）交来任务：有没送达的一版就排上。
func (c *publishStatusCallbacks) queued(job publishJob) {
	cb := job.StatusCallback
	if c == nil || cb.URL == "" || cb.Seq <= cb.DeliveredSeq || cb.State != "" || len(cb.Snapshot) == 0 {
		return
	}
	c.mu.Lock()
	if cur := c.due[job.ID]; cur == nil || cur.seq < cb.Seq || cur.url != cb.URL {
		c.due[job.ID] = &publishStatusCallbackEntry{
			jobID: job.ID, url: cb.URL, seq: cb.Seq, queuedAt: cb.QueuedAt,
			snapshot: append(json.RawMessage(nil), cb.Snapshot...), nextAt: c.now(),
		}
	}
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// run 生产里唯一的发送 goroutine：有事就发，没事就等写盘叫醒或下一次重试到点，不空转。
func (c *publishStatusCallbacks) run() {
	for {
		next := c.drain(context.Background())
		var timer *time.Timer
		var fire <-chan time.Time
		if !next.IsZero() {
			timer = time.NewTimer(max(time.Until(next), 0))
			fire = timer.C
		}
		select {
		case <-c.wake:
		case <-fire:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func (c *publishStatusCallbacks) startSending() {
	if c == nil {
		return
	}
	c.start.Do(func() { go c.run() })
}

// drain 把已到点的全发掉，返回下一次重试的时刻（没有就是零值）。
func (c *publishStatusCallbacks) drain(ctx context.Context) time.Time {
	if c == nil || publishWorkersPaused() {
		return time.Time{}
	}
	for {
		entry, next := c.nextDue()
		if entry == nil {
			return next
		}
		result, retryAfter, errText := c.deliver(ctx, *entry)
		c.settle(*entry, result, retryAfter, errText)
	}
}

func (c *publishStatusCallbacks) nextDue() (*publishStatusCallbackEntry, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	ids := make([]int64, 0, len(c.due))
	for id := range c.due {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var next time.Time
	for _, id := range ids {
		e := c.due[id]
		if !e.nextAt.After(now) {
			copied := *e
			return &copied, time.Time{}
		}
		if next.IsZero() || e.nextAt.Before(next) {
			next = e.nextAt
		}
	}
	return nil, next
}

func (c *publishStatusCallbacks) deliver(ctx context.Context, e publishStatusCallbackEntry) (publishStatusCallbackResult, time.Duration, string) {
	secret := publishSyncServiceToken()
	if secret == "" {
		return publishStatusCallbackFailed, 0, "PUBLISH_SYNC_SERVICE_TOKEN 未配置，没法签名"
	}
	body, err := publishStatusCallbackBody(e.jobID, e.seq, e.queuedAt, e.snapshot)
	if err != nil {
		return publishStatusCallbackFailed, 0, "请求体生成失败：" + err.Error()
	}
	msgID := publishStatusCallbackMessageID(e.jobID, e.seq)
	timestamp := c.now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return publishStatusCallbackFailed, 0, "请求生成失败：" + err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "FBIF-layout-status-callback/1")
	req.Header.Set("webhook-id", msgID)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("webhook-signature", signPublishStatusCallback(secret, msgID, timestamp, body))
	resp, err := c.client.Do(req)
	if err != nil {
		return publishStatusCallbackFailed, 0, "资讯订阅不可达：" + err.Error()
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return publishStatusCallbackDelivered, 0, ""
	case resp.StatusCode == http.StatusGone:
		return publishStatusCallbackRefused, 0, "对方回 410，不再推"
	default:
		text := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if s := strings.Join(strings.Fields(string(snippet)), " "); s != "" {
			text += "：" + s
		}
		return publishStatusCallbackFailed, publishRetryAfter(resp.Header.Get("Retry-After"), c.now()), text
	}
}

// publishRetryAfter 读对方的 Retry-After（秒数或 HTTP 日期），最多认到最长的那档退避。
func publishRetryAfter(raw string, now time.Time) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.Atoi(raw); err == nil {
		d = time.Duration(secs) * time.Second
	} else if at, err := http.ParseTime(raw); err == nil {
		d = at.Sub(now)
	}
	return min(max(d, 0), publishStatusCallbackRetryDelays[len(publishStatusCallbackRetryDelays)-1])
}

func (c *publishStatusCallbacks) settle(sent publishStatusCallbackEntry, result publishStatusCallbackResult, retryAfter time.Duration, errText string) {
	now := c.now().UTC()
	stamp := now.Format(time.RFC3339)
	switch result {
	case publishStatusCallbackDelivered:
		c.forget(sent, false)
		if _, _, err := c.store.updateIf(sent.jobID, func(current publishJob) bool {
			return current.StatusCallback.URL == sent.url && current.StatusCallback.DeliveredSeq < sent.seq
		}, func(j *publishJob) {
			j.StatusCallback.DeliveredSeq = sent.seq
			j.StatusCallback.DeliveredAt = stamp
		}); err != nil {
			log.Printf("publish: 任务 #%d 状态推送已送达，记账失败（重启后会再推一次）：%v", sent.jobID, err)
		}
	case publishStatusCallbackRefused:
		c.forget(sent, true)
		log.Printf("publish: 任务 #%d 状态推送对方回 410，这个地址不再推：%s", sent.jobID, sent.url)
		c.persistStop(sent, publishStatusCallbackGone, stamp, errText, false)
	default:
		c.mu.Lock()
		cur := c.due[sent.jobID]
		if cur == nil || cur.seq != sent.seq || cur.url != sent.url {
			c.mu.Unlock() // 发的时候又变了：新的一版自己从头试
			return
		}
		cur.attempts++
		cur.lastErr = errText
		attempts := cur.attempts
		if attempts >= publishStatusCallbackMaxAttempts {
			delete(c.due, sent.jobID)
		} else {
			delay := publishStatusCallbackRetryDelays[attempts-1]
			if c.jitter != nil {
				delay += c.jitter(delay / 5)
			}
			cur.nextAt = now.Add(max(delay, retryAfter))
		}
		c.mu.Unlock()
		if attempts >= publishStatusCallbackMaxAttempts {
			log.Printf("publish: 任务 #%d 第 %d 版状态连续 %d 次没推出去，放弃这一版：%s", sent.jobID, sent.seq, attempts, errText)
			c.persistStop(sent, publishStatusCallbackDeadLetter, stamp, errText, true)
		} else {
			log.Printf("publish: 任务 #%d 第 %d 版状态推送失败（第 %d 次），稍后再试：%s", sent.jobID, sent.seq, attempts, errText)
		}
	}
}

// forget 从待推里拿掉刚发的这一版；anySeq 时连同一地址更新的版本一起拿掉（410）。
func (c *publishStatusCallbacks) forget(sent publishStatusCallbackEntry, anySeq bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cur := c.due[sent.jobID]; cur != nil && cur.url == sent.url && (anySeq || cur.seq == sent.seq) {
		delete(c.due, sent.jobID)
	}
}

func (c *publishStatusCallbacks) persistStop(sent publishStatusCallbackEntry, state, stamp, errText string, sameSeq bool) {
	if _, _, err := c.store.updateIf(sent.jobID, func(current publishJob) bool {
		cb := current.StatusCallback
		return cb.URL == sent.url && (!sameSeq || cb.Seq == sent.seq)
	}, func(j *publishJob) {
		j.StatusCallback.State = state
		j.StatusCallback.StateAt = stamp
		j.StatusCallback.Error = truncateRunes(errText, 500)
	}); err != nil {
		log.Printf("publish: 任务 #%d 记状态推送 %s 失败：%v", sent.jobID, state, err)
	}
}

// statusCallbackDeadLetterRecent 7 天内放弃过的推送：monitoring 给 warning（不挡 ready，资讯订阅还有兜底回查）。
func statusCallbackDeadLetterRecent(job publishJob, now time.Time) bool {
	cb := job.StatusCallback
	if cb.State != publishStatusCallbackDeadLetter {
		return false
	}
	at, err := time.Parse(time.RFC3339, cb.StateAt)
	return err != nil || now.Sub(at) <= publishStatusCallbackWarnFor
}
