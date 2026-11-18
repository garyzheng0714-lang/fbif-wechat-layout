package mcpauth

import (
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// handleAuthorize 授权请求不对时怎么回：能确认回调地址是这个客户端的，才按 OAuth 带 error 跳回去（RFC 6749 4.1.2.1）；
// 客户端或回调地址本身不对，只给人看一页说明，绝不跳到没核实的地址。
func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	client, registered, ok := s.lookupClient(q.Get("client_id"))
	if !ok {
		s.page(w, http.StatusBadRequest, pageData{Title: "这个应用没有登记", Detail: "请在智能体里重新连接排版工具。"})
		return
	}
	redirectURI := q.Get("redirect_uri")
	if !redirectAllowed(client.RedirectURIs, redirectURI) {
		s.page(w, http.StatusBadRequest, pageData{Title: "回调地址不对", Detail: "请在智能体里重新连接排版工具。"})
		return
	}
	state := q.Get("state")
	fail := func(code, desc string) {
		s.redirectError(w, r, redirectURI, state, code, desc)
	}
	switch {
	case q.Get("response_type") != "code":
		fail("unsupported_response_type", "只支持 response_type=code")
		return
	case q.Get("code_challenge_method") != "S256" || !validChallenge(q.Get("code_challenge")):
		fail("invalid_request", "要带 PKCE：code_challenge_method=S256")
		return
	case !s.sameResource(q.Get("resource")):
		fail("invalid_target", "resource 不是这个 MCP 服务")
		return
	}
	person, ok := s.opt.Person(r)
	if !ok {
		s.page(w, http.StatusUnauthorized, pageData{Title: "请先登录", Detail: "登录排版工具后再回到智能体重新连接。"})
		return
	}
	if s.opt.Allowed != nil {
		if err := s.opt.Allowed(r.Context(), person.ID); err != nil {
			s.page(w, http.StatusForbidden, pageData{Title: "这个账号不能给智能体授权", Detail: err.Error()})
			return
		}
	}
	id := randomToken()
	now := s.opt.Now()
	s.mu.Lock()
	s.dropExpiredLocked()
	if len(s.consents) >= maxPendingConsents {
		s.mu.Unlock()
		s.page(w, http.StatusServiceUnavailable, pageData{Title: "授权的人太多", Detail: "过一会儿再试。"})
		return
	}
	s.consents[id] = &consent{
		client:      client,
		registered:  registered,
		redirectURI: redirectURI,
		state:       state,
		challenge:   q.Get("code_challenge"),
		personID:    person.ID,
		personName:  person.Name,
		expires:     now.Add(consentTTL),
	}
	s.mu.Unlock()
	s.page(w, http.StatusOK, pageData{
		Consent: true,
		Title:   consentTitle(client, registered, person.Name, s.opt.ResourceName),
		Request: id,
	})
}

func consentTitle(client Client, registered bool, person, resource string) string {
	who := client.Name + " "
	if registered {
		who = "本机上的「" + client.Name + "」"
	}
	return who + "要以" + person + "的身份使用" + resource
}

// handleConsent 确认页上点的「允许」「取消」。只认同一个人、从本站页面点的。
func (s *Server) handleConsent(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		s.page(w, http.StatusForbidden, pageData{Title: "请从授权页点", Detail: "请在智能体里重新连接排版工具。"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		s.page(w, http.StatusBadRequest, pageData{Title: "请求不对", Detail: "请在智能体里重新连接排版工具。"})
		return
	}
	id := r.PostForm.Get("request")
	s.mu.Lock()
	c := s.consents[id]
	delete(s.consents, id)
	s.mu.Unlock()
	if c == nil || s.opt.Now().After(c.expires) {
		s.page(w, http.StatusBadRequest, pageData{Title: "这次授权已过期", Detail: "请在智能体里重新连接排版工具。"})
		return
	}
	person, ok := s.opt.Person(r)
	if !ok || person.ID != c.personID {
		s.page(w, http.StatusForbidden, pageData{Title: "登录的人变了", Detail: "请在智能体里重新连接排版工具。"})
		return
	}
	if r.PostForm.Get("decision") != "allow" {
		s.redirectError(w, r, c.redirectURI, c.state, "access_denied", "用户取消了授权")
		return
	}
	raw := randomToken()
	s.mu.Lock()
	s.dropExpiredLocked()
	if len(s.codes) >= maxCodes {
		s.mu.Unlock()
		s.redirectError(w, r, c.redirectURI, c.state, "temporarily_unavailable", "授权的人太多，过一会儿再试")
		return
	}
	s.codes[hashToken(raw)] = &code{
		clientID:    c.client.ID,
		redirectURI: c.redirectURI,
		challenge:   c.challenge,
		personID:    c.personID,
		personName:  c.personName,
		clientName:  c.client.Name,
		expires:     s.opt.Now().Add(codeTTL),
	}
	s.mu.Unlock()
	s.opt.Logf("mcpauth: %s 允许 %s（%s）", c.personID, c.client.Name, c.client.ID)
	s.redirect(w, r, c.redirectURI, url.Values{"code": {raw}, "state": optional(c.state)})
}

func optional(v string) []string {
	if v == "" {
		return nil
	}
	return []string{v}
}

func (s *Server) redirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, desc string) {
	s.redirect(w, r, redirectURI, url.Values{"error": {code}, "error_description": {desc}, "state": optional(state)})
}

// redirect 跳回客户端，带上 iss（RFC 9207）。
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, redirectURI string, params url.Values) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		s.page(w, http.StatusBadRequest, pageData{Title: "回调地址不对"})
		return
	}
	q := target.Query()
	for k, vs := range params {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
	q.Set("iss", s.opt.Issuer)
	target.RawQuery = q.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// sameOrigin 表单只认本站页面提交的：带 Origin 的要等于 Issuer；老浏览器不带 Origin 就看 Referer。
func (s *Server) sameOrigin(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		return origin == s.opt.Issuer
	}
	ref := r.Header.Get("Referer")
	return ref == s.opt.Issuer || strings.HasPrefix(ref, s.opt.Issuer+"/")
}

func (s *Server) dropExpiredLocked() {
	now := s.opt.Now()
	for id, c := range s.consents {
		if now.After(c.expires) {
			delete(s.consents, id)
		}
	}
	for h, c := range s.codes {
		// 用过的码留到过期：过期前再拿来换，能认出是重复使用。
		if now.After(c.expires) {
			delete(s.codes, h)
		}
	}
}

// ---------- 客户端 ----------

// lookupClient 先看预先登记的，再看动态注册的。registered=true 是动态注册的：名字是它自己报的。
func (s *Server) lookupClient(id string) (Client, bool, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Client{}, false, false
	}
	if c, ok := s.clients[id]; ok {
		return c, false, true
	}
	if c, ok := s.store.client(id); ok {
		return c, true, true
	}
	return Client{}, false, false
}

// redirectAllowed 回调地址要是登记过的那几个之一。回到本机的（127.0.0.1、localhost、[::1]）不看端口：
// 本机应用每次挑一个空闲端口（RFC 8252 7.3），登记时也不写端口。
func redirectAllowed(registered []string, raw string) bool {
	if raw == "" {
		return false
	}
	for _, r := range registered {
		if r == raw {
			return true
		}
	}
	want, err := url.Parse(raw)
	if err != nil || !isLoopback(want) {
		return false
	}
	for _, r := range registered {
		have, err := url.Parse(r)
		if err != nil || !isLoopback(have) {
			continue
		}
		if have.Hostname() == want.Hostname() && have.Path == want.Path && have.RawQuery == want.RawQuery {
			return true
		}
	}
	return false
}

func isLoopback(u *url.URL) bool {
	if u.Scheme != "http" || u.User != nil || u.Fragment != "" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// nativeRedirect 动态注册只收回到本机的地址：http 回环，或本机应用自己的协议（cursor://…）。
func nativeRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Fragment != "" {
		return false
	}
	if isLoopback(u) {
		return true
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "javascript", "data", "file", "vbscript", "about", "blob", "ftp", "ws", "wss":
		return false
	}
	for _, c := range u.Scheme {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

// ---------- 页面 ----------

type pageData struct {
	Consent bool
	Title   string
	Detail  string
	Request string
}

func (s *Server) page(w http.ResponseWriter, status int, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	_ = pageTemplate.Execute(w, data)
}

// 样子照项目 DESIGN.md：白卡、#E5E6EB 边、10px 圆角，主按钮品牌蓝 36px 高。
var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<style>
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#F7F8FA;
font-family:"PingFang SC",-apple-system,BlinkMacSystemFont,"Segoe UI","Microsoft YaHei","Noto Sans SC",sans-serif;color:#1F2329}
main{width:min(400px,calc(100vw - 32px));background:#FFFFFF;border:1px solid #E5E6EB;border-radius:10px;padding:24px}
h1{margin:0;font-size:16px;font-weight:600;line-height:1.5}
p{margin:8px 0 0;font-size:13px;line-height:1.6;color:#646A73}
form{display:flex;gap:8px;margin-top:24px}
button{flex:1;height:36px;border-radius:6px;font:600 14px/1 inherit;cursor:pointer}
.primary{background:#1456F0;border:0;color:#FFFFFF}
.primary:hover{background:#0F4DD1}
.primary:active{background:#0B3FBA}
.secondary{background:#FFFFFF;border:1px solid #D9DCE0;color:#1F2329}
.secondary:hover{background:#F2F3F5}
button:focus-visible{outline:2px solid #1456F0;outline-offset:2px}
</style>
</head>
<body>
<main>
<h1>{{.Title}}</h1>
{{if .Consent}}<form method="post" action="/oauth/authorize">
<input type="hidden" name="request" value="{{.Request}}">
<button class="secondary" type="submit" name="decision" value="deny">取消</button>
<button class="primary" type="submit" name="decision" value="allow" autofocus>允许</button>
</form>{{else}}{{if .Detail}}<p>{{.Detail}}</p>{{end}}{{end}}
</main>
</body>
</html>
`))
