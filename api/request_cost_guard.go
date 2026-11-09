package main

import (
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"
)

const expensiveAPITokenEnv = "EXPENSIVE_API_TOKEN"

const defaultMaxRateWindows = 4096

type costGuardReason uint8

const (
	costGuardAllowed costGuardReason = iota
	costGuardRateLimited
	costGuardBusy
)

type requestRateWindow struct {
	started time.Time
	count   int
}

type requestCostGuard struct {
	mu         sync.Mutex
	limit      int
	window     time.Duration
	windows    map[string]requestRateWindow
	maxWindows int
	concurrent chan struct{}
}

func newRequestCostGuard(limit int, window time.Duration, maxConcurrent int) *requestCostGuard {
	if limit < 1 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &requestCostGuard{
		limit:      limit,
		window:     window,
		windows:    make(map[string]requestRateWindow),
		maxWindows: defaultMaxRateWindows,
		concurrent: make(chan struct{}, maxConcurrent),
	}
}

func (g *requestCostGuard) acquire(key string, now time.Time) (func(), costGuardReason) {
	select {
	case g.concurrent <- struct{}{}:
	default:
		return nil, costGuardBusy
	}

	g.mu.Lock()
	if len(g.windows) >= g.maxWindows {
		g.pruneExpiredLocked(now)
	}
	entry, exists := g.windows[key]
	if exists && (now.Sub(entry.started) >= g.window || now.Before(entry.started)) {
		delete(g.windows, key)
		entry = requestRateWindow{}
		exists = false
	}
	if !exists && len(g.windows) >= g.maxWindows {
		g.mu.Unlock()
		<-g.concurrent
		return nil, costGuardRateLimited
	}
	if !exists {
		entry = requestRateWindow{started: now}
	}
	if entry.count >= g.limit {
		g.mu.Unlock()
		<-g.concurrent
		return nil, costGuardRateLimited
	}
	entry.count++
	g.windows[key] = entry
	g.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() { <-g.concurrent })
	}, costGuardAllowed
}

func (g *requestCostGuard) pruneExpiredLocked(now time.Time) {
	for candidate, candidateWindow := range g.windows {
		if now.Before(candidateWindow.started) || now.Sub(candidateWindow.started) >= g.window {
			delete(g.windows, candidate)
		}
	}
}

var (
	imageUploadCostGuard = newRequestCostGuard(120, time.Minute, 6)
	docConvertCostGuard  = newRequestCostGuard(10, 10*time.Minute, 2)
)

func validExpensiveCredential(r *http.Request) bool {
	want := strings.TrimSpace(os.Getenv(expensiveAPITokenEnv))
	got := strings.TrimSpace(r.Header.Get("X-Expensive-API-Token"))
	if got == "" {
		got = bearerCredential(r)
	}
	if constantTimeCredentialMatch(got, want) {
		return true
	}
	// 网站发布服务本来就持有 RENDER_TOKEN，并且必须在同一流水线里调用
	// /api/render 与 /api/oss-upload。只对图片转存这一条路复用该服务凭证；
	// /api/doc-to-docx 等其他付费端点仍只接受 EXPENSIVE_API_TOKEN/admin。
	return r.URL.Path == "/api/oss-upload" &&
		constantTimeCredentialMatch(bearerCredential(r), renderToken())
}

func bearerCredential(r *http.Request) string {
	const bearer = "Bearer "
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authorization) <= len(bearer) || !strings.EqualFold(authorization[:len(bearer)], bearer) {
		return ""
	}
	return strings.TrimSpace(authorization[len(bearer):])
}

func constantTimeCredentialMatch(got, want string) bool {
	return want != "" && len(got) == len(want) &&
		subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// expensiveRoute 给写公司 OSS、占转换资源的接口把门，只放两类调用方：
//   - 登录的同事：会话有效；靠 cookie 的还得同源（SameSite=Lax 挡不住同站别的子域名借 cookie）；
//   - 持服务钥匙的服务器：EXPENSIVE_API_TOKEN，图片转存另认 RENDER_TOKEN。
//
// Origin/Referer 谁都能填，只当防跨站的补充，不当登录（2026-11-09 安全审计）。
func expensiveRoute(auth *authService, guard *requestCostGuard, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validExpensiveCredential(r) {
			if status, code, message := expensiveSessionDenial(auth, r); status != 0 {
				writeAuthError(w, status, code, message)
				return
			}
		}

		release, reason := guard.acquire(expensiveClientKey(r), time.Now())
		switch reason {
		case costGuardRateLimited:
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "请求过于频繁，请稍后重试")
			return
		case costGuardBusy:
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusServiceUnavailable, "服务正忙，请稍后重试")
			return
		}
		defer release()
		next(w, r)
	}
}

// expensiveSessionDenial 没有服务钥匙时看登录会话；放行回 status 0。
func expensiveSessionDenial(auth *authService, r *http.Request) (int, string, string) {
	if auth == nil {
		return http.StatusUnauthorized, "authentication_required", "请先登录"
	}
	if _, err := auth.currentUser(r); err != nil {
		if errors.Is(err, errAuthenticationRequired) {
			return http.StatusUnauthorized, "authentication_required", "请先登录"
		}
		return http.StatusServiceUnavailable, "auth_unavailable", "账号服务暂不可用，请稍后重试"
	}
	if strings.TrimSpace(r.Header.Get("X-Session-Token")) == "" && !sameOrigin(r) {
		return http.StatusForbidden, "invalid_request_origin", "请求来源无效，请刷新页面后重试"
	}
	return 0, "", ""
}

func expensiveClientKey(r *http.Request) string {
	remoteHost := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		remoteHost = host
	}
	remoteHost = strings.TrimSpace(remoteHost)

	if addr, err := netip.ParseAddr(strings.Trim(remoteHost, "[]")); err == nil && addr.IsLoopback() {
		if forwarded := lastForwardedIP(r.Header.Get("X-Forwarded-For")); forwarded != "" {
			return forwarded
		}
	}
	if remoteHost == "" {
		return "unknown"
	}
	return remoteHost
}

func lastForwardedIP(raw string) string {
	candidates := strings.Split(raw, ",")
	for i := len(candidates) - 1; i >= 0; i-- {
		candidate := candidates[i]
		addr, err := netip.ParseAddr(strings.TrimSpace(candidate))
		if err == nil && addr.IsValid() && !addr.IsUnspecified() {
			return addr.Unmap().String()
		}
	}
	return ""
}
