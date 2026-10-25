package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
