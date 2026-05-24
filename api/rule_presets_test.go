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
	if len(doc.Presets) != 3 {
		t.Fatalf("expected two account defaults + custom preset, got %d", len(doc.Presets))
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

func TestDefaultRuleStoreIncludesAccountBindings(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))

	doc, err := store.list()
	if err != nil {
		t.Fatalf("list default store: %v", err)
	}

	if len(doc.Accounts) != 2 {
		t.Fatalf("accounts = %d, want 2: %#v", len(doc.Accounts), doc.Accounts)
	}
	if doc.Accounts[0].ID != "fbif" {
		t.Fatalf("first account id = %q, want fbif", doc.Accounts[0].ID)
	}
	if doc.Accounts[0].Name != "FBIF公众号" {
		t.Fatalf("first account name = %q, want FBIF公众号", doc.Accounts[0].Name)
	}
	if doc.Accounts[1].ID != "foodtalks-series" {
		t.Fatalf("second account id = %q, want foodtalks-series", doc.Accounts[1].ID)
	}
	if doc.Accounts[1].Name != "FoodTalks什么是系列" {
		t.Fatalf("second account name = %q, want FoodTalks什么是系列", doc.Accounts[1].Name)
	}
	if doc.Accounts[0].ActiveID == "" || doc.Accounts[1].ActiveID == "" {
		t.Fatalf("accounts must expose active rule ids: %#v", doc.Accounts)
	}
	if doc.Accounts[0].ActiveID == doc.Accounts[1].ActiveID {
		t.Fatalf("default accounts should start with independent rule ids: %#v", doc.Accounts)
	}
}

func TestRuleActiveCanBeSelectedPerAccount(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, store)

	custom := rulePreset{ID: "foodtalks-custom", Name: "FoodTalks Custom", Config: defaultRuleConfig()}
	body, _ := json.Marshal(custom)
	req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
	req.Header.Set("X-Admin-Password", "secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save custom code = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/rules/admin/active", bytes.NewBufferString(`{"account_id":"foodtalks-series","id":"foodtalks-custom"}`))
	req.Header.Set("X-Admin-Password", "secret")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("set foodtalks active code = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/rules/active?account=foodtalks-series", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("foodtalks active code = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Preset rulePreset `json:"preset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal active payload: %v", err)
	}
	if payload.Preset.ID != "foodtalks-custom" {
		t.Fatalf("foodtalks active preset = %q, want foodtalks-custom", payload.Preset.ID)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/rules/active?account=fbif", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fbif active code = %d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal fbif active payload: %v", err)
	}
	if payload.Preset.ID == "foodtalks-custom" {
		t.Fatalf("fbif active preset changed when only FoodTalks was updated")
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
	if len(doc.Presets) != 3 {
		t.Fatalf("legacy facade presets = %d, want account defaults + campaign", len(doc.Presets))
	}
	active, err = reloaded.active()
	if err != nil {
		t.Fatalf("active after reload: %v", err)
	}
	if active.Config["font_size"] != "17" {
		t.Fatalf("active font_size after reload = %v, want 17", active.Config["font_size"])
	}
}

func TestRulePresetStoreWritesRollbackReadableLegacyShadowFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rule-presets.json")
	store := newRulePresetStore(path)
	if _, err := store.upsert(rulePreset{
		ID:     "campaign",
		Name:   "Campaign Rule",
		Config: map[string]any{"font_size": "17"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := store.setActive("campaign"); err != nil {
		t.Fatalf("set active: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read v2 file: %v", err)
	}
	var rollbackDoc rulePresetDocument
	if err := json.Unmarshal(b, &rollbackDoc); err != nil {
		t.Fatalf("old backend unmarshal shape: %v", err)
	}
	if rollbackDoc.ActiveID != "campaign" {
		t.Fatalf("rollback active_id = %q, want campaign", rollbackDoc.ActiveID)
	}
	var found *rulePreset
	for i := range rollbackDoc.Presets {
		if rollbackDoc.Presets[i].ID == "campaign" {
			found = &rollbackDoc.Presets[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("rollback presets missing campaign: %#v", rollbackDoc.Presets)
	}
	if found.Config["font_size"] != "17" {
		t.Fatalf("rollback font_size = %v, want 17", found.Config["font_size"])
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
	accounts, ok := docPayload["accounts"].([]any)
	if !ok || len(accounts) != 2 {
		t.Fatalf("admin list accounts = %#v, want two account bindings", docPayload["accounts"])
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

func TestRulePresetAdminRejectsInvalidConfig(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, newRulePresetStore(filepath.Join(t.TempDir(), "rules.json")))

	cases := []struct {
		name   string
		config map[string]any
	}{
		{
			name: "bad color",
			config: map[string]any{
				"text_color": "red",
			},
		},
		{
			name: "NaN number",
			config: map[string]any{
				"font_size": "NaN",
			},
		},
		{
			name: "font injection",
			config: map[string]any{
				"font_family": "PingFang';<img src=x>",
			},
		},
		{
			name: "unknown field",
			config: map[string]any{
				"font_size": "15",
				"html":      "<img src=x>",
			},
		},
		{
			name: "unknown module",
			config: map[string]any{
				"schema_version": float64(2),
				"modules": map[string]any{
					"surprise": map[string]any{"font_size": "16"},
				},
			},
		},
		{
			name: "field in wrong module",
			config: map[string]any{
				"schema_version": float64(2),
				"modules": map[string]any{
					"workflow": map[string]any{"font_size": "16"},
				},
			},
		},
		{
			name: "duplicate field in modules",
			config: map[string]any{
				"schema_version": float64(2),
				"modules": map[string]any{
					"theme":      map[string]any{"font_size": "16"},
					"supporting": map[string]any{"font_size": "17"},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(rulePreset{
				ID:     "bad-config",
				Name:   "Bad Config",
				Config: tc.config,
			})
			req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Admin-Password", "secret")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("invalid config code = %d, want 400 body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRulePresetStoreCapsSavedVersionsPerRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	store := newRulePresetStore(path)
	for i := 0; i < maxRulePresetVersions+5; i++ {
		if _, err := store.upsert(rulePreset{
			ID:     "campaign",
			Name:   "Campaign Rule",
			Config: map[string]any{"font_size": "16"},
		}); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}

	var raw ruleStoreDocumentV2
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal store: %v", err)
	}
	if got := len(raw.Rules["campaign"].Versions); got != maxRulePresetVersions {
		t.Fatalf("versions = %d, want %d", got, maxRulePresetVersions)
	}
}

func TestRulePresetAdminAcceptsModularConfigAndProjectsFlatLegacyPreset(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "secret")
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, store)

	body, _ := json.Marshal(rulePreset{
		ID:   "modular",
		Name: "Modular",
		Config: map[string]any{
			"schema_version": float64(2),
			"font_size":      "15",
			"modules": map[string]any{
				"theme": map[string]any{
					"font_size": "16",
				},
				"workflow": map[string]any{
					"footer_enabled": false,
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Password", "secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("modular config code = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}

	if _, err := store.setActive("modular"); err != nil {
		t.Fatalf("setActive: %v", err)
	}
	active, err := store.active()
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if active.Config["font_size"] != "16" {
		t.Fatalf("font_size = %v, want modular override 16", active.Config["font_size"])
	}
	if active.Config["footer_enabled"] != false {
		t.Fatalf("footer_enabled = %v, want false", active.Config["footer_enabled"])
	}
}
