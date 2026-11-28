package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// wakeServer 记下收到的叫醒；回什么码由 code 定。
type wakeServer struct {
	*httptest.Server
	code    atomic.Int32
	count   atomic.Int32
	mu      sync.Mutex
	invalid []string
}

func newWakeServer(t *testing.T, code int) *wakeServer {
	t.Helper()
	s := &wakeServer{}
	s.code.Store(int32(code))
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		if r.Method != http.MethodPost || r.URL.Path != publishMonitorWakePath || len(body) != 0 ||
			r.Header.Get("Authorization") != "" || r.Header.Get("X-Publish-Sync-Token") != "" {
			s.invalid = append(s.invalid, r.Method+" "+r.URL.Path+" body="+string(body))
		}
		s.mu.Unlock()
		s.count.Add(1)
		w.WriteHeader(int(s.code.Load()))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *wakeServer) waitCount(t *testing.T, want int32) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for s.count.Load() < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// 多等一会，确认不会多发。
	time.Sleep(50 * time.Millisecond)
	if got := s.count.Load(); got != want {
		t.Fatalf("叫醒次数 = %d，应为 %d", got, want)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.invalid) > 0 {
		t.Fatalf("叫醒只许 POST、不带正文和 token：%v", s.invalid)
	}
}

func testMonitorWake(t *testing.T, server *wakeServer, status func(time.Time) publishMonitoringStatus) *publishMonitorWake {
	t.Helper()
	w := newPublishMonitorWake(server.URL+publishMonitorWakePath, status)
	w.settle = 0
	w.delays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w.begin(ctx)
	return w
}

func TestMonitorWakeOnlyWhenAlertCategoriesChange(t *testing.T) {
	server := newWakeServer(t, http.StatusAccepted)
	feed := make(chan []string)
	go func() { feed <- []string{"failed_jobs_recent:1"} }() // 启动时的基线
	w := testMonitorWake(t, server, func(time.Time) publishMonitoringStatus {
		return publishMonitoringStatus{AlertIssues: <-feed}
	})
	steps := []struct {
		issues []string
		wakes  int32 // 到这一步为止一共叫醒几次
	}{
		{[]string{"failed_jobs_recent:3"}, 0}, // 只是数量变了
		{[]string{"failed_jobs_recent:3", "processing_stale:1300s"}, 1},
		{[]string{"processing_stale:1400s", "failed_jobs_recent:2"}, 1}, // 类别没变
		{[]string{"failed_jobs_recent:2"}, 2},
		{[]string{}, 3},
		{[]string{}, 3},
	}
	for i, step := range steps {
		w.notify()
		feed <- step.issues
		if i > 0 {
			// 喂下这一步时上一步一定已经发完（同一个 goroutine 串行）。
			if got := server.count.Load(); got != steps[i-1].wakes {
				t.Fatalf("第 %d 步后叫醒 %d 次，应为 %d", i, got, steps[i-1].wakes)
			}
		}
	}
	server.waitCount(t, 3)
}

func TestMonitorWakeStartupDoesNotWake(t *testing.T) {
	server := newWakeServer(t, http.StatusAccepted)
	testMonitorWake(t, server, func(time.Time) publishMonitoringStatus {
		return publishMonitoringStatus{AlertIssues: []string{"foodtalks_credential_invalid"}}
	})
	server.waitCount(t, 0)
}

func TestMonitorWakeRetriesUnreachableThenGivesUp(t *testing.T) {
	for _, tc := range []struct {
		name  string
		code  int
		wakes int32
	}{
		{"5xx 隔 1、2、5、10 分钟再发 4 次后放弃", http.StatusBadGateway, 5},
		{"403 还没放行 IP，不重发", http.StatusForbidden, 1},
		{"别的 4xx 不重发", http.StatusNotFound, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newWakeServer(t, tc.code)
			var calls atomic.Int32
			w := testMonitorWake(t, server, func(time.Time) publishMonitoringStatus {
				if calls.Add(1) == 1 {
					return publishMonitoringStatus{}
				}
				return publishMonitoringStatus{AlertIssues: []string{"queue_stale:901s"}}
			})
			w.notify()
			server.waitCount(t, tc.wakes)
		})
	}
	if !slices.Equal(publishMonitorWakeRetryDelays, []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute}) {
		t.Fatalf("重发间隔 = %v", publishMonitorWakeRetryDelays)
	}
	w := newPublishMonitorWake("http://127.0.0.1:1"+publishMonitorWakePath, nil)
	if w.client.Timeout != 5*time.Second || w.client.Transport.(*http.Transport).Proxy != nil {
		t.Fatalf("叫醒要 5 秒超时、直连不走代理：timeout=%v", w.client.Timeout)
	}
	if w.send(context.Background()) != true {
		t.Fatal("连不上要重发")
	}
}

func TestMonitorWakeRetryRestartsOnNewChange(t *testing.T) {
	server := newWakeServer(t, http.StatusServiceUnavailable)
	feed := make(chan []string)
	go func() { feed <- nil }()
	w := testMonitorWake(t, server, func(time.Time) publishMonitoringStatus {
		return publishMonitoringStatus{AlertIssues: <-feed}
	})
	w.delays = []time.Duration{time.Hour} // 第一次没送到，要等一小时才重发
	w.notify()
	feed <- []string{"queue_stale:901s"}
	server.waitCount(t, 1)
	server.code.Store(http.StatusAccepted)
	w.notify()
	feed <- []string{"queue_stale:950s", "processing_stale:1201s"} // 又多一类：不等重发，马上叫
	server.waitCount(t, 2)
}

// 只靠时间流逝就会翻的告警：到点自己重算，不靠写盘、不另设定时。
func TestMonitorWakeRechecksWhenTimeFlipsAlert(t *testing.T) {
	server := newWakeServer(t, http.StatusAccepted)
	var calls atomic.Int32
	testMonitorWake(t, server, func(now time.Time) publishMonitoringStatus {
		if calls.Add(1) == 1 {
			return publishMonitoringStatus{nextChange: now.Add(20 * time.Millisecond)}
		}
		return publishMonitoringStatus{AlertIssues: []string{"processing_stale:1201s"}}
	})
	server.waitCount(t, 1)
}

func TestMonitorWakeFollowsStoreWritesAndCredential(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	server := newWakeServer(t, http.StatusAccepted)
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	auth := &foodTalksAuthHealth{}
	job, _, err := store.create("site:https://mp.weixin.qq.com/s/wake", "https://mp.weixin.qq.com/s/wake")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.update(job.ID, func(j *publishJob) { j.Stage = publishStageAwaitingReview; j.NotificationKind = "card" }); err != nil {
		t.Fatal(err)
	}
	w := testMonitorWake(t, server, func(now time.Time) publishMonitoringStatus {
		return currentPublishMonitoring(store, auth.invalid(), 0, now)
	})
	store.onWrite(w.notify)
	auth.onFlip(w.notify)
	server.waitCount(t, 0)

	if _, err := store.update(job.ID, func(j *publishJob) { j.Stage = publishStageFailed; j.Error = "抓取失败" }); err != nil {
		t.Fatal(err)
	}
	server.waitCount(t, 1) // 多了「近期失败」「失败没闭环」
	if _, err := store.update(job.ID, func(j *publishJob) { j.Error = "抓取又失败" }); err != nil {
		t.Fatal(err)
	}
	server.waitCount(t, 1) // 告警类别没变

	auth.record(false)
	server.waitCount(t, 2) // 凭证失效
	auth.record(false)
	server.waitCount(t, 2)
	auth.record(true)
	server.waitCount(t, 3)
}

func TestMonitorWakeOnlyInProduction(t *testing.T) {
	t.Setenv(foodTalksExpectedTargetEnv, "test")
	p := &publishPipeline{store: newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json")), foodTalksAuth: &foodTalksAuthHealth{}}
	p.startPublishMonitorWake()
	if p.store.written != nil || p.foodTalksAuth.flipped != nil {
		t.Fatal("测试站不叫醒正式的看门狗")
	}
}

func TestPublishMonitoringNextChangeIsWhenAlertsFlip(t *testing.T) {
	t.Setenv(deploymentWritesPausedEnv, "0")
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	queued, _, err := store.create("site:https://mp.weixin.qq.com/s/next-queued", "https://mp.weixin.qq.com/s/next-queued")
	if err != nil {
		t.Fatal(err)
	}
	at, _ := time.Parse(time.RFC3339, queued.UpdatedAt)
	categories := func(now time.Time) []string {
		return publishAlertCategories(currentPublishMonitoring(store, false, 0, now).AlertIssues)
	}

	first := currentPublishMonitoring(store, false, 0, at.Add(time.Minute))
	if want := at.Add(publishQueueCriticalAfter + time.Second); !first.nextChange.Equal(want) {
		t.Fatalf("排队满 15 分钟那一刻要重算：%v，应为 %v", first.nextChange, want)
	}
	if got := categories(first.nextChange.Add(-time.Second)); len(got) != 0 {
		t.Fatalf("到点前不告警：%v", got)
	}
	second := currentPublishMonitoring(store, false, 0, first.nextChange)
	if got := publishAlertCategories(second.AlertIssues); !slices.Equal(got, []string{"queue_stale"}) {
		t.Fatalf("到点就告警：%v", got)
	}
	if want := at.Add(publishProcessingCriticalAfter + time.Second); !second.nextChange.Equal(want) {
		t.Fatalf("下一刻是处理中满 20 分钟：%v，应为 %v", second.nextChange, want)
	}

	if _, err := store.update(queued.ID, func(j *publishJob) { j.Stage = publishStageFailed }); err != nil {
		t.Fatal(err)
	}
	failed, _ := store.byID(queued.ID)
	failedAt, _ := time.Parse(time.RFC3339, failed.UpdatedAt)
	status := currentPublishMonitoring(store, false, 0, failedAt.Add(time.Hour))
	if want := failedAt.Add(publishRecentFailureWindow + time.Second); !status.nextChange.Equal(want) {
		t.Fatalf("近期失败满 24 小时那一刻要重算：%v，应为 %v", status.nextChange, want)
	}
	if got := categories(status.nextChange); !slices.Equal(got, []string{"failed_jobs_unresolved"}) {
		t.Fatalf("过了 24 小时少了「近期失败」一类：%v", got)
	}

	if _, err := store.update(queued.ID, func(j *publishJob) { j.Stage = publishStagePublished }); err != nil {
		t.Fatal(err)
	}
	if done := currentPublishMonitoring(store, false, 0, time.Now()); !done.nextChange.IsZero() {
		t.Fatalf("没有会随时间翻的事就不用重算：%v", done.nextChange)
	}
}
