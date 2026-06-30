// block-panel.js — 前台「底部区块」单篇内容编辑区（公众号 + 网站共用）。
//
// 设计：底部结构、排序、字段样式归规则后台；前台只做「本篇例外」。
//   (a) 顶层“底部区域”总开关控制本篇是否输出整段底部，必须与预览/复制所见一致；
//   (b) 各子模块只控制本篇显隐，间距等纯排版块不作为可勾选内容出现；
//   (c) more_articles 块的卡片编辑器嵌在“更多文章”子模块下；
//   (d) 被后台标记 frontend_editable=true 的图片块只暴露「更换图片」（走素材库，红线#10）。
// 「哪块可前端编辑」仍由块实例级 frontend_editable 驱动；作者差异走 article_footer_defaults 数据方案，
// 前台只显示命中的“文章专属设置”，不为每个作者硬编码模块。
//
// 中性：只 import 中性合并引擎/注册表/素材选择器，不 import 任何渠道渲染器（红线 #19）。
// 宿主（app.html）通过 onChange(override) 把 override 注入渲染并重渲；据 hasEditableContent() 决定整段显隐。

import { resolveBlocks, hasAnyOverride, stripReadonlyOverride } from './block-merge.js';
import { resolveBottomBlocksForArticle } from '../article-footer-defaults.js';
import { getBlockType } from './block-registry.gen.js';
import { openAssetPicker, resolveAssetThumb } from './asset-picker.js';
import { createIcon } from '../ui-icons.js';

function isObj(v) { return !!v && typeof v === 'object' && !Array.isArray(v); }
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
.bp-wrap{font-size:13px;color:var(--fs-text-1,#1f2329);display:flex;flex-direction:column;gap:16px;}
.bp-sec-title{font-weight:600;font-size:13px;color:var(--fs-text-1,#1f2329);margin-bottom:8px;}
.bp-hint{color:var(--fs-text-3,#8f959e);font-size:11px;line-height:1.6;}
.bp-empty{color:var(--fs-text-3,#8f959e);font-size:12px;padding:6px 2px;}
.bp-image{display:flex;align-items:center;gap:10px;}
.bp-image-preview{width:64px;height:48px;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);display:flex;align-items:center;justify-content:center;overflow:hidden;background:var(--fs-bg-sunken,#f7f8fa);flex:none;}
.bp-image-preview img{max-width:100%;max-height:100%;object-fit:contain;}
.bp-image-empty{font-size:11px;color:var(--fs-text-3,#8f959e);}
.bp-image-btn{border:1px solid var(--fs-border-control,#d9dce0);background:#fff;border-radius:var(--fs-r-xs,4px);padding:5px 10px;font-size:12px;cursor:pointer;color:var(--fs-text-1,#1f2329);}
.bp-image-btn:hover{border-color:var(--fs-brand,#1456f0);color:var(--fs-brand,#1456f0);}
.bp-module{display:flex;flex-direction:column;gap:10px;}
.bp-module-head{display:flex;align-items:center;justify-content:space-between;gap:10px;}
.bp-module-title{display:flex;align-items:center;gap:8px;font-weight:650;color:var(--fs-text-1,#1f2329);}
.bp-icon{width:16px;height:16px;display:inline-flex;align-items:center;justify-content:center;color:var(--fs-text-2,#646a73);flex:none;}
.bp-icon svg{display:block;width:16px;height:16px;}
.bp-profile-badge{font-size:11px;color:var(--fs-text-3,#6f757d);white-space:nowrap;}
.bp-group{background:var(--fs-bg-nested,#f7f8fa);border-radius:var(--fs-r-m,8px);padding:10px;}
.bp-group-head{display:flex;align-items:center;gap:8px;font-size:12px;font-weight:600;color:var(--fs-text-2,#646a73);margin-bottom:6px;}
.bp-toggle-row{display:flex;align-items:center;justify-content:space-between;gap:10px;min-height:32px;padding:2px 0;}
.bp-toggle-label{display:flex;align-items:center;gap:8px;min-width:0;color:var(--fs-text-1,#1f2329);}
.bp-switch{width:36px;height:20px;border:0;border-radius:var(--fs-r-full,9999px);background:var(--fs-bg-sunken,#f2f3f5);box-shadow:inset 0 0 0 1px var(--fs-border-control,#d9dce0);cursor:pointer;position:relative;flex:none;padding:0;transition:background var(--fs-t-fast,150ms) var(--fs-ease-out,ease-out),box-shadow var(--fs-t-fast,150ms) var(--fs-ease-out,ease-out);}
.bp-switch[aria-checked="true"]{background:var(--fs-brand,#1456f0);box-shadow:inset 0 0 0 1px var(--fs-brand,#1456f0);}
.bp-switch:hover{box-shadow:inset 0 0 0 1px var(--fs-brand,#1456f0);}
.bp-switch:focus{outline:none;}
.bp-switch:focus-visible{box-shadow:0 0 0 2px var(--fs-brand-soft,#ebf1ff),inset 0 0 0 1px var(--fs-brand,#1456f0);}
.bp-switch:disabled{cursor:not-allowed;opacity:.56;}
.bp-switch-knob{position:absolute;top:2px;left:2px;width:16px;height:16px;border-radius:var(--fs-r-full,9999px);background:var(--fs-bg-card,#fff);box-shadow:var(--fs-sh-s1,0 1px 2px rgba(31,35,39,.12));transition:transform var(--fs-t-fast,150ms) var(--fs-ease-out,ease-out);}
.bp-switch[aria-checked="true"] .bp-switch-knob{transform:translateX(16px);}
.bp-footer{display:flex;flex-direction:column;gap:10px;}
.bp-footer-details{background:var(--fs-bg-nested,#f7f8fa);border-radius:var(--fs-r-m,8px);padding:0;}
.bp-footer-details summary{display:flex;align-items:center;justify-content:space-between;gap:10px;min-height:40px;padding:0 10px;cursor:pointer;outline:none;list-style:none;}
.bp-footer-details summary::-webkit-details-marker{display:none;}
.bp-footer-details summary::marker{content:'';}
.bp-summary-left{display:flex;align-items:center;gap:8px;min-width:0;font-size:13px;font-weight:650;color:var(--fs-text-1,#1f2329);}
.bp-chevron{transition:transform var(--fs-t-fast,150ms) var(--fs-ease-out,ease-out);}
.bp-footer-details[open] .bp-chevron{transform:rotate(90deg);}
.bp-parent-switch{display:flex;align-items:center;gap:6px;color:var(--fs-text-2,#646a73);font-size:11px;white-space:nowrap;}
.bp-block-list{display:flex;flex-direction:column;gap:4px;padding:0 10px 10px;}
.bp-footer-off-note{margin:0 10px 10px;padding:8px 10px;border-radius:var(--fs-r-s,6px);background:var(--fs-bg-sunken,#f2f3f5);color:var(--fs-text-2,#646a73);font-size:12px;line-height:1.5;}
.bp-block-item{border-top:1px solid var(--fs-divider,rgba(31,35,39,.08));}
.bp-block-item:first-child{border-top:0;}
.bp-block-check{display:flex;align-items:center;justify-content:space-between;gap:10px;min-height:34px;padding:4px 0;}
.bp-block-label{display:flex;align-items:center;gap:8px;min-width:0;color:var(--fs-text-1,#1f2329);}
.bp-block-actions{display:flex;align-items:center;gap:8px;flex:none;}
.bp-block-name{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;}
.bp-artifact-name{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;}
.bp-block-default{font-size:11px;color:var(--fs-text-3,#8f959e);white-space:nowrap;}
.bp-block-nested{padding:6px 0 10px 23px;}
.bp-block-nested:empty{display:none;}
`;
  (doc.head || doc.documentElement).appendChild(st);
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

// blockDisplayLabel：该可编辑块的只读小标题。优先标题字段，再上方说明，name_card 用类型名，图片块退到素材名。
function blockDisplayLabel(block, type, assetLibrary, accountId) {
  const custom = String(block && block.displayName || block && block.display_name || '').trim();
  if (custom) return custom;
  const t = block && block.fields && block.fields.title && block.fields.title.value;
  if (typeof t === 'string') {
    const cleaned = t.replace(/^[\s/]+|[\s/]+$/g, '').trim();
    if (cleaned) return cleaned;
  }
  const intro = block && block.fields && block.fields.intro_text && block.fields.intro_text.value;
  if (typeof intro === 'string' && intro.trim()) return intro.trim();
  const label = assetLabelFor(block, assetLibrary, accountId);
  if (label) return label;
  return (type && type.label) || (block && block.type) || '';
}

// 该块在前端编辑区是否产出可编辑内容位：more_articles（恒为内容编辑器）或 frontend_editable 且含 image 字段。
// 仅对 enabled（会渲进正文）的块判定——隐藏块没有可编辑内容。
function blockEditableKind(block, type) {
  if (!block.enabled) return null;
  if (block.type === 'more_articles') return 'more_articles';
  if (block.frontendEditable && (type.fields || []).some(f => f.type === 'image')) return 'image';
  return null;
}

function cleanVisibilityOverride(value) {
  if (!isObj(value)) return {};
  const out = {};
  if (value.enabled === true || value.enabled === false || value.enabled === 'show' || value.enabled === 'hide') out.enabled = value.enabled;
  if (isObj(value.blocks)) {
    const blocks = {};
    for (const id of Object.keys(value.blocks)) {
      const v = value.blocks[id];
      if (v === true || v === false || v === 'show' || v === 'hide') blocks[id] = v;
    }
    if (Object.keys(blocks).length) out.blocks = blocks;
  }
  return out;
}

function clearEmptyVisibilityOverride(value) {
  const out = cleanVisibilityOverride(value);
  if (isObj(out.blocks) && !Object.keys(out.blocks).length) delete out.blocks;
  return Object.keys(out).length ? out : undefined;
}

function cleanArticleArtifactOverride(value) {
  if (!isObj(value)) return {};
  const out = {};
  for (const id of Object.keys(value)) {
    const v = value[id];
    if (v === true || v === false || v === 'show' || v === 'hide' || v === 'hidden') {
      out[id] = v;
    } else if (isObj(v)) {
      const next = {};
      if (v.hidden === true || v.hidden === false) next.hidden = v.hidden;
      if (v.enabled === true || v.enabled === false) next.enabled = v.enabled;
      if (Object.keys(next).length) out[id] = next;
    }
  }
  return out;
}

function clearEmptyArticleArtifactOverride(value) {
  const out = cleanArticleArtifactOverride(value);
  return Object.keys(out).length ? out : undefined;
}

function artifactEnabled(value, id, defaultEnabled = true) {
  const v = cleanArticleArtifactOverride(value)[id];
  if (v === undefined) return defaultEnabled;
  if (v === true || v === 'show') return true;
  if (v === false || v === 'hide' || v === 'hidden') return false;
  if (isObj(v) && (v.hidden === false || v.enabled === true)) return true;
  if (isObj(v) && (v.hidden === true || v.enabled === false)) return false;
  return defaultEnabled;
}

function isMoteFooterProfile(profile) {
  const id = String(profile && profile.id || '').toLowerCase();
  const name = String(profile && profile.name || '').toLowerCase();
  return id.includes('mote') || name.includes('mote');
}

const ARTICLE_ARTIFACT_CONTROLS = {
  mote_ref_author_card: {
    label: '参考文献前署名图',
    icon: 'image',
  },
};

function artifactControlIDs(profile) {
  const declared = cleanArticleArtifactOverride(profile && profile.artifactVisibility);
  const ids = Object.keys(declared).filter(id => ARTICLE_ARTIFACT_CONTROLS[id]);
  if (!ids.length && isMoteFooterProfile(profile)) return ['mote_ref_author_card'];
  return ids;
}

function blockByID(resolved, id) {
  return ((resolved && resolved.blocks) || []).find(b => b.id === id) || null;
}

function createSwitchControl(doc, {
  checked,
  disabled = false,
  label = '',
  dataset = {},
  onChange,
}) {
  const btn = doc.createElement('button');
  btn.type = 'button';
  btn.className = 'bp-switch';
  btn.setAttribute('role', 'switch');
  btn.setAttribute('aria-checked', checked ? 'true' : 'false');
  if (label) btn.setAttribute('aria-label', label);
  if (disabled) {
    btn.disabled = true;
    btn.setAttribute('aria-disabled', 'true');
  }
  for (const key of Object.keys(dataset || {})) btn.dataset[key] = dataset[key];

  const knob = doc.createElement('span');
  knob.className = 'bp-switch-knob';
  btn.appendChild(knob);

  function toggle(e) {
    if (e) {
      e.preventDefault();
      e.stopPropagation();
    }
    if (btn.disabled || !onChange) return;
    onChange(btn.getAttribute('aria-checked') !== 'true');
  }

  btn.onclick = toggle;
  btn.onkeydown = (e) => {
    if (e.key !== ' ' && e.key !== 'Spacebar' && e.key !== 'Enter') return;
    toggle(e);
  };
  return btn;
}

function cssAttrValue(value) {
  return String(value == null ? '' : value)
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"');
}

function focusSwitch(doc, mount, selector) {
  if (!selector) return;
  const el = mount.querySelector(selector);
  if (!el || typeof el.focus !== 'function') return;
  try {
    el.focus({ preventScroll: true });
  } catch {
    el.focus();
  }
}

function focusSelectorFromActiveElement(doc, mount) {
  const active = doc.activeElement;
  if (!active || !mount.contains(active) || active.getAttribute('role') !== 'switch') return '';
  if (active.dataset.footerSwitch) {
    return '[data-footer-switch="' + cssAttrValue(active.dataset.footerSwitch) + '"][role="switch"]';
  }
  if (active.dataset.blockSwitch) {
    return '[data-block-switch="' + cssAttrValue(active.dataset.blockSwitch) + '"][role="switch"]';
  }
  if (active.dataset.artifactId) {
    return '[data-artifact-id="' + cssAttrValue(active.dataset.artifactId) + '"][role="switch"]';
  }
  return '';
}

function captureScrollState(doc, mount) {
  const seen = new Set();
  const state = [];
  function add(el) {
    if (!el || seen.has(el)) return;
    seen.add(el);
    state.push({
      el,
      top: el.scrollTop || 0,
      left: el.scrollLeft || 0,
    });
  }

  add(mount);
  for (let el = mount.parentElement; el && el !== doc; el = el.parentElement) {
    const cls = typeof el.className === 'string' ? el.className : '';
    if (el.scrollTop || el.scrollLeft || /\bscroll-y\b/.test(cls) || /\bsidebar-body\b/.test(cls)) add(el);
    if (el === doc.body || el === doc.documentElement) break;
  }
  add(doc.scrollingElement || doc.documentElement || doc.body);
  return () => {
    for (const item of state) {
      if (!item.el) continue;
      item.el.scrollTop = item.top;
      item.el.scrollLeft = item.left;
    }
  };
}

function moduleHasRenderableFooter(module) {
  if (!isObj(module) || module.enabled === false) return false;
  if (Array.isArray(module.presets)) {
    return module.presets.some(preset => {
      if (!isObj(preset) || !isObj(preset.blocks)) return false;
      return Object.keys(preset.blocks).some(id => {
        const block = preset.blocks[id];
        return isObj(block) && block.type !== 'spacer';
      });
    });
  }
  if (isObj(module.blocks)) {
    return Object.keys(module.blocks).some(id => {
      const block = module.blocks[id];
      return isObj(block) && block.type !== 'spacer';
    });
  }
  return false;
}

// mountBlockPanel 挂载内容编辑区。
//   mount, accountId, getModule, getAssetLibrary
//   getBlockExtra(block) => HTMLElement|null：宿主为 more_articles 返回稳定持有的卡片编辑器元素
//   onChange(override) => void：宿主据此注入渲染并重渲
export function mountBlockPanel({
  mount,
  accountId,
  getModule,
  getAssetLibrary,
  getBlockExtra,
  onChange,
  getArticleMeta,
  getFooterDefaults,
  getFooterVisibilityOverride,
  onFooterVisibilityChange,
  getArticleArtifactOverride,
  onArticleArtifactChange,
  doc = document,
}) {
  if (!mount) return null;
  ensureStyle(doc);
  let override = loadOverride(accountId);
  let editableCount = 0;

  function emit() { saveOverride(accountId, override); if (onChange) onChange(override); }
  function emitFooterVisibility(next) {
    const cleaned = clearEmptyVisibilityOverride(next);
    if (onFooterVisibilityChange) onFooterVisibilityChange(cleaned);
  }
  function emitArticleArtifact(next) {
    const cleaned = clearEmptyArticleArtifactOverride(next);
    if (onArticleArtifactChange) onArticleArtifactChange(cleaned);
  }
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
      if (!b.fields) delete o[id];
    }
    if (override.overrides && !Object.keys(override.overrides).length) delete override.overrides;
  }
  // 写图片字段覆盖（同等于默认则不落盘=恢复后台默认）。这是新版前端唯一会写的 override 形状：
  // {overrides:{<frontend_editable块>:{fields:{<imageKey>:val}}}}。
  function setField(id, key, val, same) {
    const b = ensureBlock(id);
    if (!isObj(b.fields)) b.fields = {};
    if (same) delete b.fields[key]; else b.fields[key] = val;
    prune(id); emit(); render();
  }

  // 存量覆盖迁移（确定性、幂等）：旧版前端可写整段显隐(enabled)/排序(order)/任意块字段(显隐/字号/调色)。
  // 新版结构归后台，前端只保留 frontend_editable 块的图片字段覆盖。加载时一次性剥离已不可前端编辑的残留，
  // 避免老文章底部与后台默认不一致且无 UI 可恢复（红线#22 口径以后台默认为准）。
  function migrateStaleOverride() {
    const mod = getModule() || {};
    const base = resolveBlocks(mod, {}); // 用空覆盖解析，frontendEditable 取后台默认，与当前 order 无关
    if (!base.blocks.length) {
      // 模块未就绪：只安全剥离与块无关的结构性 enabled/order，块级覆盖待模块就绪再处理。
      let changed = false;
      if ('enabled' in override) { delete override.enabled; changed = true; }
      if ('order' in override) { delete override.order; changed = true; }
      if (changed) saveOverride(accountId, override);
      return;
    }
    const editable = new Set(base.blocks.filter(b => b.frontendEditable).map(b => b.id));
    const next = stripReadonlyOverride(override, editable);
    if (JSON.stringify(next) !== JSON.stringify(override)) { override = next; saveOverride(accountId, override); }
  }

  // 单个图片字段的「缩略图 + 更换图片」（走素材库，红线#10 不手填 URL）。
  function imageSwap(block, field) {
    const id = block.id, key = field.key;
    const rf = block.fields[key] || { value: field.default, default: field.default };
    const val = rf.value, def = rf.default;
    const wrap = doc.createElement('div'); wrap.className = 'bp-image';
    const thumb = resolveAssetThumb(val, getAssetLibrary ? getAssetLibrary() : null, accountId);
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
    wrap.appendChild(preview); wrap.appendChild(btn);
    return wrap;
  }

  function footerState(mod) {
    const articleMeta = getArticleMeta ? getArticleMeta() : {};
    const footerDefaults = getFooterDefaults ? getFooterDefaults() : null;
    const visibilityOverride = cleanVisibilityOverride(getFooterVisibilityOverride ? getFooterVisibilityOverride() : {});
    const base = resolveBottomBlocksForArticle({
      module: mod,
      articleMeta,
      footerDefaults,
      contentOverride: override,
    });
    const resolved = resolveBottomBlocksForArticle({
      module: mod,
      articleMeta,
      footerDefaults,
      contentOverride: override,
      visibilityOverride,
    });
    return { base, resolved, visibilityOverride };
  }

  function setFooterEnabled(state, checked) {
    const next = cleanVisibilityOverride(state.visibilityOverride);
    if (checked === state.base.enabled) delete next.enabled;
    else next.enabled = checked ? 'show' : 'hide';
    emitFooterVisibility(next);
    render({ focusSelector: '[data-footer-switch="enabled"][role="switch"]' });
  }

  function setBlockEnabled(state, id, checked) {
    const next = cleanVisibilityOverride(state.visibilityOverride);
    if (!isObj(next.blocks)) next.blocks = {};
    const baseBlock = blockByID(state.base, id);
    const baseEnabled = baseBlock ? baseBlock.enabled : true;
    if (checked === baseEnabled) delete next.blocks[id];
    else next.blocks[id] = checked ? 'show' : 'hide';
    if (!Object.keys(next.blocks).length) delete next.blocks;
    emitFooterVisibility(next);
    render({ focusSelector: '[data-block-switch="' + cssAttrValue(id) + '"][role="switch"]' });
  }

  function setArticleArtifact(id, checked, defaultEnabled = true) {
    const next = cleanArticleArtifactOverride(getArticleArtifactOverride ? getArticleArtifactOverride() : {});
    if (checked === defaultEnabled) delete next[id];
    else next[id] = checked ? 'show' : 'hide';
    emitArticleArtifact(next);
    render({ focusSelector: '[data-artifact-id="' + cssAttrValue(id) + '"][role="switch"]' });
  }

  function renderArtifactControls(moduleWrap, profile) {
    const ids = artifactControlIDs(profile);
    if (!ids.length) return;
    const defaults = cleanArticleArtifactOverride(profile && profile.artifactVisibility);
    const group = doc.createElement('div');
    group.className = 'bp-group';
    const head = doc.createElement('div');
    head.className = 'bp-group-head';
    head.appendChild(createIcon('image', doc, { className: 'bp-icon' }));
    const headText = doc.createElement('span');
    headText.textContent = '文中元素';
    head.appendChild(headText);
    group.appendChild(head);

    for (const id of ids) {
      const control = ARTICLE_ARTIFACT_CONTROLS[id];
      const defaultEnabled = artifactEnabled(defaults, id, true);
      const row = doc.createElement('div');
      row.className = 'bp-toggle-row';
      const label = doc.createElement('span');
      label.className = 'bp-toggle-label';
      const name = doc.createElement('span');
      name.className = 'bp-artifact-name';
      name.textContent = control.label;
      label.appendChild(name);
      row.appendChild(label);
      row.appendChild(createSwitchControl(doc, {
        checked: artifactEnabled(getArticleArtifactOverride ? getArticleArtifactOverride() : {}, id, defaultEnabled),
        label: control.label,
        dataset: { artifactId: id },
        onChange: (checked) => setArticleArtifact(id, checked, defaultEnabled),
      }));
      group.appendChild(row);
    }
    moduleWrap.appendChild(group);
  }

  function renderBlockToggle(state, block) {
    if (block.type === 'spacer') return null;
    const item = doc.createElement('div');
    item.className = 'bp-block-item';
    item.dataset.blockId = block.id;

    const row = doc.createElement('div');
    row.className = 'bp-block-check';
    const rowLabel = doc.createElement('span');
    rowLabel.className = 'bp-block-label';
    const name = doc.createElement('span');
    name.className = 'bp-block-name';
    const type = getBlockType(block.type) || { label: block.type };
    name.textContent = blockDisplayLabel(block, type, getAssetLibrary ? getAssetLibrary() : null, accountId);
    rowLabel.appendChild(name);
    row.appendChild(rowLabel);

    const baseBlock = blockByID(state.base, block.id);
    const actions = doc.createElement('span');
    actions.className = 'bp-block-actions';
    if (baseBlock && block.enabled !== baseBlock.enabled) {
      const note = doc.createElement('span');
      note.className = 'bp-block-default';
      note.textContent = '本篇例外';
      actions.appendChild(note);
    }
    actions.appendChild(createSwitchControl(doc, {
      checked: !!block.enabled,
      disabled: !state.resolved.enabled,
      label: name.textContent,
      dataset: { blockSwitch: block.id },
      onChange: (checked) => setBlockEnabled(state, block.id, checked),
    }));
    row.appendChild(actions);
    item.appendChild(row);

    if (block.type === 'more_articles') {
      const nested = doc.createElement('div');
      nested.className = 'bp-block-nested';
      const extra = block.enabled && state.resolved.enabled && getBlockExtra ? getBlockExtra(block) : null;
      if (extra) nested.appendChild(extra);
      item.appendChild(nested);
    }
    return item;
  }

  function renderFooterControls(wrap, mod) {
    const state = footerState(mod);
    const profile = state.resolved.articleFooterProfile;
    const hasFooter = moduleHasRenderableFooter(mod);
    const hasArtifacts = artifactControlIDs(profile).length > 0;
    if (!hasFooter && !hasArtifacts) return;
    const moduleWrap = doc.createElement('div');
    moduleWrap.className = 'bp-module';
    if (profile) {
      const head = doc.createElement('div');
      head.className = 'bp-module-head';
      const title = doc.createElement('div');
      title.className = 'bp-module-title';
      title.appendChild(createIcon('layers', doc, { className: 'bp-icon' }));
      const titleText = doc.createElement('span');
      titleText.textContent = '文章专属设置';
      title.appendChild(titleText);
      head.appendChild(title);

      const badge = doc.createElement('span');
      badge.className = 'bp-profile-badge';
      badge.textContent = '已套用：' + (profile.name || '默认方案');
      head.appendChild(badge);
      moduleWrap.appendChild(head);
      renderArtifactControls(moduleWrap, profile);
    }

    if (hasFooter) {
      const details = doc.createElement('details');
      details.className = 'bp-footer-details';
      details.open = true;
      const summary = doc.createElement('summary');
      const summaryLeft = doc.createElement('span');
      summaryLeft.className = 'bp-summary-left';
      summaryLeft.appendChild(createIcon('chevronRight', doc, { className: 'bp-icon bp-chevron' }));
      summaryLeft.appendChild(createIcon('panelBottom', doc, { className: 'bp-icon' }));
      const summaryTitle = doc.createElement('span');
      summaryTitle.textContent = '底部区域';
      summaryLeft.appendChild(summaryTitle);
      summary.appendChild(summaryLeft);

      const parentSwitch = doc.createElement('span');
      parentSwitch.className = 'bp-parent-switch';
      parentSwitch.onclick = (e) => e.stopPropagation();
      const switchText = doc.createElement('span');
      switchText.textContent = '本文显示';
      parentSwitch.appendChild(switchText);
      parentSwitch.appendChild(createSwitchControl(doc, {
        checked: !!state.resolved.enabled,
        label: '本文显示底部区域',
        dataset: { footerSwitch: 'enabled' },
        onChange: (checked) => setFooterEnabled(state, checked),
      }));
      summary.appendChild(parentSwitch);
      details.appendChild(summary);

      if (state.resolved.enabled) {
        const list = doc.createElement('div');
        list.className = 'bp-block-list';
        for (const block of state.resolved.blocks || []) {
          const item = renderBlockToggle(state, block);
          if (item) list.appendChild(item);
        }
        details.appendChild(list);
      } else {
        const note = doc.createElement('div');
        note.className = 'bp-footer-off-note';
        note.textContent = '本文不输出底部区域，预览和复制都不会包含这些模块。';
        details.appendChild(note);
      }
      moduleWrap.appendChild(details);
    }
    wrap.appendChild(moduleWrap);
    editableCount++;
  }

  function render(options = {}) {
    migrateStaleOverride();
    const focusSelector = options.focusSelector || focusSelectorFromActiveElement(doc, mount);
    const restoreScroll = captureScrollState(doc, mount);
    const mod = getModule() || {};
    const state = footerState(mod);
    const resolved = state.resolved && Array.isArray(state.resolved.blocks)
      ? state.resolved
      : resolveBlocks(mod, override);
    mount.innerHTML = '';
    const wrap = doc.createElement('div');
    wrap.className = 'bp-wrap';
    editableCount = 0;
    if (mod && typeof mod === 'object') renderFooterControls(wrap, mod);

    if (resolved.enabled !== false) {
      for (const block of resolved.blocks) {
        const type = getBlockType(block.type) || { label: block.type, fields: [] };
        const kind = blockEditableKind(block, type);
        if (!kind) continue; // 只读结构块：编辑区不出现（仅正文预览渲染）

        const sec = doc.createElement('div'); sec.className = 'bp-sec'; sec.dataset.id = block.id;
        const title = doc.createElement('div'); title.className = 'bp-sec-title';
        title.textContent = blockDisplayLabel(block, type, getAssetLibrary ? getAssetLibrary() : null, accountId);
        sec.appendChild(title);

        if (kind !== 'more_articles') {
          // frontend_editable 图片块：仅暴露图片字段换图，结构/其余字段归后台只读。
          for (const f of (type.fields || [])) if (f.type === 'image') sec.appendChild(imageSwap(block, f));
        } else {
          continue;
        }
        wrap.appendChild(sec);
        editableCount++;
      }
    }
    mount.appendChild(wrap);
    focusSwitch(doc, mount, focusSelector);
    restoreScroll();
  }

  render();
  return {
    getOverride: () => override,
    setOverride: (next) => { override = isObj(next) ? next : {}; saveOverride(accountId, override); render(); },
    refresh: render,
    hasEditableContent: () => editableCount > 0,
    destroy: () => { mount.innerHTML = ''; },
  };
}
