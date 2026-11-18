// Package mcpauth 让智能体（Codex、Claude Code……）照 MCP 授权规范登录后，用登录那个人的身份调本项目的 MCP。
//
// 本项目同时是授权服务器和受保护的资源（MCP 规范 2026-07-28「Authorization」）：
//
//   - GET /.well-known/oauth-protected-resource[/mcp]  受保护资源元数据（RFC 9728），指回本项目自己
//   - GET /.well-known/oauth-authorization-server     授权服务器元数据（RFC 8414）
//   - GET  /oauth/authorize  浏览器打开：没登录先走项目的登录（FBIF 统一登录页），登了出确认页
//   - POST /oauth/authorize  确认页上点「允许」「取消」
//   - POST /oauth/token      授权码换令牌（只收 PKCE S256）、刷新令牌（每次换新，旧的留 1 分钟）
//   - POST /oauth/register   动态注册（RFC 7591），只收回到本机的地址
//   - POST /oauth/revoke     吊销（RFC 7009）
//
// 客户端怎么认：一律走动态注册（Codex、Claude Code、Cursor……都支持），只收回到本机的回调地址
// （127.0.0.1、localhost 任意端口，或本机应用自己的协议），授权码只会落到点「允许」那台电脑上，
// 别人注册个假名字骗人点允许也拿不到码。不声明支持客户端元数据文档（CIMD）：声明了，支持 CIMD 的客户端就不再注册，
// 而服务器连不上 claude.ai、chatgpt.com 这些放文档的地方，只能认登记在代码里的几个，别的智能体全被挡在外面。
// Clients 是预先登记的客户端（配了固定 client_id 的），平常不用。
//
// 令牌都是随机串，库里只存哈希。访问令牌 1 小时，刷新令牌 30 天不用就失效、最长 90 天要重新点一次允许。
// 每次调 MCP、每次刷新都问一遍 Allowed：人停用、离职了，下一次调用就不让进。
package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const (
	accessTTL      = time.Hour
	refreshIdleTTL = 30 * 24 * time.Hour
	grantMaxAge    = 90 * 24 * time.Hour
	codeTTL        = 5 * time.Minute
	consentTTL     = 10 * time.Minute
	// refreshGrace 刷新令牌换新后，旧的还认这么久：同一台电脑上两个进程同时拿旧令牌刷新时，后到的那个不被当成盗用。
	refreshGrace = time.Minute
	// lastUsedEvery 「最近用过」最多隔这么久写一次盘。
	lastUsedEvery = time.Hour

	maxPendingConsents = 2000
	maxCodes           = 2000
)

// Person 是在浏览器里登录的那个人：ID 是项目用户库里的编号，Name 给确认页显示。
type Person struct {
	ID   string
	Name string
}

// Client 是预先登记的客户端：client_id、名字、回调地址。
type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
}

type Options struct {
	// Issuer 本项目对外的地址（https://…，不带路径）：授权服务器的 issuer，也是各端点的前缀。
	Issuer string
	// ResourcePath MCP 的路径（/mcp）；资源标识是 Issuer+ResourcePath。
	ResourcePath string
	// ResourceName 确认页和元数据里的项目名。
	ResourceName string
	// Scope 唯一的权限名（比如 layout）：令牌只有这一个。
	Scope string
	// StateFile 授权记录和动态注册的客户端存在这个文件里（只存令牌哈希）。
	StateFile string
	// Person 认出浏览器请求上登录的人；没登录返回 false。
	Person func(r *http.Request) (Person, bool)
	// RequireLogin 包住授权页：没登录先去登录、登完回到这一页。
	RequireLogin func(http.Handler) http.Handler
	// Allowed 这个人现在能不能让智能体用（停用、离职、不是飞书登录的都不行）。nil 是都能。
	Allowed func(ctx context.Context, personID string) error
	// Clients 预先登记的客户端（client_id 固定写在客户端配置里的）。平常不用，都走动态注册。
	Clients []Client
	Now     func() time.Time
	Logf    func(format string, args ...any)
}

type Server struct {
	opt      Options
	resource string
	clients  map[string]Client
	store    *store

	mu       sync.Mutex
	consents map[string]*consent // 确认页编号 → 这次授权请求
	codes    map[string]*code    // 授权码哈希 → 授权码
	limiter  *registerLimiter
}

type consent struct {
	client      Client
	registered  bool // 动态注册的（名字是它自己报的）
	redirectURI string
	state       string
	challenge   string
	personID    string
	personName  string
	expires     time.Time
}

type code struct {
	clientID    string
	redirectURI string
	challenge   string
	personID    string
	personName  string
	clientName  string
	expires     time.Time
	used        bool
	grantID     string // 换过令牌的：记下发出的授权，重复使用时吊销它（RFC 6749 4.1.2）
}

func New(opt Options) (*Server, error) {
	opt.Issuer = strings.TrimRight(strings.TrimSpace(opt.Issuer), "/")
	if opt.Issuer == "" || !strings.HasPrefix(opt.Issuer, "http") {
		return nil, errors.New("mcpauth: Issuer 要写 https://…")
	}
	if !strings.HasPrefix(opt.ResourcePath, "/") {
		return nil, errors.New("mcpauth: ResourcePath 要以 / 开头")
	}
	if opt.Scope == "" || opt.Person == nil || opt.StateFile == "" {
		return nil, errors.New("mcpauth: Scope、Person、StateFile 必填")
	}
	if opt.RequireLogin == nil {
		opt.RequireLogin = func(h http.Handler) http.Handler { return h }
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Logf == nil {
		opt.Logf = log.Printf
	}
	st, err := openStore(opt.StateFile, opt.Now)
	if err != nil {
		return nil, err
	}
	s := &Server{
		opt:      opt,
		resource: opt.Issuer + opt.ResourcePath,
		clients:  map[string]Client{},
		store:    st,
		consents: map[string]*consent{},
		codes:    map[string]*code{},
		limiter:  newRegisterLimiter(),
	}
	for _, c := range opt.Clients {
		s.clients[c.ID] = c
	}
	return s, nil
}

// Resource 是 MCP 的资源标识（Issuer+ResourcePath），客户端配置里填的就是它。
func (s *Server) Resource() string { return s.resource }

func (s *Server) metadataURL() string {
	return s.opt.Issuer + "/.well-known/oauth-protected-resource" + s.opt.ResourcePath
}

// Register 挂元数据和 /oauth/* 端点。
func (s *Server) Register(mux *http.ServeMux) {
	prm := http.HandlerFunc(s.handleResourceMetadata)
	mux.Handle("GET /.well-known/oauth-protected-resource", prm)
	mux.Handle("GET /.well-known/oauth-protected-resource"+s.opt.ResourcePath, prm)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleServerMetadata)
	mux.Handle("GET /oauth/authorize", s.opt.RequireLogin(http.HandlerFunc(s.handleAuthorize)))
	mux.HandleFunc("POST /oauth/authorize", s.handleConsent)
	mux.HandleFunc("POST /oauth/token", s.handleToken)
	mux.HandleFunc("POST /oauth/register", s.handleRegister)
	mux.HandleFunc("POST /oauth/revoke", s.handleRevoke)
}

// Protect 包住 MCP：要带本项目发的访问令牌，没带、过期、人不让用了都回 401 并指向元数据（客户端据此去登录）。
func (s *Server) Protect(next http.Handler) http.Handler {
	return sdkauth.RequireBearerToken(s.verify, &sdkauth.RequireBearerTokenOptions{
		ResourceMetadataURL: s.metadataURL(),
		Scopes:              []string{s.opt.Scope},
	})(next)
}

// PersonFrom 取 Protect 认出的人。
func PersonFrom(ctx context.Context) (Person, bool) {
	info := sdkauth.TokenInfoFromContext(ctx)
	if info == nil || info.UserID == "" {
		return Person{}, false
	}
	name, _ := info.Extra["name"].(string)
	return Person{ID: info.UserID, Name: name}, true
}

func (s *Server) verify(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
	g, expires, ok := s.store.byAccess(hashToken(token), s.opt.Now())
	if !ok {
		return nil, fmt.Errorf("%w: 令牌不对或已过期", sdkauth.ErrInvalidToken)
	}
	if s.opt.Allowed != nil {
		if err := s.opt.Allowed(ctx, g.PersonID); err != nil {
			return nil, fmt.Errorf("%w: %v", sdkauth.ErrInvalidToken, err)
		}
	}
	s.store.touch(g.ID, s.opt.Now(), lastUsedEvery)
	return &sdkauth.TokenInfo{
		Scopes:     []string{s.opt.Scope},
		Expiration: expires,
		UserID:     g.PersonID,
		Extra:      map[string]any{"name": g.PersonName, "client": g.ClientName},
	}, nil
}

func (s *Server) handleResourceMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, &oauthex.ProtectedResourceMetadata{
		Resource:               s.resource,
		AuthorizationServers:   []string{s.opt.Issuer},
		ScopesSupported:        []string{s.opt.Scope},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           s.opt.ResourceName,
	})
}

func (s *Server) handleServerMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         s.opt.Issuer,
		"authorization_endpoint":                         s.opt.Issuer + "/oauth/authorize",
		"token_endpoint":                                 s.opt.Issuer + "/oauth/token",
		"registration_endpoint":                          s.opt.Issuer + "/oauth/register",
		"revocation_endpoint":                            s.opt.Issuer + "/oauth/revoke",
		"scopes_supported":                               []string{s.opt.Scope},
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"revocation_endpoint_auth_methods_supported":     []string{"none"},
		"authorization_response_iss_parameter_supported": true,
	})
}

// ---------- 小工具 ----------

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte("mcpauth\x00" + token))
	return hex.EncodeToString(sum[:])
}

// pkceS256 RFC 7636：challenge = BASE64URL(SHA256(verifier))。
func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func validVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '.', c == '_', c == '~':
		default:
			return false
		}
	}
	return true
}

func validChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil
}

// sameResource 客户端带的 resource（RFC 8707）是不是本项目的 MCP：末尾斜杠不计。没带算是。
func (s *Server) sameResource(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	raw = strings.TrimRight(raw, "/")
	return raw == s.resource || raw == s.opt.Issuer
}

func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	host = strings.Trim(host, "[]")
	if host == "127.0.0.1" || host == "::1" {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			parts := strings.Split(fwd, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return host
}
