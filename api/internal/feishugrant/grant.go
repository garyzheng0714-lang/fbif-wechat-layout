// Package feishugrant 管「以登录的人的身份调飞书开放平台」要的飞书用户授权（user_access_token），v1（2026-11-09）。
//
// 登录交给统一登录模板（internal/auth/sso.go），它的令牌是集成平台的，调不了飞书开放平台。要在本人云空间建文档、
// 读本人云盘、列本人的群这类项目，另外请这个人授权一次本项目的飞书应用，之后由服务端替他续：
//   - refresh_token 约 7 天、只能用一次；30 天内用过本项目的人，后台每 3 天左右替他轮换一次，不用他再点授权；
//   - 飞书明确拒绝（过期、撤销、已被用过、不是这个应用的）才要他重新授权；飞书临时故障不算，留着旧令牌下次再试；
//   - refresh_token 加密后存本机文件（0600），access_token 只在内存；都不下发前端、不写日志。
//
// 照抄、不改。项目只写 Options（哪几个飞书应用、要哪些权限、谁在登录），接口用 Token、WriteError、Status。
// 规矩、接法和前端约定见同目录 README.md；算法照 feishu-login-guide 的 references/session-and-token-renewal.md。
package feishugrant

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultAuthorizeURL = "https://accounts.feishu.cn/open-apis/authen/v1/authorize"
	defaultTokenURL     = "https://open.feishu.cn/open-apis/authen/v2/oauth/token"
	defaultUserInfoURL  = "https://open.feishu.cn/open-apis/authen/v1/user_info"

	defaultStartPath = "/auth/feishu-grant"
	stateCookie      = "fbif_feishu_grant"
	stateTTL         = 10 * time.Minute
	rotateAfter      = 3 * 24 * time.Hour  // 最晚多久轮换一次 refresh_token（飞书给的寿命一半更短时用一半）
	activeWithin     = 30 * 24 * time.Hour // 这么久没用本项目就不再保活，令牌自然过期
	touchEvery       = time.Hour           // 「最近用过」最多每小时写一次盘
	keepAliveEvery   = 6 * time.Hour       // 后台多久看一次谁该轮换
	httpTimeout      = 10 * time.Second
	sealLabel        = "feishu-refresh-token"
)

var (
	// ErrLoginRequired：没登录。WriteError 回 401，和登录模板一样。
	ErrLoginRequired = errors.New("feishugrant: 没登录")
	// ErrNoFeishu：登录了，但不是用飞书登录的（账号密码登录的人、本项目没配飞书应用的企业），授权不了。
	ErrNoFeishu = errors.New("feishugrant: 不是用飞书登录的，不能授权飞书")
	// ErrGrantRequired：还没授权，或飞书明确拒绝了续期。前端带他去 StartPath 授权一次。
	ErrGrantRequired = errors.New("feishugrant: 要先授权飞书")
)

// App 是一个飞书开放平台应用（企业自建应用）。不同企业各用自己的应用。
type App struct {
	AppID     string
	AppSecret string
	// RedirectURL 是飞书开放平台「安全设置 → 重定向 URL」里登记过的完整地址，字符级一致；它的路径就是授权回调的路由。
	// 不能和登录模板的路由重名（/auth/feishu/fbif/callback 归登录模板）。
	RedirectURL string
}

// FeishuUser 是授权的飞书账号（/open-apis/authen/v1/user_info）。
type FeishuUser struct {
	OpenID    string `json:"open_id"`
	UnionID   string `json:"union_id"`
	UserID    string `json:"user_id"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
	Email     string `json:"email"`
	TenantKey string `json:"tenant_key"`
}

type Options struct {
	// Apps：键是应用名（比如 fbif、fude），Who 返回其中一个。
	Apps map[string]App
	// Scopes：业务要的用户身份权限，空格分隔，要在飞书开放平台开通并发布。offline_access 自动加上，不用写。
	Scopes string
	// SealKey：加密 refresh_token 的钥匙，至少 32 个字符。用登录模板的 SSO_TOKEN_KEY 就行（中台发的，不用另建）；
	// 换了它，大家重新授权一次。
	SealKey string
	// File：授权存在哪个 JSON 文件（0600），和登录模板的会话文件放一起。只支持单实例。
	File string
	// StartPath：去授权的地址，默认 /auth/feishu-grant（带 ?next=回到哪）。
	StartPath string
	// Who：这个请求是谁（登录模板 CurrentUser(r).ID）、用哪个飞书应用授权（Apps 的键）。
	// 没登录 user 为空；登录了但不能授权飞书（账号密码登录的人、没配应用的企业）app 为空。
	Who func(r *http.Request) (user, app string)
	// Check（可选）：授权的飞书账号要和登录的人是同一个，对不上返回 error（比如 FBIF 的人比 union_id）。
	Check  func(r *http.Request, app string, u FeishuUser) error
	Logger *slog.Logger
}

// Grants 是飞书用户授权服务。New 建，Register 挂路由。
type Grants struct {
	o    Options
	aead cipher.AEAD
	mac  []byte
	log  *slog.Logger
	http *http.Client
	now  func() time.Time

	authorizeURL, tokenURL, userInfoURL string // 测试换成假的飞书

	mu     sync.Mutex // 守 grants、access 和文件
	grants map[string]grant
	access map[string]cachedToken

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex // 每人一把刷新锁：refresh_token 只能用一次，同一时刻只许一个人在换
}

type grant struct {
	App              string    `json:"app"`
	OpenID           string    `json:"open_id"`
	UnionID          string    `json:"union_id"`
	Name             string    `json:"name"`
	RefreshSeal      string    `json:"refresh_seal,omitempty"`
	GrantedAt        time.Time `json:"granted_at"`
	RefreshedAt      time.Time `json:"refreshed_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at,omitempty"`
	ReauthRequired   bool      `json:"reauth_required,omitempty"`
	LastUsedAt       time.Time `json:"last_used_at"`
}

type cachedToken struct {
	token   string
	expires time.Time
}

type fileData struct {
	Version int              `json:"version"`
	Grants  map[string]grant `json:"grants"`
}

// New 检查配置、读授权文件，并起后台保活（跟着 ctx 停）。
func New(ctx context.Context, o Options) (*Grants, error) {
	if len(o.Apps) == 0 {
		return nil, errors.New("feishugrant: Apps 至少要一个")
	}
	paths := map[string]string{}
	for name, a := range o.Apps {
		if a.AppID == "" || a.AppSecret == "" {
			return nil, fmt.Errorf("feishugrant: 应用 %s 缺 AppID 或 AppSecret", name)
		}
		u, err := url.Parse(a.RedirectURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path == "" || u.RawQuery != "" {
			return nil, fmt.Errorf("feishugrant: 应用 %s 的 RedirectURL 要是完整地址（https://域名/路径），不带查询串", name)
		}
		if other, dup := paths[u.Path]; dup {
			return nil, fmt.Errorf("feishugrant: 应用 %s 和 %s 的回调路径一样", name, other)
		}
		paths[u.Path] = name
	}
	if len(o.SealKey) < 32 {
		return nil, errors.New("feishugrant: SealKey 至少 32 个字符（用登录模板的 SSO_TOKEN_KEY）")
	}
	if o.File == "" {
		return nil, errors.New("feishugrant: 缺 File（授权存哪）")
	}
	if o.Who == nil {
		return nil, errors.New("feishugrant: 缺 Who")
	}
	if o.StartPath == "" {
		o.StartPath = defaultStartPath
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	key := sha256.Sum256([]byte("fbif-feishu-grant-seal\x00" + o.SealKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	mac := sha256.Sum256([]byte("fbif-feishu-grant-state\x00" + o.SealKey))
	g := &Grants{
		o:            o,
		aead:         aead,
		mac:          mac[:],
		log:          o.Logger,
		http:         &http.Client{Timeout: httpTimeout},
		now:          time.Now,
		authorizeURL: defaultAuthorizeURL,
		tokenURL:     defaultTokenURL,
		userInfoURL:  defaultUserInfoURL,
		grants:       map[string]grant{},
		access:       map[string]cachedToken{},
		locks:        map[string]*sync.Mutex{},
	}
	if err := g.load(); err != nil {
		return nil, err
	}
	go g.keepAlive(ctx)
	return g, nil
}

// Register 挂授权的路由：GET StartPath（去飞书授权，带 ?next=）和每个应用的授权回调（RedirectURL 的路径）。
// require 传登录模板的 sso.RequireUser：只有登录了的人能授权，授权记在他名下。
func (g *Grants) Register(mux *http.ServeMux, require func(http.Handler) http.Handler) {
	mux.Handle("GET "+g.o.StartPath, require(http.HandlerFunc(g.handleStart)))
	for name, a := range g.o.Apps {
		u, _ := url.Parse(a.RedirectURL)
		mux.Handle("GET "+u.Path, require(g.callback(name)))
	}
}

// Token 给这个请求的人拿飞书 user_access_token：缓存里有就用，快过期就用 refresh_token 换（并轮换）。
// 出错交给 WriteError；ErrGrantRequired 时前端带他去授权。
func (g *Grants) Token(r *http.Request) (string, error) {
	user, app := g.o.Who(r)
	if user == "" {
		return "", ErrLoginRequired
	}
	if app == "" {
		return "", ErrNoFeishu
	}
	return g.token(r.Context(), user)
}

// Status 给项目的「我是谁」接口用（顺便记「最近用过」）："ok" 已授权；"required" 要授权；
// "none" 授权不了（不是用飞书登录的）；没登录是 ""。
func (g *Grants) Status(r *http.Request) string {
	user, app := g.o.Who(r)
	if user == "" {
		return ""
	}
	if app == "" {
		return "none"
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	gr, ok := g.touchLocked(user)
	if !ok || gr.ReauthRequired || gr.RefreshSeal == "" {
		return "required"
	}
	return "ok"
}

// WriteError 把 Token 的错误回给前端：401 login_required；403 feishu_login_required（要用飞书账号登录）；
// 403 feishu_grant_required（带 grant_url，前端带他去授权）；别的是飞书暂时连不上，503 feishu_unavailable。
func (g *Grants) WriteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrLoginRequired):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "login_required", "login_url": "/auth/login"})
	case errors.Is(err, ErrNoFeishu):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "feishu_login_required", "error": "这个功能要用飞书账号登录"})
	case errors.Is(err, ErrGrantRequired):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "feishu_grant_required", "error": "要先授权飞书，授权一次之后会自动续期", "grant_url": g.o.StartPath})
	default:
		g.log.Warn("feishugrant: 飞书暂时不可用", "err", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "feishu_unavailable", "error": "飞书暂时连不上，请稍后重试"})
	}
}

// ---------- 授权 ----------

type stateData struct {
	State string `json:"s"`
	App   string `json:"a"`
	Next  string `json:"n"`
	User  string `json:"u"` // 登录的人的 ID 的摘要：回调时还得是他
	Exp   int64  `json:"e"`
}

func (g *Grants) handleStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	next := safeNext(r.URL.Query().Get("next"))
	user, app := g.o.Who(r)
	a, ok := g.o.Apps[app]
	if user == "" || !ok {
		http.Redirect(w, r, withResult(next, "unavailable"), http.StatusFound)
		return
	}
	state := randomString(24)
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: g.signState(stateData{State: state, App: app, Next: next, User: digest(user), Exp: g.now().Add(stateTTL).Unix()}),
		Path: "/", MaxAge: int(stateTTL / time.Second), HttpOnly: true, Secure: strings.HasPrefix(a.RedirectURL, "https://"), SameSite: http.SameSiteLaxMode,
	})
	q := url.Values{}
	q.Set("client_id", a.AppID)
	q.Set("redirect_uri", a.RedirectURL)
	q.Set("response_type", "code")
	q.Set("state", state)
	q.Set("scope", g.scope())
	http.Redirect(w, r, g.authorizeURL+"?"+q.Encode(), http.StatusFound)
}

func (g *Grants) callback(app string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		st, ok := g.readState(r)
		http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil || strings.HasPrefix(g.o.Apps[app].RedirectURL, "https://"), SameSite: http.SameSiteLaxMode})
		next := "/"
		if ok {
			next = st.Next
		}
		user, whoApp := g.o.Who(r)
		q := r.URL.Query()
		if !ok || q.Get("state") == "" || q.Get("state") != st.State || st.App != app || whoApp != app || st.User != digest(user) {
			g.log.Warn("feishugrant: 授权回调对不上（换了浏览器、放太久、换了登录的人，或不是本人发起的），没收", "app", app)
			http.Redirect(w, r, withResult(next, "failed"), http.StatusFound)
			return
		}
		if q.Get("error") != "" || q.Get("code") == "" {
			http.Redirect(w, r, withResult(next, "denied"), http.StatusFound)
			return
		}
		a := g.o.Apps[app]
		tok, err := g.exchange(r.Context(), a, map[string]string{"grant_type": "authorization_code", "code": q.Get("code"), "redirect_uri": a.RedirectURL})
		if err != nil {
			g.log.Warn("feishugrant: 授权码换令牌失败", "app", app, "err", err.Error())
			http.Redirect(w, r, withResult(next, "failed"), http.StatusFound)
			return
		}
		if tok.RefreshToken == "" {
			g.log.Error("feishugrant: 飞书没给 refresh_token：授权地址要带 offline_access，飞书开放平台里这个应用也要开通并发布 offline_access 权限", "app", app)
			http.Redirect(w, r, withResult(next, "failed"), http.StatusFound)
			return
		}
		fu, err := g.userInfo(r.Context(), tok.AccessToken)
		if err != nil {
			g.log.Warn("feishugrant: 读授权的飞书账号失败", "app", app, "err", err.Error())
			http.Redirect(w, r, withResult(next, "failed"), http.StatusFound)
			return
		}
		if g.o.Check != nil {
			if err := g.o.Check(r, app, fu); err != nil {
				g.log.Warn("feishugrant: 授权的飞书账号和登录的人对不上，没收", "app", app, "why", err.Error())
				http.Redirect(w, r, withResult(next, "mismatch"), http.StatusFound)
				return
			}
		}
		now := g.now()
		g.mu.Lock()
		g.grants[user] = grant{
			App: app, OpenID: fu.OpenID, UnionID: fu.UnionID, Name: fu.Name, RefreshSeal: g.seal(tok.RefreshToken),
			GrantedAt: now, RefreshedAt: now, RefreshExpiresAt: after(now, tok.RefreshExpiresIn), LastUsedAt: now,
		}
		g.access[user] = cachedToken{token: tok.AccessToken, expires: after(now, tok.ExpiresIn)}
		err = g.saveLocked()
		g.mu.Unlock()
		if err != nil {
			g.log.Error("feishugrant: 授权落盘失败（这次能用，重启后要重新授权）", "err", err.Error())
		}
		g.log.Info("feishugrant: 已授权", "user", user, "app", app)
		http.Redirect(w, r, next, http.StatusFound)
	})
}

func (g *Grants) scope() string {
	fields := strings.Fields(g.o.Scopes)
	for _, f := range fields {
		if f == "offline_access" {
			return strings.Join(fields, " ")
		}
	}
	return strings.Join(append([]string{"offline_access"}, fields...), " ")
}

// ---------- 令牌 ----------

func (g *Grants) token(ctx context.Context, user string) (string, error) {
	unlock := g.lock(user)
	defer unlock()
	g.mu.Lock()
	gr, ok := g.touchLocked(user)
	c := g.access[user]
	g.mu.Unlock()
	if !ok || gr.ReauthRequired || gr.RefreshSeal == "" {
		return "", ErrGrantRequired
	}
	if c.token != "" && c.expires.After(g.now().Add(time.Minute)) {
		return c.token, nil
	}
	return g.refreshLocked(ctx, user)
}

// refreshLocked 用库里当前的 refresh_token 换新令牌并轮换。调用方持有这个人的刷新锁。
// 每次从库里重读（不信手里的旧值，可能已被用掉）；标失效和写回都比对用掉的那一份（期间重新授权过就不覆盖）。
func (g *Grants) refreshLocked(ctx context.Context, user string) (string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		g.mu.Lock()
		gr, ok := g.grants[user]
		g.mu.Unlock()
		if !ok || gr.ReauthRequired || gr.RefreshSeal == "" {
			return "", ErrGrantRequired
		}
		a, ok := g.o.Apps[gr.App]
		if !ok {
			return "", ErrGrantRequired
		}
		used := gr.RefreshSeal
		refresh, err := g.open(used)
		if err != nil {
			if g.markReauth(user, used, "解不开（SealKey 换过）") {
				return "", ErrGrantRequired
			}
			continue
		}
		tok, err := g.exchange(ctx, a, map[string]string{"grant_type": "refresh_token", "refresh_token": refresh})
		var fe *feishuError
		if errors.As(err, &fe) && fe.reauth() {
			if g.markReauth(user, used, fe.Error()) {
				return "", ErrGrantRequired
			}
			continue // 期间重新授权过：用新的再试，不误伤
		}
		if err != nil {
			return "", err // 临时故障：旧令牌没确认作废，留着下次再试
		}
		if tok.AccessToken == "" || tok.ExpiresIn <= 0 {
			return "", errors.New("feishugrant: 飞书刷新的回包不全")
		}
		if tok.RefreshToken == "" {
			// 旧的已经用掉、没给新的：这次的 access_token 能用，之后只能重新授权
			g.markReauth(user, used, "刷新没给新的 refresh_token")
			return tok.AccessToken, nil
		}
		now := g.now()
		g.mu.Lock()
		if cur, ok := g.grants[user]; ok && cur.RefreshSeal == used {
			cur.RefreshSeal = g.seal(tok.RefreshToken)
			cur.RefreshedAt = now
			cur.RefreshExpiresAt = after(now, tok.RefreshExpiresIn)
			g.grants[user] = cur
			g.access[user] = cachedToken{token: tok.AccessToken, expires: after(now, tok.ExpiresIn)}
			if err := g.saveLocked(); err != nil {
				g.log.Error("feishugrant: 轮换后落盘失败（重启后要重新授权）", "user", user, "err", err.Error())
			}
		}
		// 期间重新授权过：库里是更新的授权，不覆盖；这次换到的 access_token 只给本次用、不进缓存
		g.mu.Unlock()
		return tok.AccessToken, nil
	}
	return "", ErrGrantRequired
}

// markReauth：库里还是用掉的那一份才标失效（返回 true）；已被换成新的返回 false。
func (g *Grants) markReauth(user, used, why string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	cur, ok := g.grants[user]
	if !ok || cur.RefreshSeal != used {
		return false
	}
	cur.RefreshSeal = ""
	cur.ReauthRequired = true
	g.grants[user] = cur
	delete(g.access, user)
	if err := g.saveLocked(); err != nil {
		g.log.Error("feishugrant: 标记失效后落盘失败", "user", user, "err", err.Error())
	}
	g.log.Warn("feishugrant: 飞书授权失效，要本人重新授权", "user", user, "app", cur.App, "why", why)
	return true
}

// touchLocked 记「最近用过」（每小时最多写一次盘），返回这个人的授权。调用方持有 g.mu。
func (g *Grants) touchLocked(user string) (grant, bool) {
	gr, ok := g.grants[user]
	if !ok {
		return gr, false
	}
	if now := g.now(); now.Sub(gr.LastUsedAt) >= touchEvery {
		gr.LastUsedAt = now
		g.grants[user] = gr
		if err := g.saveLocked(); err != nil {
			g.log.Warn("feishugrant: 记最近使用落盘失败", "err", err.Error())
		}
	}
	return gr, true
}

func (g *Grants) lock(user string) func() {
	g.locksMu.Lock()
	l, ok := g.locks[user]
	if !ok {
		l = &sync.Mutex{}
		g.locks[user] = l
	}
	g.locksMu.Unlock()
	l.Lock()
	return l.Unlock
}

// ---------- 后台保活 ----------

func (g *Grants) keepAlive(ctx context.Context) {
	t := time.NewTimer(time.Minute) // 启动稍等再跑第一轮，避开发布时的高峰
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		g.keepAliveOnce(ctx)
		t.Reset(keepAliveEvery)
	}
}

// keepAliveOnce 轮换所有到期的授权，返回轮换成功、失效的人数。
func (g *Grants) keepAliveOnce(ctx context.Context) (rotated, lost int) {
	now := g.now()
	var due []string
	g.mu.Lock()
	for user, gr := range g.grants {
		if g.due(gr, now) {
			due = append(due, user)
		}
	}
	g.mu.Unlock()
	for _, user := range due {
		if ctx.Err() != nil {
			break
		}
		switch err := g.rotateIfDue(ctx, user); {
		case err == nil:
			rotated++
		case errors.Is(err, ErrGrantRequired):
			lost++
		case errors.Is(err, errNotDue):
		default:
			g.log.Warn("feishugrant: 保活失败，下轮再试", "user", user, "err", err.Error())
		}
	}
	if rotated > 0 || lost > 0 {
		g.log.Info("feishugrant: 保活", "rotated", rotated, "lost", lost)
	}
	return rotated, lost
}

var errNotDue = errors.New("feishugrant: 还没到轮换时间")

func (g *Grants) rotateIfDue(ctx context.Context, user string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	unlock := g.lock(user)
	defer unlock()
	g.mu.Lock()
	gr, ok := g.grants[user]
	g.mu.Unlock()
	if !ok || !g.due(gr, g.now()) { // 拿锁期间可能已有请求替他轮换过
		return errNotDue
	}
	_, err := g.refreshLocked(ctx, user)
	return err
}

// due：30 天内用过、到了轮换时间。只认真实使用（LastUsedAt、GrantedAt），保活自己的轮换不算，不然永远停不下来。
func (g *Grants) due(gr grant, now time.Time) bool {
	if gr.ReauthRequired || gr.RefreshSeal == "" {
		return false
	}
	last := gr.LastUsedAt
	if gr.GrantedAt.After(last) {
		last = gr.GrantedAt
	}
	if now.Sub(last) > activeWithin {
		return false
	}
	wait := rotateAfter
	if !gr.RefreshExpiresAt.IsZero() {
		if half := gr.RefreshExpiresAt.Sub(gr.RefreshedAt) / 2; half > 0 && half < wait {
			wait = half
		}
	}
	return now.Sub(gr.RefreshedAt) >= wait
}

// ---------- 飞书接口 ----------

type tokenResp struct {
	Code             int    `json:"code"`
	Error            string `json:"error"`
	AccessToken      string `json:"access_token"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int64  `json:"refresh_token_expires_in"`
}

// feishuError 是飞书业务拒绝：错误在非 2xx 的 JSON 里，要解析出业务码。Error() 不带令牌和回包原文。
type feishuError struct {
	Status int
	Code   int
	OAuth  string
}

func (e *feishuError) Error() string {
	return fmt.Sprintf("飞书 HTTP %d code=%d error=%s", e.Status, e.Code, e.OAuth)
}

// reauth：20024 不是这个应用的、20026 无效、20037 过期、20064 撤销、20073 已被用过，或 invalid_grant → 只能重新授权。
// 20050、20072、5xx、网络问题是临时的，不算。
func (e *feishuError) reauth() bool {
	switch e.Code {
	case 20024, 20026, 20037, 20064, 20073:
		return true
	}
	return e.OAuth == "invalid_grant"
}

func (g *Grants) exchange(ctx context.Context, a App, fields map[string]string) (tokenResp, error) {
	body := map[string]string{"client_id": a.AppID, "client_secret": a.AppSecret}
	for k, v := range fields {
		body[k] = v
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenURL, bytes.NewReader(raw))
	if err != nil {
		return tokenResp{}, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := g.http.Do(req)
	if err != nil {
		return tokenResp{}, fmt.Errorf("feishugrant: 连不上飞书: %w", scrub(err))
	}
	defer resp.Body.Close()
	var out tokenResp
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode/100 != 2 || out.Code != 0 {
		return tokenResp{}, &feishuError{Status: resp.StatusCode, Code: out.Code, OAuth: out.Error}
	}
	if decodeErr != nil {
		return tokenResp{}, fmt.Errorf("feishugrant: 飞书回包读不懂: %w", decodeErr)
	}
	return out, nil
}

func (g *Grants) userInfo(ctx context.Context, accessToken string) (FeishuUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.userInfoURL, nil)
	if err != nil {
		return FeishuUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := g.http.Do(req)
	if err != nil {
		return FeishuUser{}, fmt.Errorf("feishugrant: 连不上飞书: %w", scrub(err))
	}
	defer resp.Body.Close()
	var out struct {
		Code int        `json:"code"`
		Data FeishuUser `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil || resp.StatusCode/100 != 2 || out.Code != 0 {
		return FeishuUser{}, fmt.Errorf("feishugrant: 读飞书账号失败（HTTP %d code=%d）", resp.StatusCode, out.Code)
	}
	if out.Data.OpenID == "" {
		return FeishuUser{}, errors.New("feishugrant: 飞书账号没有 open_id")
	}
	return out.Data, nil
}

// scrub 去掉错误里的地址（可能带查询串），只留是什么错。
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

// ---------- 加密、状态、文件 ----------

func (g *Grants) seal(plain string) string {
	nonce := make([]byte, g.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(g.aead.Seal(nonce, nonce, []byte(plain), []byte(sealLabel)))
}

func (g *Grants) open(sealed string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(sealed)
	n := g.aead.NonceSize()
	if err != nil || len(b) <= n {
		return "", errors.New("feishugrant: 密文不对")
	}
	plain, err := g.aead.Open(nil, b[:n], b[n:], []byte(sealLabel))
	if err != nil {
		return "", errors.New("feishugrant: 解不开")
	}
	return string(plain), nil
}

func (g *Grants) signState(d stateData) string {
	raw, _ := json.Marshal(d)
	p := base64.RawURLEncoding.EncodeToString(raw)
	m := hmac.New(sha256.New, g.mac)
	m.Write([]byte(p))
	return p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (g *Grants) readState(r *http.Request) (stateData, bool) {
	c, err := r.Cookie(stateCookie)
	if err != nil {
		return stateData{}, false
	}
	p, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return stateData{}, false
	}
	m := hmac.New(sha256.New, g.mac)
	m.Write([]byte(p))
	want := m.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, want) {
		return stateData{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return stateData{}, false
	}
	var d stateData
	if json.Unmarshal(raw, &d) != nil || g.now().Unix() > d.Exp {
		return stateData{}, false
	}
	d.Next = safeNext(d.Next)
	return d, true
}

func (g *Grants) load() error {
	raw, err := os.ReadFile(g.o.File)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("feishugrant: 读 %s: %w", g.o.File, err)
	}
	var d fileData
	if err := json.Unmarshal(raw, &d); err != nil {
		return fmt.Errorf("feishugrant: %s 读不懂: %w", g.o.File, err)
	}
	if d.Grants != nil {
		g.grants = d.Grants
	}
	return nil
}

// saveLocked 整份写临时文件再改名（不会写一半）。调用方持有 g.mu。
func (g *Grants) saveLocked() error {
	raw, err := json.Marshal(fileData{Version: 1, Grants: g.grants})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(g.o.File), ".feishu-grants-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, g.o.File)
}

// ---------- 小工具 ----------

// safeNext 只认本站的路径，外站地址、协议相对地址一律回首页；去掉 #。
func safeNext(raw string) string {
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = raw[:i]
	}
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\\r\n\t") {
		return "/"
	}
	if u, err := url.Parse(raw); err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return raw
}

// withResult 在回去的地址上带 feishu_grant=<结果>（denied 不同意、failed 没成、mismatch 账号对不上、unavailable 授权不了）。
func withResult(next, result string) string {
	sep := "?"
	if strings.Contains(next, "?") {
		sep = "&"
	}
	return next + sep + "feishu_grant=" + result
}

func after(now time.Time, seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return now.Add(time.Duration(seconds) * time.Second)
}

func digest(s string) string {
	sum := sha256.Sum256([]byte("fbif-feishu-grant-user\x00" + s))
	return hex.EncodeToString(sum[:16])
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
