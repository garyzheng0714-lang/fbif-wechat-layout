// block-editor.js — 规则后台「底部区块」结构化编辑器（编辑 config.modules.bottom_blocks 的【默认】）。
//
// 两层模型的【后台默认】侧：本编辑器改的是永久默认（顺序 / 每块默认开关 / 字段默认值 / 增删区块）。
// 前台单篇覆盖另由 block-panel.js 负责。治「怕配错」：结构化表单取代裸 JSON/HTML，字段带 tooltip
// 与约束（数字范围、颜色取色器、图片占位符），管理员可一眼看懂、改不坏；裸 JSON 仅留高级折叠。
//
// 中性：只 import 中性注册表，不 import 渠道渲染器（红线 #19）。

import { getBlockType, blockTypesForChannel, defaultFieldsForType } from '../blocks/block-registry.gen.js';
import { openAssetPicker, resolveAssetThumb } from '../blocks/asset-picker.js';
import { normalizeRichHtml } from '../blocks/rich-text-render.js';

function isObj(v) { return !!v && typeof v === 'object' && !Array.isArray(v); }
function esc(s) {
  return String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
function clone(v) { return JSON.parse(JSON.stringify(v == null ? null : v)); }
function uid(type, existing) {
  let i = 1; let id = type;
  while (existing[id]) { id = type + '_' + (++i); }
  return id;
}
const KIND_LABELS = { text: '文字', image: '图片', recommendations: '推荐文章', spacer: '间距' };
function typeGroupLabel(type) {
  return KIND_LABELS[type && type.kind] || '图片';
}
function assetsOf(lib) {
  if (Array.isArray(lib)) return lib;
  if (isObj(lib) && Array.isArray(lib.assets)) return lib.assets;
  return [];
}
function assetIDFromRef(value) {
  const m = /^\{\{asset#([a-z0-9_-]+)\}\}$/i.exec(String(value == null ? '' : value).trim());
  return m ? m[1] : '';
}
function blockDisplayLabel(block, type, assetLibrary, scope) {
  const custom = String(block && block.display_name || '').trim();
  if (custom) return custom;
  const f = isObj(block && block.fields) ? block.fields : {};
  const title = typeof f.title === 'string' ? f.title.replace(/^[\s/]+|[\s/]+$/g, '').trim() : '';
  if (title) return title;
  const intro = String(f.intro_text || f.caption || '').trim();
  if (intro) return intro;
  if (block && block.type === 'name_card') return (type && type.label) || block.type || '';
  const id = assetIDFromRef(f.image);
  for (const asset of assetsOf(assetLibrary)) {
    const assetScope = asset && (asset.scope || 'global');
    if (asset && asset.id === id && (assetScope === 'global' || assetScope === scope)) return String(asset.label || '').trim();
  }
  return (type && type.label) || (block && block.type) || '';
}

const STYLE_ID = 'be-style';
function ensureStyle(doc) {
  if (doc.getElementById(STYLE_ID)) return;
  const st = doc.createElement('style');
  st.id = STYLE_ID;
  st.textContent = `
.be-wrap{font-size:13px;color:var(--fs-text-1,#1f2329);}
.be-head{display:flex;align-items:center;justify-content:space-between;margin-bottom:10px;gap:8px;flex-wrap:wrap;}
.be-master{display:flex;align-items:center;gap:6px;}
.be-add{display:flex;gap:6px;align-items:center;}
.be-add select{border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);padding:4px 6px;font-size:12px;}
.be-add button{background:var(--fs-brand,#1456f0);color:#fff;border:none;border-radius:var(--fs-r-s,6px);padding:5px 10px;font-size:12px;cursor:pointer;}
.be-card{border:1px solid var(--fs-border-card,#e5e6eb);border-radius:var(--fs-r-s,6px);margin-bottom:8px;background:var(--fs-bg-card,#fff);}
.be-fe{grid-column:1/-1;display:flex;align-items:center;gap:8px;margin-bottom:0;font-size:12px;color:var(--fs-text-2,#646a73);cursor:pointer;}
.be-fe input[disabled]{cursor:default;}
.be-row{display:flex;align-items:center;gap:8px;padding:8px 10px;}
.be-grip{cursor:grab;color:var(--fs-text-disabled,#bbbfc4);display:inline-flex;align-items:center;}
.be-name{flex:1;font-weight:500;cursor:pointer;}
.be-name .be-type{font-weight:400;color:var(--fs-text-3,#8f959e);font-size:12px;margin-left:6px;}
.be-del{background:none;border:none;color:var(--fs-text-3,#8f959e);cursor:pointer;font-size:13px;}
.be-del:hover{color:var(--fs-danger,#f54a45);}
.be-caret{color:var(--fs-text-3,#8f959e);cursor:pointer;font-size:11px;}
.be-fields{padding:10px 12px 12px 30px;border-top:1px solid var(--fs-divider,rgba(31,35,39,.08));display:grid;grid-template-columns:repeat(2,minmax(0,1fr));column-gap:12px;row-gap:10px;}
.be-field{margin-top:0;}
.be-field.wide{grid-column:1/-1;}
.be-flabel{display:flex;align-items:center;gap:6px;margin-bottom:4px;color:var(--fs-text-2,#646a73);font-size:12px;}
.be-flabel .be-defmark{font-size:11px;color:var(--fs-text-3,#8f959e);}
.be-input,.be-textarea,.be-select{width:100%;box-sizing:border-box;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);padding:5px 8px;font-size:13px;font-family:inherit;}
.be-textarea{min-height:56px;resize:vertical;}
.be-color{display:flex;gap:8px;align-items:center;}
.be-color input[type=color]{width:34px;height:26px;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);padding:0;background:none;cursor:pointer;}
.be-tip{color:var(--fs-text-3,#8f959e);font-size:11px;margin-top:3px;}
.be-empty{color:var(--fs-text-3,#8f959e);font-size:12px;padding:8px 2px;}
.be-adv{margin-top:10px;}
.be-adv summary{cursor:pointer;color:var(--fs-text-3,#8f959e);font-size:12px;}
.be-adv textarea{width:100%;box-sizing:border-box;min-height:120px;margin-top:6px;font-family:ui-monospace,monospace;font-size:12px;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);padding:8px;}
@media(max-width:980px){.be-fields{grid-template-columns:1fr}.be-field.wide{grid-column:auto}}
`;
  (doc.head || doc.documentElement).appendChild(st);
}

// createBlockEditor 创建后台区块编辑器。
//   container: 挂载元素
//   module: 初始 bottom_blocks 模块（默认）
//   channel: 'mp' | 'ft'（决定「新增区块」可选类型）
//   onChange: (module) => void
//   doc: 文档（测试可注入）
// mountWangEditor 在 rich_text 字段挂载 wangEditor 富文本编辑器（vendor 全局 window.wangEditor）。
// 工具条对齐 catalog 富文本能力（加粗/斜体/下划线 + 字色/背景/字号 + 对齐 + 列表 + 链接）。
// 产出 html 经后端 validateRichTextHTML 白名单兜底；wangEditor 不可用/出错时回退 textarea。
function mountWangEditor(doc, container, textarea, initialHtml, onChange) {
  let tb = null, ed = null, editor = null;
  try {
    textarea.style.display = 'none';
    tb = doc.createElement('div'); tb.style.cssText = 'border:1px solid #e8e8e8;';
    ed = doc.createElement('div'); ed.style.cssText = 'border:1px solid #e8e8e8;border-top:none;height:160px;overflow-y:auto;';
    container.appendChild(tb); container.appendChild(ed);
    const W = window.wangEditor;
    const parseDoc = (h) => { try { return new window.DOMParser().parseFromString(h, 'text/html').body; } catch (e2) { return null; } };
    editor = W.createEditor({ selector: ed, html: initialHtml || '<p><br></p>', config: { onChange: (e) => onChange(normalizeRichHtml(e.getHtml(), parseDoc)) } });
    W.createToolbar({ editor, selector: tb, config: { toolbarKeys: ['bold', 'italic', 'underline', '|', 'color', 'bgColor', 'fontSize', '|', 'justifyLeft', 'justifyCenter', 'justifyRight', '|', 'bulletedList', 'numberedList', 'insertLink'] } });
  } catch (e) {
    // 失败回退 textarea：清理半初始化 DOM + 销毁 editor，避免页面残留半初始化编辑器（codex review Block 4）。
    try { if (editor && editor.destroy) editor.destroy(); } catch (e2) {}
    if (ed && ed.remove) ed.remove();
    if (tb && tb.remove) tb.remove();
    textarea.style.display = '';
  }
}

export function createBlockEditor({ container, module, channel = 'mp', getAssetLibrary, getScope, onChange, onUploadAsset, doc = document }) {
  if (!container) return null;
  ensureStyle(doc);
  let mod = isObj(module) ? clone(module) : { enabled: false, order: [], blocks: {}, presets: [] };
  if (!isObj(mod.blocks)) mod.blocks = {};
  if (!Array.isArray(mod.order)) mod.order = Object.keys(mod.blocks);
  const expanded = new Set();
  let dragId = '';

  function emit() { if (onChange) onChange(clone(mod)); }

  function setMaster(v) { mod.enabled = v; emit(); render(); }
  function setGap(v) { mod.gap = v ? 'blank_line' : 'none'; emit(); render(); }
  function setBlockEnabled(id, v) { if (mod.blocks[id]) { mod.blocks[id].enabled = v; emit(); render(); } }
  function setBlockDisplayName(id, v) { if (mod.blocks[id]) { const s = String(v || '').trim(); if (s) mod.blocks[id].display_name = s; else delete mod.blocks[id].display_name; emit(); render(); } }
  // 块实例级「允许前端编辑此块」：勾选写 frontend_editable=true（前端出现换图入口），不勾删除（缺省 false=前端只读）。
  function setBlockFrontendEditable(id, v) { if (mod.blocks[id]) { if (v) mod.blocks[id].frontend_editable = true; else delete mod.blocks[id].frontend_editable; emit(); render(); } }
  function setField(id, key, v) {
    if (!mod.blocks[id]) return;
    if (!isObj(mod.blocks[id].fields)) mod.blocks[id].fields = {};
    mod.blocks[id].fields[key] = v;
    emit(); render();
  }
  function addBlock(type) {
    const t = getBlockType(type);
    if (!t) return;
    const id = uid(type, mod.blocks);
    mod.blocks[id] = { type, display_name: (t && t.label) || type, enabled: true, fields: defaultFieldsForType(type) };
    mod.order.push(id);
    expanded.add(id);
    emit(); render();
  }
  function removeBlock(id) {
    delete mod.blocks[id];
    mod.order = mod.order.filter(x => x !== id);
    emit(); render();
  }
  function reorder(fromId, toId) {
    const fi = mod.order.indexOf(fromId), ti = mod.order.indexOf(toId);
    if (fi < 0 || ti < 0 || fi === ti) return;
    mod.order.splice(ti, 0, mod.order.splice(fi, 1)[0]);
    emit(); render();
  }

  function fieldControl(id, field, value) {
    const wrap = doc.createElement('div'); wrap.className = 'be-field';
    if (field.type === 'longText' || field.type === 'html_lines') wrap.classList.add('wide');
    const label = doc.createElement('div'); label.className = 'be-flabel';
    label.innerHTML = `<span>${esc(field.label || field.key)}</span><span class="be-defmark">默认值</span>`;
    wrap.appendChild(label);
    let ctrl;
    if (field.type === 'longText' || field.type === 'html_lines') {
      ctrl = doc.createElement('textarea'); ctrl.className = 'be-textarea';
      ctrl.value = field.type === 'html_lines' ? (Array.isArray(value) ? value.join('\n') : '') : String(value == null ? '' : value);
      ctrl.oninput = () => setField(id, field.key, field.type === 'html_lines' ? ctrl.value.split('\n') : ctrl.value);
    } else if (field.type === 'number') {
      ctrl = doc.createElement('input'); ctrl.className = 'be-input'; ctrl.type = 'number';
      if (field.min != null) ctrl.min = field.min;
      if (field.max != null) ctrl.max = field.max;
      ctrl.value = value == null ? '' : value;
      ctrl.oninput = () => { const n = Number(ctrl.value); setField(id, field.key, Number.isFinite(n) ? n : (field.default || 0)); };
    } else if (field.type === 'color') {
      ctrl = doc.createElement('div'); ctrl.className = 'be-color';
      const picker = doc.createElement('input'); picker.type = 'color';
      picker.value = /^#[0-9a-f]{6}$/i.test(String(value)) ? value : '#888888';
      const hex = doc.createElement('input'); hex.className = 'be-input'; hex.style.flex = '1'; hex.value = String(value == null ? '' : value);
      picker.oninput = () => { hex.value = picker.value; setField(id, field.key, picker.value); };
      hex.oninput = () => setField(id, field.key, hex.value);
      ctrl.appendChild(picker); ctrl.appendChild(hex);
    } else if (field.type === 'select') {
      ctrl = doc.createElement('select'); ctrl.className = 'be-select';
      (field.options || []).forEach(o => { const op = doc.createElement('option'); op.value = o.value; op.textContent = o.label || o.value; ctrl.appendChild(op); });
      ctrl.value = value == null ? '' : value;
      ctrl.onchange = () => setField(id, field.key, ctrl.value);
    } else if (field.type === 'switch') {
      ctrl = doc.createElement('input'); ctrl.type = 'checkbox'; ctrl.checked = !!value;
      ctrl.onchange = () => setField(id, field.key, ctrl.checked);
    } else if (field.type === 'image') {
      // 后台素材库选图：限定当前编辑账号的 scope（global + 该账号），避免选到跨账号私有素材
      // → 渲染端 assetAllowedInScope 不放行 → 静默退化透明图（“改了不显示”根因）。不让管理员手填 URL。
      const scope = getScope ? getScope() : '';
      ctrl = doc.createElement('div');
      ctrl.style.cssText = 'display:flex;align-items:center;gap:8px;';
      const thumb = resolveAssetThumb(value, getAssetLibrary ? getAssetLibrary() : null, scope);
      const preview = doc.createElement('div');
      preview.style.cssText = 'width:64px;height:48px;border:1px solid #d9dce0;border-radius:4px;display:flex;align-items:center;justify-content:center;overflow:hidden;background:#f7f8fa;flex:none;font-size:11px;color:#8f959e;';
      if (thumb) { const im = doc.createElement('img'); im.src = thumb; im.alt = ''; im.style.cssText = 'max-width:100%;max-height:100%;object-fit:contain;'; preview.appendChild(im); }
      else { preview.textContent = '未选择'; }
      const btn = doc.createElement('button'); btn.type = 'button';
      btn.style.cssText = 'border:1px solid #d9dce0;background:#fff;border-radius:4px;padding:5px 10px;font-size:12px;cursor:pointer;';
      btn.textContent = thumb ? '更换图片' : '从素材库选择';
      btn.onclick = () => openAssetPicker({
        assetLibrary: getAssetLibrary ? getAssetLibrary() : null, scope, doc,
        onPick: (assetId) => setField(id, field.key, '{{asset#' + assetId + '}}'),
        onClear: () => setField(id, field.key, ''),
        // 上传新图（默认入库）：宿主提供 onUploadAsset(file, scope)→Promise<assetId>；缺省不显示上传按钮。
        onUpload: onUploadAsset ? (file) => onUploadAsset(file, scope) : undefined,
      });
      ctrl.appendChild(preview); ctrl.appendChild(btn);
    } else if (field.type === 'rich_text') {
      // 富文本：优先 wangEditor（vendor 全局），否则 textarea 填 HTML（后端 sanitizer 白名单兜底）。
      ctrl = doc.createElement('div');
      const ta = doc.createElement('textarea');
      ta.className = 'be-input'; ta.rows = 4; ta.style.cssText = 'width:100%;font-family:monospace;font-size:12px;box-sizing:border-box;';
      ta.value = String(value == null ? '' : value);
      ta.oninput = () => setField(id, field.key, ta.value);
      ctrl.appendChild(ta);
      if (typeof window !== 'undefined' && window.wangEditor) {
        mountWangEditor(doc, ctrl, ta, ta.value, (html) => { ta.value = html; setField(id, field.key, html); });
      }
    } else {
      ctrl = doc.createElement('input'); ctrl.className = 'be-input'; ctrl.type = 'text';
      ctrl.value = String(value == null ? '' : value);
      ctrl.oninput = () => setField(id, field.key, ctrl.value);
    }
    wrap.appendChild(ctrl);
    if (field.tooltip) { const t = doc.createElement('div'); t.className = 'be-tip'; t.textContent = field.tooltip; wrap.appendChild(t); }
    return wrap;
  }

  function render() {
    container.innerHTML = '';
    const wrap = doc.createElement('div'); wrap.className = 'be-wrap';

    const head = doc.createElement('div'); head.className = 'be-head';
    const master = doc.createElement('label'); master.className = 'be-master';
    const mcb = doc.createElement('input'); mcb.type = 'checkbox'; mcb.checked = mod.enabled !== false;
    mcb.onchange = () => setMaster(mcb.checked);
    master.appendChild(mcb);
    const ml = doc.createElement('span'); ml.textContent = '默认启用底部区块'; master.appendChild(ml);
    head.appendChild(master);

    const gap = doc.createElement('label'); gap.className = 'be-master';
    const gcb = doc.createElement('input'); gcb.type = 'checkbox'; gcb.checked = mod.gap !== 'none';
    gcb.onchange = () => setGap(gcb.checked);
    gap.appendChild(gcb);
    const gl = doc.createElement('span'); gl.textContent = '模块间默认空行'; gap.appendChild(gl);
    head.appendChild(gap);

    const add = doc.createElement('div'); add.className = 'be-add';
    const sel = doc.createElement('select');
    for (const group of ['文字', '图片', '推荐文章', '间距']) {
      const types = blockTypesForChannel(channel).filter(t => typeGroupLabel(t) === group);
      if (!types.length) continue;
      const og = doc.createElement('optgroup'); og.label = group;
      types.forEach(t => { const op = doc.createElement('option'); op.value = t.type; op.textContent = t.label; og.appendChild(op); });
      sel.appendChild(og);
    }
    const addBtn = doc.createElement('button'); addBtn.type = 'button'; addBtn.textContent = '新增区块';
    addBtn.onclick = () => sel.value && addBlock(sel.value);
    add.appendChild(sel); add.appendChild(addBtn);
    head.appendChild(add);
    wrap.appendChild(head);

    const order = mod.order.filter(id => mod.blocks[id]);
    if (!order.length) {
      const e = doc.createElement('div'); e.className = 'be-empty'; e.textContent = '暂无区块。用右上「新增区块」添加（如名片）。';
      wrap.appendChild(e);
    }
    for (const id of order) {
      const block = mod.blocks[id];
      const type = getBlockType(block.type) || { label: block.type, fields: [] };
      const card = doc.createElement('div'); card.className = 'be-card';
      card.setAttribute('draggable', 'true'); card.dataset.id = id;
      card.addEventListener('dragstart', () => { dragId = id; });
      card.addEventListener('dragover', ev => ev.preventDefault());
      card.addEventListener('drop', ev => { ev.preventDefault(); if (dragId && dragId !== id) reorder(dragId, id); dragId = ''; });

      const row = doc.createElement('div'); row.className = 'be-row';
      const grip = doc.createElement('span'); grip.className = 'be-grip'; grip.innerHTML = '<svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor" aria-hidden="true"><circle cx="9" cy="6" r="1.4"/><circle cx="9" cy="12" r="1.4"/><circle cx="9" cy="18" r="1.4"/><circle cx="15" cy="6" r="1.4"/><circle cx="15" cy="12" r="1.4"/><circle cx="15" cy="18" r="1.4"/></svg>'; grip.title = '拖拽排序';
      const en = doc.createElement('input'); en.type = 'checkbox'; en.checked = block.enabled !== false; en.title = '默认是否启用';
      en.onchange = () => setBlockEnabled(id, en.checked);
      const name = doc.createElement('span'); name.className = 'be-name';
      name.innerHTML = esc(blockDisplayLabel(block, type, getAssetLibrary ? getAssetLibrary() : null, getScope ? getScope() : '')) + `<span class="be-type">${esc(typeGroupLabel(type))} · ${esc(block.type)} · ${esc(id)}</span>`;
      const open = expanded.has(id);
      const caret = doc.createElement('span'); caret.className = 'be-caret'; caret.textContent = open ? '▾' : '▸';
      const toggle = () => { if (expanded.has(id)) expanded.delete(id); else expanded.add(id); render(); };
      name.onclick = toggle; caret.onclick = toggle;
      const del = doc.createElement('button'); del.type = 'button'; del.className = 'be-del'; del.textContent = '🗑'; del.title = '删除区块';
      del.onclick = (ev) => { ev.stopPropagation(); removeBlock(id); };
      row.appendChild(grip); row.appendChild(en); row.appendChild(name); row.appendChild(del); row.appendChild(caret);
      card.appendChild(row);

      if (open) {
        const fields = doc.createElement('div'); fields.className = 'be-fields';
        // 「允许前端编辑此块」：勾=该块在前端内容编辑区出现换图入口；不勾=前端只读（仍渲进正文预览）。
        // more_articles 的内容编辑器在前端永久可用（红线#14），不受此开关控制——故禁用并注明。
        const feRow = doc.createElement('label'); feRow.className = 'be-fe';
        const fe = doc.createElement('input'); fe.type = 'checkbox';
        const isMore = block.type === 'more_articles';
        fe.checked = isMore ? true : block.frontend_editable === true;
        fe.disabled = isMore;
        fe.onchange = () => setBlockFrontendEditable(id, fe.checked);
        const feTxt = doc.createElement('span');
        feTxt.textContent = isMore
          ? '更多文章内容编辑器在前端永久可用（不受此开关控制）'
          : '允许前端编辑此块（前端出现「更换图片」入口；不勾＝前端只读）';
        feRow.appendChild(fe); feRow.appendChild(feTxt);
        fields.appendChild(feRow);
        const labelField = doc.createElement('div'); labelField.className = 'be-field wide';
        const labelTitle = doc.createElement('div'); labelTitle.className = 'be-flabel'; labelTitle.innerHTML = '<span>模块名称</span><span class="be-defmark">显示名</span>';
        const labelInput = doc.createElement('input'); labelInput.className = 'be-input'; labelInput.type = 'text'; labelInput.value = block.display_name || '';
        labelInput.placeholder = blockDisplayLabel({ ...block, display_name: '' }, type, getAssetLibrary ? getAssetLibrary() : null, getScope ? getScope() : '');
        labelInput.oninput = () => setBlockDisplayName(id, labelInput.value);
        labelField.appendChild(labelTitle); labelField.appendChild(labelInput); fields.appendChild(labelField);
        const fdefs = type.fields || [];
        const fv = isObj(block.fields) ? block.fields : {};
        if (!fdefs.length) { const e = doc.createElement('div'); e.className = 'be-empty'; e.textContent = '该类型无可编辑字段（如「更多文章」委托卡片编辑器）。'; fields.appendChild(e); }
        for (const f of fdefs) fields.appendChild(fieldControl(id, f, (f.key in fv) ? fv[f.key] : f.default));
        card.appendChild(fields);
      }
      wrap.appendChild(card);
    }

    // 高级：原始 JSON（管理员兜底，运营默认不展开）
    const adv = doc.createElement('details'); adv.className = 'be-adv';
    const sum = doc.createElement('summary'); sum.textContent = '高级 · 原始 JSON（一般无需改）'; adv.appendChild(sum);
    const ta = doc.createElement('textarea'); ta.value = JSON.stringify(mod, null, 2);
    ta.onchange = () => { try { const parsed = JSON.parse(ta.value); if (isObj(parsed)) { mod = parsed; if (!isObj(mod.blocks)) mod.blocks = {}; if (!Array.isArray(mod.order)) mod.order = Object.keys(mod.blocks); emit(); render(); } } catch (e) { ta.style.borderColor = 'var(--fs-danger,#f54a45)'; } };
    adv.appendChild(ta);
    wrap.appendChild(adv);

    container.appendChild(wrap);
  }

  render();
  return {
    getModule: () => clone(mod),
    destroy: () => { container.innerHTML = ''; },
  };
}
