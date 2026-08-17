package main

// publish_pipeline_test.go — 发布流水线状态机测试（施工图 S6）。
// 所有外部调用走 httptest 桩 / fake 注入，绝不触真实 API。
// 🚨 本文件锁死用户红线：翻公开（UpdateNews publishStatus=1）只能由 review approve
// 触发——FoodTalks 桩默认 forbidPublish=true，任何非 approve 路径若发出 publishStatus=1
// 立即测试失败（这条断言存在的意义：一旦翻公开，单向门 1↛0 永远回不去，自动路径误触
// 等于把未审核内容直接公开给全网）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLateCanonicalDuplicateBecomesSupersededWinnerReference(t *testing.T) {
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	winnerKey, _ := normalizeSiteSourceKey("https://mp.weixin.qq.com/s/winner")
	winner, created, err := store.create(winnerKey, "https://mp.weixin.qq.com/s/winner")
	if err != nil || !created {
		t.Fatalf("create winner: job=%+v created=%v err=%v", winner, created, err)
	}
	canonicalURL := "https://mp.weixin.qq.com/s?__biz=MzWinner&mid=7&idx=1"
	canonicalKey, _ := normalizeSiteSourceKey(canonicalURL)
	if _, conflict, err := store.claimSourceAlias(winner.ID, canonicalKey); err != nil || conflict != 0 {
		t.Fatalf("claim winner alias: conflict=%d err=%v", conflict, err)
	}
	loserKey, _ := normalizeSiteSourceKey("https://mp.weixin.qq.com/s/loser")
	loser, created, err := store.create(loserKey, "https://mp.weixin.qq.com/s/loser")
	if err != nil || !created {
		t.Fatalf("create loser: job=%+v created=%v err=%v", loser, created, err)
	}
	loser, err = store.setStage(loser.ID, publishStageFetching)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := &publishPipeline{
		store: store,
		fetch: func(context.Context, string) (string, string, string, string, string, string, error) {
			return "重复文章", "FBIF", "", canonicalURL, "", testPublishBlocksJSON, nil
		},
	}
	err = pipeline.stageFetch(context.Background(), loser)
	if !errors.Is(err, errPublishDuplicateSuperseded) {
		t.Fatalf("stageFetch error=%v", err)
	}
	got, err := store.byID(loser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != publishStageSupersededDuplicate || got.SupersededByJobID != winner.ID {
		t.Fatalf("loser did not reference winner: %+v", got)
	}
	summary := newPublishSyncJobSummary(got)
	if summary.Retryable || len(summary.AvailableActions) != 1 || summary.AvailableActions[0] != "open_winner" {
		t.Fatalf("superseded action contract = %+v", summary)
	}
}

func TestStructuredSupplierShortAliasesResolveBeforeCreatingJob(t *testing.T) {
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	canonicalURL := "https://mp.weixin.qq.com/s?__biz=MzStructured&mid=9&idx=1"
	pipeline := &publishPipeline{
		store: store,
		fetch: func(context.Context, string) (string, string, string, string, string, string, error) {
			return "身份解析", "FBIF", "", canonicalURL, "", testPublishBlocksJSON, nil
		},
		spawn: func(func()) {},
		now:   time.Now,
	}
	input := officialPublishInput{
		Title: "供应商正文", SourceName: "FBIF", ContentHTML: "<p>供应商提供的完整正文内容。</p>",
	}
	input.URL = "https://mp.weixin.qq.com/s/short-a"
	first, created, err := pipeline.submitSupplier(input)
	if err != nil || !created {
		t.Fatalf("first submit: job=%+v created=%v err=%v", first, created, err)
	}
	input.URL = "https://mp.weixin.qq.com/s/short-b"
	second, created, err := pipeline.submitSupplier(input)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("second alias must reuse winner: first=%+v second=%+v created=%v err=%v", first, second, created, err)
	}
	jobs, err := store.all()
	if err != nil || len(jobs) != 1 || jobs[0].ContentSource != publishContentSupplier {
		t.Fatalf("structured canonical jobs=%+v err=%v", jobs, err)
	}
}

func TestLateOfficialContentCanRestartFailedExistingDraft(t *testing.T) {
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	pipeline := &publishPipeline{store: store, spawn: func(func()) {}, now: time.Now}
	input := officialPublishInput{
		URL: testPublishSourceURL, Title: "供应商版本", SourceName: "FBIF",
		ContentHTML: "<p>供应商提供的正文。</p>",
	}
	job, _, err := pipeline.submitSupplier(input)
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.update(job.ID, func(current *publishJob) {
		current.Stage = publishStageFailed
		current.FailedStage = publishStageStaging
		current.FoodTalksNewsID = 88
		current.StagingIntentKey = "foodtalks-create:job-1"
	})
	if err != nil {
		t.Fatal(err)
	}
	input.Title = "官方版本"
	input.ContentHTML = "<p>官方接口提供的更高权威正文。</p>"
	updated, created, err := pipeline.submitOfficial(input)
	if err != nil || created {
		t.Fatalf("official merge: job=%+v created=%v err=%v", updated, created, err)
	}
	if updated.Stage != publishStageRendering || updated.ContentSource != publishContentOfficial ||
		updated.ContentVersion != job.ContentVersion+1 || updated.PendingContentHash != "" || updated.ContentConflict {
		t.Fatalf("failed existing draft did not restart safely: %+v", updated)
	}
}

const testPublishSourceURL = "https://mp.weixin.qq.com/s?__biz=MzTest&mid=123&idx=1&sn=test-signature"

// 测试用中立 Block[]：一段正文 + 两张 mmbiz 图（第一张兼任封面兜底）。
const testPublishBlocksJSON = `[{"k":"p","text":"正文"},{"k":"img","src":"https://mmbiz.qpic.cn/a.jpg"},{"k":"img","src":"https://mmbiz.qpic.cn/b.jpg"}]`

// 渲染 fake 的输出：正文两张 mmbiz 外链图，mirroring 必须全部转存重写。
const testPublishRenderedHTML = `<p>正文</p><img src="https://mmbiz.qpic.cn/a.jpg"><img src="https://mmbiz.qpic.cn/b.jpg">`

// ftStub FoodTalks API 的 httptest 桩：记录收到的 POST/PUT 与 publishStatus，
// 按契约返回 {"code":0,"msg":"success","data":<id>}。
type ftStub struct {
	mu             sync.Mutex
	createStatuses []int // 每次 POST 收到的 publishStatus
	updateStatuses []int // 每次 PUT 收到的 publishStatus
	updateIDs      []int64
	lastIsLatest   int
	lastSourceID   int64
	forbidPublish  bool // true 时收到 publishStatus=1 直接判失败（红线守卫）
	t              *testing.T
}

func (s *ftStub) counts() (create, update int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.createStatuses), len(s.updateStatuses)
}

// newFTStub 起 FoodTalks 桩并把凭证 env 指向它（newFoodTalksClientFromEnv 走真实客户端
// 代码，顺带验证 S3 搬入的请求构造 / 响应解析逐行保真）。
func newFTStub(t *testing.T) *ftStub {
	t.Helper()
	stub := &ftStub{forbidPublish: true, t: t}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/basic/tag/page" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{"records":[]}}`)
			return
		}
		if r.URL.Path == "/basic/admin/tag/by-name" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"code":0,"msg":"success","data":99001}`)
			return
		}
		if r.URL.Path == "/news/admin/news/page" {
			// 同题防重的标题检索：默认无同名已发布稿
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{"records":[]}}`)
			return
		}
		if r.URL.Path == "/company/companies/search" {
			// 公司库关联检索（2026-08-17）：默认无命中，不影响主链路断言
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"code":0,"msg":"success","data":{"records":[]}}`)
			return
		}
		if r.URL.Path != "/news/admin/news" {
			t.Errorf("FoodTalks 桩收到意外路径 %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		// 契约 §1 固定头必须在场（缺任一真实网关直接 403）。
		if r.Header.Get("Origin") == "" || r.Header.Get("Referer") == "" || r.Header.Get("Authorization") == "" {
			t.Errorf("FoodTalks 桩收到缺头请求 Origin=%q Referer=%q", r.Header.Get("Origin"), r.Header.Get("Referer"))
		}
		var body struct {
			ID            json.RawMessage `json:"id"`
			PublishStatus int             `json:"publishStatus"`
			IsLatest      int             `json:"isLatest"`
			SourceID      int64           `json:"sourceId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("FoodTalks 桩解析请求体失败: %v", err)
		}
		stub.mu.Lock()
		if stub.forbidPublish && body.PublishStatus == foodTalksPublishStatusPublished {
			// 🚨 红线守卫：无 approve 的路径发出了翻公开请求。
			stub.t.Errorf("红线违规：收到 publishStatus=1（%s），但本测试场景没有 review approve", r.Method)
		}
		var respID int64 = 7240
		switch r.Method {
		case http.MethodPost:
			if string(body.ID) != `""` {
				stub.t.Errorf("CreateNews 的 id 字面形态应为 \"\"，收到 %s", string(body.ID))
			}
			stub.createStatuses = append(stub.createStatuses, body.PublishStatus)
		case http.MethodPut:
			var id int64
			if err := json.Unmarshal(body.ID, &id); err != nil || id <= 0 {
				stub.t.Errorf("UpdateNews 的 id 应为正整数字面量，收到 %s", string(body.ID))
			}
			stub.updateStatuses = append(stub.updateStatuses, body.PublishStatus)
			stub.updateIDs = append(stub.updateIDs, id)
			stub.lastIsLatest = body.IsLatest
			stub.lastSourceID = body.SourceID
			respID = id
		default:
			stub.t.Errorf("FoodTalks 桩收到意外方法 %s", r.Method)
		}
		stub.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":0,"msg":"success","data":%d}`, respID)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FOODTALKS_BASE_URL", srv.URL)
	t.Setenv("FOODTALKS_TOKEN", "test-token")
	t.Setenv("FOODTALKS_ORIGIN", "https://layout.test")
	return stub
}

// publishTestEnv 状态机测试装配：fetch/render/oss 用 fake（记录调用），FoodTalks 走
// httptest 桩 + 真实客户端，spawn 同步执行保证确定性。
type publishTestEnv struct {
	pipeline   *publishPipeline
	store      *publishJobStore
	ft         *ftStub
	fetchCalls int
	ossCalls   [][]string
	// ossResults 每次 ossUpload 的应答（按调用序消费，耗尽后全量成功）。
	ossResults []map[string]string
	notified   []string
	notifyErr  error
}

func ossURLFor(src string) string {
	return "https://oss.example.com/" + strings.TrimPrefix(src, "https://mmbiz.qpic.cn/")
}

func testPublishResolvedRule() publishResolvedRule {
	return publishResolvedRule{
		AccountID:  foodTalksSiteRuleAccountID,
		PresetID:   foodTalksSiteDefaultRulePresetID,
		Revision:   7,
		ConfigHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Source:     "server",
	}
}

func successfulPublishRenderResult(html string, rule publishResolvedRule) publishRenderResult {
	return publishRenderResult{
		HTML:             html,
		ResolvedRule:     rule,
		RendererBuildSHA: "test-render-build",
		OutputHash:       publishRenderOutputHash(html),
	}
}

func newPublishTestEnv(t *testing.T) *publishTestEnv {
	t.Helper()
	t.Setenv(runtimeConfigSchemaEnv, runtimeConfigSchemaVersion)
	env := &publishTestEnv{
		store: newPublishJobStore(filepath.Join(t.TempDir(), "publish-jobs.json")),
		ft:    newFTStub(t),
	}
	env.pipeline = &publishPipeline{
		store:     env.store,
		publicDir: "../public",
		fetch: func(ctx context.Context, articleURL string) (string, string, string, string, string, string, error) {
			env.fetchCalls++
			return "测试标题", "测试公众号", "", articleURL, "", testPublishBlocksJSON, nil
		},
		render: func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
			return successfulPublishRenderResult(testPublishRenderedHTML, testPublishResolvedRule()), nil
		},
		ossUpload: func(ctx context.Context, urls []string) (map[string]string, error) {
			env.ossCalls = append(env.ossCalls, append([]string{}, urls...))
			if len(env.ossResults) > 0 {
				out := env.ossResults[0]
				env.ossResults = env.ossResults[1:]
				return out, nil
			}
			out := map[string]string{}
			for _, u := range urls {
				out[u] = ossURLFor(u)
			}
			return out, nil
		},
		foodTalks: func() (foodTalksAPI, error) { return newFoodTalksClientFromEnv() },
		notify: func(ctx context.Context, text string) error {
			env.notified = append(env.notified, text)
			return env.notifyErr
		},
		spawn:             func(f func()) { f() }, // 同步推进：测试断言无竞态
		retryNotification: func(time.Duration, func()) {},
		now:               time.Now,
	}
	return env
}

// TestPublishPipelineStopsAtAwaitingReview 自动推进的尽头是 awaiting_review：
// 草稿以 publishStatus=0 进站（外界不可见）、封面用正文第一图兜底且已 OSS 化、
// 正文外链图全部重写、发一条待审核通知。这是「先隐藏进站，人审后才见光」的产品承诺。
func TestPublishPipelineStopsAtAwaitingReview(t *testing.T) {
	env := newPublishTestEnv(t)
	job, created, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if !created {
		t.Fatalf("首次提交应建新单")
	}
	job, err = env.store.byID(job.ID)
	if err != nil {
		t.Fatalf("byID: %v", err)
	}
	if job.Stage != publishStageAwaitingReview {
		t.Fatalf("stage = %q（error=%q），应停在 awaiting_review", job.Stage, job.Error)
	}
	createN, updateN := env.ft.counts()
	if createN != 1 || env.ft.createStatuses[0] != foodTalksPublishStatusDraft {
		t.Fatalf("CreateNews 应恰一次且 publishStatus=0，实际 create=%d statuses=%v", createN, env.ft.createStatuses)
	}
	if updateN != 0 {
		t.Fatalf("无 approve 时 UpdateNews 不应被调用，实际 %d 次", updateN)
	}
	if job.FoodTalksNewsID != 7240 {
		t.Fatalf("foodtalks_news_id = %d", job.FoodTalksNewsID)
	}
	if job.CoverURL != ossURLFor("https://mmbiz.qpic.cn/a.jpg") {
		t.Fatalf("封面应为正文第一图的 OSS 转存地址，实际 %q", job.CoverURL)
	}
	if strings.Contains(job.ContentHTML, "mmbiz.qpic.cn") {
		t.Fatalf("正文仍残留 mmbiz 外链图（防盗链会让发布后丢图）：%s", job.ContentHTML)
	}
	if len(env.notified) != 1 || !strings.Contains(env.notified[0], "待审核") {
		t.Fatalf("应发一条待审核通知，实际 %v", env.notified)
	}
	if job.Title != "测试标题" || job.SourceName != "测试公众号" {
		t.Fatalf("title/source_name 落库不符: %q / %q", job.Title, job.SourceName)
	}
	if job.RulePresetID == "" || job.RuleRevision < 1 || !strings.HasPrefix(job.RuleConfigHash, "sha256:") || job.RuleSnapshotJSON != "" {
		t.Fatalf("必须记录渲染器实际使用的最新规则，且不能固化旧快照: %+v", job)
	}
	if job.RendererBuildSHA != "test-render-build" || job.RenderOutputHash != publishRenderOutputHash(testPublishRenderedHTML) {
		t.Fatalf("renderer build/output hash 未落库: build=%q output=%q", job.RendererBuildSHA, job.RenderOutputHash)
	}
}

func TestPublishRenderRetryUsesLatestRuleAtRetryTime(t *testing.T) {
	env := newPublishTestEnv(t)
	renderCalls := 0
	env.pipeline.render = func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
		renderCalls++
		return publishRenderResult{}, errors.New("temporary renderer outage")
	}

	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	failed, _ := env.store.byID(job.ID)
	if failed.Stage != publishStageFailed || failed.FailedStage != publishStageRendering || failed.RuleSnapshotJSON != "" {
		t.Fatalf("首次渲染失败不得固化旧规则快照: %+v", failed)
	}

	newRule := testPublishResolvedRule()
	newRule.Revision++
	newRule.ConfigHash = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	env.pipeline.render = func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
		renderCalls++
		return successfulPublishRenderResult(testPublishRenderedHTML, newRule), nil
	}
	if _, err := env.pipeline.retry(job.ID); err != nil {
		t.Fatal(err)
	}
	done, _ := env.store.byID(job.ID)
	if done.Stage != publishStageAwaitingReview {
		t.Fatalf("retry 后应完成到待审核，实际 %+v", done)
	}
	if renderCalls != 2 || done.RuleRevision != newRule.Revision || done.RuleConfigHash != newRule.ConfigHash ||
		done.RuleSnapshotJSON != "" {
		t.Fatalf("retry 必须使用并记录重试时的最新规则：calls=%d job=%+v", renderCalls, done)
	}
}

func TestValidatePublishRenderResultRejectsMalformedRuleHash(t *testing.T) {
	result := successfulPublishRenderResult(testPublishRenderedHTML, testPublishResolvedRule())
	result.ResolvedRule.ConfigHash = "sha256:old"
	if err := validatePublishRenderResult(result); err == nil {
		t.Fatal("不完整的规则指纹必须被拒绝")
	}
}

func TestLateOfficialContentRebuildsExistingDraftWithoutCreatingDuplicate(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	before, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldReviewVersion := publishReviewCardVersion(before)
	if before.ContentSource != publishContentWebFetch || before.ContentVersion != 1 || before.ContentFrozenVersion != 1 {
		t.Fatalf("initial content provenance=%+v", before)
	}

	official, created, err := env.pipeline.submitOfficial(officialPublishInput{
		URL: testPublishSourceURL, Title: "官方标题", SourceName: "官方公众号",
		ContentHTML: `<p>官方更正正文</p><img src="https://mmbiz.qpic.cn/a.jpg">`,
		CoverURL:    "https://mmbiz.qpic.cn/a.jpg",
	})
	if err != nil || created {
		t.Fatalf("official merge created=%v err=%v", created, err)
	}
	official, err = env.store.byID(official.ID)
	if err != nil {
		t.Fatal(err)
	}
	if official.ID != before.ID || official.FoodTalksNewsID != before.FoodTalksNewsID || official.Stage != publishStageAwaitingReview {
		t.Fatalf("official merge must reuse job/draft: before=%+v after=%+v", before, official)
	}
	if official.ContentSource != publishContentOfficial || official.ContentVersion != 2 ||
		official.ContentFrozenVersion != 2 || official.ContentHash == before.ContentHash {
		t.Fatalf("official provenance/version not advanced: before=%+v after=%+v", before, official)
	}
	if publishReviewCardVersion(official) == oldReviewVersion {
		t.Fatal("late authoritative content must invalidate the old review card version")
	}
	if createN, updateN := env.ft.counts(); createN != 1 || updateN != 1 {
		t.Fatalf("must update the existing draft, not create another: create=%d update=%d", createN, updateN)
	}
}

func TestLateOfficialContentAfterPublishIsPersistedAsConflictWithoutOverwrite(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	env.ft.mu.Lock()
	env.ft.forbidPublish = false
	env.ft.mu.Unlock()
	ready, _ := env.store.byID(job.ID)
	if _, err := env.pipeline.reviewWithRiskVersion(context.Background(), job.ID, publishReviewApprove, "ou_reviewer", publishReviewCardVersion(ready)); err != nil {
		t.Fatal(err)
	}
	published, _ := env.store.byID(job.ID)

	merged, created, err := env.pipeline.submitOfficial(officialPublishInput{
		URL: testPublishSourceURL, Title: "发布后到达的官方版本", SourceName: "官方公众号",
		ContentHTML: `<p>不能静默覆盖的正文</p>`, CoverURL: "https://mmbiz.qpic.cn/a.jpg",
	})
	if err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if merged.Stage != publishStagePublished || merged.ContentVersion != published.ContentVersion ||
		!merged.ContentConflict || merged.PendingContentSource != publishContentOfficial || merged.PendingContentHash == "" {
		t.Fatalf("late published conflict was not preserved safely: %+v", merged)
	}
	if createN, updateN := env.ft.counts(); createN != 1 || updateN != 1 {
		t.Fatalf("late conflict must not call FoodTalks: create=%d update=%d", createN, updateN)
	}
}

// TestPublishNoPublicizeWithoutApprove 🚨 红线锁死：没有 review approve，无论自动推进、
// 重复推进（模拟进程重启后误触发）、还是同文重复提交，UpdateNews 永不被调——
// 翻公开是单向门，一旦误触未审核内容直接全网可见且回不去。
func TestPublishNoPublicizeWithoutApprove(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	// 数据已齐活（blocks/HTML/news_id 全有）之后再推进多轮，也必须原地停住。
	env.pipeline.runJob(job.ID)
	env.pipeline.runJob(job.ID)
	// 同文再提交也不得触发任何新动作。
	if _, created, err := env.pipeline.submit(testPublishSourceURL); err != nil || created {
		t.Fatalf("同文重复提交应幂等返回既有任务（err=%v created=%v）", err, created)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("stage = %q，应仍停在 awaiting_review", got.Stage)
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("红线违规：无 approve 时 UpdateNews 被调了 %d 次", updateN)
	}
	// forbidPublish 守卫全程开启：任何 publishStatus=1 请求已在桩内直接判失败。
}

// TestPublishApprovePublicizes approve 是翻公开的唯一入口：UpdateNews 收到 publishStatus=1
// 且 id 是 staging 回填的锚点，任务落 published 并回填前台链接、发已发布通知。
func TestPublishApprovePublicizes(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("FOODTALKS_FRONT_URL", "") // 用默认测试环境前台
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	env.ft.mu.Lock()
	env.ft.forbidPublish = false // 本场景 approve 合法，解除守卫
	env.ft.mu.Unlock()
	accepted, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, "")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if accepted.Stage != publishStagePublicizing {
		t.Fatalf("review 返回应是已持久化的 publicizing 接受态，实际 %q", accepted.Stage)
	}
	got, _ := env.store.byID(job.ID) // 测试 spawn 同步，后台 worker 此时已跑完。
	if got.Stage != publishStagePublished {
		t.Fatalf("worker 后 stage = %q，应为 published", got.Stage)
	}
	if got.ArticleURL != "https://test.foodtalks.cn/news/7240" {
		t.Fatalf("article_url = %q", got.ArticleURL)
	}
	if _, updateN := env.ft.counts(); updateN != 1 || env.ft.updateStatuses[0] != foodTalksPublishStatusPublished || env.ft.updateIDs[0] != 7240 {
		t.Fatalf("UpdateNews 应恰一次 publishStatus=1 id=7240，实际 statuses=%v ids=%v", env.ft.updateStatuses, env.ft.updateIDs)
	}
	if len(env.notified) != 2 || !strings.Contains(env.notified[1], "已发布") {
		t.Fatalf("应追加一条已发布通知，实际 %v", env.notified)
	}
}

// TestPublishRejectIsTerminal reject 置终态：FoodTalks 侧留草稿不动（不发任何请求）、
// 之后不可再 approve；source_key 终身占用，同文重提必须返回原任务，不能新建第二篇网站文章。
func TestPublishRejectIsTerminal(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, err := env.pipeline.review(context.Background(), job.ID, publishReviewReject, "")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if got.Stage != publishStageRejected {
		t.Fatalf("stage = %q，应为 rejected", got.Stage)
	}
	if _, err := env.pipeline.review(context.Background(), job.ID, publishReviewApprove, ""); err == nil {
		t.Fatalf("reject 终态后 approve 应被拒绝")
	}
	if _, updateN := env.ft.counts(); updateN != 0 {
		t.Fatalf("reject 全程不应触发 UpdateNews（文章留在草稿态即可），实际 %d 次", updateN)
	}
	// 终态也不释放幂等键：网站文章没有安全删除/去重 API，重提不能再 CreateNews。
	if existing, created, err := env.pipeline.submit(testPublishSourceURL); err != nil || created || existing.ID != job.ID {
		t.Fatalf("终态后重新提交应返回原任务（err=%v created=%v id=%d）", err, created, existing.ID)
	}
}

// TestPublishFailedRetryResumesIdempotently failed 续跑幂等（施工图 S6）：
// blocks 不重抓（公众号临时链接会过期，重抓必失败）、已转存图不重传（幂等锚点已落库）、
// CreateNews 恰一次（网站无删除接口，重复创建即垃圾数据）。
func TestPublishFailedRetryResumesIdempotently(t *testing.T) {
	env := newPublishTestEnv(t)
	// 首轮 mirroring：b.jpg 两批都转存失败 → 任务落 failed(mirroring)，a.jpg 锚点已落库。
	env.ossResults = []map[string]string{
		{"https://mmbiz.qpic.cn/a.jpg": ossURLFor("https://mmbiz.qpic.cn/a.jpg")},
		{},
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageFailed || got.FailedStage != publishStageMirroring {
		t.Fatalf("应落 failed(mirroring)，实际 stage=%q failed_stage=%q", got.Stage, got.FailedStage)
	}
	if !strings.Contains(got.Error, "b.jpg") {
		t.Fatalf("失败原因应列出仍失败的图片 URL，实际 %q", got.Error)
	}
	if createN, _ := env.ft.counts(); createN != 0 {
		t.Fatalf("mirroring 未完成不应触发 CreateNews")
	}

	// retry：从 mirroring 续跑（spawn 是同步的，返回即推进完）。
	if _, err := env.pipeline.retry(job.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	got, _ = env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("retry 后应推进到 awaiting_review，实际 %q（error=%q）", got.Stage, got.Error)
	}
	if env.fetchCalls != 1 {
		t.Fatalf("blocks 不应重抓：fetch 调用 %d 次", env.fetchCalls)
	}
	// 第三次转存请求只应包含缺失的 b.jpg，不得重传已有锚点的 a.jpg。
	last := env.ossCalls[len(env.ossCalls)-1]
	if len(last) != 1 || last[0] != "https://mmbiz.qpic.cn/b.jpg" {
		t.Fatalf("retry 只应补传缺失图，实际 %v", last)
	}
	if createN, updateN := env.ft.counts(); createN != 1 || updateN != 0 {
		t.Fatalf("CreateNews 应恰一次且无 UpdateNews，实际 create=%d update=%d", createN, updateN)
	}
}

// TestPublishNotifyFailureDoesNotFailJob 通知只是加速器不是闸门：飞书发送失败只 WARN，
// 任务照常停在 awaiting_review 等人审，不得因通知把整单打成 failed。
func TestPublishNotifyFailureDoesNotFailJob(t *testing.T) {
	env := newPublishTestEnv(t)
	env.notifyErr = fmt.Errorf("飞书接口不可达")
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("通知失败不应影响任务，实际 stage=%q error=%q", got.Stage, got.Error)
	}
	if got.NotificationKind != publishNotificationReviewPending || got.NotificationState != publishNotificationStatePending || got.NotificationError == "" {
		t.Fatalf("通知失败必须持久留在 outbox 重试，实际 kind=%q state=%q error=%q", got.NotificationKind, got.NotificationState, got.NotificationError)
	}
}

// TestPublishRetryRejectsNonFailed retry 只服务 failed 任务：awaiting_review 等阶段
// 重试没有意义（会绕过状态机语义），必须显式拒绝。
func TestPublishRetryRejectsNonFailed(t *testing.T) {
	env := newPublishTestEnv(t)
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.pipeline.retry(job.ID); err == nil {
		t.Fatalf("非 failed 任务 retry 应报错")
	}
}

// TestNormalizeSiteSourceKey 幂等键归一化语义（照抄资讯订阅仓）：同一篇文章的各种
// 链接形态必须归一到同一个 key（否则同文重复建单），非公众号/缺定位参数必须拒绝。
func TestNormalizeSiteSourceKey(t *testing.T) {
	// 长链：易变参数（chksm/scene）剔除、参数顺序归一、http→https、host 小写。
	long1, err := normalizeSiteSourceKey("http://MP.WEIXIN.QQ.COM/s?__biz=MzA1&mid=2&idx=1&sn=abc&chksm=xyz#rd")
	if err != nil {
		t.Fatalf("long1: %v", err)
	}
	long2, err := normalizeSiteSourceKey("https://mp.weixin.qq.com/s?sn=abc&idx=1&mid=2&__biz=MzA1&scene=21")
	if err != nil {
		t.Fatalf("long2: %v", err)
	}
	if long1 != long2 {
		t.Fatalf("同文不同形态应归一同 key：%q vs %q", long1, long2)
	}
	for _, variant := range []string{
		"https://mp.weixin.qq.com/s?__biz=MzA1&mid=2&idx=1",
		"https://mp.weixin.qq.com/s?__biz=MzA1&mid=2&idx=1&sn=different-signature",
	} {
		got, err := normalizeSiteSourceKey(variant)
		if err != nil || got != long1 {
			t.Fatalf("同 biz/mid/idx 不应因 sn 缺失/变化重复建单：input=%q got=%q err=%v", variant, got, err)
		}
	}
	if !strings.HasPrefix(long1, "site:https://mp.weixin.qq.com/s?") {
		t.Fatalf("key 形态异常: %q", long1)
	}
	// 短链：/s/<slug> 本身稳定，去全部 query。
	short1, err := normalizeSiteSourceKey("https://mp.weixin.qq.com/s/AbCdEf123?from=timeline")
	if err != nil {
		t.Fatalf("short1: %v", err)
	}
	if short1 != "site:https://mp.weixin.qq.com/s/AbCdEf123" {
		t.Fatalf("短链 key = %q", short1)
	}
	for _, variant := range []string{
		"https://mp.weixin.qq.com/s/AbCdEf123",
		"https://mp.weixin.qq.com/s/AbCdEf123/",
		"https://mp.weixin.qq.com/s/AbCdEf123?__biz=other&mid=9&idx=2&sn=volatile#rd",
	} {
		got, err := normalizeSiteSourceKey(variant)
		if err != nil || got != short1 {
			t.Fatalf("短链 query/fragment/尾斜杠应归一且保留 slug 大小写：input=%q got=%q err=%v", variant, got, err)
		}
	}
	// 拒绝集：非公众号域名 / 裸 /s 无定位参数 / 非 http(s)。
	for _, bad := range []string{
		"https://example.com/s?__biz=x&mid=1&idx=1&sn=a",
		"https://mp.weixin.qq.com/s?chksm=onlyvolatile",
		"https://mp.weixin.qq.com/s?__biz=x&mid=1&sn=missing-idx",
		"https://mp.weixin.qq.com/",
		"https://mp.weixin.qq.com/not-article?__biz=x&mid=1&idx=1&sn=a",
		"https://mp.weixin.qq.com/s/a/b",
		"https://evil@mp.weixin.qq.com/s/abc",
		"https://mp.weixin.qq.com:444/s/abc",
		"ftp://mp.weixin.qq.com/s/abc",
	} {
		if _, err := normalizeSiteSourceKey(bad); err == nil {
			t.Fatalf("应拒绝非法链接 %q", bad)
		}
	}
}

func TestPublishCanonicalAliasDedupesShortAndLongLinks(t *testing.T) {
	env := newPublishTestEnv(t)
	canonical := "https://mp.weixin.qq.com/s?__biz=MzA1&mid=2&idx=1&sn=canonical-signature"
	env.pipeline.fetch = func(ctx context.Context, articleURL string) (string, string, string, string, string, string, error) {
		env.fetchCalls++
		return "同一篇文章", "测试公众号", "", canonical, "", testPublishBlocksJSON, nil
	}
	first, created, err := env.pipeline.submit("https://mp.weixin.qq.com/s/ShortAliasA")
	if err != nil || !created {
		t.Fatalf("短链首次提交失败：job=%+v created=%v err=%v", first, created, err)
	}
	second, created, err := env.pipeline.submit("https://mp.weixin.qq.com/s?idx=1&mid=2&__biz=MzA1&sn=another")
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("长链应命中短链抓取后的 canonical alias：first=%d second=%d created=%v err=%v", first.ID, second.ID, created, err)
	}
	if createN, _ := env.ft.counts(); createN != 1 {
		t.Fatalf("短链↔长链同文只能 CreateNews 一次，实际 %d", createN)
	}

	third, created, err := env.pipeline.submit("https://mp.weixin.qq.com/s/AnotherShortAlias")
	if err != nil || created || third.ID != first.ID {
		t.Fatalf("第二个短链必须在建单前解析 canonical 并命中主任务：first=%d third=%d created=%v err=%v", first.ID, third.ID, created, err)
	}
	jobs, err := env.store.all()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("不同短链同文不得留下临时失败任务：jobs=%d err=%v", len(jobs), err)
	}
	if createN, _ := env.ft.counts(); createN != 1 {
		t.Fatalf("不同短链同文也只能 CreateNews 一次，实际 %d", createN)
	}
}

func TestPublishCanonicalAliasConcurrentShortLinksCreateOneJob(t *testing.T) {
	env := newPublishTestEnv(t)
	canonical := "https://mp.weixin.qq.com/s?__biz=MzA1&mid=77&idx=1&sn=canonical"
	var fetchMu sync.Mutex
	fetchN := 0
	bothFetched := make(chan struct{})
	env.pipeline.fetch = func(ctx context.Context, articleURL string) (string, string, string, string, string, string, error) {
		fetchMu.Lock()
		fetchN++
		if fetchN == 2 {
			close(bothFetched)
		}
		fetchMu.Unlock()
		<-bothFetched
		return "并发同文", "测试公众号", "", canonical, "", testPublishBlocksJSON, nil
	}

	type result struct {
		job     publishJob
		created bool
		err     error
	}
	results := make(chan result, 2)
	for _, sourceURL := range []string{
		"https://mp.weixin.qq.com/s/ShortConcurrentA",
		"https://mp.weixin.qq.com/s/ShortConcurrentB",
	} {
		go func(sourceURL string) {
			job, created, err := env.pipeline.submit(sourceURL)
			results <- result{job: job, created: created, err: err}
		}(sourceURL)
	}
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil || first.job.ID != second.job.ID || first.created == second.created {
		t.Fatalf("并发短链应收敛为一个主任务：first=%+v second=%+v", first, second)
	}
	jobs, err := env.store.all()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("并发短链不得产生临时/失败输家：jobs=%d err=%v", len(jobs), err)
	}
	if createN, _ := env.ft.counts(); createN != 1 {
		t.Fatalf("并发短链只能 CreateNews 一次，实际 %d", createN)
	}
}

func TestPublishSourceKeyMigrationKeepsLegacySNKeyDeduped(t *testing.T) {
	store := newPublishJobStore(filepath.Join(t.TempDir(), "publish-jobs.json"))
	legacyKey := "site:https://mp.weixin.qq.com/s?__biz=MzA1&idx=1&mid=2&sn=old-signature"
	legacy, created, err := store.create(legacyKey, "https://mp.weixin.qq.com/s?__biz=MzA1&mid=2&idx=1&sn=old-signature")
	if err != nil || !created {
		t.Fatalf("准备旧 key 失败：job=%+v created=%v err=%v", legacy, created, err)
	}
	got, created, err := store.create("site:https://mp.weixin.qq.com/s?__biz=MzA1&idx=1&mid=2",
		"https://mp.weixin.qq.com/s?__biz=MzA1&mid=2&idx=1&sn=new-signature")
	if err != nil || created || got.ID != legacy.ID {
		t.Fatalf("旧 sn key 必须与新 biz/mid/idx key 视为同文：got=%+v created=%v err=%v", got, created, err)
	}
}
