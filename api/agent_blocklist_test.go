package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 假中台：回 reply（nil 就 503），记下收到的钥匙、路径、请求体。
func fakeBlocklistHub(t *testing.T, reply *atomic.Value) (*httptest.Server, *atomic.Value) {
	t.Helper()
	seen := &atomic.Value{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen.Store(r.Method + " " + r.URL.Path + " " + r.Header.Get("Authorization") + " " + string(body))
		v, _ := reply.Load().(string)
		if v == "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, v)
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

func newTestBlocklist(t *testing.T, hubURL string) *agentBlocklist {
	t.Helper()
	t.Setenv("SSO_HUB_URL", hubURL+"/hub-auth/layout")
	t.Setenv("SSO_HUB_KEY", "pqk_test_key_0123456789abcdef")
	t.Setenv("SSO_RECONCILE_SECONDS", "")
	return newAgentBlocklistFromEnv(filepath.Join(t.TempDir(), "agent-blocklist.json"))
}

func blocklistReply(revision int, entries ...string) string {
	list := []map[string]string{}
	for _, sub := range entries {
		list = append(list, map[string]string{"sub": sub, "reason": "离职", "since": "2026-11-20"})
	}
	raw, _ := json.Marshal(map[string]any{"revision": revision, "complete": true, "synced_at": 1_795_000_000_000, "entries": list})
	return string(raw)
}

// 问中台同一个接口（POST <SSO_HUB_URL>/sso/blocklist，Bearer SSO_HUB_KEY，带 {}）；拉到前谁都不放，拉到后名单上的人不放；
// 落盘、重启照用；中台回旧版本、没读全、格式不对都不收，手上的照用。
func TestAgentBlocklistPullsFromHubAndPersists(t *testing.T) {
	reply := &atomic.Value{}
	srv, seen := fakeBlocklistHub(t, reply)
	b := newTestBlocklist(t, srv.URL)
	if err := b.Check("on_a"); err == nil {
		t.Fatal("从没拉到过不能放")
	}
	if err := b.syncOnce(context.Background(), []byte("{}")); err == nil {
		t.Fatal("中台 503 要报错")
	}
	if err := b.Check("on_a"); err == nil {
		t.Fatal("拉失败了也不能按空名单放")
	}

	reply.Store(blocklistReply(5, "on_gone"))
	if err := b.syncOnce(context.Background(), []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if got := seen.Load().(string); got != "POST /hub-auth/layout/sso/blocklist Bearer pqk_test_key_0123456789abcdef {}" {
		t.Fatalf("问中台的方式变了：%q", got)
	}
	if err := b.Check("on_a"); err != nil {
		t.Fatalf("不在名单上的要放：%v", err)
	}
	if err := b.Check("on_gone"); err == nil || !strings.Contains(err.Error(), "停用") {
		t.Fatalf("名单上的不放：%v", err)
	}

	for _, bad := range []string{
		blocklistReply(4),
		`{"revision":6,"complete":false,"synced_at":1,"entries":[]}`,
		`{"revision":6,"complete":true,"synced_at":1,"entries":[{"sub":"on_x"}]}`,
		`not json`,
	} {
		reply.Store(bad)
		_ = b.syncOnce(context.Background(), []byte("{}"))
		if err := b.Check("on_gone"); err == nil {
			t.Fatalf("坏回包 %s 不该换掉名单", bad)
		}
	}

	// 重启：读回落盘的名单。
	again := newAgentBlocklistFromEnv(b.path)
	if err := again.Check("on_gone"); err == nil {
		t.Fatal("重启后要照用落盘的名单")
	}
	if err := again.Check("on_a"); err != nil {
		t.Fatalf("重启后不在名单上的照放：%v", err)
	}
	if raw, _ := os.ReadFile(b.path); !strings.Contains(string(raw), `"revision":5`) {
		t.Fatalf("落盘的应是版本 5：%s", raw)
	}
}

// 后台：启动拉一次；没拉到退避重试，直到成功。中台没配就不拉、谁都不放。
func TestAgentBlocklistStartRetriesUntilSynced(t *testing.T) {
	reply := &atomic.Value{}
	srv, _ := fakeBlocklistHub(t, reply)
	b := newTestBlocklist(t, srv.URL)
	b.retry = 60 * time.Millisecond
	b.gap = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.Start(ctx)
	time.Sleep(30 * time.Millisecond)
	reply.Store(blocklistReply(1))
	deadline := time.Now().Add(3 * time.Second)
	for b.Check("on_a") != nil {
		if time.Now().After(deadline) {
			t.Fatal("中台恢复后要拉到名单")
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Setenv("SSO_HUB_URL", "")
	unset := newAgentBlocklistFromEnv(filepath.Join(t.TempDir(), "x.json"))
	unset.Start(ctx)
	if err := unset.Check("on_a"); err == nil {
		t.Fatal("中台没配，谁都不放")
	}
}

func TestBlocklistIntervalBounds(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"": 6 * time.Hour, "abc": 6 * time.Hour, "60": 10 * time.Minute, "3600": time.Hour, "999999": 24 * time.Hour,
	} {
		if got, _ := blocklistInterval(raw); got != want {
			t.Fatalf("%q → %v，要 %v", raw, got, want)
		}
	}
}

// 名单提醒（POST /internal/auth/blocklist-changed）：比手上新就马上拉、带上这个版本问中台；旧的、一样的不拉；
// 连发多次只拉一次。不过登录、不认冒充的 X-FBIF-User，一律 202。
func TestAgentBlocklistReminderPullsAtOnce(t *testing.T) {
	var pulls atomic.Int32
	var lastBody atomic.Value
	var revision atomic.Int32
	revision.Store(1)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lastBody.Store(string(body))
		pulls.Add(1)
		time.Sleep(50 * time.Millisecond) // 拉的这一会儿提醒还在来
		_, _ = io.WriteString(w, blocklistReply(int(revision.Load()), "on_gone"))
	}))
	t.Cleanup(hub.Close)
	b := newTestBlocklist(t, hub.URL)
	b.gap = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.Start(ctx)

	svc := &authService{blocklist: b}
	mux := http.NewServeMux()
	svc.Register(mux)
	handler := svc.Wrap(mux)
	remind := func(body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/internal/auth/blocklist-changed", strings.NewReader(body))
		req.Header.Set("X-FBIF-User", "FORGED")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
			t.Fatalf("提醒要回 202、不带细节：%d %q", rec.Code, rec.Body.String())
		}
	}
	waitPulls := func(want int32, within time.Duration) {
		t.Helper()
		deadline := time.Now().Add(within)
		for pulls.Load() < want {
			if time.Now().After(deadline) {
				t.Fatalf("%s 内该拉到第 %d 次，只拉了 %d 次", within, want, pulls.Load())
			}
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond) // 再等过两个间隔，看有没有多拉
		if got := pulls.Load(); got != want {
			t.Fatalf("该拉 %d 次，拉了 %d 次", want, got)
		}
	}
	waitPulls(1, time.Second) // 启动那一轮
	if got := lastBody.Load(); got != "{}" {
		t.Fatalf("定时那轮带 {}：%v", got)
	}

	// 旧的、一样的版本，读不懂的提醒：手上有名单，不拉。
	for _, body := range []string{`{"revision":1}`, `{"revision":0.5}`, `{"revision":"9"}`, `not json`, strings.Repeat("x", 5000)} {
		remind(body)
	}
	waitPulls(1, time.Second)

	// 比手上新：马上拉（不等 6 小时），带上这个版本；连发 20 次只拉一次。
	revision.Store(2)
	for range 20 {
		remind(`{"revision":2}`)
	}
	waitPulls(2, time.Second)
	if got := lastBody.Load(); got != `{"revision":2}` {
		t.Fatalf("提醒那轮要带上版本：%v", got)
	}
	if err := b.Check("on_gone"); err == nil {
		t.Fatal("拉到的名单要生效")
	}

	// 0 是要一份现拉的，照拉。
	remind(`{"revision":0}`)
	waitPulls(3, time.Second)
	if got := lastBody.Load(); got != `{"revision":0}` {
		t.Fatalf("提醒带 0 就问中台要现拉的：%v", got)
	}
}
