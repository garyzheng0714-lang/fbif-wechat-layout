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
	if err := b.syncOnce(context.Background()); err == nil {
		t.Fatal("中台 503 要报错")
	}
	if err := b.Check("on_a"); err == nil {
		t.Fatal("拉失败了也不能按空名单放")
	}

	reply.Store(blocklistReply(5, "on_gone"))
	if err := b.syncOnce(context.Background()); err != nil {
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
		_ = b.syncOnce(context.Background())
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
