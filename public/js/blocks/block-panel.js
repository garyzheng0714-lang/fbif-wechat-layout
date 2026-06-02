// block-panel.js — 前台「底部区块」单篇面板（公众号 + 网站共用）。
//
// 两层模型的【前台单篇】侧：后台默认（bottom_blocks 模块）只读，本面板把用户这一篇的改动
// 存成 override（localStorage `bb.override::<accountId>`），未覆盖项实时跟随后台默认。
// 交互：卡片列表 + 拖拽排序 + 显隐开关 + 展开字段表单 + 继承/已覆盖徽标 + 分层恢复默认。
//
// 中性：只 import 中性合并引擎与注册表，不 import 任何渠道渲染器（红线 #19）。
// 宿主（app.html / site.html）通过 onChange(override) 把 override 注入各自渲染配置并重渲。

import { resolveBlocks, hasAnyOverride } from './block-merge.js';
import { getBlockType } from './block-registry.gen.js';

function isObj(v) { return !!v && typeof v === 'object' && !Array.isArray(v); }
function esc(s) {
  return String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
function ovKey(accountId) { return 'bb.override::' + accountId; }
function loadOverride(accountId) {
  try { return JSON.parse(localStorage.getItem(ovKey(accountId)) || 'null') || {}; } catch { return {}; }
}
function saveOverride(accountId, ov) {
  try {
    if (hasAnyOverride(ov)) localStorage.setItem(ovKey(accountId), JSON.stringify(ov));
    else localStorage.removeItem(ovKey(accountId));
  } catch {}
}

const STYLE_ID = 'bp-style';
function ensureStyle(doc) {
  if (doc.getElementById(STYLE_ID)) return;
  const st = doc.createElement('style');
  st.id = STYLE_ID;
  st.textContent = `
.bp-wrap{font-size:13px;color:var(--fs-text-1,#1f2329);}
.bp-head{display:flex;align-items:center;justify-content:space-between;margin-bottom:8px;}
.bp-head-title{font-weight:600;}
.bp-master{display:flex;align-items:center;gap:6px;cursor:pointer;}
.bp-resetall{background:none;border:none;color:var(--fs-text-3,#8f959e);cursor:pointer;font-size:12px;text-decoration:underline;padding:0;}
.bp-resetall:hover{color:var(--fs-brand,#1456f0);}
.bp-card{border:1px solid var(--fs-border-card,#e5e6eb);border-radius:var(--fs-r-s,6px);margin-bottom:6px;background:var(--fs-bg-card,#fff);}
.bp-card.bp-disabled{opacity:.62;}
.bp-row{display:flex;align-items:center;gap:8px;padding:8px 10px;}
.bp-grip{cursor:grab;color:var(--fs-text-3,#8f959e);user-select:none;font-size:14px;line-height:1;}
.bp-name{flex:1;font-weight:500;cursor:pointer;}
.bp-name .bp-sub{font-weight:400;color:var(--fs-text-3,#8f959e);font-size:12px;margin-left:6px;}
.bp-caret{color:var(--fs-text-3,#8f959e);cursor:pointer;transition:transform .15s;font-size:11px;}
.bp-caret.bp-open{transform:rotate(90deg);}
.bp-fields{padding:2px 12px 12px 32px;border-top:1px solid var(--fs-divider,rgba(31,35,39,.08));}
.bp-field{margin-top:10px;}
.bp-flabel{display:flex;align-items:center;gap:6px;margin-bottom:4px;color:var(--fs-text-2,#646a73);font-size:12px;}
.bp-badge{font-size:11px;padding:1px 6px;border-radius:9999px;}
.bp-badge.inherit{background:var(--fs-bg-sunken,#f2f3f5);color:var(--fs-text-3,#8f959e);}
.bp-badge.over{background:var(--fs-brand-soft,#ebf1ff);color:var(--fs-brand,#1456f0);}
.bp-reset{background:none;border:none;color:var(--fs-text-3,#8f959e);cursor:pointer;font-size:11px;text-decoration:underline;padding:0;margin-left:auto;}
.bp-reset:hover{color:var(--fs-brand,#1456f0);}
.bp-input,.bp-textarea,.bp-select{width:100%;box-sizing:border-box;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);padding:5px 8px;font-size:13px;font-family:inherit;}
.bp-textarea{min-height:60px;resize:vertical;}
.bp-color{display:flex;align-items:center;gap:8px;}
.bp-color input[type=color]{width:34px;height:26px;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);padding:0;background:none;cursor:pointer;}
.bp-tip{color:var(--fs-text-3,#8f959e);font-size:11px;margin-top:3px;}
.bp-empty{color:var(--fs-text-3,#8f959e);font-size:12px;padding:6px 2px;}
`;
  (doc.head || doc.documentElement).appendChild(st);
}

// mountBlockPanel 挂载面板。
//   mount: 容器元素
//   accountId: 账号 id（决定 localStorage 覆盖 key）
//   getModule: () => 后台默认 bottom_blocks 模块（原始服务端模块）
//   onChange: (override) => void —— 宿主据此注入渲染并重渲
//   initialOverride: 可选，外部已加载的覆盖（否则从 localStorage 读）
export function mountBlockPanel({ mount, accountId, getModule, onChange, doc = document }) {
  if (!mount) return null;
  ensureStyle(doc);
  let override = loadOverride(accountId);
  const expanded = new Set();
  let dragId = '';

  function emit() { saveOverride(accountId, override); if (onChange) onChange(override); }
  function ensureBlock(id) {
    if (!isObj(override.overrides)) override.overrides = {};
    if (!isObj(override.overrides[id])) override.overrides[id] = {};
    return override.overrides[id];
  }
  function prune(id) {
    const o = override.overrides || {};
    const b = o[id];
    if (b) {
      if (isObj(b.fields) && !Object.keys(b.fields).length) delete b.fields;
      if (!('enabled' in b) && !b.fields) delete o[id];
    }
    if (override.overrides && !Object.keys(override.overrides).length) delete override.overrides;
  }

  function setMaster(val, def) {
    if (val === def) delete override.enabled; else override.enabled = val;
    emit(); render();
  }
  function setEnabled(id, val, def) {
    const b = ensureBlock(id);
    if (val === def) delete b.enabled; else b.enabled = val;
    prune(id); emit(); render();
  }
  function setField(id, key, val, def) {
    const b = ensureBlock(id);
    if (!isObj(b.fields)) b.fields = {};
    if (val === def) delete b.fields[key]; else b.fields[key] = val;
    prune(id); emit(); render();
  }
  function resetField(id, key) {
    const b = override.overrides && override.overrides[id];
    if (b && b.fields) delete b.fields[key];
    prune(id); emit(); render();
  }
  function resetBlock(id) {
    if (override.overrides) delete override.overrides[id];
    prune(id); emit(); render();
  }
  function blockHasOverride(id) {
    const b = override.overrides && override.overrides[id];
    return !!(isObj(b) && (('enabled' in b) || (isObj(b.fields) && Object.keys(b.fields).length)));
  }
  function resetAll() { override = {}; emit(); render(); }

  function reorder(fromId, toId, baseOrder) {
    const cur = Array.isArray(override.order) && override.order.length ? override.order.slice() : baseOrder.slice();
    const fi = cur.indexOf(fromId), ti = cur.indexOf(toId);
    if (fi < 0 || ti < 0 || fi === ti) return;
    cur.splice(ti, 0, cur.splice(fi, 1)[0]);
    override.order = cur;
    emit(); render();
  }

  function fieldControl(block, field) {
    const id = block.id, key = field.key;
    const rf = block.fields[key] || { value: field.default, default: field.default, overridden: false };
    const val = rf.value;
    const def = rf.default;
    const overridden = rf.overridden;
    const wrap = doc.createElement('div');
    wrap.className = 'bp-field';
    const label = doc.createElement('div');
    label.className = 'bp-flabel';
    label.innerHTML = `<span>${esc(field.label || key)}</span>` +
      `<span class="bp-badge ${overridden ? 'over' : 'inherit'}">${overridden ? '本篇已覆盖' : '继承默认'}</span>`;
    if (overridden) {
      const rb = doc.createElement('button');
      rb.type = 'button'; rb.className = 'bp-reset'; rb.textContent = '⟲ 恢复默认';
      rb.onclick = () => resetField(id, key);
      label.appendChild(rb);
    }
    wrap.appendChild(label);

    let ctrl;
    if (field.type === 'longText' || field.type === 'html_lines') {
      ctrl = doc.createElement('textarea');
      ctrl.className = 'bp-textarea';
      ctrl.value = field.type === 'html_lines' ? (Array.isArray(val) ? val.join('\n') : '') : String(val == null ? '' : val);
      ctrl.oninput = () => {
        const v = field.type === 'html_lines' ? ctrl.value.split('\n') : ctrl.value;
        const d = field.type === 'html_lines' ? (Array.isArray(def) ? def : []) : def;
        const same = field.type === 'html_lines' ? JSON.stringify(v) === JSON.stringify(d) : v === d;
        setFieldSoft(id, key, v, same);
      };
    } else if (field.type === 'number') {
      ctrl = doc.createElement('input');
      ctrl.className = 'bp-input'; ctrl.type = 'number';
      if (field.min != null) ctrl.min = field.min;
      if (field.max != null) ctrl.max = field.max;
      ctrl.value = val == null ? '' : val;
      ctrl.oninput = () => { const n = Number(ctrl.value); setField(id, key, Number.isFinite(n) ? n : def, n === Number(def)); };
    } else if (field.type === 'color') {
      ctrl = doc.createElement('div'); ctrl.className = 'bp-color';
      const picker = doc.createElement('input'); picker.type = 'color';
      picker.value = /^#[0-9a-f]{6}$/i.test(String(val)) ? val : '#888888';
      const hex = doc.createElement('input'); hex.className = 'bp-input'; hex.style.flex = '1'; hex.value = String(val == null ? '' : val);
      picker.oninput = () => { hex.value = picker.value; setField(id, key, picker.value, picker.value === def); };
      hex.oninput = () => { setField(id, key, hex.value, hex.value === def); };
      ctrl.appendChild(picker); ctrl.appendChild(hex);
    } else if (field.type === 'select') {
      ctrl = doc.createElement('select'); ctrl.className = 'bp-select';
      (field.options || []).forEach(o => { const op = doc.createElement('option'); op.value = o.value; op.textContent = o.label || o.value; ctrl.appendChild(op); });
      ctrl.value = val == null ? '' : val;
      ctrl.onchange = () => setField(id, key, ctrl.value, ctrl.value === def);
    } else if (field.type === 'switch') {
      ctrl = doc.createElement('input'); ctrl.type = 'checkbox'; ctrl.checked = !!val;
      ctrl.onchange = () => setField(id, key, ctrl.checked, ctrl.checked === !!def);
    } else { // text | image
      ctrl = doc.createElement('input'); ctrl.className = 'bp-input'; ctrl.type = 'text';
      ctrl.value = String(val == null ? '' : val);
      if (field.type === 'image') ctrl.placeholder = '图片 URL 或 {{asset:用途}}';
      ctrl.oninput = () => setField(id, key, ctrl.value, ctrl.value === def);
    }
    wrap.appendChild(ctrl);
    if (field.tooltip) { const t = doc.createElement('div'); t.className = 'bp-tip'; t.textContent = field.tooltip; wrap.appendChild(t); }
    return wrap;
  }
  // setField 的薄封装：html_lines 的“等于默认”判断在调用处算好后传入 same
  function setFieldSoft(id, key, val, same) {
    const b = ensureBlock(id);
    if (!isObj(b.fields)) b.fields = {};
    if (same) delete b.fields[key]; else b.fields[key] = val;
    prune(id); emit(); render();
  }

  function render() {
    const mod = getModule() || {};
    const resolved = resolveBlocks(mod, override);
    const baseOrder = resolved.blocks.map(b => b.id);
    mount.innerHTML = '';
    const wrap = doc.createElement('div');
    wrap.className = 'bp-wrap';

    const head = doc.createElement('div');
    head.className = 'bp-head';
    const master = doc.createElement('label');
    master.className = 'bp-master';
    const mcb = doc.createElement('input'); mcb.type = 'checkbox'; mcb.checked = resolved.enabled;
    const moduleDefault = mod.enabled !== false;
    mcb.onchange = () => setMaster(mcb.checked, moduleDefault);
    master.appendChild(mcb);
    const mlabel = doc.createElement('span'); mlabel.className = 'bp-head-title'; mlabel.textContent = '底部区块';
    master.appendChild(mlabel);
    head.appendChild(master);
    if (hasAnyOverride(override)) {
      const ra = doc.createElement('button'); ra.type = 'button'; ra.className = 'bp-resetall'; ra.textContent = '全部恢复默认';
      ra.onclick = resetAll; head.appendChild(ra);
    }
    wrap.appendChild(head);

    if (!resolved.blocks.length) {
      const e = doc.createElement('div'); e.className = 'bp-empty'; e.textContent = '该规则暂无可配置的底部区块。';
      wrap.appendChild(e); mount.appendChild(wrap); return;
    }

    for (const block of resolved.blocks) {
      const type = getBlockType(block.type) || { label: block.type, fields: [] };
      const card = doc.createElement('div');
      card.className = 'bp-card' + (block.enabled ? '' : ' bp-disabled');
      card.setAttribute('draggable', 'true');
      card.dataset.id = block.id;
      card.addEventListener('dragstart', () => { dragId = block.id; });
      card.addEventListener('dragover', (ev) => { ev.preventDefault(); });
      card.addEventListener('drop', (ev) => { ev.preventDefault(); if (dragId && dragId !== block.id) reorder(dragId, block.id, baseOrder); dragId = ''; });

      const row = doc.createElement('div'); row.className = 'bp-row';
      const grip = doc.createElement('span'); grip.className = 'bp-grip'; grip.textContent = '⠿'; grip.title = '拖拽排序';
      const cb = doc.createElement('input'); cb.type = 'checkbox'; cb.checked = block.enabled;
      cb.title = '在本篇显示/隐藏该区块';
      cb.onchange = () => setEnabled(block.id, cb.checked, block.enabledDefault);
      const name = doc.createElement('span'); name.className = 'bp-name';
      name.innerHTML = esc(type.label) + (block.enabledOverridden ? '<span class="bp-sub">本篇已改开关</span>' : '');
      const caret = doc.createElement('span');
      const isOpen = expanded.has(block.id) || (block.enabled && !expanded.has('__c__' + block.id));
      caret.className = 'bp-caret' + (isOpen ? ' bp-open' : ''); caret.textContent = '▸';
      const toggle = () => {
        if (isOpenNow(block)) { expanded.delete(block.id); expanded.add('__c__' + block.id); }
        else { expanded.add(block.id); expanded.delete('__c__' + block.id); }
        render();
      };
      name.onclick = toggle; caret.onclick = toggle;
      row.appendChild(grip); row.appendChild(cb); row.appendChild(name);
      if (blockHasOverride(block.id)) {
        const rb = doc.createElement('button');
        rb.type = 'button'; rb.className = 'bp-reset'; rb.textContent = '⟲'; rb.title = '此区块恢复后台默认';
        rb.onclick = (ev) => { ev.stopPropagation(); resetBlock(block.id); };
        row.appendChild(rb);
      }
      row.appendChild(caret);
      card.appendChild(row);

      if (isOpenNow(block) && (type.fields || []).length) {
        const fields = doc.createElement('div'); fields.className = 'bp-fields';
        for (const f of type.fields) fields.appendChild(fieldControl(block, f));
        card.appendChild(fields);
      }
      wrap.appendChild(card);
    }
    mount.appendChild(wrap);
  }

  // 默认展开规则：启用的块默认展开，未启用默认折叠；用户手动开合后以 expanded 记录为准。
  function isOpenNow(block) {
    if (expanded.has(block.id)) return true;
    if (expanded.has('__c__' + block.id)) return false;
    return block.enabled;
  }

  render();
  return {
    getOverride: () => override,
    refresh: render,
  };
}
