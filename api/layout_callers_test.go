package main

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestHubBundleClientReadsLayoutCallers 接口调用方和外发账号同一份配置包、同一个 ETag：
// 304 沿用上一份；包里没有这一项要分得清（Present=false），不能当成「没人能调」；中台出错留着最后一份。
func TestHubBundleClientReadsLayoutCallers(t *testing.T) {
	script, server := newHubScript(t, "pqk_test")
	client := newHubBundleClient(server.URL, "pqk_test")
	ask := func(answer func(http.ResponseWriter)) layoutCallerSnapshot {
		t.Helper()
		script.answers <- answer
		client.feed.round(context.Background(), client.poll)
		<-script.seen
		return client.LayoutCallers(time.Now())
	}
	if snap := client.LayoutCallers(time.Now()); snap.Generation != 0 {
		t.Fatalf("开机还没拿到：%+v", snap)
	}
	snap := ask(hubAnswerBody(http.StatusOK, `{"version":5,"items":{"platform_accounts":[],"api_callers":[
		{"code":"recCaller","name":"选题助手","owner":"李四","status":"在用","key_sha256":"ab12"}]}}`, "ETag", `"layout-v5"`, "X-Hub-Source-Age", "0"))
	if !snap.Present || len(snap.Rows) != 1 || snap.Rows[0].Code != "recCaller" || snap.Rows[0].KeySHA256 != "ab12" || snap.Generation != 1 || snap.CheckedAt.IsZero() {
		t.Fatalf("第一次拿全量：%+v", snap)
	}
	if again := ask(hubAnswer(http.StatusNotModified, "X-Hub-Source-Age", "0")); !again.Present || len(again.Rows) != 1 || again.Generation != 1 || again.CheckedAt.Before(snap.CheckedAt) {
		t.Fatalf("304 沿用上一份：%+v", again)
	}
	missing := ask(hubAnswerBody(http.StatusOK, `{"version":6,"items":{"platform_accounts":[]}}`, "ETag", `"layout-v6"`, "X-Hub-Source-Age", "0"))
	if missing.Present || len(missing.Rows) != 0 || missing.Generation != 2 {
		t.Fatalf("包里没有接口调用方这一项要说没有：%+v", missing)
	}
	if kept := ask(hubAnswer(http.StatusBadGateway)); kept.Generation != 2 || kept.Present {
		t.Fatalf("中台出错留着最后一份：%+v", kept)
	}
}
