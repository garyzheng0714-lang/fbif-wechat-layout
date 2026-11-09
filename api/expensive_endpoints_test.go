package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSameOriginRequiresBrowserEvidence(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/oss-upload", nil)
	req.Host = "layout.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	if sameOrigin(req) {
		t.Fatal("request without Origin/Referer unexpectedly passed")
	}

	req.Header.Set("Origin", "https://layout.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	if !sameOrigin(req) {
		t.Fatal("matching browser Origin was rejected")
	}

	req.Header.Set("Origin", "https://attacker.example")
	if sameOrigin(req) {
		t.Fatal("cross-site Origin unexpectedly passed")
	}
}

// expensiveUploadRequest 一个最小的 base64 图片转存请求（不配上游时原样回 base64，不出网）。
func expensiveUploadRequest(path string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"base64_images":{"tiny":"data:image/png;base64,AA=="}}`))
	req.Host = "layout.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func serveExpensiveUpload(auth *authService, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	expensiveRoute(auth, newRequestCostGuard(100, time.Minute, 4), handleImageUpload)(rec, req)
	return rec
}

func expensiveTestSession(t *testing.T) (*authService, string) {
	t.Helper()
	auth := newTestAuthService(t, &fakeFeishuProvider{})
	token := mustSessionForUser(t, auth, authUser{
		ID: "feishu:ou_editor", Name: "编辑", AuthMode: authModeFeishu, Status: authUserActive, FeishuOpenID: "ou_editor",
	})
	return auth, token
}

// 2026-11-09 安全审计：Origin/Referer 谁都能填，不登录只带同源头不能写公司 OSS。
func TestExpensiveRouteRejectsForgedOriginWithoutSession(t *testing.T) {
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", "")
	t.Setenv("EXPENSIVE_API_TOKEN", "")
	t.Setenv("RENDER_TOKEN", "")
	auth, _ := expensiveTestSession(t)

	for _, path := range []string{"/api/oss-upload", "/api/wechat-upload"} {
		req := expensiveUploadRequest(path)
		req.Header.Set("Origin", "https://layout.example")
		req.Header.Set("Referer", "https://layout.example/ft/ft-default")
		if rec := serveExpensiveUpload(auth, req); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s 伪造同源头不登录 status = %d, body = %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := serveExpensiveUpload(auth, expensiveUploadRequest("/api/oss-upload")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("什么都不带 status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := serveExpensiveUpload(nil, expensiveUploadRequest("/api/oss-upload")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("没有登录服务也要拒 status = %d", rec.Code)
	}
}

func TestExpensiveRouteAllowsLoggedInBrowser(t *testing.T) {
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", "")
	t.Setenv("EXPENSIVE_API_TOKEN", "")
	auth, token := expensiveTestSession(t)

	cookieReq := expensiveUploadRequest("/api/oss-upload")
	cookieReq = cookieReq.WithContext(withLayoutUser(cookieReq.Context(), token))
	cookieReq.Header.Set("Origin", "https://layout.example")
	if rec := serveExpensiveUpload(auth, cookieReq); rec.Code != http.StatusOK {
		t.Fatalf("登录 + 同源 status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// 换统一登录后只认会话 cookie，原来飞书内嵌窗口带 X-Session-Token 免同源检查的口子没了。
	headerReq := expensiveUploadRequest("/api/oss-upload")
	headerReq = headerReq.WithContext(withLayoutUser(headerReq.Context(), token))
	headerReq.Header.Set("X-Session-Token", token)
	if rec := serveExpensiveUpload(auth, headerReq); rec.Code != http.StatusForbidden {
		t.Fatalf("登录了但说不出是同源 status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// 登录了也不许别的站借 cookie 跨站提交。
func TestExpensiveRouteRejectsCrossSiteCookie(t *testing.T) {
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", "")
	t.Setenv("EXPENSIVE_API_TOKEN", "")
	auth, token := expensiveTestSession(t)

	req := expensiveUploadRequest("/api/oss-upload")
	req = req.WithContext(withLayoutUser(req.Context(), token))
	req.Header.Set("Origin", "https://evil.layout.example")
	if rec := serveExpensiveUpload(auth, req); rec.Code != http.StatusForbidden {
		t.Fatalf("跨站借 cookie status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestExpensiveRouteAllowsServerTokenWithoutSession(t *testing.T) {
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", "")
	t.Setenv("EXPENSIVE_API_TOKEN", "server-secret")
	req := expensiveUploadRequest("/api/oss-upload")
	req.Header.Set("Authorization", "Bearer server-secret")
	if rec := serveExpensiveUpload(nil, req); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestExpensiveRouteAllowsRenderServiceToken(t *testing.T) {
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", "")
	t.Setenv("EXPENSIVE_API_TOKEN", "")
	t.Setenv("RENDER_TOKEN", "render-service-secret")
	req := expensiveUploadRequest("/api/oss-upload")
	req.Header.Set("Authorization", "Bearer render-service-secret")
	if rec := serveExpensiveUpload(nil, req); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestDocToDocxRouteRejectsForgedOriginWithoutSession(t *testing.T) {
	t.Setenv("EXPENSIVE_API_TOKEN", "")
	auth, _ := expensiveTestSession(t)
	req := httptest.NewRequest(http.MethodPost, "/api/doc-to-docx", bytes.NewBufferString("x"))
	req.Host = "layout.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://layout.example")
	rec := httptest.NewRecorder()
	expensiveRoute(auth, newRequestCostGuard(10, time.Minute, 2), handleDocToDocx)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRenderServiceTokenDoesNotAuthorizeOtherExpensiveEndpoints(t *testing.T) {
	t.Setenv("EXPENSIVE_API_TOKEN", "")
	t.Setenv("RENDER_TOKEN", "render-service-secret")
	req := httptest.NewRequest(http.MethodPost, "/api/doc-to-docx", nil)
	req.Header.Set("Authorization", "Bearer render-service-secret")

	if validExpensiveCredential(req) {
		t.Fatal("RENDER_TOKEN must only authorize /api/oss-upload")
	}
}

func TestCloudConvertFallbackRequiresExplicitCredential(t *testing.T) {
	t.Setenv("LIBREOFFICE_URL", "")
	t.Setenv("LIBREOFFICE_TOKEN", "")
	t.Setenv("CLOUDCONVERT_API_KEY", "paid-cloud-key")
	t.Setenv("EXPENSIVE_API_TOKEN", "")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "legacy.doc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(append(append([]byte{}, ole2Magic...), bytes.Repeat([]byte{0}, 32)...)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/doc-to-docx", &body)
	req.Host = "layout.example"
	req.Header.Set("Origin", "https://layout.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	handleDocToDocx(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestCloudConvertPollBacksOff 等 CloudConvert 转完越问越慢：从 1 秒起翻倍，到 8 秒封顶，之后一直 8 秒。
func TestCloudConvertPollBacksOff(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second, 8 * time.Second}
	for i, w := range want {
		if got := cloudConvertPollDelay(i + 1); got != w {
			t.Fatalf("第 %d 次问完隔 %v，要 %v", i+1, got, w)
		}
	}
	if got := cloudConvertPollDelay(1000); got != 8*time.Second {
		t.Fatalf("问了很多次也不超过 8 秒：%v", got)
	}
}

func TestRequestCostGuardEnforcesRateAndConcurrency(t *testing.T) {
	guard := newRequestCostGuard(1, time.Minute, 1)
	now := time.Unix(100, 0)

	release, reason := guard.acquire("client-a", now)
	if release == nil || reason != costGuardAllowed {
		t.Fatalf("first acquire = (release=%t, reason=%v), want allowed", release != nil, reason)
	}
	if secondRelease, secondReason := guard.acquire("client-b", now); secondRelease != nil || secondReason != costGuardBusy {
		t.Fatalf("concurrent acquire = (release=%t, reason=%v), want busy", secondRelease != nil, secondReason)
	}
	release()

	if thirdRelease, thirdReason := guard.acquire("client-a", now.Add(time.Second)); thirdRelease != nil || thirdReason != costGuardRateLimited {
		t.Fatalf("rate acquire = (release=%t, reason=%v), want rate-limited", thirdRelease != nil, thirdReason)
	}
	if fourthRelease, fourthReason := guard.acquire("client-a", now.Add(2*time.Minute)); fourthRelease == nil || fourthReason != costGuardAllowed {
		t.Fatalf("post-window acquire = (release=%t, reason=%v), want allowed", fourthRelease != nil, fourthReason)
	} else {
		fourthRelease()
	}
}

func TestRequestCostGuardBoundsClientWindowMemory(t *testing.T) {
	guard := newRequestCostGuard(10, time.Minute, 1)
	guard.maxWindows = 2
	now := time.Unix(100, 0)

	for _, key := range []string{"client-a", "client-b"} {
		release, reason := guard.acquire(key, now)
		if release == nil || reason != costGuardAllowed {
			t.Fatalf("acquire %s = (release=%t, reason=%v)", key, release != nil, reason)
		}
		release()
	}
	if release, reason := guard.acquire("client-c", now); release != nil || reason != costGuardRateLimited {
		t.Fatalf("overflow acquire = (release=%t, reason=%v), want fail-closed rate limit", release != nil, reason)
	}
	if got := len(guard.windows); got != guard.maxWindows {
		t.Fatalf("window map length = %d, want %d", got, guard.maxWindows)
	}

	release, reason := guard.acquire("client-c", now.Add(2*time.Minute))
	if release == nil || reason != costGuardAllowed {
		t.Fatalf("post-expiry acquire = (release=%t, reason=%v)", release != nil, reason)
	}
	release()
}
