// behavior-form-editor.js — 规则后台「代码化规则(behavior)」的结构化表单（治怕配错）。
//
// 增量式：把最常编辑、最像「一堆代码」的两块——句式模板(templates) 与 参数(params，含识别正则)
// ——做成带标签 + tooltip + 内联校验的表单；conditions/actions（Mote 署名图等条件动作）保持在
// 标签内既有的「原始 JSON」高级区编辑，不在此暴露任意结构。表单改动经 onChange 回写整份 behavior，
// 未触及的 conditions/actions/output_contracts 原样保留（round-trip 不丢数据）。
//
// 中性：不 import 渲染器，只做表单与回写。

import { compileBehaviorRegex } from '../rule-behavior-utils.js';

function isObj(v) { return !!v && typeof v === 'object' && !Array.isArray(v); }
function esc(s) {
  return String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
function clone(v) { return JSON.parse(JSON.stringify(v == null ? null : v)); }

// 模板里出现的变量占位符（{{ article.author }} 等），抽出来给运营看可用变量。
function varsIn(str) {
  const out = [];
  const re = /\{\{\s*([a-z0-9_.]+)\s*\}\}/gi;
  let m;
  while ((m = re.exec(String(str || '')))) out.push(m[1]);
  return Array.from(new Set(out));
}

// 看起来像正则的参数键（含 _regex 或值含正则元字符）→ 提供「试匹配」。
function looksLikeRegex(key, val) {
  return /regex/i.test(key) || /[\\^$.*+?()[\]{}|]/.test(String(val || ''));
}

const STYLE_ID = 'bfe-style';
function ensureStyle(doc) {
  if (doc.getElementById(STYLE_ID)) return;
  const st = doc.createElement('style');
  st.id = STYLE_ID;
  st.textContent = `
.bfe-wrap{font-size:13px;color:var(--fs-text-1,#1f2329);margin-bottom:12px;}
.bfe-sec{margin-bottom:14px;}
.bfe-sec-h{font-weight:600;margin-bottom:8px;}
.bfe-field{margin-bottom:10px;}
.bfe-flabel{display:flex;align-items:center;gap:6px;margin-bottom:4px;color:var(--fs-text-2,#646a73);font-size:12px;}
.bfe-key{font-family:ui-monospace,monospace;}
.bfe-input,.bfe-textarea{width:100%;box-sizing:border-box;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);padding:5px 8px;font-size:13px;font-family:inherit;}
.bfe-textarea{min-height:48px;resize:vertical;font-family:ui-monospace,monospace;font-size:12px;}
.bfe-tip{color:var(--fs-text-3,#8f959e);font-size:11px;margin-top:3px;}
.bfe-vars span{display:inline-block;background:var(--fs-bg-sunken,#f2f3f5);color:var(--fs-text-2,#646a73);border-radius:9999px;padding:0 6px;margin:2px 4px 0 0;font-size:11px;font-family:ui-monospace,monospace;}
.bfe-try{display:flex;gap:6px;align-items:center;margin-top:4px;}
.bfe-try input{flex:1;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);padding:3px 6px;font-size:12px;}
.bfe-try-r{font-size:12px;}
.bfe-ok{color:var(--fs-success,#00a870);}
.bfe-no{color:var(--fs-danger,#f54a45);}
`;
  (doc.head || doc.documentElement).appendChild(st);
}

// createBehaviorFormEditor 把 behavior 的 templates / params 渲染成表单。
//   container, behavior（完整对象）, onChange(behavior), doc
export function createBehaviorFormEditor({ container, behavior, onChange, doc = document }) {
  if (!container) return null;
  ensureStyle(doc);
  let bh = isObj(behavior) ? clone(behavior) : {};
  if (!isObj(bh.templates)) bh.templates = {};
  if (!isObj(bh.params)) bh.params = {};

  function emit() { if (onChange) onChange(clone(bh)); }
  function setTemplate(key, val) { bh.templates[key] = val; emit(); }
  function setParam(key, val) {
    const p = bh.params[key];
    if (isObj(p)) p.default = val; else bh.params[key] = val;
    emit();
  }
  function paramValue(p) { return isObj(p) ? (p.default == null ? '' : p.default) : (p == null ? '' : p); }

  function templateField(key, val) {
    const wrap = doc.createElement('div'); wrap.className = 'bfe-field';
    const label = doc.createElement('div'); label.className = 'bfe-flabel';
    label.innerHTML = `<span class="bfe-key">${esc(key)}</span>`;
    wrap.appendChild(label);
    const ta = doc.createElement('textarea'); ta.className = 'bfe-textarea'; ta.value = String(val == null ? '' : val);
    ta.oninput = () => setTemplate(key, ta.value);
    wrap.appendChild(ta);
    const vars = varsIn(val);
    if (vars.length) {
      const v = doc.createElement('div'); v.className = 'bfe-tip bfe-vars';
      v.innerHTML = '可用变量：' + vars.map(x => `<span>{{ ${esc(x)} }}</span>`).join('');
      wrap.appendChild(v);
    }
    return wrap;
  }

  function paramField(key, spec) {
    const val = paramValue(spec);
    const type = isObj(spec) ? (spec.type || 'string') : 'string';
    const wrap = doc.createElement('div'); wrap.className = 'bfe-field';
    const label = doc.createElement('div'); label.className = 'bfe-flabel';
    label.innerHTML = `<span class="bfe-key">${esc(key)}</span><span class="bfe-tip">${esc(type)}</span>`;
    wrap.appendChild(label);
    const input = doc.createElement('input'); input.className = 'bfe-input'; input.type = 'text'; input.value = String(val);
    input.oninput = () => setParam(key, input.value);
    wrap.appendChild(input);
    if (looksLikeRegex(key, val)) {
      const tryWrap = doc.createElement('div'); tryWrap.className = 'bfe-try';
      const sample = doc.createElement('input'); sample.placeholder = '粘一行文本试匹配…';
      const result = doc.createElement('span'); result.className = 'bfe-try-r';
      const run = () => {
        const pat = input.value, txt = sample.value;
        if (!txt) { result.textContent = ''; return; }
        try { const ok = compileBehaviorRegex(pat).test(txt); result.textContent = ok ? '✓ 命中' : '✗ 不匹配'; result.className = 'bfe-try-r ' + (ok ? 'bfe-ok' : 'bfe-no'); }
        catch (e) { result.textContent = '正则不安全或语法错误'; result.className = 'bfe-try-r bfe-no'; }
      };
      sample.oninput = run; input.addEventListener('input', run);
      tryWrap.appendChild(sample); tryWrap.appendChild(result);
      wrap.appendChild(tryWrap);
    }
    return wrap;
  }

  function render() {
    container.innerHTML = '';
    const wrap = doc.createElement('div'); wrap.className = 'bfe-wrap';
    const intro = doc.createElement('div'); intro.className = 'bfe-tip';
    intro.textContent = '常用规则（句式模板 / 识别参数）在此可视化编辑；条件与动作（如 Mote 署名图）仍在下方「原始 JSON」高级区。';
    wrap.appendChild(intro);

    const tKeys = Object.keys(bh.templates);
    if (tKeys.length) {
      const sec = doc.createElement('div'); sec.className = 'bfe-sec';
      const h = doc.createElement('div'); h.className = 'bfe-sec-h'; h.textContent = '句式模板'; sec.appendChild(h);
      for (const k of tKeys) sec.appendChild(templateField(k, bh.templates[k]));
      wrap.appendChild(sec);
    }
    const pKeys = Object.keys(bh.params);
    if (pKeys.length) {
      const sec = doc.createElement('div'); sec.className = 'bfe-sec';
      const h = doc.createElement('div'); h.className = 'bfe-sec-h'; h.textContent = '识别参数（含正则）'; sec.appendChild(h);
      for (const k of pKeys) sec.appendChild(paramField(k, bh.params[k]));
      wrap.appendChild(sec);
    }
    container.appendChild(wrap);
  }

  render();
  return { getBehavior: () => clone(bh), destroy: () => { container.innerHTML = ''; } };
}
