// block-panel.js — 前台「底部区块」单篇内容编辑区（公众号 + 网站共用）。
//
// 设计（2026-06-04 用户裁定）：底部区块的结构/显隐/排序/字号/调色对绝大多数排版人员是「既定不变」的，
// 应在规则后台预先配好。前端不再做结构编辑，退化为【固定展开、无折叠】的内容位编辑区，只暴露排版员
// 真正要改的内容：
//   (a) more_articles 块：永远显示其内容编辑器（换链接/换封面/裁剪/增删/排序，红线#14，不挂任何开关）；
//   (b) 被后台标记 frontend_editable=true 的块：只暴露其图片字段的「更换图片」（走素材库，红线#10）。
// 其余块（提示/署名/栏目图…）在编辑区不出现，仅在右侧正文预览里按后台默认渲染。
// 「哪块可前端编辑」由块实例级 frontend_editable 驱动（服务端默认 rule_defaults.go + 校验 rule_bottom_blocks.go），
// 前端零硬编码（block-merge.js:resolveOneBlock 透传 frontendEditable 布尔）。
//
// 中性：只 import 中性合并引擎/注册表/素材选择器，不 import 任何渠道渲染器（红线 #19）。
// 宿主（app.html）通过 onChange(override) 把 override 注入渲染并重渲；据 hasEditableContent() 决定整段显隐。

import { resolveBlocks, hasAnyOverride, stripReadonlyOverride } from './block-merge.js';
import { getBlockType } from './block-registry.gen.js';
import { openAssetPicker, resolveAssetThumb } from './asset-picker.js';

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

// mountBlockPanel 挂载内容编辑区。
//   mount, accountId, getModule, getAssetLibrary
//   getBlockExtra(block) => HTMLElement|null：宿主为 more_articles 返回稳定持有的卡片编辑器元素
//   onChange(override) => void：宿主据此注入渲染并重渲
export function mountBlockPanel({ mount, accountId, getModule, getAssetLibrary, getBlockExtra, onChange, doc = document }) {
  if (!mount) return null;
  ensureStyle(doc);
  let override = loadOverride(accountId);
  let editableCount = 0;

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

  function render() {
    migrateStaleOverride();
    const mod = getModule() || {};
    const resolved = resolveBlocks(mod, override);
    mount.innerHTML = '';
    const wrap = doc.createElement('div');
    wrap.className = 'bp-wrap';
    editableCount = 0;

    for (const block of resolved.blocks) {
      const type = getBlockType(block.type) || { label: block.type, fields: [] };
      const kind = blockEditableKind(block, type);
      if (!kind) continue; // 只读结构块：编辑区不出现（仅正文预览渲染）

      const sec = doc.createElement('div'); sec.className = 'bp-sec'; sec.dataset.id = block.id;
      const title = doc.createElement('div'); title.className = 'bp-sec-title';
      title.textContent = blockDisplayLabel(block, type, getAssetLibrary ? getAssetLibrary() : null, accountId);
      sec.appendChild(title);

      if (kind === 'more_articles') {
        // 更多文章内容编辑器恒显（红线#14，与 frontend_editable 解耦）；宿主返回稳定持有的元素。
        const extra = getBlockExtra ? getBlockExtra(block) : null;
        if (extra) sec.appendChild(extra);
      } else {
        // frontend_editable 图片块：仅暴露图片字段换图，结构/其余字段归后台只读。
        for (const f of (type.fields || [])) if (f.type === 'image') sec.appendChild(imageSwap(block, f));
      }
      wrap.appendChild(sec);
      editableCount++;
    }

    if (editableCount) {
      const hint = doc.createElement('div'); hint.className = 'bp-hint';
      hint.textContent = '底部版式由后台预设；此处只改本篇可替换的内容（更多文章链接 / 封面图），改动只影响当前文章。';
      wrap.appendChild(hint);
    }
    mount.appendChild(wrap);
  }

  render();
  return {
    getOverride: () => override,
    refresh: render,
    hasEditableContent: () => editableCount > 0,
    destroy: () => { mount.innerHTML = ''; },
  };
}
