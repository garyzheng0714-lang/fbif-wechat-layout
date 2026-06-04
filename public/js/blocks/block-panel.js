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
import { openAssetPicker, resolveAssetThumb } from './asset-picker.js';
import { createTitleFormatControl, closeOpenFormatPopovers } from './block-format-control.js';

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

// 统一线性图标（替代 ▸/⠿/⟲ 文本字形，匹配编辑器其余 SVG 图标集；currentColor 跟随）
const ICON_CHEVRON = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 6l6 6-6 6"/></svg>';
const ICON_GRIP = '<svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor" aria-hidden="true"><circle cx="9" cy="6" r="1.4"/><circle cx="9" cy="12" r="1.4"/><circle cx="9" cy="18" r="1.4"/><circle cx="15" cy="6" r="1.4"/><circle cx="15" cy="12" r="1.4"/><circle cx="15" cy="18" r="1.4"/></svg>';
const ICON_RESET = '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 12a9 9 0 1 0 2.6-6.4"/><path d="M3 3v5h5"/></svg>';
// 手风琴头的层叠图标（方案A 参考稿 I('layers')）。
const ICON_LAYERS = '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 2 2 7l10 5 10-5-10-5Z"/><path d="M2 17l10 5 10-5"/><path d="M2 12l10 5 10-5"/></svg>';

// reduced-motion 探测（无 window 时安全返回 false，与 anim.js 一致语义但不引入 import）。
function prefersReduced() {
  return typeof window !== 'undefined' && !!window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

const STYLE_ID = 'bp-style';
function ensureStyle(doc) {
  if (doc.getElementById(STYLE_ID)) return;
  const st = doc.createElement('style');
  st.id = STYLE_ID;
  st.textContent = `
.bp-wrap{font-size:13px;color:var(--fs-text-1,#1f2329);}
.bp-acc{border-radius:var(--fs-r-m,8px);}
.bp-acc-head{display:flex;align-items:center;gap:10px;width:100%;height:40px;padding:0 8px;border:none;background:none;border-radius:var(--fs-r-s,6px);text-align:left;cursor:pointer;font-family:inherit;transition:background .12s var(--fs-ease-out,ease);}
.bp-acc-head:hover{background:var(--fs-bg-hover,rgba(31,35,39,.045));}
.bp-ico{width:18px;height:18px;color:var(--fs-text-2,#646a73);flex-shrink:0;display:flex;}
.bp-acc-title{flex:1;font-size:13px;font-weight:600;color:var(--fs-text-1,#1f2329);}
.bp-count{min-width:18px;height:18px;padding:0 5px;border-radius:9999px;background:var(--fs-bg-sunken,#f2f3f5);color:var(--fs-text-2,#646a73);font-size:11px;font-weight:500;font-variant-numeric:tabular-nums;display:inline-flex;align-items:center;justify-content:center;flex-shrink:0;transition:background .15s,color .15s;}
.bp-acc.is-open .bp-count{background:var(--fs-brand-soft,#ebf1ff);color:var(--fs-brand,#1456f0);}
.bp-chevron{color:var(--fs-text-3,#8f959e);flex-shrink:0;display:inline-flex;transition:transform .2s var(--fs-ease-out,ease);}
.bp-acc.is-open .bp-chevron{transform:rotate(90deg);}
.bp-acc-body{overflow:hidden;}
.bp-acc-inner{padding-top:4px;}
.bp-list{display:flex;flex-direction:column;gap:2px;}
.bp-hint{color:var(--fs-text-3,#8f959e);font-size:11px;line-height:1.6;margin-top:8px;padding:0 8px;}
.bp-resetall{background:none;border:none;color:var(--fs-text-3,#8f959e);cursor:pointer;font-size:12px;text-decoration:underline;padding:4px 8px 0;}
.bp-resetall:hover{color:var(--fs-brand,#1456f0);}
.bp-card{border:none;border-radius:var(--fs-r-s,6px);margin-bottom:2px;background:transparent;}
.bp-card:hover{background:var(--fs-bg-hover,rgba(31,35,39,.045));}
.bp-switch{appearance:none;-webkit-appearance:none;width:34px;height:20px;border-radius:9999px;background:var(--fs-border-control,#d9dce0);position:relative;cursor:pointer;flex:none;transition:background .2s;margin:0;}
.bp-switch:checked{background:var(--fs-brand,#1456f0);}
.bp-switch::after{content:'';position:absolute;top:2px;left:2px;width:16px;height:16px;border-radius:50%;background:#fff;box-shadow:0 1px 2px rgba(31,35,39,.2);transition:transform .2s;}
.bp-switch:checked::after{transform:translateX(14px);}
.bp-card.bp-disabled{opacity:.62;}
.bp-row{display:flex;align-items:center;gap:8px;padding:8px 10px;}
.bp-grip{cursor:grab;color:var(--fs-text-disabled,#bbbfc4);user-select:none;display:inline-flex;align-items:center;}
.bp-grip:hover{color:var(--fs-text-3,#8f959e);}
.bp-name{flex:1;font-weight:500;cursor:pointer;}
.bp-name .bp-sub{font-weight:400;color:var(--fs-text-3,#8f959e);font-size:12px;margin-left:6px;}
.bp-caret{color:var(--fs-text-3,#8f959e);cursor:pointer;transition:transform .18s var(--fs-ease-out,ease),background .15s,color .15s;display:inline-flex;align-items:center;padding:4px;border-radius:var(--fs-r-xs,4px);}
.bp-caret:hover{color:var(--fs-brand,#1456f0);background:var(--fs-bg-hover,rgba(31,35,39,.045));}
.bp-caret.bp-open{transform:rotate(90deg);}
.bp-fields{padding:2px 12px 12px 32px;border-top:1px solid var(--fs-divider,rgba(31,35,39,.08));}
.bp-field{margin-top:10px;}
.bp-flabel{display:flex;align-items:center;gap:6px;margin-bottom:4px;color:var(--fs-text-2,#646a73);font-size:12px;}
.bp-badge{font-size:11px;padding:1px 6px;border-radius:9999px;}
.bp-badge.over{background:var(--fs-brand-soft,#ebf1ff);color:var(--fs-brand,#1456f0);}
.bp-reset{background:none;border:none;color:var(--fs-text-3,#8f959e);cursor:pointer;display:inline-flex;align-items:center;padding:2px;margin-left:auto;}
.bp-reset:hover{color:var(--fs-brand,#1456f0);}
.bp-input,.bp-textarea,.bp-select{width:100%;box-sizing:border-box;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);padding:5px 8px;font-size:13px;font-family:inherit;}
.bp-textarea{min-height:60px;resize:vertical;}
.bp-color{display:flex;align-items:center;gap:8px;}
.bp-color input[type=color]{width:34px;height:26px;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);padding:0;background:none;cursor:pointer;}
.bp-empty{color:var(--fs-text-3,#8f959e);font-size:12px;padding:6px 2px;}
.bp-image{display:flex;align-items:center;gap:10px;}
.bp-image-preview{width:64px;height:48px;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);display:flex;align-items:center;justify-content:center;overflow:hidden;background:var(--fs-bg-sunken,#f7f8fa);flex:none;}
.bp-image-preview img{max-width:100%;max-height:100%;object-fit:contain;}
.bp-image-empty{font-size:11px;color:var(--fs-text-3,#8f959e);}
.bp-image-btn{border:1px solid var(--fs-border-control,#d9dce0);background:#fff;border-radius:var(--fs-r-xs,4px);padding:5px 10px;font-size:12px;cursor:pointer;color:var(--fs-text-1,#1f2329);}
.bp-image-btn:hover{border-color:var(--fs-brand,#1456f0);color:var(--fs-brand,#1456f0);}
`;
  (doc.head || doc.documentElement).appendChild(st);
}

// mountBlockPanel 挂载面板。
//   mount: 容器元素
//   accountId: 账号 id（决定 localStorage 覆盖 key）
//   getModule: () => 后台默认 bottom_blocks 模块（原始服务端模块）
//   onChange: (override) => void —— 宿主据此注入渲染并重渲
//   initialOverride: 可选，外部已加载的覆盖（否则从 localStorage 读）
function assetsOf(lib) {
  if (Array.isArray(lib)) return lib;
  if (isObj(lib) && Array.isArray(lib.assets)) return lib.assets;
  return [];
}

function assetIDFromRef(value) {
  const m = /^\{\{asset#([a-z0-9_-]+)\}\}$/i.exec(String(value == null ? '' : value).trim());
  return m ? m[1] : '';
}

function assetLabelFor(block, assetLibrary, accountId) {
  const ref = block && block.fields && block.fields.image && block.fields.image.value;
  const id = assetIDFromRef(ref);
  if (!id) return '';
  for (const asset of assetsOf(assetLibrary)) {
    const scope = asset && (asset.scope || 'global');
    if (asset && asset.id === id && (scope === 'global' || scope === accountId)) {
      return String(asset.label || '').trim();
    }
  }
  return '';
}

// blockDisplayLabel：左侧列表显示「这个块是什么」。优先用标题字段，再用上方说明；
// name_card 的上方说明可空，此时保留类型名「名片」（比素材文件名更像一个可编辑块）。
// 其他图片型区块可退到素材名，便于区分系列名片/二维码。
function blockDisplayLabel(block, type, assetLibrary, accountId) {
  const t = block && block.fields && block.fields.title && block.fields.title.value;
  if (typeof t === 'string') {
    const cleaned = t.replace(/^[\s/]+|[\s/]+$/g, '').trim();
    if (cleaned) return cleaned;
  }
  const intro = block && block.fields && block.fields.intro_text && block.fields.intro_text.value;
  if (typeof intro === 'string' && intro.trim()) return intro.trim();
  if (block && block.type === 'name_card') return (type && type.label) || block.type;
  const label = assetLabelFor(block, assetLibrary, accountId);
  if (label) return label;
  return (type && type.label) || (block && block.type) || '';
}

// mountBlockPanel 参数 getBlockExtra(block) => HTMLElement|null：宿主可为某些块（如 more_articles）
// 返回一个【稳定持有的】DOM 元素，挂进该块展开区。宿主必须持引用（非 getElementById），
// 这样 render() 的 mount.innerHTML='' 把它从 DOM 摘下后元素对象仍存活，下次 render 再 append 即复位。
export function mountBlockPanel({ mount, accountId, getModule, getAssetLibrary, getBlockExtra, onChange, doc = document }) {
  if (!mount) return null;
  ensureStyle(doc);
  let override = loadOverride(accountId);
  const expanded = new Set();
  let sortable = null;
  let panelOpen = true;      // 手风琴整段默认展开，chevron 折叠
  let justExpanded = null;   // 刚点开字段的区块 id，render 后做一次淡入
  // 迁移：旧版「主开关」可写 override.enabled=false（整段隐藏）。新版去掉主开关、改用每块开关+计数，
  // 清掉遗留 enabled，避免老文章卡在「整段关闭且无 UI 可恢复」。order/overrides 保留。
  if (override && 'enabled' in override) { delete override.enabled; saveOverride(accountId, override); }

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

  function setEnabled(id, val, def) {
    const b = ensureBlock(id);
    if (val === def) delete b.enabled; else b.enabled = val;
    prune(id); emit(); render();
  }
  // 第 4 参 same = 新值是否等于默认（各调用处已算好，与下方 setFieldSoft 同约定）：
  // 相同则不落盘（恢复继承默认），否则记为本篇覆盖。
  // 旧实现误写 `val === def`——对布尔型 switch 会把「关掉默认开的开关」(val=false, same=false)
  // 判成 false===false 而删除覆盖，导致默认开的开关关不掉、回弹。改用 same 与 setEnabled/setFieldSoft 对齐。
  function setField(id, key, val, same) {
    const b = ensureBlock(id);
    if (!isObj(b.fields)) b.fields = {};
    if (same) delete b.fields[key]; else b.fields[key] = val;
    prune(id); emit(); render();
  }
  // 静默版：写覆盖 + 持久化 + 刷正文预览，但不重渲面板——供格式浮层用，避免每次改值重建 DOM 关掉浮层。
  function setFieldQuiet(id, key, val, same) {
    const b = ensureBlock(id);
    if (!isObj(b.fields)) b.fields = {};
    if (same) delete b.fields[key]; else b.fields[key] = val;
    prune(id); emit();
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

  // SortableJS 拖完后回调：domIds 是拖拽后 DOM 里 .bp-card 的新顺序。
  // 只接受合法 block id（过滤脏 id，再补回缺失项，避免 resolveBlocks 静默丢块 → 卡片“消失”），
  // 与拖拽前的当前顺序一致（拖了又放回原位）时直接返回不落盘，否则存为单篇 order 覆盖。
  function applyOrder(domIds, baseOrder) {
    const valid = domIds.filter(id => baseOrder.includes(id));
    for (const id of baseOrder) if (!valid.includes(id)) valid.push(id);
    const same = valid.length === baseOrder.length && valid.every((id, i) => id === baseOrder[i]);
    if (same) return;
    override.order = valid;
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
    // 徽标只在本篇已覆盖后台默认时出现；未覆盖（绝大多数字段）保持干净，无常驻「继承默认」药丸。
    label.innerHTML = `<span>${esc(field.label || key)}</span>` +
      (overridden ? `<span class="bp-badge over">本篇已覆盖</span>` : '');
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
      // 拨动开关（与主开关/显隐开关同款 .bp-switch），label 同行右对齐，不再是裸 checkbox。
      ctrl = doc.createElement('input'); ctrl.type = 'checkbox'; ctrl.className = 'bp-switch'; ctrl.checked = !!val;
      ctrl.style.marginLeft = 'auto';
      ctrl.onchange = () => setField(id, key, ctrl.checked, ctrl.checked === !!def);
    } else if (field.type === 'image') {
      // 素材库选图（不再让用户手填 URL / {{asset}} 语法）：缩略图 + 「从素材库选择」按钮。
      ctrl = doc.createElement('div'); ctrl.className = 'bp-image';
      const lib = getAssetLibrary ? getAssetLibrary() : null;
      const thumb = resolveAssetThumb(val, lib, accountId);
      const preview = doc.createElement('div'); preview.className = 'bp-image-preview' + (thumb ? '' : ' bp-image-empty');
      if (thumb) { const im = doc.createElement('img'); im.src = thumb; im.alt = ''; preview.appendChild(im); }
      else { preview.textContent = '未选择'; }
      const btn = doc.createElement('button'); btn.type = 'button'; btn.className = 'bp-image-btn';
      btn.textContent = thumb ? '更换图片' : '从素材库选择';
      btn.onclick = () => openAssetPicker({
        assetLibrary: getAssetLibrary ? getAssetLibrary() : null,
        scope: accountId, doc,
        onPick: (assetId) => { const ref = '{{asset#' + assetId + '}}'; setField(id, key, ref, ref === def); },
        onClear: () => setField(id, key, '', '' === def),
      });
      ctrl.appendChild(preview); ctrl.appendChild(btn);
    } else { // text
      ctrl = doc.createElement('input'); ctrl.className = 'bp-input'; ctrl.type = 'text';
      ctrl.value = String(val == null ? '' : val);
      ctrl.oninput = () => setField(id, key, ctrl.value, ctrl.value === def);
    }
    // switch 的开关并入 label 行右侧；其余控件另起一行。tooltip 不再常驻一行（多为 label 回显的噪音），
    // 改挂 hover title——需要时悬停可见，平时「不显性化」，面板更干净。
    if (field.type === 'switch') {
      if (field.tooltip) ctrl.title = field.tooltip;
      label.appendChild(ctrl);
    } else {
      wrap.appendChild(ctrl);
      if (field.tooltip) wrap.title = field.tooltip;
    }
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
    if (sortable) { try { sortable.destroy(); } catch {} sortable = null; }
    closeOpenFormatPopovers();   // 关掉挂在 body 上的格式浮层，避免重渲后变孤儿
    mount.innerHTML = '';
    const wrap = doc.createElement('div');
    wrap.className = 'bp-wrap';

    // 手风琴头：层叠图标 +「底部区块」+ 启用/总数计数 + 折叠 chevron（整段可折叠，无主开关；
    // 全关=0/N 即等价隐藏整段，参考稿口径）。
    const acc = doc.createElement('div');
    acc.className = 'bp-acc' + (panelOpen ? ' is-open' : '');
    const accHead = doc.createElement('button');
    accHead.type = 'button'; accHead.className = 'bp-acc-head';
    const enabledCount = resolved.blocks.filter(b => b.enabled).length;
    accHead.innerHTML =
      `<span class="bp-ico">${ICON_LAYERS}</span>` +
      `<span class="bp-acc-title">底部区块</span>` +
      `<span class="bp-count">${enabledCount}/${resolved.blocks.length}</span>` +
      `<span class="bp-chevron">${ICON_CHEVRON}</span>`;
    acc.appendChild(accHead);

    const body = doc.createElement('div'); body.className = 'bp-acc-body';
    body.style.height = panelOpen ? 'auto' : '0';
    const inner = doc.createElement('div'); inner.className = 'bp-acc-inner';
    body.appendChild(inner);
    acc.appendChild(body);
    wrap.appendChild(acc);

    // chevron 折叠：只动 body 高度 + 旋转，不整树重渲（保留内容/输入态）。
    accHead.onclick = () => {
      panelOpen = !panelOpen;
      acc.classList.toggle('is-open', panelOpen);
      if (prefersReduced() || !window.gsap) { body.style.height = panelOpen ? 'auto' : '0'; return; }
      if (panelOpen) {
        window.gsap.set(body, { height: 'auto' });
        if (window.gFrom) window.gFrom(body, { height: 0, duration: 0.3, ease: 'power3.out' });
        if (window.gFrom) window.gFrom(inner.querySelectorAll('.bp-card, .bp-hint, .bp-resetall'),
          { y: 8, opacity: 0, duration: 0.26, stagger: 0.035, delay: 0.04, ease: 'power2.out' });
      } else if (window.gTo) {
        window.gTo(body, { height: 0, duration: 0.24, ease: 'power2.inOut' });
      }
    };

    if (!resolved.blocks.length) {
      const e = doc.createElement('div'); e.className = 'bp-empty'; e.textContent = '该规则暂无可配置的底部区块。';
      inner.appendChild(e); mount.appendChild(wrap); return;
    }

    const list = doc.createElement('div'); list.className = 'bp-list';
    for (const block of resolved.blocks) {
      const type = getBlockType(block.type) || { label: block.type, fields: [] };
      const card = doc.createElement('div');
      card.className = 'bp-card' + (block.enabled ? '' : ' bp-disabled');
      card.dataset.id = block.id;

      const row = doc.createElement('div'); row.className = 'bp-row';
      const grip = doc.createElement('span'); grip.className = 'bp-grip'; grip.innerHTML = ICON_GRIP; grip.title = '拖拽排序';
      const cb = doc.createElement('input'); cb.type = 'checkbox'; cb.className = 'bp-switch'; cb.checked = block.enabled;
      cb.title = '在本篇显示/隐藏该区块';
      cb.onchange = () => setEnabled(block.id, cb.checked, block.enabledDefault);
      const name = doc.createElement('span'); name.className = 'bp-name';
      name.innerHTML = esc(blockDisplayLabel(block, type, getAssetLibrary ? getAssetLibrary() : null, accountId)) + (block.enabledOverridden ? '<span class="bp-sub">本篇已改开关</span>' : '');
      const caret = doc.createElement('span');
      const isOpen = expanded.has(block.id) || (block.enabled && !expanded.has('__c__' + block.id));
      caret.className = 'bp-caret' + (isOpen ? ' bp-open' : ''); caret.innerHTML = ICON_CHEVRON;
      const toggle = () => {
        if (isOpenNow(block)) { expanded.delete(block.id); expanded.add('__c__' + block.id); }
        else { expanded.add(block.id); expanded.delete('__c__' + block.id); justExpanded = block.id; }
        render();
      };
      name.onclick = toggle; caret.onclick = toggle;
      row.appendChild(grip); row.appendChild(name);
      if (blockHasOverride(block.id)) {
        const rb = doc.createElement('button');
        rb.type = 'button'; rb.className = 'bp-reset'; rb.innerHTML = ICON_RESET; rb.title = '此区块恢复后台默认';
        rb.onclick = (ev) => { ev.stopPropagation(); resetBlock(block.id); };
        row.appendChild(rb);
      }
      row.appendChild(cb); row.appendChild(caret);
      card.appendChild(row);

      if (isOpenNow(block)) {
        const fields = doc.createElement('div'); fields.className = 'bp-fields';
        const defs = type.fields || [];
        const consumed = new Set();
        for (const f of defs) {
          if (consumed.has(f.key)) continue;
          // 文本字段 + 其格式兄弟（<base>_size / <base>_color，可选 center 对齐）→ 收成「格式片+浮层」，
          // 不再平铺字号/颜色/居中三行（参考稿 format-editor.html）。base 去掉 _text 后缀以兼容 intro_text。
          if (f.type === 'text') {
            const base = f.key.replace(/_text$/, '');
            const sizeF = defs.find(x => x.key === base + '_size' && x.type === 'number');
            const colorF = defs.find(x => x.key === base + '_color' && x.type === 'color');
            const alignF = defs.find(x => x.key === 'center' && x.type === 'switch');
            if (sizeF || colorF) {
              fields.appendChild(createTitleFormatControl({
                doc, esc, block, textField: f, sizeField: sizeF, colorField: colorF, alignField: alignF,
                setField: (key, val, same) => setFieldQuiet(block.id, key, val, same),
              }));
              consumed.add(f.key);
              if (sizeF) consumed.add(sizeF.key);
              if (colorF) consumed.add(colorF.key);
              if (alignF) consumed.add(alignF.key);
              continue;
            }
          }
          fields.appendChild(fieldControl(block, f));
        }
        // 宿主注入的内嵌编辑器（more_articles 的卡片列表搬进区块展开区，消除并列的独立面板）。
        const extra = getBlockExtra ? getBlockExtra(block) : null;
        if (extra) fields.appendChild(extra);
        if (fields.childNodes.length) card.appendChild(fields);
      }
      list.appendChild(card);
    }
    inner.appendChild(list);

    const hint = doc.createElement('div'); hint.className = 'bp-hint';
    hint.textContent = '勾选显示/隐藏，拖拽排序，展开可改本篇字段；改动只影响当前文章。';
    inner.appendChild(hint);
    if (hasAnyOverride(override)) {
      const ra = doc.createElement('button'); ra.type = 'button'; ra.className = 'bp-resetall'; ra.textContent = '全部恢复默认';
      ra.onclick = resetAll; inner.appendChild(ra);
    }
    mount.appendChild(wrap);

    // 拖拽排序交给 SortableJS（grip 为唯一把手；只对 .bp-list 内 .bp-card 排序，仅展开时启用）。
    // 拖完一次性按 DOM 顺序回写 override.order 并重建——拖拽会话已结束，render() 销毁旧实例安全。
    if (window.Sortable && resolved.blocks.length > 1 && panelOpen) {
      sortable = window.Sortable.create(list, {
        handle: '.bp-grip',
        draggable: '.bp-card',
        animation: 150,
        forceFallback: true,
        onEnd: () => applyOrder(
          Array.from(list.querySelectorAll('.bp-card')).map(c => c.dataset.id),
          baseOrder
        ),
      });
    }

    // 刚点开字段的区块：fields 高度+透明淡入一次（每块展开的微动效）。
    if (justExpanded && window.gFrom && window.gsap && !prefersReduced()) {
      const card = Array.from(list.querySelectorAll('.bp-card')).find(c => c.dataset.id === justExpanded);
      const f = card && card.querySelector('.bp-fields');
      if (f) window.gFrom(f, { height: 0, opacity: 0, duration: 0.28, ease: 'power3.out', clearProps: 'height' });
    }
    justExpanded = null;
  }

  // 默认开合规则：全部默认折叠，避免底部面板一打开就过长；用户手动开合后以 expanded 记录为准。
  function isOpenNow(block) {
    if (expanded.has(block.id)) return true;
    if (expanded.has('__c__' + block.id)) return false;
    return false;
  }

  render();
  return {
    getOverride: () => override,
    refresh: render,
    destroy: () => {
      if (sortable) { try { sortable.destroy(); } catch {} sortable = null; }
      mount.innerHTML = '';
    },
  };
}
