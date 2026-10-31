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

// TestHubBundleClientReadsAccountsWithETag 账号从中台配置包读：第一次拿全量，之后带 ETag 问，没变回 304 就用上一份；
// 报错里不带钥匙，缺账号这一项交给缓存整份不收。
func TestHubBundleClientReadsAccountsWithETag(t *testing.T) {
	const key = "pqk_secret-for-test"
	var seen []string
	answers := []func(http.ResponseWriter){
		func(w http.ResponseWriter) {
			w.Header().Set("ETag", `"layout-v3"`)
			fmt.Fprint(w, `{"version":3,"items":{"platform_accounts":[
				{"code":"recToutiao","platform_code":"toutiao","carrier":"article","display_name":"头条号 · FBIF","status":"在用","source_wechat_account":"FBIF食品饮料创新","notify_chat":"oc_1","auto_distribute":false},
				{"code":"recDouyin","platform_code":"douyin","carrier":"post","display_name":"抖音 · 测试","status":"测试","source_wechat_account":"FBIF食品创新展","notify_chat":"oc_2","auto_distribute":true}]},"warnings":[]}`)
		},
		func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotModified) },
		func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"request_error","message":"缺少或无效的 API key"}`)
		},
		func(w http.ResponseWriter) { fmt.Fprint(w, `{"version":4,"items":{}}`) },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Errorf("要带钥匙：%q", r.Header.Get("Authorization"))
		}
		seen = append(seen, r.Header.Get("If-None-Match"))
		answers[0](w)
		answers = answers[1:]
	}))
	defer server.Close()
	client := newHubBundleClient(server.URL+"/api/config/bundle/layout", key)

	got, err := client.Accounts(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("第一次拿全量：%v %v", got, err)
	}
	want := externalAccount{ID: "recDouyin", Platform: "douyin", Carrier: "post", Name: "抖音 · 测试", Status: "测试", Source: "FBIF食品创新展", NotifyChat: "oc_2", Auto: true}
	if got[1].ID != want.ID || got[1].Platform != want.Platform || got[1].Carrier != want.Carrier || got[1].Name != want.Name ||
		got[1].Status != want.Status || got[1].Source != want.Source || got[1].NotifyChat != want.NotifyChat || got[1].Auto != want.Auto {
		t.Fatalf("配置包的列对到账号上：%+v", got[1])
	}
	if again, err := client.Accounts(context.Background()); err != nil || len(again) != 2 || again[0].ID != "recToutiao" {
		t.Fatalf("没变就用上一份：%v %v", again, err)
	}
	if _, err := client.Accounts(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), key) {
		t.Fatalf("钥匙不对要报出来、且不带钥匙：%v", err)
	}
	if missing, err := client.Accounts(context.Background()); err != nil || missing != nil {
		t.Fatalf("包里缺账号这一项回 nil，由缓存整份不收：%v %v", missing, err)
	}
	if strings.Join(seen, "|") != `|"layout-v3"|"layout-v3"|"layout-v3"` {
		t.Fatalf("第一次不带 ETag，之后带上一版的：%q", seen)
	}
	if strings.Contains(fmt.Sprint(client), key) {
		t.Fatal("打印客户端不许露出钥匙")
	}
}

func TestHubBundleConfigRequiresURLAndKey(t *testing.T) {
	t.Setenv(hubBundleURLEnv, "")
	t.Setenv(hubKeyEnv, "pqk_x")
	if _, err := loadHubBundleClient(); err == nil || !strings.Contains(err.Error(), hubBundleURLEnv) {
		t.Fatalf("缺地址要说缺哪个：%v", err)
	}
	t.Setenv(hubBundleURLEnv, "http://hub.example.com/api/config/bundle/layout")
	if _, err := loadHubBundleClient(); err == nil {
		t.Fatal("非本机地址必须 HTTPS")
	}
	t.Setenv(hubBundleURLEnv, "http://127.0.0.1:13199/api/config/bundle/layout")
	t.Setenv(hubKeyEnv, "")
	if _, err := loadHubBundleClient(); err == nil || !strings.Contains(err.Error(), hubKeyEnv) {
		t.Fatalf("缺钥匙要说缺哪个：%v", err)
	}
}

// TestSourceCheckedAt 名单内容的核验时间按中台给的来源年龄往回减；年龄不对或中台说校验不过就不续。
func TestSourceCheckedAt(t *testing.T) {
	sentAt := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
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
		{"有年龄就用发请求的时刻往回减", header("X-Hub-Source-Age", "90"), sentAt.Add(-90 * time.Second), true},
		{"年龄和校验不过同时来，以年龄为准", header("X-Hub-Source-Age", "30", "X-Hub-Stale", "validation"), sentAt.Add(-30 * time.Second), true},
		{"年龄不是数", header("X-Hub-Source-Age", "abc"), time.Time{}, false},
		{"年龄是负的", header("X-Hub-Source-Age", "-5"), time.Time{}, false},
		{"年龄是空的", header("X-Hub-Source-Age", ""), time.Time{}, false},
		{"年龄大到减不出来", header("X-Hub-Source-Age", "99999999999"), time.Time{}, false},
		{"老中台说校验不过", header("X-Hub-Stale", "validation"), time.Time{}, false},
		{"老中台什么都没说", header(), sentAt, true},
	}
	for _, c := range cases {
		got, renew := sourceCheckedAt(c.header, sentAt)
		if renew != c.renew || !got.Equal(c.want) {
			t.Errorf("%s：得到 %v %v，要 %v %v", c.name, got, renew, c.want, c.renew)
		}
	}
}

// TestHubBundle304RenewsOnlyWhenSourceSynced 中台一直回 304 不等于名单还新：来源同步停了，核验时间就停在那里，
// 过了 10 分钟排版接口拒掉所有钥匙；问中台的节流照旧按上次问到的时间，不会因为内容旧了就每 20 秒问一次。
func TestHubBundle304RenewsOnlyWhenSourceSynced(t *testing.T) {
	key, fingerprint, err := newLayoutKey()
	if err != nil {
		t.Fatal(err)
	}
	var asked int
	answers := []func(http.ResponseWriter){
		func(w http.ResponseWriter) {
			w.Header().Set("ETag", `"layout-v7"`)
			w.Header().Set("X-Hub-Source-Age", "30")
			fmt.Fprintf(w, `{"version":7,"items":{"platform_accounts":[],"api_callers":[{"code":"recCaller","status":"在用","key_sha256":%q}]}}`, fingerprint)
		},
		func(w http.ResponseWriter) {
			w.Header().Set("X-Hub-Source-Age", "660")
			w.WriteHeader(http.StatusNotModified)
		},
		func(w http.ResponseWriter) {
			w.Header().Set("X-Hub-Source-Age", "5")
			w.WriteHeader(http.StatusNotModified)
		},
		func(w http.ResponseWriter) {
			w.Header().Set("X-Hub-Stale", "validation")
			w.WriteHeader(http.StatusNotModified)
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked++
		answers[0](w)
		answers = answers[1:]
	}))
	defer server.Close()
	client := newHubBundleClient(server.URL, "pqk_test")
	ctx := context.Background()
	within := func(step string, got time.Time, before, after time.Time, age time.Duration) {
		t.Helper()
		if got.Before(before.Add(-age)) || got.After(after.Add(-age)) {
			t.Fatalf("%s：核验时间 %v 不在 [%v, %v]", step, got, before.Add(-age), after.Add(-age))
		}
	}

	before := time.Now()
	_, _, first, err := client.LayoutCallers(ctx, time.Minute)
	within("200 按来源年龄算", first, before, time.Now(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, again, _ := client.LayoutCallers(ctx, time.Minute); asked != 1 || !again.Equal(first) {
		t.Fatalf("内容 30 秒前核验过、一分钟内问过中台，不再问：问了 %d 次", asked)
	}

	_, _, stuck, err := client.LayoutCallers(ctx, 0)
	if err != nil || asked != 2 || !stuck.Equal(first) {
		t.Fatalf("304 但来源 11 分钟没同步，核验时间不动：%v %v", stuck, err)
	}
	now := time.Now()
	directory := newLayoutCallerDirectory(client, func() time.Time { return now })
	if err := directory.refresh(ctx, time.Minute); err != nil || asked != 2 {
		t.Fatalf("刚问过中台，节流按上次问到的时间算，不再问：问了 %d 次 %v", asked, err)
	}
	directory.now = func() time.Time { return first.Add(layoutCallersMaxStale + time.Second) }
	if _, e := directory.authenticate(key); e == nil || e.Code != layoutErrAuthUnavailable {
		t.Fatalf("名单内容超过 10 分钟没核验，中台照常回 304 也不放：%v", e)
	}

	before = time.Now()
	_, _, synced, err := client.LayoutCallers(ctx, 0)
	within("来源同步恢复后 304 续上", synced, before, time.Now(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := directory.refresh(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	directory.now = func() time.Time { return synced.Add(time.Minute) }
	if caller, e := directory.authenticate(key); e != nil || caller.Code != "recCaller" {
		t.Fatalf("续上之后照常认：%v %v", caller, e)
	}

	if _, _, stale, err := client.LayoutCallers(ctx, 0); err != nil || asked != 4 || !stale.Equal(synced) {
		t.Fatalf("没有年龄、中台说校验不过，不续：%v %v", stale, err)
	}
}
