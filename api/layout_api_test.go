package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- 测试用的假东西 ----

type layoutTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func newLayoutTestClock() *layoutTestClock {
	return &layoutTestClock{now: time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)}
}

func (c *layoutTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *layoutTestClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeLayoutCallerSource 假配置包：frozen 为零时名单一直是新的（长等待请求挂着），否则新到 frozen。
type fakeLayoutCallerSource struct {
	mu         sync.Mutex
	clock      *layoutTestClock
	rows       []layoutCallerRow
	present    bool
	generation uint64
	frozen     time.Time
}

func (s *fakeLayoutCallerSource) LayoutCallers(now time.Time) layoutCallerSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	checkedAt := now
	if !s.frozen.IsZero() {
		checkedAt = s.frozen
	}
	return layoutCallerSnapshot{Rows: append([]layoutCallerRow(nil), s.rows...), Present: s.present, Generation: s.generation, CheckedAt: checkedAt}
}

// set 中台送来新的一版。
func (s *fakeLayoutCallerSource) set(present bool, rows []layoutCallerRow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.present, s.rows = present, rows
	s.generation++
}

type fakeLayoutNode struct {
	mu       sync.Mutex
	requests []layoutNodeRequest
	block    chan struct{} // 不是 nil 就等它关上（或 ctx 到点）再回
	started  chan struct{}
	html     func(req layoutNodeRequest) string
	err      *layoutError
	warnings string
	check    articleCheckSource // 排版程序给的校对原文
}

func (n *fakeLayoutNode) Layout(ctx context.Context, req layoutNodeRequest) (layoutNodeResult, *layoutError) {
	n.mu.Lock()
	n.requests = append(n.requests, req)
	block, started, makeHTML, failure, warnings, check := n.block, n.started, n.html, n.err, n.warnings, n.check
	n.mu.Unlock()
	if started != nil {
		started <- struct{}{}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return layoutNodeResult{}, layoutContextError(ctx, nil)
		}
	}
	if failure != nil {
		return layoutNodeResult{}, failure
	}
	html := `<section><p>排好的正文</p></section>`
	if makeHTML != nil {
		html = makeHTML(req)
	}
	repost := req.Repost == nil || *req.Repost
	if warnings == "" {
		warnings = `[]`
	}
	return layoutNodeResult{
		HTML: html, Title: "排版程序给的标题", Summary: "", Repost: repost, Footer: true,
		Stats: layoutNodeStats{Images: 0, Headings: 2},
		Rule:  layoutRuleJSON{PresetID: req.Account + "-preset", Revision: 7, ConfigHash: "cfg"},
		Build: "build-sha", Warnings: json.RawMessage(warnings), Check: check,
	}, nil
}

func (n *fakeLayoutNode) lastRequest(t *testing.T) layoutNodeRequest {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.requests) == 0 {
		t.Fatal("排版程序没收到请求")
	}
	return n.requests[len(n.requests)-1]
}

func (n *fakeLayoutNode) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.requests)
}

type fakeLayoutFetcher struct {
	mu          sync.Mutex
	article     layoutFetchedArticle
	err         error
	readerTitle string
	readerText  string
	readerErr   error
	calls       int
	readerCalls int
}

func (f *fakeLayoutFetcher) FetchArticle(_ context.Context, _ string, _ articleBlockOptions) (layoutFetchedArticle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.article, f.err
}

func (f *fakeLayoutFetcher) FetchReader(_ context.Context, _ string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readerCalls++
	return f.readerTitle, f.readerText, f.readerErr
}

type fakeLayoutImageMirror struct {
	mu      sync.Mutex
	calls   [][]string
	results func(urls []string) map[string]string
	err     error
}

func (m *fakeLayoutImageMirror) ByURLs(_ context.Context, urls []string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, append([]string(nil), urls...))
	if m.err != nil {
		return nil, m.err
	}
	if m.results != nil {
		return m.results(urls), nil
	}
	out := map[string]string{}
	for i, src := range urls {
		out[src] = fmt.Sprintf("https://%s/layout/%d.png", layoutTestOSSHost, i)
	}
	return out, nil
}

func (m *fakeLayoutImageMirror) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// ---- 测试环境 ----

type layoutTestKeys struct {
	active, other, disabled string
}

type layoutTestEnv struct {
	t       *testing.T
	clock   *layoutTestClock
	source  *fakeLayoutCallerSource
	callers *layoutCallerDirectory
	node    *fakeLayoutNode
	fetcher *fakeLayoutFetcher
	images  *fakeLayoutImageMirror
	paused  atomic.Bool
	api     *layoutAPI
	mux     *http.ServeMux
	keys    layoutTestKeys
}

func mustLayoutKey(t *testing.T) (string, string) {
	t.Helper()
	key, fingerprint, err := newLayoutKey()
	if err != nil {
		t.Fatal(err)
	}
	return key, fingerprint
}

func newLayoutTestEnv(t *testing.T) *layoutTestEnv {
	t.Helper()
	t.Setenv("LAYOUT_IMAGE_HOSTS", "")
	env := &layoutTestEnv{t: t, clock: newLayoutTestClock(), node: &fakeLayoutNode{}, fetcher: &fakeLayoutFetcher{}, images: &fakeLayoutImageMirror{}}
	activeKey, activeFP := mustLayoutKey(t)
	otherKey, otherFP := mustLayoutKey(t)
	disabledKey, disabledFP := mustLayoutKey(t)
	env.keys = layoutTestKeys{active: activeKey, other: otherKey, disabled: disabledKey}
	env.source = &fakeLayoutCallerSource{clock: env.clock}
	env.source.set(true, []layoutCallerRow{
		{Code: "c1", Name: "资讯订阅", Owner: "张三", Status: layoutCallerActive, KeySHA256: activeFP},
		{Code: "c2", Name: "小助手", Owner: "李四", Status: layoutCallerActive, KeySHA256: strings.ToUpper(otherFP)},
		{Code: "c3", Name: "旧系统", Owner: "王五", Status: "停用", KeySHA256: disabledFP},
	})
	env.callers = newLayoutCallerDirectory(env.source, env.clock.Now)
	env.api = newLayoutAPI(layoutAPIDeps{
		callers: env.callers,
		fetcher: env.fetcher,
		node:    env.node,
		images:  env.images,
		paused:  env.paused.Load,
		now:     env.clock.Now,
	})
	env.mux = http.NewServeMux()
	registerLayoutAPIRoutes(env.mux, env.api)
	return env
}

func (env *layoutTestEnv) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, req)
	return rec
}

func (env *layoutTestEnv) postJSON(key string, body any) *httptest.ResponseRecorder {
	raw, ok := body.(string)
	if !ok {
		encoded, err := json.Marshal(body)
		if err != nil {
			env.t.Fatal(err)
		}
		raw = string(encoded)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/layout/v1/render", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return env.do(req)
}

func (env *layoutTestEnv) poll(key, pollURL string, wait int) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("%s?wait_seconds=%d", pollURL, wait), nil)
	req.Header.Set("Authorization", "Bearer "+key)
	return env.do(req)
}

func decodeLayoutErrorBody(t *testing.T, rec *httptest.ResponseRecorder) layoutErrorJSON {
	t.Helper()
	var out layoutErrorJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Error.Code == "" {
		t.Fatalf("不是错误回答（HTTP %d）：%s", rec.Code, rec.Body.String())
	}
	return out
}

func expectLayoutError(t *testing.T, rec *httptest.ResponseRecorder, status int, code layoutErrorCode) layoutErrorJSON {
	t.Helper()
	body := decodeLayoutErrorBody(t, rec)
	if rec.Code != status || body.Error.Code != string(code) {
		t.Fatalf("要 %d %s，拿到 %d %s：%s", status, code, rec.Code, body.Error.Code, rec.Body.String())
	}
	if body.RequestID == "" || rec.Header().Get("X-Request-ID") != body.RequestID {
		t.Fatalf("错误要带 request_id，且和请求头一样：%q %q", body.RequestID, rec.Header().Get("X-Request-ID"))
	}
	if body.Error.Fix == "" || body.Error.Message == "" {
		t.Fatalf("错误要写 message 和 fix：%+v", body.Error)
	}
	return body
}

func decodeLayoutResultBody(t *testing.T, rec *httptest.ResponseRecorder) layoutResultJSON {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("要 200，拿到 %d：%s", rec.Code, rec.Body.String())
	}
	var out layoutResultJSON
	decoder := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		t.Fatalf("200 的字段对不上：%v\n%s", err, rec.Body.String())
	}
	return out
}

func decodeLayoutPendingBody(t *testing.T, rec *httptest.ResponseRecorder) layoutPendingJSON {
	t.Helper()
	if rec.Code != http.StatusAccepted {
		t.Fatalf("要 202，拿到 %d：%s", rec.Code, rec.Body.String())
	}
	var out layoutPendingJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Operation.PollURL == "" {
		t.Fatalf("202 要带 poll_url：%v %s", err, rec.Body.String())
	}
	if rec.Header().Get("Location") != out.Operation.PollURL || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("202 要带 Location 和 Retry-After：%v", rec.Header())
	}
	return out
}

// waitLayoutIdle 等后台排完（并发位都还回来）。
func waitLayoutIdle(t *testing.T, api *layoutAPI, caller string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for api.limits.runningFor(caller) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%s 的稿子一直没排完", caller)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ---- 钥匙 ----

func TestLayoutAuthRejectsMissingWrongAndDisabledKeys(t *testing.T) {
	env := newLayoutTestEnv(t)
	body := map[string]any{"account": "fbif", "text": "一段话"}
	expectLayoutError(t, env.postJSON("", body), http.StatusUnauthorized, layoutErrUnauthorized)
	expectLayoutError(t, env.postJSON("flk_short", body), http.StatusUnauthorized, layoutErrUnauthorized)
	stranger, _ := mustLayoutKey(t)
	expectLayoutError(t, env.postJSON(stranger, body), http.StatusUnauthorized, layoutErrUnauthorized)
	expectLayoutError(t, env.postJSON(env.keys.disabled, body), http.StatusForbidden, layoutErrKeyDisabled)
	// 指纹大写也认（表里是人手填的）。
	decodeLayoutResultBody(t, env.postJSON(env.keys.other, body))
	if env.node.count() != 1 {
		t.Fatalf("没过钥匙的请求不许排：%d", env.node.count())
	}
}

func TestLayoutAuthStaleDirectoryFailsClosed(t *testing.T) {
	env := newLayoutTestEnv(t)
	body := map[string]any{"account": "fbif", "text": "一段话"}
	decodeLayoutResultBody(t, env.postJSON(env.keys.active, body))
	waitLayoutIdle(t, env.api, "c1")

	// 中台连不上：名单停在断开那一刻，10 分钟内照常放行。
	env.source.mu.Lock()
	env.source.frozen = env.clock.Now()
	env.source.mu.Unlock()
	env.clock.Add(9 * time.Minute)
	decodeLayoutResultBody(t, env.postJSON(env.keys.active, body))
	waitLayoutIdle(t, env.api, "c1")

	// 超过 10 分钟没核对成功：一律 503，停用要及时生效。
	env.clock.Add(2 * time.Minute)
	rec := env.postJSON(env.keys.active, body)
	expectLayoutError(t, rec, http.StatusServiceUnavailable, layoutErrAuthUnavailable)
	if rec.Header().Get("Retry-After") != "30" {
		t.Fatalf("auth_unavailable 要带 Retry-After：%v", rec.Header())
	}

	// 中台恢复后马上放行。
	env.source.mu.Lock()
	env.source.frozen = time.Time{}
	env.source.mu.Unlock()
	decodeLayoutResultBody(t, env.postJSON(env.keys.active, body))
}

func TestLayoutAuthUnavailableWithoutCallerEntry(t *testing.T) {
	clock := newLayoutTestClock()
	key, fingerprint := mustLayoutKey(t)
	source := &fakeLayoutCallerSource{clock: clock}
	directory := newLayoutCallerDirectory(source, clock.Now)
	if _, lerr := directory.authenticate(key); lerr == nil || lerr.Code != layoutErrAuthUnavailable {
		t.Fatalf("开机还没拿到配置包要 auth_unavailable：%v", lerr)
	}
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	source.set(false, nil)
	if _, lerr := directory.authenticate(key); lerr == nil || lerr.Code != layoutErrAuthUnavailable {
		t.Fatalf("没条目一律 auth_unavailable：%v", lerr)
	}
	if !strings.Contains(logs.String(), "api_callers") {
		t.Fatalf("没条目要打日志：%q", logs.String())
	}
	// 中台加了表以后就能用。
	source.set(true, []layoutCallerRow{{Code: "c1", Status: layoutCallerActive, KeySHA256: fingerprint}})
	if caller, lerr := directory.authenticate(key); lerr != nil || caller.Code != "c1" {
		t.Fatalf("加表后认得：%+v %v", caller, lerr)
	}
}

func TestLayoutCallerRowsRejectDuplicates(t *testing.T) {
	key, fingerprint := mustLayoutKey(t)
	entries, problems := parseLayoutCallerRows([]layoutCallerRow{
		{Code: "a", Status: layoutCallerActive, KeySHA256: fingerprint},
		{Code: "b", Status: layoutCallerActive, KeySHA256: fingerprint},
		{Code: "a", Status: layoutCallerActive, KeySHA256: strings.Repeat("0", 64)},
		{Code: "", Status: layoutCallerActive, KeySHA256: strings.Repeat("1", 64)},
		{Code: "d", Status: layoutCallerActive, KeySHA256: "不是指纹"},
	})
	if len(problems) != 4 || len(entries) != 1 {
		t.Fatalf("重复、缺编号、指纹不对都要跳过：%v %+v", problems, entries)
	}
	clock := newLayoutTestClock()
	source := &fakeLayoutCallerSource{clock: clock}
	source.set(true, []layoutCallerRow{
		{Code: "a", Status: layoutCallerActive, KeySHA256: fingerprint},
		{Code: "b", Status: layoutCallerActive, KeySHA256: fingerprint},
	})
	directory := newLayoutCallerDirectory(source, clock.Now)
	if _, lerr := directory.authenticate(key); lerr == nil || lerr.Code != layoutErrKeyDisabled {
		t.Fatalf("两行同一把钥匙分不清是谁，都不认：%v", lerr)
	}
}

func TestLayoutKeysAndRenderTokenOpenOnlyTheirOwnRoutes(t *testing.T) {
	env := newLayoutTestEnv(t)
	const renderTok = "render-token-for-test-1234567890"
	t.Setenv("RENDER_TOKEN", renderTok)
	registerRenderRoutes(env.mux)

	expectLayoutError(t, env.postJSON(renderTok, map[string]any{"account": "fbif", "text": "x"}),
		http.StatusUnauthorized, layoutErrUnauthorized)

	req := httptest.NewRequest(http.MethodPost, "/api/html-to-blocks", strings.NewReader(`{"html":"<p>x</p>"}`))
	req.Header.Set("Authorization", "Bearer "+env.keys.active)
	if rec := env.do(req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("新钥匙不开旧接口：%d %s", rec.Code, rec.Body.String())
	}
}

func TestLayoutKeyFingerprintAndIssuing(t *testing.T) {
	key, fingerprint := mustLayoutKey(t)
	if !strings.HasPrefix(key, layoutKeyPrefix) || !layoutKeyShapeOK(key) || len(fingerprint) != 64 {
		t.Fatalf("钥匙长相：%q %q", key, fingerprint)
	}
	if layoutKeyFingerprint(key) != fingerprint {
		t.Fatal("指纹是整串钥匙的 sha256")
	}
	text, err := runNewLayoutKey()
	if err != nil || !strings.Contains(text, layoutKeyPrefix) || !strings.Contains(text, "key_sha256") {
		t.Fatalf("发钥匙要打印钥匙和指纹：%v %q", err, text)
	}
}

// ---- 限额 ----

func TestLayoutConcurrencySlotHeldUntilOperationEnds(t *testing.T) {
	env := newLayoutTestEnv(t)
	release := make(chan struct{})
	env.node.block = release
	body := map[string]any{"account": "fbif", "text": "一段话", "wait_seconds": 0}
	var pending []layoutPendingJSON
	for i := 0; i < layoutPerKeyConcurrent; i++ {
		pending = append(pending, decodeLayoutPendingBody(t, env.postJSON(env.keys.active, body)))
	}
	if got := env.api.limits.runningFor("c1"); got != layoutPerKeyConcurrent {
		t.Fatalf("回了 202 还在排的也占位：%d", got)
	}
	rec := env.postJSON(env.keys.active, body)
	expectLayoutError(t, rec, http.StatusServiceUnavailable, layoutErrBusy)
	// 别家的钥匙不受影响。
	decodeLayoutPendingBody(t, env.postJSON(env.keys.other, body))

	close(release)
	waitLayoutIdle(t, env.api, "c1")
	waitLayoutIdle(t, env.api, "c2")
	for _, p := range pending {
		decodeLayoutResultBody(t, env.poll(env.keys.active, p.Operation.PollURL, 5))
	}
	decodeLayoutResultBody(t, env.postJSON(env.keys.active, map[string]any{"account": "fbif", "text": "又一段"}))
}

func TestLayoutRateLimitPerKeyPerMinute(t *testing.T) {
	env := newLayoutTestEnv(t)
	body := map[string]any{"account": "fbif", "text": "一段话"}
	for i := 0; i < layoutPerKeyPerMinute; i++ {
		decodeLayoutResultBody(t, env.postJSON(env.keys.active, body))
		waitLayoutIdle(t, env.api, "c1")
	}
	rec := env.postJSON(env.keys.active, body)
	errBody := expectLayoutError(t, rec, http.StatusTooManyRequests, layoutErrRateLimited)
	if errBody.Error.RetryAfterSeconds < 1 || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("429 要说还要等几秒：%+v %v", errBody.Error, rec.Header())
	}
	decodeLayoutResultBody(t, env.postJSON(env.keys.other, body))
	env.clock.Add(time.Minute)
	decodeLayoutResultBody(t, env.postJSON(env.keys.active, body))
}

func TestLayoutWechatFetchLimitIsGlobal(t *testing.T) {
	env := newLayoutTestEnv(t)
	env.fetcher.article = layoutFetchedArticle{Title: "标题", Blocks: []Block{{K: "txt", Text: "正文"}}}
	body := map[string]any{"account": "fbif", "url": "https://mp.weixin.qq.com/s/abcdef"}
	for i := 0; i < layoutWechatFetchPerMinute; i++ {
		key, caller := env.keys.active, "c1"
		if i%2 == 1 {
			key, caller = env.keys.other, "c2"
		}
		decodeLayoutResultBody(t, env.postJSON(key, body))
		waitLayoutIdle(t, env.api, caller)
	}
	expectLayoutError(t, env.postJSON(env.keys.active, body), http.StatusTooManyRequests, layoutErrRateLimited)
	// 不抓公众号的不受影响。
	decodeLayoutResultBody(t, env.postJSON(env.keys.active, map[string]any{"account": "fbif", "text": "一段话"}))
}

func TestLayoutPollRateLimit(t *testing.T) {
	limiter := newLayoutLimiter(newLayoutTestClock().Now)
	for i := 0; i < layoutPollPerKeyPerMinute; i++ {
		if lerr := limiter.allowPoll("c1"); lerr != nil {
			t.Fatal(lerr)
		}
	}
	if lerr := limiter.allowPoll("c1"); lerr == nil || lerr.Code != layoutErrRateLimited {
		t.Fatalf("轮询也计次：%v", lerr)
	}
}

// ---- 202、轮询、重启、过期 ----

func TestLayoutPendingThenPollReturnsStoredBodyByteForByte(t *testing.T) {
	env := newLayoutTestEnv(t)
	release := make(chan struct{})
	env.node.block = release
	pending := decodeLayoutPendingBody(t, env.postJSON(env.keys.active, map[string]any{"account": "fbif", "text": "一段话", "wait_seconds": 0}))
	if !strings.HasPrefix(pending.Operation.ID, layoutOperationPrefix) || pending.Operation.PollURL != layoutPollURL(pending.Operation.ID) {
		t.Fatalf("编号与 poll_url：%+v", pending.Operation)
	}
	still := decodeLayoutPendingBody(t, env.poll(env.keys.active, pending.Operation.PollURL, 0))
	if still.Operation.State != layoutStateRunning && still.Operation.State != layoutStateQueued {
		t.Fatalf("状态：%q", still.Operation.State)
	}
	// 别家钥匙取不到。
	expectLayoutError(t, env.poll(env.keys.other, pending.Operation.PollURL, 0), http.StatusNotFound, layoutErrOperationNotFound)

	close(release)
	first := env.poll(env.keys.active, pending.Operation.PollURL, 5)
	result := decodeLayoutResultBody(t, first)
	second := env.poll(env.keys.active, pending.Operation.PollURL, 0)
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatal("同一篇取两次要逐字节一样")
	}
	if result.RequestID != pending.RequestID {
		t.Fatalf("结果里的 request_id 是提交那次的：%q %q", result.RequestID, pending.RequestID)
	}
	if env.node.count() != 1 {
		t.Fatalf("取结果不许重排：%d", env.node.count())
	}
}

func TestLayoutDirectAnswerEqualsStoredAnswer(t *testing.T) {
	env := newLayoutTestEnv(t)
	rec := env.postJSON(env.keys.active, map[string]any{"account": "fbif", "text": "一段话"})
	decodeLayoutResultBody(t, rec)
	env.api.ops.mu.Lock()
	var stored [][]byte
	for _, op := range env.api.ops.ops {
		stored = append(stored, op.result().Body)
	}
	env.api.ops.mu.Unlock()
	if len(stored) != 1 || !bytes.Equal(stored[0], rec.Body.Bytes()) {
		t.Fatal("直接回的 200 就是存好的那份")
	}
}

func TestLayoutOperationLostAfterRestart(t *testing.T) {
	env := newLayoutTestEnv(t)
	env.node.block = make(chan struct{})
	pending := decodeLayoutPendingBody(t, env.postJSON(env.keys.active, map[string]any{"account": "fbif", "text": "x", "wait_seconds": 0}))
	restarted := newLayoutTestEnv(t)
	restarted.callers = env.callers
	restarted.api.callers = env.callers
	rec := restarted.poll(env.keys.active, pending.Operation.PollURL, 0)
	body := expectLayoutError(t, rec, http.StatusNotFound, layoutErrOperationLost)
	if !body.Error.Retryable || !strings.Contains(body.Error.Fix, "10 秒、30 秒、60 秒") {
		t.Fatalf("重启丢稿要说最多重提 3 次：%+v", body.Error)
	}
	expectLayoutError(t, restarted.poll(env.keys.active, "/api/layout/v1/operations/op_garbage", 0), http.StatusNotFound, layoutErrOperationNotFound)
	close(env.node.block)
}

func TestLayoutOperationExpiresAfterTTL(t *testing.T) {
	env := newLayoutTestEnv(t)
	release := make(chan struct{})
	env.node.block = release
	pending := decodeLayoutPendingBody(t, env.postJSON(env.keys.active, map[string]any{"account": "fbif", "text": "x", "wait_seconds": 0}))
	close(release)
	waitLayoutIdle(t, env.api, "c1")
	decodeLayoutResultBody(t, env.poll(env.keys.active, pending.Operation.PollURL, 0))
	env.clock.Add(layoutResultTTL)
	expectLayoutError(t, env.poll(env.keys.active, pending.Operation.PollURL, 0), http.StatusNotFound, layoutErrOperationExpired)
	// 别家钥匙拿过期编号也只说找不到。
	expectLayoutError(t, env.poll(env.keys.other, pending.Operation.PollURL, 0), http.StatusNotFound, layoutErrOperationNotFound)
	if env.api.ops.storedBytes() != 0 {
		t.Fatalf("过期的结果要清掉：%d", env.api.ops.storedBytes())
	}
}

func TestLayoutOperationStoreEvictsOldestOverBudget(t *testing.T) {
	clock := newLayoutTestClock()
	store := newLayoutOperationStore(clock.Now)
	store.maxBytes = 25
	var ops []*layoutOperation
	for i := 0; i < 3; i++ {
		op, err := store.create("c1", "lr_x")
		if err != nil {
			t.Fatal(err)
		}
		clock.Add(time.Second)
		store.finish(op, layoutStoredResponse{Status: http.StatusOK, Body: bytes.Repeat([]byte("a"), 10)})
		ops = append(ops, op)
	}
	if store.storedBytes() > 25 {
		t.Fatalf("合计不超过上限：%d", store.storedBytes())
	}
	if _, lerr := store.lookup("c1", ops[0].id); lerr == nil || lerr.Code != layoutErrOperationExpired {
		t.Fatalf("最早排好的先挤掉：%v", lerr)
	}
	for _, op := range ops[1:] {
		if got, lerr := store.lookup("c1", op.id); lerr != nil || got != op {
			t.Fatalf("新的留着：%v", lerr)
		}
	}
	if _, ok := parseLayoutOperationID("op_short"); ok {
		t.Fatal("编号长相不对")
	}
}

func TestLayoutPausedDeploymentRejectsNewWork(t *testing.T) {
	env := newLayoutTestEnv(t)
	env.paused.Store(true)
	rec := env.postJSON(env.keys.active, map[string]any{"account": "fbif", "text": "x"})
	expectLayoutError(t, rec, http.StatusServiceUnavailable, layoutErrServiceRestarting)
	if rec.Header().Get("Retry-After") != "30" || env.node.count() != 0 {
		t.Fatalf("暂停期不接新稿：%v %d", rec.Header(), env.node.count())
	}
}

func TestLayoutContextErrorSaysTimeoutAfterDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if lerr := layoutContextError(ctx, layoutErr(layoutErrFetchFailed)); lerr.Code != layoutErrTimeout {
		t.Fatalf("到了截止时间一律 timeout：%v", lerr)
	}
	if lerr := layoutContextError(context.Background(), layoutErr(layoutErrFetchFailed)); lerr.Code != layoutErrFetchFailed {
		t.Fatalf("没到截止时间原样返回：%v", lerr)
	}
}

// ---- 日志、路由、不建发布单 ----

func TestLayoutRequestLogNeverContainsArticleText(t *testing.T) {
	env := newLayoutTestEnv(t)
	var logs syncBuffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	rec := env.postJSON(env.keys.active, map[string]any{"account": "fbif", "text": "机密正文内容不许进日志"})
	result := decodeLayoutResultBody(t, rec)
	waitLayoutIdle(t, env.api, "c1")
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logs.String(), "status=200") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	text := logs.String()
	for _, want := range []string{"request_id=" + result.RequestID, "caller=c1", "account=fbif", "input=text", "status=200", "rule=fbif-preset@7"} {
		if !strings.Contains(text, want) {
			t.Fatalf("日志要有 %q：%s", want, text)
		}
	}
	if strings.Contains(text, "机密正文") {
		t.Fatalf("日志不记正文：%s", text)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestLayoutAPIRoutesRegistered 路由写成字面量（部署检查靠正则扫 mux.HandleFunc），并和路由表一一对上。
func TestLayoutAPIRoutesRegistered(t *testing.T) {
	source, err := os.ReadFile("layout_api.go")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`mux\.HandleFunc\("((?:GET|POST) /api/layout/v1/[^"]+)"`)
	var registered []string
	for _, m := range pattern.FindAllStringSubmatch(string(source), -1) {
		registered = append(registered, m[1])
	}
	var want []string
	for _, route := range layoutAPIRoutes {
		want = append(want, route.Method+" "+route.Path)
	}
	sort.Strings(registered)
	sort.Strings(want)
	if !reflect.DeepEqual(registered, want) {
		t.Fatalf("注册的路由 %v 和路由表 %v 对不上", registered, want)
	}

	env := newLayoutTestEnv(t)
	for _, route := range layoutAPIRoutes {
		path := strings.Replace(route.Path, "{id}", "op_x", 1)
		rec := env.do(httptest.NewRequest(route.Method, path, nil))
		switch {
		case route.Auth && rec.Code != http.StatusUnauthorized:
			t.Errorf("%s %s 不带钥匙要 401：%d", route.Method, path, rec.Code)
		case !route.Auth && rec.Code != http.StatusOK:
			t.Errorf("%s %s 不用钥匙：%d", route.Method, path, rec.Code)
		}
	}
	main, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(main), "layoutAPI := newLayoutAPIFromEnv(ruleStore, authService)") ||
		!strings.Contains(string(main), "registerLayoutAPIRoutes(mux, layoutAPI)") {
		t.Fatal("main.go 要注册排版接口")
	}
}

// TestLayoutNeverCreatesPublishJobs 排版接口这条路建不了发布单：结构里没有发布依赖，代码里也不碰发布函数（红线 33）。
func TestLayoutNeverCreatesPublishJobs(t *testing.T) {
	typ := reflect.TypeOf(layoutAPI{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Type.String())
		if strings.Contains(name, "publish") || strings.Contains(name, "foodtalks") {
			t.Fatalf("layoutAPI 不许带发布依赖：%s %s", typ.Field(i).Name, typ.Field(i).Type)
		}
	}
	files, err := filepathGlobLayoutSources()
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"publishPipeline", "publishJobStore", "newPublishJobStore", ".create(sourceKey", "createSeeded",
		"site-sync", "official-sync", "stagePublicize", "CreateNews", "cloudConvertDocToDocx", "CLOUDCONVERT"}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		code := stripGoLineComments(string(raw))
		for _, word := range forbidden {
			if strings.Contains(code, word) {
				t.Fatalf("%s 不许碰 %s", file, word)
			}
		}
	}
}

func filepathGlobLayoutSources() ([]string, error) {
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "layout_") && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			out = append(out, name)
		}
	}
	if len(out) < 9 {
		return nil, fmt.Errorf("layout_*.go 少了：%v", out)
	}
	return out, nil
}

func stripGoLineComments(source string) string {
	var b strings.Builder
	for _, line := range strings.Split(source, "\n") {
		if idx := strings.Index(line, "//"); idx >= 0 && !strings.Contains(line[:idx], "\"") {
			line = line[:idx]
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func TestLayoutErrorTableIsComplete(t *testing.T) {
	for _, spec := range layoutErrorSpecs {
		if spec.Message == "" || spec.Fix == "" || spec.Status < 400 || spec.Status > 599 {
			t.Errorf("%s 要写说明、改法和 4xx/5xx：%+v", spec.Code, spec)
		}
		if spec.RetryAfter > 0 && !spec.Retryable {
			t.Errorf("%s 有建议等待就该能重发", spec.Code)
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("不在表里的错误码要 panic")
		}
	}()
	newLayoutError("no_such_code", "")
}

func TestLayoutErrorResponseShape(t *testing.T) {
	resp := layoutErrorResponse("lr_1", layoutErr(layoutErrRateLimited).withRetryAfter(1500*time.Millisecond))
	var body layoutErrorJSON
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusTooManyRequests || body.Error.RetryAfterSeconds != 2 || resp.RetryAfter != 2 || !body.Error.Retryable {
		t.Fatalf("等待秒数向上取整：%+v %+v", resp, body)
	}
	plain := layoutErrorResponse("lr_1", layoutErr(layoutErrBadRequest))
	if strings.Contains(string(plain.Body), "retry_after_seconds") {
		t.Fatalf("没有建议就不出现 retry_after_seconds：%s", plain.Body)
	}
	if !strings.Contains(string(layoutMarshal(map[string]string{"h": "<p>&</p>"})), "<p>&</p>") {
		t.Fatal("html 不转义成 \\u003c")
	}
}
