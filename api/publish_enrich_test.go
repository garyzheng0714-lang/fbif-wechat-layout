package main

// publish_enrich_test.go — M5「AI 预填发布参数」测试（MiMo 走 httptest 桩，绝不触真实
// API）。锁死三条契约：
//   1. enriching 只产元数据：AI 成功注入 summary/tags/author/ai_notes、可换封面，
//      但 content_html 必须逐字节不变（AI 没有正文篡改面）；
//   2. AI 是加速器不是闸门：MiMo 失败走规则兜底，job 照常推进到 awaiting_review，
//      绝不 fail；
//   3. buildSiteNews 消费 AI 字段（Summary/Author/NewsTags/CoverImg）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// mirroredPublishHTML 是 mirroring 完成后的成品正文（testPublishRenderedHTML 的
// mmbiz 图已全部重写为 OSS URL）——enriching 前后它必须逐字节相等。
func mirroredPublishHTML() string {
	s := testPublishRenderedHTML
	for _, src := range []string{"https://mmbiz.qpic.cn/a.jpg", "https://mmbiz.qpic.cn/b.jpg"} {
		s = strings.ReplaceAll(s, src, ossURLFor(src))
	}
	return s
}

// aiStubRequest MiMo 桩收到的请求体（断言请求形态用）。
type aiStubRequest struct {
	Model string `json:"model"`
	Input []struct {
		Role    string `json:"role"`
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL string `json:"image_url"`
		} `json:"content"`
	} `json:"input"`
}

// newAIStub 起 MiMo Responses API 桩并把 env 指向它（aiEnrichArticle 走真实客户端代码）。
// respond 返回 (HTTP 状态码, 模型输出文本)；文本为空时回 error 响应体。
func newAIStub(t *testing.T, respond func() (int, string)) *aiStubRequest {
	t.Helper()
	got := &aiStubRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != mimoResponsesPath {
			t.Errorf("MiMo 桩收到意外路径 %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-mimo-key" {
			t.Errorf("MiMo 桩收到错误鉴权头 %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			t.Errorf("MiMo 桩解析请求体失败: %v", err)
		}
		status, text := respond()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if text == "" {
			fmt.Fprint(w, `{"error":{"message":"模型服务不可用"}}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"output": []any{
				map[string]any{"type": "reasoning"}, // 非 message 项必须被跳过
				map[string]any{"type": "message", "content": []any{
					map[string]any{"type": "output_text", "text": text},
				}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MIMO_API_KEY", "test-mimo-key")
	t.Setenv("MIMO_BASE_URL", srv.URL)
	noAIRetryDelay(t)
	return got
}

// TestPublishEnrichingInjectsMetadata AI 成功：summary/tags/author/ai_notes 注入落库、
// AI 从候选里选中的封面覆盖默认第一图、content_html 逐字节不变、任务照常停在
// awaiting_review。响应带 markdown 围栏，顺带锁死解析容错。
func TestPublishEnrichingInjectsMetadata(t *testing.T) {
	env := newPublishTestEnv(t)
	renderedWithAuthor := strings.Replace(
		testPublishRenderedHTML,
		"<p>正文</p>",
		"<p>作者：Mote莫特</p><p>正文</p>",
		1,
	)
	env.pipeline.render = func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
		return successfulPublishRenderResult(renderedWithAuthor, testPublishResolvedRule()), nil
	}
	aiCover := ossURLFor("https://mmbiz.qpic.cn/b.jpg")
	gotReq := newAIStub(t, func() (int, string) {
		return http.StatusOK, "```json\n" + fmt.Sprintf(
			`{"cover_url":%q,"summary":"这是一段模型生成的文章摘要。","tags":["食品","饮料","新品"],"author":"Mote莫特","notes":"封面选第二张图，构图更完整。正文格式规范。摘要取自导语。"}`,
			aiCover) + "\n```"
	})
	t.Setenv("MIMO_FLASH_MODEL", "mimo-test-vision")
	env.pipeline.enrich = aiEnrichArticle

	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("stage = %q（error=%q），应停在 awaiting_review", got.Stage, got.Error)
	}
	// 🚨 边界红线：enriching 只产元数据，content_html 必须与 mirroring 成品逐字节相等。
	wantHTML := renderedWithAuthor
	for _, src := range []string{"https://mmbiz.qpic.cn/a.jpg", "https://mmbiz.qpic.cn/b.jpg"} {
		wantHTML = strings.ReplaceAll(wantHTML, src, ossURLFor(src))
	}
	if got.ContentHTML != wantHTML {
		t.Fatalf("enriching 改动了 content_html（禁止）：%q", got.ContentHTML)
	}
	if got.Summary != "这是一段模型生成的文章摘要。" || got.Author != "Mote莫特" {
		t.Fatalf("AI 元数据未落库：summary=%q author=%q", got.Summary, got.Author)
	}
	if len(got.Tags) != 3 || got.Tags[0] != "食品" {
		t.Fatalf("AI 标签未落库：%v", got.Tags)
	}
	if !strings.Contains(got.AINotes, "封面选第二张图") {
		t.Fatalf("AI 优化说明未落库：%q", got.AINotes)
	}
	if got.CoverURL != aiCover {
		t.Fatalf("AI 选中封面应覆盖默认第一图：%q", got.CoverURL)
	}
	// 请求形态：model 取 env、system 提示强制简体中文、图片多模态入参齐全。
	if gotReq.Model != "mimo-test-vision" {
		t.Fatalf("model = %q，看图预填应取看图档（2026-10-07 用户裁定 Flash，MIMO_FLASH_MODEL）", gotReq.Model)
	}
	if len(gotReq.Input) != 2 || gotReq.Input[0].Role != "system" {
		t.Fatalf("input 形态不符：%+v", gotReq.Input)
	}
	if !strings.Contains(gotReq.Input[0].Content[0].Text, "请用简体中文回复") {
		t.Fatalf("system 提示必须含「请用简体中文回复」：%q", gotReq.Input[0].Content[0].Text)
	}
	var imageURLs []string
	userContent := gotReq.Input[1].Content
	userText := userContent[0].Text
	for i, c := range userContent {
		if c.Type != "input_image" {
			continue
		}
		imageURLs = append(imageURLs, c.ImageURL)
		// 每张图正前方必须是它自己的 URL 标注（防模型看对图、抄错相邻 URL）。
		label := fmt.Sprintf("候选图 %d：%s", len(imageURLs), c.ImageURL)
		if i == 0 || userContent[i-1].Type != "input_text" || userContent[i-1].Text != label {
			t.Fatalf("第 %d 张图前应紧贴标注 %q，实际 %+v", len(imageURLs), label, userContent[max(i-1, 0)])
		}
	}
	if len(imageURLs) != 2 || imageURLs[0] != ossURLFor("https://mmbiz.qpic.cn/a.jpg") {
		t.Fatalf("input_image 候选应为 OSS 化正文图：%v", imageURLs)
	}
	if !strings.Contains(userText, "测试标题") || !strings.Contains(userText, "正文") {
		t.Fatalf("user 文本应含标题与纯文本正文：%q", userText)
	}
}

func TestPublishSiteIsRepostIsCMSProvenanceNotLayout(t *testing.T) {
	t.Setenv(siteOwnAccountsEnv, "FBIF食品饮料创新,FoodTalks")
	plain := `[{"k":"txt","runs":[{"type":"txt","text":"正文内容"}]}]`
	disclosed := `[{"k":"txt","runs":[{"type":"txt","text":"* 本文为转载文章。"}]},{"k":"txt","runs":[{"type":"txt","text":"正文内容"}]}]`

	if publishSiteIsRepost("FBIF食品饮料创新", plain) {
		t.Fatal("自家公众号且无转载声明时 CMS 来源类型应为原创")
	}
	if !publishSiteIsRepost("外部食品媒体", plain) {
		t.Fatal("外部公众号 CMS 来源类型应为转载")
	}
	if !publishSiteIsRepost("FBIF食品饮料创新", disclosed) {
		t.Fatal("自家公众号正文明确声明转载时，CMS 来源类型必须为转载")
	}
}

func TestPublishHTMLWechatTrailerLeakRejectsWechatChrome(t *testing.T) {
	if got := publishHTMLWechatTrailerLeak(`<p>正文</p><p>*资讯投稿与商务合作，请联系Bobo。</p><p>加入微信群</p>`); got != "" {
		t.Fatalf("网站页脚合法文案不应判泄漏：%s", got)
	}
	cases := []string{
		`* 本文为FBIF原创，欢迎转发朋友圈；`,
		`预览时标签不可点`,
		`<p>/ 更多文章 /</p>`,
		`* 转载请在后台回复“转载”了解规则；`,
		`联系Bobo（微信：fbifmedia1）`,
	}
	for _, html := range cases {
		if publishHTMLWechatTrailerLeak(html) == "" {
			t.Fatalf("应拦住公众号导流尾：%s", html)
		}
	}
}

func TestPublishRenderAlwaysStripsWechatTrailerForOwnAccount(t *testing.T) {
	env := newPublishTestEnv(t)
	var gotRepost *bool
	env.pipeline.fetch = func(ctx context.Context, articleURL string) (publishFetchedArticle, error) {
		env.fetchCalls++
		return publishFetchedArticle{Title: "印尼博士创业", SourceName: "FBIF食品饮料创新", Byline: "Sage", CanonicalURL: articleURL, BlocksJSON: testPublishBlocksJSON}, nil
	}
	env.pipeline.render = func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
		gotRepost = new(bool)
		*gotRepost = isRepost
		return successfulPublishRenderResult(testPublishRenderedHTML, testPublishResolvedRule()), nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	if gotRepost == nil || !*gotRepost {
		t.Fatal("自家公众号进网站也必须按转载切尾（isRepost=true），原创只影响 CMS 来源类型")
	}
	got, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if leak := publishHTMLWechatTrailerLeak(got.ContentHTML); leak != "" {
		t.Fatalf("落库正文仍含公众号导流尾：%s", leak)
	}
}

func TestPublishRenderRejectsWechatTrailerLeak(t *testing.T) {
	env := newPublishTestEnv(t)
	env.pipeline.render = func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
		leaked := `<p>正文</p><p>* 本文为FBIF原创，欢迎转发朋友圈；</p><p>/ 更多文章 /</p>`
		return successfulPublishRenderResult(leaked, testPublishResolvedRule()), nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := env.store.byID(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != publishStageFailed || got.FailedStage != publishStageRendering {
		t.Fatalf("含公众号导流尾的排版必须 fail closed，实际 stage=%q failed_stage=%q error=%q", got.Stage, got.FailedStage, got.Error)
	}
	if !strings.Contains(got.Error, "公众号导流尾") {
		t.Fatalf("失败原因应写明导流尾泄漏，实际 %q", got.Error)
	}
}

// TestPublishEnrichFallbackOnAIFailure MiMo Pro 与 Flash 都挂了（HTTP 500）：走规则兜底
// （摘要=正文前 40 字、author 空【编辑部宁空勿造，规范 §5】、tags 空、notes 说明
// 未执行原因），job 绝不 fail，照常推进到 awaiting_review、CreateNews 恰一次。
func TestPublishEnrichFallbackOnAIFailure(t *testing.T) {
	env := newPublishTestEnv(t)
	newAIStub(t, func() (int, string) { return http.StatusInternalServerError, "" })
	env.pipeline.enrich = aiEnrichArticle

	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("AI 失败不得 fail job，实际 stage=%q error=%q", got.Stage, got.Error)
	}
	if got.Summary != "正文" {
		t.Fatalf("兜底摘要应为正文纯文本前 40 字，实际 %q", got.Summary)
	}
	if got.Author != "" {
		t.Fatalf("兜底 author 应为空（宁空勿造，不塞 source_name），实际 %q", got.Author)
	}
	if len(got.Tags) != 0 {
		t.Fatalf("兜底 tags 应为空，实际 %v", got.Tags)
	}
	if !strings.HasPrefix(got.AINotes, "AI 预填未执行（") {
		t.Fatalf("兜底 notes 应说明未执行原因，实际 %q", got.AINotes)
	}
	if got.CoverURL != ossURLFor("https://mmbiz.qpic.cn/a.jpg") {
		t.Fatalf("兜底封面应维持 mirroring 已定的正文第一图，实际 %q", got.CoverURL)
	}
	if got.ContentHTML != mirroredPublishHTML() {
		t.Fatalf("兜底路径也不得改 content_html：%q", got.ContentHTML)
	}
	if createN, _ := env.ft.counts(); createN != 1 {
		t.Fatalf("阶段应照常推进（CreateNews 恰一次），实际 %d", createN)
	}
}

// TestPublishEnrichKeyMissingFallback MIMO_API_KEY 未配置：不发任何请求直接走兜底
// （newPublishTestEnv 未注入 enrich 时同样兜底——两种缺配置形态都不阻断流水线）。
func TestPublishEnrichKeyMissingFallback(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("MIMO_API_KEY", "")
	env.pipeline.enrich = aiEnrichArticle
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("缺 MIMO_API_KEY 不得 fail job，实际 stage=%q", got.Stage)
	}
	if !strings.Contains(got.AINotes, "MIMO_API_KEY 未配置") {
		t.Fatalf("notes 应写明原因，实际 %q", got.AINotes)
	}
}

// TestParseEnrichmentJSONTolerance 解析容错：裸 JSON / 围栏 / 前后杂话都能解析；
// cover_url 不在候选列表（模型幻觉/改写 URL）必须弃用；非 JSON 输出报错。
func TestParseEnrichmentJSONTolerance(t *testing.T) {
	candidates := []string{"https://oss.example.com/a.jpg"}
	for _, text := range []string{
		`{"cover_url":"https://oss.example.com/a.jpg","summary":"s","tags":["t"],"author":"a","notes":"n"}`,
		"```json\n{\"cover_url\":\"https://oss.example.com/a.jpg\",\"summary\":\"s\",\"tags\":[\"t\"],\"author\":\"a\",\"notes\":\"n\"}\n```",
		"好的，以下是结果：\n{\"cover_url\":\"https://oss.example.com/a.jpg\",\"summary\":\"s\",\"tags\":[\"t\"],\"author\":\"a\",\"notes\":\"n\"}",
	} {
		got, err := parseEnrichmentJSON(text, candidates)
		if err != nil {
			t.Fatalf("应能解析 %q: %v", text, err)
		}
		if got.CoverURL != candidates[0] || got.Summary != "s" || got.Author != "a" {
			t.Fatalf("解析结果不符：%+v", got)
		}
	}
	got, err := parseEnrichmentJSON(`{"cover_url":"https://evil.example.com/x.jpg","summary":"s"}`, candidates)
	if err != nil {
		t.Fatalf("解析: %v", err)
	}
	if got.CoverURL != "" {
		t.Fatalf("候选之外的 cover_url 必须弃用，实际 %q", got.CoverURL)
	}
	if _, err := parseEnrichmentJSON("模型拒绝回答", candidates); err == nil {
		t.Fatalf("非 JSON 输出应报错（走兜底）")
	}
}

// TestEnrichmentEvidenceDropsHallucinatedAttribution 把评测里的「作者编造=0」
// 变成运行时硬约束：prompt/离线评测只能降低概率，单次模型漂移仍可能编名字；
// author/source_name 若无法在本次标题+正文里找到证据，必须自动留空并向审核人说明。
func TestEnrichmentEvidenceDropsHallucinatedAttribution(t *testing.T) {
	input := "测试标题\n正文只有产品信息，没有任何作者或转载出处声明。"
	got := enforceEnrichmentEvidence(publishEnrichment{
		Author:     "热点事件洞察部",
		SourceName: "小食代",
		Notes:      "封面选择清晰。",
	}, input)
	if got.Author != "" || got.SourceName != "" {
		t.Fatalf("无正文证据的作者/出处必须留空：author=%q source=%q", got.Author, got.SourceName)
	}
	if !strings.Contains(got.Notes, "作者") || !strings.Contains(got.Notes, "出处") || !strings.Contains(got.Notes, "自动留空") {
		t.Fatalf("审核说明必须留下自动清理原因：%q", got.Notes)
	}

	kept := enforceEnrichmentEvidence(publishEnrichment{
		Author:     "Mote莫特",
		SourceName: "小食代",
	}, "文｜Mote 莫特\n本文来自微信公众号：小食代")
	if kept.Author != "Mote莫特" || kept.SourceName != "小食代" {
		t.Fatalf("有明确证据的字段不应被误删：%+v", kept)
	}
}

// TestBuildSiteNewsConsumesAIFields buildSiteNews 消费 M5/M7 字段：Summary 直取、
// Author=AI 识别署名（为空就是空——编辑部宁空勿造，不回落 source_name，规范 §5）、
// CoverImg 用（可能被 AI 覆盖过的）cover_url；来源判定走 siteSourceFor 确定性规则
// （自家号=原创 0，外部号/正文声明出处=转载 1，来源名必填——编辑部 163/163 有来源名）。
func TestBuildSiteNewsConsumesAIFields(t *testing.T) {
	t.Setenv("FOODTALKS_CATEGORY_IDS", "")
	p := &publishPipeline{now: time.Now}
	job := publishJob{
		Title:       "标题",
		SourceName:  "测试公众号",
		ContentHTML: "<p>正文</p>",
		CoverURL:    "https://oss.example.com/b.jpg",
		Summary:     "AI 摘要",
		Tags:        []string{"食品", "饮料"},
		Author:      "Mote莫特",
		CreatedAt:   "2026-07-10T00:00:00Z",
	}
	news, err := p.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if err != nil {
		t.Fatalf("buildSiteNews: %v", err)
	}
	if news.Summary != "AI 摘要" || news.Author != "Mote莫特" || news.CoverImg != "https://oss.example.com/b.jpg" {
		t.Fatalf("AI 字段未被消费：summary=%q author=%q cover=%q", news.Summary, news.Author, news.CoverImg)
	}
	// 官方发布示例必填项：isLatest=1 才能进首页最新资讯；PUT 全量覆盖下零值会把
	// 这三项盖成关，文章直链可见但首页没有。
	if news.IsLatest != 1 || !news.ImgPreviewEnable || news.CommentEnable != 1 {
		t.Fatalf("首页/必填默认值不对：isLatest=%d imgPreview=%v comment=%d", news.IsLatest, news.ImgPreviewEnable, news.CommentEnable)
	}
	raw, err := json.Marshal(buildFoodTalksNewsBody(json.RawMessage(`""`), news))
	if err != nil {
		t.Fatalf("marshal site news: %v", err)
	}
	body := string(raw)
	for _, want := range []string{`"isLatest":1`, `"imgPreviewEnable":true`, `"commentEnable":1`, `"toc":""`} {
		if !strings.Contains(body, want) {
			t.Fatalf("请求体缺少 %s：%s", want, body)
		}
	}
	// newsTags：未注入 foodTalks 客户端时必须保持空（标签解析不可用就宁缺勿错）；
	// Mote 文章的自动标签契约见 publish_site_enhance_test.go（2026-08-14 PUT 标签
	// 缺陷复测已修复，契约 §9 矩阵解除封印）。
	if len(news.NewsTags) != 0 {
		t.Fatalf("无标签解析能力时 NewsTags 必须为空：%v", news.NewsTags)
	}
	// 外部公众号 → 转载（source_type=1），来源名=公众号名，author 不填就是空。
	if news.SourceType != 1 || news.SourceName != "测试公众号" {
		t.Fatalf("外部号应判转载且带来源名：type=%d name=%q", news.SourceType, news.SourceName)
	}
	if news.SourceID != 0 {
		t.Fatalf("未加载来源目录时 sourceId 必须为 0，实际 %d", news.SourceID)
	}
	job.Author = ""
	news, err = p.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if err != nil {
		t.Fatalf("buildSiteNews: %v", err)
	}
	if news.Author != "" {
		t.Fatalf("Author 为空不得回落 source_name（编辑部 132/163 作者为空），实际 %q", news.Author)
	}
	// 自家号 → 原创（source_type=0）。
	job.SourceName = "FBIF食品饮料创新"
	news, _ = p.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if news.SourceType != 0 || news.SourceName != "FBIF食品饮料创新" {
		t.Fatalf("自家号应判原创：type=%d name=%q", news.SourceType, news.SourceName)
	}
	// 自家号转发他人文章：AI 从正文声明抽到出处 → 按转载算，来源名=声明出处。
	job.AISource = "小食代"
	news, _ = p.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if news.SourceType != 1 || news.SourceName != "小食代" {
		t.Fatalf("声明出处应判转载：type=%d name=%q", news.SourceType, news.SourceName)
	}
	// 即使 AI 未抽到外部来源，自家号正文明确声明转载时，CMS 与排版仍必须一致按转载。
	job.AISource = ""
	job.BlocksJSON = `[{"k":"txt","runs":[{"type":"txt","text":"本文为转载文章。"}]}]`
	news, _ = p.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if news.SourceType != 1 {
		t.Fatalf("正文明确转载时 CMS sourceType 必须与排版一致：type=%d", news.SourceType)
	}
}

// TestBuildSiteNewsResolvesSourceID CMS「来源出处」下拉绑定 sourceId（tagTypeId=10），
// 只传 sourceName 时后台仍显示「请选择」。按名称命中目录后必须写入真实 id。
func TestBuildSiteNewsResolvesSourceID(t *testing.T) {
	t.Setenv("FOODTALKS_CATEGORY_IDS", "")
	p := &publishPipeline{now: time.Now}
	p.sourceTags = []foodTalksTag{
		{ID: 8801, Name: "窄播", TagTypeID: foodTalksTagTypeArticleSource},
		{ID: 12, Name: "FBIF食品饮料创新", TagTypeID: foodTalksTagTypeArticleSource},
	}
	p.sourceTagsLoaded = time.Now()
	job := publishJob{
		Title: "标题", SourceName: "窄播", ContentHTML: "<p>正文</p>",
		CreatedAt: "2026-07-10T00:00:00Z",
	}
	news, err := p.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if err != nil {
		t.Fatalf("buildSiteNews: %v", err)
	}
	if news.SourceID != 8801 || news.SourceName != "窄播" {
		t.Fatalf("应按名称选中来源出处：id=%d name=%q", news.SourceID, news.SourceName)
	}
	job.SourceName = "FBIF 食品饮料创新"
	news, _ = p.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if news.SourceID != 12 {
		t.Fatalf("来源名去空格后应命中自家号目录：id=%d", news.SourceID)
	}
	job.SourceName = "不存在的媒体"
	news, _ = p.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if news.SourceID != 0 {
		t.Fatalf("测试预置目录未命中不得编造 sourceId，实际 %d", news.SourceID)
	}
}

type recordingSourceTagStub struct {
	directFoodTalksStub
	listed    []foodTalksTag
	created   []string
	createID  int64
	createErr error
}

func (s *recordingSourceTagStub) ListTagsByType(context.Context, int64, string) ([]foodTalksTag, error) {
	return s.listed, nil
}

func (s *recordingSourceTagStub) SearchNewsByTitle(context.Context, string) ([]foodTalksNewsListItem, error) {
	return nil, nil
}

func (s *recordingSourceTagStub) SearchCompanies(context.Context, string) ([]foodTalksCompany, error) {
	return nil, nil
}

func (s *recordingSourceTagStub) CreateTagByName(_ context.Context, _ int64, name string) (int64, error) {
	s.created = append(s.created, name)
	if s.createErr != nil {
		return 0, s.createErr
	}
	return s.createID, nil
}

func TestBuildSiteNewsCreatesMissingSourceID(t *testing.T) {
	t.Setenv("FOODTALKS_CATEGORY_IDS", "")
	stub := &recordingSourceTagStub{createID: 115677}
	p := &publishPipeline{
		now:       time.Now,
		foodTalks: func() (foodTalksAPI, error) { return stub, nil },
	}
	job := publishJob{
		Title: "标题", SourceName: "凤凰网《风暴眼》", ContentHTML: "<p>正文</p>",
		CreatedAt: "2026-07-10T00:00:00Z",
	}
	news, err := p.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if err != nil {
		t.Fatalf("buildSiteNews: %v", err)
	}
	if news.SourceID != 115677 || news.SourceName != "凤凰网《风暴眼》" {
		t.Fatalf("目录没有时应按文章来源名创建：id=%d name=%q", news.SourceID, news.SourceName)
	}
	if len(stub.created) != 1 || stub.created[0] != "凤凰网《风暴眼》" {
		t.Fatalf("应创建一次来源标签，实际 %v", stub.created)
	}
	news, _ = p.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if len(stub.created) != 1 {
		t.Fatalf("同名应走缓存，不得再创建，实际 %v", stub.created)
	}
	stub.listed = []foodTalksTag{{ID: 14176, Name: "窄播"}}
	job.SourceName = "窄播"
	news, _ = p.buildSiteNews(context.Background(), job, foodTalksPublishStatusDraft)
	if news.SourceID != 14176 {
		t.Fatalf("目录已有应直接用原 id，实际 %d", news.SourceID)
	}
	if len(stub.created) != 1 {
		t.Fatalf("已有来源不得再创建，实际 %v", stub.created)
	}
}

// TestHTMLToPlainText 剥标签内核：块级标签转换行、实体解码、行首尾空白清理——
// 它决定摘要兜底与模型输入的质量。
func TestHTMLToPlainText(t *testing.T) {
	in := `<p>第一段&amp;文字</p><div> 第二段 </div><img src="x"><h2>小标题</h2>`
	want := "第一段&文字\n第二段\n小标题"
	if got := htmlToPlainText(in); got != want {
		t.Fatalf("htmlToPlainText = %q，应为 %q", got, want)
	}
	if got := truncateRunes("一二三四五", 3); got != "一二三" {
		t.Fatalf("truncateRunes 按字符截断错误：%q", got)
	}
}

// TestEnrichKeepsStructuredByline 结构化署名优先（M7 用户裁定「作者从公众号页面
// 直接提取更准」）：fetching 从页面 meta/var author 直取的署名落在 job.Author，
// enriching 的 AI 识别值**不得覆盖**它；只有页面未署名（byline 空）时 AI 才兜底。
func TestEnrichKeepsStructuredByline(t *testing.T) {
	env := newPublishTestEnv(t)
	env.pipeline.fetch = func(ctx context.Context, articleURL string) (publishFetchedArticle, error) {
		return publishFetchedArticle{Title: "测试标题", SourceName: "FBIF食品饮料创新", Byline: "Mote莫特", CanonicalURL: articleURL, BlocksJSON: testPublishBlocksJSON}, nil
	}
	env.pipeline.enrich = func(ctx context.Context, job publishJob) (publishEnrichment, error) {
		return publishEnrichment{Summary: "钩子摘要。", Author: "正文里猜的名字"}, nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Author != "Mote莫特" {
		t.Fatalf("结构化署名被 AI 覆盖：%q", got.Author)
	}
	if got.Summary != "钩子摘要。" {
		t.Fatalf("摘要应正常注入：%q", got.Summary)
	}
}

// newAIModelRouterStub MiMo 桩：按请求体 model 路由响应，failModels 里的 model 回指定
// HTTP 状态；记录每个 model 收到的请求次数与 user 内容类型（断言多模态入参用）。
func newAIModelRouterStub(t *testing.T, scripts map[string]string, failModels map[string]int) *sync.Map {
	t.Helper()
	seen := &sync.Map{}
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req aiStubRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("MiMo 桩解析请求体失败: %v", err)
		}
		var userContentTypes []string
		for _, in := range req.Input {
			if in.Role != "user" {
				continue
			}
			for _, c := range in.Content {
				userContentTypes = append(userContentTypes, c.Type)
			}
		}
		mu.Lock()
		n, _ := seen.LoadOrStore(req.Model+"::calls", 0)
		seen.Store(req.Model+"::calls", n.(int)+1)
		mu.Unlock()
		seen.Store(req.Model+"::content_types", userContentTypes)
		w.Header().Set("Content-Type", "application/json")
		if status := failModels[req.Model]; status != 0 {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":{"message":"模型服务不可用"}}`)
			return
		}
		text, ok := scripts[req.Model]
		if !ok {
			t.Errorf("MiMo 桩收到无剧本的 model %q", req.Model)
			text = "{}"
		}
		json.NewEncoder(w).Encode(map[string]any{
			"output": []any{map[string]any{"type": "message", "content": []any{
				map[string]any{"type": "output_text", "text": text},
			}}},
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MIMO_API_KEY", "test-mimo-key")
	t.Setenv("MIMO_BASE_URL", srv.URL)
	noAIRetryDelay(t)
	return seen
}

func noAIRetryDelay(t *testing.T) {
	t.Helper()
	old := aiRetryDelays
	aiRetryDelays = []time.Duration{0, 0}
	t.Cleanup(func() { aiRetryDelays = old })
}

func stubCalls(seen *sync.Map, model string) int {
	n, _ := seen.Load(model + "::calls")
	v, _ := n.(int)
	return v
}

func stubGotImage(seen *sync.Map, model string) bool {
	raw, _ := seen.Load(model + "::content_types")
	types, _ := raw.([]string)
	for _, typ := range types {
		if typ == "input_image" {
			return true
		}
	}
	return false
}

const aiEnrichTestHTML = `<p>正文内容，提到了饮料与食品。</p><img src="https://cdn.example.com/cover.jpg">`
const aiEnrichTestBlocks = `[{"k":"p","text":"正文内容，提到了饮料与食品。"},{"k":"img","src":"https://cdn.example.com/cover.jpg"}]`

// TestAIEnrichUsesVisionTierWithImages 模型分工（2026-10-07 用户裁定）：看图预填用
// mimo-v2.6-flash，且必须带上候选图本身；不得调用 Pro。
func TestAIEnrichUsesVisionTierWithImages(t *testing.T) {
	seen := newAIModelRouterStub(t, map[string]string{
		defaultMimoFlashModel: `{"cover_url":"https://cdn.example.com/cover.jpg","summary":"Flash 的摘要钩子。","tags":["饮料"],"author":"","source_name":"","notes":"说明"}`,
	}, nil)
	got, err := aiEnrichArticle(context.Background(), publishJob{Title: "测试标题", ContentHTML: aiEnrichTestHTML, BlocksJSON: aiEnrichTestBlocks})
	if err != nil {
		t.Fatalf("aiEnrichArticle: %v", err)
	}
	if got.Summary != "Flash 的摘要钩子。" || got.CoverURL != "https://cdn.example.com/cover.jpg" {
		t.Fatalf("应采用看图档结果：%+v", got)
	}
	if !stubGotImage(seen, defaultMimoFlashModel) {
		t.Fatal("看图档必须收到候选图片（input_image）才能选封面")
	}
	if n := stubCalls(seen, defaultMimoProModel); n != 0 {
		t.Fatalf("看图预填不得调用 Pro，实际 %d 次", n)
	}
}

// TestAIEnrichRetriesServerErrorsButNotClientErrors 500 按退避重试后失败交给规则兜底；
// 401 这类不可重试错误只打一次。
func TestAIEnrichRetriesServerErrorsButNotClientErrors(t *testing.T) {
	seen := newAIModelRouterStub(t, nil, map[string]int{defaultMimoFlashModel: http.StatusInternalServerError})
	if _, err := aiEnrichArticle(context.Background(), publishJob{Title: "标题", ContentHTML: "<p>正文零食。</p>"}); err == nil {
		t.Fatal("500 重试耗尽后应返回 error（由 stageEnrich 规则兜底）")
	}
	if n := stubCalls(seen, defaultMimoFlashModel); n != 1+len(aiRetryDelays) {
		t.Fatalf("500 应重试 %d 次，实际调用 %d 次", len(aiRetryDelays), n)
	}
	seen = newAIModelRouterStub(t, nil, map[string]int{defaultMimoFlashModel: http.StatusUnauthorized})
	if _, err := aiEnrichArticle(context.Background(), publishJob{Title: "标题", ContentHTML: "<p>正文零食。</p>"}); err == nil {
		t.Fatal("401 应返回 error")
	}
	if n := stubCalls(seen, defaultMimoFlashModel); n != 1 {
		t.Fatalf("401 不得重试，实际调用 %d 次", n)
	}
}

// footer263HTML 复刻 #263 的成品形态：原文配图在前，网站页脚（商务合作名片、加群
// 二维码、公众号二维码、logo）在后——页脚图来自规则素材，不在原文 blocks 里。
const footer263HTML = `<p>正文</p><img src="https://oss.example.com/a.png">` +
	`<p>*资讯投稿与商务合作，请联系Bobo。</p><img src="https://oss.example.com/bobo-card.webp" alt="Bobo二维码">` +
	`<img src="https://oss.example.com/group-qr.png"><img src="https://oss.example.com/follow.jpg">`

func footer263Job() publishJob {
	return publishJob{
		Title:       "全市场都在卷自营",
		ContentHTML: footer263HTML,
		BlocksJSON:  `[{"k":"p","text":"正文"},{"k":"img","src":"https://mmbiz.qpic.cn/mmbiz_png/body-a?wx_fmt=png&amp;from=appmsg"}]`,
		ImageMap: `{"https://mmbiz.qpic.cn/mmbiz_png/body-a?wx_fmt=png&from=appmsg":"https://oss.example.com/a.png",` +
			`"https://fbifmp-layout.foodtalks.cn/assets/fbif/bobo-card.webp":"https://oss.example.com/bobo-card.webp",` +
			`"https://static.foodtalks.cn/image/news/qr.png":"https://oss.example.com/group-qr.png",` +
			`"https://static.foodtalks.cn/image/post/follow.jpg":"https://oss.example.com/follow.jpg"}`,
	}
}

// TestEnrichCandidatesExcludeSiteFooterImages #263 回归：页脚名片/二维码永远不进封面候选；
// 当前封面（公众号原封面）排第一。
func TestEnrichCandidatesExcludeSiteFooterImages(t *testing.T) {
	job := footer263Job()
	job.CoverURL = "https://oss.example.com/wechat-cover.jpg"
	got := enrichCandidateImages(job)
	want := []string{"https://oss.example.com/wechat-cover.jpg", "https://oss.example.com/a.png"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("候选图应只含公众号原封面 + 原文配图，实际 %v", got)
	}
}

// TestAIResponsesRetriesRateLimit 429（账号级 100 RPM）退避后重试成功。
func TestAIResponsesRetriesRateLimit(t *testing.T) {
	var calls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		fmt.Fprint(w, "\n\n"+`{"output":[{"type":"message","content":[{"type":"output_text","text":"好"}]}]}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MIMO_API_KEY", "k")
	t.Setenv("MIMO_BASE_URL", srv.URL)
	noAIRetryDelay(t)
	text, err := aiResponsesCall(context.Background(), aiModel(aiTierFlash), "sys", []map[string]any{{"type": "input_text", "text": "hi"}})
	if err != nil || text != "好" || calls != 2 {
		t.Fatalf("429 应重试一次后成功（且容忍保活换行）：text=%q err=%v calls=%d", text, err, calls)
	}
}

// TestAIRewriteCallTurnsThinkingOff 改写短文字走 Chat Completions、带 thinking.type=disabled（Responses 接口不认它，
// 2026-11-03 #340 缩一条图注想了 80 秒），429 照样退避重试。
func TestAIRewriteCallTurnsThinkingOff(t *testing.T) {
	var calls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string              `json:"model"`
			Messages []map[string]string `json:"messages"`
			Thinking map[string]string   `json:"thinking"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("解析请求体失败: %v", err)
		}
		if r.URL.Path != mimoChatPath || body.Thinking["type"] != "disabled" || body.Model != "mimo-v2.6-flash" ||
			len(body.Messages) != 2 || body.Messages[0]["role"] != "system" || body.Messages[0]["content"] != "sys" ||
			body.Messages[1]["role"] != "user" || body.Messages[1]["content"] != "原文" {
			t.Errorf("请求不对：path=%s body=%+v", r.URL.Path, body)
		}
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":" 缩好的 \n"}}]}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MIMO_API_KEY", "k")
	t.Setenv("MIMO_BASE_URL", srv.URL)
	t.Setenv("MIMO_FLASH_MODEL", "")
	noAIRetryDelay(t)
	text, err := aiRewriteCall(context.Background(), aiModel(aiTierFlash), "sys", "原文")
	if err != nil || text != "缩好的" || calls != 2 {
		t.Fatalf("应重试一次后拿到回答：text=%q err=%v calls=%d", text, err, calls)
	}
	for _, c := range []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusBadRequest, `{"error":{"message":"bad"}}`, "HTTP 400"},
		{http.StatusOK, `{"choices":[]}`, "没有模型输出文本"},
		{http.StatusOK, `{"error":{"message":"额度用完"}}`, "额度用完"},
	} {
		if _, err := aiExtractChatText([]byte(c.body), c.status); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%d %s 应报含「%s」的错，实际 %v", c.status, c.body, c.want, err)
		}
	}
}

// TestAIModelTiers 默认档位与 env 覆盖。
func TestAIModelTiers(t *testing.T) {
	t.Setenv("MIMO_PRO_MODEL", "")
	t.Setenv("MIMO_FLASH_MODEL", "")
	if aiModel(aiTierPro) != "mimo-v2.6-pro" || aiModel(aiTierFlash) != "mimo-v2.6-flash" {
		t.Fatalf("默认档位不符：pro=%q flash=%q", aiModel(aiTierPro), aiModel(aiTierFlash))
	}
	t.Setenv("MIMO_PRO_MODEL", "mimo-v2.6-pro-ultraspeed")
	if aiModel(aiTierPro) != "mimo-v2.6-pro-ultraspeed" {
		t.Fatalf("MIMO_PRO_MODEL 覆盖未生效：%q", aiModel(aiTierPro))
	}
}

// TestPublishFetchSeedsWechatCoverAsDefault 抓取阶段带回的公众号原封面成为默认封面
// （AI 未改选时即最终封面），与正文首图无关。
func TestPublishFetchSeedsWechatCoverAsDefault(t *testing.T) {
	env := newPublishTestEnv(t)
	env.pipeline.fetch = func(ctx context.Context, articleURL string) (publishFetchedArticle, error) {
		return publishFetchedArticle{Title: "测试标题", SourceName: "测试公众号", CanonicalURL: articleURL,
			BlocksJSON: testPublishBlocksJSON, CoverURL: "https://mmbiz.qpic.cn/wechat-cover.jpg"}, nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("stage = %q（error=%q）", got.Stage, got.Error)
	}
	if got.CoverURL != ossURLFor("https://mmbiz.qpic.cn/wechat-cover.jpg") {
		t.Fatalf("默认封面应为转存后的公众号原封面，实际 %q", got.CoverURL)
	}
}

// TestPublishMirrorNeverDefaultsCoverToFooterImage #5 回归：原文无配图、页面也没带封面时，
// 成品里只剩网站页脚名片/二维码——不得把它们兜底成封面，必须显式失败给出路。
func TestPublishMirrorNeverDefaultsCoverToFooterImage(t *testing.T) {
	env := newPublishTestEnv(t)
	env.pipeline.fetch = func(ctx context.Context, articleURL string) (publishFetchedArticle, error) {
		return publishFetchedArticle{Title: "沃集鲜推出药食同源系列", SourceName: "测试公众号", CanonicalURL: articleURL,
			BlocksJSON: `[{"k":"p","text":"正文没有配图"}]`}, nil
	}
	env.pipeline.render = func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
		return successfulPublishRenderResult(`<p>正文没有配图</p><p>*商务合作请联系Bobo。</p>`+
			`<img src="https://mmbiz.qpic.cn/bobo-card.webp" alt="Bobo二维码"><img src="https://mmbiz.qpic.cn/group-qr.png">`,
			testPublishResolvedRule()), nil
	}
	job, _, _ := env.pipeline.submit(testPublishSourceURL)
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageFailed || got.FailedStage != publishStageMirroring {
		t.Fatalf("只有页脚图时应在 mirroring 显式失败，实际 stage=%q failed=%q cover=%q", got.Stage, got.FailedStage, got.CoverURL)
	}
	if !strings.Contains(got.Error, "页脚名片") {
		t.Fatalf("失败原因应说明页脚图不能当封面：%q", got.Error)
	}
}

// TestPublishMirrorDefaultCoverSkipsFooterImage 没有公众号封面时，默认封面取原文自带
// 的第一张图，即使页脚图在成品里排得更靠前也不选。
func TestPublishMirrorDefaultCoverSkipsFooterImage(t *testing.T) {
	env := newPublishTestEnv(t)
	env.pipeline.render = func(ctx context.Context, title, source, blocksJSON string, isRepost bool) (publishRenderResult, error) {
		return successfulPublishRenderResult(`<img src="https://mmbiz.qpic.cn/site-banner.png"><p>正文</p>`+
			`<img src="https://mmbiz.qpic.cn/a.jpg"><img src="https://mmbiz.qpic.cn/b.jpg">`, testPublishResolvedRule()), nil
	}
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.CoverURL != ossURLFor("https://mmbiz.qpic.cn/a.jpg") {
		t.Fatalf("默认封面应为原文第一张图，实际 %q（stage=%q error=%q）", got.CoverURL, got.Stage, got.Error)
	}
}

// TestLegacyFooterCoverIsDiscarded 旧任务遗留的页脚名片封面（#263）不得以「当前封面」
// 身份混进候选，mirroring 也要作废它、改取原文首图。
func TestLegacyFooterCoverIsDiscarded(t *testing.T) {
	job := footer263Job()
	job.CoverURL = "https://oss.example.com/bobo-card.webp"
	got := enrichCandidateImages(job)
	if strings.Join(got, "|") != "https://oss.example.com/a.png" {
		t.Fatalf("页脚名片封面必须作废，候选应只剩原文配图，实际 %v", got)
	}

	env := newPublishTestEnv(t)
	submitted, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	// 模拟旧任务回到 mirroring：成品带页脚名片，封面字段停在名片上。
	legacy, err := env.store.update(submitted.ID, func(j *publishJob) {
		j.Stage = publishStageMirroring
		j.ContentHTML = testPublishRenderedHTML + `<img src="https://mmbiz.qpic.cn/bobo-card.webp">`
		j.CoverURL = "https://mmbiz.qpic.cn/bobo-card.webp"
		j.ImageMap = ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.pipeline.stageMirror(context.Background(), legacy); err != nil {
		t.Fatalf("stageMirror: %v", err)
	}
	mirrored, _ := env.store.byID(submitted.ID)
	if mirrored.CoverURL != ossURLFor("https://mmbiz.qpic.cn/a.jpg") {
		t.Fatalf("mirroring 应作废页脚名片封面并取原文首图，实际 %q", mirrored.CoverURL)
	}
}
