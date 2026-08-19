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
	"strings"
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

func TestFoodTalksGetNewsReadsPublishedDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/news/admin/news/62852" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "msg": "success",
			"data": map[string]any{
				"id": 62852, "title": "把茶做成零食", "publishStatus": 1,
				"content": "<p>最新已发布正文</p>", "coverImg": "https://cdn.example.com/a.jpg",
				"sourceName": "FBIF食品饮料创新", "sourceType": 0,
			},
		})
	}))
	defer srv.Close()
	t.Setenv("FOODTALKS_BASE_URL", srv.URL)
	t.Setenv("FOODTALKS_TOKEN", "tok")
	t.Setenv("FOODTALKS_ORIGIN", "")
	cli, err := newFoodTalksClientFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	news, err := cli.GetNews(context.Background(), 62852)
	if err != nil {
		t.Fatal(err)
	}
	if news.ID != 62852 || news.PublishStatus != foodTalksPublishStatusPublished || !strings.Contains(news.Content, "最新已发布正文") {
		t.Fatalf("GetNews 未读到已发布详情：%+v", news)
	}
}

func TestFoodTalksNewsTagsUseConfirmedObjectShape(t *testing.T) {
	withTag, err := json.Marshal(buildFoodTalksNewsBody(json.RawMessage(`""`), foodTalksNews{
		NewsTags: []foodTalksNewsTag{{TagID: 10752, TagName: "沃尔玛"}},
	}))
	if err != nil {
		t.Fatalf("marshal newsTags: %v", err)
	}
	if !strings.Contains(string(withTag), `"newsTags":[{"tagId":10752,"tagName":"沃尔玛"}]`) {
		t.Fatalf("newsTags 必须使用 API 确认的对象数组结构：%s", withTag)
	}

	empty, err := json.Marshal(buildFoodTalksNewsBody(json.RawMessage(`""`), foodTalksNews{}))
	if err != nil {
		t.Fatalf("marshal empty newsTags: %v", err)
	}
	if !strings.Contains(string(empty), `"newsTags":[]`) {
		t.Fatalf("没有标签时必须显式发送空数组，不能是 null：%s", empty)
	}
}
