package main

// 白名单模式头契约（2026-07-10 用户裁定 + 双环境实测）：
// FOODTALKS_ORIGIN 未配置 = 正式环境 IP 白名单模式，不得伪装浏览器——
// 请求必须无 Origin/Referer 且 UA 为空（后端按「白名单 IP + 非浏览器请求」识别服务调用）。
// ORIGIN 配置 = 测试环境浏览器头模式，三头齐全否则网关 403。
// 本测试防止任何一侧被悄悄改坏（改坏白名单侧=正式环境被当伪装拦；改坏浏览器侧=测试环境 403）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func foodTalksHeaderProbe(t *testing.T, origin string) http.Header {
	t.Helper()
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "success", "data": 1})
	}))
	defer srv.Close()
	t.Setenv("FOODTALKS_BASE_URL", srv.URL)
	t.Setenv("FOODTALKS_TOKEN", "tok")
	t.Setenv("FOODTALKS_ORIGIN", origin)
	cli, err := newFoodTalksClientFromEnv()
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if _, err := cli.CreateNews(context.Background(), foodTalksNews{PublishStatus: foodTalksPublishStatusDraft}); err != nil {
		t.Fatalf("CreateNews: %v", err)
	}
	return got
}

func TestFoodTalksWhitelistModeNoBrowserHeaders(t *testing.T) {
	h := foodTalksHeaderProbe(t, "")
	if h.Get("Origin") != "" || h.Get("Referer") != "" || h.Get("User-Agent") != "" {
		t.Fatalf("白名单模式不得带浏览器头：Origin=%q Referer=%q UA=%q", h.Get("Origin"), h.Get("Referer"), h.Get("User-Agent"))
	}
}

func TestFoodTalksBrowserHeaderModeKeepsAllThree(t *testing.T) {
	h := foodTalksHeaderProbe(t, "https://admin-we-test.example")
	if h.Get("Origin") == "" || h.Get("Referer") == "" || h.Get("User-Agent") == "" {
		t.Fatalf("浏览器头模式三头必须齐全（测试环境网关 403 教训）：Origin=%q Referer=%q UA=%q", h.Get("Origin"), h.Get("Referer"), h.Get("User-Agent"))
	}
}
