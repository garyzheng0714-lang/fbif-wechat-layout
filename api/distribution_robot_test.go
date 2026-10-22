package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testRobotToken = "robot-secret-token"

// TestDistributionRobotConfigIsOptionalAndRedacted 两个变量都不是必配项；缺了只说缺哪个，地址只认
// HTTPS 或本机回环，token 不进日志。
func TestDistributionRobotConfigIsOptionalAndRedacted(t *testing.T) {
	for _, tc := range []struct{ url, token, want string }{
		{"", testRobotToken, "缺少 " + distributionAPIURLEnv},
		{"http://10.0.0.5:18095", testRobotToken, "必须是 HTTPS"},
		{"http://127.0.0.1:18095", "", "缺少 " + distributionAPITokenEnv},
	} {
		t.Setenv(distributionAPIURLEnv, tc.url)
		t.Setenv(distributionAPITokenEnv, tc.token)
		if _, err := loadDistributionRobotConfig(); err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), testRobotToken) {
			t.Fatalf("url=%q token=%q 应报 %q：%v", tc.url, tc.token, tc.want, err)
		}
	}
	t.Setenv(distributionAPIURLEnv, "http://127.0.0.1:18095/")
	t.Setenv(distributionAPITokenEnv, testRobotToken)
	cfg, err := loadDistributionRobotConfig()
	if err != nil || cfg.BaseURL != "http://127.0.0.1:18095" {
		t.Fatalf("本机通道地址应可用：%+v %v", cfg, err)
	}
	if printed := fmt.Sprintf("%v %+v %s", cfg, cfg, cfg); strings.Contains(printed, testRobotToken) {
		t.Fatalf("打印配置不得带 token：%s", printed)
	}
	if raw, _ := json.Marshal(cfg); strings.Contains(string(raw), testRobotToken) {
		t.Fatalf("序列化配置不得带 token：%s", raw)
	}
}

// TestDistributionRobotClientMapsResponses 分发服务每种回答映射成哪类错误（契约见分发工具 README「接口」）：
// 点存草稿之前被拒的是 rejected（没建稿、可再存）；草稿箱要人删的是 reconcile；平台出错、超时、
// 成功响应不完整的是 uncertain（按同一个键查）。
func TestDistributionRobotClientMapsResponses(t *testing.T) {
	type reply struct {
		status int
		body   string
	}
	var current reply
	var seen []*http.Request
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seen = append(seen, r)
		bodies = append(bodies, string(raw))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(current.status)
		_, _ = io.WriteString(w, current.body)
	}))
	defer server.Close()
	client := newDistributionRobotClient(distributionRobotConfig{BaseURL: server.URL, Token: testRobotToken})
	errorBody := func(code, message string) string {
		return `{"error":{"code":"` + code + `","message":"` + message + `"}}`
	}
	saved := `{"status":"saved","article_id":"1879718514554909812","draft_url":"https://baijiahao.baidu.com/builder/rc/edit?type=news&article_id=1879718514554909812",` +
		`"expected":{"headings":3,"bold":9,"images":9,"captions":["图注"]},"read_back":{"title":"t","headings":3,"bold":9,"images":9,"captions":1,"cover_uploaded":true},` +
		`"match":true,"mismatches":[],"read_back_error":null,"replayed":false,"attempts":1}`
	request := robotDraftRequest{IdempotencyKey: "job-282.baijiahao", Title: "标题", HTML: "<p>正文</p>", CoverURL: "https://cdn.example.com/a.jpg"}

	current = reply{http.StatusOK, saved}
	record, err := client.SaveDraft(context.Background(), "baijiahao", request)
	if err != nil || record.ArticleID != "1879718514554909812" || record.ReadBack == nil || record.ReadBack.Captions != 1 || record.Match == nil || !*record.Match {
		t.Fatalf("成功响应应解成完整记录：%+v %v", record, err)
	}
	last := seen[len(seen)-1]
	if last.Method != http.MethodPost || last.URL.Path != "/v1/platforms/baijiahao/drafts" || last.Header.Get("Authorization") != "Bearer "+testRobotToken {
		t.Fatalf("请求不对：%s %s auth=%q", last.Method, last.URL.Path, last.Header.Get("Authorization"))
	}
	var sent robotDraftRequest
	if err := json.Unmarshal([]byte(bodies[len(bodies)-1]), &sent); err != nil || sent != request {
		t.Fatalf("请求体应是契约里的四个字段：%s", bodies[len(bodies)-1])
	}

	var rejected *robotRejectedError
	var reconcile *robotReconcileError
	var uncertain *robotUncertainError
	for _, tc := range []struct {
		name  string
		reply reply
		check func(error) bool
	}{
		{"400 参数", reply{400, errorBody("invalid_request", "标题要 2–64 字")}, func(err error) bool { return errors.As(err, &rejected) && rejected.Status == 400 }},
		{"401 token", reply{401, errorBody("unauthorized", "token 不对")}, func(err error) bool { return errors.As(err, &rejected) && rejected.Status == 401 }},
		{"409 键已用于别的内容", reply{409, errorBody("idempotency_key_reused", "这个键已用来存过别的内容")}, func(err error) bool { return errors.As(err, &rejected) }},
		{"409 要人对账", reply{409, errorBody("needs_reconcile", "同名草稿多于一篇")}, func(err error) bool {
			return errors.As(err, &reconcile) && reconcile.Message == "同名草稿多于一篇"
		}},
		{"422 封面", reply{422, errorBody("cover_unavailable", "封面下不下来")}, func(err error) bool { return errors.As(err, &rejected) && rejected.Code == "cover_unavailable" }},
		{"503 没登录", reply{503, errorBody("login_required", "服务器浏览器没登录百家号")}, func(err error) bool { return errors.As(err, &rejected) && rejected.Code == "login_required" }},
		{"503 别的", reply{503, errorBody("busy", "忙")}, func(err error) bool { return errors.As(err, &uncertain) }},
		{"502 平台出错", reply{502, errorBody("platform_error", "平台页面出错")}, func(err error) bool { return errors.As(err, &uncertain) }},
		{"500", reply{500, "oops"}, func(err error) bool { return errors.As(err, &uncertain) }},
		{"200 但还没存好", reply{200, `{"status":"saving"}`}, func(err error) bool { return errors.As(err, &uncertain) }},
		{"200 但不是 JSON", reply{200, "<html>"}, func(err error) bool { return errors.As(err, &uncertain) }},
		{"200 已存但没编号", reply{200, `{"status":"saved","article_id":""}`}, func(err error) bool { return errors.As(err, &uncertain) }},
	} {
		current = tc.reply
		if _, err := client.SaveDraft(context.Background(), "baijiahao", request); !tc.check(err) {
			t.Errorf("%s：映射不对：%T %v", tc.name, err, err)
		}
	}

	// 出错说明里带了 token 也不回显。
	current = reply{400, errorBody("invalid_request", "bad token "+testRobotToken)}
	if _, err := client.SaveDraft(context.Background(), "baijiahao", request); err == nil || strings.Contains(err.Error(), testRobotToken) {
		t.Fatalf("错误信息不得带 token：%v", err)
	}

	current = reply{http.StatusOK, saved}
	if record, err := client.Draft(context.Background(), "baijiahao", "job-282.baijiahao"); err != nil || record.Status != robotDraftSaved {
		t.Fatalf("GET 应解成记录：%+v %v", record, err)
	}
	if last := seen[len(seen)-1]; last.Method != http.MethodGet || last.URL.Path != "/v1/platforms/baijiahao/drafts/job-282.baijiahao" {
		t.Fatalf("GET 路径不对：%s %s", last.Method, last.URL.Path)
	}
	current = reply{404, errorBody("not_found", "没有这个键")}
	if _, err := client.Draft(context.Background(), "baijiahao", "job-282.baijiahao"); !errors.Is(err, errRobotDraftNotFound) {
		t.Fatalf("没有这个键应是 errRobotDraftNotFound：%v", err)
	}
	current = reply{404, "not found"}
	if _, err := client.Draft(context.Background(), "baijiahao", "job-282.baijiahao"); !errors.As(err, &uncertain) {
		t.Fatalf("不是分发服务的 404（路径错、通道指错）不能当没建稿：%v", err)
	}

	server.Close()
	if _, err := client.SaveDraft(context.Background(), "baijiahao", request); !errors.As(err, &uncertain) || strings.Contains(err.Error(), testRobotToken) {
		t.Fatalf("连不上应是 uncertain：%v", err)
	}
}

// TestDistributionRobotClientDoesNotFollowRedirects 跳转不跟，免得把 token 带去别处。
func TestDistributionRobotClientDoesNotFollowRedirects(t *testing.T) {
	leaked := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization") != ""
	}))
	defer elsewhere.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/steal", http.StatusFound)
	}))
	defer server.Close()
	client := newDistributionRobotClient(distributionRobotConfig{BaseURL: server.URL, Token: testRobotToken})
	var uncertain *robotUncertainError
	if _, err := client.Draft(context.Background(), "baijiahao", "k"); !errors.As(err, &uncertain) || leaked {
		t.Fatalf("跳转不得跟随：err=%v leaked=%v", err, leaked)
	}
}
