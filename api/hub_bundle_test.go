package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// hubScript 假中台：每个 GET 按顺序领一个回答；回答没给之前请求就挂着（像长等待），连接断了就放手。POST（密钥回执）一律收下。
type hubScript struct {
	t       *testing.T
	answers chan func(http.ResponseWriter)
	seen    chan http.Header
}

func newHubScript(t *testing.T, key string) (*hubScript, *httptest.Server) {
	s := &hubScript{t: t, answers: make(chan func(http.ResponseWriter), 16), seen: make(chan http.Header, 16)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Errorf("要带钥匙：%q", r.Header.Get("Authorization"))
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.seen <- r.Header.Clone()
		select {
		case answer := <-s.answers:
			answer(w)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	return s, server
}

// hubDrop 回包写到一半就断开（中台重启、通道断了）。只关连接的话，Go 会把复用连接上的 GET 自动重发一次。
func hubDrop(w http.ResponseWriter) {
	conn, buf, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 64\r\n\r\n{")
	buf.Flush()
	conn.Close()
}

func hubAnswer(status int, headers ...string) func(http.ResponseWriter) {
	return hubAnswerBody(status, "", headers...)
}

func hubAnswerBody(status int, body string, headers ...string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		for i := 0; i+1 < len(headers); i += 2 {
			w.Header().Set(headers[i], headers[i+1])
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

// TestHubBundleFeedAsksWithETagAndWait 每次都请中台挂着等（Prefer: wait），带上一版的 ETag；换了版本才把账号交出去；
// 没问成之后的那一次不带 ETag，好让中台马上回话。
func TestHubBundleFeedAsksWithETagAndWait(t *testing.T) {
	const key = "pqk_secret-for-test"
	script, server := newHubScript(t, key)
	client := newHubBundleClient(server.URL+"/api/config/bundle/layout", key)
	type change struct {
		accounts []externalAccount
		first    bool
	}
	var changes []change
	client.onChange = func(accounts []externalAccount, first bool) { changes = append(changes, change{accounts, first}) }
	ctx := context.Background()
	steps := []struct {
		answer func(http.ResponseWriter)
		want   hubOutcome
	}{
		{hubAnswerBody(http.StatusOK, `{"version":3,"items":{"platform_accounts":[
			{"code":"recToutiao","platform_code":"toutiao","carrier":"article","display_name":"头条号 · FBIF","status":"在用","source_wechat_account":"FBIF食品饮料创新","notify_chat":"oc_1","auto_distribute":false},
			{"code":"recDouyin","platform_code":"douyin","carrier":"post","display_name":"抖音 · 测试","status":"测试","source_wechat_account":"FBIF食品创新展","notify_chat":"oc_2","auto_distribute":true}]},"warnings":[]}`,
			"ETag", `"layout-v3"`), hubAnswered},
		{hubAnswer(http.StatusNotModified), hubAnswered},
		{hubAnswerBody(http.StatusUnauthorized, `{"error":"request_error","message":"缺少或无效的 API key"}`), hubDenied},
		{hubAnswerBody(http.StatusOK, `{"version":4,"items":{}}`, "ETag", `"layout-v4"`, "Preference-Applied", "wait=10800"), hubWaiting},
		{hubAnswer(http.StatusBadGateway), hubFailed},
		{hubAnswerBody(http.StatusOK, `{"version":4,"items":{}}`, "ETag", `"layout-v4"`, "Preference-Applied", "wait=10800"), hubWaiting},
	}
	var etags []string
	for i, step := range steps {
		script.answers <- step.answer
		if got := client.feed.round(ctx, client.poll); got != step.want {
			t.Fatalf("第 %d 次：结果 %v，要 %v", i+1, got, step.want)
		}
		header := <-script.seen
		if header.Get("Prefer") != "wait=10800" {
			t.Fatalf("第 %d 次要请中台挂着等：%q", i+1, header.Get("Prefer"))
		}
		etags = append(etags, header.Get("If-None-Match"))
	}
	if got := strings.Join(etags, "|"); got != `|"layout-v3"|"layout-v3"|"layout-v3"|"layout-v4"|` {
		t.Fatalf("第一次和没问成之后不带 ETag，其余带上一版的：%q", got)
	}
	if len(changes) != 2 || !changes[0].first || changes[1].first || changes[1].accounts != nil {
		t.Fatalf("换了版本才交出账号（第二版缺这一项交 nil），同一版不再交：%+v", changes)
	}
	want := externalAccount{ID: "recDouyin", Platform: "douyin", Carrier: "post", Name: "抖音 · 测试", Status: "测试", Source: "FBIF食品创新展", NotifyChat: "oc_2", Auto: true}
	if got := changes[0].accounts; len(got) != 2 || got[1].ID != want.ID || got[1].Platform != want.Platform || got[1].Carrier != want.Carrier ||
		got[1].Name != want.Name || got[1].Status != want.Status || got[1].Source != want.Source || got[1].NotifyChat != want.NotifyChat || got[1].Auto != want.Auto {
		t.Fatalf("配置包的列对到账号上：%+v", got)
	}
	if strings.Contains(fmt.Sprint(client), key) {
		t.Fatal("打印客户端不许露出钥匙")
	}
}

func TestHubBundleConfigRequiresURLAndKey(t *testing.T) {
	t.Setenv(hubBundleURLEnv, "")
	t.Setenv(hubKeyEnv, "pqk_x")
	if _, _, err := loadHubConfig(); err == nil || !strings.Contains(err.Error(), hubBundleURLEnv) {
		t.Fatalf("缺地址要说缺哪个：%v", err)
	}
	t.Setenv(hubBundleURLEnv, "http://hub.example.com/api/config/bundle/layout")
	if _, _, err := loadHubConfig(); err == nil {
		t.Fatal("非本机地址必须 HTTPS")
	}
	t.Setenv(hubBundleURLEnv, "http://127.0.0.1:13199/api/config/bundle/layout")
	t.Setenv(hubKeyEnv, "")
	if _, _, err := loadHubConfig(); err == nil || !strings.Contains(err.Error(), hubKeyEnv) {
		t.Fatalf("缺钥匙要说缺哪个：%v", err)
	}
}

// TestSourceCheckedAt 名单内容的核验时间按中台给的来源年龄往回减；年龄不对或中台说校验不过就不续。
func TestSourceCheckedAt(t *testing.T) {
	receivedAt := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	header := func(pairs ...string) http.Header {
		h := http.Header{}
		for i := 0; i < len(pairs); i += 2 {
			h.Set(pairs[i], pairs[i+1])
		}
		return h
	}
	cases := []struct {
		name   string
		header http.Header
		want   time.Time
		renew  bool
	}{
		{"有年龄就从收到回包的时刻往回减", header("X-Hub-Source-Age", "90"), receivedAt.Add(-90 * time.Second), true},
		{"年龄和校验不过同时来，以年龄为准", header("X-Hub-Source-Age", "30", "X-Hub-Stale", "validation"), receivedAt.Add(-30 * time.Second), true},
		{"年龄不是数", header("X-Hub-Source-Age", "abc"), time.Time{}, false},
		{"年龄是负的", header("X-Hub-Source-Age", "-5"), time.Time{}, false},
		{"年龄是空的", header("X-Hub-Source-Age", ""), time.Time{}, false},
		{"年龄大到减不出来", header("X-Hub-Source-Age", "99999999999"), time.Time{}, false},
		{"老中台说校验不过", header("X-Hub-Stale", "validation"), time.Time{}, false},
		{"老中台什么都没说", header(), receivedAt, true},
	}
	for _, c := range cases {
		got, renew := sourceCheckedAt(c.header, receivedAt)
		if renew != c.renew || !got.Equal(c.want) {
			t.Errorf("%s：得到 %v %v，要 %v %v", c.name, got, renew, c.want, c.renew)
		}
	}
}

// TestHubBundleUnheldRepliesRenewOnlyWhenSourceSynced 中台回了话却没挂着等（意外）：一直回 304 不等于名单还新，
// 来源同步停了，核验时间就停在那里，过了 10 分钟排版接口拒掉所有钥匙；来源恢复同步就续上。
func TestHubBundleUnheldRepliesRenewOnlyWhenSourceSynced(t *testing.T) {
	key, fingerprint := mustLayoutKey(t)
	script, server := newHubScript(t, "pqk_test")
	clock := newLayoutTestClock()
	client := newHubBundleClient(server.URL, "pqk_test")
	client.feed.now = clock.Now
	directory := newLayoutCallerDirectory(client, clock.Now)
	ask := func(answer func(http.ResponseWriter)) {
		t.Helper()
		script.answers <- answer
		if got := client.feed.round(context.Background(), client.poll); got != hubAnswered {
			t.Fatalf("没挂着等的回包算回了话，按出错退避再问：%v", got)
		}
		<-script.seen
	}

	ask(hubAnswerBody(http.StatusOK, fmt.Sprintf(`{"version":7,"items":{"platform_accounts":[],"api_callers":[{"code":"recCaller","status":"在用","key_sha256":%q}]}}`, fingerprint),
		"ETag", `"layout-v7"`, "X-Hub-Source-Age", "30"))
	first := clock.Now().Add(-30 * time.Second)
	if snap := client.LayoutCallers(clock.Now()); !snap.CheckedAt.Equal(first) || snap.Generation != 1 || !snap.Present || len(snap.Rows) != 1 {
		t.Fatalf("200 按来源年龄算核验时间：%+v", snap)
	}
	clock.Add(time.Minute)
	ask(hubAnswer(http.StatusNotModified, "X-Hub-Source-Age", "660"))
	if snap := client.LayoutCallers(clock.Now()); !snap.CheckedAt.Equal(first) {
		t.Fatalf("304 但来源 11 分钟没同步，核验时间不动：%v", snap.CheckedAt)
	}
	clock.Add(layoutCallersMaxStale - time.Minute + time.Second)
	if _, e := directory.authenticate(key); e == nil || e.Code != layoutErrAuthUnavailable {
		t.Fatalf("名单内容超过 10 分钟没核验，中台照常回 304 也不放：%v", e)
	}
	ask(hubAnswer(http.StatusNotModified, "X-Hub-Source-Age", "5"))
	synced := clock.Now().Add(-5 * time.Second)
	if caller, e := directory.authenticate(key); e != nil || caller.Code != "recCaller" {
		t.Fatalf("来源同步恢复后照常认：%v %v", caller, e)
	}
	clock.Add(time.Minute)
	ask(hubAnswer(http.StatusNotModified, "X-Hub-Stale", "validation"))
	if snap := client.LayoutCallers(clock.Now()); !snap.CheckedAt.Equal(synced) {
		t.Fatalf("没有年龄、中台说校验不过，不续：%v", snap.CheckedAt)
	}
}

// TestHubBundleCallersFreshWhileHubHolds 中台升级后（挂着等）：来源年龄是 0、请求正挂着，名单就一直是新的；
// 中台回 304 说来源变旧了，照年龄算、10 分钟保护照样起作用；挂着的请求断了，名单新到断开那一刻。
func TestHubBundleCallersFreshWhileHubHolds(t *testing.T) {
	key, fingerprint := mustLayoutKey(t)
	script, server := newHubScript(t, "pqk_test")
	clock := newLayoutTestClock()
	client := newHubBundleClient(server.URL, "pqk_test")
	client.feed.now = clock.Now
	directory := newLayoutCallerDirectory(client, clock.Now)
	held := func(answer string, age string) func(http.ResponseWriter) {
		if answer == "" {
			return hubAnswer(http.StatusNotModified, "X-Hub-Source-Age", age, "Preference-Applied", "wait=10800")
		}
		return hubAnswerBody(http.StatusOK, answer, "ETag", `"layout-v1"`, "X-Hub-Source-Age", age, "Preference-Applied", "wait=10800")
	}
	// hang 发下一轮，等请求到了中台（挂着）再回来；返回这一轮的结果。
	hang := func() (<-chan hubOutcome, http.Header) {
		done := make(chan hubOutcome, 1)
		go func() { done <- client.feed.round(context.Background(), client.poll) }()
		return done, <-script.seen
	}
	allowed := func(step string, want bool) {
		t.Helper()
		caller, e := directory.authenticate(key)
		if want && (e != nil || caller.Code != "recCaller") {
			t.Fatalf("%s：要放行：%v %v", step, caller, e)
		}
		if !want && (e == nil || e.Code != layoutErrAuthUnavailable) {
			t.Fatalf("%s：要 auth_unavailable：%v", step, e)
		}
	}

	body := fmt.Sprintf(`{"version":1,"items":{"platform_accounts":[],"api_callers":[{"code":"recCaller","status":"在用","key_sha256":%q}]}}`, fingerprint)
	script.answers <- held(body, "0")
	if got := client.feed.round(context.Background(), client.poll); got != hubWaiting {
		t.Fatalf("中台照办了等待，马上再问：%v", got)
	}
	<-script.seen

	done, header := hang()
	if header.Get("If-None-Match") != `"layout-v1"` {
		t.Fatalf("挂着的请求带上一版 ETag：%q", header.Get("If-None-Match"))
	}
	clock.Add(2 * time.Hour)
	allowed("来源年龄 0、请求挂了 2 小时", true)

	script.answers <- held("", "120")
	if got := <-done; got != hubWaiting {
		t.Fatalf("304 也马上再问：%v", got)
	}
	done, _ = hang()
	clock.Add(9 * time.Minute)
	allowed("中台说来源 2 分钟前才确认过、又过了 9 分钟，请求挂着也不放", false)

	script.answers <- held("", "0")
	<-done
	allowed("来源又确认上了", true)
	done, _ = hang()
	clock.Add(time.Hour)
	script.answers <- hubDrop
	if got := <-done; got != hubFailed {
		t.Fatalf("挂着的请求断了算没问成：%v", got)
	}
	clock.Add(9 * time.Minute)
	allowed("断开不到 10 分钟", true)
	clock.Add(2 * time.Minute)
	allowed("断开超过 10 分钟", false)

	script.answers <- held(body, "0")
	client.feed.round(context.Background(), client.poll)
	if header := <-script.seen; header.Get("If-None-Match") != "" {
		t.Fatalf("断开之后的那一次不带 ETag，中台马上回话：%q", header.Get("If-None-Match"))
	}
	allowed("重新连上", true)
}
