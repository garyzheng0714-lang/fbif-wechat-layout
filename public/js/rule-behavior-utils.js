// rule-behavior-utils.js — 行为规则读取 / 正则编译 / 中立文本提取的共享助手（L1 中立层）。
//
// 架构体检 S6（2026-07-10）：此前这些函数在 rule-behavior-foodtalks.js、
// rule-behavior-foodtalks-series.js、site-contact-layout.js、templates/foodtalks-site.js
// 各有一份手工同步的拷贝（compileSiteRegex 与 compileSeriesRegex 曾字节级相同），
// 收敛为唯一实现。边界约束（docs/adr/2026-07-10-share-vs-isolate-boundary.md）：
// 本文件禁止出现任何账号/渠道字面量；渠道差异只能以参数进入。
// 契约测试：tests/rule-behavior-utils.test.mjs。

// ── 正则 ──
// 规则正则由 Go/RE2 校验、由浏览器回溯引擎执行。两者并不具有相同的最坏时间复杂度，
// 因此这里与服务端共同限定一个保守安全子集，并拒绝超长输入。这样旧数据即使绕过保存
// 校验，也不能用 (a+)+、(a|aa)+ 或多个 .* 把编辑器主线程卡死。
export const MAX_BEHAVIOR_REGEX_LENGTH = 300;
export const MAX_BEHAVIOR_REGEX_INPUT = 4096;

function regexKey(key) {
  const source = String(key || '').toLowerCase();
  return source.includes('regex') || source.includes('pattern') ||
    source.includes('marker') || source.includes('signature');
}

function normalizeInlineFlags(source) {
  let pattern = source;
  let flags = '';
  const match = pattern.match(/^\(\?([a-z]+)\)/);
  if (match) {
    if (!/^[ims]+$/.test(match[1])) {
      throw new Error('只支持开头的 (?i)、(?m) 或 (?s) 标志');
    }
    flags = Array.from(new Set(match[1])).join('');
    pattern = pattern.slice(match[0].length);
  }
  return { pattern, flags };
}

function parseBraceQuantifier(source, start) {
  let i = start + 1;
  const minStart = i;
  while (i < source.length && /[0-9]/.test(source[i])) i++;
  if (i === minStart) return null;
  const min = Number(source.slice(minStart, i));
  if (source[i] === '}') return { variable: false, unbounded: false, max: min, end: i + 1 };
  if (source[i] !== ',') return null;
  i++;
  const maxStart = i;
  while (i < source.length && /[0-9]/.test(source[i])) i++;
  if (source[i] !== '}') return null;
  if (i === maxStart) return { variable: true, unbounded: true, max: 0, end: i + 1 };
  const max = Number(source.slice(maxStart, i));
  return { variable: min !== max, unbounded: false, max, end: i + 1 };
}

function isWildcardClass(source) {
  return source.startsWith('[^') ||
    (source.includes('\\s') && source.includes('\\S')) ||
    (source.includes('\\d') && source.includes('\\D')) ||
    (source.includes('\\w') && source.includes('\\W'));
}

function assertSafeRegexStructure(source) {
  const frames = [{ hasVariableRepeat: false, hasAlternation: false, hasWildcard: false, last: null }];
  let unboundedWildcards = 0;

  const applyQuantifier = (frame, quantifier) => {
    if (!frame.last) throw new Error('量词前必须有可匹配内容');
    const repeatsMoreThanOnce = quantifier.unbounded || quantifier.max > 1;
    if (frame.last.group && repeatsMoreThanOnce &&
        (frame.last.hasVariableRepeat || frame.last.hasAlternation)) {
      throw new Error('不允许嵌套量词或带分支的重复组');
    }
    if (frame.last.wildcard && quantifier.unbounded && ++unboundedWildcards > 1) {
      throw new Error('不允许多个无上界通配符');
    }
    if (quantifier.variable) frame.hasVariableRepeat = true;
  };

  for (let i = 0; i < source.length;) {
    let frame = frames[frames.length - 1];
    const ch = source[i];
    if (ch === '\\') {
      if (i + 1 >= source.length) throw new Error('正则不能以转义符结尾');
      if (/[1-9k]/.test(source[i + 1])) throw new Error('不支持反向引用');
      frame.last = { group: false, wildcard: false };
      i += 2;
      continue;
    }
    if (ch === '[') {
      let end = i + 1;
      while (end < source.length) {
        if (source[end] === '\\') { end += 2; continue; }
        if (source[end] === ']') break;
        end++;
      }
      if (end >= source.length) throw new Error('字符组没有闭合');
      const wildcard = isWildcardClass(source.slice(i, end + 1));
      frame.hasWildcard ||= wildcard;
      frame.last = { group: false, wildcard };
      i = end + 1;
      continue;
    }
    if (ch === '(') {
      if (source[i + 1] === '?') {
        if (source[i + 2] !== ':') throw new Error('(? 后只允许非捕获组 (?:...)');
        i += 3;
      } else {
        i++;
      }
      frames.push({ hasVariableRepeat: false, hasAlternation: false, hasWildcard: false, last: null });
      continue;
    }
    if (ch === ')') {
      if (frames.length === 1) throw new Error('存在多余的右括号');
      const closed = frames.pop();
      frame = frames[frames.length - 1];
      frame.hasVariableRepeat ||= closed.hasVariableRepeat;
      frame.hasAlternation ||= closed.hasAlternation;
      frame.hasWildcard ||= closed.hasWildcard;
      frame.last = {
        group: true,
        wildcard: closed.hasWildcard,
        hasVariableRepeat: closed.hasVariableRepeat,
        hasAlternation: closed.hasAlternation,
      };
      i++;
      continue;
    }
    if (ch === '|') {
      frame.hasAlternation = true;
      frame.last = null;
      i++;
      continue;
    }
    if (ch === '*' || ch === '+' || ch === '?') {
      applyQuantifier(frame, {
        variable: true,
        unbounded: ch === '*' || ch === '+',
        max: 1,
      });
      i++;
      if (source[i] === '?') i++; // 懒惰后缀不改变重复上界
      continue;
    }
    if (ch === '{') {
      const quantifier = parseBraceQuantifier(source, i);
      if (!quantifier) {
        frame.last = { group: false, wildcard: false };
        i++;
        continue;
      }
      applyQuantifier(frame, quantifier);
      i = quantifier.end;
      if (source[i] === '?') i++;
      continue;
    }
    if (ch === '.') {
      frame.hasWildcard = true;
      frame.last = { group: false, wildcard: true };
      i++;
      continue;
    }
    if (ch === '^' || ch === '$') {
      frame.last = null;
      i++;
      continue;
    }
    frame.last = { group: false, wildcard: false };
    i++;
  }
  if (frames.length !== 1) throw new Error('存在未闭合的分组');
}

export function validateBehaviorRegexSource(src) {
  const source = String(src);
  if (Array.from(source).length > MAX_BEHAVIOR_REGEX_LENGTH) {
    throw new Error(`正则不能超过 ${MAX_BEHAVIOR_REGEX_LENGTH} 个字符`);
  }
  const normalized = normalizeInlineFlags(source);
  assertSafeRegexStructure(normalized.pattern);
  // 最后交给原生编译器检查普通语法错误；安全检查不能替代语法检查。
  new RegExp(normalized.pattern, normalized.flags);
  return normalized;
}

class BoundedBehaviorRegExp extends RegExp {
  exec(input) {
    const source = String(input);
    if (source.length > MAX_BEHAVIOR_REGEX_INPUT) {
      this.lastIndex = 0;
      return null;
    }
    return super.exec(source);
  }
}

// 正则字符串 → 受限 RegExp。识别开头 (?i) 等 RE2 内联 flag；可信代码中的
// RegExp 常量保持透传，规则数据字符串则始终走安全校验和输入上限。
export function compileBehaviorRegex(src) {
  if (src instanceof RegExp) return src;
  const { pattern, flags } = validateBehaviorRegexSource(src);
  return new BoundedBehaviorRegExp(pattern, flags);
}

const reportedRegexErrors = new Set();
export function tryCompileBehaviorRegex(src, path = 'behavior.regex') {
  try {
    return compileBehaviorRegex(src);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    const reportKey = `${path}:${message}`;
    if (!reportedRegexErrors.has(reportKey)) {
      reportedRegexErrors.add(reportKey);
      globalThis.console?.error?.(`[rule-regex] ${path}: ${message}`);
    }
    return null;
  }
}

export function validateBehaviorRegexDocument(raw) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return true;
  for (const [key, value] of Object.entries(raw)) {
    if (!regexKey(key) || typeof value !== 'string') continue;
    try { validateBehaviorRegexSource(value); }
    catch (error) { throw new Error(`behavior.${key}: ${error.message}`); }
  }
  if (raw.params && typeof raw.params === 'object' && !Array.isArray(raw.params)) {
    for (const [key, spec] of Object.entries(raw.params)) {
      if (!regexKey(key) || !spec || typeof spec !== 'object' || Array.isArray(spec) || spec.default == null) continue;
      if (typeof spec.default !== 'string') throw new Error(`behavior.params.${key}.default: 正则必须是字符串`);
      try { validateBehaviorRegexSource(spec.default); }
      catch (error) { throw new Error(`behavior.params.${key}.default: ${error.message}`); }
    }
  }
  if (raw.lists && typeof raw.lists === 'object' && !Array.isArray(raw.lists)) {
    for (const [key, items] of Object.entries(raw.lists)) {
      if (!regexKey(key)) continue;
      if (!Array.isArray(items)) throw new Error(`behavior.lists.${key}: 正则列表必须是数组`);
      items.forEach((item, index) => {
        if (typeof item !== 'string') throw new Error(`behavior.lists.${key}[${index}]: 正则必须是字符串`);
        try { validateBehaviorRegexSource(item); }
        catch (error) { throw new Error(`behavior.lists.${key}[${index}]: ${error.message}`); }
      });
    }
  }
  return true;
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
  return tryCompileBehaviorRegex(src, `behavior.params.${key}.default`) || compileBehaviorRegex(fallbackStr);
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
// 原文单独一行的章节号（「01」「1」「PART 2」）：抓取把它和下一行标题认成两个小标题
// （api/wechat_blocks.go sectionNumberTitle）。FoodTalks 网站去掉号，FoodTalks食品资讯换成序号图。
const SECTION_NUMBER_RE = /^(?:part\s*\.?\s*)?(?:0?\d{1,2}|[一二三四五六七八九十]{1,3})$/i;
export function isSectionNumberHeading(e, next) {
  return !!e && e.k === 'h' && !!next && next.k === 'h' && SECTION_NUMBER_RE.test(elemText(e).trim());
}
export function stripSectionPrefix(text) {
  return String(text || '').replace(/^\s*(?:[一二三四五六七八九十]+|[0-9]+)\s*[、.．]\s*/, '').trim();
}
export function hasSectionPrefix(text) {
  const raw = String(text || '').trim();
  return !!raw && stripSectionPrefix(raw) !== raw;
}
