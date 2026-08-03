package main

import (
	"net/http"
	"net/url"
	"strings"
)

// withEditorAuthGate prevents unauthenticated users from receiving an editor
// shell. The browser-side session check remains as defense in depth for cached
// or directly served HTML, but the server is the authoritative access gate.
func withEditorAuthGate(service *authService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !editorEntryPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		if _, err := service.currentUser(r); err == nil {
			next.ServeHTTP(w, r)
			return
		}

		returnTo := r.URL.RequestURI()
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			returnTo = "/mp/fbif"
		}
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Login-Required", "1")
		http.Redirect(
			w,
			r,
			"/login.html?return_to="+url.QueryEscape(returnTo),
			http.StatusFound,
		)
	})
}

func editorEntryPath(path string) bool {
	switch path {
	case "/", "/index.html", "/app", "/app.html", "/site.html", "/mp", "/ft":
		return true
	default:
		return strings.HasPrefix(path, "/mp/") || strings.HasPrefix(path, "/ft/")
	}
}
