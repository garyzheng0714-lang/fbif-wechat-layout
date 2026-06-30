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
const foodTalksRuleAccountID = "foodtalks-series"
const foodTalksDefaultRulePresetID = "foodtalks-series-default"
const foodTalksSiteRuleAccountID = "ft-default"
const foodTalksSiteDefaultRulePresetID = "ft-default-v1"
const foodTalksSeriesSiteRuleAccountID = "ft-series"          // 网站渠道「什么是系列」= registry pack.id
const foodTalksSeriesSiteDefaultRulePresetID = "ft-series-v1" // 网站系列独立 preset
const wowFoodRuleAccountID = "wowfood"                        // = registry pack.id
const wowFoodDefaultRulePresetID = "wowfood-default"          // WowFood爆品超市 独立 preset
const maxRulePresetVersions = 20

type rulePreset struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Config      map[string]any `json:"config"`
	Behavior    map[string]any `json:"behavior,omitempty"`
	UpdatedAt   string         `json:"updated_at"`
}

type rulePresetDocument struct {
	ActiveID  string               `json:"active_id"`
	Presets   []rulePreset         `json:"presets"`
	Accounts  []ruleAccountBinding `json:"-"`
	UpdatedAt string               `json:"updated_at"`
}

type ruleAccountBinding struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ActiveID  string `json:"active_id"`
	UpdatedAt string `json:"updated_at"`
}

type rulePresetVersion struct {
	Version   int            `json:"version"`
	Config    map[string]any `json:"config"`
	Behavior  map[string]any `json:"behavior,omitempty"`
	CreatedAt string         `json:"created_at"`
}

type ruleDefinition struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Versions    []rulePresetVersion `json:"versions"`
	UpdatedAt   string              `json:"updated_at"`
	// BuiltinVersion 标记该内置默认 preset 对应的代码版本。store 加载时若持久化值
	// 落后于代码 currentBuiltinRuleVersion，则用代码默认覆盖（修复历史持久化旧数据 +
	// 防代码更新被旧持久化挡住）。仅作用于 defaultRuleDefinitions 里的固定 id；
	// 用户自建的新 id preset 不在其中，BuiltinVersion 留 0、永不被覆盖。
	BuiltinVersion int `json:"builtin_version,omitempty"`
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

func mergeRuleDefaults(config map[string]any) map[string]any {
	merged := defaultRuleConfig()
	for k, v := range flattenRuleConfig(config) {
		merged[k] = v
	}
	// 透传 flattenRuleConfig 不处理的结构化嵌套字段，否则会被丢弃：
	//   - 顶层 image（网站渲染器图片配置）
	//   - modules 下不在 ruleModuleOrder 的结构化 module（mctoc 目录、fbif_footer 页脚库）
	// 这些是「规则数据」而非可展平的扁平参数，必须整体保留传给前端渲染器。
	if img, ok := config["image"]; ok {
		merged["image"] = img
	}
	if mods, ok := config["modules"].(map[string]any); ok {
		mergedMods, _ := merged["modules"].(map[string]any)
		if mergedMods == nil {
			mergedMods = map[string]any{}
		}
		for mk, mv := range mods {
			isFlatModule := false
			for _, fm := range ruleModuleOrder {
				if fm == mk {
					isFlatModule = true
					break
				}
			}
			if !isFlatModule {
				mergedMods[mk] = mv
			}
		}
		if len(mergedMods) > 0 {
			merged["modules"] = mergedMods
		}
	}
	mods, _ := merged["modules"].(map[string]any)
	srcMods, _ := config["modules"].(map[string]any)
	if mods != nil && (srcMods == nil || srcMods["article_footer_defaults"] == nil) {
		mods["article_footer_defaults"] = disabledArticleFooterDefaultsModule()
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
		p.Behavior = mergeRuleBehavior(p.Behavior)
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
		Accounts:      defaultRuleAccounts(now),
		Rules:         make(map[string]ruleDefinition, len(doc.Presets)),
		UpdatedAt:     doc.UpdatedAt,
	}
	if out.UpdatedAt == "" {
		out.UpdatedAt = now
	}
	fbifAccount := out.Accounts[defaultRuleAccountID]
	fbifAccount.ActiveRuleID = doc.ActiveID
	fbifAccount.UpdatedAt = doc.UpdatedAt
	if fbifAccount.UpdatedAt == "" {
		fbifAccount.UpdatedAt = now
	}
	out.Accounts[defaultRuleAccountID] = fbifAccount
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
				Behavior:  mergeRuleBehavior(p.Behavior),
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
				Behavior:  defaultRuleBehavior(),
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
			version.Behavior = mergeRuleBehavior(version.Behavior)
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
	for id, def := range defaultRuleDefinitions(now) {
		existing, ok := normalizedRules[id]
		if !ok {
			normalizedRules[id] = def
			continue
		}
		// 内置默认 preset 的代码版本领先持久化 → 用代码默认覆盖（修复历史持久化旧数据，
		// 并让后续对内置默认的代码更新能落地已部署环境）。只覆盖 defaultRuleDefinitions 里
		// 的固定 id；用户自建 preset 的 id 不在其中，永不被波及。账号 active 绑定不在此处改
		// （下方 accounts 循环会把失效的 ActiveRuleVersion 校正到最新）。
		if existing.BuiltinVersion < def.BuiltinVersion {
			normalizedRules[id] = def
		}
	}
	doc.SchemaVersion = 2
	doc.Rules = normalizedRules
	if doc.Accounts == nil {
		doc.Accounts = map[string]ruleAccount{}
	}
	for accountID, defaultAccount := range defaultRuleAccounts(now) {
		account := doc.Accounts[accountID]
		if account.Name == "" {
			account.Name = defaultAccount.Name
		}
		account.ActiveRuleID = sanitizeRulePresetID(account.ActiveRuleID)
		if account.ActiveRuleID == "" || doc.Rules[account.ActiveRuleID].ID == "" {
			account.ActiveRuleID = defaultAccount.ActiveRuleID
		}
		if !ruleHasVersion(doc.Rules[account.ActiveRuleID], account.ActiveRuleVersion) {
			account.ActiveRuleVersion = latestRuleVersion(doc.Rules[account.ActiveRuleID]).Version
		}
		if account.UpdatedAt == "" {
			account.UpdatedAt = now
		}
		doc.Accounts[accountID] = account
	}
	if doc.UpdatedAt == "" {
		doc.UpdatedAt = now
	}
	return doc
}

func accountBindingsFromStore(doc ruleStoreDocumentV2) []ruleAccountBinding {
	// 展示顺序读 accountCatalog（单一真相源），不再写死切片（消灭 D4）。
	order := catalogOrderIDs()
	for id := range doc.Accounts {
		found := false
		for _, known := range order {
			if id == known {
				found = true
				break
			}
		}
		if !found {
			order = append(order, id)
		}
	}
	out := make([]ruleAccountBinding, 0, len(order))
	for _, id := range order {
		account, ok := doc.Accounts[id]
		if !ok {
			continue
		}
		out = append(out, ruleAccountBinding{
			ID:        id,
			Name:      account.Name,
			ActiveID:  account.ActiveRuleID,
			UpdatedAt: account.UpdatedAt,
		})
	}
	return out
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
			Behavior:    mergeRuleBehavior(version.Behavior),
			UpdatedAt:   rule.UpdatedAt,
		})
	}
	return rulePresetDocument{
		ActiveID:  account.ActiveRuleID,
		Presets:   presets,
		Accounts:  accountBindingsFromStore(doc),
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
		return rulePresetVersion{Version: 1, Config: defaultRuleConfig(), Behavior: defaultRuleBehavior(), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
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

func (s *rulePresetStore) list() (rulePresetDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked()
}

func (s *rulePresetStore) active() (rulePreset, error) {
	return s.activeForAccount(defaultRuleAccountID)
}

func (s *rulePresetStore) activeForAccount(accountID string) (rulePreset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readStoreLocked()
	if err != nil {
		return rulePreset{}, err
	}
	account := doc.Accounts[sanitizeRuleAccountID(accountID)]
	if account.ActiveRuleID == "" {
		account = doc.Accounts[defaultRuleAccountID]
	}
	rule := doc.Rules[account.ActiveRuleID]
	if rule.ID == "" {
		rule = doc.Rules[defaultRulePresetID]
		account.ActiveRuleVersion = latestRuleVersion(rule).Version
	}
	version := ruleVersionOrLatest(rule, account.ActiveRuleVersion)
	return rulePreset{
		ID:          rule.ID,
		Name:        rule.Name,
		Description: rule.Description,
		Config:      mergeRuleDefaults(version.Config),
		Behavior:    mergeRuleBehavior(version.Behavior),
		UpdatedAt:   rule.UpdatedAt,
	}, nil
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
	if err := validateRulePresetPayload(p); err != nil {
		return legacyDocumentFromStore(doc), err
	}
	p.Config = mergeRuleDefaults(p.Config)
	p.Behavior = mergeRuleBehavior(p.Behavior)
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	rule := doc.Rules[p.ID]
	nextVersion := 1
	if rule.ID != "" {
		nextVersion = latestRuleVersion(rule).Version + 1
	}
	rule.ID = p.ID
	rule.Name = p.Name
	rule.Description = p.Description
	// 用户保存内置默认 preset 即视为接受该 preset 的当前代码基线，避免下次加载被
	// 内置默认覆盖；各内置账号有独立目标版本，不能再写全局 currentBuiltinRuleVersion。
	if isProtectedDefaultPreset(p.ID) {
		rule.BuiltinVersion = builtinVersionForDefaultRule(p.ID)
	} else {
		rule.BuiltinVersion = 0
	}
	rule.Versions = append(rule.Versions, rulePresetVersion{
		Version:   nextVersion,
		Config:    p.Config,
		Behavior:  p.Behavior,
		CreatedAt: p.UpdatedAt,
	})
	rule.UpdatedAt = p.UpdatedAt
	doc.Rules[p.ID] = rule
	for accountID, account := range doc.Accounts {
		if account.ActiveRuleID == p.ID {
			account.ActiveRuleVersion = nextVersion
			account.UpdatedAt = p.UpdatedAt
			doc.Accounts[accountID] = account
		}
	}
	if err := s.writeStoreLocked(doc); err != nil {
		return legacyDocumentFromStore(doc), err
	}
	return legacyDocumentFromStore(doc), nil
}

func (s *rulePresetStore) setActive(id string) (rulePresetDocument, error) {
	return s.setActiveForAccount(defaultRuleAccountID, id)
}

func (s *rulePresetStore) setActiveForAccount(accountID, id string) (rulePresetDocument, error) {
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
	accountID = sanitizeRuleAccountID(accountID)
	account := doc.Accounts[accountID]
	if account.Name == "" {
		account.Name = accountID
	}
	account.ActiveRuleID = id
	account.ActiveRuleVersion = latestRuleVersion(rule).Version
	account.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	doc.Accounts[accountID] = account
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
	// 保护所有账号的 catalog 默认 preset（含 FBIF default）：删了会让账号回退目标悬空。
	if id == defaultRulePresetID || isProtectedDefaultPreset(id) {
		return legacyDocumentFromStore(doc), fmt.Errorf("default preset cannot be deleted")
	}
	if _, ok := doc.Rules[id]; !ok {
		return legacyDocumentFromStore(doc), fmt.Errorf("rule preset not found")
	}
	delete(doc.Rules, id)
	for accountID, account := range doc.Accounts {
		if account.ActiveRuleID == id {
			// 回退 preset 读 accountCatalog（单一真相源），不再逐账号 if-chain（消灭 D5）。
			fallbackID := defaultRulePresetID
			if meta, ok := accountMetaByID(accountID); ok {
				fallbackID = meta.DefaultPresetID
			}
			account.ActiveRuleID = fallbackID
			account.ActiveRuleVersion = latestRuleVersion(doc.Rules[fallbackID]).Version
			account.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			doc.Accounts[accountID] = account
		}
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

func sanitizeRuleAccountID(id string) string {
	id = sanitizeRulePresetID(id)
	if id == "" {
		return defaultRuleAccountID
	}
	return id
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

func writeRulePresetDocument(w http.ResponseWriter, status int, doc rulePresetDocument) {
	writeJSON(w, status, struct {
		ActiveID  string               `json:"active_id"`
		Presets   []rulePreset         `json:"presets"`
		Accounts  []ruleAccountBinding `json:"accounts"`
		UpdatedAt string               `json:"updated_at"`
	}{
		ActiveID:  doc.ActiveID,
		Presets:   doc.Presets,
		Accounts:  doc.Accounts,
		UpdatedAt: doc.UpdatedAt,
	})
}

func registerRulePresetHandlers(mux *http.ServeMux, store *rulePresetStore) {
	mux.HandleFunc("GET /api/rules/active", func(w http.ResponseWriter, r *http.Request) {
		preset, err := store.activeForAccount(r.URL.Query().Get("account"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"preset": preset})
	})

	mux.HandleFunc("GET /api/rules/presets", func(w http.ResponseWriter, r *http.Request) {
		doc, err := store.list()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeRulePresetCatalog(w, http.StatusOK, doc)
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
		writeRulePresetDocument(w, http.StatusOK, doc)
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
		writeRulePresetDocument(w, http.StatusOK, doc)
	})

	mux.HandleFunc("PUT /api/rules/admin/active", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminPassword(w, r) {
			return
		}
		var req struct {
			ID        string `json:"id"`
			AccountID string `json:"account_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		doc, err := store.setActiveForAccount(req.AccountID, req.ID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeRulePresetDocument(w, http.StatusOK, doc)
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
		writeRulePresetDocument(w, http.StatusOK, doc)
	})
}
