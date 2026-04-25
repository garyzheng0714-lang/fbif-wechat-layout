package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultRulePresetID = "default"

type rulePreset struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Config      map[string]any `json:"config"`
	UpdatedAt   string         `json:"updated_at"`
}

type rulePresetDocument struct {
	ActiveID  string       `json:"active_id"`
	Presets   []rulePreset `json:"presets"`
	UpdatedAt string       `json:"updated_at"`
}

type rulePresetStore struct {
	mu   sync.Mutex
	path string
}

func newRulePresetStore(path string) *rulePresetStore {
	if strings.TrimSpace(path) == "" {
		path = filepath.Join("data", "rule-presets.json")
	}
	return &rulePresetStore{path: path}
}

func defaultRuleConfig() map[string]any {
	return map[string]any{
		"font_size":                "15",
		"heading_size":             "18",
		"text_color":               "#544545",
		"link_color":               "#0070C0",
		"muted_color":              "#888888",
		"line_height":              "1.75",
		"letter_spacing":           "0.034",
		"font_family":              "PingFang SC",
		"footer_enabled":           true,
		"skip_upload":              true,
		"paragraph_margin_x":       "8",
		"paragraph_gap":            "20",
		"caption_font_size":        "12",
		"attribution_font_size":    "15",
		"blockquote_padding_left":  "12",
		"blockquote_border_width":  "3",
		"md_heading_max_chars":     "60",
		"decorative_image_max_px":  "640",
		"more_articles_slots":      "3",
		"banner_overlay_alpha":     "0.47058823529411764",
		"banner_title_x":           "61",
		"banner_title_y":           "92",
		"banner_title_width":       "878",
		"banner_title_box_height":  "116",
		"banner_title_font_size":   "48",
		"banner_title_line_height": "70",
		"banner_title_max_lines":   "2",
	}
}

func defaultRuleDocument() rulePresetDocument {
	now := time.Now().UTC().Format(time.RFC3339)
	return rulePresetDocument{
		ActiveID: defaultRulePresetID,
		Presets: []rulePreset{{
			ID:          defaultRulePresetID,
			Name:        "默认规则",
			Description: "当前 FBIF 公众号排版默认参数",
			Config:      defaultRuleConfig(),
			UpdatedAt:   now,
		}},
		UpdatedAt: now,
	}
}

func mergeRuleDefaults(config map[string]any) map[string]any {
	merged := defaultRuleConfig()
	for k, v := range config {
		merged[k] = v
	}
	return merged
}

func (s *rulePresetStore) readLocked() (rulePresetDocument, error) {
	doc := defaultRuleDocument()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return doc, err
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return defaultRuleDocument(), err
	}
	doc = normalizeRuleDocument(doc)
	return doc, nil
}

func normalizeRuleDocument(doc rulePresetDocument) rulePresetDocument {
	if len(doc.Presets) == 0 {
		return defaultRuleDocument()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	seen := make(map[string]bool)
	out := make([]rulePreset, 0, len(doc.Presets)+1)
	hasDefault := false
	for _, p := range doc.Presets {
		p.ID = sanitizeRulePresetID(p.ID)
		if p.ID == "" || seen[p.ID] {
			continue
		}
		if p.Name == "" {
			p.Name = "未命名规则"
		}
		p.Config = mergeRuleDefaults(p.Config)
		if p.UpdatedAt == "" {
			p.UpdatedAt = now
		}
		seen[p.ID] = true
		if p.ID == defaultRulePresetID {
			hasDefault = true
		}
		out = append(out, p)
	}
	if !hasDefault {
		d := defaultRuleDocument().Presets[0]
		out = append([]rulePreset{d}, out...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID == defaultRulePresetID {
			return true
		}
		if out[j].ID == defaultRulePresetID {
			return false
		}
		return out[i].Name < out[j].Name
	})
	doc.Presets = out
	doc.ActiveID = sanitizeRulePresetID(doc.ActiveID)
	if doc.ActiveID == "" || !seenRulePreset(out, doc.ActiveID) {
		doc.ActiveID = defaultRulePresetID
	}
	if doc.UpdatedAt == "" {
		doc.UpdatedAt = now
	}
	return doc
}

func seenRulePreset(presets []rulePreset, id string) bool {
	for _, p := range presets {
		if p.ID == id {
			return true
		}
	}
	return false
}

func (s *rulePresetStore) writeLocked(doc rulePresetDocument) error {
	doc = normalizeRuleDocument(doc)
	doc.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".rule-presets-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.path)
}

func (s *rulePresetStore) list() (rulePresetDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked()
}

func (s *rulePresetStore) active() (rulePreset, error) {
	doc, err := s.list()
	if err != nil {
		return rulePreset{}, err
	}
	for _, p := range doc.Presets {
		if p.ID == doc.ActiveID {
			return p, nil
		}
	}
	return doc.Presets[0], nil
}

func (s *rulePresetStore) upsert(p rulePreset) (rulePresetDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return doc, err
	}
	if strings.TrimSpace(p.ID) == "" {
		p.ID = makeRulePresetID(p.Name)
	}
	p.ID = sanitizeRulePresetID(p.ID)
	if p.ID == "" {
		p.ID = fmt.Sprintf("rule-%d", time.Now().UnixNano())
	}
	if strings.TrimSpace(p.Name) == "" {
		p.Name = "未命名规则"
	}
	p.Config = mergeRuleDefaults(p.Config)
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	replaced := false
	for i := range doc.Presets {
		if doc.Presets[i].ID == p.ID {
			doc.Presets[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		doc.Presets = append(doc.Presets, p)
	}
	if err := s.writeLocked(doc); err != nil {
		return doc, err
	}
	return normalizeRuleDocument(doc), nil
}

func (s *rulePresetStore) setActive(id string) (rulePresetDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return doc, err
	}
	id = sanitizeRulePresetID(id)
	if !seenRulePreset(doc.Presets, id) {
		return doc, fmt.Errorf("rule preset not found")
	}
	doc.ActiveID = id
	if err := s.writeLocked(doc); err != nil {
		return doc, err
	}
	return normalizeRuleDocument(doc), nil
}

func (s *rulePresetStore) delete(id string) (rulePresetDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return doc, err
	}
	id = sanitizeRulePresetID(id)
	if id == defaultRulePresetID {
		return doc, fmt.Errorf("default preset cannot be deleted")
	}
	next := doc.Presets[:0]
	found := false
	for _, p := range doc.Presets {
		if p.ID == id {
			found = true
			continue
		}
		next = append(next, p)
	}
	if !found {
		return doc, fmt.Errorf("rule preset not found")
	}
	doc.Presets = next
	if doc.ActiveID == id {
		doc.ActiveID = defaultRulePresetID
	}
	if err := s.writeLocked(doc); err != nil {
		return doc, err
	}
	return normalizeRuleDocument(doc), nil
}

var rulePresetIDUnsafe = regexp.MustCompile(`[^a-z0-9_-]+`)

func makeRulePresetID(name string) string {
	id := strings.ToLower(strings.TrimSpace(name))
	id = strings.ReplaceAll(id, " ", "-")
	id = rulePresetIDUnsafe.ReplaceAllString(id, "-")
	id = strings.Trim(id, "-_")
	if id == "" || id == defaultRulePresetID {
		return fmt.Sprintf("rule-%d", time.Now().UnixNano())
	}
	return id
}

func sanitizeRulePresetID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	id = rulePresetIDUnsafe.ReplaceAllString(id, "-")
	return strings.Trim(id, "-_")
}

func adminPassword() string {
	return strings.TrimSpace(os.Getenv("ADMIN_PASSWORD"))
}

func adminPasswordConfigured() bool {
	return adminPassword() != ""
}

func validAdminPassword(input string) bool {
	got := strings.TrimSpace(input)
	want := adminPassword()
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func requireAdminPassword(w http.ResponseWriter, r *http.Request) bool {
	if !adminPasswordConfigured() {
		writeError(w, http.StatusServiceUnavailable, "ADMIN_PASSWORD is not configured")
		return false
	}
	if !validAdminPassword(r.Header.Get("X-Admin-Password")) {
		writeError(w, http.StatusUnauthorized, "admin password required")
		return false
	}
	return true
}

func registerRulePresetHandlers(mux *http.ServeMux, store *rulePresetStore) {
	mux.HandleFunc("GET /api/rules/active", func(w http.ResponseWriter, r *http.Request) {
		preset, err := store.active()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"preset": preset})
	})

	mux.HandleFunc("POST /api/rules/admin/login", func(w http.ResponseWriter, r *http.Request) {
		if !adminPasswordConfigured() {
			writeError(w, http.StatusServiceUnavailable, "规则后台未配置密码")
			return
		}
		var req struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if !validAdminPassword(req.Password) {
			writeError(w, http.StatusUnauthorized, "密码不正确")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/rules/admin/presets", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminPassword(w, r) {
			return
		}
		doc, err := store.list()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, doc)
	})

	mux.HandleFunc("POST /api/rules/admin/presets", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminPassword(w, r) {
			return
		}
		var req rulePreset
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		doc, err := store.upsert(req)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, doc)
	})

	mux.HandleFunc("PUT /api/rules/admin/active", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminPassword(w, r) {
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		doc, err := store.setActive(req.ID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, doc)
	})

	mux.HandleFunc("DELETE /api/rules/admin/presets/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminPassword(w, r) {
			return
		}
		doc, err := store.delete(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, doc)
	})
}
