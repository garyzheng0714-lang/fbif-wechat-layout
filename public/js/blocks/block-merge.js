// block-merge.js — 底部区块「两层合并」中性纯函数（后台永久默认 ← 前台单篇覆盖）。
//
// 借鉴 Figma/Webflow 的「主组件默认 vs 实例逐字段覆盖」模型：
//   - 后台 config.modules.bottom_blocks = 主组件默认（order + 每块 enabled 默认 + 每块字段默认值）；
//   - 前台单篇 override 只存 diff = { enabled?, order?, overrides:{ <id>:{ enabled?, fields?:{<key>:val} } } }；
//   - 未被覆盖的 enabled/order/字段实时跟随后台默认（per-property selective override）。
//
// 本文件无 DOM、无 HTML 拼接、不 import 任何渠道渲染器——可安全被公众号与网站两侧共用（红线 #19）。
// 渲染由各渠道渲染器消费 resolveBlocks 的输出（flattenFields 取值），本文件只负责合并与覆盖标记。

import { defaultFieldsForType } from './block-registry.gen.js';

function isObj(v) {
  return !!v && typeof v === 'object' && !Array.isArray(v);
}

function sameArray(a, b) {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

// dedupe 保序去重——区块 id 在解析顺序里必须唯一，否则同一区块会被渲染多次
// （asset 重复解析、DOM 重复元素）。后台校验也拒绝重复，这里是前台覆盖的防御兜底。
function dedupe(arr) {
  const seen = new Set();
  const out = [];
  for (const x of arr) {
    if (!seen.has(x)) {
      seen.add(x);
      out.push(x);
    }
  }
  return out;
}

// resolveOneBlock 合并单个区块的默认与覆盖，输出字段级 {value, default, overridden}。
function resolveOneBlock(id, base, ov) {
  base = isObj(base) ? base : {};
  ov = isObj(ov) ? ov : {};
  const type = base.type || '';
  // enabled 契约：布尔值，缺省视为启用，仅显式 false 关闭。后台对 base.enabled 已校验为布尔；
  // 前台 override.enabled 在此严格 typeof 守卫，非布尔一律忽略（不让脏数据翻转开关）。
  const enabledDefault = base.enabled !== false;
  const enabledOv = typeof ov.enabled === 'boolean' ? ov.enabled : null;
  const enabled = enabledOv == null ? enabledDefault : enabledOv;

  // 字段默认值优先级：区块类型 schema 默认 < 后台 base.fields < 前台 override.fields。
  // key 集合取三者并集——override 独有的字段也必须出现在结果里（否则单篇覆盖被静默丢弃）。
  const schemaDefaults = defaultFieldsForType(type);
  const baseFields = isObj(base.fields) ? base.fields : {};
  const ovFields = isObj(ov.fields) ? ov.fields : {};
  const keys = new Set([...Object.keys(schemaDefaults), ...Object.keys(baseFields), ...Object.keys(ovFields)]);
  const fields = {};
  for (const k of keys) {
    const def = (k in baseFields) ? baseFields[k] : schemaDefaults[k];
    const overridden = (k in ovFields);
    fields[k] = { value: overridden ? ovFields[k] : def, default: def, overridden };
  }
  return {
    id,
    type,
    enabled,
    enabledDefault,
    enabledOverridden: enabledOv != null && enabledOv !== enabledDefault,
    fields,
  };
}

// resolveBlocks 合并后台默认模块与前台单篇覆盖，返回按解析顺序排列的区块列表。
//   defaultModule: config.modules.bottom_blocks = { enabled, order:[id], blocks:{ id:{type,enabled,fields} }, presets }
//   override:      { enabled?, order?, overrides:{ id:{enabled?, fields?} } }
// 返回：{ enabled, enabledOverridden, orderOverridden, blocks:[ resolvedBlock ] }
export function resolveBlocks(defaultModule, override) {
  const mod = isObj(defaultModule) ? defaultModule : {};
  const ov = isObj(override) ? override : {};
  const baseBlocks = isObj(mod.blocks) ? mod.blocks : {};
  const ovBlocks = isObj(ov.overrides) ? ov.overrides : {};

  // 解析顺序：后台 order 过滤掉不存在的块 + 保序去重；缺省用 blocks 的键序。
  const baseOrder = dedupe(
    Array.isArray(mod.order) ? mod.order.filter(id => baseBlocks[id]) : Object.keys(baseBlocks)
  );

  let order = baseOrder;
  let orderOverridden = false;
  if (Array.isArray(ov.order) && ov.order.length) {
    const filtered = dedupe(ov.order.filter(id => baseBlocks[id]));
    // 后台在用户存了覆盖之后新增的块，追加到末尾（不丢新块）。
    const missing = baseOrder.filter(id => !filtered.includes(id));
    order = dedupe([...filtered, ...missing]);
    orderOverridden = !sameArray(order, baseOrder);
  }

  const moduleEnabled = mod.enabled !== false;
  const enabledOv = typeof ov.enabled === 'boolean' ? ov.enabled : null;
  const enabled = enabledOv == null ? moduleEnabled : enabledOv;

  const blocks = order.map(id => resolveOneBlock(id, baseBlocks[id], ovBlocks[id]));
  return {
    enabled,
    enabledOverridden: enabledOv != null && enabledOv !== moduleEnabled,
    orderOverridden,
    blocks,
  };
}

// flattenFields 把已解析区块的字段降成纯 { key: value } 映射，供渠道渲染器取值。
export function flattenFields(block) {
  const out = {};
  const fields = (block && block.fields) || {};
  for (const k in fields) out[k] = fields[k].value;
  return out;
}

// hasAnyOverride 报告该单篇覆盖是否含任何有效改动（用于「全部恢复默认」按钮显隐）。
export function hasAnyOverride(override) {
  const ov = isObj(override) ? override : {};
  if (typeof ov.enabled === 'boolean') return true;
  if (Array.isArray(ov.order) && ov.order.length) return true;
  const ovBlocks = isObj(ov.overrides) ? ov.overrides : {};
  for (const id in ovBlocks) {
    const b = ovBlocks[id];
    if (!isObj(b)) continue;
    if (typeof b.enabled === 'boolean') return true;
    if (isObj(b.fields) && Object.keys(b.fields).length) return true;
  }
  return false;
}
