package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoadYunsuoConfigAndRedactSecret(t *testing.T) {
	t.Setenv(yunsuoBaseURLEnv, "")
	t.Setenv(yunsuoUserCodeEnv, "user")
	t.Setenv(yunsuoAPIKeyEnv, "never-print-this-key")
	t.Setenv(yunsuoTargetAccountsEnv, `[{"id":"11111111-1111-4111-8111-111111111111","name":"头条号"},{"id":"22222222-2222-4222-8222-222222222222","name":"百家号"}]`)
	t.Setenv(yunsuoCategoriesEnv, "美食,饮食文化,财经,消费,公司")

	cfg, err := loadYunsuoConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != defaultYunsuoBaseURL || len(cfg.Targets) != 2 || len(cfg.Categories) != 5 {
		t.Fatalf("配置解析不符合预期：%v", cfg)
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), cfg.APIKey) || strings.Contains(cfg.String(), cfg.APIKey) {
		t.Fatalf("配置不得回显 API key：json=%s string=%s", encoded, cfg.String())
	}
}

func TestLoadYunsuoConfigRejectsPlainHTTPPublicBaseURL(t *testing.T) {
	setRequired := func() {
		t.Setenv(yunsuoUserCodeEnv, "user")
		t.Setenv(yunsuoAPIKeyEnv, "secret")
		t.Setenv(yunsuoTargetAccountsEnv, `[{"id":"11111111-1111-4111-8111-111111111111","name":"头条号"}]`)
		t.Setenv(yunsuoCategoriesEnv, "美食,饮食文化,财经,消费,公司")
	}
	setRequired()
	t.Setenv(yunsuoBaseURLEnv, "http://example.com/v1")
	if _, err := loadYunsuoConfig(); err == nil {
		t.Fatal("公网云梭地址不得使用明文 HTTP")
	}

	// 本地临时服务用于单测/开发，按共用 configuredHTTPBaseURL
	// 口径保留 HTTP 回环例外。
	t.Setenv(yunsuoBaseURLEnv, "http://127.0.0.1:19090/v1")
	if _, err := loadYunsuoConfig(); err != nil {
		t.Fatalf("本机回环 HTTP 应允许：%v", err)
	}
}

func TestYunsuoSignatureUsesDocumentedOrder(t *testing.T) {
	const expected = "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"
	if got := yunsuoSignature("a", "b", "c"); got != expected {
		t.Fatalf("SHA512(API_KEY+Nonce+USER_CODE) = %s", got)
	}
}

func TestYunsuoClientPushStatusAndErrorClasses(t *testing.T) {
	const apiKey = "private-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Nonce") != "fixed-nonce" || r.Header.Get("User-Code") != "user" {
			t.Errorf("认证头缺失：%v", r.Header)
		}
		if r.Header.Get("Signature") != yunsuoSignature(apiKey, "fixed-nonce", "user") {
			t.Errorf("签名不符合文档顺序")
		}
		switch r.URL.Path {
		case "/v1/distribution/push":
			_, _ = w.Write([]byte(`{"code":1,"msg":"ok","data":{"content_pk":"content-1"}}`))
		case "/v1/distribution/status":
			_, _ = w.Write([]byte(`{"code":1,"msg":"ok","data":[{"pk":"record-1","platform_name":"今日头条","publish_time":"2026-08-11 15:30:00","status":1,"remark":"ok","accountPool":{"id":"a-1","name":"头条号"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newYunsuoClient(yunsuoConfig{BaseURL: server.URL + "/v1", UserCode: "user", APIKey: apiKey})
	client.nonce = func() (string, error) { return "fixed-nonce", nil }
	contentPK, err := client.Push(context.Background(), yunsuoPushPayload{Type: 0, Title: "title"})
	if err != nil || contentPK != "content-1" {
		t.Fatalf("Push = %q, %v", contentPK, err)
	}
	statuses, err := client.Status(context.Background(), contentPK)
	if err != nil || len(statuses) != 1 || statuses[0].AccountPool.ID != "a-1" || statuses[0].Status != 1 ||
		statuses[0].PK != "record-1" || statuses[0].PlatformName != "今日头条" || statuses[0].PublishTime != "2026-08-11 15:30:00" {
		t.Fatalf("Status = %#v, %v", statuses, err)
	}

	rejectedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":-1,"msg":"bad private-key","data":null}`))
	}))
	defer rejectedServer.Close()
	rejectedClient := newYunsuoClient(yunsuoConfig{BaseURL: rejectedServer.URL, UserCode: "user", APIKey: apiKey})
	rejectedClient.nonce = func() (string, error) { return "fixed-nonce", nil }
	_, err = rejectedClient.Push(context.Background(), yunsuoPushPayload{})
	if !isYunsuoUncertainError(err) || isYunsuoRejectedError(err) || strings.Contains(err.Error(), apiKey) {
		t.Fatalf("Push 负码不能证明未受理，必须标记不确定且脱敏：%v", err)
	}

	serverError := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":-1,"msg":"temporary failure","data":null}`))
	}))
	defer serverError.Close()
	serverErrorClient := newYunsuoClient(yunsuoConfig{BaseURL: serverError.URL, UserCode: "user", APIKey: apiKey})
	serverErrorClient.nonce = func() (string, error) { return "fixed-nonce", nil }
	_, err = serverErrorClient.Push(context.Background(), yunsuoPushPayload{})
	if !isYunsuoUncertainError(err) || isYunsuoRejectedError(err) {
		t.Fatalf("HTTP 5xx 即使携带负业务码也必须标记结果不确定：%v", err)
	}

	for name, body := range map[string]string{
		"missing":  `{"msg":"ok","data":{"content_pk":"possibly-created"}}`,
		"zero":     `{"code":0,"msg":"unknown","data":{"content_pk":"possibly-created"}}`,
		"negative": `{"code":-1,"msg":"service error","data":{"content_pk":"possibly-created"}}`,
	} {
		t.Run("unknown-code-"+name, func(t *testing.T) {
			unknownCodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer unknownCodeServer.Close()
			unknownCodeClient := newYunsuoClient(yunsuoConfig{BaseURL: unknownCodeServer.URL, UserCode: "user", APIKey: apiKey})
			unknownCodeClient.nonce = func() (string, error) { return "fixed-nonce", nil }
			contentPK, unknownErr := unknownCodeClient.Push(context.Background(), yunsuoPushPayload{})
			if unknownErr != nil || contentPK != "possibly-created" {
				t.Fatalf("响应只要含 content_pk 就必须优先进入对账：pk=%q err=%v", contentPK, unknownErr)
			}
		})
	}

	topLevelPKServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":-1,"msg":"conflict","content_pk":"top-level-created","data":null}`))
	}))
	defer topLevelPKServer.Close()
	topLevelPKClient := newYunsuoClient(yunsuoConfig{BaseURL: topLevelPKServer.URL, UserCode: "user", APIKey: apiKey})
	topLevelPKClient.nonce = func() (string, error) { return "fixed-nonce", nil }
	contentPK, err = topLevelPKClient.Push(context.Background(), yunsuoPushPayload{})
	if err != nil || contentPK != "top-level-created" {
		t.Fatalf("4xx 负码若带顶层 content_pk 也必须继续对账：pk=%q err=%v", contentPK, err)
	}

	uncertainServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":1,"msg":"ok","data":{}}`))
	}))
	defer uncertainServer.Close()
	uncertainClient := newYunsuoClient(yunsuoConfig{BaseURL: uncertainServer.URL, UserCode: "user", APIKey: apiKey})
	uncertainClient.nonce = func() (string, error) { return "fixed-nonce", nil }
	_, err = uncertainClient.Push(context.Background(), yunsuoPushPayload{})
	if !isYunsuoUncertainError(err) || isYunsuoRejectedError(err) {
		t.Fatalf("成功响应缺 content_pk 必须标记结果不确定：%v", err)
	}
}

func TestYunsuoClientDoesNotForwardSignedHeadersAcrossRedirect(t *testing.T) {
	received := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		received = true
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	client := newYunsuoClient(yunsuoConfig{BaseURL: redirector.URL, UserCode: "user", APIKey: "secret"})
	client.nonce = func() (string, error) { return "fixed-nonce", nil }
	_, err := client.Push(context.Background(), yunsuoPushPayload{})
	if received || !isYunsuoUncertainError(err) {
		t.Fatalf("签名请求不得跟随 redirect：received=%v err=%v", received, err)
	}
}

func TestRenderAndBuildYunsuoPayloadStayChannelNeutral(t *testing.T) {
	blocks := `[
		{"k":"h","level":2,"text":"章节 <一>"},
		{"k":"p","runs":[{"type":"txt","text":"正文 & ","bold":true},{"type":"link","text":"不安全链接","href":"javascript:alert(1)"}]},
		{"k":"img","src":"https://origin.example/image.jpg"},
		{"k":"li","runs":[{"type":"txt","text":"第一项"}]},
		{"k":"li","runs":[{"type":"link","text":"第二项","href":"https://example.com/?a=1&b=2"}]},
		{"k":"table","rows":[[{"header":true,"runs":[{"type":"txt","text":"表头"}]}],[{"runs":[{"type":"txt","text":"<script>"}]}]]},
		{"k":"video","src":"https://cdn.example/video.mp4","poster":"https://origin.example/poster.jpg"}
	]`
	imageMap := `{"https://origin.example/image.jpg":"https://cdn.example/image.jpg","https://origin.example/poster.jpg":"https://cdn.example/poster.jpg"}`
	rendered, err := renderYunsuoHTML(blocks, imageMap)
	if err != nil {
		t.Fatal(err)
	}
	for _, wanted := range []string{"<h2>章节 &lt;一&gt;</h2>", "<ul><li>第一项</li><li>第二项</li></ul>", "<table", "https://cdn.example/poster.jpg", "https://cdn.example/image.jpg"} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("中性 HTML 缺少 %q：%s", wanted, rendered)
		}
	}
	if strings.Contains(strings.ToLower(rendered), "javascript:") || strings.Contains(rendered, "<a ") ||
		strings.Contains(rendered, "<script>") || strings.Contains(rendered, "FoodTalks") {
		t.Fatalf("中性 HTML 不得包含活性链接、脚本或网站耦合：%s", rendered)
	}

	job := publishJob{
		Title:      "新品趋势",
		SourceName: "FBIF食品饮料创新",
		Summary:    "这是一段用于补全云梭长标题的真实文章摘要。",
		Tags:       []string{"饮料", "新品"},
		BlocksJSON: blocks,
		ImageMap:   imageMap,
		CoverURL:   "https://origin.example/image.jpg",
	}
	payload, err := buildYunsuoPushPayload(job,
		[]yunsuoAccountTarget{{ID: "a-1", Name: "头条号"}, {ID: "a-2", Name: "百家号"}},
		[]string{"美食", "饮食文化", "财经", "消费", "公司"})
	if err != nil {
		t.Fatal(err)
	}
	shortLen, titleLen, longLen := len([]rune(payload.ShortTitle)), len([]rune(payload.Title)), len([]rune(payload.LongTitle))
	if shortLen < 8 || shortLen > 20 || titleLen < 15 || titleLen > 40 || longLen < 20 || longLen > 60 {
		t.Fatalf("云梭标题长度越界：short=%d title=%d long=%d", shortLen, titleLen, longLen)
	}
	if payload.Cover != "https://cdn.example/image.jpg" || len(payload.AccountIDs) != 2 || payload.Type != 0 {
		t.Fatalf("推送快照不符合预期：%#v", payload)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["content_html"]; ok {
		t.Fatalf("推送必须用官方字段 html，不得再发 content_html：%s", encoded)
	}
	html, _ := wire["html"].(string)
	if strings.TrimSpace(html) == "" {
		t.Fatalf("推送缺少 html：%s", encoded)
	}
}

// manualLayoutTestJob 照编辑手发稿拼的一篇：署名、带图注的首图、链接、整段加粗、两级小标题、
// 作者名片与参考来源。
func manualLayoutTestJob() publishJob {
	blocks := `[
		{"k":"txt","text":"作者：Sage，编辑：Bobo。"},
		{"k":"txt","blankBefore":true,"text":"一个人，可以在同一赛道成功2次吗？"},
		{"k":"img","blankBefore":true,"src":"https://cdn.example/lead.jpg"},
		{"k":"cap","text":"蛋白棒RXBAR和David"},
		{"k":"cap","text":"图片来源：RXBAR、David"},
		{"k":"txt","blankBefore":true,"runs":[{"type":"txt","text":"例如我们曾介绍过的Poppi等。https://mp.weixin.qq.com/s/M_dPjnJED8Xx4JJw_3lQ6g同样是主打干净的产品，"},{"type":"txt","text":"RXBAR却没选这条路。","bold":true}]},
		{"k":"txt","runs":[{"type":"txt","text":"整段加粗的导语。","bold":true}]},
		{"k":"h","level":2,"text":"第一次创业，他把蛋白棒做简单"},
		{"k":"txt","runs":[{"type":"txt","text":"这是我"},{"type":"link","text":"茶叶系列","href":"https://www.foodtalks.cn/news/tag/114000"},{"type":"txt","text":"的第38篇。"}]},
		{"k":"img","src":"https://cdn.example/photo.png"},
		{"k":"cap","text":"Peter Rahal在2024年推出蛋白质品牌David，主打每根28克蛋白质、只有150卡路里的蛋白棒"},
		{"k":"cap","text":"图片来源：Forbes"},
		{"k":"h","level":3,"text":"1、包装更新"},
		{"k":"img","src":"https://cdn.example/wide-but-captioned.png"},
		{"k":"cap","text":"三得利伊右卫门绿茶全家福"},
		{"k":"img","src":"https://cdn.example/photo.png"},
		{"k":"cap","text":"真的！是西柚味！"},
		{"k":"cap","text":"图片来源：X@木本牙狼"},
		{"k":"h","level":2,"text":"结语"},
		{"k":"txt","text":"下一篇，我继续写中国茶叶品牌。"},
		{"k":"img","blankBefore":true,"src":"https://cdn.example/mote-card.png"},
		{"k":"txt","blankBefore":true,"text":"参考来源："},
		{"k":"txt","text":"[1] The RXBAR Story"},
		{"k":"img","blankBefore":true,"src":"https://cdn.example/sage-card.png"}
	]`
	imageMap := `{"https://cdn.example/lead.jpg":"https://cdn.example/lead.jpg","https://cdn.example/photo.png":"https://cdn.example/photo.png",
		"https://cdn.example/wide-but-captioned.png":"https://cdn.example/wide-but-captioned.png",
		"https://cdn.example/mote-card.png":"https://cdn.example/mote-card.png","https://cdn.example/sage-card.png":"https://cdn.example/sage-card.png",
		"https://cdn.example/wechat-cover.jpg":"https://cdn.example/wechat-cover.jpg"}`
	return publishJob{
		Title:      "估值超151亿元！蛋白棒赛道创业，他连赢了两次",
		SourceName: "FBIF食品饮料创新",
		Summary:    "竞业协议到期后，明星创业者重走“老路”。",
		CoverURL:   "https://cdn.example/wechat-cover.jpg",
		BlocksJSON: blocks,
		ImageMap:   imageMap,
	}
}

// 外发版式的金标准是编辑手发在头条号、百家号上的稿（2026-10-20 用户：「按照人工稿改，这应该是
// 你的标准」；对照 10-15 起的 #286 #287 #289 #290 #292 #299 与 8 月老稿）。两个平台都经自己的分发服务
// 写进编辑器（百家号 2026-10-22、头条号 2026-10-24），正文规则同一套（开头不放署名、编号、去名片、
// 无链接、文末声明），写法各用编辑器留得住的：标题1 百家号是 h3（只有一级小标题）、头条号是 h1，
// 标题2 与整段加粗是加粗段，图注在 div.pgc-img 里；首图留在原位，封面是公众号封面、单独上传不进正文，标题原样。
func TestEditorDraftsUseEachPlatformsMarkup(t *testing.T) {
	stubYunsuoImageSize(t, nil)
	job := manualLayoutTestJob()
	for _, c := range []struct{ name, key, heading string }{{"百家号", "baijiahao", "h3"}, {"头条号", "toutiao", "h1"}} {
		profile := yunsuoProfileForTarget(yunsuoAccountTarget{ID: "a-1", Name: c.name})
		payload, err := buildRobotDraftPayload(job, profile, publishExternalDraft{})
		if err != nil {
			t.Fatal(err)
		}
		h := func(text string) string { return "<" + c.heading + ">" + text + "</" + c.heading + ">" }
		html := payload.HTML
		for _, want := range []string{
			"<p>一个人，可以在同一赛道成功2次吗？</p>",
			`<div class="pgc-img"><img src="https://cdn.example/lead.jpg" alt=""><p class="pgc-img-caption">蛋白棒RXBAR和David；图片来源：RXBAR、David</p></div>`,
			`<p class="pgc-img-caption">Peter Rahal在2024年推出蛋白质品牌David，主打每根28克蛋白质、只有150卡路里的蛋白棒；图片来源：Forbes</p></div>`,
			h("一、第一次创业，他把蛋白棒做简单"),
			"<p><strong>1、包装更新</strong></p>",
			h("二、结语"),
			"<p><strong>整段加粗的导语。</strong></p>",
			"<p>这是我茶叶系列的第38篇。</p>",
			"例如我们曾介绍过的Poppi等。同样是主打干净的产品，<strong>RXBAR却没选这条路。</strong>",
			"wide-but-captioned.png",
			"<p>参考来源：</p><p>[1] The RXBAR Story</p><p>本文为FBIF食品饮料创新原创，作者：Sage，编辑：Bobo，转载请联系授权。</p>",
		} {
			if !strings.Contains(html, want) {
				t.Errorf("%s草稿正文缺少 %q\n%s", c.name, want, html)
			}
		}
		for _, banned := range []string{"作者：Sage</p>", "<p><br></p>", "<a ", "mp.weixin.qq.com", "<h2", "<small>", "mote-card.png", "sage-card.png", "wechat-cover.jpg"} {
			if strings.Contains(html, banned) {
				t.Errorf("%s草稿正文不得出现 %q\n%s", c.name, banned, html)
			}
		}
		if payload.Platform != c.key || payload.CoverURL != "https://cdn.example/wechat-cover.jpg" || payload.Title != job.Title {
			t.Fatalf("%s封面用公众号封面、标题原样：%+v", c.name, payload)
		}
	}
}

// 编译稿的「英文来源」「中文编辑」在正文最后（#289），人工稿把它们写进文末声明。
func TestYunsuoDistributionMovesTailCreditsIntoStatement(t *testing.T) {
	stubYunsuoImageSize(t, nil)
	blocks := `[
		{"k":"txt","text":"当地时间10月14日，Whole Foods Market发布2027年八大食品趋势。"},
		{"k":"txt","text":"Lentiful杯装即食扁豆餐以扁豆为主要原料。"},
		{"k":"txt","text":"英文来源：Whole Foods Market"},
		{"k":"txt","text":"中文编辑：kiki"}
	]`
	html, err := renderYunsuoHTML(blocks, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	want := "<p>Lentiful杯装即食扁豆餐以扁豆为主要原料。</p><p>本文为FBIF食品饮料创新原创，英文来源：Whole Foods Market，中文编辑：kiki，转载请联系授权。</p>"
	if !strings.HasSuffix(html, want) {
		t.Fatalf("文末署名应并进声明：%s", html)
	}
	// 没有署名就只写原创声明（#287）。
	html, err = renderYunsuoHTML(`[{"k":"txt","text":"十项趋势并不是一张需要同时完成的清单。"}]`, `{}`)
	if err != nil || !strings.HasSuffix(html, "<p>本文为FBIF食品饮料创新原创，转载请联系授权。</p>") {
		t.Fatalf("无署名稿的声明不对：%s %v", html, err)
	}
}

// 小标题序号：人工稿两篇以上的标题1 都编「一、二、」；原文已经编了号（#292）、或只有一个标题
// （#290）就不动；参考来源不算章节。
func TestYunsuoDistributionNumbersSectionHeadings(t *testing.T) {
	render := func(blocks string) string {
		t.Helper()
		html, err := renderYunsuoHTML(blocks, `{}`)
		if err != nil {
			t.Fatal(err)
		}
		return html
	}
	if html := render(`[{"k":"h","level":2,"text":"趋势一 营养设计"},{"k":"txt","text":"正文。"},{"k":"h","level":2,"text":"趋势二 精准蛋白"},{"k":"h","level":2,"text":"参考来源："},{"k":"txt","text":"[1] Innova"}]`); !strings.Contains(html, "<h2>一、趋势一 营养设计</h2>") ||
		!strings.Contains(html, "<h2>二、趋势二 精准蛋白</h2>") || !strings.Contains(html, "<h2>参考来源：</h2>") {
		t.Fatalf("两个以上标题1 应编号、参考来源不编：%s", html)
	}
	if html := render(`[{"k":"h","level":2,"text":"一、 一群做设计的人，“起义”做茶"},{"k":"h","level":2,"text":"被tea'stone的茶惊艳到"},{"k":"h","level":3,"text":"1. 山河瓶"}]`); !strings.Contains(html, "<h2>一、 一群做设计的人，“起义”做茶</h2>") ||
		!strings.Contains(html, "<h2>被tea&#39;stone的茶惊艳到</h2>") || !strings.Contains(html, "<h3>1. 山河瓶</h3>") {
		t.Fatalf("原文已编号的不再编：%s", html)
	}
	if html := render(`[{"k":"h","level":2,"text":"炸薯片，还能把油换成水？"},{"k":"txt","text":"正文。"}]`); !strings.Contains(html, "<h2>炸薯片，还能把油换成水？</h2>") {
		t.Fatalf("只有一个标题不编号：%s", html)
	}
	if html := render(`[{"k":"h","level":2,"text":"3.9元的瓶装茶"},{"k":"h","level":2,"text":"便利店的新货架"}]`); !strings.Contains(html, "<h2>一、3.9元的瓶装茶</h2>") {
		t.Fatalf("小数开头不算已编号：%s", html)
	}
}

// 封面：百家号、头条号都经自己的分发服务，封面在编辑器的封面弹窗里单独上传、不进正文，首图留在原位。
// 云梭在百家号上曾把 cover 插到正文最前面（#95 8 月稿、#282）：会插封面的平台（CoverPrepended，现在没有
// 平台开）文章若以图开头，就用这张图当封面并从正文拿掉，插回来正好是原位。
func TestYunsuoCoverFollowsHowEachPlatformPlacesIt(t *testing.T) {
	stubYunsuoImageSize(t, nil)
	blocks := `[
		{"k":"txt","text":"作者：Mote莫特"},
		{"k":"img","src":"https://cdn.example/lead.jpg"},
		{"k":"cap","text":"tea'stone深圳万象前海店"},
		{"k":"txt","text":"这是我茶叶系列的第39篇。"}
	]`
	job := publishJob{
		Title:      "最美的中国茶馆tea'stone，8年开了18家店：我的2周体验和观察",
		SourceName: "FBIF食品饮料创新",
		CoverURL:   "https://cdn.example/wechat-cover.jpg",
		BlocksJSON: blocks,
		ImageMap:   `{"https://cdn.example/lead.jpg":"https://cdn.example/lead.jpg","https://cdn.example/wechat-cover.jpg":"https://cdn.example/wechat-cover.jpg"}`,
	}
	for _, name := range []string{"头条号", "百家号"} {
		payload, err := buildRobotDraftPayload(job, yunsuoProfileForTarget(yunsuoAccountTarget{ID: "a-1", Name: name}), publishExternalDraft{})
		if err != nil {
			t.Fatal(err)
		}
		if payload.CoverURL != "https://cdn.example/wechat-cover.jpg" ||
			!strings.HasPrefix(payload.HTML, `<div class="pgc-img"><img src="https://cdn.example/lead.jpg" alt=""><p class="pgc-img-caption">tea&#39;stone深圳万象前海店</p></div>`) {
			t.Fatalf("%s：封面用公众号封面、单独上传，首图留在原位：cover=%q html=%s", name, payload.CoverURL, payload.HTML)
		}
	}
	prepended := yunsuoPlatformProfiles["toutiao"]
	prepended.CoverPrepended = true
	_, rendered, err := renderExternalBody(job, prepended, publishExternalDraft{})
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Cover != "https://cdn.example/lead.jpg" || strings.Contains(rendered.HTML, "lead.jpg") ||
		!strings.HasPrefix(rendered.HTML, "<p>tea&#39;stone深圳万象前海店</p><p>这是我茶叶系列的第39篇。</p>") {
		t.Fatalf("会插封面的平台：开头的图做封面（云梭会插回最前面），图注留在原处：cover=%q html=%s", rendered.Cover, rendered.HTML)
	}
}

// 标题用原文：经分发服务的平台标题原样送（百家号 2–64 字、头条号 2–30 字），不补「｜摘要」。
// 云梭三档（short 8–20、title 15–40、long 20–60）只留给切换前的旧 lane：放得下就原样写，放不下才补或截。
func TestYunsuoTitleFieldsKeepOriginalTitle(t *testing.T) {
	stubYunsuoImageSize(t, nil)
	job := publishJob{
		SourceName: "FBIF食品饮料创新",
		Summary:    "饮品越来越像甜品，背后的原料也有了更多值得探索的使用方式。",
		CoverURL:   "https://cdn.example/wechat-cover.jpg",
		BlocksJSON: `[{"k":"txt","text":"不管吃的喝的，现在都能喝了。"}]`,
		ImageMap:   `{"https://cdn.example/wechat-cover.jpg":"https://cdn.example/wechat-cover.jpg"}`,
	}
	categories := []string{"美食", "饮食文化", "财经", "消费", "公司"}
	job.Title = "西柚汁原料价格翻倍，可果美开始用胡萝卜做“西柚汁”了"                   // 26 字
	legacy := []yunsuoAccountTarget{{ID: "a-9", Name: "云梭旧号"}} // 认不出平台的号：通用版式，经云梭
	payload, err := buildYunsuoPushPayload(job, legacy, categories)
	if err != nil {
		t.Fatal(err)
	}
	if toutiao, err := buildRobotDraftPayload(job, yunsuoProfileForTarget(yunsuoAccountTarget{Name: "头条号"}), publishExternalDraft{}); err != nil || toutiao.Title != job.Title {
		t.Fatalf("头条号标题原样送：%+v %v", toutiao, err)
	}
	if payload.Title != job.Title || payload.LongTitle != job.Title {
		t.Fatalf("放得下的档位应原样用原标题：title=%q long=%q", payload.Title, payload.LongTitle)
	}
	job.Title = "巧克力也想成新茶饮的“心头好”" // 15 字：title 档原样，long 档不足 20 字才补
	if payload, err = buildYunsuoPushPayload(job, legacy, categories); err != nil ||
		payload.Title != job.Title || !strings.HasPrefix(payload.LongTitle, job.Title+"｜") {
		t.Fatalf("15 字标题：title=%q long=%q err=%v", payload.Title, payload.LongTitle, err)
	}
	baijia := yunsuoProfileForTarget(yunsuoAccountTarget{Name: "百家号"})
	if payload, err := buildRobotDraftPayload(job, baijia, publishExternalDraft{}); err != nil || payload.Title != job.Title {
		t.Fatalf("百家号标题原样送：%+v %v", payload, err)
	}
	job.Title = "茶"
	if _, err := buildRobotDraftPayload(job, baijia, publishExternalDraft{}); err == nil || !strings.Contains(err.Error(), "百家号标题要 2–64 字，现在 1 字") {
		t.Fatalf("百家号标题不到 2 字应拦：%v", err)
	}
}

// 头条号标题字数：英文字母、数字算半个字，2–30 字（与分发服务同一数法）。编辑手发的 #287（32 个字符）、
// #292（35）、#289（42）头条都收了；按每个字符算 1 会把它们误拦成待修改。
func TestToutiaoTitleCountsASCIIAsHalf(t *testing.T) {
	profile := yunsuoProfileForTarget(yunsuoAccountTarget{Name: "头条号"})
	for _, title := range []string{
		"Innova揭晓2027全球食品饮料十大趋势，穿越二十年创新周期",
		"最美的中国茶馆tea'stone，8年开了18家店：我的2周体验和观察",
		"Whole Foods Market发布2027年8大食品趋势！苦味升级、豆类热潮…",
	} {
		if issues := externalTextIssues(publishJob{Title: title}, profile, publishExternalDraft{}); len(issues) != 0 {
			t.Errorf("头条号收过的标题不应拦：%q %+v", title, issues)
		}
	}
	issues := externalTextIssues(publishJob{Title: "这个标题全是汉字一共三十一个字远远超过了头条号的标题字数上限啊"}, profile, publishExternalDraft{})
	if len(issues) != 1 || issues[0].Length != 31 || issues[0].Max != 30 {
		t.Fatalf("31 个汉字应超头条 30 字：%+v", issues)
	}
	if got := profile.titleLength("Whole Foods Market发布2027年8大食品趋势！苦味升级、豆类热潮…"); got != 30 {
		t.Fatalf("头条字数 = %d，想要 30", got)
	}
	stubYunsuoImageSize(t, nil)
	job := manualLayoutTestJob()
	job.Title = "这个标题全是汉字一共三十一个字远远超过了头条号的标题字数上限啊"
	if _, err := buildRobotDraftPayload(job, profile, publishExternalDraft{}); err == nil || !strings.Contains(err.Error(), "头条号标题要 2–30 字（英文字母、数字两个算一个字），现在 31 字") {
		t.Fatalf("存草稿前按头条的数法拦：%v", err)
	}
}

func TestYunsuoDistributionDropsTrailingNameCardWithoutReferences(t *testing.T) {
	stubYunsuoImageSize(t, map[string][2]int{
		"https://cdn.example/card.png":  {970, 283},
		"https://cdn.example/photo.png": {1080, 922},
	})
	render := func(last string) string {
		blocks := `[
			{"k":"txt","text":"作者：Mote莫特"},
			{"k":"img","src":"https://cdn.example/lead.png"},
			{"k":"txt","text":"正文第一段。"},
			{"k":"img","src":"` + last + `"}
		]`
		imageMap := `{"https://cdn.example/lead.png":"https://cdn.example/lead.png","` + last + `":"` + last + `"}`
		html, err := renderYunsuoHTML(blocks, imageMap)
		if err != nil {
			t.Fatal(err)
		}
		return html
	}
	if html := render("https://cdn.example/card.png"); strings.Contains(html, "card.png") ||
		!strings.HasSuffix(html, "<p>本文为FBIF食品饮料创新原创，作者：Mote莫特，转载请联系授权。</p>") {
		t.Fatalf("文末无图注的宽条名片应去掉，声明署名取作者：%s", html)
	}
	if html := render("https://cdn.example/photo.png"); !strings.Contains(html, "photo.png") {
		t.Fatalf("文末普通配图不得误删：%s", html)
	}
}

func init() {
	// 测试默认读不到图片尺寸（不出网）；要尺寸的用例用 stubYunsuoImageSize 显式给。
	yunsuoImageSize = func(string) (int, int, bool) { return 0, 0, false }
}

// TestYunsuoCoverRespectsToutiaoMinimum 2026-10-11 #183 头条号回执「封面图尺寸过小780x332，
// 平台要求不得小于452x352」，受理后失败只能人工处理：推之前就查封面尺寸。
func TestYunsuoCoverRespectsToutiaoMinimum(t *testing.T) {
	blocks := `[
		{"k":"txt","text":"正文第一段，交代这篇文章要讲的品牌故事。"},
		{"k":"img","src":"https://cdn.example/lead.png"},
		{"k":"txt","text":"正文第二段。"}
	]`
	job := publishJob{
		Title:      "一款中国食品品牌如何用产品创新打开新的消费市场",
		SourceName: "FBIF食品饮料创新",
		Summary:    "食品品牌通过产品创新打开新的消费市场。",
		Tags:       []string{"食品", "创新"},
		BlocksJSON: blocks,
		ImageMap:   `{"https://cdn.example/lead.png":"https://cdn.example/lead.png","https://cdn.example/wx-cover.jpg":"https://cdn.example/wx-cover.jpg"}`,
		CoverURL:   "https://cdn.example/wx-cover.jpg",
	}
	targets := []yunsuoAccountTarget{{ID: "a-1", Name: "头条号"}}
	categories := []string{"美食", "饮食文化", "财经", "消费", "公司"}

	stubYunsuoImageSize(t, map[string][2]int{
		"https://cdn.example/lead.png":     {1080, 720},
		"https://cdn.example/wx-cover.jpg": {1200, 510},
	})
	payload, err := buildYunsuoPushPayload(job, targets, categories)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Cover != "https://cdn.example/wx-cover.jpg" || !strings.Contains(payload.HTML, "lead.png") {
		t.Fatalf("封面用公众号封面，正文图留在原位：cover=%q html=%s", payload.Cover, payload.HTML)
	}

	// 公众号封面太小（#183 780×332）：推了也会被平台拒，直接不推并说明原因。
	stubYunsuoImageSize(t, map[string][2]int{
		"https://cdn.example/lead.png":     {1080, 720},
		"https://cdn.example/wx-cover.jpg": {780, 332},
	})
	if _, err = buildYunsuoPushPayload(job, targets, categories); err == nil || !strings.Contains(err.Error(), "780x332") {
		t.Fatalf("封面不够头条下限应拒绝推送并给出尺寸，实际 err=%v", err)
	}

	// 没有公众号封面才用正文第一张够大的图，正文照旧不动。
	job.CoverURL = ""
	stubYunsuoImageSize(t, map[string][2]int{"https://cdn.example/lead.png": {1080, 720}})
	if payload, err = buildYunsuoPushPayload(job, targets, categories); err != nil ||
		payload.Cover != "https://cdn.example/lead.png" || !strings.Contains(payload.HTML, "lead.png") {
		t.Fatalf("没有公众号封面时用正文第一张图：cover=%q err=%v", payload.Cover, err)
	}
}

func stubYunsuoImageSize(t *testing.T, sizes map[string][2]int) {
	t.Helper()
	previous := yunsuoImageSize
	yunsuoImageSize = func(src string) (int, int, bool) {
		size, ok := sizes[src]
		return size[0], size[1], ok
	}
	t.Cleanup(func() { yunsuoImageSize = previous })
}

func TestYunsuoShortTitleBreaksAtPunctuation(t *testing.T) {
	// 2026-10-12 头条号后台实拍：short_title 硬截 20 字，标题停在半个词上。
	cases := []struct{ title, want string }{
		{"三得利卖出100亿瓶绿茶，背后藏着一家236年的老茶铺", "三得利卖出100亿瓶绿茶"},
		{"估值超151亿元！蛋白棒赛道创业，他连赢了两次", "估值超151亿元！蛋白棒赛道创业"},
		{"英国销售额第一的蛋白棒，亿滋CEO夸它“比美国所有蛋白棒都好吃”", "英国销售额第一的蛋白棒"},
		{"元气森林推出天真白日梦气泡水；古茗上线HPP瓶装冷萃茶 | 创新周报", "元气森林推出天真白日梦气泡水"},
		{"这家300年日本老茶铺，为何成了全球最难抢的抹茶品牌？", "这家300年日本老茶铺"},
		{"把茶做成零食，这家老茶铺只开6家店", "把茶做成零食，这家老茶铺只开6家店"},
		// 断点在引号里会留下半边引号，宁可硬截。
		{"新品“轻负担零添加高蛋白，好喝不胖还便宜”", "新品“轻负担零添加高蛋白，好喝不胖还便宜"},
		// 唯一的断点前不足 8 字：硬截。
		{"蛋白棒，亿滋首席执行官称赞这是全英国最好吃的蛋白棒", "蛋白棒，亿滋首席执行官称赞这是全英国最好"},
	}
	for _, tc := range cases {
		got := yunsuoTitleVariant(tc.title, "", yunsuoTitleShort)
		if got != tc.want {
			t.Errorf("short_title(%q) = %q，想要 %q", tc.title, got, tc.want)
		}
	}
	long := yunsuoTitleVariant("三得利",
		"三得利伊右卫门绿茶，卖出100亿瓶，背后藏着一家236年、年收60亿日元的老茶铺。三得利伊右卫门绿茶，卖出100亿瓶，背后藏着一家236年。", yunsuoTitleLong)
	// 补上摘要超过 60 字：在标点处截短。
	if want := "三得利｜三得利伊右卫门绿茶，卖出100亿瓶，背后藏着一家236年、年收60亿日元的老茶铺。三得利伊右卫门绿茶"; long != want {
		t.Errorf("long_title = %q，想要 %q", long, want)
	}
}

func TestYunsuoKeywordsRespectDocumentedLimit(t *testing.T) {
	keywords := yunsuoKeywords([]string{strings.Repeat("食品创新", 100), "饮料", "消费"})
	if got := len([]rune(keywords)); got > 255 {
		t.Fatalf("keywords 长度=%d，超过云梭 255 字上限", got)
	}
}

func TestYunsuoHTMLDropsUnmirroredAndNonPublicURLs(t *testing.T) {
	unsafeURLs := []string{
		"http://localhost/image.jpg",
		"http://127.0.0.1/image.jpg",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/image.jpg",
		"http://172.16.0.1/image.jpg",
		"http://192.168.1.1/image.jpg",
		"https://example.com:8443/image.jpg",
	}
	blocks := make([]map[string]any, 0, len(unsafeURLs)*2+2)
	imageMap := map[string]string{
		"https://origin.example/cover.jpg": "https://cdn.example/cover.jpg",
	}
	for i, unsafeURL := range unsafeURLs {
		imageKey := "https://origin.example/unsafe-" + string(rune('a'+i)) + ".jpg"
		imageMap[imageKey] = unsafeURL
		blocks = append(blocks,
			map[string]any{"k": "img", "src": imageKey},
			map[string]any{"k": "p", "runs": []map[string]any{{"type": "link", "text": "不安全链接", "href": unsafeURL}}},
		)
	}
	// 没出现在 ImageMap key/value 中的公网图同样必须丢弃，防止绕过
	// mirroring 后由云梭代替我们回源。
	blocks = append(blocks,
		map[string]any{"k": "img", "src": "https://unmirrored.example/image.jpg"},
		map[string]any{"k": "img", "src": "https://origin.example/cover.jpg"},
	)
	blocksJSON, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	imageMapJSON, err := json.Marshal(imageMap)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := renderYunsuoHTML(string(blocksJSON), string(imageMapJSON))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "https://cdn.example/cover.jpg") {
		t.Fatalf("唯一合规的图应留在正文：%s", rendered)
	}
	if strings.Contains(rendered, "unmirrored.example") {
		t.Fatalf("未镜像媒体不得进入云梭 HTML：%s", rendered)
	}
	for _, unsafeURL := range unsafeURLs {
		if strings.Contains(rendered, unsafeURL) {
			t.Fatalf("非公网 URL 不得进入云梭 HTML：%s", unsafeURL)
		}
	}

	job := publishJob{
		Title:      "安全分发的原创文章",
		Summary:    "测试外部分发中的媒体和链接地址策略。",
		BlocksJSON: string(blocksJSON),
		ImageMap:   string(imageMapJSON),
		CoverURL:   "https://cdn.example/cover.jpg", // 已是 ImageMap value，允许。
	}
	payload, err := buildYunsuoPushPayload(job,
		[]yunsuoAccountTarget{{ID: "a-1", Name: "头条号"}},
		[]string{"美食", "饮食文化", "财经", "消费", "公司"})
	if err != nil {
		t.Fatal(err)
	}
	if payload.Cover != "https://cdn.example/cover.jpg" || payload.HTML != rendered {
		t.Fatalf("推送快照应只使用已镜像的公网媒体：cover=%q html=%s", payload.Cover, payload.HTML)
	}
}

func TestYunsuoRenderStripsWechatTrailerBeforePush(t *testing.T) {
	blocks := `[
		{"k":"txt","runs":[{"type":"txt","text":"把家乡一度被低估的食物卖到英国第一。"}]},
		{"k":"txt","text":"参考来源："},
		{"k":"txt","text":"[1] Food Manufacture"},
		{"k":"txt","text":"提示："},
		{"k":"txt","text":"* 本文为FBIF原创，欢迎转发朋友圈；"},
		{"k":"cap","text":"/ 更多文章 /"},
		{"k":"img","src":"https://origin.example/card.jpg"},
		{"k":"txt","text":"预览时标签不可点"}
	]`
	imageMap := `{"https://origin.example/card.jpg":"https://cdn.example/card.jpg","https://cdn.example/cover.jpg":"https://cdn.example/cover.jpg"}`
	rendered, err := renderYunsuoHTML(blocks, imageMap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "英国第一") || !strings.Contains(rendered, "参考来源") {
		t.Fatalf("正文应保留：%s", rendered)
	}
	if leak := publishHTMLWechatTrailerLeak(rendered); leak != "" {
		t.Fatalf("云梭 HTML 仍含导流尾 %s：%s", leak, rendered)
	}
	if strings.Contains(rendered, "cdn.example/card.jpg") {
		t.Fatalf("作者名片图应随导流尾切除：%s", rendered)
	}

	payload, err := buildYunsuoPushPayload(publishJob{
		Title:      "印尼博士创业，把家乡穷人食物卖成英国第一品牌",
		SourceName: "FBIF食品饮料创新",
		Summary:    "Better Nature 把天贝卖到英国品类第一。",
		CoverURL:   "https://cdn.example/cover.jpg",
		BlocksJSON: blocks,
		ImageMap:   imageMap,
	}, []yunsuoAccountTarget{{ID: "a-1", Name: "头条号"}, {ID: "a-2", Name: "百家号"}},
		[]string{"美食", "饮食文化", "财经", "消费", "公司"})
	if err != nil {
		t.Fatal(err)
	}
	if leak := publishHTMLWechatTrailerLeak(payload.HTML); leak != "" {
		t.Fatalf("冻结 payload 仍含导流尾 %s", leak)
	}
	// 正文没有配图时退回原封面；不得把导流尾里的名片图拿来当封面。
	if payload.Cover != "https://cdn.example/cover.jpg" {
		t.Fatalf("正文无图时应用原封面，实际 %q", payload.Cover)
	}
}

func TestParseYunsuoStatusesAcceptsAPICreatedTypeString(t *testing.T) {
	raw := json.RawMessage(`[
		{"status":0,"remark":"等待发布","failed_count":0,"created_type":"api","accountPool":{"pk":"11111111-1111-4111-8111-111111111111","name":"头条号","platform_name":"头条号"}},
		{"status":0,"remark":"等待发布","failed_count":0,"created_type":"api","accountPool":{"pk":"22222222-2222-4222-8222-222222222222","name":"百家号","platform_name":"百家号"}}
	]`)
	got, err := parseYunsuoStatuses(raw)
	if err != nil {
		t.Fatalf("真实云梭状态（created_type 为字符串）必须能解析：%v", err)
	}
	if len(got) != 2 || got[0].Status != 0 || got[0].AccountPool.ID != "11111111-1111-4111-8111-111111111111" ||
		got[0].CreatedType != "api" || got[1].AccountPool.Name != "百家号" {
		t.Fatalf("状态解析不符合预期：%#v", got)
	}
}

// FoodTalks 署名会把「编译/翻译/采访」并进来，外发按原文逐项拆出来写进文末声明。
func TestYunsuoBylineSplitsTranslatorCredits(t *testing.T) {
	credits, ok := parseYunsuoByline("作者：Chloe Sorvino，翻译：Lemin。")
	if !ok || strings.Join(credits, "|") != "作者：Chloe Sorvino|翻译：Lemin" {
		t.Fatalf("credits=%v ok=%v", credits, ok)
	}
	credits, ok = parseYunsuoByline("英文来源：Whole Foods Market")
	if !ok || strings.Join(credits, "|") != "英文来源：Whole Foods Market" {
		t.Fatalf("credits=%v ok=%v", credits, ok)
	}
	if _, ok := parseYunsuoByline("作者是谁，这很重要。"); ok {
		t.Fatal("正文句不能当署名拆")
	}
	if _, ok := parseYunsuoByline("来源：艾媒咨询《2025新式茶饮报告》"); ok {
		t.Fatal("数据出处「来源：」不是署名")
	}
}
