package main

// publish_status_callback_test.go — 任务状态推给资讯订阅（docs/publish-status-callback.md）。

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type statusCallbackHit struct {
	header http.Header
	body   []byte
}

type statusCallbackReceiver struct {
	t      *testing.T
	server *httptest.Server
	url    string

	mu      sync.Mutex
	hits    []statusCallbackHit
	replies []int // 依次回的状态码，用完回 204
	header  http.Header
}

// newStatusCallbackReceiver 起一个假的资讯订阅，并把 FOODTALKS_FEED_BASE_URL 指过去（回调只许同源）。
func newStatusCallbackReceiver(t *testing.T) *statusCallbackReceiver {
	t.Helper()
	r := &statusCallbackReceiver{t: t, header: http.Header{}}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.hits = append(r.hits, statusCallbackHit{header: req.Header.Clone(), body: body})
		code := http.StatusNoContent
		if len(r.replies) > 0 {
			code, r.replies = r.replies[0], r.replies[1:]
		}
		for k, v := range r.header {
			w.Header()[k] = v
		}
		r.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(r.server.Close)
	t.Setenv("FOODTALKS_FEED_BASE_URL", r.server.URL)
	r.url = r.server.URL + "/api/internal/layout-status"
	return r
}

func (r *statusCallbackReceiver) reply(codes ...int) {
	r.mu.Lock()
	r.replies = append(r.replies, codes...)
	r.mu.Unlock()
}

func (r *statusCallbackReceiver) received() []statusCallbackHit {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]statusCallbackHit(nil), r.hits...)
}

type statusCallbackPush struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Data      struct {
		JobID int64           `json:"job_id"`
		Seq   int64           `json:"seq"`
		Job   json.RawMessage `json:"job"`
	} `json:"data"`
}

// verifyStatusCallbackHit 照 Standard Webhooks 规范独立验签（不调被测的签名函数），再解出推送内容。
func verifyStatusCallbackHit(t *testing.T, hit statusCallbackHit) statusCallbackPush {
	t.Helper()
	id, ts, sig := hit.header.Get("webhook-id"), hit.header.Get("webhook-timestamp"), hit.header.Get("webhook-signature")
	if id == "" || ts == "" || sig == "" {
		t.Fatalf("推送缺签名头：%v", hit.header)
	}
	mac := hmac.New(sha256.New, []byte(testPublishSyncServiceToken))
	mac.Write([]byte(id + "." + ts + "." + string(hit.body)))
	if want := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)); !hmac.Equal([]byte(sig), []byte(want)) {
		t.Fatalf("签名对不上：got %s want %s", sig, want)
	}
	if strings.Contains(fmt.Sprint(hit.header), testPublishSyncServiceToken) {
		t.Fatalf("共享密钥不能随推送发出去：%v", hit.header)
	}
	if ct := hit.header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	var push statusCallbackPush
	if err := json.Unmarshal(hit.body, &push); err != nil {
		t.Fatalf("推送不是 JSON：%v %s", err, hit.body)
	}
	if push.Type != publishStatusCallbackEventType || push.Data.JobID <= 0 || push.Data.Seq <= 0 {
		t.Fatalf("推送缺字段：%s", hit.body)
	}
	if want := fmt.Sprintf("layout-job-%d-%d", push.Data.JobID, push.Data.Seq); id != want {
		t.Fatalf("webhook-id = %q，应为 %q", id, want)
	}
	return push
}

func pushedJob(t *testing.T, push statusCallbackPush) publishSyncJobSummary {
	t.Helper()
	var job publishSyncJobSummary
	if err := json.Unmarshal(push.Data.Job, &job); err != nil {
		t.Fatal(err)
	}
	return job
}

type statusCallbackClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *statusCallbackClock) get() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *statusCallbackClock) set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

func newTestStatusCallbacks(store *publishJobStore) (*publishStatusCallbacks, *statusCallbackClock) {
	clock := &statusCallbackClock{now: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)}
	cb := newPublishStatusCallbacks(store)
	cb.now = clock.get
	cb.jitter = nil
	return cb, clock
}

func siteSyncBody(callbackURL string) string {
	extra := ""
	if callbackURL != "" {
		extra = fmt.Sprintf(`,"status_callback_url":%q`, callbackURL)
	}
	return fmt.Sprintf(`{"url":%q,"content_kind":"article","classification":"ordinary_confirmed","classifier_version":"test-v1"%s}`,
		testPublishSourceURL, extra)
}

// 签名与 Standard Webhooks 官方库同一个结果（它 Go 库 TestWebhookSign 的那组数；资讯订阅可直接用
// standardwebhooks.NewWebhookRaw([]byte(PUBLISH_SYNC_SERVICE_TOKEN)) 验）。
func TestStatusCallbackSignatureMatchesStandardWebhooks(t *testing.T) {
	key, err := base64.StdEncoding.DecodeString("MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw")
	if err != nil {
		t.Fatal(err)
	}
	got := signPublishStatusCallback(string(key), "msg_p5jXN8AQM9LWM0D4loKWxJek", 1614265330, []byte(`{"test": 2432232314}`))
	if want := "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="; got != want {
		t.Fatalf("签名 = %s，官方库给 %s", got, want)
	}
}

// 不带回调地址：请求、响应、任务和以前一模一样，一条都不推。
func TestStatusCallbackAbsentKeepsOldBehavior(t *testing.T) {
	receiver := newStatusCallbackReceiver(t)
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	cb, _ := newTestStatusCallbacks(env.store)

	rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync", siteSyncBody(""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("site-sync %d %s", rec.Code, rec.Body.String())
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response) != 3 || response["job"] == nil || response["existing"] == nil || response["rerendered"] == nil {
		t.Fatalf("没带回调时响应只应有 job/existing/rerendered：%s", rec.Body.String())
	}
	jobs, _ := env.store.all()
	if len(jobs) != 1 || jobs[0].StatusCallback.URL != "" || jobs[0].StatusCallback.Seq != 0 {
		t.Fatalf("没带回调不应登记：%+v", jobs[0].StatusCallback)
	}
	if next := cb.drain(context.Background()); !next.IsZero() || len(receiver.received()) != 0 {
		t.Fatalf("没带回调不应推送：%d 条", len(receiver.received()))
	}
}

// 回调只许指向资讯订阅：别的域名、带用户信息的直接 400，任务不建。
func TestStatusCallbackRejectsForeignTarget(t *testing.T) {
	receiver := newStatusCallbackReceiver(t)
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	for _, target := range []string{
		"https://evil.example.com/hook",
		"http://127.0.0.1:18095/v1/drafts",
		strings.Replace(receiver.url, "://", "://user:pass@", 1),
		receiver.url + "#x",
		"/api/internal/layout-status",
	} {
		rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync", siteSyncBody(target))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "status_callback_url") {
			t.Fatalf("%s 应 400，实际 %d %s", target, rec.Code, rec.Body.String())
		}
	}
	if jobs, _ := env.store.all(); len(jobs) != 0 {
		t.Fatalf("回调地址不合格不应建任务：%d", len(jobs))
	}
}

// 登记即推一次当前状态；之后摘要每变一次推一次，内容就是 sync-status 那份；只改了摘要以外的字段不推。
func TestStatusCallbackPushesEachStatusChangeSigned(t *testing.T) {
	receiver := newStatusCallbackReceiver(t)
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	cb, _ := newTestStatusCallbacks(env.store)
	ctx := context.Background()

	rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync", siteSyncBody(receiver.url))
	if rec.Code != http.StatusCreated {
		t.Fatalf("site-sync %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Job               publishSyncJobSummary `json:"job"`
		StatusCallbackURL string                `json:"status_callback_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.StatusCallbackURL != receiver.url {
		t.Fatalf("响应应回显登记的地址：%s", rec.Body.String())
	}
	id := response.Job.ID

	cb.drain(ctx)
	hits := receiver.received()
	if len(hits) != 1 {
		t.Fatalf("登记后应先推一次当前状态，实际 %d 次", len(hits))
	}
	first := verifyStatusCallbackHit(t, hits[0])
	if first.Data.JobID != id || first.Data.Seq != 1 || pushedJob(t, first).Stage != publishStageAwaitingReview {
		t.Fatalf("第一次推送：%s", hits[0].body)
	}

	// 推的 job 就是 sync-status 那一项（updated_at 是这一版记下的时刻）。
	status := doPublishServiceReq(mux, http.MethodPost, "/api/publish/sync-status", fmt.Sprintf(`{"job_ids":[%d]}`, id))
	var polled struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &polled); err != nil || len(polled.Jobs) != 1 {
		t.Fatalf("sync-status：%v %s", err, status.Body.String())
	}
	var pushed map[string]any
	if err := json.Unmarshal(first.Data.Job, &pushed); err != nil {
		t.Fatal(err)
	}
	delete(pushed, "updated_at")
	delete(polled.Jobs[0], "updated_at")
	if a, b := fmt.Sprint(pushed), fmt.Sprint(polled.Jobs[0]); a != b {
		t.Fatalf("推送内容应与 sync-status 一致：\npush=%s\npoll=%s", a, b)
	}

	if _, err := env.store.update(id, func(j *publishJob) {
		j.Stage = publishStageRejected
		j.ReviewAction = "reject"
	}); err != nil {
		t.Fatal(err)
	}
	cb.drain(ctx)
	hits = receiver.received()
	if len(hits) != 2 {
		t.Fatalf("状态变了应再推一次，实际 %d 次", len(hits))
	}
	second := verifyStatusCallbackHit(t, hits[1])
	if second.Data.Seq != 2 || pushedJob(t, second).Stage != publishStageRejected || pushedJob(t, second).ReviewAction != "reject" {
		t.Fatalf("第二次推送：%s", hits[1].body)
	}

	// 摘要以外的字段（卡片封面）变了、同一地址再登记一次：都不推。
	if _, err := env.store.update(id, func(j *publishJob) { j.ReviewCoverImgKey = "img_cover" }); err != nil {
		t.Fatal(err)
	}
	if rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync", siteSyncBody(receiver.url)); rec.Code != http.StatusOK {
		t.Fatalf("重复提交 %d %s", rec.Code, rec.Body.String())
	}
	cb.drain(ctx)
	if n := len(receiver.received()); n != 2 {
		t.Fatalf("摘要没变不应推送，实际共 %d 次", n)
	}
	job, _ := env.store.byID(id)
	if job.StatusCallback.DeliveredSeq != 2 || job.StatusCallback.State != "" {
		t.Fatalf("送达应记在任务上：%+v", job.StatusCallback)
	}
}

// 还没发出去又变了：只推最新的一版。
func TestStatusCallbackCoalescesUnsentVersions(t *testing.T) {
	receiver := newStatusCallbackReceiver(t)
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", testPublishSyncServiceToken)
	env := newPublishTestEnv(t)
	cb, _ := newTestStatusCallbacks(env.store)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.registerStatusCallback(job.ID, receiver.url); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{publishStagePublicizing, publishStagePublished} {
		if _, err := env.store.update(job.ID, func(j *publishJob) { j.Stage = stage }); err != nil {
			t.Fatal(err)
		}
	}
	cb.drain(context.Background())
	hits := receiver.received()
	if len(hits) != 1 {
		t.Fatalf("没发出去的几版应合成最新一次，实际 %d 次", len(hits))
	}
	push := verifyStatusCallbackHit(t, hits[0])
	if push.Data.Seq != 3 || pushedJob(t, push).Stage != publishStagePublished {
		t.Fatalf("应推最新一版：%s", hits[0].body)
	}
}

// 推不出去：按 5 秒、30 秒、2 分、10 分、30 分、1 小时、2 小时退避，共 8 次不成就放弃这一版、
// monitoring 给 warning；状态再变重新开始推。同一版每次重试请求体一字不差。
func TestStatusCallbackBacksOffThenGivesUp(t *testing.T) {
	receiver := newStatusCallbackReceiver(t)
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", testPublishSyncServiceToken)
	env := newPublishTestEnv(t)
	cb, clock := newTestStatusCallbacks(env.store)
	ctx := context.Background()
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.registerStatusCallback(job.ID, receiver.url); err != nil {
		t.Fatal(err)
	}
	receiver.reply(500, 500, 500, 500, 500, 500, 500, 500)
	start := clock.get()
	wantGaps := []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour}
	for i := 0; i < 8; i++ {
		next := cb.drain(ctx)
		if i < 7 {
			if gap := next.Sub(clock.get()); gap != wantGaps[i] {
				t.Fatalf("第 %d 次失败后应隔 %v，实际 %v", i+1, wantGaps[i], gap)
			}
			clock.set(next)
		} else if !next.IsZero() {
			t.Fatalf("第 8 次失败后应放弃，还排了 %v", next)
		}
	}
	hits := receiver.received()
	if len(hits) != 8 {
		t.Fatalf("应共试 8 次，实际 %d", len(hits))
	}
	for _, hit := range hits {
		verifyStatusCallbackHit(t, hit)
		if string(hit.body) != string(hits[0].body) || hit.header.Get("webhook-id") != hits[0].header.Get("webhook-id") {
			t.Fatalf("同一版重试的请求体与 webhook-id 应不变")
		}
	}
	if clock.get().Sub(start) != 3*time.Hour+42*time.Minute+35*time.Second {
		t.Fatalf("8 次应跨 3 小时 42 分 35 秒，实际 %v", clock.get().Sub(start))
	}
	stored, _ := env.store.byID(job.ID)
	if stored.StatusCallback.State != publishStatusCallbackDeadLetter || !strings.Contains(stored.StatusCallback.Error, "HTTP 500") {
		t.Fatalf("应记放弃：%+v", stored.StatusCallback)
	}
	monitoring := currentPublishMonitoring(env.store, false, clock.get())
	if !strings.Contains(strings.Join(monitoring.Warnings, ","), "status_callback_dead_letters:1") ||
		strings.Contains(strings.Join(monitoring.Issues, ","), "status_callback") {
		t.Fatalf("放弃的推送只给 warning：warnings=%v issues=%v", monitoring.Warnings, monitoring.Issues)
	}

	// 状态再变：新的一版从头推，送达后 warning 消失。
	if _, err := env.store.update(job.ID, func(j *publishJob) { j.Stage = publishStageRejected }); err != nil {
		t.Fatal(err)
	}
	cb.drain(ctx)
	hits = receiver.received()
	if len(hits) != 9 || verifyStatusCallbackHit(t, hits[8]).Data.Seq != 2 {
		t.Fatalf("状态变了应重新推：%d 次", len(hits))
	}
	stored, _ = env.store.byID(job.ID)
	if stored.StatusCallback.State != "" || stored.StatusCallback.DeliveredSeq != 2 {
		t.Fatalf("新的一版送达后应清掉放弃标记：%+v", stored.StatusCallback)
	}
	monitoring = currentPublishMonitoring(env.store, false, clock.get())
	if strings.Contains(strings.Join(monitoring.Warnings, ","), "status_callback") {
		t.Fatalf("送达后不应再有 warning：%v", monitoring.Warnings)
	}
}

// 对方回 Retry-After：比退避长就照它等。
func TestStatusCallbackHonorsRetryAfter(t *testing.T) {
	receiver := newStatusCallbackReceiver(t)
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", testPublishSyncServiceToken)
	env := newPublishTestEnv(t)
	cb, clock := newTestStatusCallbacks(env.store)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	receiver.reply(http.StatusServiceUnavailable)
	receiver.header.Set("Retry-After", "120")
	if _, err := env.store.registerStatusCallback(job.ID, receiver.url); err != nil {
		t.Fatal(err)
	}
	if gap := cb.drain(context.Background()).Sub(clock.get()); gap != 2*time.Minute {
		t.Fatalf("应照 Retry-After 等 2 分钟，实际 %v", gap)
	}
}

// 对方回 410：这个地址不再推；重新登记才恢复。
func TestStatusCallbackStopsOnGone(t *testing.T) {
	receiver := newStatusCallbackReceiver(t)
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	cb, _ := newTestStatusCallbacks(env.store)
	ctx := context.Background()
	receiver.reply(http.StatusGone)
	rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync", siteSyncBody(receiver.url))
	if rec.Code != http.StatusCreated {
		t.Fatalf("site-sync %d %s", rec.Code, rec.Body.String())
	}
	cb.drain(ctx)
	jobs, _ := env.store.all()
	id := jobs[0].ID
	if jobs[0].StatusCallback.State != publishStatusCallbackGone {
		t.Fatalf("410 后应记 gone：%+v", jobs[0].StatusCallback)
	}
	if _, err := env.store.update(id, func(j *publishJob) { j.Stage = publishStageRejected }); err != nil {
		t.Fatal(err)
	}
	cb.drain(ctx)
	if n := len(receiver.received()); n != 1 {
		t.Fatalf("410 后不应再推，实际共 %d 次", n)
	}
	if rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/site-sync", siteSyncBody(receiver.url)); rec.Code != http.StatusOK {
		t.Fatalf("重新登记 %d %s", rec.Code, rec.Body.String())
	}
	cb.drain(ctx)
	hits := receiver.received()
	if len(hits) != 2 || pushedJob(t, verifyStatusCallbackHit(t, hits[1])).Stage != publishStageRejected {
		t.Fatalf("重新登记后应推一次当前状态：%d 次", len(hits))
	}
}

// 进程重启：没送达的那一版按盘上记录补推，送达过的不重推。
func TestStatusCallbackResumesAfterRestart(t *testing.T) {
	receiver := newStatusCallbackReceiver(t)
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", testPublishSyncServiceToken)
	env := newPublishTestEnv(t)
	old, _ := newTestStatusCallbacks(env.store) // 旧进程：试了一次没推出去，就退出了
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.registerStatusCallback(job.ID, receiver.url); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.update(job.ID, func(j *publishJob) { j.Stage = publishStageRejected }); err != nil {
		t.Fatal(err)
	}
	receiver.reply(http.StatusBadGateway)
	old.drain(context.Background())
	if n := len(receiver.received()); n != 1 {
		t.Fatalf("旧进程应试过一次，实际 %d", n)
	}

	restart := func() *publishStatusCallbacks {
		store := newPublishJobStore(env.store.path)
		cb, _ := newTestStatusCallbacks(store)
		jobs, err := store.all()
		if err != nil {
			t.Fatal(err)
		}
		for _, j := range jobs {
			cb.queued(j)
		}
		return cb
	}
	restart().drain(context.Background())
	hits := receiver.received()
	if len(hits) != 2 {
		t.Fatalf("重启后应补推没送达的那一版，实际共 %d 次", len(hits))
	}
	if push := verifyStatusCallbackHit(t, hits[1]); push.Data.Seq != 2 || pushedJob(t, push).Stage != publishStageRejected {
		t.Fatalf("应补推最新一版：%s", hits[1].body)
	}
	if string(hits[1].body) != string(hits[0].body) || hits[1].header.Get("webhook-id") != hits[0].header.Get("webhook-id") {
		t.Fatalf("重启前后同一版的请求体与 webhook-id 应一字不差：\n%s\n%s", hits[0].body, hits[1].body)
	}
	restart().drain(context.Background())
	if n := len(receiver.received()); n != 2 {
		t.Fatalf("送达过的不应重推，实际共 %d 次", n)
	}
}

// official-sync 与 site-sync 字段一致：也认 status_callback_url。
func TestStatusCallbackOfficialSyncAcceptsField(t *testing.T) {
	receiver := newStatusCallbackReceiver(t)
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	cb, _ := newTestStatusCallbacks(env.store)
	rec := doPublishServiceReq(mux, http.MethodPost, "/api/publish/official-sync", siteSyncBody(receiver.url))
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"status_callback_url"`) {
		t.Fatalf("official-sync %d %s", rec.Code, rec.Body.String())
	}
	cb.drain(context.Background())
	if n := len(receiver.received()); n != 1 {
		t.Fatalf("official-sync 登记后也应推当前状态，实际 %d 次", n)
	}
}

// 生产里的发送 goroutine：任务库写盘就叫醒它，不用定时去扫。
func TestStatusCallbackSenderWakesOnStoreWrite(t *testing.T) {
	receiver := newStatusCallbackReceiver(t)
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", testPublishSyncServiceToken)
	env := newPublishTestEnv(t)
	cb := newPublishStatusCallbacks(env.store)
	cb.startSending()
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.registerStatusCallback(job.ID, receiver.url); err != nil {
		t.Fatal(err)
	}
	waitHits := func(n int) []statusCallbackHit {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if hits := receiver.received(); len(hits) >= n {
				return hits
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("5 秒内应收到 %d 次推送，实际 %d", n, len(receiver.received()))
		return nil
	}
	waitHits(1)
	if _, err := env.store.update(job.ID, func(j *publishJob) { j.Stage = publishStageRejected }); err != nil {
		t.Fatal(err)
	}
	hits := waitHits(2)
	if pushedJob(t, verifyStatusCallbackHit(t, hits[len(hits)-1])).Stage != publishStageRejected {
		t.Fatalf("最后一次应是驳回：%s", hits[len(hits)-1].body)
	}
}
