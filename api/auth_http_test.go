package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeFeishuProvider struct {
	exchangeToken feishuUserToken
	userInfo      feishuUserProfile
	searchResults []feishuDocument
	exportResult  feishuExportedFile
	exportErr     error
	chatResults   []feishuChat
	searchCalls   int
	exportCalls   int
	chatCalls     int
}

func (f *fakeFeishuProvider) ExchangeCode(_ context.Context, _, _ string) (feishuUserToken, error) {
	return f.exchangeToken, nil
}

func (f *fakeFeishuProvider) RefreshUserToken(_ context.Context, _ string) (feishuUserToken, error) {
	return f.exchangeToken, nil
}

func (f *fakeFeishuProvider) GetUserProfile(_ context.Context, _ string) (feishuUserProfile, error) {
	return f.userInfo, nil
}

func (f *fakeFeishuProvider) SearchDocuments(_ context.Context, _ string, query string, limit int) ([]feishuDocument, error) {
	f.searchCalls++
	if query == "" || limit <= 0 {
		return nil, nil
	}
	return f.searchResults, nil
}

func (f *fakeFeishuProvider) ExportDocument(_ context.Context, _ string, ref feishuDocumentRef) (feishuExportedFile, error) {
	f.exportCalls++
	if ref.Token == "" {
		return feishuExportedFile{}, errFeishuInvalidDocument
	}
	if f.exportErr != nil {
		return feishuExportedFile{}, f.exportErr
	}
	return f.exportResult, nil
}

func (f *fakeFeishuProvider) ListChats(_ context.Context, _ string) ([]feishuChat, error) {
	f.chatCalls++
	return f.chatResults, nil
}

func newTestAuthService(t *testing.T, provider feishuProvider) *authService {
	t.Helper()
	store := newAuthUserStore(filepath.Join(t.TempDir(), "auth-users.json"))
	service, err := newAuthService(authConfig{
		SessionSecret: []byte("test-session-secret-that-is-long-enough"),
		FeishuAppID:   "cli_test",
		FeishuSecret:  "app-secret",
		RedirectURI:   "https://layout.example.test/auth/feishu/fbif/callback",
		SessionTTL:    30 * 24 * time.Hour,
		Now:           func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	}, store, provider)
	if err != nil {
		t.Fatalf("newAuthService: %v", err)
	}
	return service
}

func registerTestAuthMux(service *authService) *http.ServeMux {
	mux := http.NewServeMux()
	registerAuthRoutes(mux, service)
	registerFeishuDocumentRoutes(mux, service)
	registerFeishuChatRoutes(mux, service)
	return mux
}

func mustSessionForUser(t *testing.T, service *authService, user authUser) string {
	t.Helper()
	if err := service.users.Upsert(user); err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	token, err := service.issueSession(user)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	return token
}

func requestWithSession(method, target, token string, body []byte) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("X-Session-Token", token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func TestPasswordLoginReturnsIdentityButCannotUseFeishuDocuments(t *testing.T) {
	provider := &fakeFeishuProvider{}
	service := newTestAuthService(t, provider)
	passwordHash, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := service.users.Upsert(authUser{
		ID:           "password:vendor",
		Name:         "外部编辑",
		Username:     "vendor",
		PasswordHash: passwordHash,
		AuthMode:     authModePassword,
		Status:       authUserActive,
	}); err != nil {
		t.Fatalf("seed password user: %v", err)
	}
	mux := registerTestAuthMux(service)

	loginBody := []byte(`{"username":"vendor","password":"correct horse battery staple"}`)
	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, requestWithSession(http.MethodPost, "/auth/password/login", "", loginBody))
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", loginRec.Code, loginRec.Body.String())
	}
	var loginPayload struct {
		SessionToken string         `json:"session_token"`
		User         authPublicUser `json:"user"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginPayload); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if loginPayload.User.Name != "外部编辑" || loginPayload.User.AuthMode != string(authModePassword) {
		t.Fatalf("unexpected login user: %+v", loginPayload.User)
	}
	if loginPayload.User.Capabilities.FeishuDocuments {
		t.Fatalf("password user must not receive Feishu document capability")
	}
	loginCookies := loginRec.Result().Cookies()
	if len(loginCookies) != 1 || loginCookies[0].Name != authSessionCookieName || loginCookies[0].MaxAge <= 0 {
		t.Fatalf("remembered login must receive a persistent session cookie: %+v", loginCookies)
	}

	sessionOnlyBody := []byte(`{"username":"vendor","password":"correct horse battery staple","remember":false}`)
	sessionOnlyRec := httptest.NewRecorder()
	mux.ServeHTTP(sessionOnlyRec, requestWithSession(http.MethodPost, "/auth/password/login", "", sessionOnlyBody))
	if sessionOnlyRec.Code != http.StatusOK {
		t.Fatalf("session-only login status=%d body=%s", sessionOnlyRec.Code, sessionOnlyRec.Body.String())
	}
	sessionOnlyCookies := sessionOnlyRec.Result().Cookies()
	if len(sessionOnlyCookies) != 1 || sessionOnlyCookies[0].Name != authSessionCookieName || sessionOnlyCookies[0].MaxAge != 0 {
		t.Fatalf("non-remembered login must receive a session cookie: %+v", sessionOnlyCookies)
	}

	searchRec := httptest.NewRecorder()
	mux.ServeHTTP(searchRec, requestWithSession(http.MethodGet, "/api/feishu/docs/search?q=新品", loginPayload.SessionToken, nil))
	if searchRec.Code != http.StatusForbidden {
		t.Fatalf("password search status=%d body=%s", searchRec.Code, searchRec.Body.String())
	}
	if !strings.Contains(searchRec.Body.String(), `"code":"feishu_login_required"`) {
		t.Fatalf("missing capability error code: %s", searchRec.Body.String())
	}
	if provider.searchCalls != 0 {
		t.Fatalf("password user reached Feishu provider")
	}
}

func TestPasswordLoginRateLimitBlocksRepeatedGuessing(t *testing.T) {
	service := newTestAuthService(t, &fakeFeishuProvider{})
	passwordHash, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := service.users.Upsert(authUser{
		ID:           "password:vendor",
		Name:         "外部编辑",
		Username:     "vendor",
		PasswordHash: passwordHash,
		AuthMode:     authModePassword,
		Status:       authUserActive,
	}); err != nil {
		t.Fatalf("seed password user: %v", err)
	}
	mux := registerTestAuthMux(service)
	for attempt := 0; attempt < 8; attempt++ {
		rec := httptest.NewRecorder()
		body := []byte(`{"username":"vendor","password":"wrong-password"}`)
		mux.ServeHTTP(rec, requestWithSession(http.MethodPost, "/auth/password/login", "", body))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d body=%s", attempt+1, rec.Code, rec.Body.String())
		}
	}
	blocked := httptest.NewRecorder()
	body := []byte(`{"username":"vendor","password":"correct horse battery staple"}`)
	mux.ServeHTTP(blocked, requestWithSession(http.MethodPost, "/auth/password/login", "", body))
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	if blocked.Header().Get("Retry-After") == "" {
		t.Fatalf("rate-limited response missing Retry-After")
	}
}

func TestFeishuDocumentRoutesRequireAuthentication(t *testing.T) {
	service := newTestAuthService(t, &fakeFeishuProvider{})
	mux := registerTestAuthMux(service)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, requestWithSession(http.MethodGet, "/api/feishu/docs/search?q=新品", "", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"authentication_required"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestFeishuChatRouteUsesLoggedInUserToken(t *testing.T) {
	provider := &fakeFeishuProvider{chatResults: []feishuChat{{ChatID: "oc_chat_1", Name: "内容审核群", ChatMode: "group"}}}
	service := newTestAuthService(t, provider)
	session := mustSessionForUser(t, service, authUser{
		ID: "feishu:ou_chat", Name: "张三", AuthMode: authModeFeishu, Status: authUserActive,
		FeishuOpenID: "ou_chat", FeishuRefreshTokenSeal: "test-refresh-seal",
	})
	service.putAccessToken("feishu:ou_chat", "user-token", service.now().Add(time.Hour))
	rec := httptest.NewRecorder()
	registerTestAuthMux(service).ServeHTTP(rec, requestWithSession(http.MethodGet, "/api/feishu/chats", session, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "内容审核群") {
		t.Fatalf("chat list status=%d body=%s", rec.Code, rec.Body.String())
	}
	if provider.chatCalls != 1 {
		t.Fatalf("chat provider calls=%d, want 1", provider.chatCalls)
	}
}

func TestFeishuLoginCanSearchAndExportDocx(t *testing.T) {
	provider := &fakeFeishuProvider{
		searchResults: []feishuDocument{{
			Token:      "doxcn_test",
			Type:       "docx",
			Title:      "2026 新品趋势",
			URL:        "https://example.feishu.cn/docx/doxcn_test",
			ModifiedAt: time.Unix(1_799_999_000, 0).UTC(),
		}},
		exportResult: feishuExportedFile{
			Filename: "2026 新品趋势.docx",
			Content:  []byte("PK\x03\x04docx-test"),
		},
	}
	service := newTestAuthService(t, provider)
	session := mustSessionForUser(t, service, authUser{
		ID:                     "feishu:ou_test",
		Name:                   "张三",
		AuthMode:               authModeFeishu,
		Status:                 authUserActive,
		FeishuOpenID:           "ou_test",
		FeishuRefreshTokenSeal: "test-refresh-seal",
	})
	service.putAccessToken("feishu:ou_test", "u-test", service.now().Add(time.Hour))
	mux := registerTestAuthMux(service)

	searchRec := httptest.NewRecorder()
	mux.ServeHTTP(searchRec, requestWithSession(http.MethodGet, "/api/feishu/docs/search?q="+url.QueryEscape("新品")+"&limit=8", session, nil))
	if searchRec.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", searchRec.Code, searchRec.Body.String())
	}
	if !strings.Contains(searchRec.Body.String(), `"title":"2026 新品趋势"`) {
		t.Fatalf("search result missing: %s", searchRec.Body.String())
	}

	exportBody := []byte(`{"token":"doxcn_test","type":"docx","title":"2026 新品趋势"}`)
	exportRec := httptest.NewRecorder()
	mux.ServeHTTP(exportRec, requestWithSession(http.MethodPost, "/api/feishu/docs/export", session, exportBody))
	if exportRec.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", exportRec.Code, exportRec.Body.String())
	}
	if got := exportRec.Header().Get("Content-Type"); !strings.Contains(got, "application/vnd.openxmlformats-officedocument.wordprocessingml.document") {
		t.Fatalf("content-type=%q", got)
	}
	if got := exportRec.Header().Get("Content-Disposition"); !strings.Contains(got, "filename*=UTF-8''") {
		t.Fatalf("content-disposition=%q", got)
	}
	if !bytes.Equal(exportRec.Body.Bytes(), provider.exportResult.Content) {
		t.Fatalf("export bytes changed")
	}
	if provider.searchCalls != 1 || provider.exportCalls != 1 {
		t.Fatalf("provider calls search=%d export=%d", provider.searchCalls, provider.exportCalls)
	}
}

func TestFeishuDocumentExportStripsEmbeddedImagesBeforeBrowserDownload(t *testing.T) {
	imageBytes := make([]byte, 32<<10)
	if _, err := rand.New(rand.NewSource(1)).Read(imageBytes); err != nil {
		t.Fatal(err)
	}
	docx := zipFixture(t, map[string][]byte{
		"word/document.xml":            []byte(`<w:document/>`),
		"word/_rels/document.xml.rels": []byte(`<Relationships/>`),
		"word/media/image1.png":        imageBytes,
		"[Content_Types].xml":          []byte(`<Types/>`),
		"_rels/.rels":                  []byte(`<Relationships/>`),
	})
	provider := &fakeFeishuProvider{
		exportResult: feishuExportedFile{
			Filename: "图片文章.docx",
			Content:  docx,
		},
	}
	service := newTestAuthService(t, provider)
	session := mustSessionForUser(t, service, authUser{
		ID:                     "feishu:ou_test",
		Name:                   "张三",
		AuthMode:               authModeFeishu,
		Status:                 authUserActive,
		FeishuOpenID:           "ou_test",
		FeishuRefreshTokenSeal: "test-refresh-seal",
	})
	service.putAccessToken("feishu:ou_test", "u-test", service.now().Add(time.Hour))

	rec := httptest.NewRecorder()
	body := []byte(`{"token":"doxcn_test","type":"docx","title":"图片文章"}`)
	registerTestAuthMux(service).ServeHTTP(
		rec,
		requestWithSession(http.MethodPost, "/api/feishu/docs/export", session, body),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-FBIF-Docx-Original-Bytes"); got == "" {
		t.Fatal("missing original byte count")
	}
	if got := rec.Header().Get("X-FBIF-Docx-Optimized-Bytes"); got == "" {
		t.Fatal("missing optimized byte count")
	}
	if got := rec.Header().Get("X-FBIF-Images-Deferred"); got != "true" {
		t.Fatalf("images deferred=%q, want true", got)
	}
	if rec.Body.Len() >= len(docx) {
		t.Fatalf("optimized docx size=%d, original=%d", rec.Body.Len(), len(docx))
	}

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("open optimized docx: %v", err)
	}
	names := map[string]bool{}
	for _, file := range zr.File {
		names[file.Name] = true
	}
	if names["word/media/image1.png"] {
		t.Fatal("embedded image was still sent inside the browser DOCX")
	}
	if !names["word/_fbif_imageUrls.json"] {
		t.Fatal("optimized DOCX is missing deferred image URL map")
	}
}

func TestFeishuDocumentExportMapsPermissionFailure(t *testing.T) {
	provider := &fakeFeishuProvider{
		exportErr: &feishuExportTaskError{JobStatus: 110, Message: "no permission"},
	}
	service := newTestAuthService(t, provider)
	session := mustSessionForUser(t, service, authUser{
		ID:                     "feishu:ou_test",
		Name:                   "张三",
		AuthMode:               authModeFeishu,
		Status:                 authUserActive,
		FeishuOpenID:           "ou_test",
		FeishuRefreshTokenSeal: "test-refresh-seal",
	})
	service.putAccessToken("feishu:ou_test", "u-test", service.now().Add(time.Hour))

	rec := httptest.NewRecorder()
	body := []byte(`{"token":"doxcn_test","type":"docx","title":"无权限文档"}`)
	registerTestAuthMux(service).ServeHTTP(
		rec,
		requestWithSession(http.MethodPost, "/api/feishu/docs/export", session, body),
	)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("export status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "feishu_document_permission_denied" {
		t.Fatalf("code=%q", payload.Code)
	}
}

func TestFeishuOAuthStateRejectsOpenRedirectAndTampering(t *testing.T) {
	provider := &fakeFeishuProvider{
		exchangeToken: feishuUserToken{
			AccessToken:  "u-access",
			RefreshToken: "ur-refresh",
			ExpiresIn:    time.Hour,
		},
		userInfo: feishuUserProfile{
			OpenID:    "ou_test",
			Name:      "张三",
			AvatarURL: "https://example.test/avatar.png",
		},
	}
	service := newTestAuthService(t, provider)
	mux := registerTestAuthMux(service)

	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, httptest.NewRequest(http.MethodGet, "/auth/feishu/fbif/login?return_to=https://evil.example/phish", nil))
	if loginRec.Code != http.StatusFound {
		t.Fatalf("login status=%d body=%s", loginRec.Code, loginRec.Body.String())
	}
	authorizeURL, err := url.Parse(loginRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	state := authorizeURL.Query().Get("state")
	if state == "" {
		t.Fatalf("authorize URL missing state: %s", authorizeURL.String())
	}

	callbackRec := httptest.NewRecorder()
	callbackTarget := "/auth/feishu/fbif/callback?code=code-test&state=" + url.QueryEscape(state)
	mux.ServeHTTP(callbackRec, httptest.NewRequest(http.MethodGet, callbackTarget, nil))
	if callbackRec.Code != http.StatusFound {
		t.Fatalf("callback status=%d body=%s", callbackRec.Code, callbackRec.Body.String())
	}
	callbackLocation := callbackRec.Header().Get("Location")
	if !strings.HasPrefix(callbackLocation, "/auth-callback.html#") {
		t.Fatalf("unexpected callback location: %s", callbackLocation)
	}
	if strings.Contains(callbackLocation, "evil.example") {
		t.Fatalf("open redirect leaked into callback: %s", callbackLocation)
	}
	if !strings.Contains(callbackLocation, "return_to=%2Fmp%2Ffbif") {
		t.Fatalf("callback did not fall back to editor: %s", callbackLocation)
	}

	backslashState, err := service.issueOAuthState(`/\evil.example/path`)
	if err != nil {
		t.Fatal(err)
	}
	backslashDecoded, err := service.verifyOAuthState(backslashState)
	if err != nil {
		t.Fatal(err)
	}
	if backslashDecoded.ReturnTo != "/mp/fbif" {
		t.Fatalf("backslash return_to=%q", backslashDecoded.ReturnTo)
	}

	tamperedRec := httptest.NewRecorder()
	mux.ServeHTTP(tamperedRec, httptest.NewRequest(http.MethodGet, callbackTarget+"x", nil))
	if tamperedRec.Code != http.StatusFound {
		t.Fatalf("tampered state status=%d body=%s", tamperedRec.Code, tamperedRec.Body.String())
	}
	if got := tamperedRec.Header().Get("Location"); got != "/login.html?error=feishu_login_failed" {
		t.Fatalf("tampered state location=%q", got)
	}

	deniedRec := httptest.NewRecorder()
	mux.ServeHTTP(deniedRec, httptest.NewRequest(http.MethodGet, "/auth/feishu/fbif/callback?error=access_denied", nil))
	if deniedRec.Code != http.StatusFound || deniedRec.Header().Get("Location") != "/login.html?error=denied" {
		t.Fatalf("denied callback status=%d location=%q", deniedRec.Code, deniedRec.Header().Get("Location"))
	}
}
