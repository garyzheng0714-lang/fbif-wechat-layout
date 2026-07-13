// footer-library-rule-editor.js — 页脚库编辑器（方案Y：表单主导 + JSON 折叠兜底）。
// 导出：createFooterLibraryEditor({ container, footerData, onChange })
import { FOOTER_SEGMENTS_DEFAULT, FOOTER_SEGMENT_ORDER_DEFAULT, FOOTER_PRESETS_DEFAULT } from './footer-segments-defaults.js';
import { buildFooterHtml } from './rule-behavior-foodtalks.js';

const SEGMENT_LABELS = {
  biz_coop: '商务合作',
  bd_card: 'BD 名片',
  coop_table: '相关合作表',
  join_group: '加入微信群',
  follow_account: '关注公众号',
  logo: 'FoodTalks logo',
  more_info: '点击获取更多',
};
const BASE_SEG_KEYS = ['biz_coop', 'join_group', 'follow_account', 'logo', 'more_info'];
const XSS_RE = /<script|on\w+\s*=|javascript:/i;

const clone = o => JSON.parse(JSON.stringify(o));
const esc = v => String(v == null ? '' : v).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');

function extractImgSrc(lines) { const m = lines.join('\n').match(/<img[^>]+src\s*=\s*["']([^"']+)["']/i); return m ? m[1] : null; }
function replaceImgSrc(lines, src) { return lines.map(l => l.replace(/(<img[^>]+src\s*=\s*["'])([^"']+)(["'])/i, `$1${src}$3`)); }
function assetIDFromSrc(src) { const m = String(src || '').match(/^\{\{asset#([a-z0-9_-]+)\}\}$/i); return m ? m[1] : ''; }
function normalizeAssets(assetLibrary) {
  const list = assetLibrary && Array.isArray(assetLibrary.assets) ? assetLibrary.assets : [];
  return list.filter(a => a && a.type === 'image' && (a.scope === 'global' || a.scope === 'ft-default'));
}

function validateImgUrl(url) {
  if (!url) return '请输入图片链接';
  if (!/^https?:\/\//i.test(url)) return '链接须以 http:// 或 https:// 开头';
  if (/[\s"'<>]/.test(url)) return '链接不能含空格或引号/尖括号';
  return '';
}

function validateHtmlLines(lines) {
  return XSS_RE.test(lines.join('\n')) ? '内容含不安全代码（<script> / on*= / javascript:），请删除后再保存' : '';
}

function isSegChanged(key, lines, fallbackDefault) {
  const def = FOOTER_SEGMENTS_DEFAULT[key] || fallbackDefault;
  return def ? JSON.stringify(lines) !== JSON.stringify(def.html_lines) : false;
}

function genId() { return 'preset-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2,6); }

export function createFooterLibraryEditor({ container, footerData, assetLibrary, onChange }) {
  if (!container) throw new Error('createFooterLibraryEditor: container is required');
  const snapshot = clone(footerData || {});
  let state = clone(footerData || {});
  const openCards = {};
  let draggingKey = '';

  function allSegKeys() {
    const keys = new Set([
      ...(Array.isArray(state.segment_order) ? state.segment_order : []),
      ...BASE_SEG_KEYS,
      ...Object.keys(state.segments || {}),
    ]);
    return [...keys].filter(k => k && k !== 'tip');
  }

  function orderedSegKeys() {
    const order = Array.isArray(state.segment_order) && state.segment_order.length
      ? state.segment_order.slice()
      : FOOTER_SEGMENT_ORDER_DEFAULT.slice();
    for (const key of allSegKeys()) {
      if (!order.includes(key)) order.push(key);
    }
    return order.filter(k => k !== 'tip');
  }

  function segmentToggleDefaults() {
    return Object.fromEntries(['tip', ...orderedSegKeys()].map(k => [k, false]));
  }

  function segmentDefault(key) {
    return FOOTER_SEGMENTS_DEFAULT[key] || ((snapshot.segments || {})[key]);
  }

  // 补全 state
  if (!state.segments) state.segments = {};
  if (!Array.isArray(state.segment_order)) state.segment_order = [...FOOTER_SEGMENT_ORDER_DEFAULT];
  if (!Array.isArray(state.presets)) state.presets = clone(FOOTER_PRESETS_DEFAULT);
  if (state.enabled == null) state.enabled = false;
  if (!state.active_preset_id) state.active_preset_id = 'fbif-original';
  for (const k of allSegKeys()) {
    if (!state.segments[k]) state.segments[k] = clone(segmentDefault(k) || { html_lines: [] });
    if (!Array.isArray(state.segments[k].html_lines)) state.segments[k].html_lines = [];
  }

  function emit(next) { state = next; if (typeof onChange === 'function') onChange(clone(state)); render(); }

  function renderStatusBar() {
    const dirty = JSON.stringify(state) !== JSON.stringify(snapshot);
    return `<div class="footer-lib-status-bar">
      <span class="footer-lib-sync-label ${dirty?'dirty':'synced'}" role="status" aria-live="polite">${dirty?'有未保存改动':'已同步 ✓'}</span>
      <button type="button" class="admin-secondary footer-lib-toggle-enabled ${state.enabled?'on':''}" data-action="toggle-enabled" role="switch" aria-checked="${state.enabled?'true':'false'}">${state.enabled?'默认输出页脚':'默认不输出页脚'}</button>
      <button type="button" class="admin-secondary footer-lib-undo-btn" data-action="undo-all" ${dirty?'':'disabled'}>撤销所有修改</button>
    </div>`;
  }

  function renderAssetPicker(key, imgSrc) {
    const assets = normalizeAssets(assetLibrary);
    if (!assets.length) return '';
    const current = assetIDFromSrc(imgSrc);
    return `<div class="footer-lib-img-row">
      <label class="footer-lib-img-label">从素材库选图</label>
      <select class="footer-lib-img-input" data-action="pick-asset" data-seg-key="${esc(key)}" aria-label="${esc(SEGMENT_LABELS[key] || key)}：从素材库选图">
        <option value="">保持当前链接</option>
        ${assets.map(asset => `<option value="${esc(asset.id)}" ${asset.id === current ? 'selected' : ''}>${esc(asset.label || asset.id)} · ${esc(asset.usage || 'custom')}</option>`).join('')}
      </select>
    </div>`;
  }

  function renderCard(key) {
    const lines = (state.segments[key] || {}).html_lines || [];
    const label = SEGMENT_LABELS[key] || key;
    const open = !!openCards[key];
    const imgSrc = extractImgSrc(lines);
    const xssErr = validateHtmlLines(lines);
    return `<div class="footer-lib-card${open?' open':''}" data-seg-key="${esc(key)}" draggable="true">
      <button type="button" class="footer-lib-card-head" data-action="toggle-card" data-seg-key="${esc(key)}" aria-expanded="${open?'true':'false'}">
        <span class="footer-lib-card-label"><span class="footer-lib-drag-handle" title="拖拽排序" aria-hidden="true">⠿</span>${esc(label)}${isSegChanged(key,lines,segmentDefault(key))?'<span class="footer-lib-changed-dot" title="与默认值不同">已改</span>':''}</span>
        <span class="footer-lib-card-arrow" aria-hidden="true">${open?'▲':'▼'}</span>
      </button>
      ${open?`<div class="footer-lib-card-body">
        ${imgSrc!=null?`<div class="footer-lib-img-row">
          <label class="footer-lib-img-label">图片链接快速替换</label>
          <input type="url" class="footer-lib-img-input" data-action="replace-img" data-seg-key="${esc(key)}" value="${esc(imgSrc)}" placeholder="https://..." aria-label="${esc(label)}：图片链接快速替换" />
          <span class="footer-lib-err" data-role="img-err-${esc(key)}" aria-live="polite"></span>
        </div>${renderAssetPicker(key,imgSrc)}`:''}
        <div class="footer-lib-textarea-row">
          <label class="footer-lib-textarea-label">HTML 内容（每行一段）</label>
          <textarea class="footer-lib-html-area" data-action="edit-html" data-seg-key="${esc(key)}" spellcheck="false" rows="6" aria-label="${esc(label)}：HTML 内容">${esc(lines.join('\n'))}</textarea>
          ${xssErr?`<span class="footer-lib-err" role="alert">${esc(xssErr)}</span>`:''}
          <span class="footer-lib-err" data-role="html-err-${esc(key)}" aria-live="polite"></span>
        </div>
        <div class="footer-lib-card-actions">
          <button type="button" class="footer-lib-reset-link" data-action="reset-seg" data-seg-key="${esc(key)}">重置为默认</button>
        </div>
      </div>`:''}
    </div>`;
  }

  function renderPreview() {
    const preset = (state.presets || []).find(p => p.id === state.active_preset_id) || {};
    const html = state.enabled ? buildFooterHtml(preset.segments || {}, state, assetLibrary, 'ft-default') : '';
    return `<div class="footer-lib-preview">
      <h3 class="footer-lib-section-title">即时预览</h3>
      <div class="footer-lib-preview-box">${html || '<p class="footer-lib-preview-empty">当前默认不输出页脚。</p>'}</div>
    </div>`;
  }

  function renderPresetTable() {
    const presets = Array.isArray(state.presets) ? state.presets : [];
    const cols = ['tip', ...orderedSegKeys()];
    const labels = { tip: '提示', ...SEGMENT_LABELS };
    return `<div class="footer-lib-preset-section">
      <h3 class="footer-lib-section-title">预设管理</h3>
      <div class="footer-lib-table-wrap"><table class="footer-lib-preset-table" aria-label="页脚预设管理">
        <thead><tr><th>预设名</th>${cols.map(k=>`<th>${esc(labels[k]||k)}</th>`).join('')}<th>默认</th><th></th></tr></thead>
        <tbody>${presets.map((p,i)=>{
          const isD = p.id === state.active_preset_id;
          return `<tr class="footer-lib-preset-row${isD?' footer-lib-preset-active':''}" data-preset-idx="${i}">
            <td><input type="text" class="footer-lib-preset-name" data-action="rename-preset" data-preset-idx="${i}" value="${esc(p.name)}" aria-label="预设名称：${esc(p.name)}" /></td>
            ${cols.map(k=>`<td class="footer-lib-preset-cell"><input type="checkbox" class="footer-lib-preset-chk" data-action="toggle-preset-seg" data-preset-idx="${i}" data-seg-key="${k}" aria-label="${esc(p.name)}：${esc(labels[k]||k)}" ${(p.segments||{})[k]?'checked':''} /></td>`).join('')}
            <td><button type="button" class="admin-secondary footer-lib-set-default-btn" data-action="set-default" data-preset-id="${esc(p.id)}" ${isD?'disabled':''}>${isD?'当前默认':'设为默认'}</button></td>
            <td><button type="button" class="admin-secondary danger footer-lib-del-preset-btn" data-action="delete-preset" data-preset-idx="${i}" title="删除预设" aria-label="删除预设：${esc(p.name)}">✕</button></td>
          </tr>`;
        }).join('')}</tbody>
      </table></div>
      <button type="button" class="admin-secondary footer-lib-add-preset-btn" data-action="add-preset">+ 新增预设</button>
    </div>`;
  }

  function renderJsonPanel() {
    return `<details class="footer-lib-json-details">
      <summary class="footer-lib-json-summary">高级 JSON（折叠）</summary>
      <div class="admin-json-panel footer-lib-json-body">
        <textarea class="footer-lib-json-area" data-role="json-area" rows="14" spellcheck="false" aria-label="页脚库 JSON">${esc(JSON.stringify(state,null,2))}</textarea>
        <div class="admin-json-actions footer-lib-json-actions">
          <button type="button" class="admin-secondary" data-action="validate-json">校验</button>
          <button type="button" class="admin-secondary" data-action="apply-json">应用 JSON</button>
          <span class="footer-lib-err" data-role="json-err" aria-live="polite"></span>
        </div>
      </div>
    </details>`;
  }

  function setJsonFeedback(el, message, tone) {
    el.textContent = message;
    el.dataset.tone = tone;
  }

  function render() {
    const order = orderedSegKeys();
    container.innerHTML = `<div class="footer-lib-editor">
      ${renderStatusBar()}
      <div class="footer-lib-main">
        <div class="footer-lib-segments-section">
          <h3 class="footer-lib-section-title">片段库</h3>
          ${order.map(renderCard).join('')}
        </div>
        ${renderPreview()}
      </div>
      ${renderPresetTable()}
      ${renderJsonPanel()}
    </div>`;
    injectStyles();
    // 监听器在初始化时绑定一次（事件委托，持久），不在每次 render 重绑——否则
    // XSS 拦截这类「不 emit/不 render」的分支会让 once 监听器永久丢失。
  }

  function handleClick(e) {
    const btn = e.target.closest('[data-action]');
    if (!btn) return;
    const { action } = btn.dataset;
    if (action === 'undo-all') { emit(clone(snapshot)); return; }
    if (action === 'toggle-enabled') { const next = clone(state); next.enabled = !next.enabled; emit(next); return; }
    if (action === 'toggle-card') { openCards[btn.dataset.segKey] = !openCards[btn.dataset.segKey]; emit(clone(state)); return; }
    if (action === 'reset-seg') {
      e.preventDefault();
      const key = btn.dataset.segKey;
      const def = segmentDefault(key);
      if (!def || !confirm(`确认将「${SEGMENT_LABELS[key]||key}」重置为默认内容？`)) return;
      const next = clone(state); next.segments[key] = clone(def); emit(next); return;
    }
    if (action === 'set-default') { const next = clone(state); next.active_preset_id = btn.dataset.presetId; emit(next); return; }
    if (action === 'delete-preset') {
      const idx = Number(btn.dataset.presetIdx);
      const preset = (state.presets || [])[idx];
      if (!preset || !confirm(`确认删除预设「${preset.name}」？`)) return;
      const next = clone(state);
      next.presets = next.presets.filter((_,i) => i !== idx);
      if (next.active_preset_id === preset.id && next.presets.length > 0) next.active_preset_id = next.presets[0].id;
      emit(next); return;
    }
    if (action === 'add-preset') {
      const next = clone(state);
      if (!Array.isArray(next.presets)) next.presets = [];
      next.presets.push({ id: genId(), name: '新预设', segments: segmentToggleDefaults() });
      emit(next); return;
    }
    if (action === 'validate-json') {
      const ta = container.querySelector('[data-role="json-area"]');
      const errEl = container.querySelector('[data-role="json-err"]');
      if (!ta || !errEl) return;
      try { JSON.parse(ta.value); setJsonFeedback(errEl, '✓ JSON 格式正确', 'success'); }
      catch(ex) { setJsonFeedback(errEl, 'JSON 解析失败：'+ex.message, 'error'); }
      return;
    }
    if (action === 'apply-json') {
      const ta = container.querySelector('[data-role="json-area"]');
      const errEl = container.querySelector('[data-role="json-err"]');
      if (!ta || !errEl) return;
      try {
        const p = JSON.parse(ta.value);
        if (typeof p !== 'object' || p === null) throw new Error('顶层必须是对象');
        if (p.segments && typeof p.segments !== 'object') throw new Error('segments 必须是对象');
        if (p.presets !== undefined && !Array.isArray(p.presets)) throw new Error('presets 必须是数组');
        emit(p);
        const nextDetails = container.querySelector('.footer-lib-json-details');
        const nextErr = container.querySelector('[data-role="json-err"]');
        if (nextDetails) nextDetails.open = true;
        if (nextErr) setJsonFeedback(nextErr, '✓ 应用成功', 'success');
      } catch(ex) { setJsonFeedback(errEl, '应用失败：'+ex.message, 'error'); }
      return;
    }
  }

  function handleChange(e) {
    const el = e.target; const { action } = el.dataset;
    if (action === 'pick-asset') {
      const key = el.dataset.segKey;
      if (!key || !el.value) return;
      const next = clone(state);
      next.segments[key].html_lines = replaceImgSrc(next.segments[key].html_lines, `{{asset#${el.value}}}`);
      emit(next); return;
    }
    if (action === 'toggle-preset-seg') {
      const next = clone(state); const idx = Number(el.dataset.presetIdx);
      if (!next.presets[idx]) return;
      if (!next.presets[idx].segments) next.presets[idx].segments = {};
      next.presets[idx].segments[el.dataset.segKey] = el.checked;
      emit(next); return;
    }
    if (action === 'rename-preset') {
      const idx = Number(el.dataset.presetIdx);
      const next = clone(state); if (!next.presets[idx]) return;
      next.presets[idx].name = el.value;
      state = next; if (typeof onChange === 'function') onChange(clone(state));
      const sb = container.querySelector('.footer-lib-status-bar');
      if (sb) sb.outerHTML = renderStatusBar();
      return;
    }
  }

  function handleBlur(e) {
    const el = e.target; const { action, segKey: key } = el.dataset;
    if (action === 'replace-img' && key) {
      const url = el.value.trim(); const err = validateImgUrl(url);
      const errEl = container.querySelector(`[data-role="img-err-${key}"]`);
      if (err) { if (errEl) errEl.textContent = err; return; }
      if (errEl) errEl.textContent = '';
      const next = clone(state); next.segments[key].html_lines = replaceImgSrc(next.segments[key].html_lines, url); emit(next); return;
    }
    if (action === 'edit-html' && key) {
      const lines = el.value.split('\n'); const xssErr = validateHtmlLines(lines);
      const errEl = container.querySelector(`[data-role="html-err-${key}"]`);
      if (xssErr) { if (errEl) errEl.textContent = xssErr; return; }
      if (errEl) errEl.textContent = '';
      const next = clone(state); next.segments[key].html_lines = lines; emit(next); return;
    }
  }

  function moveSegment(fromKey, toKey) {
    if (!fromKey || !toKey || fromKey === toKey) return;
    const order = orderedSegKeys();
    const from = order.indexOf(fromKey);
    const to = order.indexOf(toKey);
    if (from < 0 || to < 0) return;
    order.splice(to, 0, order.splice(from, 1)[0]);
    const next = clone(state);
    next.segment_order = order;
    emit(next);
  }

  function handleDragStart(e) {
    const card = e.target.closest('.footer-lib-card');
    draggingKey = card && card.dataset.segKey || '';
    if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move';
  }

  function handleDragOver(e) {
    if (draggingKey && e.target.closest('.footer-lib-card')) e.preventDefault();
  }

  function handleDrop(e) {
    const card = e.target.closest('.footer-lib-card');
    if (!card) return;
    e.preventDefault();
    moveSegment(draggingKey, card.dataset.segKey);
    draggingKey = '';
  }

  let stylesInjected = false;
  function injectStyles() {
    if (stylesInjected || document.getElementById('footer-lib-styles')) return;
    stylesInjected = true;
    const s = document.createElement('style'); s.id = 'footer-lib-styles';
    s.textContent = `
.footer-lib-editor{display:grid;gap:16px}
.footer-lib-status-bar{display:flex;align-items:center;flex-wrap:wrap;gap:10px;padding:8px 10px;border:1px solid var(--fs-divider,rgba(31,35,39,.08));border-radius:var(--fs-r-s,6px);background:var(--fs-bg-nested,#F7F8FA);font-size:12px}
.footer-lib-sync-label{display:inline-flex;align-items:center;gap:6px;color:var(--fs-text-2,#646A73);font-weight:600}
.footer-lib-sync-label::before{content:"";width:7px;height:7px;border-radius:var(--fs-r-full,9999px);background:var(--fs-text-3,#6F757D)}
.footer-lib-sync-label.synced::before{background:var(--fs-success,#00A870)}
.footer-lib-sync-label.dirty::before{background:var(--fs-warning,#FF8800)}
.footer-lib-toggle-enabled.on{border-color:var(--fs-brand,#1456F0);color:var(--fs-brand,#1456F0);background:var(--fs-brand-soft,#EBF1FF)}
.footer-lib-undo-btn{margin-left:auto}
.footer-lib-main{display:grid;grid-template-columns:minmax(0,1fr) minmax(260px,.42fr);gap:16px;align-items:start}
.footer-lib-section-title{margin:12px 0 8px;color:var(--fs-text-1,#1F2329);font-size:13px;font-weight:700}
.footer-lib-card{overflow:hidden;margin-bottom:6px;border:1px solid var(--fs-border-card,#E5E6EB);border-radius:var(--fs-r-s,6px);background:var(--fs-bg-card,#fff)}
.footer-lib-card[draggable=true]{cursor:grab}
.footer-lib-card-head{display:flex;align-items:center;justify-content:space-between;width:100%;min-height:44px;padding:10px 12px;border:0;background:var(--fs-bg-nested,#F7F8FA);color:var(--fs-text-1,#1F2329);font-family:inherit;font-size:13px;font-weight:600;text-align:left;cursor:pointer;transition:background var(--fs-t-fast,150ms) var(--fs-ease-out,ease)}
.footer-lib-card-head:hover,.footer-lib-card.open .footer-lib-card-head{background:var(--fs-bg-sunken,#F2F3F5)}
.footer-lib-card-label{display:flex;align-items:center;gap:8px;min-width:0}
.footer-lib-drag-handle{flex:0 0 auto;color:var(--fs-text-3,#6F757D);font-size:14px}
.footer-lib-changed-dot{display:inline-flex;align-items:center;gap:5px;padding:2px 6px;border-radius:var(--fs-r-full,9999px);background:var(--fs-warning-soft,#FFF3E5);color:var(--fs-text-1,#1F2329);font-size:11px;font-weight:600}
.footer-lib-changed-dot::before{content:"";width:6px;height:6px;border-radius:var(--fs-r-full,9999px);background:var(--fs-warning,#FF8800)}
.footer-lib-card-arrow{flex:0 0 auto;color:var(--fs-text-3,#6F757D);font-size:11px}
.footer-lib-card-body{display:grid;gap:10px;padding:12px;border-top:1px solid var(--fs-divider,rgba(31,35,39,.08));background:var(--fs-bg-card,#fff)}
.footer-lib-img-row,.footer-lib-textarea-row{display:grid;gap:6px}
.footer-lib-img-label,.footer-lib-textarea-label{color:var(--fs-text-2,#646A73);font-size:12px}
.footer-lib-img-input,.footer-lib-preset-name,.footer-lib-html-area,.footer-lib-json-area{box-sizing:border-box;width:100%;border:1px solid var(--fs-border-control,#D9DCE0);border-radius:var(--fs-r-xs,4px);background:var(--fs-bg-card,#fff);color:var(--fs-text-1,#1F2329)}
.footer-lib-img-input{padding:7px 9px;font-family:inherit;font-size:13px}
.footer-lib-html-area,.footer-lib-json-area{padding:10px 12px;font-family:var(--fs-mono,"JetBrains Mono",ui-monospace,monospace);font-size:12px;line-height:1.55;resize:vertical}
.footer-lib-json-area{background:var(--fs-bg-sunken,#F2F3F5)}
.footer-lib-img-input:focus,.footer-lib-preset-name:focus,.footer-lib-html-area:focus,.footer-lib-json-area:focus{outline:none;border-color:var(--fs-brand,#1456F0);box-shadow:0 0 0 2px var(--fs-brand-soft,#EBF1FF)}
.footer-lib-err{color:var(--fs-danger,#F54A45);font-size:12px;line-height:1.5}
.footer-lib-err:empty{display:none}
.footer-lib-err[data-tone]{display:inline-flex;align-items:center;gap:6px;padding:3px 7px;border-radius:var(--fs-r-xs,4px);color:var(--fs-text-1,#1F2329)}
.footer-lib-err[data-tone]::before{content:"";width:7px;height:7px;border-radius:var(--fs-r-full,9999px)}
.footer-lib-err[data-tone="success"]{background:var(--fs-success-soft,#E5F7F0)}
.footer-lib-err[data-tone="success"]::before{background:var(--fs-success,#00A870)}
.footer-lib-err[data-tone="error"]{background:var(--fs-danger-soft,#FEECEC)}
.footer-lib-err[data-tone="error"]::before{background:var(--fs-danger,#F54A45)}
.footer-lib-card-actions{display:flex;gap:12px}
.footer-lib-reset-link{padding:2px 0;border:0;background:transparent;color:var(--fs-brand,#1456F0);font-family:inherit;font-size:12px;text-decoration:underline;text-underline-offset:2px;cursor:pointer}
.footer-lib-preview{position:sticky;top:0}
.footer-lib-preview-box{max-height:56vh;overflow:auto;padding:12px;border-radius:var(--fs-r-s,6px);background:var(--fs-bg-card,#fff);box-shadow:var(--fs-sh-s1,0 1px 3px rgba(31,35,39,.04));font-size:13px}
.footer-lib-preview-box img{max-width:100%;height:auto}
.footer-lib-preview-empty{margin:0;color:var(--fs-text-3,#6F757D);font-size:13px;line-height:1.6}
.footer-lib-preset-section{display:grid;gap:10px}
.footer-lib-table-wrap{overflow-x:auto}
.footer-lib-preset-table{width:100%;border-collapse:collapse;color:var(--fs-text-1,#1F2329);font-size:12px}
.footer-lib-preset-table th{padding:6px 8px;border-bottom:1px solid var(--fs-divider,rgba(31,35,39,.08));background:var(--fs-bg-nested,#F7F8FA);color:var(--fs-text-2,#646A73);font-weight:700;text-align:center;white-space:nowrap}
.footer-lib-preset-table th:first-child{text-align:left}
.footer-lib-preset-table td{padding:6px 8px;border-bottom:1px solid var(--fs-divider,rgba(31,35,39,.08));vertical-align:middle}
.footer-lib-preset-row.footer-lib-preset-active td{background:var(--fs-brand-soft,#EBF1FF)}
.footer-lib-preset-name{min-width:120px;padding:5px 7px;font-family:inherit;font-size:13px}
.footer-lib-preset-cell{text-align:center}
.footer-lib-preset-chk{width:16px;height:16px;accent-color:var(--fs-brand,#1456F0);cursor:pointer}
.footer-lib-set-default-btn,.footer-lib-del-preset-btn{padding:4px 8px;font-size:12px;white-space:nowrap}
.footer-lib-add-preset-btn{justify-self:start;font-size:12px}
.footer-lib-json-details{margin-top:4px}
.footer-lib-json-summary{display:flex;align-items:center;min-height:32px;padding:4px 0;color:var(--fs-text-2,#646A73);font-size:12px;font-weight:600;cursor:pointer;user-select:none;transition:color var(--fs-t-fast,150ms) var(--fs-ease-out,ease)}
.footer-lib-json-summary:hover{color:var(--fs-text-1,#1F2329)}
.footer-lib-json-body{display:grid;gap:6px;padding:10px 0}
.footer-lib-json-actions{margin-top:8px}
.footer-lib-card-head:focus-visible,.footer-lib-reset-link:focus-visible,.footer-lib-json-summary:focus-visible,.footer-lib-editor .admin-secondary:focus-visible{outline:none;box-shadow:0 0 0 2px var(--fs-brand-soft,#EBF1FF),0 0 0 3px var(--fs-brand,#1456F0)}
@media(max-width:900px){.footer-lib-main{grid-template-columns:1fr}.footer-lib-preview{position:static}.footer-lib-img-input,.footer-lib-preset-name,.footer-lib-html-area,.footer-lib-json-area{font-size:14px}.footer-lib-preset-chk{width:20px;height:20px}.footer-lib-toggle-enabled,.footer-lib-undo-btn,.footer-lib-set-default-btn,.footer-lib-del-preset-btn,.footer-lib-add-preset-btn{min-height:44px}}
@media(prefers-reduced-motion:reduce){.footer-lib-editor *{animation-duration:.001ms!important;transition-duration:.001ms!important}}
`;
    document.head.appendChild(s);
  }

  // 事件委托绑定一次（持久）：覆盖所有 render 后的子元素，不随 render 重绑。
  container.addEventListener('click', handleClick);
  container.addEventListener('change', handleChange);
  container.addEventListener('blur', handleBlur, true); // capture：blur 不冒泡
  container.addEventListener('dragstart', handleDragStart);
  container.addEventListener('dragover', handleDragOver);
  container.addEventListener('drop', handleDrop);
  render();
  return {
    destroy() {
      container.removeEventListener('click', handleClick);
      container.removeEventListener('change', handleChange);
      container.removeEventListener('blur', handleBlur, true);
      container.removeEventListener('dragstart', handleDragStart);
      container.removeEventListener('dragover', handleDragOver);
      container.removeEventListener('drop', handleDrop);
      container.innerHTML = '';
    },
  };
}
