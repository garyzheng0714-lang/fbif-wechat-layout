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
	for _, wanted := range []string{"<h2>章节 &lt;一&gt;</h2>", "https://cdn.example/image.jpg", "<ol>", "<table", "https://cdn.example/poster.jpg"} {
		if !strings.Contains(rendered, wanted) {
			t.Fatalf("中性 HTML 缺少 %q：%s", wanted, rendered)
		}
	}
	if strings.Contains(strings.ToLower(rendered), "javascript:") || strings.Contains(rendered, "<script>") || strings.Contains(rendered, "FoodTalks") {
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
		t.Fatalf("已镜像且符合公网策略的图片应保留：%s", rendered)
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
	imageMap := `{"https://origin.example/card.jpg":"https://cdn.example/card.jpg"}`
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
}
