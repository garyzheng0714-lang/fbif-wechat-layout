package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	codexID    = "https://chatgpt.com/oauth/codex/client.json"
	verifier   = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXkaaaaaa"
	testIssuer = "https://layout.example.com"
)

type harness struct {
	t       *testing.T
	srv     *Server
	mux     *http.ServeMux
	now     time.Time
	person  string // 浏览器上登录的人（header X-Test-Person）
	blocked map[string]bool
	mu      sync.Mutex
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	// 从真实时间起算：SDK 判访问令牌过期用的是 time.Now。
	h := &harness{t: t, now: time.Now().Truncate(time.Second), blocked: map[string]bool{}}
	srv, err := New(Options{
		Issuer:       testIssuer,
		ResourcePath: "/mcp",
		ResourceName: "排版工具",
		Scope:        "layout",
		StateFile:    filepath.Join(t.TempDir(), "grants.json"),
		Person: func(r *http.Request) (Person, bool) {
			id := r.Header.Get("X-Test-Person")
			return Person{ID: id, Name: "王" + id}, id != ""
		},
		Allowed: func(_ context.Context, id string) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.blocked[id] {
				return errors.New("已停用")
			}
			return nil
		},
		Clients: []Client{{ID: codexID, Name: "Codex", RedirectURIs: []string{"http://127.0.0.1/callback", "http://localhost/callback"}}},
		Now:     func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now },
		Logf:    func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
	h.mux = http.NewServeMux()
	srv.Register(h.mux)
	h.mux.Handle("/mcp", srv.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := PersonFrom(r.Context())
		_, _ = io.WriteString(w, p.ID)
	})))
	return h
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	h.now = h.now.Add(d)
	h.mu.Unlock()
}

func (h *harness) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func (h *harness) get(path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if h.person != "" {
		req.Header.Set("X-Test-Person", h.person)
	}
	return h.do(req)
}

func (h *harness) postForm(path string, form url.Values, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if h.person != "" {
		req.Header.Set("X-Test-Person", h.person)
	}
	return h.do(req)
}

func authorizeQuery(clientID, redirect string) url.Values {
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirect},
		"code_challenge":        {pkceS256(verifier)},
		"code_challenge_method": {"S256"},
		"state":                 {"st"},
		"resource":              {testIssuer + "/mcp"},
	}
}

var requestField = `name="request" value="`

// consentAndAllow 打开授权页、点允许，返回跳回去的地址。
func (h *harness) consentAndAllow(clientID, redirect string) *url.URL {
	h.t.Helper()
	page := h.get("/oauth/authorize?" + authorizeQuery(clientID, redirect).Encode())
	if page.Code != http.StatusOK {
		h.t.Fatalf("授权页 %d：%s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	i := strings.Index(body, requestField)
	if i < 0 {
		h.t.Fatalf("授权页里没有确认表单：%s", body)
	}
	id := body[i+len(requestField):]
	id = id[:strings.IndexByte(id, '"')]
	rec := h.postForm("/oauth/authorize", url.Values{"request": {id}, "decision": {"allow"}}, testIssuer)
	if rec.Code != http.StatusFound {
		h.t.Fatalf("点允许 %d：%s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		h.t.Fatal(err)
	}
	return loc
}

func (h *harness) token(form url.Values) (int, map[string]any) {
	h.t.Helper()
	rec := h.postForm("/oauth/token", form, "")
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (h *harness) login(clientID, redirect string) (access, refresh string) {
	h.t.Helper()
	loc := h.consentAndAllow(clientID, redirect)
	status, out := h.token(url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {loc.Query().Get("code")},
		"redirect_uri":  {redirect},
		"client_id":     {clientID},
		"code_verifier": {verifier},
		"resource":      {testIssuer + "/mcp"},
	})
	if status != http.StatusOK {
		h.t.Fatalf("换令牌 %d：%v", status, out)
	}
	return out["access_token"].(string), out["refresh_token"].(string)
}

func (h *harness) callMCP(access string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	if access != "" {
		req.Header.Set("Authorization", "Bearer "+access)
	}
	return h.do(req)
}

func TestMetadataPointsClientsToThisServer(t *testing.T) {
	h := newHarness(t)
	rec := h.callMCP("")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("没带令牌应是 401，实际 %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, `resource_metadata="`+testIssuer+`/.well-known/oauth-protected-resource/mcp"`) || !strings.Contains(got, `scope="layout"`) {
		t.Fatalf("WWW-Authenticate 不对：%s", got)
	}
	var prm map[string]any
	_ = json.Unmarshal(h.get("/.well-known/oauth-protected-resource/mcp").Body.Bytes(), &prm)
	if prm["resource"] != testIssuer+"/mcp" || prm["authorization_servers"].([]any)[0] != testIssuer {
		t.Fatalf("资源元数据不对：%v", prm)
	}
	var asm map[string]any
	_ = json.Unmarshal(h.get("/.well-known/oauth-authorization-server").Body.Bytes(), &asm)
	// 不声明 CIMD：声明了，支持 CIMD 的客户端就不再动态注册，没登记的全被挡住。
	if _, cimd := asm["client_id_metadata_document_supported"]; cimd {
		t.Fatalf("不该声明支持 CIMD：%v", asm)
	}
	if asm["issuer"] != testIssuer ||
		asm["authorization_response_iss_parameter_supported"] != true ||
		asm["token_endpoint_auth_methods_supported"].([]any)[0] != "none" {
		t.Fatalf("授权服务器元数据不对：%v", asm)
	}
}

func TestCodexLogsInWithPKCEAndCallsMCPAsThePerson(t *testing.T) {
	h := newHarness(t)
	h.person = "u1"
	page := h.get("/oauth/authorize?" + authorizeQuery(codexID, "http://127.0.0.1:53682/callback").Encode())
	if !strings.Contains(page.Body.String(), "Codex 要以王u1的身份使用排版工具") {
		t.Fatalf("确认页要写清谁、哪个应用：%s", page.Body.String())
	}
	h.person = "u1"
	loc := h.consentAndAllow(codexID, "http://127.0.0.1:53682/callback")
	if loc.Host != "127.0.0.1:53682" || loc.Query().Get("state") != "st" || loc.Query().Get("iss") != testIssuer {
		t.Fatalf("跳回地址不对：%s", loc)
	}
	// 码只能用一次；verifier 不对换不了。
	bad, _ := h.token(url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")},
		"redirect_uri": {"http://127.0.0.1:53682/callback"}, "client_id": {codexID}, "code_verifier": {strings.Repeat("x", 43)}})
	if bad != http.StatusBadRequest {
		t.Fatalf("verifier 不对应拒，实际 %d", bad)
	}

	access, _ := h.login(codexID, "http://localhost:6001/callback")
	rec := h.callMCP(access)
	if rec.Code != http.StatusOK || rec.Body.String() != "u1" {
		t.Fatalf("带令牌调 MCP：%d %s", rec.Code, rec.Body.String())
	}
	h.advance(accessTTL + time.Second)
	if rec := h.callMCP(access); rec.Code != http.StatusUnauthorized {
		t.Fatalf("访问令牌过期应 401，实际 %d", rec.Code)
	}
}

func TestAuthorizationCodeReuseRevokesTheGrant(t *testing.T) {
	h := newHarness(t)
	h.person = "u1"
	redirect := "http://127.0.0.1:7000/callback"
	loc := h.consentAndAllow(codexID, redirect)
	form := url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")},
		"redirect_uri": {redirect}, "client_id": {codexID}, "code_verifier": {verifier}}
	status, out := h.token(form)
	if status != http.StatusOK {
		t.Fatalf("第一次换 %d %v", status, out)
	}
	if again, _ := h.token(form); again != http.StatusBadRequest {
		t.Fatalf("同一个码第二次应拒，实际 %d", again)
	}
	if rec := h.callMCP(out["access_token"].(string)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("码被重复使用后第一次发的令牌要作废，实际 %d", rec.Code)
	}
}

func TestRefreshRotatesAndDetectsReuse(t *testing.T) {
	h := newHarness(t)
	h.person = "u1"
	_, r0 := h.login(codexID, "http://127.0.0.1:7000/callback")
	refresh := func(token string) (int, map[string]any) {
		return h.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}, "client_id": {codexID}})
	}
	status, out := refresh(r0)
	if status != http.StatusOK {
		t.Fatalf("刷新 %d %v", status, out)
	}
	r1 := out["refresh_token"].(string)
	// 同一台电脑上另一个进程一分钟内也拿 r0 刷新：给它一个新的，两边的都认。
	status, out = refresh(r0)
	if status != http.StatusOK {
		t.Fatalf("宽限期内旧令牌应能刷新：%d %v", status, out)
	}
	r2 := out["refresh_token"].(string)
	if s, _ := refresh(r1); s != http.StatusOK {
		t.Fatalf("r1 应还认，实际 %d", s)
	}
	h.advance(2 * refreshGrace)
	// r0 过了宽限期又来：当成被盗用，整份吊销，连 r2 也不认。
	if s, _ := refresh(r0); s != http.StatusBadRequest {
		t.Fatalf("过了宽限期再用旧令牌应拒，实际 %d", s)
	}
	if s, _ := refresh(r2); s != http.StatusBadRequest {
		t.Fatalf("吊销后 r2 也不应认，实际 %d", s)
	}
}

func TestDisabledPersonIsShutOutImmediately(t *testing.T) {
	h := newHarness(t)
	h.person = "u1"
	access, refresh := h.login(codexID, "http://127.0.0.1:7000/callback")
	h.mu.Lock()
	h.blocked["u1"] = true
	h.mu.Unlock()
	if rec := h.callMCP(access); rec.Code != http.StatusUnauthorized {
		t.Fatalf("停用后再调应 401，实际 %d", rec.Code)
	}
	if s, _ := h.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {codexID}}); s != http.StatusBadRequest {
		t.Fatalf("停用后刷新应拒，实际 %d", s)
	}
	page := h.get("/oauth/authorize?" + authorizeQuery(codexID, "http://127.0.0.1:7000/callback").Encode())
	if page.Code != http.StatusForbidden {
		t.Fatalf("停用的人打开授权页应 403，实际 %d", page.Code)
	}
}

func TestGrantsSurviveRestart(t *testing.T) {
	h := newHarness(t)
	h.person = "u1"
	access, refresh := h.login(codexID, "http://127.0.0.1:7000/callback")
	reopened, err := New(h.srv.opt)
	if err != nil {
		t.Fatal(err)
	}
	h.srv = reopened
	h.mux = http.NewServeMux()
	reopened.Register(h.mux)
	h.mux.Handle("/mcp", reopened.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	if rec := h.callMCP(access); rec.Code != http.StatusOK {
		t.Fatalf("重启后访问令牌应还能用，实际 %d", rec.Code)
	}
	if s, _ := h.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {codexID}}); s != http.StatusOK {
		t.Fatalf("重启后刷新令牌应还能用，实际 %d", s)
	}
}

func TestRevokeEndsTheGrant(t *testing.T) {
	h := newHarness(t)
	h.person = "u1"
	access, refresh := h.login(codexID, "http://127.0.0.1:7000/callback")
	if rec := h.postForm("/oauth/revoke", url.Values{"token": {refresh}, "client_id": {codexID}}, ""); rec.Code != http.StatusOK {
		t.Fatalf("吊销 %d", rec.Code)
	}
	if rec := h.callMCP(access); rec.Code != http.StatusUnauthorized {
		t.Fatalf("吊销后访问令牌也作废，实际 %d", rec.Code)
	}
}

func TestUnverifiedRedirectNeverLeavesTheSite(t *testing.T) {
	h := newHarness(t)
	h.person = "u1"
	for _, redirect := range []string{"https://evil.example/callback", "http://127.0.0.1:7000/other", "http://10.0.0.1/callback"} {
		rec := h.get("/oauth/authorize?" + authorizeQuery(codexID, redirect).Encode())
		if rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" {
			t.Fatalf("%s：没登记的回调地址只能出说明页，实际 %d %s", redirect, rec.Code, rec.Header().Get("Location"))
		}
	}
	rec := h.get("/oauth/authorize?" + authorizeQuery("https://unknown.example/client.json", "http://127.0.0.1/callback").Encode())
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" {
		t.Fatalf("没登记的客户端只能出说明页，实际 %d", rec.Code)
	}
	// 回调地址核实过的，别的错按 OAuth 带 error 跳回去。
	q := authorizeQuery(codexID, "http://127.0.0.1:7000/callback")
	q.Set("code_challenge_method", "plain")
	rec = h.get("/oauth/authorize?" + q.Encode())
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || loc.Query().Get("error") != "invalid_request" {
		t.Fatalf("不带 S256 应跳回 invalid_request，实际 %d %s", rec.Code, rec.Header().Get("Location"))
	}
	q = authorizeQuery(codexID, "http://127.0.0.1:7000/callback")
	q.Set("resource", "https://other.example/mcp")
	rec = h.get("/oauth/authorize?" + q.Encode())
	loc, _ = url.Parse(rec.Header().Get("Location"))
	if loc == nil || loc.Query().Get("error") != "invalid_target" {
		t.Fatalf("别家的 resource 应跳回 invalid_target，实际 %s", rec.Header().Get("Location"))
	}
}

func TestConsentOnlyFromThisSiteAndSamePerson(t *testing.T) {
	h := newHarness(t)
	h.person = "u1"
	page := h.get("/oauth/authorize?" + authorizeQuery(codexID, "http://127.0.0.1:7000/callback").Encode()).Body.String()
	i := strings.Index(page, requestField)
	id := page[i+len(requestField):]
	id = id[:strings.IndexByte(id, '"')]
	if rec := h.postForm("/oauth/authorize", url.Values{"request": {id}, "decision": {"allow"}}, "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Fatalf("别的网站提交的确认应拒，实际 %d", rec.Code)
	}
	page = h.get("/oauth/authorize?" + authorizeQuery(codexID, "http://127.0.0.1:7000/callback").Encode()).Body.String()
	i = strings.Index(page, requestField)
	id = page[i+len(requestField):]
	id = id[:strings.IndexByte(id, '"')]
	h.person = "u2"
	if rec := h.postForm("/oauth/authorize", url.Values{"request": {id}, "decision": {"allow"}}, testIssuer); rec.Code != http.StatusForbidden {
		t.Fatalf("换了人点的确认应拒，实际 %d", rec.Code)
	}
}

func TestDenyRedirectsAccessDenied(t *testing.T) {
	h := newHarness(t)
	h.person = "u1"
	page := h.get("/oauth/authorize?" + authorizeQuery(codexID, "http://127.0.0.1:7000/callback").Encode()).Body.String()
	i := strings.Index(page, requestField)
	id := page[i+len(requestField):]
	id = id[:strings.IndexByte(id, '"')]
	rec := h.postForm("/oauth/authorize", url.Values{"request": {id}, "decision": {"deny"}}, testIssuer)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc == nil || loc.Query().Get("error") != "access_denied" || loc.Query().Get("code") != "" {
		t.Fatalf("点取消应跳回 access_denied，实际 %s", rec.Header().Get("Location"))
	}
}

func TestDynamicRegistrationOnlyForThisComputer(t *testing.T) {
	h := newHarness(t)
	register := func(body string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := h.do(req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	for _, bad := range []string{
		`{"client_name":"x","redirect_uris":["https://evil.example/cb"]}`,
		`{"client_name":"x","redirect_uris":["javascript:alert(1)"]}`,
		`{"client_name":"x","redirect_uris":["http://127.0.0.1/cb"],"token_endpoint_auth_method":"client_secret_basic"}`,
		`{"client_name":"x","redirect_uris":[]}`,
	} {
		if status, _ := register(bad); status != http.StatusBadRequest {
			t.Fatalf("%s 应拒，实际 %d", bad, status)
		}
	}
	status, out := register(`{"client_name":"Cursor","redirect_uris":["cursor://anysphere.cursor-mcp/oauth/callback","http://127.0.0.1/callback"]}`)
	if status != http.StatusCreated {
		t.Fatalf("回到本机的应能注册：%d %v", status, out)
	}
	id := out["client_id"].(string)
	h.person = "u1"
	page := h.get("/oauth/authorize?" + authorizeQuery(id, "http://127.0.0.1:9999/callback").Encode())
	if !strings.Contains(page.Body.String(), "本机上的「Cursor」要以王u1的身份使用排版工具") {
		t.Fatalf("动态注册的应用名是它自己报的，确认页要写「本机上的」：%s", page.Body.String())
	}
	access, _ := h.login(id, "cursor://anysphere.cursor-mcp/oauth/callback")
	if rec := h.callMCP(access); rec.Code != http.StatusOK {
		t.Fatalf("动态注册的应用登录后调 MCP：%d", rec.Code)
	}
}

func TestRedirectLoopbackIgnoresPortOnly(t *testing.T) {
	registered := []string{"http://127.0.0.1/callback", "http://localhost/callback"}
	for raw, want := range map[string]bool{
		"http://127.0.0.1:1234/callback":     true,
		"http://localhost:80/callback":       true,
		"http://127.0.0.1/callback":          true,
		"http://127.0.0.1:1234/callback2":    false,
		"http://127.0.0.1:1234/callback?x=1": false,
		"https://127.0.0.1:1234/callback":    false,
		"http://[::1]:1234/callback":         false,
		"http://127.0.0.1.evil.com/callback": false,
	} {
		if got := redirectAllowed(registered, raw); got != want {
			t.Errorf("%s：期望 %v，实际 %v", raw, want, got)
		}
	}
}
