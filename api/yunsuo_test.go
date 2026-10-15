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
		{"k":"list","ordered":true,"items":["第一项",{"runs":[{"type":"link","text":"第二项","href":"https://example.com/?a=1&b=2"}]}]},
		{"k":"table","rows":[[{"header":true,"runs":[{"type":"txt","text":"表头"}]}],[{"runs":[{"type":"txt","text":"<script>"}]}]]},
		{"k":"video","src":"https://cdn.example/video.mp4","poster":"https://origin.example/poster.jpg"}
	]`
	imageMap := `{"https://origin.example/image.jpg":"https://cdn.example/image.jpg","https://origin.example/poster.jpg":"https://cdn.example/poster.jpg"}`
	rendered, err := renderYunsuoHTML(blocks, imageMap)
	if err != nil {
		t.Fatal(err)
	}
	for _, wanted := range []string{`<h1 class="pgc-h-forward-slash">章节 &lt;一&gt;</h1>`, "<ol>", "<table", "https://cdn.example/poster.jpg"} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("中性 HTML 缺少 %q：%s", wanted, rendered)
		}
	}
	if strings.Contains(rendered, "https://cdn.example/image.jpg") {
		t.Fatalf("正文第一张图做封面，不得再留在正文里：%s", rendered)
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
	if shortLen < 8 || shortLen > 20 || titleLen < 21 || titleLen > 40 || longLen < 41 || longLen > 60 {
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

// 2026-10-12 用户逐条指出头条号外发稿的问题，金标准是人工发在头条号的 16 篇稿。
// 这里用网站已发布稿的实际形态（#193 署名合并、#260 Mote 名片在参考来源上方）回归。
func TestYunsuoDistributionFollowsManualToutiaoLayout(t *testing.T) {
	stubYunsuoImageSize(t, nil)
	blocks := `[
		{"k":"txt","text":"作者：Sage，编辑：Bobo。"},
		{"k":"txt","blankBefore":true,"text":"一个人，可以在同一赛道成功2次吗？"},
		{"k":"img","blankBefore":true,"src":"https://cdn.example/lead.jpg"},
		{"k":"cap","text":"蛋白棒RXBAR和David"},
		{"k":"cap","text":"图片来源：RXBAR、David"},
		{"k":"txt","blankBefore":true,"runs":[{"type":"txt","text":"例如我们曾介绍过的Poppi等。https://mp.weixin.qq.com/s/M_dPjnJED8Xx4JJw_3lQ6g同样是主打干净的产品，"},{"type":"txt","text":"RXBAR却没选这条路。","bold":true}]},
		{"k":"h","level":2,"text":"第一次创业，他把蛋白棒做简单"},
		{"k":"txt","runs":[{"type":"txt","text":"这是我"},{"type":"link","text":"茶叶系列","href":"https://www.foodtalks.cn/news/tag/114000"},{"type":"txt","text":"的第38篇。"}]},
		{"k":"img","src":"https://cdn.example/photo.png"},
		{"k":"cap","text":"Peter Rahal"},
		{"k":"cap","text":"图片来源：Forbes"},
		{"k":"h","level":3,"text":"1、包装更新"},
		{"k":"img","src":"https://cdn.example/wide-but-captioned.png"},
		{"k":"cap","text":"三得利伊右卫门绿茶全家福"},
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
	payload, err := buildYunsuoPushPayload(publishJob{
		Title:      "估值超151亿元！蛋白棒赛道创业，他连赢了两次",
		SourceName: "FBIF食品饮料创新",
		Summary:    "竞业协议到期后，明星创业者重走“老路”。",
		CoverURL:   "https://cdn.example/wechat-cover.jpg",
		BlocksJSON: blocks,
		ImageMap:   imageMap,
	}, []yunsuoAccountTarget{{ID: "a-1", Name: "头条号"}}, []string{"美食", "饮食文化", "财经", "消费", "公司"})
	if err != nil {
		t.Fatal(err)
	}
	html := payload.HTML
	if payload.Cover != "https://cdn.example/lead.jpg" || strings.Contains(html, "lead.jpg") || strings.Contains(html, "蛋白棒RXBAR和David") {
		t.Fatalf("封面应取正文第一张图并连图注一起从正文拿掉，cover=%q html=%s", payload.Cover, html)
	}
	if strings.Contains(html, "wechat-cover.jpg") {
		t.Fatalf("不得再另传公众号封面：%s", html)
	}
	for _, want := range []string{
		"<p>作者：Sage</p><p>编辑：Bobo</p><p>一个人",
		`<div class="pgc-img"><img src="https://cdn.example/photo.png" alt=""><p class="pgc-img-caption">Peter Rahal；图片来源：Forbes</p></div>`,
		`<h1 class="pgc-h-forward-slash">第一次创业，他把蛋白棒做简单</h1>`,
		"<p><strong>1、包装更新</strong></p>",
		"<p>这是我茶叶系列的第38篇。</p>",
		"例如我们曾介绍过的Poppi等。同样是主打干净的产品，<strong>RXBAR却没选这条路。</strong>",
		"wide-but-captioned.png",
		"<p>参考来源：</p><p>[1] The RXBAR Story</p><p>本文为FBIF食品饮料创新原创，作者：Sage，转载请联系授权。</p>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("外发正文缺少 %q\n%s", want, html)
		}
	}
	for _, banned := range []string{"作者：Sage，编辑：Bobo", "Bobo。", "<p><br></p>", "<a ", "mp.weixin.qq.com", "<h2", "<small>", "mote-card.png", "sage-card.png"} {
		if strings.Contains(html, banned) {
			t.Errorf("外发正文不得出现 %q\n%s", banned, html)
		}
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
// 平台要求不得小于452x352」，受理后失败只能人工处理：推之前就挑够大的封面。
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

	// #220 实测：正文第一张图 437×336 → 留在正文，封面退回公众号封面。
	stubYunsuoImageSize(t, map[string][2]int{
		"https://cdn.example/lead.png":     {437, 336},
		"https://cdn.example/wx-cover.jpg": {1200, 510},
	})
	payload, err := buildYunsuoPushPayload(job, targets, categories)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Cover != "https://cdn.example/wx-cover.jpg" || !strings.Contains(payload.HTML, "lead.png") {
		t.Fatalf("第一张图太小应留在正文、封面用公众号封面：cover=%q html=%s", payload.Cover, payload.HTML)
	}

	// 够大就照旧取第一张图做封面并从正文拿掉。
	stubYunsuoImageSize(t, map[string][2]int{"https://cdn.example/lead.png": {1080, 720}})
	if payload, err = buildYunsuoPushPayload(job, targets, categories); err != nil ||
		payload.Cover != "https://cdn.example/lead.png" || strings.Contains(payload.HTML, "lead.png") {
		t.Fatalf("第一张图够大应做封面：cover=%q err=%v", payload.Cover, err)
	}

	// 两张都太小（#183 公众号封面 780×332）：推了也会被平台拒，直接不推并说明原因。
	stubYunsuoImageSize(t, map[string][2]int{
		"https://cdn.example/lead.png":     {437, 336},
		"https://cdn.example/wx-cover.jpg": {780, 332},
	})
	if _, err = buildYunsuoPushPayload(job, targets, categories); err == nil || !strings.Contains(err.Error(), "780x332") {
		t.Fatalf("封面不够头条下限应拒绝推送并给出尺寸，实际 err=%v", err)
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
		got := yunsuoTitleVariant(tc.title, "", 8, 20)
		if got != tc.want {
			t.Errorf("short_title(%q) = %q，想要 %q", tc.title, got, tc.want)
		}
	}
	long := yunsuoTitleVariant("三得利卖出100亿瓶绿茶，背后藏着一家236年的老茶铺",
		"三得利伊右卫门绿茶，卖出100亿瓶，背后藏着一家236年、年收60亿日元的老茶铺。", 41, 60)
	if want := "三得利卖出100亿瓶绿茶，背后藏着一家236年的老茶铺｜三得利伊右卫门绿茶，卖出100亿瓶，背后藏着一家236年"; long != want {
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
	if strings.Contains(rendered, "https://cdn.example/cover.jpg") {
		t.Fatalf("唯一合规的图是正文第一张，应拿去做封面而不留在正文：%s", rendered)
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

// FoodTalks 署名会把「编译/翻译/采访」并进来，外发同样按原文分行。
func TestYunsuoBylineSplitsTranslatorCredits(t *testing.T) {
	lines, author, ok := parseYunsuoByline("作者：Chloe Sorvino，翻译：Lemin。")
	if !ok || author != "Chloe Sorvino" || strings.Join(lines, "|") != "作者：Chloe Sorvino|翻译：Lemin" {
		t.Fatalf("lines=%v author=%q ok=%v", lines, author, ok)
	}
	lines, _, ok = parseYunsuoByline("编译：李言")
	if !ok || strings.Join(lines, "|") != "编译：李言" {
		t.Fatalf("lines=%v ok=%v", lines, ok)
	}
	if _, _, ok := parseYunsuoByline("作者是谁，这很重要。"); ok {
		t.Fatal("正文句不能当署名拆")
	}
}
