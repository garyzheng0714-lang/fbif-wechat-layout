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

func TestHandleImageUploadRejectsAnonymousRequestWithoutOrigin(t *testing.T) {
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", "")
	t.Setenv("EXPENSIVE_API_TOKEN", "")
	req := httptest.NewRequest(http.MethodPost, "/api/oss-upload", bytes.NewBufferString(`{"base64_images":{"tiny":"data:image/png;base64,AA=="}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handleImageUpload(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleImageUploadAllowsServerTokenWithoutBrowserOrigin(t *testing.T) {
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", "")
	t.Setenv("EXPENSIVE_API_TOKEN", "server-secret")
	req := httptest.NewRequest(http.MethodPost, "/api/oss-upload", bytes.NewBufferString(`{"base64_images":{"tiny":"data:image/png;base64,AA=="}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer server-secret")
	rec := httptest.NewRecorder()

	handleImageUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleImageUploadAllowsRenderServiceToken(t *testing.T) {
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", "")
	t.Setenv("EXPENSIVE_API_TOKEN", "")
	t.Setenv("RENDER_TOKEN", "render-service-secret")
	req := httptest.NewRequest(http.MethodPost, "/api/oss-upload", bytes.NewBufferString(`{"base64_images":{"tiny":"data:image/png;base64,AA=="}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer render-service-secret")
	rec := httptest.NewRecorder()

	handleImageUpload(rec, req)

	if rec.Code != http.StatusOK {
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
