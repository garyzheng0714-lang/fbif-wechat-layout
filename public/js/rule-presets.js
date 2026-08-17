// rule-presets.js — rule document normalization and persistence.
// The normal editor keeps a local fallback; the admin panel can persist the
// same rule object to the Go server as named presets. Editable field facts live
// in rule-document.js.
import { DEFAULT_BEHAVIOR, normalizeRuleBehavior } from './rule-behavior.js';
import { fetchWithTimeout } from './fetch-timeout.js';
import { authFetch } from './auth-session.js';

import {
  normalizeMoreArticleDefaultCards,
} from './more-articles-config.js';
import { isValidMoreArticleUrl } from './more-articles-meta.js';
import { RULE_PACKS } from './rule-packs/registry.js';
export {
  DEFAULT_RULE_CONFIG,
  FIELD_BY_KEY,
  RULE_MODULE_FIELDS,
  RULE_MODULE_GROUPS,
  RULE_SCHEMA,
} from './rule-document.js';
import {
  DEFAULT_RULE_CONFIG,
  FIELD_BY_KEY,
  RULE_MODULE_FIELDS,
  RULE_MODULE_GROUPS,
  RULE_SCHEMA,
  SAFE_FONT_FAMILY_RE,
} from './rule-document.js';

const LOCAL_CONFIG_KEY = 'config_fbif';
const LOCAL_PRESET_KEY_PREFIX = 'active_rule_fallback_v1::';
const DEFAULT_RULE_ACCOUNT_ID = 'fbif';
const LEGACY_FOOTER_KEY = 'footer_enabled';
const LEGACY_SKIP_UPLOAD_KEY = 'skip_upload';
const BAD_SKIP_UPLOAD_MIGRATION_KEY = 'skip_upload_migrated_v2';
const RESTORED_SKIP_UPLOAD_KEY = 'skip_upload_restored_v3';
const STRUCTURED_RULE_MODULES = new Set([
  'mctoc',
  'fbif_footer',
  'ft_series_materials',
  'asset_library',
  'bottom_blocks',
  'article_footer_defaults',
]);

function hasWindowStorage() {
  return typeof window !== 'undefined' && window.localStorage;
}

function normalizeAccountID(accountID) {
  const id = String(accountID || DEFAULT_RULE_ACCOUNT_ID).trim().toLowerCase();
  return id.replace(/[^a-z0-9_-]+/g, '-').replace(/^-+|-+$/g, '') || DEFAULT_RULE_ACCOUNT_ID;
}

function localConfigKey(accountID) {
  const id = normalizeAccountID(accountID);
  return id === DEFAULT_RULE_ACCOUNT_ID ? LOCAL_CONFIG_KEY : 'config_' + id;
}

function localPresetKey(accountID) {
  return LOCAL_PRESET_KEY_PREFIX + normalizeAccountID(accountID);
}

function isPlainObject(value) {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function clonePlain(value) {
  if (!isPlainObject(value) && !Array.isArray(value)) return value;
  try { return JSON.parse(JSON.stringify(value)); } catch { return value; }
}

function ruleFamilyForIdentity(identity = {}) {
  const explicit = String(identity.rule_family || identity.ruleFamily || '').trim();
  if (explicit) return explicit;
  const rawAccountID = identity.rule_account_id || identity.account_id || identity.accountID;
  if (rawAccountID) {
    const accountID = normalizeAccountID(rawAccountID);
    if (RULE_PACKS[accountID] && RULE_PACKS[accountID].templateId) return RULE_PACKS[accountID].templateId;
  }
  const baselineID = String(identity.baseline_id || identity.baselineId || '').trim();
  if (baselineID) {
    const pack = Object.values(RULE_PACKS).find(candidate => candidate.defaultPresetId === baselineID);
    if (pack && pack.templateId) return pack.templateId;
  }
  return 'fbif';
}

function usesWebsiteRuleSchema(identity = {}) {
  return ruleFamilyForIdentity(identity) === 'foodtalks-site';
}

function usesIndependentRuleSchema(identity = {}) {
  return ruleFamilyForIdentity(identity) !== 'fbif';
}

// FoodTalks 网站 behavior 是独立 schema（顶层 *_regex + lists + params），
// 绝不能经过公众号 normalizeRuleBehavior，否则会裁掉网站字段并
// 注入 Mote actions。公众号路径仍保留原 normalizer 的补默认/校验语义。
export function normalizeRuleBehaviorForRule(raw, identity = {}) {
  if (usesIndependentRuleSchema(identity)) {
    return isPlainObject(raw) ? clonePlain(raw) : {};
  }
  return normalizeRuleBehavior(raw);
}

function withStructuredRuleConfig(config, normalized = normalizeRuleConfig(config)) {
  const source = isPlainObject(config && config.raw_config)
    ? config.raw_config
    : (isPlainObject(config && config.config) && (config.config.schema_version === 2 || isPlainObject(config.config.modules))
      ? config.config
      : config);
  const out = { ...normalized };
  if (isPlainObject(source) && isPlainObject(source.modules)) out.modules = clonePlain(source.modules);
  if (isPlainObject(source) && isPlainObject(source.image)) out.image = clonePlain(source.image);
  return out;
}

// 网站规则还有未进公众号参数表的顶层字段（tip_*/author_*）。
// 它们已由 Go 服务端白名单校验，前端必须保留；已知公共字段仍用
// normalizeRuleConfig 做范围/颜色归一。rawSource 用于后台预览：原规则保留网站字段，
// 当前可视化表单 config 覆盖已知公共字段。
export function normalizeRuleConfigForRule(config, identity = {}, rawSource = config) {
  const normalized = withStructuredRuleConfig(config);
  if (!usesIndependentRuleSchema(identity)) return normalized;
  const source = isPlainObject(rawSource && rawSource.raw_config)
    ? rawSource.raw_config
    : (isPlainObject(rawSource && rawSource.config) ? rawSource.config : rawSource);
  const preserved = isPlainObject(source) ? clonePlain(source) : {};
  const out = { ...preserved, ...normalized };
  if (isPlainObject(preserved.modules)) out.modules = preserved.modules;
  if (isPlainObject(preserved.image)) out.image = preserved.image;
  return out;
}

function websiteRuleConfigFallback(accountID) {
  const config = {
    tip_title_color: '#01396a',
    tip_body_color: '#7f7f7f',
    author_font_size: '14',
    tip_font_size: '14',
    // 与 api/rule_defaults.go defaultFoodTalksSiteRuleConfig().image 保持同值
    //（2026-08-14 62765 金标准：长图比≥1.7 固定 400、竖图宽下限 300、二维码自然宽阈值 300）。
    image: { width: 600, portrait_px: 375, landscape_px: 600, qr_px: 150, qr_max_natural_px: 300, long_px: 400, long_min_ratio: 1.7, portrait_min_px: 300 },
    modules: {
      mctoc: { enabled: false },
      bottom_blocks: { schema_version: 2, enabled: false, presets: [] },
      article_footer_defaults: { schema_version: 1, enabled: false, author_aliases: {}, profiles: [] },
    },
  };
  if (accountID === 'ft-series') Object.assign(config, { text_color: '#333333', muted_color: '#7f7f7f', footer_note_color: '#7f7f7f', table_border_color: '#7f7f7f' });
  return config;
}

function websiteBehaviorFallback(accountID) {
  return accountID === 'ft-series'
    ? { params: { site_variant: { type: 'string', default: 'ft-series' } } }
    : {};
}

function readableMoreArticlesKey(key) {
  if (key === 'enabled') return 'more_articles_enabled';
  if (key === 'default_cards') return 'more_articles_default_cards';
  return key;
}

function normalizeReadableMoreArticlesAliases(moduleConfig) {
  const out = {};
  for (const [key, value] of Object.entries(moduleConfig)) {
    out[readableMoreArticlesKey(key)] = value;
  }
  return out;
}

function numberToString(value, field) {
  let n = Number(value);
  if (!Number.isFinite(n)) n = Number(field.default);
  if (Number.isFinite(field.min)) n = Math.max(field.min, n);
  if (Number.isFinite(field.max)) n = Math.min(field.max, n);
  if (field.step >= 1) return String(Math.round(n));
  return String(n);
}

function normalizeColor(value, fallback) {
  const s = String(value || '').trim();
  return /^#[0-9a-f]{6}$/i.test(s) ? s.toUpperCase() : fallback;
}

function normalizeFieldValue(value, field) {
  if (field.type === 'number') return numberToString(value, field);
  if (field.type === 'color') return normalizeColor(value, field.default);
  if (field.type === 'boolean') {
    if (value == null) return field.default === true;
    return value === true || value === 'true';
  }
  if (field.type === 'text') {
    const s = String(value == null ? '' : value).trim();
    if (field.key === 'font_family' && !SAFE_FONT_FAMILY_RE.test(s)) return field.default;
    return s || field.default;
  }
  return value == null ? field.default : value;
}

function flattenRuleConfigInput(raw) {
  let input = isPlainObject(raw) ? raw : {};
  if (isPlainObject(input.config) && (input.config.schema_version === 2 || isPlainObject(input.config.modules))) {
    input = input.config;
  }
  const modules = isPlainObject(input.modules) ? input.modules : {};
  const flat = {};
  for (const moduleGroup of RULE_MODULE_GROUPS) {
    const moduleConfig = modules[moduleGroup.id];
    if (isPlainObject(moduleConfig)) {
      Object.assign(
        flat,
        moduleGroup.id === 'more_articles'
          ? normalizeReadableMoreArticlesAliases(moduleConfig)
          : moduleConfig
      );
    }
  }
  return { ...input, ...flat };
}

export function normalizeRuleConfig(raw) {
  const input = flattenRuleConfigInput(raw);
  const out = {};
  for (const field of FIELD_BY_KEY.values()) {
    out[field.key] = normalizeFieldValue(input[field.key], field);
  }
  out.more_articles_default_cards = normalizeMoreArticleDefaultCards(input.more_articles_default_cards);
  return out;
}

export function buildRuleConfigDocument(config, meta = {}) {
  const normalized = normalizeRuleConfig(config);
  const modules = {};
  for (const moduleGroup of RULE_MODULE_GROUPS) {
    modules[moduleGroup.id] = {};
    for (const groupID of moduleGroup.groupIds) {
      const group = RULE_SCHEMA.find(item => item.id === groupID);
      if (!group) continue;
      for (const field of group.fields) {
        if (moduleGroup.id === 'more_articles' && field.key === 'more_articles_enabled') {
          modules[moduleGroup.id].enabled = normalized.more_articles_enabled;
          continue;
        }
        modules[moduleGroup.id][field.key] = normalized[field.key];
      }
    }
    if (moduleGroup.id === 'more_articles') {
      modules[moduleGroup.id].default_cards = normalizeMoreArticleDefaultCards(
        normalized.more_articles_default_cards,
        []
      );
    }
  }
  return {
    schema_version: 2,
    name: String(meta.name || '未命名规则').trim() || '未命名规则',
    description: String(meta.description || '').trim(),
    modules,
  };
}

function parseRuleConfigJSON(input) {
  let raw = input;
  if (typeof input === 'string') {
    try {
      raw = JSON.parse(input);
    } catch {
      throw new Error('规则 JSON 格式不正确');
    }
  }
  if (!isPlainObject(raw)) {
    throw new Error('规则 JSON 必须是对象');
  }
  return raw;
}

function validateRawField(key, value, errors) {
  const field = FIELD_BY_KEY.get(key);
  if (!field) {
    errors.push('未知规则字段：' + key);
    return;
  }
  if (field.type === 'number') {
    const n = Number(value);
    if (!Number.isFinite(n)) {
      errors.push(field.label + '必须是数字');
      return;
    }
    if (Number.isFinite(field.min) && n < field.min) {
      errors.push(field.label + '不能小于 ' + field.min);
      return;
    }
    if (Number.isFinite(field.max) && n > field.max) {
      errors.push(field.label + '不能大于 ' + field.max);
    }
    return;
  }
  if (field.type === 'color' && !/^#[0-9a-f]{6}$/i.test(String(value || '').trim())) {
    errors.push(field.label + '必须是 #RRGGBB 颜色');
    return;
  }
  if (field.type === 'boolean' && !(value === true || value === false || value === 'true' || value === 'false')) {
    errors.push(field.label + '必须是布尔值');
    return;
  }
  if (field.key === 'font_family' && !SAFE_FONT_FAMILY_RE.test(String(value || '').trim())) {
    errors.push('字体只能包含中英文、数字、空格和连字符，且不超过 40 个字符');
  }
}

function isSafeMoreArticleImageUrl(value) {
  if (typeof value !== 'string') return false;
  const url = value.trim();
  if (!url) return true;
  return /^https?:\/\//i.test(url) ||
         /^\/\//.test(url) ||
         /^data:image\//i.test(url);
}

function validateMoreArticleDefaultCards(value, errors) {
  if (!Array.isArray(value)) {
    errors.push('默认更多文章卡片必须是数组');
    return;
  }
  for (const card of value) {
    if (!isPlainObject(card)) {
      errors.push('默认更多文章卡片必须是对象');
      return;
    }
    const href = String(card.href || '').trim();
    if (href && !isValidMoreArticleUrl(href)) {
      errors.push('默认更多文章卡片链接必须是公众号链接');
      return;
    }
    if (card.title != null && typeof card.title !== 'string') {
      errors.push('默认更多文章卡片 title 必须是字符串');
      return;
    }
    if (card.imgurl != null && !isSafeMoreArticleImageUrl(card.imgurl)) {
      errors.push('默认更多文章卡片 imgurl 必须是安全图片 URL');
      return;
    }
    if (card.image_urls != null) {
      if (!Array.isArray(card.image_urls)) {
        errors.push('默认更多文章卡片 image_urls 必须是数组');
        return;
      }
      for (const imageUrl of card.image_urls) {
        if (!isSafeMoreArticleImageUrl(imageUrl)) {
          errors.push('默认更多文章卡片 image_urls 必须都是安全图片 URL');
          return;
        }
      }
    }
  }
}

function validateRawConfigEntry(key, value, errors) {
  if (key === 'more_articles_default_cards') {
    validateMoreArticleDefaultCards(value, errors);
    return;
  }
  validateRawField(key, value, errors);
}

function validateRuleConfigSource(source) {
  const errors = [];
  const modules = isPlainObject(source.modules) ? source.modules : null;
  if (source.schema_version === 2 && !modules) {
    errors.push('modules 必须是对象');
  }
  for (const [key, value] of Object.entries(source)) {
    if (['schema_version', 'name', 'description', 'modules', 'config'].includes(key)) continue;
    validateRawConfigEntry(key, value, errors);
  }
  if (modules) {
    const seenModuleFields = new Set();
    for (const [moduleName, moduleConfig] of Object.entries(modules)) {
      const allowedFields = RULE_MODULE_FIELDS.get(moduleName);
      if (!allowedFields) {
        if (STRUCTURED_RULE_MODULES.has(moduleName)) continue;
        errors.push('未知规则模块：' + moduleName);
        continue;
      }
      if (!isPlainObject(moduleConfig)) {
        errors.push(moduleName + ' 模块必须是对象');
        continue;
      }
      for (const [key, value] of Object.entries(moduleConfig)) {
        const canonicalKey = moduleName === 'more_articles' ? readableMoreArticlesKey(key) : key;
        validateRawConfigEntry(canonicalKey, value, errors);
        const isMoreArticleCards = moduleName === 'more_articles' && canonicalKey === 'more_articles_default_cards';
        if (!isMoreArticleCards && !allowedFields.has(canonicalKey)) {
          errors.push(key + ' 不能放在 ' + moduleName + ' 模块');
        }
        if (seenModuleFields.has(canonicalKey)) {
          errors.push('重复规则字段：' + canonicalKey);
        }
        seenModuleFields.add(canonicalKey);
      }
    }
  }
  if (errors.length) throw new Error(errors[0]);
}

export function validateRuleConfigDocument(input) {
  const raw = parseRuleConfigJSON(input);
  const source = isPlainObject(raw.config) ? raw.config : raw;
  validateRuleConfigSource(source);
  return {
    name: String(raw.name || '').trim(),
    description: String(raw.description || '').trim(),
    config: withStructuredRuleConfig(source),
    document: raw,
  };
}

export function parseRuleConfigDocument(input) {
  return validateRuleConfigDocument(input);
}

export function getRuleNumber(config, key) {
  const normalized = normalizeRuleConfig(config);
  const n = Number(normalized[key]);
  return Number.isFinite(n) ? n : Number(DEFAULT_RULE_CONFIG[key]);
}

export function getRuleBoolean(config, key) {
  return normalizeRuleConfig(config)[key] === true;
}

export function getActiveRuleConfig() {
  if (typeof window !== 'undefined' && window._activeConfig && window._activeConfig.config) {
    return normalizeRuleConfigForRule(window._activeConfig.config, window._activeConfig.preset || {});
  }
  return normalizeRuleConfig(DEFAULT_RULE_CONFIG);
}

export function getActiveRuleBehavior() {
  if (typeof window !== 'undefined' && window._activeConfig && window._activeConfig.behavior) {
    return normalizeRuleBehaviorForRule(window._activeConfig.behavior, window._activeConfig.preset || {});
  }
  return normalizeRuleBehavior(DEFAULT_BEHAVIOR);
}

export function loadLocalRuleConfig(accountID = DEFAULT_RULE_ACCOUNT_ID) {
  let raw = {};
  const account = normalizeAccountID(accountID);
  if (hasWindowStorage()) {
    try {
      raw = JSON.parse(window.localStorage.getItem(localConfigKey(account)) || '{}') || {};
    } catch {
      raw = {};
    }
    if (account === DEFAULT_RULE_ACCOUNT_ID) {
      if (raw.footer_enabled == null) {
        raw.footer_enabled = window.localStorage.getItem(LEGACY_FOOTER_KEY) !== 'false';
      }
      if (raw.skip_upload == null) {
        raw.skip_upload = window.localStorage.getItem(LEGACY_SKIP_UPLOAD_KEY) !== 'false';
      }
      if (window.localStorage.getItem(BAD_SKIP_UPLOAD_MIGRATION_KEY) === 'true' &&
          window.localStorage.getItem(RESTORED_SKIP_UPLOAD_KEY) !== 'true') {
        raw.skip_upload = true;
        window.localStorage.setItem(LEGACY_SKIP_UPLOAD_KEY, 'true');
        window.localStorage.setItem(RESTORED_SKIP_UPLOAD_KEY, 'true');
      }
    }
  }
  if (usesWebsiteRuleSchema({ rule_account_id: account })) {
    const fallback = websiteRuleConfigFallback(account);
    raw = {
      ...fallback,
      ...raw,
      image: { ...fallback.image, ...(isPlainObject(raw.image) ? raw.image : {}) },
      modules: { ...fallback.modules, ...(isPlainObject(raw.modules) ? raw.modules : {}) },
    };
  }
  return normalizeRuleConfigForRule(raw, { rule_account_id: account });
}

export function saveLocalRuleConfig(config, accountID = DEFAULT_RULE_ACCOUNT_ID) {
  const account = normalizeAccountID(accountID);
  const normalized = normalizeRuleConfig(config);
  const stored = normalizeRuleConfigForRule(config, { rule_account_id: account });
  if (hasWindowStorage()) {
    try {
      window.localStorage.setItem(localConfigKey(account), JSON.stringify(stored));
      if (account === DEFAULT_RULE_ACCOUNT_ID) {
        window.localStorage.setItem(LEGACY_FOOTER_KEY, String(normalized.footer_enabled));
        window.localStorage.setItem(LEGACY_SKIP_UPLOAD_KEY, String(normalized.skip_upload));
      }
    } catch {}
  }
  return stored;
}

export function loadLocalRulePreset(accountID = DEFAULT_RULE_ACCOUNT_ID) {
  const account = normalizeAccountID(accountID);
  let cached = {};
  if (hasWindowStorage()) {
    try { cached = JSON.parse(window.localStorage.getItem(localPresetKey(account)) || '{}') || {}; } catch { cached = {}; }
  }
  const identity = {
    id: String(cached.id || 'local'),
    name: String(cached.name || '本机设置'),
    baseline_id: String(cached.baseline_id || ''),
    rule_account_id: account,
  };
  const fallbackBehavior = usesWebsiteRuleSchema(identity)
    ? websiteBehaviorFallback(account)
    : (usesIndependentRuleSchema(identity) ? {} : DEFAULT_BEHAVIOR);
  return {
    ...identity,
    config: loadLocalRuleConfig(account),
    behavior: normalizeRuleBehaviorForRule(isPlainObject(cached.behavior) ? cached.behavior : fallbackBehavior, identity),
  };
}

export function saveLocalRulePreset(preset, accountID = DEFAULT_RULE_ACCOUNT_ID) {
  const account = normalizeAccountID(accountID);
  const identity = { ...(preset || {}), rule_account_id: account };
  const stored = {
    id: String(identity.id || 'local'),
    name: String(identity.name || '本机设置'),
    baseline_id: String(identity.baseline_id || ''),
    rule_account_id: account,
    config: saveLocalRuleConfig(identity.config || {}, account),
    behavior: normalizeRuleBehaviorForRule(identity.behavior, identity),
  };
  if (hasWindowStorage()) {
    try {
      window.localStorage.setItem(localPresetKey(account), JSON.stringify({
        id: stored.id,
        name: stored.name,
        baseline_id: stored.baseline_id,
        behavior: stored.behavior,
      }));
    } catch {}
  }
  return stored;
}

export function applyActiveRuleConfig(config, preset) {
  const identity = preset || {};
  const rawConfig = isPlainObject(identity.raw_config)
    ? identity.raw_config
    : (usesIndependentRuleSchema(identity) && isPlainObject(identity.config) ? identity.config : config);
  const normalized = normalizeRuleConfigForRule(config, identity, rawConfig);
  if (isPlainObject(rawConfig) && isPlainObject(rawConfig.modules)) normalized.modules = rawConfig.modules;
  if (isPlainObject(rawConfig) && isPlainObject(rawConfig.image)) normalized.image = rawConfig.image;
  const behavior = normalizeRuleBehaviorForRule(preset && preset.behavior, preset || {});
  if (typeof window !== 'undefined') {
    window._activeConfig = { preset: preset || null, config: normalized, behavior };
    window._skipUpload = normalized.skip_upload === true;
  }
  return normalized;
}

async function parseJSONResponse(resp) {
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    const error = new Error(data.error || ('HTTP ' + resp.status));
    error.status = resp.status;
    error.code = data.code || '';
    error.expectedRevision = data.expected_revision;
    error.currentRevision = data.current_revision;
    error.changedPaths = Array.isArray(data.changed_paths) ? data.changed_paths : [];
    throw error;
  }
  return data;
}

export async function loadActiveRulePreset(accountID = DEFAULT_RULE_ACCOUNT_ID) {
  const account = normalizeAccountID(accountID);
  const fallback = loadLocalRulePreset(account);
  try {
    const resp = await fetchWithTimeout('/api/rules/active?account=' + encodeURIComponent(account), { cache: 'no-store' }, { timeoutMs: 10000, timeoutMessage: '规则加载超时' });
    const data = await parseJSONResponse(resp);
    const preset = data.preset || fallback;
    return {
      source: 'server',
      preset: {
        ...preset,
        rule_account_id: account,
        raw_config: preset.config,
        config: normalizeRuleConfigForRule(preset.config, { ...preset, rule_account_id: account }),
        behavior: normalizeRuleBehaviorForRule(preset.behavior, { ...preset, rule_account_id: account }),
      },
    };
  } catch (err) {
    console.warn('[rules] using local fallback', err);
    return { source: 'local', preset: fallback, error: String((err && err.message) || err || '规则服务不可用') };
  }
}

export async function listPublicRulePresets() {
  const resp = await fetchWithTimeout('/api/rules/presets', { cache: 'no-store' }, { timeoutMs: 10000 });
  return parseJSONResponse(resp);
}

function adminRequest(url, options = {}) {
  return fetchWithTimeout(url, options, {
    timeoutMs: 10000,
    fetchImpl: (input, init) => authFetch(input, init),
  });
}

export async function listRulePresets() {
  const resp = await adminRequest('/api/rules/admin/presets', {
    headers: { 'Content-Type': 'application/json' },
    cache: 'no-store',
  });
  return parseJSONResponse(resp);
}

export async function saveRulePreset(preset) {
  const requestedRevision = Number(preset && preset.base_revision);
  const presetRevision = Number(preset && preset.version);
  const baseRevision = Number.isInteger(requestedRevision) && requestedRevision >= 0
    ? requestedRevision
    : (Number.isInteger(presetRevision) && presetRevision >= 0 ? presetRevision : 0);
  const resp = await adminRequest('/api/rules/admin/presets', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      ...preset,
      base_revision: baseRevision,
      config: normalizeRuleConfigForRule(preset.config, preset),
      behavior: normalizeRuleBehaviorForRule(preset.behavior, preset),
    }),
  });
  return parseJSONResponse(resp);
}

export async function setActiveRulePreset(id, accountID = DEFAULT_RULE_ACCOUNT_ID, baseRevision = 0) {
  const revision = Number(baseRevision);
  const resp = await adminRequest('/api/rules/admin/active', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      id,
      account_id: normalizeAccountID(accountID),
      base_revision: Number.isInteger(revision) && revision >= 0 ? revision : 0,
    }),
  });
  return parseJSONResponse(resp);
}

export async function deleteRulePreset(id) {
  const resp = await adminRequest('/api/rules/admin/presets/' + encodeURIComponent(id), {
    method: 'DELETE',
    headers: { 'Content-Type': 'application/json' },
  });
  return parseJSONResponse(resp);
}

export function validateRuleConfig(config) {
  const c = normalizeRuleConfig(config);
  const warnings = [];
  const body = Number(c.font_size);
  const heading = Number(c.heading_size);
  const caption = Number(c.caption_font_size);
  const titleFont = Number(c.banner_title_font_size);
  const titleLine = Number(c.banner_title_line_height);
  const titleBox = Number(c.banner_title_box_height);
  const titleMaxLines = Number(c.banner_title_max_lines);
  const titleX = Number(c.banner_title_x);
  const titleWidth = Number(c.banner_title_width);

  if (heading < body) {
    warnings.push('标题字号小于正文字号，层级会变弱。');
  }
  if (caption > body) {
    warnings.push('图片说明字号大于正文字号，说明文字会抢正文层级。');
  }
  if (titleLine < titleFont * 1.1) {
    warnings.push('更多文章标题行高过小，双行标题可能拥挤。');
  }
  if (titleMaxLines > 1 && titleLine * titleMaxLines > titleBox + titleLine * 0.35) {
    warnings.push('更多文章标题框高度不足，多行标题可能超出封面区域。');
  }
  if (titleX + titleWidth > 1000) {
    warnings.push('更多文章标题宽度超出 1000 宽画布。');
  }
  if (Number(c.more_articles_slots) === 0 && c.footer_enabled) {
    warnings.push('默认卡片数为 0 时，页脚里的更多文章区域会被移除。');
  }
  return warnings;
}
