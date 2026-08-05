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
function cssAttrValue(s) { return String(s == null ? '' : s).replace(/\\/g, '\\\\').replace(/"/g, '\\"'); }
function clone(v) { return JSON.parse(JSON.stringify(v == null ? null : v)); }
function uid(type, existing) {
  let i = 1; let id = type;
  while (existing[id]) { id = type + '_' + (++i); }
  return id;
}
const KIND_LABELS = { text: '文字', image: '图片', recommendations: '推荐文章', spacer: '间距' };
const BLOCK_ID_LABELS = { tip: '提示声明', biz_coop: '商务合作', join_group: '加入微信群', follow_account: '关注公众号', logo: '品牌标志', more_info: '获取更多内容' };
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
function blockDisplayLabel(block, type, assetLibrary, scope, blockID = '') {
  const custom = String(block && block.display_name || '').trim();
  if (custom && custom !== String(type && type.label || '').trim()) return custom;
  if (BLOCK_ID_LABELS[blockID]) return BLOCK_ID_LABELS[blockID];
  const f = isObj(block && block.fields) ? block.fields : {};
  const title = typeof f.title === 'string' ? f.title.replace(/^[\s/]+|[\s/]+$/g, '').trim() : '';
  if (title) return title;
  const intro = String(f.intro_text || f.caption || '').trim();
  if (intro) return intro;
  const id = assetIDFromRef(f.image);
  for (const asset of assetsOf(assetLibrary)) {
    const assetScope = asset && (asset.scope || 'global');
    if (asset && asset.id === id && (assetScope === 'global' || assetScope === scope)) return String(asset.label || '').trim();
  }
  return custom || (type && type.label) || (block && block.type) || '';
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
.be-add select{min-height:32px;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);padding:4px 6px;font-size:12px;}
.be-add button{min-height:32px;background:var(--fs-brand,#1456f0);color:var(--fs-brand-on,#fff);border:none;border-radius:var(--fs-r-s,6px);padding:5px 10px;font-size:12px;cursor:pointer;}
.be-card{border:1px solid var(--fs-border-card,#e5e6eb);border-radius:var(--fs-r-s,6px);margin-bottom:8px;background:var(--fs-bg-card,#fff);}
.be-fe{grid-column:1/-1;display:flex;align-items:center;gap:8px;margin-bottom:0;font-size:12px;color:var(--fs-text-2,#646a73);cursor:pointer;}
.be-fe input[disabled]{cursor:default;}
.be-row{display:flex;align-items:center;gap:8px;padding:8px 10px;}
.be-grip{cursor:grab;color:var(--fs-text-disabled,#bbbfc4);display:inline-flex;align-items:center;}
.be-enabled{width:30px;height:30px;display:inline-flex;align-items:center;justify-content:center;cursor:pointer;flex:0 0 30px;}
.be-enabled input{width:16px;height:16px;accent-color:var(--fs-brand,#1456f0);}
.be-toggle{min-width:0;min-height:34px;flex:1;display:flex;align-items:center;gap:8px;padding:2px 4px;border:0;border-radius:var(--fs-r-xs,4px);background:transparent;color:inherit;text-align:left;font:inherit;cursor:pointer;}
.be-toggle:hover{background:var(--fs-bg-hover,rgba(31,35,39,.04));}
.be-name{min-width:0;flex:1;font-weight:500;overflow-wrap:anywhere;}
.be-name .be-type{font-weight:400;color:var(--fs-text-3,#8f959e);font-size:12px;margin-left:6px;}
.be-move,.be-del{width:30px;height:30px;display:inline-flex;align-items:center;justify-content:center;flex:0 0 30px;padding:0;background:none;border:0;border-radius:var(--fs-r-xs,4px);color:var(--fs-text-3,#8f959e);cursor:pointer;font:inherit;}
.be-move:hover,.be-del:hover{background:var(--fs-bg-hover,rgba(31,35,39,.04));color:var(--fs-text-1,#1f2329);}
.be-move:disabled{opacity:.32;cursor:not-allowed;}
.be-del:hover{color:var(--fs-danger,#f54a45);}
.be-caret{color:var(--fs-text-3,#8f959e);font-size:11px;}
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
.be-presets{display:flex;gap:4px;margin:0 0 10px;border-bottom:1px solid var(--fs-divider,rgba(31,35,39,.08));}
.be-preset{background:none;border:none;border-bottom:2px solid transparent;padding:6px 14px;font-size:13px;cursor:pointer;color:var(--fs-text-2,#646a73);margin-bottom:-1px;}
.be-preset.active{color:var(--fs-brand,#1456f0);border-bottom-color:var(--fs-brand,#1456f0);font-weight:500;}
.be-wrap button:focus-visible,.be-wrap select:focus-visible,.be-wrap input:focus-visible,.be-wrap textarea:focus-visible,.be-wrap summary:focus-visible{outline:2px solid var(--fs-brand,#1456f0);outline-offset:2px;}
@media(max-width:980px){.be-fields{grid-template-columns:1fr}.be-field.wide{grid-column:auto}}
@media(max-width:900px),(pointer:coarse){.be-add select,.be-add button,.be-preset,.be-toggle,.be-enabled,.be-move,.be-del{min-height:44px}.be-enabled,.be-move,.be-del{width:44px;flex-basis:44px}.be-row{gap:4px;padding-inline:6px}.be-grip{display:none}}
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
    const toolbarKeys = ['bold', 'italic', 'underline', '|', 'color', 'bgColor', 'fontSize', '|', 'justifyLeft', 'justifyCenter', 'justifyRight', '|', 'bulletedList', 'numberedList', 'insertLink'];
    W.createToolbar({ editor, selector: tb, config: { toolbarKeys } });
    tb.setAttribute('role', 'toolbar');
    tb.setAttribute('aria-label', '富文本格式');
    const toolbarLabels = { bold: '加粗', italic: '斜体', underline: '下划线', color: '文字颜色', bgColor: '背景颜色', fontSize: '字号', justifyLeft: '左对齐', justifyCenter: '居中', justifyRight: '右对齐', bulletedList: '无序列表', numberedList: '有序列表', insertLink: '插入链接' };
    const applyToolbarLabels = () => {
      const toolbarButtons = [...tb.querySelectorAll('button')];
      toolbarKeys.filter(key => key !== '|').forEach((key, index) => {
        if (toolbarButtons[index] && !toolbarButtons[index].getAttribute('aria-label')) toolbarButtons[index].setAttribute('aria-label', toolbarLabels[key]);
      });
    };
    applyToolbarLabels();
    const view = doc.defaultView || window;
    if (view.queueMicrotask) view.queueMicrotask(applyToolbarLabels);
    else Promise.resolve().then(applyToolbarLabels);
    if (view.requestAnimationFrame) view.requestAnimationFrame(applyToolbarLabels);
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
  let mod = isObj(module) ? clone(module) : { schema_version: 2, enabled: false, presets: [] };
  const expanded = new Set();
  let dragId = '';
  let activeIdx = 0; // 双预设（FBIF 原创/转载）：当前正在编辑哪一套

  function normalizePreset(p, idx) {
    if (!isObj(p.blocks)) p.blocks = {};
    if (!Array.isArray(p.order)) p.order = Object.keys(p.blocks);
    if (!String(p.id || '').trim()) p.id = 'preset_' + (idx + 1);
  }
  function isV2Module(m) { return Number(m && m.schema_version) >= 2 && Array.isArray(m && m.presets); }
  function normalizeModuleShape() {
    if (isV2Module(mod)) {
      mod.schema_version = 2;
      delete mod.blocks;
      delete mod.order;
      delete mod.gap;
      mod.presets.forEach((p, i) => {
        if (!isObj(p)) {
          mod.presets[i] = { id: 'preset_' + (i + 1), name: '预设' + (i + 1), order: [], blocks: {} };
          return;
        }
        normalizePreset(p, i);
      });
      if (activeIdx >= mod.presets.length) activeIdx = 0;
      return;
    }
    if (!isObj(mod.blocks)) mod.blocks = {};
    if (!Array.isArray(mod.order)) mod.order = Object.keys(mod.blocks);
  }
  normalizeModuleShape();

  // 双套结构（schema_version>=2，原创/转载分两套放进 presets[]，顶层 blocks/order 为空）vs 单套（顶层 blocks/order）。
  // layer() 返回「当前编辑层」的 {blocks, order} 引用——所有增删改读写都走它，于是同一套逻辑既能编单套（系列），
  // 也能编双套里选中的那一套（FBIF 原创/转载切换）。
  function isV2() { return isV2Module(mod); }
  function activePreset() { const ps = mod.presets || []; return ps[activeIdx] || ps[0] || null; }
  function layer() {
    normalizeModuleShape();
    if (isV2()) {
      const p = activePreset();
      if (p) { normalizePreset(p, activeIdx); return p; }
      return { blocks: {}, order: [] };
    }
    return mod;
  }

  function emit() { normalizeModuleShape(); if (onChange) onChange(clone(mod)); }

  function setMaster(v) { mod.enabled = v; emit(); }
  function setGap(v) { mod.gap = v ? 'blank_line' : 'none'; emit(); }
  function setBlockEnabled(id, v) { const L = layer(); if (L.blocks[id]) { L.blocks[id].enabled = v; emit(); } }
  function setBlockDisplayName(id, v) { const L = layer(); if (L.blocks[id]) { const s = String(v || '').trim(); if (s) L.blocks[id].display_name = s; else delete L.blocks[id].display_name; emit(); } }
  // 块实例级「允许前端编辑此块」：勾选写 frontend_editable=true（前端出现换图入口），不勾删除（缺省 false=前端只读）。
  function setBlockFrontendEditable(id, v) { const L = layer(); if (L.blocks[id]) { if (v) L.blocks[id].frontend_editable = true; else delete L.blocks[id].frontend_editable; emit(); } }
  function setField(id, key, v) {
    const L = layer();
    if (!L.blocks[id]) return;
    if (!isObj(L.blocks[id].fields)) L.blocks[id].fields = {};
    L.blocks[id].fields[key] = v;
    emit();
  }
  function addBlock(type) {
    const t = getBlockType(type);
    if (!t) return;
    const L = layer();
    const id = uid(type, L.blocks);
    L.blocks[id] = { type, display_name: (t && t.label) || type, enabled: true, fields: defaultFieldsForType(type) };
    L.order.push(id);
    expanded.add(id);
    emit(); render(`[data-block-toggle="${cssAttrValue(id)}"]`);
  }
  function removeBlock(id) {
    const L = layer();
    const index = L.order.indexOf(id);
    const focusID = L.order[index + 1] || L.order[index - 1] || '';
    delete L.blocks[id];
    L.order = L.order.filter(x => x !== id);
    expanded.delete(id);
    emit(); render(focusID ? `[data-block-toggle="${cssAttrValue(focusID)}"]` : '.be-add button');
  }
  function reorder(fromId, toId) {
    const L = layer();
    const fi = L.order.indexOf(fromId), ti = L.order.indexOf(toId);
    if (fi < 0 || ti < 0 || fi === ti) return;
    L.order.splice(ti, 0, L.order.splice(fi, 1)[0]);
    emit(); render();
  }
  function moveBlock(id, delta) {
    const L = layer();
    const from = L.order.indexOf(id), to = from + delta;
    if (from < 0 || to < 0 || to >= L.order.length) return;
    [L.order[from], L.order[to]] = [L.order[to], L.order[from]];
    emit();
    const action = delta < 0 ? 'up' : 'down';
    render(`[data-block-id="${cssAttrValue(id)}"][data-block-action="${action}"]`);
  }

  function fieldControl(id, field, value) {
    const wrap = doc.createElement('div'); wrap.className = 'be-field';
    const controlLabel = (field.label || field.key) + '（默认值）';
    if (field.type === 'longText' || field.type === 'html_lines') wrap.classList.add('wide');
    const label = doc.createElement('div'); label.className = 'be-flabel';
    label.innerHTML = `<span>${esc(field.label || field.key)}</span><span class="be-defmark">默认值</span>`;
    wrap.appendChild(label);
    let ctrl;
    if (field.type === 'longText' || field.type === 'html_lines') {
      ctrl = doc.createElement('textarea'); ctrl.className = 'be-textarea';
      ctrl.setAttribute('aria-label', controlLabel);
      ctrl.value = field.type === 'html_lines' ? (Array.isArray(value) ? value.join('\n') : '') : String(value == null ? '' : value);
      ctrl.oninput = () => setField(id, field.key, field.type === 'html_lines' ? ctrl.value.split('\n') : ctrl.value);
    } else if (field.type === 'number') {
      ctrl = doc.createElement('input'); ctrl.className = 'be-input'; ctrl.type = 'number';
      ctrl.setAttribute('aria-label', controlLabel);
      if (field.min != null) ctrl.min = field.min;
      if (field.max != null) ctrl.max = field.max;
      ctrl.value = value == null ? '' : value;
      ctrl.oninput = () => { const n = Number(ctrl.value); setField(id, field.key, Number.isFinite(n) ? n : (field.default || 0)); };
    } else if (field.type === 'color') {
      ctrl = doc.createElement('div'); ctrl.className = 'be-color';
      const picker = doc.createElement('input'); picker.type = 'color';
      picker.setAttribute('aria-label', (field.label || field.key) + '取色器');
      picker.value = /^#[0-9a-f]{6}$/i.test(String(value)) ? value : '#888888';
      const hex = doc.createElement('input'); hex.className = 'be-input'; hex.style.flex = '1'; hex.value = String(value == null ? '' : value);
      hex.setAttribute('aria-label', (field.label || field.key) + '色值');
      picker.oninput = () => { hex.value = picker.value; setField(id, field.key, picker.value); };
      hex.oninput = () => setField(id, field.key, hex.value);
      ctrl.appendChild(picker); ctrl.appendChild(hex);
    } else if (field.type === 'select') {
      ctrl = doc.createElement('select'); ctrl.className = 'be-select';
      ctrl.setAttribute('aria-label', controlLabel);
      (field.options || []).forEach(o => {
        const op = doc.createElement('option'); op.value = o.value; op.textContent = o.label || o.value;
        if (String(o.value) === String(value == null ? '' : value)) op.selected = true;
        ctrl.appendChild(op);
      });
      ctrl.onchange = () => setField(id, field.key, ctrl.value);
    } else if (field.type === 'switch') {
      ctrl = doc.createElement('input'); ctrl.type = 'checkbox'; ctrl.checked = !!value;
      ctrl.setAttribute('aria-label', controlLabel);
      ctrl.onchange = () => setField(id, field.key, ctrl.checked);
    } else if (field.type === 'image') {
      // 后台素材库选图：限定当前编辑账号的 scope（global + 该账号），避免选到跨账号私有素材
      // → 渲染端 assetAllowedInScope 不放行 → 静默退化透明图（“改了不显示”根因）。不让管理员手填 URL。
      const scope = getScope ? getScope() : '';
      ctrl = doc.createElement('div');
      ctrl.style.cssText = 'display:flex;align-items:center;gap:8px;';
      const thumb = resolveAssetThumb(value, getAssetLibrary ? getAssetLibrary() : null, scope);
      const preview = doc.createElement('div');
      preview.style.cssText = 'width:64px;height:48px;border:1px solid #d9dce0;border-radius:4px;display:flex;align-items:center;justify-content:center;overflow:hidden;background:#f7f8fa;flex:none;font-size:11px;color:#6f757d;';
      if (thumb) { const im = doc.createElement('img'); im.src = thumb; im.alt = ''; im.style.cssText = 'max-width:100%;max-height:100%;object-fit:contain;'; preview.appendChild(im); }
      else { preview.textContent = '未选择'; }
      const btn = doc.createElement('button'); btn.type = 'button';
      btn.style.cssText = 'border:1px solid #d9dce0;background:#fff;border-radius:4px;padding:5px 10px;font-size:12px;cursor:pointer;';
      btn.textContent = thumb ? '更换图片' : '从素材库选择';
      btn.setAttribute('aria-label', (thumb ? '更换' : '选择') + (field.label || '图片'));
      btn.onclick = () => openAssetPicker({
        assetLibrary: getAssetLibrary ? getAssetLibrary() : null, scope, doc,
        onPick: (assetId) => { setField(id, field.key, '{{asset#' + assetId + '}}'); render(`[data-block-toggle="${cssAttrValue(id)}"]`); },
        onClear: () => { setField(id, field.key, ''); render(`[data-block-toggle="${cssAttrValue(id)}"]`); },
        // 上传新图（默认入库）：宿主提供 onUploadAsset(file, scope)→Promise<assetId>；缺省不显示上传按钮。
        onUpload: onUploadAsset ? (file) => onUploadAsset(file, scope) : undefined,
      });
      ctrl.appendChild(preview); ctrl.appendChild(btn);
    } else if (field.type === 'rich_text') {
      // 富文本：优先 wangEditor（vendor 全局），否则 textarea 填 HTML（后端 sanitizer 白名单兜底）。
      ctrl = doc.createElement('div');
      const ta = doc.createElement('textarea');
      ta.className = 'be-input'; ta.rows = 4; ta.style.cssText = 'width:100%;font-family:monospace;font-size:12px;box-sizing:border-box;';
      ta.setAttribute('aria-label', controlLabel);
      ta.value = String(value == null ? '' : value);
      ta.oninput = () => setField(id, field.key, ta.value);
      ctrl.appendChild(ta);
      if (typeof window !== 'undefined' && window.wangEditor) {
        mountWangEditor(doc, ctrl, ta, ta.value, (html) => { ta.value = html; setField(id, field.key, html); });
      }
    } else {
      ctrl = doc.createElement('input'); ctrl.className = 'be-input'; ctrl.type = 'text';
      ctrl.setAttribute('aria-label', controlLabel);
      ctrl.value = String(value == null ? '' : value);
      ctrl.oninput = () => setField(id, field.key, ctrl.value);
    }
    wrap.appendChild(ctrl);
    if (field.tooltip) { const t = doc.createElement('div'); t.className = 'be-tip'; t.textContent = field.tooltip; wrap.appendChild(t); }
    return wrap;
  }

  function render(focusSelector = '') {
    container.innerHTML = '';
    const wrap = doc.createElement('div'); wrap.className = 'be-wrap';

    const head = doc.createElement('div'); head.className = 'be-head';
    const master = doc.createElement('label'); master.className = 'be-master';
    const mcb = doc.createElement('input'); mcb.type = 'checkbox'; mcb.checked = mod.enabled !== false;
    mcb.onchange = () => setMaster(mcb.checked);
    master.appendChild(mcb);
    const ml = doc.createElement('span'); ml.textContent = channel === 'ft' ? '新文章默认显示网站页脚' : '新文章默认显示底部区块'; master.appendChild(ml);
    head.appendChild(master);

    // gap（模块间空行）仅单套结构有意义；双套（原创/转载）间距由「间距」块承载，不显示此开关。
    if (!isV2()) {
      const gap = doc.createElement('label'); gap.className = 'be-master';
      const gcb = doc.createElement('input'); gcb.type = 'checkbox'; gcb.checked = mod.gap !== 'none';
      gcb.onchange = () => setGap(gcb.checked);
      gap.appendChild(gcb);
      const gl = doc.createElement('span'); gl.textContent = '模块间默认空行'; gap.appendChild(gl);
      head.appendChild(gap);
    }

    const add = doc.createElement('div'); add.className = 'be-add';
    const sel = doc.createElement('select');
    sel.setAttribute('aria-label', '新增区块类型');
    for (const group of ['文字', '图片', '推荐文章', '间距']) {
      const types = blockTypesForChannel(channel).filter(t => typeGroupLabel(t) === group);
      if (!types.length) continue;
      const og = doc.createElement('optgroup'); og.label = group;
      types.forEach(t => { const op = doc.createElement('option'); op.value = t.type; op.textContent = t.label; og.appendChild(op); });
      sel.appendChild(og);
    }
    const addBtn = doc.createElement('button'); addBtn.type = 'button'; addBtn.textContent = '添加页脚区块';
    addBtn.onclick = () => sel.value && addBlock(sel.value);
    add.appendChild(sel); add.appendChild(addBtn);
    head.appendChild(add);
    wrap.appendChild(head);

    // 双预设（FBIF）：原创/转载切换 tab。切换只改正在编辑哪一套；名片/更多文章等结构通常两套一致，
    // 仅「提示正文」声明文案不同。改完任一套，保存时整套（含两个预设）一起写回。
    if (isV2()) {
      const tabs = doc.createElement('div'); tabs.className = 'be-presets';
      tabs.setAttribute('role', 'tablist');
      tabs.setAttribute('aria-label', '底部区块预设');
      const presets = mod.presets || [];
      presets.forEach((p, i) => {
        const b = doc.createElement('button'); b.type = 'button';
        b.className = 'be-preset' + (i === activeIdx ? ' active' : '');
        b.dataset.presetIndex = String(i);
        b.setAttribute('role', 'tab');
        b.setAttribute('aria-selected', i === activeIdx ? 'true' : 'false');
        b.tabIndex = i === activeIdx ? 0 : -1;
        b.textContent = p.name || p.id || ('预设' + (i + 1));
        const activate = (index) => { activeIdx = index; expanded.clear(); render(`[data-preset-index="${index}"]`); };
        b.onclick = () => activate(i);
        b.onkeydown = (event) => {
          let next = i;
          if (event.key === 'ArrowRight' || event.key === 'ArrowDown') next = (i + 1) % presets.length;
          else if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') next = (i - 1 + presets.length) % presets.length;
          else if (event.key === 'Home') next = 0;
          else if (event.key === 'End') next = presets.length - 1;
          else return;
          event.preventDefault();
          activate(next);
        };
        tabs.appendChild(b);
      });
      wrap.appendChild(tabs);
    }

    const L = layer();
    const order = L.order.filter(id => L.blocks[id]);
    if (!order.length) {
      const e = doc.createElement('div'); e.className = 'be-empty'; e.textContent = '暂无页脚区块，可从上方选择类型并添加。';
      wrap.appendChild(e);
    }
    for (const [orderIndex, id] of order.entries()) {
      const block = L.blocks[id];
      const type = getBlockType(block.type) || { label: block.type, fields: [] };
      const displayLabel = blockDisplayLabel(block, type, getAssetLibrary ? getAssetLibrary() : null, getScope ? getScope() : '', id) || id;
      const card = doc.createElement('div'); card.className = 'be-card';
      card.setAttribute('draggable', 'true'); card.dataset.id = id;
      card.setAttribute('role', 'group');
      card.setAttribute('aria-label', displayLabel + ' 区块');
      card.addEventListener('dragstart', () => { dragId = id; });
      card.addEventListener('dragover', ev => ev.preventDefault());
      card.addEventListener('drop', ev => { ev.preventDefault(); if (dragId && dragId !== id) reorder(dragId, id); dragId = ''; });

      const row = doc.createElement('div'); row.className = 'be-row';
      const grip = doc.createElement('span'); grip.className = 'be-grip'; grip.setAttribute('aria-hidden', 'true'); grip.innerHTML = '<svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor" aria-hidden="true"><circle cx="9" cy="6" r="1.4"/><circle cx="9" cy="12" r="1.4"/><circle cx="9" cy="18" r="1.4"/><circle cx="15" cy="6" r="1.4"/><circle cx="15" cy="12" r="1.4"/><circle cx="15" cy="18" r="1.4"/></svg>';
      const enabledWrap = doc.createElement('label'); enabledWrap.className = 'be-enabled';
      const en = doc.createElement('input'); en.type = 'checkbox'; en.checked = block.enabled !== false;
      en.setAttribute('aria-label', displayLabel + '：默认启用');
      en.onchange = () => setBlockEnabled(id, en.checked);
      enabledWrap.appendChild(en);
      const open = expanded.has(id);
      const fieldsID = 'be-fields-' + String(id).replace(/[^a-z0-9_-]/gi, '-');
      const toggle = doc.createElement('button'); toggle.type = 'button'; toggle.className = 'be-toggle'; toggle.dataset.blockToggle = id;
      toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
      toggle.setAttribute('aria-controls', fieldsID);
      const name = doc.createElement('span'); name.className = 'be-name';
      name.innerHTML = esc(displayLabel) + `<span class="be-type">${esc(typeGroupLabel(type))}</span>`;
      const caret = doc.createElement('span'); caret.className = 'be-caret'; caret.setAttribute('aria-hidden', 'true'); caret.textContent = open ? '▾' : '▸';
      toggle.appendChild(name); toggle.appendChild(caret);
      toggle.onclick = () => { if (expanded.has(id)) expanded.delete(id); else expanded.add(id); render(`[data-block-toggle="${cssAttrValue(id)}"]`); };
      const moveButton = (action, label, delta, disabled) => {
        const btn = doc.createElement('button'); btn.type = 'button'; btn.className = 'be-move';
        btn.dataset.blockId = id; btn.dataset.blockAction = action; btn.disabled = disabled;
        btn.setAttribute('aria-label', label + displayLabel); btn.textContent = delta < 0 ? '↑' : '↓';
        btn.onclick = () => moveBlock(id, delta);
        return btn;
      };
      const moveUp = moveButton('up', '上移', -1, orderIndex === 0);
      const moveDown = moveButton('down', '下移', 1, orderIndex === order.length - 1);
      const del = doc.createElement('button'); del.type = 'button'; del.className = 'be-del';
      del.setAttribute('aria-label', '删除' + displayLabel);
      del.innerHTML = '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M4 7h16M9 4h6l1 3M7 7l1 13h8l1-13M10 11v5M14 11v5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>';
      del.onclick = (ev) => { ev.stopPropagation(); removeBlock(id); };
      row.appendChild(grip); row.appendChild(enabledWrap); row.appendChild(toggle); row.appendChild(moveUp); row.appendChild(moveDown); row.appendChild(del);
      card.appendChild(row);

      if (open) {
        const fields = doc.createElement('div'); fields.className = 'be-fields';
        fields.id = fieldsID;
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
        labelInput.setAttribute('aria-label', '模块名称（显示名）');
        labelInput.placeholder = blockDisplayLabel({ ...block, display_name: '' }, type, getAssetLibrary ? getAssetLibrary() : null, getScope ? getScope() : '', id);
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
    ta.setAttribute('aria-label', '底部区块原始 JSON');
    ta.onchange = () => { try { const parsed = JSON.parse(ta.value); if (isObj(parsed)) { mod = parsed; activeIdx = 0; normalizeModuleShape(); emit(); render(); } } catch (e) { ta.style.borderColor = 'var(--fs-danger,#f54a45)'; } };
    adv.appendChild(ta);
    wrap.appendChild(adv);

    container.appendChild(wrap);
    if (focusSelector) container.querySelector(focusSelector)?.focus();
  }

  render();
  return {
    getModule: () => { normalizeModuleShape(); return clone(mod); },
    destroy: () => { container.innerHTML = ''; },
  };
}
