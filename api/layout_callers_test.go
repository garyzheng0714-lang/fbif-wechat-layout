package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHubBundleClientReadsLayoutCallers 接口调用方和外发账号同一份配置包、同一个 ETag：
// 刚核对过就不再问；304 只刷新核对时间、沿用上一份；包里没有这一项要分得清（present=false），不能当成「没人能调」。
func TestHubBundleClientReadsLayoutCallers(t *testing.T) {
	var asked int
	answers := []func(http.ResponseWriter){
		func(w http.ResponseWriter) {
			w.Header().Set("ETag", `"layout-v5"`)
			fmt.Fprint(w, `{"version":5,"items":{"platform_accounts":[],"api_callers":[
				{"code":"recCaller","name":"选题助手","owner":"李四","status":"在用","key_sha256":"ab12"}]}}`)
		},
		func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotModified) },
		func(w http.ResponseWriter) { fmt.Fprint(w, `{"version":6,"items":{"platform_accounts":[]}}`) },
		func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked++
		answers[0](w)
		answers = answers[1:]
	}))
	defer server.Close()
	client := newHubBundleClient(server.URL, "pqk_test")
	ctx := context.Background()

	rows, present, okAt, err := client.LayoutCallers(ctx, time.Minute)
	if err != nil || !present || len(rows) != 1 || rows[0].Code != "recCaller" || rows[0].KeySHA256 != "ab12" || okAt.IsZero() {
		t.Fatalf("第一次拿全量：%v %v %v %v", rows, present, okAt, err)
	}
	if again, _, _, err := client.LayoutCallers(ctx, time.Minute); err != nil || len(again) != 1 || asked != 1 {
		t.Fatalf("一分钟内核对过就不再问：问了 %d 次 %v", asked, err)
	}

	rows, present, notModifiedAt, err := client.LayoutCallers(ctx, 0)
	if err != nil || !present || len(rows) != 1 || asked != 2 || notModifiedAt.Before(okAt) {
		t.Fatalf("304 沿用上一份并刷新核对时间：%v %v %v %v", rows, present, notModifiedAt, err)
	}

	rows, present, _, err = client.LayoutCallers(ctx, 0)
	if err != nil || present || len(rows) != 0 {
		t.Fatalf("包里没有接口调用方这一项要说没有：%v %v %v", rows, present, err)
	}

	if _, _, _, err := client.LayoutCallers(ctx, 0); err == nil {
		t.Fatal("中台出错要报出来，由目录决定还能不能用上一份")
	}
}
