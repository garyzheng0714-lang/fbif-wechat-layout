package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// 叫醒资讯订阅的看门狗（2026-11-14 资讯订阅提的约定，见它仓库 docs/64-内容链路监控运行手册.md §7）：
// 看门狗平时不再每 2 分钟来问 GET /api/publish/monitoring，改成 4 小时兜底一次；
// 本服务「要找人的告警」（monitoring 的 alert_issues）多了一类或少了一类时，
// POST 一次 …/monitor-alert/wake 叫它马上来读。数量变了不叫，启动不叫。
//
// 什么时候重算告警：任务库每次写盘、FoodTalks 凭证结论翻转，再加上「只靠时间流逝就会翻」的
// 下一刻（处理中满 20 分钟、近期失败过 24 小时……由 currentPublishMonitoring 算出 nextChange）。
// 没有另设定时去问。发不出去（连不上、超时、5xx）隔 1、2、5、10 分钟再发，还不行记日志放弃；
// 403（资讯订阅还没把我们的出口 IP 登进中台）和别的 4xx 不重发，只记日志。
const (
	publishMonitorWakePath    = "/monitor-alert/wake"
	publishMonitorWakeTimeout = 5 * time.Second
	// 一阵连着的写盘（一篇稿推进几个阶段）只重算一次：任务库 25MB，重算要整份读一遍。
	publishMonitorWakeSettle = 5 * time.Second
)

var publishMonitorWakeRetryDelays = []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute}

type publishMonitorWake struct {
	url    string
	client *http.Client
	status func(now time.Time) publishMonitoringStatus
	now    func() time.Time
	settle time.Duration
	delays []time.Duration

	changed chan struct{}
	start   sync.Once
}

func newPublishMonitorWake(url string, status func(now time.Time) publishMonitoringStatus) *publishMonitorWake {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// 资讯订阅按来源 IP 放行：一律直连，不走环境里的代理。
	transport.Proxy = nil
	return &publishMonitorWake{
		url: url,
		client: &http.Client{
			Timeout:       publishMonitorWakeTimeout,
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		status:  status,
		now:     time.Now,
		settle:  publishMonitorWakeSettle,
		delays:  publishMonitorWakeRetryDelays,
		changed: make(chan struct{}, 1),
	}
}

// startPublishMonitorWake 只在正式环境开：测试站的告警不该叫醒正式的看门狗。
// 要在任务恢复、worker 起来之前调：基线取在这一刻，启动本身不叫醒。
func (p *publishPipeline) startPublishMonitorWake() {
	if p == nil || p.store == nil {
		return
	}
	if target := strings.ToLower(strings.TrimSpace(os.Getenv(foodTalksExpectedTargetEnv))); target != "production" {
		log.Printf("monitor-wake: FoodTalks 目标是 %q，不叫醒资讯订阅的看门狗", target)
		return
	}
	w := newPublishMonitorWake(foodTalksFeedBaseURL()+publishMonitorWakePath, func(now time.Time) publishMonitoringStatus {
		return currentPublishMonitoring(p.store, p.foodTalksAuth.invalid(), now)
	})
	w.begin(context.Background())
	p.store.onWrite(w.notify)
	p.foodTalksAuth.onFlip(w.notify)
	log.Printf("monitor-wake: 要找人的告警多一类或少一类就 POST %s", w.url)
}

// begin 取基线、起后台 goroutine；只起一次。
func (w *publishMonitorWake) begin(ctx context.Context) {
	w.start.Do(func() {
		last, next := w.observe()
		go w.run(ctx, last, next)
	})
}

// notify 告警可能变了：不等、不堵调用方（写盘在锁里调它）。
func (w *publishMonitorWake) notify() {
	if w == nil {
		return
	}
	select {
	case w.changed <- struct{}{}:
	default:
	}
}

func (w *publishMonitorWake) run(ctx context.Context, last []string, next time.Time) {
	recheck := time.NewTimer(time.Hour)
	resetAt(recheck, next, w.now())
	retry := time.NewTimer(time.Hour)
	stopTimer(retry)
	attempt := 0 // 这一次叫醒已经重发了几回
	deliver := func() {
		if !w.send(ctx) {
			return
		}
		if attempt >= len(w.delays) {
			log.Printf("monitor-wake: 叫醒连着 %d 次没送到，放弃；看门狗 4 小时兜底会自己来读", attempt+1)
			return
		}
		retry.Reset(w.delays[attempt])
		attempt++
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-retry.C:
			deliver()
			continue
		case <-recheck.C:
		case <-w.changed:
			if !sleepCtx(ctx, w.settle) {
				return
			}
			select {
			case <-w.changed:
			default:
			}
		}
		categories, next := w.observe()
		resetAt(recheck, next, w.now())
		if slices.Equal(categories, last) {
			continue
		}
		log.Printf("monitor-wake: 要找人的告警从 %v 变成 %v，叫醒看门狗", last, categories)
		last = categories
		// 这一次叫醒顶掉还没重发完的上一次。
		stopTimer(retry)
		attempt = 0
		deliver()
	}
}

// observe 现在要找人的告警有哪几类（排好序），以及下一次只靠时间就可能变的时刻。
func (w *publishMonitorWake) observe() ([]string, time.Time) {
	status := w.status(w.now())
	return publishAlertCategories(status.AlertIssues), status.nextChange
}

// publishAlertCategories "failed_jobs_recent:2" 这类只取冒号前的类别；去重、排序。
func publishAlertCategories(issues []string) []string {
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		category, _, _ := strings.Cut(issue, ":")
		if category = strings.TrimSpace(category); category != "" {
			out = append(out, category)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// send POST 一次，不带正文、不带 token。返回 true 表示没送到、值得过一会再发。
func (w *publishMonitorWake) send(ctx context.Context) (retry bool) {
	ctx, cancel := context.WithTimeout(ctx, publishMonitorWakeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, http.NoBody)
	if err != nil {
		log.Printf("monitor-wake: 地址不对 %q：%v", w.url, err)
		return false
	}
	resp, err := w.client.Do(req)
	if err != nil {
		log.Printf("monitor-wake: 叫醒没送到：%v", err)
		return true
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode/100 == 2: // 约定回 202
		log.Printf("monitor-wake: 看门狗已叫醒（%d）", resp.StatusCode)
		return false
	case resp.StatusCode >= 500:
		log.Printf("monitor-wake: 叫醒回 %d，过会再发", resp.StatusCode)
		return true
	case resp.StatusCode == http.StatusForbidden:
		log.Printf("monitor-wake: 叫醒回 403：资讯订阅还没在中台放行本机出口 IP，不重发")
		return false
	default:
		log.Printf("monitor-wake: 叫醒回 %d，不重发", resp.StatusCode)
		return false
	}
}

func resetAt(t *time.Timer, at, now time.Time) {
	stopTimer(t)
	if at.IsZero() {
		return
	}
	d := at.Sub(now)
	if d < 0 {
		d = 0
	}
	t.Reset(d)
}

func stopTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
