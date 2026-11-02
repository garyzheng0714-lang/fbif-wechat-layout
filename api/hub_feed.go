package main

// hub_feed.go — 读中台下发的资源（配置包 hub_bundle.go、密钥 hub_secrets.go）：每个资源一个长等待循环，
// 有变化中台才回话（2026-11-03 和中台定的约定，One-Api 仓库 .local/poll-audit-20261103/contract.md）。
//
// 每次请求带 Prefer: wait=10800 和上一次 200 的 ETag。中台照办了等待，回包就带 Preference-Applied，
// 收到就马上再问，请求一直挂在中台上，内容一变中台立刻回 200。长等待是唯一的路：回包不带 Preference-Applied
// 是意外，和网络错、超时、5xx 一样退避，1 秒起翻倍到 5 分钟（±20%）；401/403/404 隔 5 分钟。
// 开机和断开后的那一次不带 If-None-Match：中台马上回 200，断开期间错过的变化当场补上，新鲜度也当场续上。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"
)

const (
	hubWaitSeconds = 10800
	hubHTTPTimeout = hubWaitSeconds*time.Second + time.Minute
	hubDeniedEvery = 5 * time.Minute
	hubBackoffMax  = 5 * time.Minute
	// hubMinGap 中台一秒内就回了话（连着几次改表、通道时断时续）也隔满一秒再问，不空转。
	hubMinGap      = time.Second
	hubMaxResponse = 1 << 20
)

// hubOutcome 问一轮的结果，决定隔多久再问。
type hubOutcome int

const (
	hubWaiting  hubOutcome = iota // 回了话，中台照办了等待：马上再问，下一个请求挂在中台上
	hubAnswered                   // 回了话，中台却没挂着等（意外）：算连着中台，按出错退避
	hubDenied                     // 401/403/404：5 分钟后再问
	hubFailed                     // 网络错、超时、5xx、回包读不懂：退避
)

// hubNextWait 这一轮之后隔多久再问。failures 是连着几轮没挂着等（出错或意外回包），spread 是 0.8–1.2 的抖动。
func hubNextWait(outcome hubOutcome, failures int, elapsed time.Duration, spread float64) time.Duration {
	switch outcome {
	case hubWaiting:
		return max(hubMinGap-elapsed, 0)
	case hubDenied:
		return hubDeniedEvery
	}
	backoff := hubBackoffMax
	if failures < 10 {
		backoff = min(time.Second<<max(failures-1, 0), hubBackoffMax)
	}
	return time.Duration(float64(backoff) * spread)
}

// hubReplied 中台回了 200 或 304：照办了等待（Preference-Applied）就马上再问。
func hubReplied(resp *http.Response) hubOutcome {
	if resp.Header.Get("Preference-Applied") != "" {
		return hubWaiting
	}
	return hubAnswered
}

// hubFeed 中台的一个下发资源：一条长等待请求、它的 ETag，和最后一次确知连着中台的时刻。
type hubFeed struct {
	url  string
	key  string
	http *http.Client
	now  func() time.Time

	ready     chan struct{} // 开机第一轮问完就关
	readyOnce sync.Once

	mu       sync.Mutex
	etag     string
	refetch  bool      // 下一次不带 If-None-Match：开机、断开之后
	waiting  bool      // 上一轮中台照办了等待：这一轮的请求挂在中台上
	inFlight bool      // 这一轮的请求发出去了、还没回来
	seenAt   time.Time // 最后一次确知连着中台：收到回包，或挂着的请求断开的那一刻
}

func newHubFeed(url, key string) *hubFeed {
	return &hubFeed{url: url, key: key, now: time.Now, refetch: true, ready: make(chan struct{}), http: &http.Client{
		Timeout:       hubHTTPTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (f *hubFeed) String() string {
	return fmt.Sprintf("{URL:%q Key:[REDACTED]}", f.url)
}

// ask 发这一轮的请求，读回包。
func (f *hubFeed) ask(ctx context.Context) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+f.key)
	req.Header.Set("Prefer", fmt.Sprintf("wait=%d", hubWaitSeconds))
	f.mu.Lock()
	if f.etag != "" && !f.refetch {
		req.Header.Set("If-None-Match", f.etag)
	}
	f.mu.Unlock()
	resp, err := f.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, hubMaxResponse+1))
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > hubMaxResponse {
		return nil, nil, errors.New("回包太大")
	}
	return resp, raw, nil
}

// setETag 换上这次 200 的 ETag，回内容是不是换了（没有 ETag 一律算换了）。
func (f *hubFeed) setETag(etag string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	changed := etag == "" || etag != f.etag
	f.etag = etag
	return changed
}

// round 问一轮：poll 发请求并换上内容。挂着的请求断开了，算到断开那一刻都连着中台：
// 它挂着的时候中台一有变化（含来源变旧）就会回话。
func (f *hubFeed) round(ctx context.Context, poll func(context.Context) hubOutcome) hubOutcome {
	f.mu.Lock()
	f.inFlight = true
	f.mu.Unlock()
	outcome := poll(ctx)
	at := f.now()
	f.mu.Lock()
	switch outcome {
	case hubWaiting, hubAnswered:
		f.seenAt, f.refetch = at, false
	default:
		if f.waiting {
			f.seenAt = at
		}
		f.refetch = f.refetch || outcome == hubFailed
	}
	f.waiting, f.inFlight = outcome == hubWaiting, false
	f.mu.Unlock()
	f.readyOnce.Do(func() { close(f.ready) })
	return outcome
}

// liveAt 最后一次确知连着中台的时刻；长等待请求正挂着就是 now。
func (f *hubFeed) liveAt(now time.Time) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.waiting && f.inFlight {
		return now
	}
	return f.seenAt
}

// start 一直问下去。
func (f *hubFeed) start(poll func(context.Context) hubOutcome) {
	go func() {
		failures, unheld := 0, false
		for {
			started := time.Now()
			outcome := f.round(context.Background(), poll)
			if outcome == hubFailed || outcome == hubAnswered {
				failures++
			} else {
				failures = 0
			}
			if outcome == hubAnswered && !unheld {
				log.Printf("publish: 中台回了话却没挂着等（回包没带 Preference-Applied），按出错退避再问：%s", f.url)
			}
			unheld = outcome == hubAnswered
			time.Sleep(hubNextWait(outcome, failures, time.Since(started), 0.8+0.4*rand.Float64()))
		}
	}()
}

// waitReady 等开机第一轮问完，最多等到 ctx 到点。
func (f *hubFeed) waitReady(ctx context.Context) {
	select {
	case <-f.ready:
	case <-ctx.Done():
	}
}
