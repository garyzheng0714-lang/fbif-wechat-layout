// asset-library-rule-editor.js — 规则后台素材库编辑器。
// 元数据随 /api/rules 保存；图片上传复用 uploader.js 的 /api/oss-upload 入口。

import { ASSET_LIBRARY_DEFAULT } from './asset-library-defaults.js';
import { uploadAssetFile } from './uploader.js';
import { listRulePacks } from './rule-packs/registry.js';

const USAGES = ['qr_group', 'qr_account', 'logo', 'banner', 'avatar', 'decor', 'custom'];
const USAGE_LABELS = { qr_group: '入群二维码', qr_account: '公众号二维码', logo: '品牌标志', banner: '横幅', avatar: '头像', decor: '装饰图', custom: '其他素材' };
const clone = value => JSON.parse(JSON.stringify(value));
const esc = value => String(value == null ? '' : value).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
const ASSET_REF_RE = /\{\{asset(?::([a-z0-9_-]+)|#([a-z0-9_-]+))\}\}/ig;

function scopeOptions() {
  const ids = ['global'];
  for (const pack of listRulePacks()) if (pack && pack.id && !ids.includes(pack.id)) ids.push(pack.id);
  return ids;
}

function scopeLabel(scope) {
  if (scope === 'global') return '共用素材';
  return listRulePacks().find(pack => pack && pack.id === scope)?.accountName || scope;
}

function isSafeURL(value) {
  const url = String(value || '').trim();
  if (!url || /[\x00-\x20\x7F"'<>\\]/.test(url)) return false;
  if (url.startsWith('/')) return !url.startsWith('//');
  try {
    const parsed = new URL(url);
    return parsed.protocol === 'https:';
  } catch {
    return false;
  }
}

function cleanID(value) {
  return String(value || '').trim().toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '_')
    .replace(/^_+|_+$/g, '')
    .slice(0, 64) || ('asset_' + Date.now().toString(36));
}

function normalizeLibrary(input) {
  const raw = input && typeof input === 'object' ? input : ASSET_LIBRARY_DEFAULT;
  const assets = Array.isArray(raw.assets) ? raw.assets : [];
  const scopes = scopeOptions();
  return {
    schema_version: 1,
    assets: assets.map((asset, index) => ({
      id: cleanID(asset.id || ('asset_' + index)),
      type: 'image',
      usage: USAGES.includes(asset.usage) ? asset.usage : 'custom',
      scope: scopes.includes(asset.scope) ? asset.scope : 'global',
      label: String(asset.label || asset.id || '未命名素材').slice(0, 80),
      url: String(asset.url || '').trim(),
      tags: Array.isArray(asset.tags) ? asset.tags.map(String).slice(0, 20) : [],
      width: Number(asset.width) > 0 ? Math.round(Number(asset.width)) : undefined,
      height: Number(asset.height) > 0 ? Math.round(Number(asset.height)) : undefined,
      updated_at: asset.updated_at || undefined,
    })),
  };
}

function validateLibrary(library) {
  const ids = new Set();
  for (const asset of library.assets || []) {
    if (ids.has(asset.id)) return '素材 ID 重复：' + asset.id;
    ids.add(asset.id);
    if (!isSafeURL(asset.url)) return '素材链接必须是站内路径或 HTTPS：' + asset.id;
  }
  return '';
}

function genID(prefix = 'asset') {
  return cleanID(prefix + '_' + Date.now().toString(36) + '_' + Math.random().toString(36).slice(2, 6));
}

function tagsText(asset) {
  return Array.isArray(asset.tags) ? asset.tags.join('，') : '';
}

function parseTags(value) {
  return String(value || '').split(/[，,]/).map(x => x.trim()).filter(Boolean).slice(0, 20);
}

function collectAssetRefs(value, path = [], out = []) {
  if (typeof value === 'string') {
    ASSET_REF_RE.lastIndex = 0;
    let match;
    while ((match = ASSET_REF_RE.exec(value))) {
      out.push({ id: match[2] || '', usage: match[1] || '', path: path.join('.') });
    }
    return out;
  }
  if (Array.isArray(value)) {
    value.forEach((item, i) => collectAssetRefs(item, path.concat(String(i)), out));
    return out;
  }
  if (value && typeof value === 'object') {
    Object.keys(value).forEach(key => collectAssetRefs(value[key], path.concat(key), out));
  }
  return out;
}

export function buildAssetReferenceIndex(presets = [], assets = []) {
  const byUsage = new Map();
  for (const asset of assets || []) {
    if (!asset || !asset.id) continue;
    const usage = String(asset.usage || '').trim();
    if (!usage) continue;
    if (!byUsage.has(usage)) byUsage.set(usage, []);
    byUsage.get(usage).push(asset.id);
  }
  const index = {};
  for (const preset of presets || []) {
    const config = (preset && (preset.config || preset.raw_config)) || {};
    const refs = collectAssetRefs(config.modules || {});
    for (const ref of refs) {
      const ids = ref.id ? [ref.id] : (byUsage.get(ref.usage) || []);
      for (const id of ids) {
        if (!index[id]) index[id] = [];
        index[id].push({
          presetId: preset.id || '',
          presetName: preset.name || preset.id || '未命名规则',
          path: ref.path,
        });
      }
    }
  }
  return index;
}

export function createAssetLibraryEditor({ container, assetLibrary, onChange, referencePresets = [], currentScope = 'global', compact = false }) {
  if (!container) throw new Error('createAssetLibraryEditor: container is required');
  const snapshot = normalizeLibrary(assetLibrary);
  let state = normalizeLibrary(assetLibrary);
  let status = '';
  let filterScope = currentScope !== 'global' && scopeOptions().includes(currentScope) ? 'context' : 'all';
  let filterUsage = 'all';
  let filterText = '';
  const expanded = new Set(); // 默认折叠：素材列表为可扫的紧凑行，点开某素材才展开 8 字段编辑表单

  function referencesFor(asset) {
    const refs = buildAssetReferenceIndex(referencePresets, state.assets);
    return refs[asset.id] || [];
  }

  function visibleAssets() {
    const text = filterText.trim().toLowerCase();
    return state.assets
      .map((asset, index) => ({ asset, index }))
      .filter(({ asset }) => filterScope === 'all' || (filterScope === 'context' ? asset.scope === 'global' || asset.scope === currentScope : asset.scope === filterScope))
      .filter(({ asset }) => filterUsage === 'all' || asset.usage === filterUsage)
      .filter(({ asset }) => {
        if (!text) return true;
        return [asset.id, asset.label, asset.usage, asset.scope, ...(asset.tags || [])]
          .some(v => String(v || '').toLowerCase().includes(text));
      });
  }

  function tagOptions() {
    const counts = new Map();
    for (const asset of state.assets || []) {
      for (const tag of asset.tags || []) {
        const key = String(tag || '').trim();
        if (key) counts.set(key, (counts.get(key) || 0) + 1);
      }
    }
    return [...counts.entries()]
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0], 'zh-Hans-CN'))
      .slice(0, 12)
      .map(([tag]) => tag);
  }

  function emit(next) {
    state = normalizeLibrary(next);
    status = validateLibrary(state);
    if (!status && typeof onChange === 'function') onChange(clone(state));
    render();
  }

  function renderAsset(asset, index) {
    const ok = isSafeURL(asset.url);
    const refs = referencesFor(asset);
    const refTitle = refs.map(ref => `${ref.presetName} / ${ref.path}`).join('\n');
    const open = expanded.has(asset.id);
    // 折叠行（始终可见）：小缩略图 + 名称 + 用途·范围·ID + 引用数 + 展开箭头；点行切换展开。
    const row = `<div class="asset-lib-row" data-action="toggle" data-asset-id="${esc(asset.id)}">
      <div class="asset-lib-thumb">${ok ? `<img src="${esc(asset.url)}" alt="">` : '<span>无图</span>'}</div>
      <div class="asset-lib-rowmeta">
        <div class="asset-lib-rowname">${esc(asset.label)}</div>
        <div class="asset-lib-rowsub">${esc(USAGE_LABELS[asset.usage] || asset.usage)} · ${esc(scopeLabel(asset.scope))}</div>
      </div>
      <span class="asset-lib-ref ${refs.length ? 'used' : ''}" title="${esc(refTitle)}">被 ${refs.length} 处引用</span>
      <span class="asset-lib-caret ${open ? 'open' : ''}" aria-hidden="true">▸</span>
    </div>`;
    // 展开详情：8 字段编辑表单 + 上传替换/删除（仅展开时渲染，去掉默认的表单墙）。
    const detail = open ? `<div class="asset-lib-detail">
      <div class="asset-lib-fields">
        <label><span>名称</span><input data-field="label" value="${esc(asset.label)}"></label>
        <label><span>ID</span><input value="${esc(asset.id)}" readonly title="素材 ID 创建后不可改，避免已引用区块失效"></label>
        <label><span>用途</span><select data-field="usage">${USAGES.map(u => `<option value="${u}" ${asset.usage === u ? 'selected' : ''}>${esc(USAGE_LABELS[u] || u)}</option>`).join('')}</select></label>
        <label><span>范围</span><select data-field="scope">${scopeOptions().map(s => `<option value="${s}" ${asset.scope === s ? 'selected' : ''}>${esc(scopeLabel(s))}</option>`).join('')}</select></label>
        <label class="asset-lib-wide"><span>链接</span><input data-field="url" value="${esc(asset.url)}" placeholder="https:// 或 /assets/..."></label>
        <label><span>宽</span><input data-field="width" type="number" min="0" max="5000" value="${esc(asset.width || '')}"></label>
        <label><span>高</span><input data-field="height" type="number" min="0" max="5000" value="${esc(asset.height || '')}"></label>
        <label class="asset-lib-wide"><span>标签</span><input data-field="tags" value="${esc(tagsText(asset))}" placeholder="用逗号分隔"></label>
      </div>
      <div class="asset-lib-actions">
        <button type="button" class="admin-secondary" data-action="upload-replace" data-asset-idx="${index}">上传替换</button>
        <button type="button" class="admin-secondary danger" data-action="remove" data-asset-idx="${index}" ${refs.length ? 'disabled' : ''}>删除</button>
      </div>
    </div>` : '';
    return `<div class="asset-lib-card${open ? ' open' : ''}" data-asset-idx="${index}">${row}${detail}</div>`;
  }

  function renderAssetList(visible) {
    if (!visible.length) return '<div class="asset-lib-empty">没有符合筛选条件的素材。</div>';
    const grouped = new Map();
    for (const item of visible) {
      const usage = item.asset.usage || 'custom';
      if (!grouped.has(usage)) grouped.set(usage, []);
      grouped.get(usage).push(item);
    }
    return USAGES.filter(usage => grouped.has(usage)).map(usage => `
      <section class="asset-lib-group">
        <div class="asset-lib-group-title">${esc(USAGE_LABELS[usage] || usage)}</div>
        ${grouped.get(usage).map(({ asset, index }) => renderAsset(asset, index)).join('')}
      </section>`).join('');
  }

  function render() {
    const dirty = JSON.stringify(state) !== JSON.stringify(snapshot);
    const visible = visibleAssets();
    const tags = tagOptions();
    container.innerHTML = `<div class="asset-lib-editor ${compact ? 'compact' : ''}">
      <div class="asset-lib-toolbar">
        <span class="asset-lib-status ${dirty ? 'dirty' : 'synced'}">${dirty ? '有未保存改动' : `${visible.length} / ${state.assets.length} 项素材`}</span>
        <input class="asset-lib-search" data-filter="text" value="${esc(filterText)}" placeholder="搜索名称、ID、标签" aria-label="搜索素材">
        <select data-filter="scope" aria-label="按素材范围筛选"><option value="context" ${filterScope === 'context' ? 'selected' : ''}>当前账号 + 共用</option><option value="all" ${filterScope === 'all' ? 'selected' : ''}>全部范围</option>${scopeOptions().map(s => `<option value="${s}" ${filterScope === s ? 'selected' : ''}>${esc(scopeLabel(s))}</option>`).join('')}</select>
        <select data-filter="usage" aria-label="按素材用途筛选"><option value="all">全部用途</option>${USAGES.map(u => `<option value="${u}" ${filterUsage === u ? 'selected' : ''}>${esc(USAGE_LABELS[u] || u)}</option>`).join('')}</select>
        <button type="button" class="admin-secondary" data-action="add-url">添加链接</button>
        <button type="button" class="admin-secondary" data-action="upload-new">上传并入库</button>
        <button type="button" class="admin-secondary" data-action="undo" ${dirty ? '' : 'disabled'}>撤销所有修改</button>
        <input type="file" accept="image/*" hidden data-role="asset-file">
      </div>
      ${tags.length ? `<div class="asset-lib-tags"><span>标签</span>${tags.map(tag => `<button type="button" data-action="filter-tag" data-tag="${esc(tag)}" class="${filterText.trim() === tag ? 'active' : ''}">${esc(tag)}</button>`).join('')}</div>` : ''}
      ${status ? `<div class="asset-lib-error">${esc(status)}</div>` : ''}
      <div class="asset-lib-list">${renderAssetList(visible)}</div>
    </div>`;
    injectStyles();
  }

  function updateAsset(index, field, value) {
    const next = clone(state);
    const asset = next.assets[index];
    if (!asset) return;
    if (field === 'tags') asset.tags = parseTags(value);
    else if (field === 'width' || field === 'height') asset[field] = Number(value) > 0 ? Math.round(Number(value)) : undefined;
    else asset[field] = value;
    asset.updated_at = new Date().toISOString();
    emit(next);
  }

  async function uploadInto(index = -1) {
    const input = container.querySelector('[data-role="asset-file"]');
    if (!input) return;
    input.onchange = async () => {
      const file = input.files && input.files[0];
      input.value = '';
      if (!file) return;
      status = '上传中...'; render();
      try {
        const url = await uploadAssetFile(file);
        const next = clone(state);
        if (index >= 0 && next.assets[index]) {
          next.assets[index].url = url;
          next.assets[index].updated_at = new Date().toISOString();
        } else {
          next.assets.unshift({ id: genID('asset'), type: 'image', usage: 'custom', scope: scopeOptions().includes(currentScope) ? currentScope : 'global', label: file.name.replace(/\.[^.]+$/, ''), url, tags: ['上传'], updated_at: new Date().toISOString() });
        }
        emit(next);
      } catch (err) {
        status = err.message || '上传失败';
        render();
      }
    };
    input.click();
  }

  function handleClick(e) {
    const btn = e.target.closest('[data-action]');
    if (!btn) return;
    const action = btn.dataset.action;
    if (action === 'toggle') {
      const id = btn.dataset.assetId;
      if (expanded.has(id)) expanded.delete(id); else expanded.add(id);
      render();
      return;
    }
    if (action === 'filter-tag') { filterText = btn.dataset.tag || ''; render(); return; }
    if (action === 'undo') { emit(snapshot); return; }
    if (action === 'add-url') {
      const next = clone(state);
      next.assets.unshift({ id: genID('asset'), type: 'image', usage: 'custom', scope: scopeOptions().includes(currentScope) ? currentScope : 'global', label: '新素材', url: 'https://static.foodtalks.cn/', tags: [], updated_at: new Date().toISOString() });
      emit(next); return;
    }
    if (action === 'upload-new') { uploadInto(-1); return; }
    const index = Number(btn.dataset.assetIdx);
    if (action === 'upload-replace') { uploadInto(index); return; }
    if (action === 'remove') {
      const refs = referencesFor(state.assets[index]);
      if (refs.length) {
        window.alert('该素材仍被 ' + refs.length + ' 处引用，不能删除。请先替换引用它的底部区块或页脚片段。');
        return;
      }
      const next = clone(state);
      next.assets.splice(index, 1);
      emit(next);
    }
  }

  function handleChange(e) {
    const filter = e.target && e.target.dataset && e.target.dataset.filter;
    if (filter) {
      if (filter === 'scope') filterScope = e.target.value || 'all';
      if (filter === 'usage') filterUsage = e.target.value || 'all';
      if (filter === 'text') filterText = e.target.value || '';
      render();
      return;
    }
    const field = e.target && e.target.dataset && e.target.dataset.field;
    if (!field) return;
    const card = e.target.closest('[data-asset-idx]');
    if (!card) return;
    updateAsset(Number(card.dataset.assetIdx), field, e.target.value);
  }

  let stylesInjected = false;
  function injectStyles() {
    if (stylesInjected || document.getElementById('asset-lib-styles')) return;
    stylesInjected = true;
    const style = document.createElement('style');
    style.id = 'asset-lib-styles';
    style.textContent = `.asset-lib-editor{display:grid;gap:12px}.asset-lib-toolbar{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.asset-lib-toolbar select,.asset-lib-search{height:32px;border:1px solid var(--fs-border-control,#D9DCE0);border-radius:var(--fs-r-xs,4px);padding:0 8px;background:var(--fs-bg-card,#fff);font-family:inherit;font-size:12px}.asset-lib-search{min-width:190px}.asset-lib-status{font-size:12px;font-weight:700}.asset-lib-status.synced{color:var(--fs-success,#00A870)}.asset-lib-status.dirty{color:var(--fs-warning,#FF8800)}.asset-lib-tags{display:flex;align-items:center;gap:6px;flex-wrap:wrap}.asset-lib-tags span{font-size:11px;color:var(--fs-text-3,#8F959E);font-weight:700}.asset-lib-tags button{border:0;border-radius:var(--fs-r-full,9999px);padding:3px 8px;background:var(--fs-bg-sunken,#F2F3F5);color:var(--fs-text-2,#646A73);font-size:11px;font-family:inherit;cursor:pointer}.asset-lib-tags button:hover,.asset-lib-tags button.active{background:var(--fs-brand-soft,#EBF1FF);color:var(--fs-brand,#1456F0)}.asset-lib-error{padding:8px 10px;border-radius:var(--fs-r-s,6px);background:rgba(255,136,0,.08);color:var(--fs-warning,#FF8800);font-size:12px}.asset-lib-list{display:grid;gap:10px}.asset-lib-group{display:grid;gap:6px}.asset-lib-group-title{font-size:11px;color:var(--fs-text-3,#8F959E);font-weight:700;text-transform:uppercase}.asset-lib-empty{padding:18px;text-align:center;color:var(--fs-text-3,#8F959E);background:var(--fs-bg-nested,#F7F8FA);border-radius:var(--fs-r-m,8px);font-size:13px}.asset-lib-card{border-radius:var(--fs-r-m,8px);background:var(--fs-bg-nested,#F7F8FA);overflow:hidden}.asset-lib-card.open{box-shadow:inset 0 0 0 1px var(--fs-border-control,#D9DCE0)}.asset-lib-row{display:flex;align-items:center;gap:10px;padding:8px 10px;cursor:pointer;transition:background .12s}.asset-lib-row:hover{background:var(--fs-bg-hover,rgba(31,35,39,.045))}.asset-lib-rowmeta{flex:1;min-width:0}.asset-lib-rowname{font-size:13px;font-weight:600;color:var(--fs-text-1,#1F2329);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.asset-lib-rowsub{font-size:11px;color:var(--fs-text-3,#8F959E);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;margin-top:1px}.asset-lib-caret{flex:none;color:var(--fs-text-3,#8F959E);font-size:12px;transition:transform .15s}.asset-lib-caret.open{transform:rotate(90deg)}.asset-lib-detail{padding:6px 12px 14px;border-top:1px solid var(--fs-divider,rgba(31,35,39,.08));display:grid;gap:10px}.asset-lib-thumb{width:44px;height:44px;flex:none;border-radius:var(--fs-r-s,6px);background:var(--fs-bg-card,#fff);display:flex;align-items:center;justify-content:center;overflow:hidden;color:var(--fs-text-3,#8F959E);font-size:11px}.asset-lib-thumb img{max-width:100%;max-height:100%;object-fit:contain}.asset-lib-fields{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:8px}.asset-lib-fields label{display:grid;gap:4px}.asset-lib-fields span{font-size:12px;color:var(--fs-text-2,#646A73)}.asset-lib-fields input,.asset-lib-fields select{min-width:0;height:32px;border:1px solid var(--fs-border-control,#D9DCE0);border-radius:var(--fs-r-xs,4px);padding:0 8px;background:var(--fs-bg-card,#fff);font-family:inherit;font-size:12px}.asset-lib-fields input[readonly]{background:var(--fs-bg-sunken,#F2F3F5);color:var(--fs-text-3,#8F959E)}.asset-lib-fields input:focus,.asset-lib-fields select:focus,.asset-lib-toolbar select:focus,.asset-lib-search:focus{outline:none;border-color:var(--fs-brand,#1456F0);box-shadow:0 0 0 2px var(--fs-brand-soft,#EBF1FF)}.asset-lib-wide{grid-column:span 2}.asset-lib-actions{display:flex;gap:8px}.asset-lib-ref{flex:none;display:inline-flex;align-items:center;justify-content:center;min-height:22px;padding:0 8px;border-radius:var(--fs-r-full,9999px);background:var(--fs-bg-sunken,#F2F3F5);color:var(--fs-text-3,#8F959E);font-size:11px;font-weight:600}.asset-lib-ref.used{background:var(--fs-brand-soft,#EBF1FF);color:var(--fs-brand,#1456F0)}.asset-lib-editor.compact .asset-lib-toolbar{display:grid;grid-template-columns:1fr;align-items:stretch}.asset-lib-editor.compact .asset-lib-search{min-width:0;width:100%;box-sizing:border-box}.asset-lib-editor.compact .asset-lib-toolbar select,.asset-lib-editor.compact .asset-lib-toolbar button{width:100%;box-sizing:border-box}.asset-lib-editor.compact .asset-lib-row{padding:7px 8px}.asset-lib-editor.compact .asset-lib-thumb{width:38px;height:38px}.asset-lib-editor.compact .asset-lib-ref{display:none}.asset-lib-editor.compact .asset-lib-fields{grid-template-columns:1fr}.asset-lib-editor.compact .asset-lib-wide{grid-column:auto}@media(max-width:900px){.asset-lib-fields{grid-template-columns:1fr}.asset-lib-wide{grid-column:auto}.asset-lib-detail{padding:6px 10px 12px}}`;
    style.textContent += `.asset-lib-toolbar{padding-block:2px}.asset-lib-status.synced{color:var(--fs-text-2,#646A73)}.asset-lib-list{gap:18px}.asset-lib-group{gap:0;border-bottom:1px solid var(--fs-divider,rgba(31,35,39,.08))}.asset-lib-group-title{padding:6px 2px 8px;text-transform:none}.asset-lib-card{border-radius:0;background:var(--fs-bg-card,#fff);border-top:1px solid var(--fs-divider,rgba(31,35,39,.08))}.asset-lib-card.open{box-shadow:none}.asset-lib-row{padding:9px 2px}.asset-lib-detail{background:var(--fs-bg-nested,#F7F8FA)}@media(max-width:700px){.asset-lib-toolbar{display:grid;grid-template-columns:1fr 1fr}.asset-lib-toolbar .asset-lib-status,.asset-lib-toolbar .asset-lib-search{grid-column:1/-1}.asset-lib-search{width:100%;box-sizing:border-box}.asset-lib-toolbar button{width:100%}.asset-lib-ref{display:none}}`;
    document.head.appendChild(style);
  }

  container.addEventListener('click', handleClick);
  container.addEventListener('change', handleChange);
  render();
  return {
    destroy() {
      container.removeEventListener('click', handleClick);
      container.removeEventListener('change', handleChange);
      container.innerHTML = '';
    },
  };
}
