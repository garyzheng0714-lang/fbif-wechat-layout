package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestRulePresetStoreMigratesLegacyV1FileToV2OnWriteKeepingLegacyFacade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rule-presets.json")
	legacy := `{
  "active_id": "campaign",
  "presets": [
    {
      "id": "campaign",
      "name": "Campaign Rule",
      "config": { "font_size": "16" },
      "updated_at": "2026-05-22T00:00:00Z"
    }
  ],
  "updated_at": "2026-05-22T00:00:00Z"
}`
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy fixture: %v", err)
	}

	store := newRulePresetStore(path)
	active, err := store.active()
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if active.ID != "campaign" {
		t.Fatalf("active ID = %q, want campaign", active.ID)
	}
	if active.Config["font_size"] != "16" {
		t.Fatalf("font_size = %v, want 16", active.Config["font_size"])
	}

	if _, err := store.upsert(rulePreset{
		ID:     "campaign",
		Name:   "Campaign Rule",
		Config: map[string]any{"font_size": "17"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var raw map[string]any
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migrated file: %v", err)
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal migrated file: %v", err)
	}
	if raw["schema_version"] != float64(2) {
		t.Fatalf("schema_version = %v, want 2", raw["schema_version"])
	}
	if _, ok := raw["accounts"]; !ok {
		t.Fatalf("migrated file missing accounts")
	}
	if _, ok := raw["rules"]; !ok {
		t.Fatalf("migrated file missing rules")
	}

	reloaded := newRulePresetStore(path)
	doc, err := reloaded.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if doc.ActiveID != "campaign" {
		t.Fatalf("legacy facade active_id = %q, want campaign", doc.ActiveID)
	}
	if len(doc.Presets) != 2 {
		t.Fatalf("legacy facade presets = %d, want default + campaign", len(doc.Presets))
	}
	active, err = reloaded.active()
	if err != nil {
		t.Fatalf("active after reload: %v", err)
	}
	if active.Config["font_size"] != "17" {
		t.Fatalf("active font_size after reload = %v, want 17", active.Config["font_size"])
	}
}

func TestRulePresetHandlersKeepLegacyResponseShapeOnV2Store(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	path := filepath.Join(t.TempDir(), "rules.json")
	store := newRulePresetStore(path)
	if _, err := store.upsert(rulePreset{
		ID:     "campaign",
		Name:   "Campaign Rule",
		Config: map[string]any{"font_size": "16"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, store)

	req := httptest.NewRequest(http.MethodGet, "/api/rules/admin/presets", nil)
	req.Header.Set("X-Admin-Password", "secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list code = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	var docPayload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &docPayload); err != nil {
		t.Fatalf("unmarshal admin list: %v", err)
	}
	if _, ok := docPayload["active_id"]; !ok {
		t.Fatalf("admin list missing active_id")
	}
	if _, ok := docPayload["presets"]; !ok {
		t.Fatalf("admin list missing presets")
	}
	if _, ok := docPayload["accounts"]; ok {
		t.Fatalf("admin list leaked v2 accounts")
	}
	if _, ok := docPayload["rules"]; ok {
		t.Fatalf("admin list leaked v2 rules")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/rules/active", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("active code = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	var activePayload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &activePayload); err != nil {
		t.Fatalf("unmarshal active: %v", err)
	}
	if _, ok := activePayload["preset"]; !ok {
		t.Fatalf("active response missing preset")
	}
	if _, ok := activePayload["account"]; ok {
		t.Fatalf("active response leaked account")
	}
	if _, ok := activePayload["rules"]; ok {
		t.Fatalf("active response leaked rules")
	}
}
