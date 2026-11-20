package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	agentTestPerson    = fbifCompanyCode + ":union_me"
	agentTestColleague = fbifCompanyCode + ":union_colleague"
	agentTestVerifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXkaaaaaa"
)

type agentTestEnv struct {
	t      *testing.T
	layout *layoutTestEnv
	auth   *authService
	check  *articleCheckFake
	srv    *httptest.Server
	issuer string
}

// newAgentTestEnv 起一个真的 HTTP 服务：授权、MCP、上传、编辑器取回都走真路由；排版程序、抓取、模型是假的。
// 请求上的人由 X-Test-User 放进 context（生产里是 auth_login.go 的 Wrap 读入口给的 X-FBIF-User 再放）。
func newAgentTestEnv(t *testing.T) *agentTestEnv {
	t.Helper()
	env := &agentTestEnv{t: t, layout: newLayoutTestEnv(t), auth: newTestAuthService(t, nil)}
	env.layout.node.check = articleCheckSource{Title: "标题", Text: articleCheckBody}
	for _, u := range []authUser{
		{ID: agentTestPerson, Name: "王小明", AuthMode: authModeFeishu, Status: authUserActive, FeishuUnionID: "union_me"},
		{ID: agentTestColleague, Name: "李四", AuthMode: authModeFeishu, Status: authUserActive, FeishuUnionID: "union_colleague"},
	} {
		mustSessionForUser(t, env.auth, u)
	}
	env.check = &articleCheckFake{
		risk:      articleCheckRisk(),
		verifyRaw: `{"items":[{"index":1,"definite":true,"wrong":"重回重回","correct":"重回"},{"index":2,"definite":false}]}`,
	}
	mux := http.NewServeMux()
	env.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get("X-Test-User"); id != "" {
			r = r.WithContext(withLayoutUser(r.Context(), id))
		}
		mux.ServeHTTP(w, r)
	}))
	env.issuer = "http://" + env.srv.Listener.Addr().String()
	if err := registerAgentEntry(mux, agentDeps{
		auth:     env.auth,
		layout:   env.layout.api,
		checker:  newArticleCheckFake(t, env.check),
		stateDir: t.TempDir(),
		issuer:   env.issuer,
		wait:     3 * time.Second,
	}); err != nil {
		t.Fatalf("registerAgentEntry: %v", err)
	}
	env.srv.Start()
	t.Cleanup(env.srv.Close)
	return env
}

func (env *agentTestEnv) request(method, path, user string, body io.Reader, header map[string]string) *http.Response {
	env.t.Helper()
	req, err := http.NewRequest(method, env.issuer+path, body)
	if err != nil {
		env.t.Fatal(err)
	}
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		env.t.Fatal(err)
	}
	env.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// login 照智能体的样子走一遍：动态注册 → 浏览器打开授权页（已登录）→ 点「允许」→ 授权码换令牌。
func (env *agentTestEnv) login(person string) string {
	t := env.t
	t.Helper()
	reg := env.request(http.MethodPost, "/oauth/register", "", strings.NewReader(
		`{"client_name":"Codex","redirect_uris":["http://127.0.0.1/callback"],"token_endpoint_auth_method":"none"}`),
		map[string]string{"Content-Type": "application/json"})
	var client struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal([]byte(readAll(t, reg)), &client); err != nil || client.ClientID == "" {
		t.Fatalf("动态注册：%d %v", reg.StatusCode, err)
	}
	sum := sha256.Sum256([]byte(agentTestVerifier))
	redirect := "http://127.0.0.1:53682/callback"
	q := url.Values{
		"response_type": {"code"}, "client_id": {client.ClientID}, "redirect_uri": {redirect},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"state": {"s1"}, "resource": {env.issuer + "/mcp"},
	}
	page := env.request(http.MethodGet, "/oauth/authorize?"+q.Encode(), person, nil, nil)
	html := readAll(t, page)
	if !strings.Contains(html, "本机上的「Codex」") {
		t.Fatalf("确认页不对：%d %s", page.StatusCode, html)
	}
	m := regexp.MustCompile(`name="request" value="([^"]+)"`).FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("确认页没有 request：%s", html)
	}
	allow := env.request(http.MethodPost, "/oauth/authorize", person, strings.NewReader(url.Values{"request": {m[1]}, "decision": {"allow"}}.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": env.issuer})
	loc, err := url.Parse(allow.Header.Get("Location"))
	if err != nil || loc.Query().Get("code") == "" {
		t.Fatalf("点允许后没跳回带码：%d %s", allow.StatusCode, allow.Header.Get("Location"))
	}
	tok := env.request(http.MethodPost, "/oauth/token", "", strings.NewReader(url.Values{
		"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "redirect_uri": {redirect},
		"client_id": {client.ClientID}, "code_verifier": {agentTestVerifier}, "resource": {env.issuer + "/mcp"},
	}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	var tokens struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal([]byte(readAll(t, tok)), &tokens); err != nil || tokens.AccessToken == "" {
		t.Fatalf("换令牌：%d %v", tok.StatusCode, err)
	}
	return tokens.AccessToken
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func (env *agentTestEnv) connect(token string) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "agent-test", Version: "1"}, nil)
	return client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: env.issuer + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token}}, MaxRetries: -1,
	}, nil)
}

func (env *agentTestEnv) session(person string) *mcp.ClientSession {
	env.t.Helper()
	cs, err := env.connect(env.login(person))
	if err != nil {
		env.t.Fatalf("连 MCP：%v", err)
	}
	env.t.Cleanup(func() { cs.Close() })
	return cs
}

// call 调一个工具；工具报错（IsError）时返回错误文字。
func call[Out any](t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (Out, string) {
	t.Helper()
	var out Out
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s：%v", name, err)
	}
	if res.IsError {
		var parts []string
		for _, c := range res.Content {
			if text, ok := c.(*mcp.TextContent); ok {
				parts = append(parts, text.Text)
			}
		}
		return out, strings.Join(parts, "\n")
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s 的结果读不出来：%v %s", name, err, raw)
	}
	return out, ""
}

func (env *agentTestEnv) handoff(id, user string) (int, agentHandoffJSON) {
	resp := env.request(http.MethodGet, "/api/agent/results/"+id, user, nil, nil)
	var out agentHandoffJSON
	_ = json.Unmarshal([]byte(readAll(env.t, resp)), &out)
	return resp.StatusCode, out
}

// 交 Markdown：和网页上传 .md 同一条路排，排好给编辑器链接；同事打开链接时编辑器拿回同一份 Markdown 和文件名。
func TestAgentLayoutsMarkdownAndHandsOffToEditor(t *testing.T) {
	env := newAgentTestEnv(t)
	cs := env.session(agentTestPerson)
	out, errText := call[agentLayoutOutput](t, cs, "layout_article", map[string]any{
		"account": "fbif", "markdown": "# 茶的故事\n\n正文", "title": "茶/的故事",
	})
	if errText != "" || out.Status != "done" {
		t.Fatalf("没排成：%s %+v", errText, out)
	}
	if out.EditorURL != env.issuer+"/mp/fbif?agent="+out.ResultID || out.Title != "排版程序给的标题" || out.PasteInto != "微信公众号后台编辑器" {
		t.Fatalf("结果不对：%+v", out)
	}
	if out.HTML != "" {
		t.Fatal("没要 HTML 就不该带上")
	}
	req := env.layout.node.lastRequest(t)
	if req.Input.Kind != layoutCarrierMarkdown || req.Input.FileName != "茶的故事.md" || req.Title != "" || req.Repost != nil {
		t.Fatalf("交给排版程序的不是「上传 .md」：%+v", req)
	}

	if status, _ := env.handoff(out.ResultID, ""); status != http.StatusUnauthorized {
		t.Fatalf("没登录也能取回：%d", status)
	}
	status, got := env.handoff(out.ResultID, agentTestColleague)
	want := agentHandoffJSON{Account: "fbif", Channel: "mp", Kind: agentKindMarkdown, Text: "# 茶的故事\n\n正文", FileName: "茶的故事.md"}
	if status != http.StatusOK || got != want {
		t.Fatalf("编辑器取回：%d %+v", status, got)
	}

	full, _ := call[agentLayoutOutput](t, cs, "get_layout_result", map[string]any{"result_id": out.ResultID, "include_html": true})
	if full.HTML != `<section><p>排好的正文</p></section>` {
		t.Fatalf("include_html 没带上成品：%+v", full)
	}
}

// 校对：和编辑器「AI 校对」卡同一个检查器、同一份标题正文；同一篇只查一次（智能体问两遍、编辑器再问，都接上同一次）。
func TestAgentCheckSharesTheEditorCheck(t *testing.T) {
	env := newAgentTestEnv(t)
	cs := env.session(agentTestPerson)
	out, _ := call[agentLayoutOutput](t, cs, "layout_article", map[string]any{"account": "fbif", "text": "一段文字"})
	if out.Status != "done" {
		t.Fatalf("没排成：%+v", out)
	}
	var check agentCheckOutput
	for i := 0; i < 5 && check.Status != articleCheckDone; i++ {
		check, _ = call[agentCheckOutput](t, cs, "check_article", map[string]any{"result_id": out.ResultID})
	}
	if check.Status != articleCheckDone || len(check.Fixes) != 1 || check.Fixes[0].Wrong != "重回重回" || check.Fixes[0].Correct != "重回" {
		t.Fatalf("校对结果不对：%+v", check)
	}
	if check.EditorURL != out.EditorURL || len(check.Issues) != 2 || len(check.Notes) != 1 {
		t.Fatalf("校对结果不对：%+v", check)
	}
	if env.check.seenTitle != "标题" || env.check.seenBody != articleCheckBody {
		t.Fatalf("送去查的不是排版程序给的校对原文：%q", env.check.seenTitle)
	}
	call[agentCheckOutput](t, cs, "check_article", map[string]any{"result_id": out.ResultID})
	if n := env.check.assessCalls.Load(); n != 1 {
		t.Fatalf("同一篇查了 %d 次", n)
	}
}

// 各人只看得到自己排的：别人拿到编号也取不了结果、查不了校对、用不了上传地址（编辑器链接例外，见上）。
func TestAgentResultsArePrivateToThePerson(t *testing.T) {
	env := newAgentTestEnv(t)
	mine := env.session(agentTestPerson)
	other := env.session(agentTestColleague)
	out, _ := call[agentLayoutOutput](t, mine, "layout_article", map[string]any{"account": "fbif", "text": "一段文字"})
	if _, errText := call[agentLayoutOutput](t, other, "get_layout_result", map[string]any{"result_id": out.ResultID}); errText == "" {
		t.Fatal("别人取到了我的结果")
	}
	if _, errText := call[agentCheckOutput](t, other, "check_article", map[string]any{"result_id": out.ResultID}); errText == "" {
		t.Fatal("别人查了我的校对")
	}
	up, _ := call[agentUploadOutput](t, mine, "prepare_upload", nil)
	env.upload(up.UploadURL, "a.docx", layoutTestDocx(t, false))
	if _, errText := call[agentLayoutOutput](t, other, "layout_article", map[string]any{"account": "fbif", "upload_id": up.UploadID}); errText == "" {
		t.Fatal("别人用了我的上传地址")
	}
}

func (env *agentTestEnv) upload(target, name string, data []byte) (int, string) {
	env.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", name)
	_, _ = part.Write(data)
	_ = mw.Close()
	u, _ := url.Parse(target)
	resp := env.request(http.MethodPost, u.Path, "", &body, map[string]string{"Content-Type": mw.FormDataContentType()})
	return resp.StatusCode, readAll(env.t, resp)
}

// Word：拿一次性地址 → curl 传上去（只收 Word、只能传一次）→ 交编号排；编辑器能取回原件，和人自己上传同一个文件。
func TestAgentWordUploadThenLayout(t *testing.T) {
	env := newAgentTestEnv(t)
	cs := env.session(agentTestPerson)
	up, errText := call[agentUploadOutput](t, cs, "prepare_upload", nil)
	if errText != "" || !strings.HasPrefix(up.UploadURL, env.issuer+"/api/agent/uploads/") || !strings.Contains(up.Command, up.UploadURL) {
		t.Fatalf("上传地址不对：%s %+v", errText, up)
	}
	if _, errText := call[agentLayoutOutput](t, cs, "layout_article", map[string]any{"account": "fbif", "upload_id": up.UploadID}); !strings.Contains(errText, "还没传上来") {
		t.Fatalf("没传就排：%q", errText)
	}
	if status, _ := env.upload(up.UploadURL, "稿子.txt", []byte("x")); status != http.StatusUnsupportedMediaType {
		t.Fatalf("收了不是 Word 的文件：%d", status)
	}
	docx := layoutTestDocx(t, false)
	if status, body := env.upload(up.UploadURL, `C:\稿子\茶的故事.docx`, docx); status != http.StatusOK {
		t.Fatalf("传 Word：%d %s", status, body)
	}
	if status, _ := env.upload(up.UploadURL, "茶的故事.docx", docx); status != http.StatusConflict {
		t.Fatalf("同一个地址传了两次：%d", status)
	}
	out, errText := call[agentLayoutOutput](t, cs, "layout_article", map[string]any{"account": "fbif", "upload_id": up.UploadID})
	if errText != "" || out.Status != "done" {
		t.Fatalf("没排成：%s %+v", errText, out)
	}
	if req := env.layout.node.lastRequest(t); req.Input.Kind != layoutCarrierDocx || req.Input.FileName != "茶的故事.docx" {
		t.Fatalf("交给排版程序的不是「上传 Word」：%+v", req.Input)
	}
	if _, errText := call[agentLayoutOutput](t, cs, "layout_article", map[string]any{"account": "fbif", "upload_id": up.UploadID}); errText == "" {
		t.Fatal("一个上传用了两次")
	}
	status, got := env.handoff(out.ResultID, agentTestColleague)
	if status != http.StatusOK || got.Kind != agentKindWord || got.FileName != "茶的故事.docx" {
		t.Fatalf("编辑器取回：%d %+v", status, got)
	}
	file := env.request(http.MethodGet, "/api/agent/results/"+out.ResultID+"/file", agentTestColleague, nil, nil)
	if body := readAll(t, file); file.StatusCode != http.StatusOK || body != string(docx) {
		t.Fatalf("取回的 Word 原件不对：%d %d 字节", file.StatusCode, len(body))
	}
	if status, _ := env.upload(env.issuer+"/api/agent/uploads/ag_AAAAAAAAAAAAAAAAAAAAAA", "a.docx", docx); status != http.StatusNotFound {
		t.Fatalf("没发过的地址收了文件：%d", status)
	}
}

// 框里只粘一个链接就是抓链接（和编辑器一样）；FoodTalks 账号的编辑器链接在 /ft/ 下。
func TestAgentTextThatIsALinkFetchesIt(t *testing.T) {
	env := newAgentTestEnv(t)
	env.layout.fetcher.article = layoutFetchedArticle{Title: "标题", Blocks: []Block{{K: "txt", Text: "正文"}}}
	cs := env.session(agentTestPerson)
	out, errText := call[agentLayoutOutput](t, cs, "layout_article", map[string]any{"account": "ft-default", "text": " https://mp.weixin.qq.com/s/abc \n"})
	if errText != "" || out.Status != "done" || out.EditorURL != env.issuer+"/ft/ft-default?agent="+out.ResultID {
		t.Fatalf("没按链接排：%s %+v", errText, out)
	}
	if req := env.layout.node.lastRequest(t); req.Input.Kind != layoutCarrierBlocks {
		t.Fatalf("没抓链接：%+v", req.Input)
	}
	if _, got := env.handoff(out.ResultID, agentTestPerson); got.Kind != agentKindURL || got.URL != "https://mp.weixin.qq.com/s/abc" || got.Channel != "ft" {
		t.Fatalf("编辑器取回：%+v", got)
	}
	if _, errText := call[agentLayoutOutput](t, cs, "layout_article", map[string]any{"account": "fbif", "text": "字", "markdown": "字"}); errText == "" {
		t.Fatal("给了两样也收了")
	}
	if _, errText := call[agentLayoutOutput](t, cs, "layout_article", map[string]any{"account": "nope", "text": "字"}); errText == "" {
		t.Fatal("不认识的账号也收了")
	}
}

// 排得慢：一次调用最多等一会儿，给编号；用 get_layout_result 接着等到排好。
func TestAgentSlowLayoutHandsBackResultID(t *testing.T) {
	env := newAgentTestEnv(t)
	env.layout.node.block = make(chan struct{})
	cs := env.session(agentTestPerson)
	out, _ := call[agentLayoutOutput](t, cs, "layout_article", map[string]any{"account": "fbif", "text": "一段文字"})
	if out.Status != "running" || out.ResultID == "" || out.EditorURL != "" {
		t.Fatalf("没排完应该给编号：%+v", out)
	}
	close(env.layout.node.block)
	done, _ := call[agentLayoutOutput](t, cs, "get_layout_result", map[string]any{"result_id": out.ResultID})
	if done.Status != "done" || done.EditorURL == "" {
		t.Fatalf("接着等没拿到：%+v", done)
	}
}

// 人停用了：已经拿到的令牌下一次调用就进不来。
func TestAgentStopsWhenPersonDisabled(t *testing.T) {
	env := newAgentTestEnv(t)
	token := env.login(agentTestPerson)
	if _, err := env.connect(token); err != nil {
		t.Fatalf("停用前连不上：%v", err)
	}
	user, _, _ := env.auth.users.Get(agentTestPerson)
	user.Status = authUserDisabled
	if err := env.auth.users.Upsert(user); err != nil {
		t.Fatal(err)
	}
	if _, err := env.connect(token); err == nil {
		t.Fatal("停用了还能用")
	}
	resp := env.request(http.MethodPost, "/mcp", "", strings.NewReader(`{}`), map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatalf("没带令牌要回 401 并指向元数据：%d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
}

// 离职、停用（中台停用名单上）：浏览器那边入口挡，智能体已经拿到的令牌下一次调用、换令牌也进不来。
func TestAgentStopsWhenPersonOnBlocklist(t *testing.T) {
	env := newAgentTestEnv(t)
	token := env.login(agentTestPerson)
	if _, err := env.connect(token); err != nil {
		t.Fatalf("上名单前连不上：%v", err)
	}
	env.auth.blocklist.state.Entries["union_me"] = blockEntry{Reason: "离职"}
	if _, err := env.connect(token); err == nil {
		t.Fatal("上了停用名单还能用")
	}
}

// 飞书文档用这个人自己的飞书授权读。
func TestAgentReadsFeishuAsThePerson(t *testing.T) {
	env := newAgentTestEnv(t)
	env.auth.putAccessToken(agentTestPerson, "u-token-me", time.Time{})
	hub := &agentHub{auth: env.auth}
	token, err := hub.feishuTokenFor(agentTestPerson)(context.Background())
	if err != nil || token != "u-token-me" {
		t.Fatalf("取到的不是本人的飞书令牌：%q %v", token, err)
	}
	if _, err := hub.feishuTokenFor(agentTestColleague)(context.Background()); err == nil {
		t.Fatal("没授权过飞书的人也取到了令牌")
	}
}

func TestAgentMarkdownFileName(t *testing.T) {
	for in, want := range map[string]string{"": "文章.md", " 茶/的:故事 ": "茶的故事.md", "...": "文章.md", strings.Repeat("长", 90): strings.Repeat("长", 80) + ".md"} {
		if got := agentMarkdownFileName(in); got != want {
			t.Errorf("agentMarkdownFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

// 智能体入口只排版和校对：不建发布单、不碰 FoodTalks、不碰第三方平台、不存公众号草稿。
func TestAgentNeverTouchesPlatforms(t *testing.T) {
	typ := reflect.TypeOf(agentHub{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Type.String())
		for _, word := range []string{"publish", "foodtalks", "distribution", "external"} {
			if strings.Contains(name, word) {
				t.Fatalf("agentHub 不许带平台依赖：%s %s", typ.Field(i).Name, typ.Field(i).Type)
			}
		}
	}
	raw, err := os.ReadFile("agent_mcp.go")
	if err != nil {
		t.Fatal(err)
	}
	code := stripGoLineComments(string(raw))
	for _, word := range []string{"publish", "Publish", "distribution", "Distribution", "external", "External",
		"site-sync", "official-sync", "CreateNews", "draft", "Draft", "freepublish"} {
		if strings.Contains(code, word) {
			t.Fatalf("agent_mcp.go 不许碰 %s", word)
		}
	}
}
