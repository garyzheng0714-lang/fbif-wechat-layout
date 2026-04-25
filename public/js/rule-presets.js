// rule-presets.js — single source of truth for configurable layout rules.
// The normal editor keeps a local fallback; the admin panel can persist the
// same rule object to the Go server as named presets.

const LOCAL_CONFIG_KEY = 'config_fbif';
const LEGACY_FOOTER_KEY = 'footer_enabled';
const LEGACY_SKIP_UPLOAD_KEY = 'skip_upload';
const BAD_SKIP_UPLOAD_MIGRATION_KEY = 'skip_upload_migrated_v2';
const RESTORED_SKIP_UPLOAD_KEY = 'skip_upload_restored_v3';

export const RULE_SCHEMA = Object.freeze([
  {
    id: 'article',
    label: '文章排版',
    fields: Object.freeze([
      { key: 'font_size', label: '正文字号', type: 'number', min: 12, max: 24, step: 1, default: '15' },
      { key: 'heading_size', label: '标题字号', type: 'number', min: 14, max: 32, step: 1, default: '18' },
      { key: 'line_height', label: '行高', type: 'number', min: 1, max: 3, step: 0.05, default: '1.75' },
      { key: 'letter_spacing', label: '字间距', type: 'number', min: 0, max: 0.2, step: 0.002, default: '0.034' },
      { key: 'paragraph_margin_x', label: '段落横向边距', type: 'number', min: 0, max: 28, step: 1, default: '8' },
      { key: 'paragraph_gap', label: '段后间距', type: 'number', min: 0, max: 48, step: 1, default: '20' },
      { key: 'font_family', label: '字体', type: 'text', default: 'PingFang SC' },
      { key: 'text_color', label: '正文颜色', type: 'color', default: '#544545' },
      { key: 'link_color', label: '链接颜色', type: 'color', default: '#0070C0' },
      { key: 'muted_color', label: '辅助文字颜色', type: 'color', default: '#888888' },
    ]),
  },
  {
    id: 'supporting',
    label: '说明与引用',
    fields: Object.freeze([
      { key: 'caption_font_size', label: '图片说明字号', type: 'number', min: 10, max: 18, step: 1, default: '12' },
      { key: 'attribution_font_size', label: '作者来源字号', type: 'number', min: 10, max: 20, step: 1, default: '15' },
      { key: 'blockquote_padding_left', label: '引用左缩进', type: 'number', min: 0, max: 32, step: 1, default: '12' },
      { key: 'blockquote_border_width', label: '引用线宽', type: 'number', min: 0, max: 8, step: 1, default: '3' },
      { key: 'md_heading_max_chars', label: 'Markdown 粗体标题字数上限', type: 'number', min: 10, max: 120, step: 1, default: '60' },
      { key: 'decorative_image_max_px', label: '小图保持原宽阈值', type: 'number', min: 120, max: 1000, step: 10, default: '640' },
    ]),
  },
  {
    id: 'more_articles',
    label: '更多文章',
    fields: Object.freeze([
      { key: 'more_articles_slots', label: '新文章默认卡片数', type: 'number', min: 0, max: 8, step: 1, default: '3' },
      { key: 'banner_overlay_alpha', label: '封面遮罩透明度', type: 'number', min: 0, max: 1, step: 0.01, default: '0.47058823529411764' },
      { key: 'banner_title_x', label: '标题 X', type: 'number', min: 0, max: 400, step: 1, default: '61' },
      { key: 'banner_title_y', label: '标题 Y', type: 'number', min: 0, max: 220, step: 1, default: '92' },
      { key: 'banner_title_width', label: '标题宽度', type: 'number', min: 300, max: 1000, step: 1, default: '878' },
      { key: 'banner_title_box_height', label: '标题框高度', type: 'number', min: 40, max: 220, step: 1, default: '116' },
      { key: 'banner_title_font_size', label: '标题字号', type: 'number', min: 24, max: 80, step: 1, default: '48' },
      { key: 'banner_title_line_height', label: '标题行高', type: 'number', min: 32, max: 100, step: 1, default: '70' },
      { key: 'banner_title_max_lines', label: '标题最多行数', type: 'number', min: 1, max: 4, step: 1, default: '2' },
    ]),
  },
  {
    id: 'workflow',
    label: '工作流',
    fields: Object.freeze([
      { key: 'footer_enabled', label: '默认显示页脚', type: 'boolean', default: true },
      { key: 'skip_upload', label: 'DOCX 跳过上传', type: 'boolean', default: true },
    ]),
  },
]);

const FIELD_BY_KEY = new Map(
  RULE_SCHEMA.flatMap(group => group.fields.map(field => [field.key, field]))
);

export const DEFAULT_RULE_CONFIG = Object.freeze(
  Object.fromEntries(Array.from(FIELD_BY_KEY.values()).map(field => [field.key, field.default]))
);

function hasWindowStorage() {
  return typeof window !== 'undefined' && window.localStorage;
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
    return s || field.default;
  }
  return value == null ? field.default : value;
}

export function normalizeRuleConfig(raw) {
  const input = raw && typeof raw === 'object' ? raw : {};
  const out = {};
  for (const field of FIELD_BY_KEY.values()) {
    out[field.key] = normalizeFieldValue(input[field.key], field);
  }
  return out;
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
    return normalizeRuleConfig(window._activeConfig.config);
  }
  return normalizeRuleConfig(DEFAULT_RULE_CONFIG);
}

export function loadLocalRuleConfig() {
  let raw = {};
  if (hasWindowStorage()) {
    try {
      raw = JSON.parse(window.localStorage.getItem(LOCAL_CONFIG_KEY) || '{}') || {};
    } catch {
      raw = {};
    }
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
  return normalizeRuleConfig(raw);
}

export function saveLocalRuleConfig(config) {
  const normalized = normalizeRuleConfig(config);
  if (hasWindowStorage()) {
    try {
      window.localStorage.setItem(LOCAL_CONFIG_KEY, JSON.stringify(normalized));
      window.localStorage.setItem(LEGACY_FOOTER_KEY, String(normalized.footer_enabled));
      window.localStorage.setItem(LEGACY_SKIP_UPLOAD_KEY, String(normalized.skip_upload));
    } catch {}
  }
  return normalized;
}

export function applyActiveRuleConfig(config, preset) {
  const normalized = normalizeRuleConfig(config);
  if (typeof window !== 'undefined') {
    window._activeConfig = { preset: preset || null, config: normalized };
    window._skipUpload = normalized.skip_upload === true;
  }
  return normalized;
}

async function parseJSONResponse(resp) {
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    throw new Error(data.error || ('HTTP ' + resp.status));
  }
  return data;
}

export async function loadActiveRulePreset() {
  const fallback = {
    id: 'local',
    name: '本机设置',
    config: loadLocalRuleConfig(),
  };
  try {
    const resp = await fetch('/api/rules/active', { cache: 'no-store' });
    const data = await parseJSONResponse(resp);
    const preset = data.preset || fallback;
    return {
      source: 'server',
      preset: {
        ...preset,
        config: normalizeRuleConfig(preset.config),
      },
    };
  } catch (err) {
    console.warn('[rules] using local fallback', err);
    return { source: 'local', preset: fallback };
  }
}

export async function adminLogin(password) {
  const resp = await fetch('/api/rules/admin/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password: String(password || '').trim() }),
  });
  return parseJSONResponse(resp);
}

function adminHeaders(password) {
  return {
    'Content-Type': 'application/json',
    'X-Admin-Password': String(password || '').trim(),
  };
}

export async function listRulePresets(password) {
  const resp = await fetch('/api/rules/admin/presets', {
    headers: adminHeaders(password),
    cache: 'no-store',
  });
  return parseJSONResponse(resp);
}

export async function saveRulePreset(password, preset) {
  const resp = await fetch('/api/rules/admin/presets', {
    method: 'POST',
    headers: adminHeaders(password),
    body: JSON.stringify({
      ...preset,
      config: normalizeRuleConfig(preset.config),
    }),
  });
  return parseJSONResponse(resp);
}

export async function setActiveRulePreset(password, id) {
  const resp = await fetch('/api/rules/admin/active', {
    method: 'PUT',
    headers: adminHeaders(password),
    body: JSON.stringify({ id }),
  });
  return parseJSONResponse(resp);
}

export async function deleteRulePreset(password, id) {
  const resp = await fetch('/api/rules/admin/presets/' + encodeURIComponent(id), {
    method: 'DELETE',
    headers: adminHeaders(password),
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
