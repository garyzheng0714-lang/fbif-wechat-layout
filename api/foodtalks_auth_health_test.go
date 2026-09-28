package main

// 凭证失效必须直接进监控（2026-09-24 事故：token 失效 17 小时内监控全绿，
// 之后只能从「对账积压」间接看出，4 天没人认出是凭证问题）。
// 信号只跟「最近一次有鉴权结论的 FoodTalks 响应」走：401/JSON 403 置位，
// 任何 2xx（含业务 code!=0）清除；网络错、5xx 不是鉴权证据，不改状态。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type foodTalksAuthStub struct {
	status atomic.Int32
	body   atomic.Value
}

func newFoodTalksAuthStub(t *testing.T) *foodTalksAuthStub {
	t.Helper()
	stub := &foodTalksAuthStub{}
	stub.set(http.StatusOK, `{"code":0,"msg":"success","data":{"records":[]}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(stub.status.Load()))
		_, _ = w.Write([]byte(stub.body.Load().(string)))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FOODTALKS_BASE_URL", srv.URL)
	t.Setenv("FOODTALKS_TOKEN", redactionProbeToken)
	t.Setenv("FOODTALKS_ORIGIN", "")
	return stub
}

func (s *foodTalksAuthStub) set(status int, body string) {
	s.status.Store(int32(status))
	s.body.Store(body)
}

func TestFoodTalksCredentialSignalFollowsLatestAuthResult(t *testing.T) {
	stub := newFoodTalksAuthStub(t)
	health := &foodTalksAuthHealth{}
	cli, err := newFoodTalksClientFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cli.authHealth = health
	search := func() { _, _ = cli.SearchNewsByTitle(context.Background(), "标题") }

	if health.invalid() {
		t.Fatal("启动后尚未调用 FoodTalks，不得判凭证失效")
	}
	steps := []struct {
		name    string
		status  int
		body    string
		invalid bool
	}{
		{"5xx 不是鉴权证据", http.StatusBadGateway, `bad gateway`, false},
		{"401 置位", http.StatusUnauthorized, `{"code":401,"msg":"error","data":"` + redactionProbeToken + `"}`, true},
		{"失效后 5xx 不清除", http.StatusInternalServerError, `oops`, true},
		{"业务错 code!=0 说明凭证有效", http.StatusOK, `{"code":1,"msg":"参数错误"}`, false},
		{"JSON 403 同样置位", http.StatusForbidden, `{"code":403,"msg":"forbidden"}`, true},
		{"换好 token 后 200 清除", http.StatusOK, `{"code":0,"msg":"success","data":{"records":[]}}`, false},
	}
	for _, step := range steps {
		stub.set(step.status, step.body)
		search()
		if got := health.invalid(); got != step.invalid {
			t.Fatalf("%s：invalid=%v，期望 %v", step.name, got, step.invalid)
		}
	}
}

func TestFoodTalksAuthHealthNilSafe(t *testing.T) {
	var health *foodTalksAuthHealth
	health.record(false)
	if health.invalid() {
		t.Fatal("未装配凭证状态时不得报失效")
	}
}

func TestPublishMonitoringAlertsOnFoodTalksCredentialInvalid(t *testing.T) {
	t.Setenv(publishSyncServiceTokenEnv, "monitor-secret")
	t.Setenv(deploymentWritesPausedEnv, "0")
	stub := newFoodTalksAuthStub(t)
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	p := newPublishPipeline(store, nil)
	mux := http.NewServeMux()
	registerPublishRoutes(mux, p)
	monitor := func() (map[string]any, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/publish/monitoring", nil)
		req.Header.Set(publishSyncServiceHeader, "monitor-secret")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("monitoring status=%d body=%s", rec.Code, rec.Body.String())
		}
		var wire map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		return wire, rec.Body.String()
	}
	search := func() {
		ft, err := p.foodTalks()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = ft.SearchNewsByTitle(context.Background(), "标题")
	}

	// 无任何任务、无积压：凭证失效也必须立刻告警（事故里的 17 小时空窗）。
	stub.set(http.StatusUnauthorized, `{"code":401,"msg":"error","data":"`+redactionProbeToken+`"}`)
	search()
	wire, raw := monitor()
	if wire["ready"] != false || wire["foodtalks_credential_invalid"] != true {
		t.Fatalf("凭证失效时监控必须 ready=false 且标记 foodtalks_credential_invalid：%s", raw)
	}
	if !strings.Contains(raw, `"foodtalks_credential_invalid"`) || !publishMonitoringWireHas(wire["alert_issues"], "foodtalks_credential_invalid") {
		t.Fatalf("凭证失效必须进 alert_issues：%s", raw)
	}
	assertNoTokenFragment(t, raw)

	stub.set(http.StatusOK, `{"code":0,"msg":"success","data":{"records":[]}}`)
	search()
	wire, raw = monitor()
	if wire["foodtalks_credential_invalid"] != false || publishMonitoringWireHas(wire["alert_issues"], "foodtalks_credential_invalid") {
		t.Fatalf("换好 token 后信号必须清除：%s", raw)
	}
	if wire["ready"] != true {
		t.Fatalf("无任务且凭证恢复后应 ready：%s", raw)
	}
}

func publishMonitoringWireHas(list any, want string) bool {
	items, _ := list.([]any)
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
