const MOTE_REF_HEAD_IMAGE_URL = 'https://fbif-feishu-base.oss-cn-shanghai.aliyuncs.com/fbif-attachment-to-url/2026/05/tblMQeXvSGd7Hebf_-CkOuPdpNzb-_3u9Rxc5KA_1778028720411/img_v3_0211e_8b79127d-72a5-4011-9f01-fe3b30c7548g_1778028720565.png';

export const DEFAULT_BEHAVIOR = Object.freeze({
  schema_version: 1,
  params: Object.freeze({
    mote_ref_image_url: Object.freeze({
      type: 'url',
      required: true,
      default: MOTE_REF_HEAD_IMAGE_URL,
    }),
  }),
  conditions: Object.freeze([
    Object.freeze({
      id: 'is_mote_author',
      all: Object.freeze([
        Object.freeze({ path: 'article.author_normalized', op: 'eq', value: 'mote莫特' }),
      ]),
    }),
  ]),
  actions: Object.freeze([
    Object.freeze({
      when: 'is_mote_author',
      type: 'insert_image_before',
      target: 'first_ref_heading',
      src_param: 'mote_ref_image_url',
      class: 'wx-mote-author',
    }),
  ]),
  templates: Object.freeze({
    author_line: '作者：{{ article.author }}',
    source_line: '来源：{{ article.source }}',
    footer_original_notice: '* 本文为{{ account.name }}原创，欢迎转发朋友圈；',
    footer_repost_notice: '* 本文为转载文章。',
  }),
  output_contracts: Object.freeze({
    author_line: Object.freeze({ max_length: 80, allow_html: false }),
    source_line: Object.freeze({ max_length: 120, allow_html: false }),
  }),
});

const ALLOWED_ACTION_TYPES = new Set(['insert_image_before']);
const ALLOWED_TARGETS = new Set(['first_ref_heading']);
const ALLOWED_OPS = new Set(['eq', 'neq', 'exists', 'includes']);
const ALLOWED_INSERT_CLASSES = new Set(['wx-mote-author']);
const ALLOWED_TOP_LEVEL_KEYS = new Set([
  'schema_version',
  'params',
  'conditions',
  'actions',
  'templates',
  'output_contracts',
]);
const UNSAFE_URL_CHARS = /[\u0000-\u0020\u007f"'<>\\]/;

function isPlainObject(value) {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function cloneJSON(value) {
  return JSON.parse(JSON.stringify(value));
}

export function normalizeAuthorName(author) {
  return String(author || '').replace(/\s+/g, '').toLowerCase();
}

export function isSafeBehaviorURL(value) {
  const source = String(value || '').trim();
  if (!source || UNSAFE_URL_CHARS.test(source)) return false;
  if (source.startsWith('/')) return !source.startsWith('//');
  try {
    const parsed = new URL(source);
    return parsed.protocol === 'https:' && !!parsed.hostname && !parsed.username && !parsed.password;
  } catch (_err) {
    return false;
  }
}

export function normalizeRuleBehavior(raw = {}) {
  const input = isPlainObject(raw) ? raw : {};
  return {
    schema_version: 1,
    params: { ...cloneJSON(DEFAULT_BEHAVIOR.params), ...(isPlainObject(input.params) ? input.params : {}) },
    conditions: Array.isArray(input.conditions) ? input.conditions : cloneJSON(DEFAULT_BEHAVIOR.conditions),
    actions: Array.isArray(input.actions) ? input.actions : cloneJSON(DEFAULT_BEHAVIOR.actions),
    templates: { ...cloneJSON(DEFAULT_BEHAVIOR.templates), ...(isPlainObject(input.templates) ? input.templates : {}) },
    output_contracts: {
      ...cloneJSON(DEFAULT_BEHAVIOR.output_contracts),
      ...(isPlainObject(input.output_contracts) ? input.output_contracts : {}),
    },
  };
}

export function getBehaviorParam(behavior, key) {
  const normalized = normalizeRuleBehavior(behavior);
  const spec = normalized.params[key];
  return spec && spec.default != null ? String(spec.default) : '';
}

export function getPath(input, path) {
  return String(path || '').split('.').filter(Boolean).reduce((value, key) => {
    if (value == null) return undefined;
    return value[key];
  }, input);
}

function validateParam(key, spec, errors) {
  if (!isPlainObject(spec)) {
    errors.push('参数必须是对象：' + key);
    return;
  }
  if (!['url', 'string', 'boolean', 'number'].includes(spec.type)) {
    errors.push('未知参数类型：' + key);
  }
  const value = String(spec.default || '').trim();
  if (spec.required && !value) {
    errors.push('缺少必填参数默认值：' + key);
  }
  if (spec.type === 'url' && value && !isSafeBehaviorURL(value)) {
    errors.push('URL 参数只允许站内路径或 HTTPS：' + key);
  }
}

function validateCondition(condition, errors) {
  if (!isPlainObject(condition)) {
    errors.push('条件必须是对象');
    return;
  }
  if (!String(condition.id || '').trim()) errors.push('条件缺少 id');
  const groups = ['all', 'any'].filter(key => condition[key] != null);
  if (groups.length !== 1) {
    errors.push('条件必须且只能包含 all 或 any：' + String(condition.id || '未命名'));
    return;
  }
  const entries = condition[groups[0]];
  if (!Array.isArray(entries) || !entries.length) {
    errors.push('条件列表不能为空：' + String(condition.id || '未命名'));
    return;
  }
  for (const entry of entries) {
    if (!isPlainObject(entry)) {
      errors.push('条件项必须是对象：' + String(condition.id || '未命名'));
      continue;
    }
    if (!String(entry.path || '').trim()) errors.push('条件项缺少 path：' + String(condition.id || '未命名'));
    if (!ALLOWED_OPS.has(entry.op)) errors.push('未知条件操作符：' + String(entry.op || ''));
  }
}

function validateAction(action, conditionIDs, params, errors) {
  if (!isPlainObject(action)) {
    errors.push('动作必须是对象');
    return;
  }
  if (!conditionIDs.has(action.when)) errors.push('动作引用了不存在的条件：' + String(action.when || ''));
  if (!ALLOWED_ACTION_TYPES.has(action.type)) errors.push('未知动作类型：' + String(action.type || ''));
  if (!ALLOWED_TARGETS.has(action.target)) errors.push('未知动作目标：' + String(action.target || ''));
  const srcParam = String(action.src_param || '').trim();
  if (action.type === 'insert_image_before' && !srcParam) {
    errors.push('插图动作缺少 src_param');
  }
  if (action.type === 'insert_image_before' && srcParam && !Object.prototype.hasOwnProperty.call(params, srcParam)) {
    errors.push('动作引用了不存在的参数：' + srcParam);
  }
}

function validateTemplate(slot, template, contract, errors) {
  const source = String(template == null ? '' : template);
  const maxLength = Number(contract && contract.max_length) > 0 ? Number(contract.max_length) : 2000;
  if (source.length > maxLength || source.length > 2000) errors.push('模板过长：' + slot);
  if (/<script|on\w+\s*=|javascript:/i.test(source)) errors.push('模板包含不允许的 HTML 或脚本：' + slot);
}

function validateOutputContracts(contracts, errors) {
  for (const [slot, contract] of Object.entries(contracts || {})) {
    if (!isPlainObject(contract)) {
      errors.push('输出约束必须是对象：' + slot);
      continue;
    }
    const rawMaxLength = contract.max_length;
    const maxLength = Number(rawMaxLength);
    if (rawMaxLength != null && (!Number.isFinite(maxLength) || maxLength < 0 || maxLength > 2000)) {
      errors.push('输出约束长度无效：' + slot);
    }
    if (contract.allow_html === true) {
      errors.push('模板不允许开启 HTML：' + slot);
    }
  }
}

function validateRawBehaviorShape(raw, errors) {
  if (raw == null) return;
  if (!isPlainObject(raw)) {
    errors.push('代码化规则必须是对象');
    return;
  }
  for (const key of Object.keys(raw)) {
    if (!ALLOWED_TOP_LEVEL_KEYS.has(key)) errors.push('未知行为规则字段：' + key);
  }
  if (raw.params != null && !isPlainObject(raw.params)) errors.push('行为参数必须是对象');
  if (raw.templates != null && !isPlainObject(raw.templates)) errors.push('行为模板必须是对象');
  if (raw.output_contracts != null && !isPlainObject(raw.output_contracts)) errors.push('行为输出约束必须是对象');
  if (raw.conditions != null && !Array.isArray(raw.conditions)) errors.push('行为条件必须是数组');
  if (raw.actions != null && !Array.isArray(raw.actions)) errors.push('行为动作必须是数组');
}

export function validateRuleBehavior(raw) {
  const behavior = normalizeRuleBehavior(raw);
  const errors = [];
  validateRawBehaviorShape(raw, errors);
  if (raw && isPlainObject(raw) && raw.schema_version != null && Number(raw.schema_version) !== 1) {
    errors.push('不支持的行为规则版本：' + raw.schema_version);
  }
  for (const [key, spec] of Object.entries(behavior.params)) validateParam(key, spec, errors);
  for (const condition of behavior.conditions) validateCondition(condition, errors);
  const conditionIDs = new Set(behavior.conditions.map(item => item && item.id).filter(Boolean));
  for (const action of behavior.actions) validateAction(action, conditionIDs, behavior.params, errors);
  validateOutputContracts(behavior.output_contracts, errors);
  for (const [slot, template] of Object.entries(behavior.templates)) {
    validateTemplate(slot, template, behavior.output_contracts[slot], errors);
  }
  return { ok: errors.length === 0, errors, behavior };
}

function compareCondition(entry, context) {
  const value = getPath(context, entry.path);
  if (entry.op === 'exists') return value != null && value !== '';
  if (entry.op === 'eq') return String(value ?? '') === String(entry.value ?? '');
  if (entry.op === 'neq') return String(value ?? '') !== String(entry.value ?? '');
  if (entry.op === 'includes') return String(value ?? '').includes(String(entry.value ?? ''));
  return false;
}

function evaluateCondition(condition, context) {
  const group = Array.isArray(condition.all) ? 'all' : 'any';
  const entries = condition[group] || [];
  return group === 'all'
    ? entries.every(entry => compareCondition(entry, context))
    : entries.some(entry => compareCondition(entry, context));
}

export function renderBehaviorTemplate(template, context) {
  return String(template || '').replace(/\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}/g, (_, path) => {
    const value = getPath(context, path);
    return value == null ? '' : String(value);
  });
}

function findInsertIndex(elems, target) {
  if (target === 'first_ref_heading') return elems.findIndex(item => item && item.k === 'refH');
  return -1;
}

function safeInsertClass(action) {
  const className = String(action && action.class || '').trim();
  return ALLOWED_INSERT_CLASSES.has(className) ? className : '';
}

export function applyBehaviorToElems(elems, context, rawBehavior) {
  const behavior = normalizeRuleBehavior(rawBehavior);
  const conditionState = new Map(
    behavior.conditions.map(condition => [condition.id, evaluateCondition(condition, context)])
  );
  let out = Array.isArray(elems) ? elems.slice() : [];
  for (const action of behavior.actions) {
    if (!conditionState.get(action.when)) continue;
    if (action.type !== 'insert_image_before') continue;
    const idx = findInsertIndex(out, action.target);
    const src = getBehaviorParam(behavior, action.src_param);
    if (idx < 0 || !src) continue;
    const target = out[idx] || {};
    const className = safeInsertClass(action);
    const inserted = { k: 'img', src, w: '100%', maxPx: 0 };
    if (className) {
      inserted.className = className;
      inserted.role = className === 'wx-mote-author' ? 'mote-author-card' : '';
    }
    if (target.blankBefore) inserted.blankBefore = target.blankBefore;
    const nextTarget = target.blankBefore ? { ...target, blankBefore: false } : target;
    out = out.slice(0, idx).concat([inserted, nextTarget], out.slice(idx + 1));
  }
  return out;
}
