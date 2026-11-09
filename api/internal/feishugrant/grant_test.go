package feishugrant

// 照抄、不改：和 grant.go 一起复制进项目，go test 必须全过。用假的飞书，不连外网。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeFeishu 是假的飞书：授权码 code-<名字> 换令牌；refresh_token 只能用一次；可以让刷新失败或卡住。
type fakeFeishu struct {
	mu       sync.Mutex
	srv      *httptest.Server
	n        int
	valid    map[string]bool       // 还能用的 refresh_token
	users    map[string]FeishuUser // access_token → 账号
	refreshN int
	fail     func() (int, string) // 刷新时强制回这个（状态码、回包）
	noRT     bool                 // 换码时不给 refresh_token
	entered  chan struct{}        // 刷新进来了
	hold     chan struct{}        // 刷新卡在这，关了才继续
}

func newFakeFeishu(t *testing.T) *fakeFeishu {
	f := &fakeFeishu{valid: map[string]bool{}, users: map[string]FeishuUser{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeFeishu) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/user_info" {
		f.mu.Lock()
		u, ok := f.users[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":99991668}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": u})
		return
	}
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body["client_secret"] != "secret-"+body["client_id"] {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":20002,"error":"invalid_client"}`))
		return
	}
	var name string
	switch body["grant_type"] {
	case "authorization_code":
		name = strings.TrimPrefix(body["code"], "code-")
	case "refresh_token":
		f.mu.Lock()
		f.refreshN++
		entered, hold, fail := f.entered, f.hold, f.fail
		f.mu.Unlock()
		if entered != nil {
			entered <- struct{}{}
		}
		if hold != nil {
			<-hold
		}
		if fail != nil {
			status, out := fail()
			w.WriteHeader(status)
			_, _ = w.Write([]byte(out))
			return
		}
		f.mu.Lock()
		ok := f.valid[body["refresh_token"]]
		delete(f.valid, body["refresh_token"])
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":20073,"error":"invalid_grant","error_description":"used"}`))
			return
		}
		name = strings.SplitN(strings.TrimPrefix(body["refresh_token"], "SECRET-RT-"), "-", 2)[0]
	}
	f.mu.Lock()
	f.n++
	at, rt := fmt.Sprintf("SECRET-AT-%s-%d", name, f.n), fmt.Sprintf("SECRET-RT-%s-%d", name, f.n)
	f.users[at] = FeishuUser{OpenID: "ou_" + name, UnionID: "on_" + name, Name: name}
	out := map[string]any{"code": 0, "access_token": at, "expires_in": 7200, "refresh_token_expires_in": 604800}
	if !f.noRT {
		f.valid[rt] = true
		out["refresh_token"] = rt
	}
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(out)
}

func (f *fakeFeishu) refreshes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshN
}

func (f *fakeFeishu) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

type harness struct {
	g     *Grants
	mux   *http.ServeMux
	f     *fakeFeishu
	file  string
	logs  *bytes.Buffer
	clock time.Time
}

var testApps = map[string]App{
	"fbif": {AppID: "cli_fbif", AppSecret: "secret-cli_fbif", RedirectURL: "https://app.test/auth/feishu/fbif/qr-callback"},
	"fude": {AppID: "cli_fude", AppSecret: "secret-cli_fude", RedirectURL: "https://app.test/auth/feishu/fude/qr-callback"},
}

// 测试里 X-User、X-App 头就是登录的人和他的飞书应用（项目里是登录模板的 CurrentUser）。
func who(r *http.Request) (string, string) { return r.Header.Get("X-User"), r.Header.Get("X-App") }

func newHarness(t *testing.T, f *fakeFeishu, file string, check func(*http.Request, string, FeishuUser) error) *harness {
	t.Helper()
	if file == "" {
		file = filepath.Join(t.TempDir(), "feishu-grants.json")
	}
	h := &harness{f: f, file: file, logs: &bytes.Buffer{}, clock: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	g, err := New(ctx, Options{
		Apps: testApps, Scopes: "drive:drive docx:document", SealKey: strings.Repeat("k", 44), File: file,
		Who: who, Check: check, Logger: slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatal(err)
	}
	g.authorizeURL, g.tokenURL, g.userInfoURL = "https://feishu.test/authorize", f.srv.URL+"/token", f.srv.URL+"/user_info"
	g.now = func() time.Time { return h.clock }
	h.g = g
	h.mux = http.NewServeMux()
	g.Register(h.mux, func(next http.Handler) http.Handler { return next })
	t.Cleanup(func() {
		if strings.Contains(h.logs.String(), "SECRET-") {
			t.Errorf("日志里有令牌：%s", h.logs.String())
		}
	})
	return h
}

func (h *harness) req(method, target, user, app string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	if user != "" {
		r.Header.Set("X-User", user)
		r.Header.Set("X-App", app)
	}
	return r
}

func (h *harness) serve(r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, r)
	return rec
}

// start 走一遍「去授权」，返回飞书的授权地址和状态 cookie。
func (h *harness) start(t *testing.T, user, app, next string) (*url.URL, *http.Cookie) {
	t.Helper()
	rec := h.serve(h.req("GET", "/auth/feishu-grant?next="+url.QueryEscape(next), user, app))
	if rec.Code != http.StatusFound {
		t.Fatalf("去授权应 302，实际 %d", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	for _, c := range rec.Result().Cookies() {
		if c.Name == stateCookie {
			return loc, c
		}
	}
	t.Fatal("没设状态 cookie")
	return nil, nil
}

// grant 授权一次（飞书账号是 name），返回回调跳去哪。
func (h *harness) grant(t *testing.T, user, app, name, next string) string {
	t.Helper()
	loc, c := h.start(t, user, app, next)
	cb := h.req("GET", "/auth/feishu/"+app+"/qr-callback?code=code-"+name+"&state="+loc.Query().Get("state"), user, app)
	cb.AddCookie(c)
	return h.serve(cb).Header().Get("Location")
}

func TestStartAsksForOfflineAccessAndCallbackStoresSealedGrant(t *testing.T) {
	f := newFakeFeishu(t)
	h := newHarness(t, f, "", nil)
	loc, c := h.start(t, "u1", "fbif", "/task/1?x=1")
	q := loc.Query()
	if q.Get("client_id") != "cli_fbif" || q.Get("redirect_uri") != testApps["fbif"].RedirectURL || q.Get("response_type") != "code" || q.Get("state") == "" {
		t.Fatalf("授权地址不对：%s", loc)
	}
	if q.Get("scope") != "offline_access drive:drive docx:document" {
		t.Fatalf("scope 要带 offline_access：%q", q.Get("scope"))
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("状态 cookie 要 HttpOnly、Secure、SameSite=Lax：%+v", c)
	}
	if h.g.Status(h.req("GET", "/", "u1", "fbif")) != "required" {
		t.Fatal("授权前 Status 应是 required")
	}

	cb := h.req("GET", "/auth/feishu/fbif/qr-callback?code=code-alice&state="+q.Get("state"), "u1", "fbif")
	cb.AddCookie(c)
	rec := h.serve(cb)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/task/1?x=1" {
		t.Fatalf("授权完应回 next：%d %s", rec.Code, rec.Header().Get("Location"))
	}
	tok, err := h.g.Token(h.req("GET", "/", "u1", "fbif"))
	if err != nil || !strings.HasPrefix(tok, "SECRET-AT-alice-") {
		t.Fatalf("授权后应拿到 access_token：%q %v", tok, err)
	}
	if h.g.Status(h.req("GET", "/", "u1", "fbif")) != "ok" {
		t.Fatal("授权后 Status 应是 ok")
	}
	raw, _ := os.ReadFile(h.file)
	if strings.Contains(string(raw), "SECRET-") {
		t.Fatal("文件里不能有明文令牌")
	}
	if info, _ := os.Stat(h.file); info.Mode().Perm() != 0o600 {
		t.Fatalf("文件权限应 0600：%v", info.Mode().Perm())
	}
	if f.refreshes() != 0 {
		t.Fatal("刚授权的 access_token 应直接用缓存，不去刷新")
	}

	// 重启：从文件读回来，用 refresh_token 换新的
	h2 := newHarness(t, f, h.file, nil)
	h2.clock = h.clock
	if tok, err := h2.g.Token(h2.req("GET", "/", "u1", "fbif")); err != nil || !strings.HasPrefix(tok, "SECRET-AT-alice-") || f.refreshN != 1 {
		t.Fatalf("重启后应用存着的 refresh_token 换：%q %v %d", tok, err, f.refreshes())
	}
}

func TestCallbackRejectsWhatItDidNotStart(t *testing.T) {
	f := newFakeFeishu(t)
	h := newHarness(t, f, "", nil)
	loc, c := h.start(t, "u1", "fbif", "/x")
	state := loc.Query().Get("state")
	cases := []struct {
		name   string
		target string
		user   string
		app    string
		cookie bool
	}{
		{"没有状态 cookie（换了浏览器）", "/auth/feishu/fbif/qr-callback?code=code-a&state=" + state, "u1", "fbif", false},
		{"state 被改", "/auth/feishu/fbif/qr-callback?code=code-a&state=xx", "u1", "fbif", true},
		{"换了登录的人", "/auth/feishu/fbif/qr-callback?code=code-a&state=" + state, "u2", "fbif", true},
		{"回到别的应用的回调", "/auth/feishu/fude/qr-callback?code=code-a&state=" + state, "u1", "fude", true},
	}
	for _, tc := range cases {
		r := h.req("GET", tc.target, tc.user, tc.app)
		if tc.cookie {
			r.AddCookie(c)
		}
		rec := h.serve(r)
		if !strings.Contains(rec.Header().Get("Location"), "feishu_grant=failed") {
			t.Errorf("%s：应回 feishu_grant=failed，实际 %s", tc.name, rec.Header().Get("Location"))
		}
	}
	if h.g.Status(h.req("GET", "/", "u1", "fbif")) != "required" || h.g.Status(h.req("GET", "/", "u2", "fbif")) != "required" {
		t.Fatal("对不上的回调不能存授权")
	}
	h.clock = h.clock.Add(stateTTL + time.Second)
	r := h.req("GET", "/auth/feishu/fbif/qr-callback?code=code-a&state="+state, "u1", "fbif")
	r.AddCookie(c)
	if !strings.Contains(h.serve(r).Header().Get("Location"), "feishu_grant=failed") {
		t.Fatal("放太久的状态不认")
	}
	// 用户在飞书点了拒绝
	loc, c = h.start(t, "u1", "fbif", "/x")
	r = h.req("GET", "/auth/feishu/fbif/qr-callback?error=access_denied&state="+loc.Query().Get("state"), "u1", "fbif")
	r.AddCookie(c)
	if got := h.serve(r).Header().Get("Location"); got != "/x?feishu_grant=denied" {
		t.Fatalf("拒绝授权应回 feishu_grant=denied：%s", got)
	}
}

func TestCallbackWithoutRefreshTokenOrWrongAccountIsNotStored(t *testing.T) {
	f := newFakeFeishu(t)
	h := newHarness(t, f, "", func(_ *http.Request, _ string, u FeishuUser) error {
		if u.UnionID != "on_alice" {
			return errors.New("union_id 对不上")
		}
		return nil
	})
	if got := h.grant(t, "u1", "fbif", "mallory", "/"); got != "/?feishu_grant=mismatch" {
		t.Fatalf("飞书账号对不上应回 mismatch：%s", got)
	}
	f.set(func() { f.noRT = true })
	if got := h.grant(t, "u1", "fbif", "alice", "/"); got != "/?feishu_grant=failed" {
		t.Fatalf("没给 refresh_token 应回 failed：%s", got)
	}
	if !strings.Contains(h.logs.String(), "offline_access") {
		t.Fatal("没给 refresh_token 时日志要点明 offline_access")
	}
	if h.g.Status(h.req("GET", "/", "u1", "fbif")) != "required" {
		t.Fatal("都不该存授权")
	}
}

func TestRefreshRejectedNeedsRegrantAndStopsCallingFeishu(t *testing.T) {
	for _, body := range []string{`{"code":20037,"error":"invalid_grant"}`, `{"code":20064,"error":"invalid_grant"}`, `{"code":20026}`} {
		f := newFakeFeishu(t)
		h := newHarness(t, f, "", nil)
		h.grant(t, "u1", "fbif", "alice", "/")
		h.clock = h.clock.Add(3 * time.Hour) // access_token 过期了
		f.set(func() { f.fail = func() (int, string) { return http.StatusBadRequest, body } })
		_, err := h.g.Token(h.req("GET", "/", "u1", "fbif"))
		if !errors.Is(err, ErrGrantRequired) {
			t.Fatalf("%s：应 ErrGrantRequired，实际 %v", body, err)
		}
		if _, err := h.g.Token(h.req("GET", "/", "u1", "fbif")); !errors.Is(err, ErrGrantRequired) || f.refreshes() != 1 {
			t.Fatalf("%s：标了失效之后不再找飞书：%v %d", body, err, f.refreshes())
		}
		if h.g.Status(h.req("GET", "/", "u1", "fbif")) != "required" {
			t.Fatal("失效后 Status 应是 required")
		}
		rec := httptest.NewRecorder()
		h.g.WriteError(rec, err)
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != http.StatusForbidden || out["code"] != "feishu_grant_required" || out["grant_url"] != "/auth/feishu-grant" {
			t.Fatalf("应 403 feishu_grant_required 带 grant_url：%d %s", rec.Code, rec.Body)
		}
		// 重新授权就好
		f.set(func() { f.fail = nil })
		h.grant(t, "u1", "fbif", "alice", "/")
		if _, err := h.g.Token(h.req("GET", "/", "u1", "fbif")); err != nil {
			t.Fatalf("重新授权后应能用：%v", err)
		}
	}
}

func TestTemporaryFailureKeepsGrant(t *testing.T) {
	for _, fail := range []func() (int, string){
		func() (int, string) { return http.StatusInternalServerError, `{"code":20050,"error":"server_error"}` },
		func() (int, string) { return http.StatusServiceUnavailable, `{"code":20072}` },
		func() (int, string) { return http.StatusBadGateway, `<html>bad gateway</html>` },
	} {
		f := newFakeFeishu(t)
		h := newHarness(t, f, "", nil)
		h.grant(t, "u1", "fbif", "alice", "/")
		before := h.g.grants["u1"].RefreshSeal
		h.clock = h.clock.Add(3 * time.Hour)
		f.set(func() { f.fail = fail })
		_, err := h.g.Token(h.req("GET", "/", "u1", "fbif"))
		if err == nil || errors.Is(err, ErrGrantRequired) {
			t.Fatalf("临时故障不能当成要重新授权：%v", err)
		}
		if strings.Contains(err.Error(), "SECRET-") {
			t.Fatal("错误里不能有令牌")
		}
		if h.g.grants["u1"].RefreshSeal != before || h.g.Status(h.req("GET", "/", "u1", "fbif")) != "ok" {
			t.Fatal("临时故障要留着旧令牌")
		}
		rec := httptest.NewRecorder()
		h.g.WriteError(rec, err)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("临时故障应 503：%d", rec.Code)
		}
		f.set(func() { f.fail = nil })
		if _, err := h.g.Token(h.req("GET", "/", "u1", "fbif")); err != nil {
			t.Fatalf("恢复后应能用：%v", err)
		}
	}
}

func TestRegrantDuringRefreshIsNotOverwritten(t *testing.T) {
	f := newFakeFeishu(t)
	h := newHarness(t, f, "", nil)
	h.grant(t, "u1", "fbif", "alice", "/")
	h.clock = h.clock.Add(3 * time.Hour)
	entered, hold := make(chan struct{}), make(chan struct{})
	f.set(func() { f.entered, f.hold = entered, hold })
	done := make(chan error, 1)
	go func() {
		_, err := h.g.Token(h.req("GET", "/", "u1", "fbif"))
		done <- err
	}()
	<-entered
	h.grant(t, "u1", "fbif", "alice", "/") // 刷新在路上时本人又授权了一次
	h.g.mu.Lock()
	regranted, cached := h.g.grants["u1"].RefreshSeal, h.g.access["u1"].token
	h.g.mu.Unlock()
	f.set(func() { f.entered, f.hold = nil, nil })
	close(hold)
	if err := <-done; err != nil {
		t.Fatalf("刷新应成功：%v", err)
	}
	h.g.mu.Lock()
	same := h.g.grants["u1"].RefreshSeal == regranted && h.g.access["u1"].token == cached
	h.g.mu.Unlock()
	if !same {
		t.Fatal("刷新的结果不能盖掉刚授权的")
	}

	// 刷新拿到 20073（旧的被别人用掉了），而库里已换成新授权：用新的再试，不误伤
	h.clock = h.clock.Add(3 * time.Hour)
	entered, hold = make(chan struct{}), make(chan struct{})
	f.set(func() {
		f.entered, f.hold = entered, hold
		f.valid = map[string]bool{}
	})
	go func() {
		_, err := h.g.Token(h.req("GET", "/", "u1", "fbif"))
		done <- err
	}()
	<-entered
	h.grant(t, "u1", "fbif", "alice", "/")
	f.set(func() { f.entered, f.hold = nil, nil })
	close(hold)
	if err := <-done; err != nil {
		t.Fatalf("应用新授权重试成功：%v", err)
	}
	h.g.mu.Lock()
	reauth := h.g.grants["u1"].ReauthRequired
	h.g.mu.Unlock()
	if reauth || f.refreshes() != 3 {
		t.Fatalf("不该被标成失效，应刷新 3 次（第一次、被拒的旧令牌、新授权），实际 %d", f.refreshes())
	}
}

func TestKeepAliveRotatesOnlyRecentUsersThatAreDue(t *testing.T) {
	f := newFakeFeishu(t)
	h := newHarness(t, f, "", nil)
	t0 := h.clock
	for _, u := range []string{"due", "fresh", "idle", "revoked"} {
		h.grant(t, u, "fbif", u, "/")
	}
	h.g.mu.Lock()
	idle := h.g.grants["idle"]
	idle.GrantedAt, idle.LastUsedAt, idle.RefreshedAt = t0.Add(-40*24*time.Hour), t0.Add(-40*24*time.Hour), t0.Add(-5*24*time.Hour)
	h.g.grants["idle"] = idle
	h.g.mu.Unlock()
	h.clock = t0.Add(4 * 24 * time.Hour)
	h.g.mu.Lock()
	fresh := h.g.grants["fresh"]
	fresh.RefreshedAt = h.clock.Add(-24 * time.Hour) // 昨天刚续过
	h.g.grants["fresh"] = fresh
	h.g.mu.Unlock()
	revokedSeal := h.g.grants["revoked"].RefreshSeal
	f.set(func() {
		f.fail = nil
		for rt := range f.valid {
			if strings.Contains(rt, "-revoked-") {
				delete(f.valid, rt) // 飞书那边撤销了
			}
		}
	})
	rotated, lost := h.g.keepAliveOnce(context.Background())
	if rotated != 1 || lost != 1 {
		t.Fatalf("应轮换 1 个（due）、失效 1 个（revoked），实际 %d %d", rotated, lost)
	}
	if h.g.grants["due"].RefreshedAt != h.clock || h.g.grants["fresh"].RefreshedAt == h.clock || h.g.grants["idle"].RefreshedAt == h.clock {
		t.Fatal("只该轮换 due")
	}
	if !h.g.grants["revoked"].ReauthRequired || h.g.grants["revoked"].RefreshSeal == revokedSeal {
		t.Fatal("撤销的要标失效")
	}
	if h.g.grants["due"].LastUsedAt != t0 {
		t.Fatal("保活不算使用")
	}
	// 再过 31 天没人用：保活自己轮换过，也不能继续续
	h.clock = t0.Add(31*24*time.Hour + time.Hour)
	if rotated, _ := h.g.keepAliveOnce(context.Background()); rotated != 0 {
		t.Fatalf("31 天没用的不再保活，实际轮换 %d", rotated)
	}
}

func TestWhoDecidesWhoCanUseFeishu(t *testing.T) {
	f := newFakeFeishu(t)
	h := newHarness(t, f, "", nil)
	if _, err := h.g.Token(h.req("GET", "/", "", "")); !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("没登录应 ErrLoginRequired：%v", err)
	}
	_, err := h.g.Token(h.req("GET", "/", "account:rec1", ""))
	if !errors.Is(err, ErrNoFeishu) || h.g.Status(h.req("GET", "/", "account:rec1", "")) != "none" {
		t.Fatalf("账号密码登录的人应 ErrNoFeishu、Status none：%v", err)
	}
	rec := httptest.NewRecorder()
	h.g.WriteError(rec, err)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "feishu_login_required") {
		t.Fatalf("应 403 feishu_login_required：%d %s", rec.Code, rec.Body)
	}
	rec = h.serve(h.req("GET", "/auth/feishu-grant?next=/x", "account:rec1", ""))
	if rec.Header().Get("Location") != "/x?feishu_grant=unavailable" {
		t.Fatalf("授权不了的人去授权应回 unavailable：%s", rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	h.g.WriteError(rec, ErrLoginRequired)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("没登录应 401：%d", rec.Code)
	}
}

func TestSafeNextOnlyAllowsThisSite(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "/",
		"/task/1?x=1#frag":       "/task/1?x=1",
		"//evil.com/x":           "/",
		"https://evil.com/":      "/",
		"/\\evil.com":            "/",
		"task/1":                 "/",
		"/ok\r\nSet-Cookie: x=1": "/",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q，应 %q", in, got, want)
		}
	}
}

func TestNewChecksOptions(t *testing.T) {
	dir := t.TempDir()
	base := Options{Apps: testApps, SealKey: strings.Repeat("k", 44), File: filepath.Join(dir, "g.json"), Who: who}
	bad := []func(*Options){
		func(o *Options) { o.Apps = nil },
		func(o *Options) { o.SealKey = "short" },
		func(o *Options) { o.File = "" },
		func(o *Options) { o.Who = nil },
		func(o *Options) {
			o.Apps = map[string]App{"fbif": {AppID: "a", AppSecret: "b", RedirectURL: "/relative"}}
		},
		func(o *Options) {
			o.Apps = map[string]App{"a": {AppID: "a", AppSecret: "b", RedirectURL: "https://x/cb"}, "b": {AppID: "c", AppSecret: "d", RedirectURL: "https://x/cb"}}
		},
	}
	for i, mutate := range bad {
		o := base
		mutate(&o)
		if _, err := New(context.Background(), o); err == nil {
			t.Errorf("第 %d 种错配应启动失败", i)
		}
	}
}
