package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: 构造一个挂上 channel 路由的 mux，serve 测试用 publicDir。
func newRoutingMux(t *testing.T, publicDir string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	registerChannelRoutes(mux, publicDir)
	return mux
}

// mustWriteAppHTML 在 t.TempDir 里写一份带 marker 的 app.html，返回目录。
func mustWriteAppHTML(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	content := []byte("<!doctype html><html><body><!--APP-HTML-MARKER--></body></html>")
	if err := os.WriteFile(filepath.Join(dir, "app.html"), content, 0644); err != nil {
		t.Fatalf("write app.html: %v", err)
	}
	return dir
}

func TestChannelRoute_MPWithPackID_ServesAppHTML(t *testing.T) {
	publicDir := mustWriteAppHTML(t)
	mux := newRoutingMux(t, publicDir)

	req := httptest.NewRequest(http.MethodGet, "/mp/fbif", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<!--APP-HTML-MARKER-->") {
		t.Fatalf("body did not contain app.html marker; got=%q", rec.Body.String())
	}
}

func TestChannelRoute_MPWithHyphenatedPackID(t *testing.T) {
	publicDir := mustWriteAppHTML(t)
	mux := newRoutingMux(t, publicDir)
	req := httptest.NewRequest(http.MethodGet, "/mp/foodtalks-series", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestChannelRoute_FT_ServesSiteHTML(t *testing.T) {
	publicDir := mustWriteBothHTML(t)
	mux := newRoutingMux(t, publicDir)
	req := httptest.NewRequest(http.MethodGet, "/ft/default", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestChannelRoute_MPWithoutPackID_ServesAppHTML(t *testing.T) {
	publicDir := mustWriteAppHTML(t)
	mux := newRoutingMux(t, publicDir)
	req := httptest.NewRequest(http.MethodGet, "/mp", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestChannelRoute_SetsNoCacheHeader(t *testing.T) {
	publicDir := mustWriteAppHTML(t)
	mux := newRoutingMux(t, publicDir)
	req := httptest.NewRequest(http.MethodGet, "/mp/fbif", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	got := rec.Header().Get("Cache-Control")
	if got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
}

// mustWriteBothHTML 写一份 app.html（带 APP marker）和 site.html（带 SITE marker），
// 用于验证 /mp 系列 serve app.html、/ft 系列 serve site.html。
func mustWriteBothHTML(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	appContent := []byte("<!doctype html><html><body><!--APP-HTML-MARKER--></body></html>")
	siteContent := []byte("<!doctype html><html><body><!--SITE-HTML-MARKER--></body></html>")
	if err := os.WriteFile(filepath.Join(dir, "app.html"), appContent, 0644); err != nil {
		t.Fatalf("write app.html: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "site.html"), siteContent, 0644); err != nil {
		t.Fatalf("write site.html: %v", err)
	}
	return dir
}

func TestChannelRoute_MP_StillServesAppHTML(t *testing.T) {
	publicDir := mustWriteBothHTML(t)
	mux := newRoutingMux(t, publicDir)

	req := httptest.NewRequest(http.MethodGet, "/mp/fbif", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "<!--APP-HTML-MARKER-->") {
		t.Fatalf("/mp/fbif should serve app.html; got=%q", rec.Body.String())
	}
}

func TestChannelRoute_FTWithPackID_ServesSiteHTML(t *testing.T) {
	publicDir := mustWriteBothHTML(t)
	mux := newRoutingMux(t, publicDir)

	req := httptest.NewRequest(http.MethodGet, "/ft/default", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<!--SITE-HTML-MARKER-->") {
		t.Fatalf("/ft/default should serve site.html; got=%q", rec.Body.String())
	}
}

func TestChannelRoute_FTWithoutPackID_ServesSiteHTML(t *testing.T) {
	publicDir := mustWriteBothHTML(t)
	mux := newRoutingMux(t, publicDir)

	req := httptest.NewRequest(http.MethodGet, "/ft", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "<!--SITE-HTML-MARKER-->") {
		t.Fatalf("/ft should serve site.html; got=%q", rec.Body.String())
	}
}
