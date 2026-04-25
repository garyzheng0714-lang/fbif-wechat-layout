package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestRulePresetStorePersistsActivePreset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rule-presets.json")
	store := newRulePresetStore(path)

	doc, err := store.upsert(rulePreset{
		ID:   "campaign",
		Name: "Campaign Rule",
		Config: map[string]any{
			"font_size": "16",
		},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if len(doc.Presets) != 2 {
		t.Fatalf("expected default + custom preset, got %d", len(doc.Presets))
	}

	if _, err := store.setActive("campaign"); err != nil {
		t.Fatalf("setActive: %v", err)
	}

	reloaded := newRulePresetStore(path)
	active, err := reloaded.active()
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if active.ID != "campaign" {
		t.Fatalf("active ID = %q, want campaign", active.ID)
	}
	if active.Config["font_size"] != "16" {
		t.Fatalf("font_size = %v, want 16", active.Config["font_size"])
	}
	if active.Config["heading_size"] == "" {
		t.Fatalf("expected default fields to be merged")
	}
}

func TestRulePresetAdminHandlersRequirePassword(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, newRulePresetStore(filepath.Join(t.TempDir(), "rules.json")))

	body, _ := json.Marshal(rulePreset{ID: "test", Name: "Test", Config: defaultRuleConfig()})
	req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without password code = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Password", "secret")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with password code = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
}

func TestRulePresetLoginValidatesPassword(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, newRulePresetStore(filepath.Join(t.TempDir(), "rules.json")))

	req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/login", bytes.NewBufferString(`{"password":"bad"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad password code = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/rules/admin/login", bytes.NewBufferString(`{"password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("good password code = %d, want 200", rec.Code)
	}
}

func TestAdminHandlersRequireConfiguredPassword(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "")
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, newRulePresetStore(filepath.Join(t.TempDir(), "rules.json")))

	req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/login", bytes.NewBufferString(`{"password":"FBIF2026"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("login without configured password code = %d, want 503", rec.Code)
	}
}
