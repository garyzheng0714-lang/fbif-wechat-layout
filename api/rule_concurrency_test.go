package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRulePresetConcurrentCASAllowsExactlyOneWriter(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	if _, err := store.upsert(rulePreset{
		ID:         "concurrent-rule",
		Name:       "Concurrent Rule",
		BaselineID: defaultRulePresetID,
		Config:     map[string]any{"font_size": "15"},
	}); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, fontSize := range []string{"16", "17"} {
		fontSize := fontSize
		go func() {
			<-start
			_, err := store.upsertAtRevision(rulePreset{
				ID:         "concurrent-rule",
				Name:       "Concurrent Rule",
				BaselineID: defaultRulePresetID,
				Config:     map[string]any{"font_size": fontSize},
			}, 1)
			results <- err
		}()
	}
	close(start)

	successes := 0
	conflicts := 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
			continue
		}
		var conflict *ruleRevisionConflictError
		if errors.As(err, &conflict) {
			conflicts++
			continue
		}
		t.Fatalf("unexpected concurrent save error: %v", err)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("same base revision must yield one success and one conflict, got success=%d conflict=%d", successes, conflicts)
	}

	doc, err := store.list()
	if err != nil {
		t.Fatal(err)
	}
	for _, preset := range doc.Presets {
		if preset.ID == "concurrent-rule" {
			if preset.Version != 2 {
				t.Fatalf("winning write version = %d, want 2", preset.Version)
			}
			return
		}
	}
	t.Fatal("concurrent-rule missing after winning write")
}

func TestRulePresetHTTPRejectsStaleSaveWithFieldDiff(t *testing.T) {
	t.Setenv("PUBLISH_ADMIN_SERVICE_TOKEN", "secret")
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, store)

	save := func(fontSize string, baseRevision int) *httptest.ResponseRecorder {
		body, err := json.Marshal(rulePreset{
			ID:           "shared-rule",
			Name:         "Shared Rule",
			BaseRevision: ruleRevisionPointer(baseRevision),
			BaselineID:   defaultRulePresetID,
			Config:       map[string]any{"font_size": fontSize},
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Publish-Admin-Token", "secret")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := save("15", 0); rec.Code != http.StatusOK {
		t.Fatalf("initial save = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := save("16", 1); rec.Code != http.StatusOK {
		t.Fatalf("newer admin save = %d body=%s", rec.Code, rec.Body.String())
	}
	stale := save("17", 1)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale save = %d, want 409 body=%s", stale.Code, stale.Body.String())
	}
	var payload struct {
		Code             string   `json:"code"`
		ExpectedRevision int      `json:"expected_revision"`
		CurrentRevision  int      `json:"current_revision"`
		ChangedPaths     []string `json:"changed_paths"`
	}
	if err := json.Unmarshal(stale.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "revision_conflict" || payload.ExpectedRevision != 1 || payload.CurrentRevision != 2 {
		t.Fatalf("unexpected conflict payload: %+v", payload)
	}
	if len(payload.ChangedPaths) != 1 || payload.ChangedPaths[0] != "config.font_size" {
		t.Fatalf("field diff = %#v, want config.font_size", payload.ChangedPaths)
	}

	doc, err := store.list()
	if err != nil {
		t.Fatal(err)
	}
	for _, preset := range doc.Presets {
		if preset.ID == "shared-rule" {
			if preset.Version != 2 || preset.Config["font_size"] != "16" {
				t.Fatalf("stale writer changed winner: version=%d config=%#v", preset.Version, preset.Config)
			}
			return
		}
	}
	t.Fatal("shared-rule missing")
}

func TestRuleBindingHTTPRejectsStaleRevision(t *testing.T) {
	t.Setenv("PUBLISH_ADMIN_SERVICE_TOKEN", "secret")
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, store)

	setActive := func(id string, baseRevision int) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]any{
			"account_id":    wowFoodRuleAccountID,
			"id":            id,
			"base_revision": baseRevision,
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/rules/admin/active", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Publish-Admin-Token", "secret")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := setActive(defaultRulePresetID, 1); rec.Code != http.StatusOK {
		t.Fatalf("first binding update = %d body=%s", rec.Code, rec.Body.String())
	}
	stale := setActive(wowFoodDefaultRulePresetID, 1)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale binding update = %d, want 409 body=%s", stale.Code, stale.Body.String())
	}
	var payload struct {
		Code            string `json:"code"`
		CurrentRevision int    `json:"current_revision"`
		CurrentActiveID string `json:"current_active_id"`
	}
	if err := json.Unmarshal(stale.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "binding_revision_conflict" || payload.CurrentRevision != 2 || payload.CurrentActiveID != defaultRulePresetID {
		t.Fatalf("unexpected binding conflict payload: %+v", payload)
	}
	active, err := store.activeForAccount(wowFoodRuleAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != defaultRulePresetID {
		t.Fatalf("stale writer changed active rule to %q", active.ID)
	}
}

func TestSavingActiveRuleAdvancesBindingRevision(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	doc, err := store.upsertAtRevision(rulePreset{
		ID:         defaultRulePresetID,
		Name:       "FBIF默认规则",
		BaselineID: defaultRulePresetID,
		Config:     map[string]any{"font_size": "16"},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range doc.Accounts {
		if binding.ID != defaultRuleAccountID {
			continue
		}
		if binding.ActiveRevision != 2 {
			t.Fatalf("active rule save binding revision = %d, want 2", binding.ActiveRevision)
		}
		if _, err := store.setActiveForAccountAtRevision(defaultRuleAccountID, defaultRulePresetID, 1); err == nil {
			t.Fatal("stale binding revision must conflict after active rule save")
		} else {
			var conflict *accountRevisionConflictError
			if !errors.As(err, &conflict) || conflict.CurrentRevision != 2 {
				t.Fatalf("unexpected stale binding error: %v", err)
			}
		}
		return
	}
	t.Fatal("fbif binding missing")
}

func TestRuleWriteHandlersRequireRevisionPrecondition(t *testing.T) {
	t.Setenv("PUBLISH_ADMIN_SERVICE_TOKEN", "secret")
	mux := http.NewServeMux()
	registerRulePresetHandlers(mux, newRulePresetStore(filepath.Join(t.TempDir(), "rules.json")))

	body := []byte(`{"id":"without-revision","name":"Without Revision","baseline_id":"default","config":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/rules/admin/presets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Publish-Admin-Token", "secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("save without revision = %d, want 428 body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/rules/admin/active", bytes.NewBufferString(`{"account_id":"fbif","id":"default"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Publish-Admin-Token", "secret")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("binding without revision = %d, want 428 body=%s", rec.Code, rec.Body.String())
	}
}

func TestRuleStoreV3MigratesBindingRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	doc := defaultRuleStoreDocumentV2()
	doc.SchemaVersion = 3
	account := doc.Accounts[defaultRuleAccountID]
	account.Revision = 0
	doc.Accounts[defaultRuleAccountID] = account
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}

	migrated, err := newRulePresetStore(path).readStoreLocked()
	if err != nil {
		t.Fatal(err)
	}
	if migrated.SchemaVersion != currentRuleStoreSchemaVersion {
		t.Fatalf("schema version = %d, want %d", migrated.SchemaVersion, currentRuleStoreSchemaVersion)
	}
	if migrated.Accounts[defaultRuleAccountID].Revision != 1 {
		t.Fatalf("migrated binding revision = %d, want 1", migrated.Accounts[defaultRuleAccountID].Revision)
	}
}
