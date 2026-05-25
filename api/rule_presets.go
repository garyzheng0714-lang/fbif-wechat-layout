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
const defaultRuleAccountID = "fbif"
const maxRulePresetVersions = 20

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

type rulePresetVersion struct {
	Version   int            `json:"version"`
	Config    map[string]any `json:"config"`
	CreatedAt string         `json:"created_at"`
}

type ruleDefinition struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Versions    []rulePresetVersion `json:"versions"`
	UpdatedAt   string              `json:"updated_at"`
}

type ruleAccount struct {
	Name              string `json:"name"`
	ActiveRuleID      string `json:"active_rule_id"`
	ActiveRuleVersion int    `json:"active_rule_version"`
	UpdatedAt         string `json:"updated_at"`
}

type ruleStoreDocumentV2 struct {
	SchemaVersion int                       `json:"schema_version"`
	Accounts      map[string]ruleAccount    `json:"accounts"`
	Rules         map[string]ruleDefinition `json:"rules"`
	ActiveID      string                    `json:"active_id,omitempty"`
	Presets       []rulePreset              `json:"presets,omitempty"`
	UpdatedAt     string                    `json:"updated_at"`
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
		"font_size":                   "15",
		"heading_size":                "18",
		"text_color":                  "#544545",
		"link_color":                  "#0070C0",
		"muted_color":                 "#888888",
		"line_height":                 "1.75",
		"letter_spacing":              "0.034",
		"font_family":                 "PingFang SC",
		"footer_enabled":              true,
		"skip_upload":                 true,
		"paragraph_margin_x":          "8",
		"paragraph_gap":               "20",
		"caption_font_size":           "12",
		"attribution_font_size":       "15",
		"blockquote_padding_left":     "12",
		"blockquote_border_width":     "3",
		"md_heading_max_chars":        "60",
		"decorative_image_max_px":     "640",
		"more_articles_enabled":       true,
		"more_articles_slots":         "3",
		"more_articles_default_cards": defaultMoreArticleCards(),
		"banner_overlay_alpha":        "0.47058823529411764",
		"banner_title_x":              "61",
		"banner_title_y":              "92",
		"banner_title_width":          "878",
		"banner_title_box_height":     "116",
		"banner_title_font_size":      "48",
		"banner_title_line_height":    "70",
		"banner_title_max_lines":      "2",
	}
}

func defaultMoreArticleCards() []any {
	return []any{
		map[string]any{
			"href":   "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=2652909589&idx=1&sn=d0bdeacee2b1ba7f03e48b38f91bbefa&scene=21",
			"imgurl": "https://mmbiz.qpic.cn/sz_mmbiz_jpg/6JHhlvvjTRHibhuMnzJTuvhqr6Neq7ib00icPLXRyof5H9ARUnw9eWIshgyHibyfVXtgSRkWmOP2MWZjHLLbFec9VAFSYH6ZsarKf2LUC4ONEkE/0?wx_fmt=jpeg",
			"title":  "没想到！英国人把豆腐做到英国第一，还像卖衣服一样出XXL超大号！",
		},
		map[string]any{
			"href":   "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=2652916388&idx=1&sn=b5605734c9c27ce2f8d54a6534b6543d&scene=21",
			"imgurl": "https://mmbiz.qpic.cn/sz_mmbiz_jpg/6JHhlvvjTREtc1dUPY9VokWQTtQsdevBIcfoMMavrQNSVFoce8gDOtTqlWhciadMP6VJwzErl7HnzECBECMMILEXt653GCeub3nDZ8VrvTsM/0?wx_fmt=jpeg",
			"title":  "做了20年糖，她把豆腐棒放在鸡胸肉旁边，5年卖出1亿根",
		},
		map[string]any{
			"href":   "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=2652906407&idx=1&sn=7ede62b74d3a5750c2c95ca40c216290&scene=21",
			"imgurl": "https://mmbiz.qpic.cn/sz_mmbiz_jpg/6JHhlvvjTRGEHDwCvfSmEXO9VBXpibZHE3ZqLYicsIyhfiajfq9MNduKq03KbDeqU0iaj3WEQeBEY2j0mSCetzD1Jdic1iciaL7M1KZjHu8x5TLdmM/0?wx_fmt=jpeg",
			"title":  "日本最牛包子铺，单店年销2329万，怎么做到的？",
		},
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

func defaultRuleStoreDocumentV2() ruleStoreDocumentV2 {
	now := time.Now().UTC().Format(time.RFC3339)
	return ruleStoreDocumentV2{
		SchemaVersion: 2,
		Accounts: map[string]ruleAccount{
			defaultRuleAccountID: {
				Name:              "FBIF 公众号排版",
				ActiveRuleID:      defaultRulePresetID,
				ActiveRuleVersion: 1,
				UpdatedAt:         now,
			},
		},
		Rules: map[string]ruleDefinition{
			defaultRulePresetID: {
				ID:          defaultRulePresetID,
				Name:        "默认规则",
				Description: "当前 FBIF 公众号排版默认参数",
				Versions: []rulePresetVersion{{
					Version:   1,
					Config:    defaultRuleConfig(),
					CreatedAt: now,
				}},
				UpdatedAt: now,
			},
		},
		UpdatedAt: now,
	}
}

func mergeRuleDefaults(config map[string]any) map[string]any {
	merged := defaultRuleConfig()
	for k, v := range flattenRuleConfig(config) {
		merged[k] = v
	}
	return merged
}

func (s *rulePresetStore) readStoreLocked() (ruleStoreDocumentV2, error) {
	doc := defaultRuleStoreDocumentV2()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return doc, err
	}

	var probe struct {
		SchemaVersion int             `json:"schema_version"`
		Accounts      json.RawMessage `json:"accounts"`
		Rules         json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return defaultRuleStoreDocumentV2(), err
	}
	if probe.SchemaVersion == 2 || len(probe.Accounts) > 0 || len(probe.Rules) > 0 {
		if err := json.Unmarshal(b, &doc); err != nil {
			return defaultRuleStoreDocumentV2(), err
		}
		return normalizeRuleStoreDocument(doc), nil
	}

	var legacy rulePresetDocument
	if err := json.Unmarshal(b, &legacy); err != nil {
		return defaultRuleStoreDocumentV2(), err
	}
	return legacyDocumentToStore(legacy), nil
}

func (s *rulePresetStore) readLocked() (rulePresetDocument, error) {
	doc, err := s.readStoreLocked()
	if err != nil {
		return rulePresetDocument{}, err
	}
	return legacyDocumentFromStore(doc), nil
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

func legacyDocumentToStore(doc rulePresetDocument) ruleStoreDocumentV2 {
	doc = normalizeRuleDocument(doc)
	now := time.Now().UTC().Format(time.RFC3339)
	out := ruleStoreDocumentV2{
		SchemaVersion: 2,
		Accounts: map[string]ruleAccount{
			defaultRuleAccountID: {
				Name:              "FBIF 公众号排版",
				ActiveRuleID:      doc.ActiveID,
				ActiveRuleVersion: 1,
				UpdatedAt:         doc.UpdatedAt,
			},
		},
		Rules:     make(map[string]ruleDefinition, len(doc.Presets)),
		UpdatedAt: doc.UpdatedAt,
	}
	if out.UpdatedAt == "" {
		out.UpdatedAt = now
	}
	for _, p := range doc.Presets {
		updatedAt := p.UpdatedAt
		if updatedAt == "" {
			updatedAt = now
		}
		out.Rules[p.ID] = ruleDefinition{
			ID:          p.ID,
			Name:        p.Name,
			Description: p.Description,
			Versions: []rulePresetVersion{{
				Version:   1,
				Config:    mergeRuleDefaults(p.Config),
				CreatedAt: updatedAt,
			}},
			UpdatedAt: updatedAt,
		}
	}
	return normalizeRuleStoreDocument(out)
}

func normalizeRuleStoreDocument(doc ruleStoreDocumentV2) ruleStoreDocumentV2 {
	now := time.Now().UTC().Format(time.RFC3339)
	if doc.Rules == nil {
		doc.Rules = map[string]ruleDefinition{}
	}
	normalizedRules := make(map[string]ruleDefinition, len(doc.Rules)+1)
	for id, rule := range doc.Rules {
		rule.ID = sanitizeRulePresetID(firstNonEmpty(rule.ID, id))
		if rule.ID == "" {
			continue
		}
		if rule.Name == "" {
			rule.Name = "未命名规则"
		}
		if len(rule.Versions) == 0 {
			rule.Versions = []rulePresetVersion{{
				Version:   1,
				Config:    defaultRuleConfig(),
				CreatedAt: now,
			}}
		}
		seenVersions := map[int]bool{}
		versions := make([]rulePresetVersion, 0, len(rule.Versions))
		nextVersion := 1
		for _, version := range rule.Versions {
			if version.Version <= 0 || seenVersions[version.Version] {
				for seenVersions[nextVersion] {
					nextVersion++
				}
				version.Version = nextVersion
			}
			seenVersions[version.Version] = true
			if version.CreatedAt == "" {
				version.CreatedAt = now
			}
			version.Config = mergeRuleDefaults(version.Config)
			versions = append(versions, version)
		}
		sort.SliceStable(versions, func(i, j int) bool {
			return versions[i].Version < versions[j].Version
		})
		if len(versions) > maxRulePresetVersions {
			versions = versions[len(versions)-maxRulePresetVersions:]
		}
		rule.Versions = versions
		if rule.UpdatedAt == "" {
			rule.UpdatedAt = latestRuleVersion(rule).CreatedAt
		}
		normalizedRules[rule.ID] = rule
	}
	if _, ok := normalizedRules[defaultRulePresetID]; !ok {
		d := defaultRuleStoreDocumentV2().Rules[defaultRulePresetID]
		normalizedRules[defaultRulePresetID] = d
	}
	doc.SchemaVersion = 2
	doc.Rules = normalizedRules
	if doc.Accounts == nil {
		doc.Accounts = map[string]ruleAccount{}
	}
	account := doc.Accounts[defaultRuleAccountID]
	if account.Name == "" {
		account.Name = "FBIF 公众号排版"
	}
	account.ActiveRuleID = sanitizeRulePresetID(account.ActiveRuleID)
	if account.ActiveRuleID == "" || doc.Rules[account.ActiveRuleID].ID == "" {
		account.ActiveRuleID = defaultRulePresetID
	}
	if !ruleHasVersion(doc.Rules[account.ActiveRuleID], account.ActiveRuleVersion) {
		account.ActiveRuleVersion = latestRuleVersion(doc.Rules[account.ActiveRuleID]).Version
	}
	if account.UpdatedAt == "" {
		account.UpdatedAt = now
	}
	doc.Accounts[defaultRuleAccountID] = account
	if doc.UpdatedAt == "" {
		doc.UpdatedAt = now
	}
	return doc
}

func legacyDocumentFromStore(doc ruleStoreDocumentV2) rulePresetDocument {
	doc = normalizeRuleStoreDocument(doc)
	account := doc.Accounts[defaultRuleAccountID]
	ids := make([]string, 0, len(doc.Rules))
	for id := range doc.Rules {
		ids = append(ids, id)
	}
	sort.SliceStable(ids, func(i, j int) bool {
		if ids[i] == defaultRulePresetID {
			return true
		}
		if ids[j] == defaultRulePresetID {
			return false
		}
		return doc.Rules[ids[i]].Name < doc.Rules[ids[j]].Name
	})
	presets := make([]rulePreset, 0, len(ids))
	for _, id := range ids {
		rule := doc.Rules[id]
		version := latestRuleVersion(rule)
		if id == account.ActiveRuleID {
			version = ruleVersionOrLatest(rule, account.ActiveRuleVersion)
		}
		presets = append(presets, rulePreset{
			ID:          rule.ID,
			Name:        rule.Name,
			Description: rule.Description,
			Config:      mergeRuleDefaults(version.Config),
			UpdatedAt:   rule.UpdatedAt,
		})
	}
	return rulePresetDocument{
		ActiveID:  account.ActiveRuleID,
		Presets:   presets,
		UpdatedAt: doc.UpdatedAt,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func latestRuleVersion(rule ruleDefinition) rulePresetVersion {
	if len(rule.Versions) == 0 {
		return rulePresetVersion{Version: 1, Config: defaultRuleConfig(), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	latest := rule.Versions[0]
	for _, version := range rule.Versions[1:] {
		if version.Version > latest.Version {
			latest = version
		}
	}
	return latest
}

func ruleVersionOrLatest(rule ruleDefinition, versionNumber int) rulePresetVersion {
	for _, version := range rule.Versions {
		if version.Version == versionNumber {
			return version
		}
	}
	return latestRuleVersion(rule)
}

func ruleHasVersion(rule ruleDefinition, versionNumber int) bool {
	for _, version := range rule.Versions {
		if version.Version == versionNumber {
			return true
		}
	}
	return false
}

func seenRulePreset(presets []rulePreset, id string) bool {
	for _, p := range presets {
		if p.ID == id {
			return true
		}
	}
	return false
}

func (s *rulePresetStore) writeStoreLocked(doc ruleStoreDocumentV2) error {
	doc = normalizeRuleStoreDocument(doc)
	doc.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	shadow := legacyDocumentFromStore(doc)
	doc.ActiveID = shadow.ActiveID
	doc.Presets = shadow.Presets
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

func (s *rulePresetStore) writeLocked(doc rulePresetDocument) error {
	return s.writeStoreLocked(legacyDocumentToStore(doc))
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
	doc, err := s.readStoreLocked()
	if err != nil {
		return legacyDocumentFromStore(doc), err
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
	rule := doc.Rules[p.ID]
	nextVersion := 1
	if rule.ID != "" {
		nextVersion = latestRuleVersion(rule).Version + 1
	}
	rule.ID = p.ID
	rule.Name = p.Name
	rule.Description = p.Description
	rule.Versions = append(rule.Versions, rulePresetVersion{
		Version:   nextVersion,
		Config:    p.Config,
		CreatedAt: p.UpdatedAt,
	})
	rule.UpdatedAt = p.UpdatedAt
	doc.Rules[p.ID] = rule
	account := doc.Accounts[defaultRuleAccountID]
	if account.ActiveRuleID == p.ID {
		account.ActiveRuleVersion = nextVersion
		account.UpdatedAt = p.UpdatedAt
		doc.Accounts[defaultRuleAccountID] = account
	}
	if err := s.writeStoreLocked(doc); err != nil {
		return legacyDocumentFromStore(doc), err
	}
	return legacyDocumentFromStore(doc), nil
}

func (s *rulePresetStore) setActive(id string) (rulePresetDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readStoreLocked()
	if err != nil {
		return legacyDocumentFromStore(doc), err
	}
	id = sanitizeRulePresetID(id)
	rule, ok := doc.Rules[id]
	if !ok || rule.ID == "" {
		return legacyDocumentFromStore(doc), fmt.Errorf("rule preset not found")
	}
	account := doc.Accounts[defaultRuleAccountID]
	account.ActiveRuleID = id
	account.ActiveRuleVersion = latestRuleVersion(rule).Version
	account.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	doc.Accounts[defaultRuleAccountID] = account
	if err := s.writeStoreLocked(doc); err != nil {
		return legacyDocumentFromStore(doc), err
	}
	return legacyDocumentFromStore(doc), nil
}

func (s *rulePresetStore) delete(id string) (rulePresetDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readStoreLocked()
	if err != nil {
		return legacyDocumentFromStore(doc), err
	}
	id = sanitizeRulePresetID(id)
	if id == defaultRulePresetID {
		return legacyDocumentFromStore(doc), fmt.Errorf("default preset cannot be deleted")
	}
	if _, ok := doc.Rules[id]; !ok {
		return legacyDocumentFromStore(doc), fmt.Errorf("rule preset not found")
	}
	delete(doc.Rules, id)
	account := doc.Accounts[defaultRuleAccountID]
	if account.ActiveRuleID == id {
		account.ActiveRuleID = defaultRulePresetID
		account.ActiveRuleVersion = latestRuleVersion(doc.Rules[defaultRulePresetID]).Version
		account.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		doc.Accounts[defaultRuleAccountID] = account
	}
	if err := s.writeStoreLocked(doc); err != nil {
		return legacyDocumentFromStore(doc), err
	}
	return legacyDocumentFromStore(doc), nil
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
		r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := validateRulePresetPayload(req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
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
