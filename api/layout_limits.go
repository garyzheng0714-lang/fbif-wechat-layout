package main

// layout_limits.go — 排版接口的限额与时限。数字只写在下面的常量里，说明书「限额」一节读 layoutLimits 这张表，
// 不手写数字。

import (
	"fmt"
	"sync"
	"time"
)

const (
	layoutPerKeyConcurrent     = 4  // 每把钥匙同时在排（含 202 之后还在后台排的）
	layoutPerKeyPerMinute      = 30 // 每把钥匙每分钟新提交
	layoutPollPerKeyPerMinute  = 120
	layoutWechatFetchPerMinute = 20 // 全部钥匙合计抓公众号：保护服务器出口，别让公众号限流波及编辑和发布

	layoutDefaultWait       = 90 * time.Second
	layoutMaxWait           = 100 * time.Second
	layoutPollDefaultWait   = 25 * time.Second
	layoutPollMaxWait       = maxFeishuStatusLongPollWait // 25 秒，和编辑器的飞书长轮询同一个上限
	layoutPendingRetryAfter = 2 * time.Second
	layoutClientTimeout     = 130 * time.Second // 说明书里建议调用方设的 HTTP 超时：大于 wait_seconds 上限加余量

	layoutOperationTimeout  = 5 * time.Minute // 一篇从收下到排完的总时限
	layoutResultTTL         = 15 * time.Minute
	layoutResultMaxBytes    = 256 << 20
	layoutTombstoneTTL      = 24 * time.Hour
	layoutMaxTombstones     = 4096
	layoutUploadReadTimeout = 3 * time.Minute // 只放宽上传 Word 的那一个请求；全局 ReadTimeout 30 秒不动

	layoutJSONMaxBytes    = 8 << 20
	layoutFileMaxBytes    = maxDocUploadBytes // 50 MB，和网页上传 .doc 同一个常量
	layoutDocxMaxBytes    = 16 << 20          // 拆掉图片之后剩下的 Word；base64 后仍在排版程序的请求上限内
	layoutTitleMaxRunes   = 200
	layoutNodeMaxBody     = 24 << 20 // 排版程序（Node）一次请求的上限
	layoutNodeMaxResponse = 32 << 20
	layoutNodeConcurrent  = 1 // 排版程序本来就一篇一篇排；排队只在 Go 这边
	layoutNodeTimeout     = 90 * time.Second

	layoutInlineImagesMaxBytes = 16 << 20 // 成品里 data: 位图加起来的上限（页脚、素材图一般几十 KB）
	layoutDataImageUploads     = 4        // data: 图同时传几张

	layoutFeishuConcurrent  = 2 // 接口自己的飞书导出位，编辑器那份额度不动
	layoutFeishuMaxJobs     = 16
	layoutFeishuResultBytes = 64 << 20
)

// layoutLimit 说明书「限额」表的一行。
type layoutLimit struct {
	Name  string
	Value string
	Note  string
}

func layoutDurationLabel(d time.Duration) string {
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%d 小时", d/time.Hour)
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%d 分钟", d/time.Minute)
	default:
		return fmt.Sprintf("%d 秒", d/time.Second)
	}
}

func layoutBytesLabel(n int64) string { return fmt.Sprintf("%d MB", n>>20) }

var layoutLimits = []layoutLimit{
	{"每把钥匙同时在排", fmt.Sprint(layoutPerKeyConcurrent), "从提交算到排完（拿到 202 之后后台还在排的也算），超了回 503 busy"},
	{"每把钥匙每分钟提交", fmt.Sprint(layoutPerKeyPerMinute), "超了回 429 rate_limited，retry_after_seconds 写着还要等几秒"},
	{"每把钥匙每分钟轮询", fmt.Sprint(layoutPollPerKeyPerMinute), "GET poll_url 的次数，超了回 429 rate_limited"},
	{"全部调用方合计每分钟抓公众号", fmt.Sprint(layoutWechatFetchPerMinute), "保护服务器出口，公众号限流会波及编辑；超了回 429 rate_limited"},
	{"POST 最多等", layoutDurationLabel(layoutMaxWait), fmt.Sprintf("wait_seconds 默认 %d，可填 0 到 %d；到点没排完回 202", int(layoutDefaultWait/time.Second), int(layoutMaxWait/time.Second))},
	{"轮询一次最多等", layoutDurationLabel(layoutPollMaxWait), fmt.Sprintf("GET poll_url 可带 wait_seconds（0 到 %d，默认 %d）", int(layoutPollMaxWait/time.Second), int(layoutPollDefaultWait/time.Second))},
	{"一篇最长排", layoutDurationLabel(layoutOperationTimeout), "超了回 504 timeout"},
	{"排好的结果保留", layoutDurationLabel(layoutResultTTL), "过期回 404 operation_expired；服务器内存紧张时最早排好的会先清掉"},
	{"JSON 请求体", layoutBytesLabel(layoutJSONMaxBytes), "超了回 413 request_too_large"},
	{"上传的 Word", layoutBytesLabel(layoutFileMaxBytes), fmt.Sprintf("超了回 413 file_too_large；上传本身最多可以传 %s", layoutDurationLabel(layoutUploadReadTimeout))},
	{"Word 去掉图片后", layoutBytesLabel(layoutDocxMaxBytes), "图片单独转存不算在内；超了多半是嵌了附件或对象，回 413 file_too_large"},
	{"标题", fmt.Sprintf("%d 字", layoutTitleMaxRunes), "超了回 400 bad_request"},
	{"建议的客户端超时", layoutDurationLabel(layoutClientTimeout), "比 wait_seconds 上限多留余量，curl 用 --max-time 130"},
}

// layoutWindow 一个固定 1 分钟的计数窗口。
type layoutWindow struct {
	started time.Time
	count   int
}

// take 在窗口里记一次；满了返回还要等多久。
func (w *layoutWindow) take(now time.Time, limit int) (time.Duration, bool) {
	if w.started.IsZero() || now.Before(w.started) || now.Sub(w.started) >= time.Minute {
		*w = layoutWindow{started: now}
	}
	if w.count >= limit {
		wait := time.Minute - now.Sub(w.started)
		if wait < time.Second {
			wait = time.Second
		}
		return wait, false
	}
	w.count++
	return 0, true
}

// layoutLimiter 每把钥匙的并发位与每分钟次数，以及全局的公众号抓取次数。只在内存，重启归零。
type layoutLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	running map[string]int
	renders map[string]*layoutWindow
	polls   map[string]*layoutWindow
	wechat  layoutWindow
}

func newLayoutLimiter(now func() time.Time) *layoutLimiter {
	if now == nil {
		now = time.Now
	}
	return &layoutLimiter{
		now:     now,
		running: map[string]int{},
		renders: map[string]*layoutWindow{},
		polls:   map[string]*layoutWindow{},
	}
}

func layoutWindowFor(windows map[string]*layoutWindow, key string) *layoutWindow {
	w := windows[key]
	if w == nil {
		w = &layoutWindow{}
		windows[key] = w
	}
	return w
}

// acquireRender 收一篇新稿：先看并发位（满了不扣次数），再扣每分钟次数，最后占位。
// 位子由排这篇的后台任务在排完时还（不是 handler 返回时），所以 202 之后还在排的照样占着。
func (l *layoutLimiter) acquireRender(caller string) (func(), *layoutError) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.running[caller] >= layoutPerKeyConcurrent {
		return nil, newLayoutError(layoutErrBusy,
			fmt.Sprintf("这把钥匙已经有 %d 篇在排，等排完再发", layoutPerKeyConcurrent))
	}
	if wait, ok := layoutWindowFor(l.renders, caller).take(l.now(), layoutPerKeyPerMinute); !ok {
		return nil, newLayoutError(layoutErrRateLimited,
			fmt.Sprintf("这把钥匙一分钟内已经提交了 %d 篇", layoutPerKeyPerMinute)).withRetryAfter(wait)
	}
	l.running[caller]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.running[caller] <= 1 {
				delete(l.running, caller)
				return
			}
			l.running[caller]--
		})
	}, nil
}

func (l *layoutLimiter) allowPoll(caller string) *layoutError {
	l.mu.Lock()
	defer l.mu.Unlock()
	if wait, ok := layoutWindowFor(l.polls, caller).take(l.now(), layoutPollPerKeyPerMinute); !ok {
		return newLayoutError(layoutErrRateLimited,
			fmt.Sprintf("这把钥匙一分钟内已经轮询了 %d 次；轮询请带 wait_seconds 长等，不要连着发", layoutPollPerKeyPerMinute)).withRetryAfter(wait)
	}
	return nil
}

func (l *layoutLimiter) allowWechatFetch() *layoutError {
	l.mu.Lock()
	defer l.mu.Unlock()
	if wait, ok := l.wechat.take(l.now(), layoutWechatFetchPerMinute); !ok {
		return newLayoutError(layoutErrRateLimited,
			fmt.Sprintf("所有调用方这一分钟合计已经抓了 %d 篇公众号文章", layoutWechatFetchPerMinute)).withRetryAfter(wait)
	}
	return nil
}

func (l *layoutLimiter) runningFor(caller string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running[caller]
}
