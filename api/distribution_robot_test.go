package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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

// TestDistributionRobotClientMapsResponses 分发服务每种回答映射成哪类错误（契约见分发服务 docs/api.md，每个错误码的状态和 outcome 照 testdata/distribution-api.json）：
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
		`"match":true,"mismatches":[],"read_back_error":null,"replayed":false,"attempts":1,` +
		`"assists":[{"step":"cover.open","goal":"打开换封面的弹窗","wanted":["选择封面","更换"],"picked":"替换封面","reason":"设置封面那一行的按钮"}]}`
	request := robotDraftRequest{IdempotencyKey: "job-282.baijiahao", Title: "标题", Blocks: []Block{{K: "txt", Runs: []Run{{Type: "txt", Text: "正文"}}}}, CoverURL: "https://cdn.example.com/a.jpg"}

	current = reply{http.StatusOK, saved}
	record, err := client.SaveDraft(context.Background(), "baijiahao", request)
	if err != nil || record.ArticleID != "1879718514554909812" || record.ReadBack == nil || record.ReadBack.Captions != 1 || record.Match == nil || !*record.Match {
		t.Fatalf("成功响应应解成完整记录：%+v %v", record, err)
	}
	if len(record.Assists) != 1 || record.Assists[0].Step != "cover.open" || record.Assists[0].Picked != "替换封面" ||
		strings.Join(record.Assists[0].Wanted, "/") != "选择封面/更换" {
		t.Fatalf("模型兜底的记录应解出来：%+v", record.Assists)
	}
	last := seen[len(seen)-1]
	if last.Method != http.MethodPost || last.URL.Path != "/v1/platforms/baijiahao/drafts" || last.Header.Get("Authorization") != "Bearer "+testRobotToken {
		t.Fatalf("请求不对：%s %s auth=%q", last.Method, last.URL.Path, last.Header.Get("Authorization"))
	}
	var sent robotDraftRequest
	if err := json.Unmarshal([]byte(bodies[len(bodies)-1]), &sent); err != nil || !reflect.DeepEqual(sent, request) ||
		bodies[len(bodies)-1] != `{"idempotency_key":"job-282.baijiahao","account":"","title":"标题","blocks":[{"k":"txt","runs":[{"type":"txt","text":"正文"}]}],"cover_url":"https://cdn.example.com/a.jpg"}` {
		t.Fatalf("请求体应是契约里的四个字段，正文是外发通用版正文块：%s", bodies[len(bodies)-1])
	}

	// 写草稿可能回的每个错误码，照分发服务给的接口契约（状态、outcome）一个个过：没动手的拒、等人的对账、说不准的按同一个键查。
	var rejected *robotRejectedError
	var reconcile *robotReconcileError
	var uncertain *robotUncertainError
	contract := loadDistributionAPIContract(t)
	codes := contract.routeErrors(http.MethodPost, "/v1/platforms/{platform}/drafts")
	if len(codes) < 10 {
		t.Fatalf("接口契约里写草稿的错误码太少，testdata/%s 不对：%v", distributionAPIFile, codes)
	}
	for _, code := range codes {
		spec := contract.Errors[code]
		current = reply{spec.Status, `{"error":{"code":"` + code + `","message":"说明","outcome":"` + spec.Outcome + `"}}`}
		_, err := client.SaveDraft(context.Background(), "baijiahao", request)
		ok := false
		switch spec.Outcome {
		case robotOutcomeRejected:
			ok = errors.As(err, &rejected) && rejected.Status == spec.Status && rejected.Code == code
		case robotOutcomeHuman:
			ok = errors.As(err, &reconcile) && reconcile.Message == "说明"
		case robotOutcomeUncertain:
			ok = errors.As(err, &uncertain)
		default:
			t.Fatalf("契约里 %s 的 outcome %q 不认得：分发服务加了新的 outcome，这里要先接上", code, spec.Outcome)
		}
		if !ok {
			t.Errorf("%d %s（%s）映射不对：%T %v", spec.Status, code, spec.Outcome, err, err)
		}
	}
	for _, tc := range []struct {
		name  string
		reply reply
	}{
		{"没带 outcome（不是分发服务回的）", reply{400, errorBody("invalid_request", "x")}},
		{"500 不是 JSON", reply{500, "oops"}},
		{"200 但还没存好", reply{200, `{"status":"saving"}`}},
		{"200 但不是 JSON", reply{200, "<html>"}},
		{"200 已存但没编号", reply{200, `{"status":"saved","article_id":""}`}},
	} {
		current = tc.reply
		if _, err := client.SaveDraft(context.Background(), "baijiahao", request); !errors.As(err, &uncertain) {
			t.Errorf("%s：应按说不准、用同一个键查：%T %v", tc.name, err, err)
		}
	}

	// 出错说明里带了 token 也不回显。
	current = reply{400, `{"error":{"code":"invalid_request","message":"bad token ` + testRobotToken + `","outcome":"rejected"}}`}
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

// TestDistributionRobotClientPublish 发布（2026-10-30 起草稿存好就调）：POST …/drafts/{key}/publish，请求体只有 actor；
// 200、202（还在点）都解成记录；发布可能回的每个错误码照接口契约映射，错误里带的记录（分发服务记下的那次发布）留在错误上。
func TestDistributionRobotClientPublish(t *testing.T) {
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
	const key = "job-282.recTestBaijia001.abc"
	const articleURL = "https://baijiahao.baidu.com/s?id=1879718514554909812"

	current = reply{http.StatusOK, `{"status":"saved","article_id":"1879718514554909812","draft_url":"https://baijiahao.baidu.com/builder/rc/edit?type=news&article_id=1879718514554909812",` +
		`"state":{"value":"published","raw":"已发布","reason":"","url":"` + articleURL + `"},"article_url":"` + articleURL + `",` +
		`"revision":{"hash":"sha256:abc","title":"标题","chars":1200,"images":3},` +
		`"action":{"id":"act-1","kind":"publish","status":"done","outcome":"by_us","actor":"ou_reviewer_1","evidence":"文章列表里已发布","error":""}}`}
	record, err := client.Publish(context.Background(), "baijiahao", key, "ou_reviewer_1")
	if err != nil || record.State == nil || record.State.Value != robotStatePublished || record.ArticleURL != articleURL ||
		record.Action == nil || record.Action.ID != "act-1" || record.Action.Status != robotActionDone || record.Action.Kind != robotActionPublish ||
		record.Revision == nil || record.Revision.Hash != "sha256:abc" {
		t.Fatalf("发布成功应解出平台现状、文章链接与这次发布：%+v %v", record, err)
	}
	last := seen[len(seen)-1]
	if last.Method != http.MethodPost || last.URL.Path != "/v1/platforms/baijiahao/drafts/"+key+"/publish" ||
		last.Header.Get("Authorization") != "Bearer "+testRobotToken || bodies[len(bodies)-1] != `{"actor":"ou_reviewer_1"}` {
		t.Fatalf("发布请求不对：%s %s auth=%q body=%s", last.Method, last.URL.Path, last.Header.Get("Authorization"), bodies[len(bodies)-1])
	}

	current = reply{http.StatusAccepted, `{"status":"saved","article_id":"1879718514554909812","action":{"id":"act-2","kind":"publish","status":"doing"}}`}
	if record, err := client.Publish(context.Background(), "baijiahao", key, "system:auto_distribute"); err != nil || record.Action == nil || record.Action.Status != robotActionDoing {
		t.Fatalf("202 是还在点，照样解成记录：%+v %v", record, err)
	}

	var rejected *robotRejectedError
	var reconcile *robotReconcileError
	var uncertain *robotUncertainError
	contract := loadDistributionAPIContract(t)
	codes := contract.routeErrors(http.MethodPost, "/v1/platforms/{platform}/drafts/{key}/publish")
	if len(codes) < 10 {
		t.Fatalf("接口契约里发布的错误码太少，testdata/%s 不对：%v", distributionAPIFile, codes)
	}
	kept := `"record":{"status":"saved","article_id":"1","action":{"id":"act-9","kind":"publish","status":"unknown"}}`
	for _, code := range codes {
		spec := contract.Errors[code]
		current = reply{spec.Status, `{"error":{"code":"` + code + `","message":"说明","outcome":"` + spec.Outcome + `"},` + kept + `}`}
		_, err := client.Publish(context.Background(), "baijiahao", key, "ou_reviewer_1")
		var record *robotDraftRecord
		switch spec.Outcome {
		case robotOutcomeRejected:
			if errors.As(err, &rejected) && rejected.Status == spec.Status && rejected.Code == code {
				record = rejected.Record
			}
		case robotOutcomeHuman:
			if errors.As(err, &reconcile) && reconcile.Code == code && reconcile.Message == "说明" {
				record = reconcile.Record
			}
		case robotOutcomeUncertain:
			if errors.As(err, &uncertain) {
				record = uncertain.Record
			}
		default:
			t.Fatalf("契约里 %s 的 outcome %q 不认得：分发服务加了新的 outcome，这里要先接上", code, spec.Outcome)
		}
		if record == nil || record.Action == nil || record.Action.ID != "act-9" || record.Action.Status != robotActionUnknown {
			t.Errorf("%d %s（%s）映射不对或丢了记录：%T %v", spec.Status, code, spec.Outcome, err, err)
		}
	}
	current = reply{409, `{"error":{"code":"busy","message":"这篇正在做别的","outcome":"rejected"}}`}
	if _, err := client.Publish(context.Background(), "baijiahao", key, "ou_reviewer_1"); !errors.As(err, &rejected) || rejected.Record != nil {
		t.Fatalf("没带记录的错误，Record 为空：%T %v", err, err)
	}
	for _, tc := range []struct {
		name  string
		reply reply
	}{
		{"500 不是 JSON", reply{500, "oops"}},
		{"200 但不是 JSON", reply{200, "<html>"}},
		{"没带 outcome（不是分发服务回的）", reply{409, `{"error":{"code":"busy","message":"x"}}`}},
	} {
		current = tc.reply
		if _, err := client.Publish(context.Background(), "baijiahao", key, "ou_reviewer_1"); !errors.As(err, &uncertain) {
			t.Errorf("%s：应按说不准、用同一个键查：%T %v", tc.name, err, err)
		}
	}
	server.Close()
	if _, err := client.Publish(context.Background(), "baijiahao", key, "ou_reviewer_1"); !errors.As(err, &uncertain) || strings.Contains(err.Error(), testRobotToken) {
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

const distributionAPIFile = "distribution-api.json"

// distributionAPIContract 分发服务的接口契约（它那边 npm run gen:docs 生成 test/fixtures/api.json，拷一份在 testdata）。
type distributionAPIContract struct {
	Outcomes []string `json:"outcomes"`
	Errors   map[string]struct {
		Status  int    `json:"status"`
		Outcome string `json:"outcome"`
	} `json:"errors"`
	Routes []struct {
		Method string   `json:"method"`
		Path   string   `json:"path"`
		Errors []string `json:"errors"`
	} `json:"routes"`
}

func loadDistributionAPIContract(t *testing.T) distributionAPIContract {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", distributionAPIFile))
	if err != nil {
		t.Fatal(err)
	}
	var c distributionAPIContract
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func (c distributionAPIContract) routeErrors(method, path string) []string {
	for _, r := range c.Routes {
		if r.Method == method && r.Path == path {
			return r.Errors
		}
	}
	return nil
}
