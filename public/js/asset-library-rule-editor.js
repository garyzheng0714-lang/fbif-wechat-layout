// asset-library-rule-editor.js — 规则后台素材库编辑器。
// 元数据随 /api/rules 保存；图片上传复用 uploader.js 的 /api/oss-upload 入口。

import { ASSET_LIBRARY_DEFAULT } from './asset-library-defaults.js';
import { uploadAssetFile } from './uploader.js';

const USAGES = ['qr_group', 'qr_account', 'logo', 'banner', 'avatar', 'decor', 'custom'];
const SCOPES = ['global', 'fbif', 'wowfood', 'foodtalks-series', 'ft-default'];
const clone = value => JSON.parse(JSON.stringify(value));
const esc = value => String(value == null ? '' : value).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

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
  return {
    schema_version: 1,
    assets: assets.map((asset, index) => ({
      id: cleanID(asset.id || ('asset_' + index)),
      type: 'image',
      usage: USAGES.includes(asset.usage) ? asset.usage : 'custom',
      scope: SCOPES.includes(asset.scope) ? asset.scope : 'global',
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

export function createAssetLibraryEditor({ container, assetLibrary, onChange }) {
  if (!container) throw new Error('createAssetLibraryEditor: container is required');
  const snapshot = normalizeLibrary(assetLibrary);
  let state = normalizeLibrary(assetLibrary);
  let status = '';

  function emit(next) {
    state = normalizeLibrary(next);
    status = validateLibrary(state);
    if (!status && typeof onChange === 'function') onChange(clone(state));
    render();
  }

  function renderAsset(asset, index) {
    const ok = isSafeURL(asset.url);
    return `<div class="asset-lib-card" data-asset-idx="${index}">
      <div class="asset-lib-thumb">${ok ? `<img src="${esc(asset.url)}" alt="">` : '<span>无图</span>'}</div>
      <div class="asset-lib-fields">
        <label><span>名称</span><input data-field="label" value="${esc(asset.label)}"></label>
        <label><span>ID</span><input data-field="id" value="${esc(asset.id)}"></label>
        <label><span>用途</span><select data-field="usage">${USAGES.map(u => `<option value="${u}" ${asset.usage === u ? 'selected' : ''}>${u}</option>`).join('')}</select></label>
        <label><span>范围</span><select data-field="scope">${SCOPES.map(s => `<option value="${s}" ${asset.scope === s ? 'selected' : ''}>${s}</option>`).join('')}</select></label>
        <label class="asset-lib-wide"><span>链接</span><input data-field="url" value="${esc(asset.url)}" placeholder="https:// 或 /assets/..."></label>
        <label><span>宽</span><input data-field="width" type="number" min="0" max="5000" value="${esc(asset.width || '')}"></label>
        <label><span>高</span><input data-field="height" type="number" min="0" max="5000" value="${esc(asset.height || '')}"></label>
        <label class="asset-lib-wide"><span>标签</span><input data-field="tags" value="${esc(tagsText(asset))}" placeholder="用逗号分隔"></label>
      </div>
      <div class="asset-lib-actions">
        <button type="button" class="admin-secondary" data-action="upload-replace" data-asset-idx="${index}">上传替换</button>
        <button type="button" class="admin-secondary danger" data-action="remove" data-asset-idx="${index}">删除</button>
      </div>
    </div>`;
  }

  function render() {
    const dirty = JSON.stringify(state) !== JSON.stringify(snapshot);
    container.innerHTML = `<div class="asset-lib-editor">
      <div class="asset-lib-toolbar">
        <span class="asset-lib-status ${dirty ? 'dirty' : 'synced'}">${dirty ? '有未保存改动' : '已同步 ✓'}</span>
        <button type="button" class="admin-secondary" data-action="add-url">新增链接素材</button>
        <button type="button" class="admin-secondary" data-action="upload-new">上传并入库</button>
        <button type="button" class="admin-secondary" data-action="undo" ${dirty ? '' : 'disabled'}>撤销所有修改</button>
        <input type="file" accept="image/*" hidden data-role="asset-file">
      </div>
      ${status ? `<div class="asset-lib-error">${esc(status)}</div>` : ''}
      <div class="asset-lib-list">${state.assets.map(renderAsset).join('')}</div>
    </div>`;
    injectStyles();
  }

  function updateAsset(index, field, value) {
    const next = clone(state);
    const asset = next.assets[index];
    if (!asset) return;
    if (field === 'id') asset.id = cleanID(value);
    else if (field === 'tags') asset.tags = parseTags(value);
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
          next.assets.unshift({ id: genID('asset'), type: 'image', usage: 'custom', scope: 'global', label: file.name.replace(/\.[^.]+$/, ''), url, tags: ['上传'], updated_at: new Date().toISOString() });
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
    if (action === 'undo') { emit(snapshot); return; }
    if (action === 'add-url') {
      const next = clone(state);
      next.assets.unshift({ id: genID('asset'), type: 'image', usage: 'custom', scope: 'global', label: '新素材', url: 'https://static.foodtalks.cn/', tags: [], updated_at: new Date().toISOString() });
      emit(next); return;
    }
    if (action === 'upload-new') { uploadInto(-1); return; }
    const index = Number(btn.dataset.assetIdx);
    if (action === 'upload-replace') { uploadInto(index); return; }
    if (action === 'remove') {
      const next = clone(state);
      next.assets.splice(index, 1);
      emit(next);
    }
  }

  function handleChange(e) {
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
    style.textContent = `.asset-lib-editor{display:grid;gap:12px}.asset-lib-toolbar{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.asset-lib-status{font-size:12px;font-weight:700}.asset-lib-status.synced{color:var(--fs-success,#00A870)}.asset-lib-status.dirty{color:var(--fs-warning,#FF8800)}.asset-lib-error{padding:8px 10px;border-radius:var(--fs-r-s,6px);background:rgba(255,136,0,.08);color:var(--fs-warning,#FF8800);font-size:12px}.asset-lib-list{display:grid;gap:10px}.asset-lib-card{display:grid;grid-template-columns:92px minmax(0,1fr) 120px;gap:12px;align-items:start;padding:12px;border-radius:var(--fs-r-m,8px);background:var(--fs-bg-nested,#F7F8FA)}.asset-lib-thumb{width:92px;aspect-ratio:1;border-radius:var(--fs-r-s,6px);background:var(--fs-bg-card,#fff);display:flex;align-items:center;justify-content:center;overflow:hidden;color:var(--fs-text-3,#8F959E);font-size:12px}.asset-lib-thumb img{max-width:100%;max-height:100%;object-fit:contain}.asset-lib-fields{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:8px}.asset-lib-fields label{display:grid;gap:4px}.asset-lib-fields span{font-size:12px;color:var(--fs-text-2,#646A73)}.asset-lib-fields input,.asset-lib-fields select{min-width:0;height:32px;border:1px solid var(--fs-border-control,#D9DCE0);border-radius:var(--fs-r-xs,4px);padding:0 8px;background:var(--fs-bg-card,#fff);font-family:inherit;font-size:12px}.asset-lib-fields input:focus,.asset-lib-fields select:focus{outline:none;border-color:var(--fs-brand,#1456F0);box-shadow:0 0 0 2px var(--fs-brand-soft,#EBF1FF)}.asset-lib-wide{grid-column:span 2}.asset-lib-actions{display:grid;gap:8px}@media(max-width:900px){.asset-lib-card{grid-template-columns:1fr}.asset-lib-thumb{width:100%}.asset-lib-fields{grid-template-columns:1fr}.asset-lib-wide{grid-column:auto}.asset-lib-actions{display:flex;flex-wrap:wrap}}`;
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
