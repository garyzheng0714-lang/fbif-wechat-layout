package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 网页校对的假模型：assess、verify 各回一份，可以卡住等放行。
type articleCheckFake struct {
	risk        publishRiskAssessment
	riskErr     error
	verifyRaw   string
	verifyErr   error
	assessCalls atomic.Int32
	verifyCalls atomic.Int32
	holdAssess  chan struct{}
	holdVerify  chan struct{}
	seenTitle   string
	seenBody    string
}

func newArticleCheckFake(t *testing.T, fake *articleCheckFake) *articleChecker {
	t.Helper()
	checker := newArticleChecker()
	checker.wait = 2 * time.Second
	checker.available = func(context.Context) bool { return true }
	checker.assess = func(_ context.Context, title, body string) (publishRiskAssessment, error) {
		fake.assessCalls.Add(1)
		fake.seenTitle, fake.seenBody = title, body
		if fake.holdAssess != nil {
			<-fake.holdAssess
		}
		return fake.risk, fake.riskErr
	}
	checker.verify = func(_ context.Context, _, _ string, _ []publishRiskItem) (string, error) {
		fake.verifyCalls.Add(1)
		if fake.holdVerify != nil {
			<-fake.holdVerify
		}
		return fake.verifyRaw, fake.verifyErr
	}
	return checker
}

const articleCheckBody = "如今中产买酸奶，仿佛重回重回高中生物课堂。\n有些菌种转为健身牛马打造、有些益生菌适合抗炎的时候喝。\n前面写市占率 77%，后面又写 80%。"

func articleCheckRisk() publishRiskAssessment {
	return publishRiskAssessment{Level: publishRiskWarning, Items: []publishRiskItem{
		{Category: publishRiskCategoryIntegrity, Level: publishRiskWarning, Certainty: publishRiskCertaintyConfirmed,
			Evidence: "仿佛重回重回高中生物课堂", Reason: "「重回」叠字。"},
		{Category: publishRiskCategoryNumber, Level: publishRiskWarning, Certainty: publishRiskCertaintyConfirmed,
			Evidence: "前面写市占率 77%，后面又写 80%。", Reason: "同一个市占率前后两个数，必须二选一。"},
		{Category: publishRiskCategoryNaming, Level: publishRiskWarning, Certainty: publishRiskCertaintyUnverified,
			Evidence: "有些菌种转为健身牛马打造", Reason: "按我了解的情况可能另有所指，请核实。"},
		{Category: publishRiskCategoryCompliance, Level: publishRiskBlock, Certainty: publishRiskCertaintyUnverified,
			Evidence: "有些益生菌适合抗炎的时候喝", Reason: "普通食品写「抗炎」，《食品安全法》第七十三条。"},
	}}
}

// 同一套流程：初检 → 挑出确定条目 → 复核出唯一改法；改法唯一的进 fixes，其余确定条目和阻断级进 issues，拿不准的进 notes。
func TestArticleCheckRunsRiskThenProofread(t *testing.T) {
	fake := &articleCheckFake{
		risk:      articleCheckRisk(),
		verifyRaw: `{"items":[{"index":1,"definite":true,"wrong":"重回重回","correct":"重回"},{"index":2,"definite":false}]}`,
	}
	checker := newArticleCheckFake(t, fake)
	view, err := checker.check(context.Background(), "u1", "标题", articleCheckBody, articleCheckVerifying, false)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if view.Status != articleCheckDone {
		t.Fatalf("status = %q, want done", view.Status)
	}
	if fake.seenTitle != "标题" || fake.seenBody != articleCheckBody {
		t.Fatalf("模型看到的不是交上来的标题和正文：%q / %q", fake.seenTitle, fake.seenBody)
	}
	if len(view.Fixes) != 1 || view.Fixes[0].Wrong != "重回重回" || view.Fixes[0].Correct != "重回" ||
		view.Fixes[0].Evidence != "仿佛重回重回高中生物课堂" {
		t.Fatalf("fixes = %+v", view.Fixes)
	}
	// 阻断级排最前；已经给出改法的那条不再列在要核对里。
	if len(view.Issues) != 2 || view.Issues[0].Level != publishRiskBlock || view.Issues[0].Label != "合规提示" ||
		view.Issues[1].Label != "数字与口径" {
		t.Fatalf("issues = %+v", view.Issues)
	}
	if len(view.Notes) != 1 || view.Notes[0].Label != "名称与身份" {
		t.Fatalf("notes = %+v", view.Notes)
	}
}

// 没有确定条目就不复核：一次模型调用就完。
func TestArticleCheckSkipsVerifyWithoutCandidates(t *testing.T) {
	fake := &articleCheckFake{risk: publishRiskAssessment{Level: publishRiskPass}}
	checker := newArticleCheckFake(t, fake)
	view, err := checker.check(context.Background(), "u1", "", "一篇没毛病的稿子。", "", false)
	if err != nil || view.Status != articleCheckDone {
		t.Fatalf("view = %+v err = %v", view, err)
	}
	if fake.verifyCalls.Load() != 0 {
		t.Fatal("没有候选也调了复核")
	}
	if len(view.Fixes) != 0 || len(view.Issues) != 0 || len(view.Notes) != 0 {
		t.Fatalf("干净的稿子不该有条目：%+v", view)
	}
	raw, _ := json.Marshal(view)
	if !strings.Contains(string(raw), `"fixes":[]`) || !strings.Contains(string(raw), `"issues":[]`) || !strings.Contains(string(raw), `"notes":[]`) {
		t.Fatalf("空列表要回 []，网页不用判空：%s", raw)
	}
}

// 同一份内容只查一次：再交接上同一个结果，谁交都一样。
func TestArticleCheckSameContentChecksOnce(t *testing.T) {
	fake := &articleCheckFake{risk: publishRiskAssessment{Level: publishRiskPass}}
	checker := newArticleCheckFake(t, fake)
	for _, user := range []string{"u1", "u2", "u1"} {
		view, err := checker.check(context.Background(), user, "标题", "同一篇。", "", false)
		if err != nil || view.Status != articleCheckDone {
			t.Fatalf("view = %+v err = %v", view, err)
		}
	}
	if got := fake.assessCalls.Load(); got != 1 {
		t.Fatalf("同一份内容查了 %d 次", got)
	}
	if _, err := checker.check(context.Background(), "u1", "标题", "改过一个字。", "", false); err != nil {
		t.Fatal(err)
	}
	if got := fake.assessCalls.Load(); got != 2 {
		t.Fatalf("内容变了要重查，实际查了 %d 次", got)
	}
}

// 长等待：没出新结果等满就回当前这一步；初检一完带着条目回；带上 have 接着等到复核完。
func TestArticleCheckLongWaitReturnsEachStep(t *testing.T) {
	fake := &articleCheckFake{
		risk:       articleCheckRisk(),
		verifyRaw:  `{"items":[{"index":1,"definite":true,"wrong":"重回重回","correct":"重回"}]}`,
		holdAssess: make(chan struct{}),
		holdVerify: make(chan struct{}),
	}
	checker := newArticleCheckFake(t, fake)
	checker.wait = 80 * time.Millisecond

	view, _ := checker.check(context.Background(), "u1", "标题", articleCheckBody, "", false)
	if view.Status != articleCheckChecking {
		t.Fatalf("初检没完应回 checking，得到 %q", view.Status)
	}

	checker.wait = 2 * time.Second
	close(fake.holdAssess)
	view, _ = checker.check(context.Background(), "u1", "标题", articleCheckBody, "", false)
	if view.Status != articleCheckVerifying || len(view.Issues) != 3 || len(view.Fixes) != 0 {
		t.Fatalf("初检完应先把条目给出来：%+v", view)
	}

	done := make(chan articleCheckView, 1)
	go func() {
		v, _ := checker.check(context.Background(), "u1", "标题", articleCheckBody, articleCheckVerifying, false)
		done <- v
	}()
	select {
	case v := <-done:
		t.Fatalf("复核没完不该回：%+v", v)
	case <-time.After(60 * time.Millisecond):
	}
	close(fake.holdVerify)
	view = <-done
	if view.Status != articleCheckDone || len(view.Fixes) != 1 || len(view.Issues) != 2 {
		t.Fatalf("复核完的结果不对：%+v", view)
	}
	if fake.assessCalls.Load() != 1 || fake.verifyCalls.Load() != 1 {
		t.Fatalf("接着等不该重查：assess %d verify %d", fake.assessCalls.Load(), fake.verifyCalls.Load())
	}
}

// 没查成：不把内部错误给编辑看；一分钟内再交不会自己重查，点重试才重查。
func TestArticleCheckFailureNeedsExplicitRetry(t *testing.T) {
	fake := &articleCheckFake{riskErr: errors.New("MiMo 调用失败（HTTP 500）: secret upstream detail")}
	checker := newArticleCheckFake(t, fake)
	view, err := checker.check(context.Background(), "u1", "", "一篇稿子。", "", false)
	if err != nil || view.Status != articleCheckFailed {
		t.Fatalf("view = %+v err = %v", view, err)
	}
	if strings.Contains(view.Error, "MiMo") || strings.Contains(view.Error, "secret") || view.Error == "" {
		t.Fatalf("给编辑的话不该带内部细节：%q", view.Error)
	}
	if view, _ = checker.check(context.Background(), "u1", "", "一篇稿子。", "", false); view.Status != articleCheckFailed {
		t.Fatalf("再交应还是没查成：%+v", view)
	}
	if got := fake.assessCalls.Load(); got != 1 {
		t.Fatalf("没点重试就重查了：%d 次", got)
	}
	fake.riskErr = nil
	view, _ = checker.check(context.Background(), "u1", "", "一篇稿子。", "", true)
	if view.Status != articleCheckDone || fake.assessCalls.Load() != 2 {
		t.Fatalf("点重试应重查：%+v（%d 次）", view, fake.assessCalls.Load())
	}
}

// 复核没成整次算没查成，不把缺了改法的半份结果当查完留两小时。
func TestArticleCheckVerifyFailureFailsWholeCheck(t *testing.T) {
	fake := &articleCheckFake{risk: articleCheckRisk(), verifyRaw: "模型没按格式回"}
	checker := newArticleCheckFake(t, fake)
	view, _ := checker.check(context.Background(), "u1", "标题", articleCheckBody, articleCheckVerifying, false)
	if view.Status != articleCheckFailed {
		t.Fatalf("status = %q, want failed", view.Status)
	}
}

// 每人新开检查有频率上限；再交同一份不算。
func TestArticleCheckLimitsNewChecksPerUser(t *testing.T) {
	fake := &articleCheckFake{risk: publishRiskAssessment{Level: publishRiskPass}}
	checker := newArticleCheckFake(t, fake)
	checker.starts = newRequestCostGuard(2, time.Minute, 8)
	for i, text := range []string{"第一篇。", "第二篇。", "第一篇。"} {
		if _, err := checker.check(context.Background(), "u1", "", text, "", false); err != nil {
			t.Fatalf("第 %d 次不该被限：%v", i+1, err)
		}
	}
	if _, err := checker.check(context.Background(), "u1", "", "第三篇。", "", false); !errors.Is(err, errArticleCheckTooFrequent) {
		t.Fatalf("第三篇应被限，err = %v", err)
	}
	if _, err := checker.check(context.Background(), "u2", "", "第三篇。", "", false); err != nil {
		t.Fatalf("别人不受影响：%v", err)
	}
}

// 结果留 2 小时，过了重查；没查成的只留一分钟。
func TestArticleCheckDropsExpiredResults(t *testing.T) {
	fake := &articleCheckFake{risk: publishRiskAssessment{Level: publishRiskPass}}
	checker := newArticleCheckFake(t, fake)
	now := time.Unix(1_800_000_000, 0)
	checker.now = func() time.Time { return now }
	check := func() {
		t.Helper()
		if view, err := checker.check(context.Background(), "u1", "", "一篇稿子。", "", false); err != nil || view.Status != articleCheckDone {
			t.Fatalf("view = %+v err = %v", view, err)
		}
	}
	check()
	now = now.Add(articleCheckKeep - time.Second)
	check()
	if got := fake.assessCalls.Load(); got != 1 {
		t.Fatalf("没过期就重查了：%d 次", got)
	}
	now = now.Add(2 * time.Second)
	check()
	if got := fake.assessCalls.Load(); got != 2 {
		t.Fatalf("过期应重查：%d 次", got)
	}
}

// 模型用不了（没配密钥）：不开查，照常回 200、状态 off，网页据此不出校对卡。
func TestArticleCheckUnavailableWithoutModel(t *testing.T) {
	auth := newTestAuthService(t, nil)
	userID := mustSessionForUser(t, auth, authUser{ID: "account:editor", Name: "编辑", Status: authUserActive})
	fake := &articleCheckFake{risk: publishRiskAssessment{Level: publishRiskPass}}
	checker := newArticleCheckFake(t, fake)
	checker.available = func(context.Context) bool { return false }
	mux := http.NewServeMux()
	registerArticleCheckRoute(mux, auth, checker)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, articleCheckRequest(userID, `{"text":"一篇稿子。"}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"off"`) {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if fake.assessCalls.Load() != 0 {
		t.Fatal("模型用不了不该开查")
	}

	// 网页加载时先问一句能不能用：没登录不答，登录了照实说。
	probe := func(userID string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, requestWithSession(http.MethodGet, "/api/article-check", userID, nil))
		return rec
	}
	if rec := probe(""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("没登录问能不能用应 401，得到 %d", rec.Code)
	}
	if rec := probe(userID); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	checker.available = func(context.Context) bool { return true }
	if rec := probe(userID); !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func articleCheckRequest(userID, body string) *http.Request {
	return requestWithSession(http.MethodPost, "/api/article-check", userID, []byte(body))
}

// 接口只给登录的同事在网页上用：没登录 401、跨站 403、没正文 400、太长 413；不建发布单。
func TestArticleCheckRouteRequiresSessionAndBody(t *testing.T) {
	auth := newTestAuthService(t, nil)
	userID := mustSessionForUser(t, auth, authUser{ID: "account:editor", Name: "编辑", Status: authUserActive})
	fake := &articleCheckFake{risk: publishRiskAssessment{Level: publishRiskPass}}
	mux := http.NewServeMux()
	registerArticleCheckRoute(mux, auth, newArticleCheckFake(t, fake))
	serve := func(req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := serve(articleCheckRequest("", `{"text":"正文"}`)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("没登录应 401，得到 %d", rec.Code)
	}
	crossSite := articleCheckRequest(userID, `{"text":"正文"}`)
	crossSite.Header.Set("Origin", "https://evil.example")
	if rec := serve(crossSite); rec.Code != http.StatusForbidden {
		t.Fatalf("跨站应 403，得到 %d", rec.Code)
	}
	if rec := serve(articleCheckRequest(userID, `{"title":"只有标题","text":"  "}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("没正文应 400，得到 %d", rec.Code)
	}
	huge := `{"text":"` + strings.Repeat("字", articleCheckMaxBody/3+1) + `"}`
	if rec := serve(articleCheckRequest(userID, huge)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("太长应 413（不截断），得到 %d", rec.Code)
	}
	if fake.assessCalls.Load() != 0 {
		t.Fatal("被拒的请求不该调模型")
	}

	rec := serve(articleCheckRequest(userID, `{"title":" 标题 ","text":"\n一篇稿子。\n"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var view articleCheckView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil || view.Status != articleCheckDone {
		t.Fatalf("view = %+v err = %v", view, err)
	}
	if fake.seenTitle != "标题" || fake.seenBody != "一篇稿子。" {
		t.Fatalf("标题正文应去掉首尾空白：%q / %q", fake.seenTitle, fake.seenBody)
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("结果不该被缓存：%q", rec.Header().Get("Cache-Control"))
	}
}

// 与发布流水线共用同一份提示词输入：标题、正文、当天日期、注入隔离标记一样。
func TestArticleCheckSharesRiskInputWithPipeline(t *testing.T) {
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	job := publishJob{Title: "标题", ContentHTML: "<p>第一段</p><p>第二段</p>"}
	body, viaJob := buildRiskUserInput(job, now)
	if body != "第一段\n第二段" {
		t.Fatalf("body = %q", body)
	}
	if viaText := riskUserInput("标题", "第一段\n第二段", now); viaText != viaJob {
		t.Fatalf("两个入口拼出的输入不一样：\n%s\n---\n%s", viaJob, viaText)
	}
}
