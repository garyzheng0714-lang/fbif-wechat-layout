package mcpauth

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// handleToken 授权码换令牌、刷新令牌。只认公开客户端（不带密钥），client_id 在表单里或 Basic 的用户名里。
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request", "请求体要是表单")
		return
	}
	form := r.PostForm
	clientID := form.Get("client_id")
	if user, _, ok := r.BasicAuth(); ok && clientID == "" {
		clientID = user
	}
	if _, _, ok := s.lookupClient(clientID); !ok {
		tokenError(w, http.StatusUnauthorized, "invalid_client", "这个应用没有登记")
		return
	}
	if !s.sameResource(form.Get("resource")) {
		tokenError(w, http.StatusBadRequest, "invalid_target", "resource 不是这个 MCP 服务")
		return
	}
	switch form.Get("grant_type") {
	case "authorization_code":
		s.exchangeCode(w, r, clientID)
	case "refresh_token":
		s.refresh(w, r, clientID)
	default:
		tokenError(w, http.StatusBadRequest, "unsupported_grant_type", "只支持 authorization_code、refresh_token")
	}
}

func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request, clientID string) {
	form := r.PostForm
	h := hashToken(form.Get("code"))
	now := s.opt.Now()
	s.mu.Lock()
	c := s.codes[h]
	if c == nil || now.After(c.expires) || c.clientID != clientID {
		s.mu.Unlock()
		tokenError(w, http.StatusBadRequest, "invalid_grant", "授权码不对或已过期")
		return
	}
	if c.used {
		// 同一个码用了两次：可能被人截走了，把第一次发出的授权也吊销（RFC 6749 4.1.2）。
		grantID := c.grantID
		s.mu.Unlock()
		if grantID != "" {
			_ = s.store.revoke(grantID)
		}
		s.opt.Logf("mcpauth: 授权码重复使用，已吊销 %s", c.clientName)
		tokenError(w, http.StatusBadRequest, "invalid_grant", "授权码已用过")
		return
	}
	if form.Get("redirect_uri") != c.redirectURI {
		s.mu.Unlock()
		tokenError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri 和授权时的不一样")
		return
	}
	verifier := form.Get("code_verifier")
	if !validVerifier(verifier) || pkceS256(verifier) != c.challenge {
		s.mu.Unlock()
		tokenError(w, http.StatusBadRequest, "invalid_grant", "code_verifier 对不上")
		return
	}
	c.used = true
	s.mu.Unlock()

	if s.opt.Allowed != nil {
		if err := s.opt.Allowed(r.Context(), c.personID); err != nil {
			tokenError(w, http.StatusBadRequest, "invalid_grant", err.Error())
			return
		}
	}
	access, refresh := randomToken(), randomToken()
	g := &grant{
		ID:         randomToken()[:22],
		PersonID:   c.personID,
		PersonName: c.personName,
		ClientID:   c.clientID,
		ClientName: c.clientName,
		CreatedAt:  now,
		LastUsedAt: now,
	}
	if err := s.store.create(g, hashToken(access), hashToken(refresh), now); err != nil {
		s.opt.Logf("mcpauth: 存授权失败：%v", err)
		tokenError(w, http.StatusInternalServerError, "server_error", "授权没存下来，再试一次")
		return
	}
	s.mu.Lock()
	c.grantID = g.ID
	s.mu.Unlock()
	s.opt.Logf("mcpauth: %s 的 %s 拿到令牌（授权 %s）", c.personID, c.clientName, g.ID)
	s.writeTokens(w, access, refresh)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request, clientID string) {
	now := s.opt.Now()
	access, refresh := randomToken(), randomToken()
	g, err := s.store.rotate(hashToken(r.PostForm.Get("refresh_token")), clientID, hashToken(access), hashToken(refresh), now)
	if err != nil {
		if err == errRefreshReused {
			s.opt.Logf("mcpauth: 刷新令牌被重复使用，已吊销授权")
		}
		tokenError(w, http.StatusBadRequest, "invalid_grant", "刷新令牌不对或已过期，请重新连接")
		return
	}
	if s.opt.Allowed != nil {
		if err := s.opt.Allowed(r.Context(), g.PersonID); err != nil {
			_ = s.store.revoke(g.ID)
			tokenError(w, http.StatusBadRequest, "invalid_grant", err.Error())
			return
		}
	}
	s.writeTokens(w, access, refresh)
}

func (s *Server) writeTokens(w http.ResponseWriter, access, refresh string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(accessTTL / time.Second),
		"refresh_token": refresh,
		"scope":         s.opt.Scope,
	})
}

// handleRevoke RFC 7009：访问令牌或刷新令牌都行，吊销整份授权。认不出的也回 200。
func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request", "请求体要是表单")
		return
	}
	if id, ok := s.store.grantByAnyToken(hashToken(r.PostForm.Get("token")), s.opt.Now()); ok {
		_ = s.store.revoke(id)
		s.opt.Logf("mcpauth: 吊销授权 %s", id)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

// ---------- 动态注册 ----------

type registerRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

// handleRegister RFC 7591。只收回到本机的回调地址、不带密钥的客户端：码只会落到点「允许」那台电脑上，
// 别人注册一个假名字的应用骗人点允许也拿不到码。每个 IP 每小时最多 20 个。
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(clientIP(r), s.opt.Now()) {
		w.Header().Set("Retry-After", "3600")
		registerError(w, http.StatusTooManyRequests, "invalid_client_metadata", "注册太频繁")
		return
	}
	var req registerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		registerError(w, http.StatusBadRequest, "invalid_client_metadata", "请求体要是 JSON")
		return
	}
	if req.TokenEndpointAuthMethod != "" && req.TokenEndpointAuthMethod != "none" {
		registerError(w, http.StatusBadRequest, "invalid_client_metadata", "只支持不带密钥的客户端（token_endpoint_auth_method=none）")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 {
		registerError(w, http.StatusBadRequest, "invalid_redirect_uri", "要写 1 到 10 个回调地址")
		return
	}
	for _, uri := range req.RedirectURIs {
		if len(uri) > 512 || !nativeRedirect(uri) {
			registerError(w, http.StatusBadRequest, "invalid_redirect_uri", "回调地址只能回到本机（http://127.0.0.1、http://localhost 或应用自己的协议）："+uri)
			return
		}
	}
	name := strings.TrimSpace(req.ClientName)
	if r := []rune(name); len(r) > 40 {
		name = string(r[:40])
	}
	if name == "" {
		name = "未命名应用"
	}
	now := s.opt.Now()
	c := Client{ID: "dcr_" + randomToken()[:22], Name: name, RedirectURIs: req.RedirectURIs}
	if err := s.store.register(c, now); err != nil {
		s.opt.Logf("mcpauth: 存注册的客户端失败：%v", err)
		registerError(w, http.StatusInternalServerError, "server_error", "没存下来，再试一次")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  c.ID,
		"client_id_issued_at":        now.Unix(),
		"client_name":                c.Name,
		"redirect_uris":              c.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}

type registerLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newRegisterLimiter() *registerLimiter { return &registerLimiter{hits: map[string][]time.Time{}} }

func (l *registerLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-time.Hour)
	if len(l.hits) > 10000 {
		l.hits = map[string][]time.Time{}
	}
	kept := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= 20 {
		l.hits[ip] = kept
		return false
	}
	l.hits[ip] = append(kept, now)
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func tokenError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="oauth"`)
	}
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func registerError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}
