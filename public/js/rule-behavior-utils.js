// rule-behavior-utils.js — 行为规则读取 / 正则编译 / 中立文本提取的共享助手（L1 中立层）。
//
// 架构体检 S6（2026-07-10）：此前这些函数在 rule-behavior-foodtalks.js、
// rule-behavior-foodtalks-series.js、site-contact-layout.js、templates/foodtalks-site.js
// 各有一份手工同步的拷贝（compileSiteRegex 与 compileSeriesRegex 曾字节级相同），
// 收敛为唯一实现。边界约束（docs/adr/2026-07-10-share-vs-isolate-boundary.md）：
// 本文件禁止出现任何账号/渠道字面量；渠道差异只能以参数进入。
// 契约测试：tests/rule-behavior-utils.test.mjs。

// ── 正则 ──
// 正则字符串 → RegExp。识别开头 (?i) 等内联 flag（服务端 RE2 用 (?i) 表达大小写
// 不敏感，JS new RegExp 不支持内联 flag 组，这里转成构造函数 flag）。RegExp 实例透传。
export function compileBehaviorRegex(src) {
  if (src instanceof RegExp) return src;
  let s = String(src), flags = '';
  const m = s.match(/^\(\?([a-z]+)\)/);
  if (m) { flags = m[1].replace(/[^gimsuy]/g, ''); s = s.slice(m[0].length); }
  return new RegExp(s, flags);
}

// ── behavior 派签名（以 behavior 对象为参；rule-behavior-foodtalks.js 口径）──
export function behaviorTemplate(behavior, key, fallback) {
  const v = behavior && behavior.templates && behavior.templates[key];
  return (typeof v === 'string' && v) ? v : fallback;
}
export function behaviorParamStr(behavior, key, fallback) {
  const p = behavior && behavior.params && behavior.params[key];
  const v = p && p.default;
  return (typeof v === 'string' && v) ? v : fallback;
}
export function behaviorListRaw(behavior, key) {
  const v = behavior && behavior.lists && behavior.lists[key];
  return (Array.isArray(v) && v.length) ? v : null;
}

// ── config 派签名（以完整 config 为参；series / site-contact / foodtalks-site 口径）──
export function behOf(config) { return (config && config.behavior) || {}; }
export function behTemplate(config, key, fallback) {
  return behaviorTemplate(behOf(config), key, fallback);
}
export function behParamStr(config, key, fallback) {
  return behaviorParamStr(behOf(config), key, fallback);
}
export function behList(config, key, fallback) {
  const v = behaviorListRaw(behOf(config), key);
  return v !== null ? v : fallback;
}
// 单条正则：优先 behavior.params.<key>.default，非法/缺省降级编译 fallback 字符串。
export function behaviorRegex(config, key, fallbackStr) {
  const src = behParamStr(config, key, fallbackStr);
  try { return compileBehaviorRegex(src); } catch (e) { return compileBehaviorRegex(fallbackStr); }
}

// ── 中立文本提取 ──
export function runsToText(runs) {
  return (runs || [])
    .filter(r => r && (r.type === 'txt' || r.type === 'link'))
    .map(r => r.text || '')
    .join('');
}
export function elemText(e) {
  if (!e) return '';
  if (Array.isArray(e.runs)) return runsToText(e.runs);
  return typeof e.text === 'string' ? e.text : '';
}
export function stripSectionPrefix(text) {
  return String(text || '').replace(/^\s*(?:[一二三四五六七八九十]+|[0-9]+)\s*[、.．]\s*/, '').trim();
}
export function hasSectionPrefix(text) {
  const raw = String(text || '').trim();
  return !!raw && stripSectionPrefix(raw) !== raw;
}
