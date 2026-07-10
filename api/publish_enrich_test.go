package main

// publish_enrich_test.go — M5「AI 预填发布参数」测试（ARK 走 httptest 桩，绝不触真实
// API）。锁死三条契约：
//   1. enriching 只产元数据：AI 成功注入 summary/tags/author/ai_notes、可换封面，
//      但 content_html 必须逐字节不变（AI 没有正文篡改面）；
//   2. AI 是加速器不是闸门：ARK 失败走规则兜底，job 照常推进到 awaiting_review，
//      绝不 fail；
//   3. buildSiteNews 消费 AI 字段（Summary/Author/NewsTags/CoverImg）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
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

// arkStubRequest ARK 桩收到的请求体（断言请求形态用）。
type arkStubRequest struct {
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

// newARKStub 起 ARK responses API 桩并把 env 指向它（arkEnrichArticle 走真实客户端代码）。
// respond 返回 (HTTP 状态码, 模型输出文本)；文本为空时回 error 响应体。
func newARKStub(t *testing.T, respond func() (int, string)) *arkStubRequest {
	t.Helper()
	got := &arkStubRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != arkResponsesPath {
			t.Errorf("ARK 桩收到意外路径 %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-ark-key" {
			t.Errorf("ARK 桩收到错误鉴权头 %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			t.Errorf("ARK 桩解析请求体失败: %v", err)
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
	t.Setenv("ARK_API_KEY", "test-ark-key")
	t.Setenv("ARK_BASE_URL", srv.URL)
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
	env.pipeline.render = func(ctx context.Context, title, source, blocksJSON string) (string, error) {
		return renderedWithAuthor, nil
	}
	aiCover := ossURLFor("https://mmbiz.qpic.cn/b.jpg")
	gotReq := newARKStub(t, func() (int, string) {
		return http.StatusOK, "```json\n" + fmt.Sprintf(
			`{"cover_url":%q,"summary":"这是一段模型生成的文章摘要。","tags":["食品","饮料","新品"],"author":"Mote莫特","notes":"封面选第二张图，构图更完整。正文格式规范。摘要取自导语。"}`,
			aiCover) + "\n```"
	})
	t.Setenv("ARK_MODEL_ID", "ep-test-model")
	env.pipeline.enrich = arkEnrichArticle

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
	if gotReq.Model != "ep-test-model" {
		t.Fatalf("model = %q，应取 ARK_MODEL_ID", gotReq.Model)
	}
	if len(gotReq.Input) != 2 || gotReq.Input[0].Role != "system" {
		t.Fatalf("input 形态不符：%+v", gotReq.Input)
	}
	if !strings.Contains(gotReq.Input[0].Content[0].Text, "请用简体中文回复") {
		t.Fatalf("system 提示必须含「请用简体中文回复」：%q", gotReq.Input[0].Content[0].Text)
	}
	var imageURLs []string
	userText := ""
	for _, c := range gotReq.Input[1].Content {
		switch c.Type {
		case "input_image":
			imageURLs = append(imageURLs, c.ImageURL)
		case "input_text":
			userText = c.Text
		}
	}
	if len(imageURLs) != 2 || imageURLs[0] != ossURLFor("https://mmbiz.qpic.cn/a.jpg") {
		t.Fatalf("input_image 候选应为 OSS 化正文图：%v", imageURLs)
	}
	if !strings.Contains(userText, "测试标题") || !strings.Contains(userText, "正文") {
		t.Fatalf("user 文本应含标题与纯文本正文：%q", userText)
	}
}

// TestPublishEnrichFallbackOnARKFailure ARK 挂了（HTTP 500）：走规则兜底
// （摘要=正文前 40 字、author 空【编辑部宁空勿造，规范 §5】、tags 空、notes 说明
// 未执行原因），job 绝不 fail，照常推进到 awaiting_review、CreateNews 恰一次。
func TestPublishEnrichFallbackOnARKFailure(t *testing.T) {
	env := newPublishTestEnv(t)
	newARKStub(t, func() (int, string) { return http.StatusInternalServerError, "" })
	env.pipeline.enrich = arkEnrichArticle

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

// TestPublishEnrichKeyMissingFallback ARK_API_KEY 未配置：不发任何请求直接走兜底
// （newPublishTestEnv 未注入 enrich 时同样兜底——两种缺配置形态都不阻断流水线）。
func TestPublishEnrichKeyMissingFallback(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("ARK_API_KEY", "")
	env.pipeline.enrich = arkEnrichArticle
	job, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := env.store.byID(job.ID)
	if got.Stage != publishStageAwaitingReview {
		t.Fatalf("缺 ARK_API_KEY 不得 fail job，实际 stage=%q", got.Stage)
	}
	if !strings.Contains(got.AINotes, "ARK_API_KEY 未配置") {
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
	news, err := p.buildSiteNews(job, foodTalksPublishStatusDraft)
	if err != nil {
		t.Fatalf("buildSiteNews: %v", err)
	}
	if news.Summary != "AI 摘要" || news.Author != "Mote莫特" || news.CoverImg != "https://oss.example.com/b.jpg" {
		t.Fatalf("AI 字段未被消费：summary=%q author=%q cover=%q", news.Summary, news.Author, news.CoverImg)
	}
	// newsTags 恒空：结构已破解（[{tagId,tagName}]），但后端 PUT 带标签必 500 且会把
	// 文章污染成「任何 PUT 都 500」（翻公开卡死），契约 §9 矩阵。修复前这条断言守住恒空。
	if len(news.NewsTags) != 0 {
		t.Fatalf("NewsTags 必须恒空（后端结构未知，字符串数组会 500）：%v", news.NewsTags)
	}
	// 外部公众号 → 转载（source_type=1），来源名=公众号名，author 不填就是空。
	if news.SourceType != 1 || news.SourceName != "测试公众号" {
		t.Fatalf("外部号应判转载且带来源名：type=%d name=%q", news.SourceType, news.SourceName)
	}
	job.Author = ""
	news, err = p.buildSiteNews(job, foodTalksPublishStatusDraft)
	if err != nil {
		t.Fatalf("buildSiteNews: %v", err)
	}
	if news.Author != "" {
		t.Fatalf("Author 为空不得回落 source_name（编辑部 132/163 作者为空），实际 %q", news.Author)
	}
	// 自家号 → 原创（source_type=0）。
	job.SourceName = "FBIF食品饮料创新"
	news, _ = p.buildSiteNews(job, foodTalksPublishStatusDraft)
	if news.SourceType != 0 || news.SourceName != "FBIF食品饮料创新" {
		t.Fatalf("自家号应判原创：type=%d name=%q", news.SourceType, news.SourceName)
	}
	// 自家号转发他人文章：AI 从正文声明抽到出处 → 按转载算，来源名=声明出处。
	job.AISource = "小食代"
	news, _ = p.buildSiteNews(job, foodTalksPublishStatusDraft)
	if news.SourceType != 1 || news.SourceName != "小食代" {
		t.Fatalf("声明出处应判转载：type=%d name=%q", news.SourceType, news.SourceName)
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
	env.pipeline.fetch = func(ctx context.Context, articleURL string) (string, string, string, string, string, error) {
		return "测试标题", "FBIF食品饮料创新", "Mote莫特", articleURL, testPublishBlocksJSON, nil
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

// newARKModelRouterStub 双模型场景的 ARK 桩：按请求体 model 路由响应（A/B/判决三个
// 端点各自剧本），并记录每个 model 收到的完整请求文本（断言判决输入形态用）。
func newARKModelRouterStub(t *testing.T, scripts map[string]string, failModels map[string]bool) *sync.Map {
	t.Helper()
	seen := &sync.Map{} // model → 拼接后的 user 文本
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req arkStubRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("ARK 桩解析请求体失败: %v", err)
		}
		var userText string
		for _, in := range req.Input {
			for _, c := range in.Content {
				if c.Type == "input_text" {
					userText += in.Role + "::" + c.Text + "\n"
				}
			}
		}
		seen.Store(req.Model, userText)
		w.Header().Set("Content-Type", "application/json")
		if failModels[req.Model] {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"模型服务不可用"}}`)
			return
		}
		text, ok := scripts[req.Model]
		if !ok {
			t.Errorf("ARK 桩收到无剧本的 model %q", req.Model)
			text = "{}"
		}
		json.NewEncoder(w).Encode(map[string]any{
			"output": []any{map[string]any{"type": "message", "content": []any{
				map[string]any{"type": "output_text", "text": text},
			}}},
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ARK_API_KEY", "test-ark-key")
	t.Setenv("ARK_BASE_URL", srv.URL)
	return seen
}

// TestDualModelJudgeArbitrates 双模型对比 + 判决节点（2026-07-10 用户裁定）：
// A/B 并行各出方案 → 判决模型收到匿名的方案一/方案二与编辑部规范 → 逐字段裁决；
// 最终结果=判决产出，verdict 判决说明并入 notes（审核人可见两案对比结论）。
func TestDualModelJudgeArbitrates(t *testing.T) {
	seen := newARKModelRouterStub(t, map[string]string{
		"ep-A": `{"cover_url":"","summary":"方案A的摘要钩子。","tags":["食品"],"author":"","source_name":"","notes":"A案说明"}`,
		"ep-B": `{"cover_url":"","summary":"方案B的摘要钩子。","tags":["饮料"],"author":"","source_name":"","notes":"B案说明"}`,
		"ep-J": `{"cover_url":"","summary":"方案B的摘要钩子。","tags":["饮料"],"author":"","source_name":"","notes":"综合两案：摘要取更凝练的一版。","verdict":"摘要与标签采用方案二：更贴近一句话钩子规范；其余字段两案一致。"}`,
	}, nil)
	t.Setenv("ARK_MODEL_ID", "ep-A")
	t.Setenv("ARK_MODEL_ID_B", "ep-B")
	t.Setenv("ARK_JUDGE_MODEL_ID", "ep-J")
	got, err := arkEnrichArticle(context.Background(), publishJob{
		Title: "测试标题", ContentHTML: "<p>正文内容，提到了饮料与食品。</p>",
	})
	if err != nil {
		t.Fatalf("arkEnrichArticle: %v", err)
	}
	if got.Summary != "方案B的摘要钩子。" || len(got.Tags) != 1 || got.Tags[0] != "饮料" {
		t.Fatalf("最终结果应为判决产出：%+v", got)
	}
	if !strings.Contains(got.Notes, "【双模型判决】") || !strings.Contains(got.Notes, "采用方案二") {
		t.Fatalf("notes 应并入判决说明：%q", got.Notes)
	}
	rawJ, ok := seen.Load("ep-J")
	if !ok {
		t.Fatal("判决模型未被调用")
	}
	judgeText := rawJ.(string)
	// 判决输入必须匿名带两案 + 文章 + 规范（判决标准）。
	for _, want := range []string{"方案一", "方案二", "方案A的摘要钩子。", "方案B的摘要钩子。", "测试标题", "编辑部规范"} {
		if !strings.Contains(judgeText, want) {
			t.Fatalf("判决输入缺 %q", want)
		}
	}
	for _, banned := range []string{"ep-A", "ep-B", "豆包", "DeepSeek", "Doubao"} {
		if strings.Contains(judgeText, banned) {
			t.Fatalf("判决输入泄漏模型身份 %q（必须匿名保证公平）", banned)
		}
	}
}

func TestDualModelJudgeCannotInventOrMixFields(t *testing.T) {
	newARKModelRouterStub(t, map[string]string{
		"ep-A": `{"cover_url":"","summary":"A摘要。","tags":["食品","饮料"],"author":"","source_name":"","notes":"A案"}`,
		"ep-B": `{"cover_url":"","summary":"B摘要。","tags":["零食"],"author":"","source_name":"","notes":"B案"}`,
		"ep-J": `{"cover_url":"","summary":"判决节点自创摘要。","tags":["食品","零食"],"author":"虚构作者","source_name":"虚构来源","notes":"判决说明","verdict":"自行综合"}`,
	}, nil)
	t.Setenv("ARK_MODEL_ID", "ep-A")
	t.Setenv("ARK_MODEL_ID_B", "ep-B")
	t.Setenv("ARK_JUDGE_MODEL_ID", "ep-J")

	got, err := arkEnrichArticle(context.Background(), publishJob{
		Title: "测试标题", ContentHTML: "<p>正文提到了食品、饮料与零食，但没有作者和出处。</p>",
	})
	if err != nil {
		t.Fatalf("arkEnrichArticle: %v", err)
	}
	if got.Summary != "A摘要。" || !reflect.DeepEqual(got.Tags, []string{"食品", "饮料"}) {
		t.Fatalf("自创/混拼字段必须回退方案一：%+v", got)
	}
	if got.Author != "" || got.SourceName != "" {
		t.Fatalf("作者/出处不得脱离两案与正文证据：%+v", got)
	}
	for _, field := range []string{"摘要", "标签", "作者", "出处"} {
		if !strings.Contains(got.Notes, field) {
			t.Fatalf("notes 应留痕无效字段 %q：%q", field, got.Notes)
		}
	}
}

// TestDualModelOneSideFailureDegrades 一侧模型失败：不进判决、直接采用存活侧方案，
// notes 说明降级原因——多模型是增强器不是新故障点。
func TestDualModelOneSideFailureDegrades(t *testing.T) {
	newARKModelRouterStub(t, map[string]string{
		"ep-B": `{"cover_url":"","summary":"存活侧摘要。","tags":["零食"],"author":"","source_name":"","notes":"B案说明"}`,
	}, map[string]bool{"ep-A": true})
	t.Setenv("ARK_MODEL_ID", "ep-A")
	t.Setenv("ARK_MODEL_ID_B", "ep-B")
	t.Setenv("ARK_JUDGE_MODEL_ID", "ep-J")
	got, err := arkEnrichArticle(context.Background(), publishJob{Title: "标题", ContentHTML: "<p>正文零食。</p>"})
	if err != nil {
		t.Fatalf("单侧失败不应整体失败: %v", err)
	}
	if got.Summary != "存活侧摘要。" || !strings.Contains(got.Notes, "主模型失败") {
		t.Fatalf("应采用存活侧并注明降级：%+v", got)
	}
}
