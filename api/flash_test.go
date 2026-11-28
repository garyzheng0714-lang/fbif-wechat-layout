package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- 假的资讯订阅、模型、FoodTalks、卡片 ----

type fakeFlashSite struct {
	mu      sync.Mutex
	flashes []foodTalksFlash
	created []foodTalksNewFlash
	nextID  int64
	news    []foodTalksNewsListItem
	failGet bool
}

func (s *fakeFlashSite) CreateFlash(_ context.Context, f foodTalksNewFlash) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := 1000 + s.nextID
	s.created = append(s.created, f)
	status := 0
	if f.Publish {
		status = 1
	}
	s.flashes = append(s.flashes, foodTalksFlash{ID: id, Title: f.Title, Content: f.Content, SourceURL: f.SourceURL, PublishStatus: status,
		PublishTime: time.Now().In(beijing).Format("2006-01-02 15:04:05")})
	return id, nil
}

func (s *fakeFlashSite) GetFlash(_ context.Context, id int64) (foodTalksFlash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failGet {
		return foodTalksFlash{}, errors.New("读回超时")
	}
	for _, f := range s.flashes {
		if f.ID == id {
			return f, nil
		}
	}
	return foodTalksFlash{}, errors.New("没有")
}

func (s *fakeFlashSite) SearchFlashes(_ context.Context, title string, _ int) ([]foodTalksFlash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []foodTalksFlash
	for i := len(s.flashes) - 1; i >= 0; i-- { // 和真接口一样新的在前
		if f := s.flashes[i]; title == "" || strings.Contains(f.Title, title) {
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *fakeFlashSite) BanFlash(context.Context, int64) error { return nil }

func (s *fakeFlashSite) RecentNewsTitles(context.Context, int) ([]foodTalksNewsListItem, error) {
	return s.news, nil
}

type fakeFlashCards struct {
	mu      sync.Mutex
	sent    []map[string]any
	updated []map[string]any
}

func (c *fakeFlashCards) chatID() string { return "oc_flash_test" }
func (c *fakeFlashCards) send(_ context.Context, card map[string]any) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, card)
	return "om_flash_card_1", nil
}
func (c *fakeFlashCards) update(_ context.Context, _ string, card map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updated = append(c.updated, card)
	return nil
}

const flashTestDraftJSON = `{"skip":"","duplicate_of":"","title":"乐乐茶全线降价超30%","body":["11月27日，据界面新闻报道，乐乐茶宣布全线产品降价，平均降幅超过30%，调整后多数饮品价格在10元至15元之间。这是乐乐茶今年以来规模最大的一次价格调整，涉及门店菜单上的全部现制茶饮。","据了解，此次调价覆盖乐乐茶全国门店，包括招牌芝士茶、鲜果茶等系列产品。乐乐茶方面表示，调价后将同步上线小程序点单优惠，会员可叠加使用积分抵扣。","乐乐茶成立于2016年，总部位于上海，目前在全国拥有门店一百余家。"],"source":{"name":"界面新闻","title":"开打价格战？乐乐茶全线降价超30%","url":"https://example.com/a1","published_at":"2026-11-27T10:00:00+08:00"},"hints":[]}`

func flashTestSource(id string) flashSourceDoc {
	return flashSourceDoc{
		Article: flashSourceArticle{ID: id, URL: "https://example.com/a1", Title: "开打价格战？乐乐茶全线降价超30%",
			PublishedAt: "2026-11-27T10:00:00+08:00", SourceName: "界面新闻", Text: "乐乐茶全线降价超30%……"},
		Judgment: &flashSourceJudgment{Genre: "news", Flags: []string{"newsy"}, SiteTier: "publish"},
		Event:    &flashSourceEvent{ID: "ev-" + id, Title: "乐乐茶降价", SourceCount: 1},
	}
}

type flashTestEnv struct {
	p     *flashPipeline
	site  *fakeFlashSite
	cards *fakeFlashCards
	ai    func(system, user string) (string, error)
	calls int
	mu    sync.Mutex
	src   map[string]flashSourceDoc
}

func newFlashTestEnv(t *testing.T) *flashTestEnv {
	t.Helper()
	env := &flashTestEnv{site: &fakeFlashSite{}, cards: &fakeFlashCards{}, src: map[string]flashSourceDoc{}}
	store := newFlashJobStore(t.TempDir() + "/flash-jobs.json")
	p := newFlashPipeline(store)
	p.source = func(_ context.Context, id string) (flashSourceDoc, error) {
		env.mu.Lock()
		defer env.mu.Unlock()
		doc, ok := env.src[id]
		if !ok {
			return flashSourceDoc{}, errFlashSourceNotFound
		}
		return doc, nil
	}
	p.spec = func(context.Context) (flashSpec, error) {
		return flashSpec{Revision: 1117, Text: "规范全文", Avoid: []flashSpecPhrase{{Avoid: "重磅推出", Instead: "推出、发布"}}}, nil
	}
	env.ai = func(system, user string) (string, error) {
		if strings.HasPrefix(system, strings.TrimSpace(flashVerifyTask)[:20]) {
			return `{"unsupported":[]}`, nil
		}
		return flashTestDraftJSON, nil
	}
	p.writer = flashWriter{now: time.Now, ai: func(_ context.Context, system, user string) (string, error) {
		env.mu.Lock()
		env.calls++
		env.mu.Unlock()
		return env.ai(system, user)
	}}
	p.risk = func(context.Context, string, string, string) (publishRiskAssessment, error) {
		return publishRiskAssessment{Level: publishRiskPass, Headline: "没发现问题"}, nil
	}
	p.site = func() (foodTalksFlashAPI, error) { return env.site, nil }
	p.cards = env.cards
	p.frontURL = "https://test.foodtalks.cn"
	t.Cleanup(p.stop)
	env.p = p
	return env
}

func waitFlashStage(t *testing.T, p *flashPipeline, id int64, stage string) flashJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, err := p.store.byID(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Stage == stage {
			p.mu.Lock()
			running := p.running[id]
			p.mu.Unlock()
			if !running {
				return job
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("快讯 #%d 等不到 %s，现在是 %s（%s）", id, stage, job.Stage, job.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ---- 走一遍：收下 → 写 → 查 → 出卡 → 通过 → 发到 FoodTalks → 状态推回 ----

func TestFlashPipelineWritesChecksCardsAndPublishesOnApprove(t *testing.T) {
	env := newFlashTestEnv(t)
	env.src["a1"] = flashTestSource("a1")
	job, existing, err := env.p.submit("a1", "https://example.com/a1", "")
	if err != nil || existing {
		t.Fatalf("submit: %v existing=%v", err, existing)
	}
	job = waitFlashStage(t, env.p, job.ID, flashStageAwaitingReview)
	if job.Draft == nil || job.Draft.Title != "乐乐茶全线降价超30%" || len(job.Draft.Body) != 3 {
		t.Fatalf("稿子不对：%+v", job.Draft)
	}
	if job.Draft.Source.Name != "界面新闻" || job.Draft.Source.PublishedAt != "2026-11-27 10:00" {
		t.Fatalf("来源不对：%+v", job.Draft.Source)
	}
	if job.Checks == nil || job.Checks.Version != 1 || job.Checks.Risk.Level != publishRiskPass || job.SpecRevision != 1117 {
		t.Fatalf("检查不对：%+v spec=%d", job.Checks, job.SpecRevision)
	}
	if job.CardMessageID != "om_flash_card_1" || len(env.cards.sent) != 1 {
		t.Fatalf("应该发一张卡：%q %d", job.CardMessageID, len(env.cards.sent))
	}
	if _, err := env.p.approve(job.ID, 2, "ou_test"); err == nil {
		t.Fatal("旧版本的卡不该能通过")
	}
	if _, err := env.p.approve(job.ID, 1, "ou_test"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	job = waitFlashStage(t, env.p, job.ID, flashStagePublished)
	if job.FlashID != 1001 || job.FlashURL != "https://test.foodtalks.cn/flash/1001" || job.ReviewedBy != "ou_test" {
		t.Fatalf("发布结果不对：%+v", job)
	}
	if len(env.site.created) != 1 || !env.site.created[0].Publish || env.site.created[0].SourceURL != "https://example.com/a1" {
		t.Fatalf("FoodTalks 收到的不对：%+v", env.site.created)
	}
	if !strings.Contains(env.site.created[0].Content, "</p>\n<p>&nbsp;</p>\n<p>") {
		t.Fatalf("正文要照人工发的样子段间隔空段：%s", env.site.created[0].Content)
	}
	if _, existing, _ := env.p.submit("a1", "", ""); !existing {
		t.Fatal("同一篇再交应该回已有的")
	}
}

// 建快讯断在中间（记了尝试、没记上编号）：下一次按标题找到点通过之后建的那条就认，不建第二条；
// 别的任务早先发过的同题旧快讯不认（2026-11-29 试跑第三遍，测试环境上同题的有第一遍留下的一条）。
func TestFlashPublishAdoptsSameTitleAfterUncertainCreate(t *testing.T) {
	env := newFlashTestEnv(t)
	store := env.p.store
	draft := flashDraft{Title: "乐乐茶全线降价超30%", Body: []string{"甲", "乙"}}
	intent := time.Now().Add(-time.Minute)
	bj := func(t time.Time) string { return t.In(beijing).Format("2006-01-02 15:04:05") }
	env.site.flashes = []foodTalksFlash{
		{ID: 66, Title: draft.Title, PublishStatus: 1, IsBanned: 1, PublishTime: bj(intent.Add(-24 * time.Hour))},
		{ID: 77, Title: draft.Title, PublishStatus: 1, PublishTime: bj(intent.Add(10 * time.Second))},
	}
	publishing := func(articleID string) flashJob {
		job, _, _ := store.create(articleID, "", "")
		job, _ = store.update(job.ID, func(j *flashJob) {
			j.Draft = &draft
			j.Version = 1
			j.Stage = flashStagePublishing
			j.PublishAttempts = 1
			j.PublishIntentAt = intent.UTC().Format(time.RFC3339)
		})
		return job
	}
	job := publishing("a2")
	if err := env.p.stepPublish(job); err != nil {
		t.Fatal(err)
	}
	got, _ := store.byID(job.ID)
	if got.Stage != flashStagePublished || got.FlashID != 77 || len(env.site.created) != 0 {
		t.Fatalf("应该认点通过后建的 77、不新建：%+v created=%d", got, len(env.site.created))
	}
	env.site.flashes = env.site.flashes[:1] // 只剩一天前那条旧的
	job = publishing("a2-old")
	if err := env.p.stepPublish(job); err != nil {
		t.Fatal(err)
	}
	got, _ = store.byID(job.ID)
	if got.FlashID == 66 || len(env.site.created) != 1 {
		t.Fatalf("早先的同题旧快讯不该认：%+v created=%d", got, len(env.site.created))
	}
}

// 一次都没建过、网站上已有同题的：别人发过了，不发第二条。
func TestFlashPublishSkipsWhenSiteAlreadyHasSameTitle(t *testing.T) {
	env := newFlashTestEnv(t)
	store := env.p.store
	job, _, _ := store.create("a3", "", "")
	draft := flashDraft{Title: "乐乐茶全线降价超30%", Body: []string{"甲", "乙"}}
	env.site.flashes = []foodTalksFlash{{ID: 88, Title: draft.Title, PublishStatus: 1}}
	job, _ = store.update(job.ID, func(j *flashJob) { j.Draft = &draft; j.Version = 1; j.Stage = flashStagePublishing })
	if err := env.p.stepPublish(job); err != nil {
		t.Fatal(err)
	}
	got, _ := store.byID(job.ID)
	if got.Stage != flashStageSkipped || got.DuplicateOf != "https://test.foodtalks.cn/flash/88" || len(env.site.created) != 0 {
		t.Fatalf("应该判重复不发：%+v", got)
	}
}

// 建好了但读回失败：编号先记上，下次直接读回，不再建。
func TestFlashPublishRecordsIDBeforeReadBack(t *testing.T) {
	env := newFlashTestEnv(t)
	store := env.p.store
	job, _, _ := store.create("a4", "", "")
	draft := flashDraft{Title: "某某推出新品", Body: []string{"甲", "乙"}}
	job, _ = store.update(job.ID, func(j *flashJob) { j.Draft = &draft; j.Version = 1; j.Stage = flashStagePublishing })
	env.site.failGet = true
	if err := env.p.stepPublish(job); err == nil {
		t.Fatal("读回失败应该报错")
	}
	got, _ := store.byID(job.ID)
	if got.FlashID == 0 || got.PublishAttempts != 1 {
		t.Fatalf("编号和尝试次数要先记上：%+v", got)
	}
	env.site.failGet = false
	if err := env.p.stepPublish(got); err != nil {
		t.Fatal(err)
	}
	got, _ = store.byID(job.ID)
	if got.Stage != flashStagePublished || len(env.site.created) != 1 {
		t.Fatalf("第二次只读回不再建：%+v created=%d", got, len(env.site.created))
	}
}

func TestFlashSkipsHardFilterSponsoredAndSameEvent(t *testing.T) {
	env := newFlashTestEnv(t)
	hard := flashTestSource("h1")
	hard.Judgment.HardFilter = "ad"
	sponsored := flashTestSource("s1")
	sponsored.Judgment.Flags = []string{"sponsored"}
	first := flashTestSource("e1")
	second := flashTestSource("e2")
	second.Event.ID = first.Event.ID
	env.src["h1"], env.src["s1"], env.src["e1"], env.src["e2"] = hard, sponsored, first, second

	for _, id := range []string{"h1", "s1"} {
		job, _, _ := env.p.submit(id, "", "")
		job = waitFlashStage(t, env.p, job.ID, flashStageSkipped)
		if job.SkipReason == "" {
			t.Fatalf("%s 要写不写的原因", id)
		}
	}
	a, _, _ := env.p.submit("e1", "", "")
	waitFlashStage(t, env.p, a.ID, flashStageAwaitingReview)
	b, _, _ := env.p.submit("e2", "", "")
	b = waitFlashStage(t, env.p, b.ID, flashStageSkipped)
	if !strings.Contains(b.SkipReason, "#1") && !strings.Contains(b.SkipReason, "#"+itoa(a.ID)) {
		t.Fatalf("同一件事应指向前一条：%q", b.SkipReason)
	}
	if env.calls != 2 { // 只有 e1 写了稿、对了一次原文
		t.Fatalf("不写的不该调模型，实际调了 %d 次", env.calls)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// 模型说网站已经发过同一件事：不写，记下是哪条。
func TestFlashSkipsWhenModelFindsSiteDuplicate(t *testing.T) {
	env := newFlashTestEnv(t)
	env.src["d1"] = flashTestSource("d1")
	env.ai = func(string, string) (string, error) {
		return `{"skip":"","duplicate_of":"快讯 2026-11-28 乐乐茶全线降价","title":"","body":[],"source":{},"hints":[]}`, nil
	}
	job, _, _ := env.p.submit("d1", "", "")
	job = waitFlashStage(t, env.p, job.ID, flashStageSkipped)
	if job.DuplicateOf == "" || job.Draft != nil {
		t.Fatalf("应该判重复：%+v", job)
	}
}

// 字数、段数不对或用了规范表里不推荐的说法：让模型改一次；对不上原文的再改，改完还对不上的留在卡上。
func TestFlashWriterRevisesFormatAndKeepsUnresolvedUnsupported(t *testing.T) {
	var prompts []string
	short := `{"title":"某品牌重磅推出新品","body":["太短了"],"source":{"url":"https://example.com/a1"},"hints":[]}`
	ai := func(_ context.Context, system, user string) (string, error) {
		prompts = append(prompts, user)
		if strings.HasPrefix(system, strings.TrimSpace(flashVerifyTask)[:20]) {
			return `{"unsupported":[{"quote":"平均降幅超过30%","why":"原文没说平均"},{"quote":"稿子里没有的话","why":"编的"}]}`, nil
		}
		if len(prompts) == 1 {
			return short, nil
		}
		return flashTestDraftJSON, nil
	}
	w := flashWriter{ai: ai, now: time.Now}
	spec := flashSpec{Revision: 1, Text: "规范", Avoid: []flashSpecPhrase{{Avoid: "重磅推出", Instead: "推出、发布"}}}
	res, err := w.write(context.Background(), spec, flashTestSource("x"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompts[1], "正文要 2 到 3 段") || !strings.Contains(prompts[1], "重磅推出") {
		t.Fatalf("第二次要带上格式问题：%s", prompts[1])
	}
	if len(res.Unsupported) != 1 || !strings.Contains(res.Unsupported[0], "平均降幅超过30%") {
		t.Fatalf("只留稿子里真有的那条：%v", res.Unsupported)
	}
	// 写 1 + 改格式 1 + 核对 1 + 改 2 + 再核对 2 = 7
	if len(prompts) != 7 {
		t.Fatalf("调用次数不对：%d", len(prompts))
	}
}

func TestFlashApproveBlockedByRiskAndEditRechecks(t *testing.T) {
	env := newFlashTestEnv(t)
	env.src["r1"] = flashTestSource("r1")
	level := publishRiskBlock
	env.p.risk = func(context.Context, string, string, string) (publishRiskAssessment, error) {
		return publishRiskAssessment{Level: level, Headline: "有疾病功效宣称"}, nil
	}
	job, _, _ := env.p.submit("r1", "", "")
	job = waitFlashStage(t, env.p, job.ID, flashStageAwaitingReview)
	if _, err := env.p.approve(job.ID, 1, "ou_test"); err == nil {
		t.Fatal("风险判了不能发，不该能通过")
	}
	card, _ := json.Marshal(buildFlashCard(job, time.Now()))
	if strings.Contains(string(card), flashCardActionApprove) {
		t.Fatal("风险判了不能发，卡上不该有通过按钮")
	}
	level = publishRiskPass
	if _, err := env.p.edit(job.ID, 1, "乐乐茶全线降价", "第一段\n\n第二段", "ou_editor"); err != nil {
		t.Fatal(err)
	}
	job = waitFlashStage(t, env.p, job.ID, flashStageAwaitingReview)
	if job.Version != 2 || job.Checks == nil || job.Checks.Version != 2 || len(job.Draft.Body) != 2 || job.EditedBy != "ou_editor" {
		t.Fatalf("改字后要第 2 版、重新查：%+v %+v", job, job.Checks)
	}
	if _, err := env.p.approve(job.ID, 2, "ou_test"); err != nil {
		t.Fatal(err)
	}
	waitFlashStage(t, env.p, job.ID, flashStagePublished)
}

func TestFlashSourceErrorsRetryOrGiveUp(t *testing.T) {
	env := newFlashTestEnv(t)
	env.p.source = func(context.Context, string) (flashSourceDoc, error) {
		return flashSourceDoc{}, errors.New("资讯订阅回 HTTP 503")
	}
	job, _, _ := env.p.store.create("t1", "", "")
	env.p.advance(job.ID)
	got, _ := env.p.store.byID(job.ID)
	if got.Stage != flashStageWriting || got.Failures != 1 || got.RetryAt == "" || got.FailedStage != flashStageWriting {
		t.Fatalf("503 要排下一次：%+v", got)
	}
	env.p.source = func(context.Context, string) (flashSourceDoc, error) { return flashSourceDoc{}, errFlashSourceNotFound }
	env.p.advance(job.ID)
	got, _ = env.p.store.byID(job.ID)
	if got.Stage != flashStageFailed {
		t.Fatalf("404 重试没用，直接失败：%+v", got)
	}
	if n := env.p.flashMonitoringFailures(); n != 1 {
		t.Fatalf("监控要报 1 条：%d", n)
	}
	status := currentPublishMonitoring(newPublishJobStore(t.TempDir()+"/jobs.json"), false, 1, time.Now())
	if !containsString(status.AlertIssues, "flash_failed:1") {
		t.Fatalf("要有 flash_failed 告警：%v", status.AlertIssues)
	}
}

// 状态推给资讯订阅：事件类型是快讯的，发布后带 FoodTalks 编号和网址，签名同文章那套。
func TestFlashStatusCallbackCarriesFlashEvent(t *testing.T) {
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", "test-secret")
	var mu sync.Mutex
	var bodies []map[string]any
	var headers []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		bodies = append(bodies, body)
		headers = append(headers, r.Header.Clone())
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	env := newFlashTestEnv(t)
	env.p.store.callbacks.jitter = func(time.Duration) time.Duration { return 0 }
	env.p.store.callbacks.startSending()
	env.src["c1"] = flashTestSource("c1")
	job, _, _ := env.p.submit("c1", "", srv.URL+"/api/internal/flash-status")
	job = waitFlashStage(t, env.p, job.ID, flashStageAwaitingReview)
	if _, err := env.p.approve(job.ID, 1, "ou_test"); err != nil {
		t.Fatal(err)
	}
	waitFlashStage(t, env.p, job.ID, flashStagePublished)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		var last map[string]any
		if len(bodies) > 0 {
			last = bodies[len(bodies)-1]
		}
		mu.Unlock()
		if last != nil {
			data, _ := last["data"].(map[string]any)
			j, _ := data["job"].(map[string]any)
			if j["stage"] == flashStagePublished {
				if last["type"] != flashStatusCallbackEventType || j["foodtalks_flash_id"] != float64(1001) || j["flash_url"] != "https://test.foodtalks.cn/flash/1001" {
					t.Fatalf("推送内容不对：%v", last)
				}
				h := headers[len(headers)-1]
				if !strings.HasPrefix(h.Get("webhook-id"), "layout-flash-") || h.Get("webhook-signature") == "" {
					t.Fatalf("签名头不对：%v", h)
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("没等到已发布的推送：%v", bodies)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ---- 入口 ----

func TestFlashSyncIntake(t *testing.T) {
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", "test-token")
	t.Setenv("FOODTALKS_FEED_BASE_URL", "https://feed.example.com")
	pipe := &publishPipeline{}
	mux := http.NewServeMux()
	registerFlashRoutes(mux, pipe, nil)
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/publish/flash-sync", strings.NewReader(body))
		req.Header.Set(publishSyncServiceHeader, "test-token")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(`{"article_id":"a"}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("没开要 503：%d", rec.Code)
	}
	env := newFlashTestEnv(t)
	env.src["a"] = flashTestSource("a")
	pipe.flash = env.p
	for _, bad := range []string{`{"article_id":"a","title":"x"}`, `{"article_id":"a","text":"正文"}`, `{"url":"https://x"}`, `{"article_id":"a","status_callback_url":"https://evil.example.com/x"}`} {
		if rec := post(bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 要 400：%d %s", bad, rec.Code, rec.Body.String())
		}
	}
	rec := post(`{"article_id":"a","url":"https://example.com/a1","status_callback_url":"https://feed.example.com/api/internal/flash-status"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("第一次要 201：%d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Existing bool            `json:"existing"`
		Job      flashJobSummary `json:"job"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Existing || resp.Job.ArticleID != "a" || resp.Job.Stage != flashStageQueued {
		t.Fatalf("回的不对：%s", rec.Body.String())
	}
	waitFlashStage(t, env.p, resp.Job.ID, flashStageAwaitingReview)
	rec = post(`{"article_id":"a"}`)
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != http.StatusOK || !resp.Existing || resp.Job.Stage != flashStageAwaitingReview {
		t.Fatalf("重复交要 200 + existing：%d %s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodPost, "/api/publish/flash-sync", strings.NewReader(`{"article_id":"a"}`))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusCreated || rec.Code == http.StatusOK {
		t.Fatalf("不带口令不能收：%d", rec.Code)
	}
}

// ---- 卡片 ----

func TestFlashCardShapeAndActions(t *testing.T) {
	env := newFlashTestEnv(t)
	env.src["k1"] = flashTestSource("k1")
	job, _, _ := env.p.submit("k1", "", "")
	job = waitFlashStage(t, env.p, job.ID, flashStageAwaitingReview)
	card := buildFlashCard(job, time.Now())
	raw, _ := json.Marshal(card)
	text := string(raw)
	if n := feishuCardElementCount(card); n > feishuCardElementLimit {
		t.Fatalf("组件 %d 个，超上限", n)
	}
	for _, want := range []string{`"schema":"2.0"`, `"update_multi":true`, "通过并发布", "改字", "驳回", `"confirm"`, "界面新闻"} {
		if !strings.Contains(text, want) {
			t.Fatalf("卡上缺 %s：%s", want, text)
		}
	}
	for _, r := range text {
		if r >= 0x1F300 && r <= 0x1FAFF {
			t.Fatalf("卡上不能有 emoji：%q", r)
		}
	}
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer")
	value := func(action string, version int) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"action": action, "flash_id": job.ID, "version": version})
		return b
	}
	if _, handled := env.p.cardAction("ou_reviewer", "", json.RawMessage(`{"action":"approve","job_id":3}`), nil); handled {
		t.Fatal("文章卡的按钮不归快讯管")
	}
	if out, _ := env.p.cardAction("ou_stranger", "oc_other", value(flashCardActionApprove, 1), nil); out.toastType != "error" {
		t.Fatal("不在名单、不在群里的人不能点")
	}
	out, _ := env.p.cardAction("ou_reviewer", "", value(flashCardActionEdit, 1), nil)
	editRaw, _ := json.Marshal(out.card)
	if !strings.Contains(string(editRaw), `"form_action_type":"submit"`) || !strings.Contains(string(editRaw), "flash_body") {
		t.Fatalf("点改字要换成编辑框：%s", editRaw)
	}
	out, _ = env.p.cardAction("ou_reviewer", "", value(flashCardActionSave, 1), map[string]any{"flash_title": "新标题", "flash_body": "一段\n二段"})
	if out.toastType != "success" {
		t.Fatalf("保存：%+v", out)
	}
	job = waitFlashStage(t, env.p, job.ID, flashStageAwaitingReview)
	if job.Draft.Title != "新标题" || job.Version != 2 {
		t.Fatalf("改字没生效：%+v", job.Draft)
	}
	if out, _ := env.p.cardAction("ou_reviewer", "", value(flashCardActionApprove, 1), nil); out.toastType != "error" {
		t.Fatal("旧卡不能通过")
	}
	if out, _ := env.p.cardAction("ou_reviewer", "", value(flashCardActionReject, 2), nil); out.toastType != "success" {
		t.Fatalf("驳回：%+v", out)
	}
	got, _ := env.p.store.byID(job.ID)
	if got.Stage != flashStageRejected || got.ReviewedBy != "ou_reviewer" {
		t.Fatalf("驳回没记上：%+v", got)
	}
}

// ---- 规范怎么读 ----

func TestFlashSpecDropsStrikeImagesAndInlinesSheet(t *testing.T) {
	content := "# 规范\n\n## 一、任务\n\n- \\*\\*篇幅：\\*\\*约300字\n\n![图](https://feishu.cn/file/x)\n\n<sheet sheet-id=\"S1\" token=\"SHEETTOKEN\"></sheet>\n\n违反广告法的表述<cite doc-id=\"D\" file-type=\"docx\" title=\"公司广告法制度\" type=\"doc\"></cite>。\n\n## ~~旧模板~~\n\n~~请按照规范……~~\n\n## 五、优先级\n\n事实准确。" + strings.Repeat("补足字数。", 60)
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer tenant-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/wiki/v2/spaces/get_node"):
			_, _ = w.Write([]byte(`{"code":0,"data":{"node":{"obj_token":"DOCX1","obj_type":"docx","title":"规范"}}}`))
		case strings.HasSuffix(r.URL.Path, "/docs_ai/v1/documents/DOCX1/fetch"):
			b, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"document": map[string]any{"content": content, "revision_id": 1117}}})
			_, _ = w.Write(b)
		case strings.Contains(r.URL.Path, "/sheets/v2/spreadsheets/SHEETTOKEN/values/"):
			_, _ = w.Write([]byte(`{"code":0,"data":{"valueRange":{"values":[["不推荐使用","推荐处理方式"],["重磅推出","推出、发布"],["首创、全球第一","核实后再用"],[null,null]]}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	s := &flashSpecSource{doc: "WIKINODE", baseURL: srv.URL, client: srv.Client(), now: time.Now,
		token:     func(context.Context) (string, error) { return "tenant-token", nil },
		cachePath: t.TempDir() + "/flash-spec-cache.json"}
	spec, err := s.load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if spec.Revision != 1117 {
		t.Fatalf("版本号：%d", spec.Revision)
	}
	for _, gone := range []string{"旧模板", "请按照规范", "![", "<sheet", "<cite", `\*`} {
		if strings.Contains(spec.Text, gone) {
			t.Fatalf("不该留 %q：%s", gone, spec.Text)
		}
	}
	for _, want := range []string{"| 不推荐使用 | 推荐处理方式 |", "| 重磅推出 | 推出、发布 |", "《公司广告法制度》", "**篇幅：**", "## 五、优先级"} {
		if !strings.Contains(spec.Text, want) {
			t.Fatalf("缺 %q：%s", want, spec.Text)
		}
	}
	if len(spec.Avoid) != 3 || spec.Avoid[1].Avoid != "首创" || spec.Avoid[2].Avoid != "全球第一" {
		t.Fatalf("不推荐说法表：%+v", spec.Avoid)
	}
	// 一分钟内再要用同一份；飞书挂了用存下的旧版，并写明原因。
	if _, err := s.load(context.Background()); err != nil || len(paths) != 3 {
		t.Fatalf("一分钟内不该再取：%v %v", err, paths)
	}
	s2 := &flashSpecSource{doc: "WIKINODE", baseURL: "http://127.0.0.1:1", client: &http.Client{Timeout: time.Second}, now: time.Now,
		token: func(context.Context) (string, error) { return "tenant-token", nil }, cachePath: s.cachePath}
	stale, err := s2.load(context.Background())
	if err != nil || stale.Revision != 1117 || stale.Stale == "" {
		t.Fatalf("取不到要用存下的：%v %+v", err, stale.Stale)
	}
}

// ---- FoodTalks 快讯接口 ----

func TestFoodTalksFlashClientRequests(t *testing.T) {
	var got []string
	var createBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == foodTalksFlashPath:
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &createBody)
			_, _ = w.Write([]byte(`{"code":0,"data":1541}`))
		case r.Method == http.MethodGet && r.URL.Path == foodTalksFlashPath+"/1541":
			_, _ = w.Write([]byte(`{"code":0,"data":{"id":1541,"title":"T","publishStatus":1,"isBanned":0}}`))
		case r.URL.Path == foodTalksFlashPagePath:
			_, _ = w.Write([]byte(`{"code":0,"data":{"records":[{"id":1541,"title":"T","publishStatus":1,"isBanned":0}]}}`))
		case r.URL.Path == foodTalksFlashBannedPath:
			_, _ = w.Write([]byte(`{"code":0,"data":true}`))
		case r.URL.Path == foodTalksNewsPath+"/page":
			_, _ = w.Write([]byte(`{"code":0,"data":{"records":[{"id":9,"title":"资讯标题","publishTime":"2026-11-28 10:00:00"}]}}`))
		default:
			_, _ = w.Write([]byte(`{"code":500,"msg":"参数错误"}`))
		}
	}))
	defer srv.Close()
	c := &foodTalksClient{httpClient: srv.Client(), baseURL: srv.URL, token: "tok"}
	ctx := context.Background()
	id, err := c.CreateFlash(ctx, foodTalksNewFlash{Title: "T", Content: foodTalksFlashHTML([]string{"甲<", "乙"}), SourceURL: "https://s", Publish: true})
	if err != nil || id != 1541 {
		t.Fatalf("create: %v %d", err, id)
	}
	if createBody["publishStatus"] != float64(1) || createBody["seoTitle"] != "T-FoodTalks全球食品资讯" || createBody["id"] != "" ||
		createBody["content"] != "<p>甲&lt;</p>\n<p>&nbsp;</p>\n<p>乙</p>" || createBody["language"] != "ZH" {
		t.Fatalf("新建请求体不对：%v", createBody)
	}
	f, err := c.GetFlash(ctx, 1541)
	if err != nil || !f.live() {
		t.Fatalf("get: %v %+v", err, f)
	}
	list, err := c.SearchFlashes(ctx, "T", 5)
	if err != nil || len(list) != 1 {
		t.Fatalf("search: %v", err)
	}
	if err := c.BanFlash(ctx, 1541); err != nil {
		t.Fatal(err)
	}
	news, err := c.RecentNewsTitles(ctx, 3)
	if err != nil || len(news) != 1 {
		t.Fatalf("news: %v", err)
	}
	want := []string{"PUT /news/admin/short/news/banned?bannedStatus=1&id=1541", "GET /news/admin/short/news/page?current=1&size=5&title=T"}
	joined := strings.Join(got, "\n")
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Fatalf("缺请求 %s：\n%s", w, joined)
		}
	}
	c.token = "bad"
	if _, err := c.CreateFlash(ctx, foodTalksNewFlash{Title: "T"}); !errors.Is(err, errFoodTalksUnauthorized) {
		t.Fatalf("401 要归成凭证失效：%v", err)
	}
}

func TestFlashSourceFetch(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != flashSourcePath || r.Header.Get(publishSyncServiceHeader) != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotBody, _ = io.ReadAll(r.Body)
		if bytes.Contains(gotBody, []byte("missing")) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"article":{"id":"a1","url":"https://e/1","title":"T","published_at":"2026-11-28T09:00:00+08:00","source_name":"甲","text":"正文"},"event":{"id":"ev","title":"事","source_count":2,"reports":[{"article_id":"b0","source_name":"乙","published_at":"2026-11-28T08:00:00+08:00"},{"article_id":"b1","source_name":"丙","published_at":"2026-11-28T10:00:00+08:00"}]}}`))
	}))
	defer srv.Close()
	t.Setenv("PUBLISH_SYNC_SERVICE_TOKEN", "tok")
	t.Setenv("FOODTALKS_FEED_BASE_URL", srv.URL)
	doc, err := fetchFlashSource(context.Background(), "a1")
	if err != nil || doc.Article.Title != "T" || string(gotBody) != `{"article_id":"a1"}` {
		t.Fatalf("fetch: %v %s", err, gotBody)
	}
	if r, ok := doc.earliestOtherReport(); !ok || r.ArticleID != "b0" {
		t.Fatalf("最早的别家报道应是 b0：%+v %v", r, ok)
	}
	if _, err := fetchFlashSource(context.Background(), "missing"); !errors.Is(err, errFlashSourceNotFound) {
		t.Fatalf("404 要认成没有这篇：%v", err)
	}
}

// 同一篇重复交带回调地址：版本号接着加、不清零；同地址只在放弃或 410 之后才重新登记。
func TestFlashCallbackReRegistrationKeepsSeq(t *testing.T) {
	store := newFlashJobStore(t.TempDir() + "/flash-jobs.json")
	job, _, err := store.create("a", "", "https://feed.example.com/api/internal/flash-status")
	if err != nil {
		t.Fatal(err)
	}
	job, _ = store.update(job.ID, func(j *flashJob) { j.Stage = flashStageWriting })
	if job.StatusCallback.Seq != 2 {
		t.Fatalf("登记一版、变一次又一版：%d", job.StatusCallback.Seq)
	}
	again, existing, _ := store.create("a", "", "https://feed.example.com/api/internal/flash-status")
	if !existing || again.StatusCallback.Seq != 2 {
		t.Fatalf("同地址重复登记不动：%+v", again.StatusCallback)
	}
	_ = store.updateStatusCallback(job.ID, func(publishStatusCallback) bool { return true }, func(cb *publishStatusCallback) { cb.State = publishStatusCallbackGone })
	again, _, _ = store.create("a", "", "https://feed.example.com/api/internal/flash-status")
	if again.StatusCallback.Seq != 3 || again.StatusCallback.State != "" {
		t.Fatalf("回过 410 再登记要恢复、版本号接着加：%+v", again.StatusCallback)
	}
	moved, _, _ := store.create("a", "", "https://feed.example.com/api/internal/flash-status-v2")
	if moved.StatusCallback.Seq != 4 || moved.StatusCallback.URL != "https://feed.example.com/api/internal/flash-status-v2" {
		t.Fatalf("换地址要登记、版本号接着加：%+v", moved.StatusCallback)
	}
}

// 字数照编辑的数法：英文单词、数字一串算一个；公众号链接只留打得开的四个参数。
func TestFlashBodyLengthCountsLikeEditorsAndCleansWechatLinks(t *testing.T) {
	for _, tc := range []struct {
		body []string
		want int
	}{
		{[]string{"乐乐茶降价30%。"}, 7},
		{[]string{"据FoodBev报道，Oterra推出ColorFruit Red Radish Powder。"}, 13},
		{[]string{"营收155亿兹罗提（约合38亿美元）", "Coca-Cola's 3.9% 增长"}, 19},
	} {
		if got := flashBodyRunes(tc.body); got != tc.want {
			t.Fatalf("%q 数成 %d，应为 %d", tc.body, got, tc.want)
		}
	}
	raw := "http://mp.weixin.qq.com/s?__biz=MzA4ODEyNzMxMg%3D%3D&chksm=89d316a9&idx=2&mid=2650294970&scene=0&sn=fbb30e27"
	if got := flashShareableURL(raw); got != "https://mp.weixin.qq.com/s?__biz=MzA4ODEyNzMxMg%3D%3D&mid=2650294970&idx=2&sn=fbb30e27" {
		t.Fatalf("公众号链接：%s", got)
	}
	for _, keep := range []string{"https://www.foodbev.com/post301/x", "https://mp.weixin.qq.com/s/AbCd", "http://mp.weixin.qq.com/s?__biz=X&mid=1&idx=1"} {
		if got := flashShareableURL(keep); got != keep {
			t.Fatalf("别的链接、短链、缺 sn 的原样留：%s → %s", keep, got)
		}
	}
}

// 2026-11-29 试跑第二遍：第一遍发过又下架的 10 条被当成“网站已发过”，全判重复。下架的快讯、没公开的资讯草稿不算。
func TestFlashSiteRecentOnlyCountsLiveItems(t *testing.T) {
	env := newFlashTestEnv(t)
	env.site.flashes = []foodTalksFlash{
		{ID: 1, Title: "在架的快讯", PublishStatus: 1, PublishTime: "2026-11-28 10:00:00"},
		{ID: 2, Title: "下架的快讯", PublishStatus: 1, IsBanned: 1},
	}
	env.site.news = []foodTalksNewsListItem{
		{ID: 3, Title: "公开的资讯", PublishStatus: foodTalksPublishStatusPublished},
		{ID: 4, Title: "隐藏草稿", PublishStatus: 0},
		{ID: 5, Title: "下线的资讯", PublishStatus: 2},
	}
	got := strings.Join(env.p.siteRecent(context.Background()), "\n")
	if !strings.Contains(got, "在架的快讯") || !strings.Contains(got, "公开的资讯") {
		t.Fatalf("在架的要给模型看：%s", got)
	}
	for _, gone := range []string{"下架的快讯", "隐藏草稿", "下线的资讯"} {
		if strings.Contains(got, gone) {
			t.Fatalf("%s 不算网站发过：%s", gone, got)
		}
	}
	// 发布前按标题查：同题的那条下架了，照发。
	store := env.p.store
	job, _, _ := store.create("b1", "", "")
	draft := flashDraft{Title: "下架的快讯", Body: []string{"甲", "乙"}}
	job, _ = store.update(job.ID, func(j *flashJob) { j.Draft = &draft; j.Version = 1; j.Stage = flashStagePublishing })
	if err := env.p.stepPublish(job); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.byID(job.ID); got.Stage != flashStagePublished || len(env.site.created) != 1 {
		t.Fatalf("同题的下架了要照发：%+v", got)
	}
}
