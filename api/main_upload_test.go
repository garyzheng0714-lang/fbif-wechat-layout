package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleImageUploadProxyAddsForwardedOrigin(t *testing.T) {
	var gotOrigin string
	var gotReferer string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrigin = r.Header.Get("Origin")
		gotReferer = r.Header.Get("Referer")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":{"tiny":"ok"}}`))
	}))
	defer upstream.Close()

	t.Setenv("WECHAT_UPLOAD_ENDPOINT", upstream.URL)
	t.Setenv("WECHAT_UPLOAD_ORIGIN", "")

	req := httptest.NewRequest(http.MethodPost, "/api/oss-upload", bytes.NewBufferString(`{"base64_images":{"tiny":"data:image/png;base64,AA=="}}`))
	req.Host = "fbifmp-layout.foodtalks.cn"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	handleImageUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if gotOrigin != "https://fbifmp-layout.foodtalks.cn" {
		t.Fatalf("Origin = %q", gotOrigin)
	}
	if gotReferer != "https://fbifmp-layout.foodtalks.cn/" {
		t.Fatalf("Referer = %q", gotReferer)
	}
}

func TestHandleImageUploadProxyPreservesRequestOrigin(t *testing.T) {
	var gotOrigin string
	var gotReferer string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrigin = r.Header.Get("Origin")
		gotReferer = r.Header.Get("Referer")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":{"tiny":"ok"}}`))
	}))
	defer upstream.Close()

	t.Setenv("WECHAT_UPLOAD_ENDPOINT", upstream.URL)
	t.Setenv("WECHAT_UPLOAD_ORIGIN", "")

	req := httptest.NewRequest(http.MethodPost, "/api/oss-upload", bytes.NewBufferString(`{"base64_images":{"tiny":"data:image/png;base64,AA=="}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://fbifmp-layout.foodtalks.cn")
	req.Header.Set("Referer", "https://fbifmp-layout.foodtalks.cn/ft/default")
	rec := httptest.NewRecorder()

	handleImageUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if gotOrigin != "https://fbifmp-layout.foodtalks.cn" {
		t.Fatalf("Origin = %q", gotOrigin)
	}
	if gotReferer != "https://fbifmp-layout.foodtalks.cn/ft/default" {
		t.Fatalf("Referer = %q", gotReferer)
	}
}
