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
const currentRuleStoreSchemaVersion = 3

type rulePreset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Channel     string `json:"channel,omitempty"`
	RuleFamily  string `json:"rule_family,omitempty"`
	// BaselineID 显式指向一个内置账号基线 preset。自建规则不再从
	// ID/当前账号猜继承关系；只允许 defaultRuleDefinitions 中的受控 id。
	BaselineID string `json:"baseline_id"`
	// MigrationRequired 只来自 schema v1/v2 迁移：旧数据无法唯一推断
	// baseline 时显式标记，禁止新绑定，直到管理员选择基线并重新保存。
	MigrationRequired  bool           `json:"migration_required,omitempty"`
	MigrationNote      string         `json:"migration_note,omitempty"`
	MigrationConfirmed bool           `json:"migration_confirmed,omitempty"`
	Config             map[string]any `json:"config"`
	Behavior           map[string]any `json:"behavior,omitempty"`
	UpdatedAt          string         `json:"updated_at"`
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
	ID                string              `json:"id"`
	Name              string              `json:"name"`
	Description       string              `json:"description,omitempty"`
	BaselineID        string              `json:"baseline_id"`
	MigrationRequired bool                `json:"migration_required,omitempty"`
	MigrationNote     string              `json:"migration_note,omitempty"`
	Versions          []rulePresetVersion `json:"versions"`
	UpdatedAt         string              `json:"updated_at"`
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

// baselineID 是已经在保存/启动边界校验过的显式继承目标。
// 未知基线在 baselineRuleConfigForPreset 内 fail loud，绝不默认落 FBIF。
func mergeRuleDefaults(baselineID string, config map[string]any) map[string]any {
	merged := baselineRuleConfigForPreset(baselineID)
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
	if probe.SchemaVersion >= 2 || len(probe.Accounts) > 0 || len(probe.Rules) > 0 {
		if err := json.Unmarshal(b, &doc); err != nil {
			return defaultRuleStoreDocumentV2(), err
		}
		if probe.SchemaVersion > currentRuleStoreSchemaVersion {
			return defaultRuleStoreDocumentV2(), fmt.Errorf("规则库 schema_version=%d 高于当前支持版本 %d", probe.SchemaVersion, currentRuleStoreSchemaVersion)
		}
		if probe.SchemaVersion < currentRuleStoreSchemaVersion {
			migrateLegacyRuleBaselines(&doc)
		}
		if err := validateRuleStoreBaselines(doc); err != nil {
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
		p.BaselineID = legacyCompatibleBaselineID(p.ID, p.BaselineID)
		p.Config = mergeRuleDefaults(p.BaselineID, p.Config)
		p.Behavior = mergeRuleBehavior(p.BaselineID, p.Behavior)
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
			BaselineID:  p.BaselineID,
			Versions: []rulePresetVersion{{
				Version:   1,
				Config:    mergeRuleDefaults(p.BaselineID, p.Config),
				Behavior:  mergeRuleBehavior(p.BaselineID, p.Behavior),
				CreatedAt: updatedAt,
			}},
			UpdatedAt: updatedAt,
		}
	}
	return normalizeRuleStoreDocument(out)
}

func normalizeRuleStoreDocument(doc ruleStoreDocumentV2) ruleStoreDocumentV2 {
	now := time.Now().UTC().Format(time.RFC3339)
	if doc.SchemaVersion < currentRuleStoreSchemaVersion {
		migrateLegacyRuleBaselines(&doc)
	}
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
		rule.BaselineID = sanitizeRulePresetID(rule.BaselineID)
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
			// 旧规则基线存在歧义时不能先用占位 baseline 展开并写回内存，
			// 否则其他账号再按自己的基线合并时仍会残留错误默认值。
			if !rule.MigrationRequired {
				version.Config = mergeRuleDefaults(rule.BaselineID, version.Config)
				version.Behavior = mergeRuleBehavior(rule.BaselineID, version.Behavior)
			}
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
	doc.SchemaVersion = currentRuleStoreSchemaVersion
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
		config := mergeRuleDefaults(rule.BaselineID, version.Config)
		behavior := mergeRuleBehavior(rule.BaselineID, version.Behavior)
		if rule.MigrationRequired {
			// 后台确认基线前只能看到历史原始 overrides，不得把
			// 占位 baseline 的默认值烘进下一版。
			config = cloneRuleMap(version.Config)
			behavior = cloneRuleMap(version.Behavior)
		}
		presets = append(presets, rulePreset{
			ID:                rule.ID,
			Name:              rule.Name,
			Description:       rule.Description,
			Channel:           ruleChannelForBaseline(rule.BaselineID),
			RuleFamily:        ruleFamilyForBaseline(rule.BaselineID),
			BaselineID:        rule.BaselineID,
			MigrationRequired: rule.MigrationRequired,
			MigrationNote:     rule.MigrationNote,
			Config:            config,
			Behavior:          behavior,
			UpdatedAt:         rule.UpdatedAt,
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
	if err := validateRuleStoreBaselines(doc); err != nil {
		return err
	}
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

func (s *rulePresetStore) validate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.readStoreLocked()
	return err
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
	accountID = sanitizeRuleAccountID(accountID)
	meta, ok := accountMetaByID(accountID)
	if !ok {
		return rulePreset{}, fmt.Errorf("未知规则账号 account_id=%q", accountID)
	}
	account := doc.Accounts[accountID]
	if account.ActiveRuleID == "" {
		account.ActiveRuleID = meta.DefaultPresetID
		account.ActiveRuleVersion = latestRuleVersion(doc.Rules[meta.DefaultPresetID]).Version
	}
	rule := doc.Rules[account.ActiveRuleID]
	if rule.ID == "" {
		rule = doc.Rules[meta.DefaultPresetID]
		account.ActiveRuleVersion = latestRuleVersion(rule).Version
	}
	version := ruleVersionOrLatest(rule, account.ActiveRuleVersion)
	baselineID := rule.BaselineID
	if rule.MigrationRequired {
		// 历史规则可能已绑定多个规则族。迁移确认前，每个现有绑定
		// 按该账号自己的受控默认基线合并，不把占位 baseline 串到其他渠道。
		baselineID = meta.DefaultPresetID
	}
	return rulePreset{
		ID:                rule.ID,
		Name:              rule.Name,
		Description:       rule.Description,
		Channel:           ruleChannelForBaseline(baselineID),
		RuleFamily:        ruleFamilyForBaseline(baselineID),
		BaselineID:        baselineID,
		MigrationRequired: rule.MigrationRequired,
		MigrationNote:     rule.MigrationNote,
		Config:            mergeRuleDefaults(baselineID, version.Config),
		Behavior:          mergeRuleBehavior(baselineID, version.Behavior),
		UpdatedAt:         rule.UpdatedAt,
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
	if isProtectedDefaultPreset(p.ID) {
		if strings.TrimSpace(p.BaselineID) != "" && sanitizeRulePresetID(p.BaselineID) != p.ID {
			return legacyDocumentFromStore(doc), fmt.Errorf("内置规则 %s 的 baseline_id 必须是 %s", p.ID, p.ID)
		}
		p.BaselineID = p.ID
	} else {
		p.BaselineID = sanitizeRulePresetID(p.BaselineID)
	}
	rule := doc.Rules[p.ID]
	if rule.MigrationRequired {
		if !p.MigrationConfirmed {
			return legacyDocumentFromStore(doc), fmt.Errorf("规则 %q 尚未确认历史基线；请在规则后台显式选择 baseline_id", rule.ID)
		}
		// 首次确认只改基线：忽略客户端可能已被占位 baseline 展开的
		// config/behavior，从 store 中的原始 overrides 重建新版本。
		raw := latestRuleVersion(rule)
		p.Config = cloneRuleMap(raw.Config)
		p.Behavior = cloneRuleMap(raw.Behavior)
	}
	if err := validateRulePresetPayload(p); err != nil {
		return legacyDocumentFromStore(doc), err
	}
	p.Config = mergeRuleDefaults(p.BaselineID, p.Config)
	p.Behavior = mergeRuleBehavior(p.BaselineID, p.Behavior)
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	nextVersion := 1
	if rule.ID != "" {
		nextVersion = latestRuleVersion(rule).Version + 1
	}
	rule.ID = p.ID
	rule.Name = p.Name
	rule.Description = p.Description
	rule.BaselineID = p.BaselineID
	// 能通过 payload 校验走到这里，说明管理员已显式选择 baseline；
	// 新版本从此不再属于待迁移规则。
	rule.MigrationRequired = false
	rule.MigrationNote = ""
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
	if _, ok := accountMetaByID(accountID); !ok {
		return legacyDocumentFromStore(doc), fmt.Errorf("未知规则账号 account_id=%q", accountID)
	}
	if rule.MigrationRequired {
		return legacyDocumentFromStore(doc), fmt.Errorf("规则 %q 需先确认历史基线后才能绑定：%s", rule.ID, rule.MigrationNote)
	}
	if err := validateRuleBindingFamily(accountID, rule); err != nil {
		return legacyDocumentFromStore(doc), err
	}
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

// legacyCompatibleBaselineID 只用于 schema v1/v2 迁移：内置规则的基线是自身，
// 历史自建规则明确标记为 default（它们过去已经按 FBIF 合并）。
// 这是一次性数据迁移，不是新规则的猜测路径。
func legacyCompatibleBaselineID(ruleID, baselineID string) string {
	baselineID = sanitizeRulePresetID(baselineID)
	if baselineID != "" {
		return baselineID
	}
	ruleID = sanitizeRulePresetID(ruleID)
	if isProtectedDefaultPreset(ruleID) {
		return ruleID
	}
	return defaultRulePresetID
}

func ruleChannelForBaseline(baselineID string) string {
	baselineID = sanitizeRulePresetID(baselineID)
	channel := ""
	for _, meta := range accountCatalog() {
		if sanitizeRulePresetID(meta.DefaultPresetID) != baselineID {
			continue
		}
		if channel != "" && channel != meta.Channel {
			return ""
		}
		channel = meta.Channel
	}
	return channel
}

func ruleFamilyForBaseline(baselineID string) string {
	baselineID = sanitizeRulePresetID(baselineID)
	family := ""
	for _, meta := range accountCatalog() {
		if sanitizeRulePresetID(meta.DefaultPresetID) != baselineID {
			continue
		}
		if family != "" && family != meta.TemplateID {
			return ""
		}
		family = meta.TemplateID
	}
	return family
}

func validateRuleBindingFamily(accountID string, rule ruleDefinition) error {
	meta, ok := accountMetaByID(sanitizeRuleAccountID(accountID))
	if !ok {
		return fmt.Errorf("未知规则账号 account_id=%q", accountID)
	}
	ruleFamily := ruleFamilyForBaseline(rule.BaselineID)
	if ruleFamily == "" {
		return fmt.Errorf("规则 %q 的 baseline_id=%q 没有已登记规则族", rule.ID, rule.BaselineID)
	}
	if ruleFamily != meta.TemplateID {
		return fmt.Errorf("规则 %q 属于 %s 规则族，不能绑定到 %s 规则族账号 %q", rule.ID, ruleFamily, meta.TemplateID, meta.ID)
	}
	return nil
}

func legacyBaselineFromAccountBindings(doc ruleStoreDocumentV2, ruleID string) (string, bool, string) {
	candidates := make([]string, 0, 2)
	seen := map[string]bool{}
	// 按 accountCatalog 声明顺序推导，不依赖 Go map 的随机迭代顺序。
	for _, meta := range accountCatalog() {
		account, ok := doc.Accounts[meta.ID]
		if !ok || sanitizeRulePresetID(account.ActiveRuleID) != ruleID {
			continue
		}
		baselineID := sanitizeRulePresetID(meta.DefaultPresetID)
		if !seen[baselineID] {
			seen[baselineID] = true
			candidates = append(candidates, baselineID)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], false, ""
	}
	if len(candidates) == 0 {
		return defaultRulePresetID, true, "历史规则未绑定账号，无法推断 baseline_id；请在规则后台选择基线并保存"
	}
	return candidates[0], true, fmt.Sprintf("历史规则同时来自多个账号基线（%s），无法推断唯一 baseline_id；请拆分绑定或确认基线并保存", strings.Join(candidates, "、"))
}

func migrateLegacyRuleBaselines(doc *ruleStoreDocumentV2) {
	if doc == nil {
		return
	}
	for id, rule := range doc.Rules {
		ruleID := sanitizeRulePresetID(firstNonEmpty(rule.ID, id))
		baselineID := sanitizeRulePresetID(rule.BaselineID)
		if baselineID == "" {
			if isProtectedDefaultPreset(ruleID) {
				baselineID = ruleID
			} else {
				var migrationRequired bool
				var migrationNote string
				baselineID, migrationRequired, migrationNote = legacyBaselineFromAccountBindings(*doc, ruleID)
				rule.MigrationRequired = migrationRequired
				rule.MigrationNote = migrationNote
			}
		}
		rule.BaselineID = baselineID
		doc.Rules[id] = rule
	}
	doc.SchemaVersion = currentRuleStoreSchemaVersion
}

func validateRuleBaselineID(baselineID string) error {
	baselineID = sanitizeRulePresetID(baselineID)
	if baselineID == "" {
		return fmt.Errorf("请选择规则基线（baseline_id）")
	}
	if _, ok := defaultRuleDefinitions("")[baselineID]; !ok {
		return fmt.Errorf("未知规则基线 baseline_id=%q；只能继承已登记的账号默认规则", baselineID)
	}
	return nil
}

func validateRuleStoreBaselines(doc ruleStoreDocumentV2) error {
	for key, rule := range doc.Rules {
		ruleID := sanitizeRulePresetID(firstNonEmpty(rule.ID, key))
		if err := validateRuleBaselineID(rule.BaselineID); err != nil {
			return fmt.Errorf("规则 %q 的基线无效：%w", ruleID, err)
		}
		if isProtectedDefaultPreset(ruleID) && sanitizeRulePresetID(rule.BaselineID) != ruleID {
			return fmt.Errorf("内置规则 %q 的 baseline_id 必须指向自身", ruleID)
		}
	}
	for accountID, account := range doc.Accounts {
		if _, ok := accountMetaByID(accountID); !ok {
			return fmt.Errorf("未知规则账号 account_id=%q", accountID)
		}
		ruleID := sanitizeRulePresetID(account.ActiveRuleID)
		if ruleID == "" {
			continue
		}
		rule, ok := doc.Rules[ruleID]
		if !ok || rule.ID == "" {
			continue
		}
		if rule.MigrationRequired {
			// 旧库的现有绑定允许继续读取，activeForAccount 会按各账号
			// 自己的受控基线合并；新绑定由 setActiveForAccount 明确拒绝。
			continue
		}
		if err := validateRuleBindingFamily(accountID, rule); err != nil {
			return err
		}
	}
	return nil
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
		if !requireDeploymentWritesActive(w) {
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
		if !requireDeploymentWritesActive(w) {
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
		if !requireDeploymentWritesActive(w) {
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
