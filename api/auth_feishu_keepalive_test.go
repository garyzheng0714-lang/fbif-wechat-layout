package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// scriptedRefreshProvider 按调用次序返回预设的刷新结果，并记录每次收到的
// refresh_token，用来断言轮换只用最新一份令牌。
type scriptedRefreshProvider struct {
	fakeFeishuProvider
	refresh  func(call int, refreshToken string) (feishuUserToken, error)
	received []string
}

func (p *scriptedRefreshProvider) RefreshUserToken(_ context.Context, refreshToken string) (feishuUserToken, error) {
	p.received = append(p.received, refreshToken)
	return p.refresh(len(p.received), refreshToken)
}

// keepAliveTestService 的时钟比真实时间晚 400 天，存储层用真实时间写的
// UpdatedAt 因此一定落在 30 天活跃窗口之外，测试不随运行日期漂移。
func keepAliveTestService(t *testing.T, provider feishuProvider) (*authService, time.Time) {
	t.Helper()
	service := newTestAuthService(t, provider)
	now := time.Now().UTC().Add(400 * 24 * time.Hour).Truncate(time.Second)
	service.config.Now = func() time.Time { return now }
	return service, now
}

func seedFeishuUser(t *testing.T, service *authService, user authUser, refreshToken string) authUser {
	t.Helper()
	seal, err := service.sealRefreshToken(refreshToken)
	if err != nil {
		t.Fatal(err)
	}
	user.AuthMode = authModeFeishu
	user.Status = authUserActive
	user.FeishuRefreshTokenSeal = seal
	if err := service.users.Upsert(user); err != nil {
		t.Fatal(err)
	}
	stored, _, err := service.users.Get(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func mustGetUser(t *testing.T, service *authService, id string) authUser {
	t.Helper()
	user, ok, err := service.users.Get(id)
	if err != nil || !ok {
		t.Fatalf("get user %s: ok=%v err=%v", id, ok, err)
	}
	return user
}

func TestFeishuRefreshRejectionRequiresReloginWithClearError(t *testing.T) {
	provider := &scriptedRefreshProvider{refresh: func(int, string) (feishuUserToken, error) {
		return feishuUserToken{}, &feishuOAuthError{Code: 20037, OAuthError: "invalid_grant"}
	}}
	service, now := keepAliveTestService(t, provider)
	user := seedFeishuUser(t, service, authUser{ID: "feishu:ou_expired", Name: "陈实", FeishuOpenID: "ou_expired", LastSeenAt: now}, "rt-expired")
	session, err := service.issueSession(user)
	if err != nil {
		t.Fatal(err)
	}
	mux := registerTestAuthMux(service)

	for attempt := 0; attempt < 2; attempt++ {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, requestWithSession(http.MethodPost, "/api/feishu/docs/export", session,
			[]byte(`{"token":"doxcn_test","type":"docx","title":"测试"}`)))
		var payload struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &payload)
		if rec.Code != http.StatusForbidden || payload.Code != "feishu_reauth_required" || !strings.Contains(payload.Error, "重新用飞书登录") {
			t.Fatalf("attempt %d status=%d body=%s", attempt, rec.Code, rec.Body.String())
		}
	}
	// 第二次直接按标记拒绝，不再拿已作废的令牌去打飞书。
	if len(provider.received) != 1 {
		t.Fatalf("refresh calls=%d, want 1", len(provider.received))
	}
	stored := mustGetUser(t, service, user.ID)
	if !stored.FeishuReauthRequired || stored.FeishuRefreshTokenSeal != "" {
		t.Fatalf("reauth=%v seal=%q", stored.FeishuReauthRequired, stored.FeishuRefreshTokenSeal)
	}
	// 会话本身不受影响：/api/me 仍认得这个人。
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, requestWithSession(http.MethodGet, "/api/me", session, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/me status=%d", rec.Code)
	}
}

func TestFeishuRefreshTransientFailureKeepsToken(t *testing.T) {
	for _, transient := range []error{
		&feishuOAuthError{Code: 20072},
		&feishuOAuthError{Code: 20050},
		errors.New("feishu API status 502"),
	} {
		provider := &scriptedRefreshProvider{refresh: func(int, string) (feishuUserToken, error) {
			return feishuUserToken{}, transient
		}}
		service, now := keepAliveTestService(t, provider)
		user := seedFeishuUser(t, service, authUser{ID: "feishu:ou_flaky", FeishuOpenID: "ou_flaky", LastSeenAt: now}, "rt-still-valid")
		if _, err := service.userAccessToken(context.Background(), user); err == nil || errors.Is(err, errFeishuReauthRequired) {
			t.Fatalf("%v: err=%v, want transient error", transient, err)
		}
		stored := mustGetUser(t, service, user.ID)
		if stored.FeishuReauthRequired || stored.FeishuRefreshTokenSeal != user.FeishuRefreshTokenSeal {
			t.Fatalf("%v: transient failure must keep the token", transient)
		}
	}
}

func TestFeishuRefreshRetriesWhenTokenWasRotatedConcurrently(t *testing.T) {
	var service *authService
	provider := &scriptedRefreshProvider{}
	provider.refresh = func(call int, refreshToken string) (feishuUserToken, error) {
		if call == 1 {
			// 另一个进程抢先用掉了 rt-old 并写回了新令牌。
			seal, err := service.sealRefreshToken("rt-rotated-elsewhere")
			if err != nil {
				return feishuUserToken{}, err
			}
			current := mustGetUser(t, service, "feishu:ou_race")
			if _, ok, err := service.users.RotateFeishuRefreshToken("feishu:ou_race", current.FeishuRefreshTokenSeal, seal, service.now(), time.Time{}); err != nil || !ok {
				return feishuUserToken{}, fmt.Errorf("rotate elsewhere ok=%v err=%v", ok, err)
			}
			return feishuUserToken{}, &feishuOAuthError{Code: 20073, OAuthError: "invalid_grant"}
		}
		return feishuUserToken{AccessToken: "u-new", RefreshToken: "rt-next", ExpiresIn: 2 * time.Hour, RefreshExpiresIn: 7 * 24 * time.Hour}, nil
	}
	service, now := keepAliveTestService(t, provider)
	user := seedFeishuUser(t, service, authUser{ID: "feishu:ou_race", FeishuOpenID: "ou_race", LastSeenAt: now}, "rt-old")

	token, err := service.userAccessToken(context.Background(), user)
	if err != nil || token != "u-new" {
		t.Fatalf("token=%q err=%v", token, err)
	}
	if strings.Join(provider.received, ",") != "rt-old,rt-rotated-elsewhere" {
		t.Fatalf("refresh tokens used=%v", provider.received)
	}
	stored := mustGetUser(t, service, user.ID)
	if stored.FeishuReauthRequired {
		t.Fatal("concurrent rotation must not force relogin")
	}
	plain, err := service.openRefreshToken(stored.FeishuRefreshTokenSeal)
	if err != nil || plain != "rt-next" {
		t.Fatalf("stored refresh token=%q err=%v", plain, err)
	}
	if !stored.FeishuTokenRefreshedAt.Equal(now) || !stored.FeishuRefreshExpiresAt.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("refreshed=%v expires=%v", stored.FeishuTokenRefreshedAt, stored.FeishuRefreshExpiresAt)
	}
}

func TestFeishuRefreshKeepsNewerLoginGrant(t *testing.T) {
	var service *authService
	provider := &scriptedRefreshProvider{}
	provider.refresh = func(int, string) (feishuUserToken, error) {
		// 飞书已经用掉 rt-old，响应回来之前这个人重新登录、写进了新授权。
		seal, err := service.sealRefreshToken("rt-from-relogin")
		if err != nil {
			return feishuUserToken{}, err
		}
		if _, err := service.users.UpdateFeishuTokens("feishu:ou_relogin", seal, service.now()); err != nil {
			return feishuUserToken{}, err
		}
		return feishuUserToken{AccessToken: "u-old-chain", RefreshToken: "rt-old-chain-next", ExpiresIn: 2 * time.Hour, RefreshExpiresIn: 7 * 24 * time.Hour}, nil
	}
	service, now := keepAliveTestService(t, provider)
	user := seedFeishuUser(t, service, authUser{ID: "feishu:ou_relogin", FeishuOpenID: "ou_relogin", LastSeenAt: now}, "rt-old")

	token, err := service.userAccessToken(context.Background(), user)
	if err != nil || token != "u-old-chain" {
		t.Fatalf("token=%q err=%v, want this refresh's access token", token, err)
	}
	stored := mustGetUser(t, service, user.ID)
	plain, err := service.openRefreshToken(stored.FeishuRefreshTokenSeal)
	if err != nil || plain != "rt-from-relogin" {
		t.Fatalf("stored refresh token=%q err=%v, the newer login grant must survive", plain, err)
	}
	if _, cached := service.accessCache[user.ID]; cached {
		t.Fatal("old-chain access token must not be cached over the newer login")
	}
}

func TestFeishuTokenKeepAliveRotatesOnlyActiveUsers(t *testing.T) {
	provider := &scriptedRefreshProvider{refresh: func(_ int, refreshToken string) (feishuUserToken, error) {
		if refreshToken == "rt-revoked" {
			return feishuUserToken{}, &feishuOAuthError{Code: 20064, OAuthError: "invalid_grant"}
		}
		return feishuUserToken{AccessToken: "u-" + refreshToken, RefreshToken: refreshToken + "-next", ExpiresIn: 2 * time.Hour, RefreshExpiresIn: 7 * 24 * time.Hour}, nil
	}}
	service, now := keepAliveTestService(t, provider)
	day := 24 * time.Hour
	seed := func(id string, user authUser, refreshToken string) {
		user.ID = "feishu:" + id
		user.FeishuOpenID = id
		seedFeishuUser(t, service, user, refreshToken)
	}
	// 6 天没用飞书功能、但这周打开过页面：refresh_token 快到 7 天，必须续。
	seed("ou_due", authUser{LastSeenAt: now.Add(-2 * day), FeishuTokenRefreshedAt: now.Add(-6 * day), FeishuRefreshExpiresAt: now.Add(day)}, "rt-due")
	// 上线前的老记录：没有续期时间，只要 30 天内登录过就先续一次。
	seed("ou_legacy", authUser{LastLoginAt: now.Add(-20 * day)}, "rt-legacy")
	// 昨天刚续过：不动。
	seed("ou_fresh", authUser{LastSeenAt: now, FeishuTokenRefreshedAt: now.Add(-day), FeishuRefreshExpiresAt: now.Add(6 * day)}, "rt-fresh")
	// 31 天没用：会话已过期，停止保活。
	seed("ou_idle", authUser{LastSeenAt: now.Add(-31 * day), LastLoginAt: now.Add(-40 * day), FeishuTokenRefreshedAt: now.Add(-6 * day)}, "rt-idle")
	// 授权被撤销：标记需重新登录。
	seed("ou_revoked", authUser{LastSeenAt: now, FeishuTokenRefreshedAt: now.Add(-4 * day)}, "rt-revoked")

	rotated, reauth := service.keepFeishuTokensAlive(context.Background())
	if rotated != 2 || reauth != 1 {
		t.Fatalf("rotated=%d reauth=%d, want 2/1 (tokens used %v)", rotated, reauth, provider.received)
	}
	if got := strings.Join(provider.received, ","); got != "rt-due,rt-legacy,rt-revoked" {
		t.Fatalf("refresh tokens used=%s", got)
	}
	for _, id := range []string{"ou_due", "ou_legacy"} {
		stored := mustGetUser(t, service, "feishu:"+id)
		if !stored.FeishuTokenRefreshedAt.Equal(now) || stored.FeishuReauthRequired {
			t.Fatalf("%s refreshed=%v reauth=%v", id, stored.FeishuTokenRefreshedAt, stored.FeishuReauthRequired)
		}
	}
	if !mustGetUser(t, service, "feishu:ou_revoked").FeishuReauthRequired {
		t.Fatal("revoked token must require relogin")
	}
	// 轮换会刷新 UpdatedAt，但不能被当成「使用过」：再过 31 天、没人打开页面时就停。
	later := now.Add(31 * day)
	service.config.Now = func() time.Time { return later }
	if mustGetUser(t, service, "feishu:ou_due").UpdatedAt.IsZero() {
		t.Fatal("rotation should stamp UpdatedAt")
	}
	provider.received = nil
	if rotated, _ := service.keepFeishuTokensAlive(context.Background()); rotated != 0 || len(provider.received) != 0 {
		t.Fatalf("idle users must not be kept alive by their own rotations: rotated=%d used=%v", rotated, provider.received)
	}
}

func TestCurrentUserRecordsLastSeenAndSlidesSession(t *testing.T) {
	service, now := keepAliveTestService(t, &fakeFeishuProvider{})
	user := seedFeishuUser(t, service, authUser{ID: "feishu:ou_seen", FeishuOpenID: "ou_seen"}, "rt-seen")
	session, err := service.issueSession(user)
	if err != nil {
		t.Fatal(err)
	}
	later := now.Add(29 * 24 * time.Hour)
	service.config.Now = func() time.Time { return later }
	rec := httptest.NewRecorder()
	registerTestAuthMux(service).ServeHTTP(rec, requestWithSession(http.MethodGet, "/api/me", session, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/me status=%d", rec.Code)
	}
	if got := mustGetUser(t, service, user.ID).LastSeenAt; !got.Equal(later) {
		t.Fatalf("last seen=%v, want %v", got, later)
	}
	renewed := rec.Header().Get("X-Session-Token")
	var claims authSessionClaims
	if err := service.verifyPayload("session", renewed, &claims); err != nil {
		t.Fatal(err)
	}
	if want := later.Add(30 * 24 * time.Hour).Unix(); claims.Expires != want {
		t.Fatalf("renewed session expires=%d, want %d (30 days from last use)", claims.Expires, want)
	}
}

func TestFeishuReloginClearsReauthAndRecordsRotation(t *testing.T) {
	provider := &fakeFeishuProvider{
		exchangeToken: feishuUserToken{AccessToken: "u-login", RefreshToken: "rt-login", ExpiresIn: 2 * time.Hour, RefreshExpiresIn: 7 * 24 * time.Hour},
		userInfo:      feishuUserProfile{OpenID: "ou_back", Name: "陈实"},
	}
	service, now := keepAliveTestService(t, provider)
	if err := service.users.Upsert(authUser{
		ID: "feishu:ou_back", Name: "陈实", AuthMode: authModeFeishu, Status: authUserActive,
		FeishuOpenID: "ou_back", FeishuReauthRequired: true,
	}); err != nil {
		t.Fatal(err)
	}
	state, err := service.issueOAuthState("/mp/fbif")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	registerTestAuthMux(service).ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/auth/feishu/fbif/callback?code=ok&state="+state, nil))
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/auth-callback.html#") {
		t.Fatalf("callback status=%d location=%s", rec.Code, rec.Header().Get("Location"))
	}
	stored := mustGetUser(t, service, "feishu:ou_back")
	if stored.FeishuReauthRequired || stored.FeishuRefreshTokenSeal == "" {
		t.Fatalf("relogin must restore authorization: reauth=%v", stored.FeishuReauthRequired)
	}
	if !stored.FeishuTokenRefreshedAt.Equal(now) || !stored.LastSeenAt.Equal(now) || !stored.FeishuRefreshExpiresAt.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("refreshed=%v seen=%v expires=%v", stored.FeishuTokenRefreshedAt, stored.LastSeenAt, stored.FeishuRefreshExpiresAt)
	}
}

func TestHTTPFeishuProviderRefreshReportsOAuthErrorCode(t *testing.T) {
	cases := []struct {
		status int
		body   string
		reauth bool
	}{
		{http.StatusBadRequest, `{"code":20037,"error":"invalid_grant","error_description":"The refresh token has expired."}`, true},
		{http.StatusBadRequest, `{"code":20064,"error":"invalid_grant"}`, true},
		{http.StatusServiceUnavailable, `{"code":20072,"error":"server_error"}`, false},
	}
	for _, tc := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		provider := newHTTPFeishuProvider(server.Client())
		provider.apiBaseURL = server.URL
		_, err := provider.RefreshUserToken(context.Background(), "rt-test")
		server.Close()
		var oauthErr *feishuOAuthError
		if !errors.As(err, &oauthErr) || oauthErr.reauthRequired() != tc.reauth {
			t.Fatalf("body=%s err=%v", tc.body, err)
		}
		if strings.Contains(err.Error(), "rt-test") {
			t.Fatal("error must not leak the refresh token")
		}
	}
}

func TestHTTPFeishuProviderStatusErrorKeepsFeishuCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":1069914,"msg":"file token invalid"}`)
	}))
	defer server.Close()
	provider := newHTTPFeishuProvider(server.Client())
	provider.apiBaseURL = server.URL
	_, err := provider.ExportDocument(context.Background(), "u-user-token", feishuDocumentRef{Token: "doccn_test", Type: "docx"})
	if err == nil || !strings.Contains(err.Error(), "code=1069914") {
		t.Fatalf("err=%v", err)
	}
}
