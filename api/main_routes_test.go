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

func TestStaticCache_VersionedJSIsImmutable(t *testing.T) {
	publicDir := mustWriteStaticAsset(t, "js/engine.js", []byte("export {};"))
	handler := cacheStatic(http.FileServer(http.Dir(publicDir)))
	req := httptest.NewRequest(http.MethodGet, "/js/engine.js?v=commit123", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != versionedAssetCacheControl {
		t.Fatalf("Cache-Control = %q, want %q", got, versionedAssetCacheControl)
	}
}

func TestStaticCache_DevVersionedJSStaysNoCache(t *testing.T) {
	publicDir := mustWriteStaticAsset(t, "js/engine.js", []byte("export {};"))
	handler := cacheStatic(http.FileServer(http.Dir(publicDir)))
	req := httptest.NewRequest(http.MethodGet, "/js/engine.js?v=dev", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != appShellCacheControl {
		t.Fatalf("Cache-Control = %q, want %q", got, appShellCacheControl)
	}
}

func TestStaticCache_BareNestedJSStaysNoCache(t *testing.T) {
	publicDir := mustWriteStaticAsset(t, "js/parser.js", []byte("export {};"))
	handler := cacheStatic(http.FileServer(http.Dir(publicDir)))
	req := httptest.NewRequest(http.MethodGet, "/js/parser.js", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != appShellCacheControl {
		t.Fatalf("Cache-Control = %q, want %q", got, appShellCacheControl)
	}
}

func TestStaticCache_VersionFileStaysNoCache(t *testing.T) {
	publicDir := mustWriteStaticAsset(t, "version.json", []byte(`{"build":"test"}`))
	handler := cacheStatic(http.FileServer(http.Dir(publicDir)))
	req := httptest.NewRequest(http.MethodGet, "/version.json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != appShellCacheControl {
		t.Fatalf("Cache-Control = %q, want %q", got, appShellCacheControl)
	}
}

func TestUnknownAPIRouteReturnsJSON404InsteadOfStaticHTML(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/new-route-missing", nil)
	rec := httptest.NewRecorder()
	handleUnknownAPI(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if !strings.Contains(rec.Body.String(), `"code":"api_not_found"`) {
		t.Fatalf("unexpected JSON error body: %s", rec.Body.String())
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "<html") {
		t.Fatalf("unknown API must never return static HTML: %s", rec.Body.String())
	}
}

func mustWriteStaticAsset(t *testing.T, name string, content []byte) string {
	t.Helper()
	dir := t.TempDir()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir asset dir: %v", err)
	}
	if err := os.WriteFile(full, content, 0644); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	return dir
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
