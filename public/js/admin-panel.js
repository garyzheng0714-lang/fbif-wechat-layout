// admin-panel.js — password-gated rule preset editor.
// Keeps UI orchestration separate from the rule schema and from the rendering
// engine, so adding an editable field means updating rule-document.js first.

import {
  DEFAULT_RULE_CONFIG,
  RULE_SCHEMA,
  adminLogin,
  buildRuleConfigDocument,
  deleteRulePreset,
  listRulePresets,
  normalizeRuleConfig,
  parseRuleConfigDocument,
  setActiveRulePreset,
  validateRuleConfig,
} from './rule-presets.js';
import {
  DEFAULT_BEHAVIOR,
  normalizeRuleBehavior,
  validateRuleBehavior,
} from './rule-behavior.js';
import { createMoreArticlesRuleEditor } from './more-articles-rule-editor.js';
import { createFooterLibraryEditor } from './footer-library-rule-editor.js';
import { defaultFooterData, footerFromPreset } from './admin-footer-data.js';
import { createAssetLibraryEditor } from './asset-library-rule-editor.js';
import { assetLibraryFromPreset, defaultAssetLibraryData } from './admin-asset-data.js';
import { bottomBlocksFromPreset, createBlockEditorBinder, renderRuleBottomTabHtml } from './admin-block-data.js';
import { createBehaviorFormBinder } from './admin-behavior-data.js';
import { serverRuleConfigDocument } from './admin-rule-payload.js';
import { syncRuleVisualEditor } from './admin-rule-visual-sync.js';
import { adminDirtyCount, updateDirtyChip } from './admin-dirty-state.js';
import { numberField, bindSteppers } from './admin-field-controls.js';
import { livePreviewHTML, applyLivePreview, targetForKey } from './admin-live-preview.js';
import { RULE_PACKS } from './rule-packs/registry.js';

const ADMIN_HINT = '为防止外部人员篡改，请找 Gary 要密码';
function esc(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function clientRuleID(name) {
  const id = String(name || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return id && id !== 'default' ? id : 'rule-' + Date.now();
}

function ruleListFromDoc(doc) {
  if (doc && Array.isArray(doc.presets)) return doc.presets;
  if (doc && Array.isArray(doc.rules)) return doc.rules;
  if (doc && doc.rules && typeof doc.rules === 'object') return Object.values(doc.rules);
  return [];
}
export function uniqueClientRuleID(name, doc) {
  const base = clientRuleID(name);
  const used = new Set(ruleListFromDoc(doc)
    .map(rule => rule && rule.id)
    .filter(Boolean));
  if (!used.has(base)) return base;
  let suffix = 2;
  while (used.has(base + '-' + suffix)) suffix++;
  return base + '-' + suffix;
}

function fieldValue(config, field) {
  const normalized = normalizeRuleConfig(config);
  return normalized[field.key];
}

function renderField(config, field) {
  const value = fieldValue(config, field);
  if (field.type === 'color') {
    return `
      <label class="admin-field">
        <span>${esc(field.label)}</span>
        <span class="admin-color-field">
          <input type="color" data-rule-key="${esc(field.key)}" value="${esc(value)}">
          <span data-color-hex="${esc(field.key)}">${esc(value)}</span>
        </span>
      </label>`;
  }
  if (field.type === 'boolean') {
    return `
      <label class="admin-field">
        <span>${esc(field.label)}</span>
        <button type="button" class="toggle-switch admin-toggle ${value === true ? 'on' : ''}" data-rule-key="${esc(field.key)}" aria-pressed="${value === true ? 'true' : 'false'}">
          <span class="toggle-knob"></span>
        </button>
      </label>`;
  }
  if (field.type === 'text') {
    return `
      <label class="admin-field">
        <span>${esc(field.label)}</span>
        <input type="text" data-rule-key="${esc(field.key)}" value="${esc(value)}">
      </label>`;
  }
  return numberField(field, value, esc);
}

function renderGroups(config) {
  return RULE_SCHEMA.map(group => `
    <section class="admin-rule-group">
      <h3>${esc(group.label)}</h3>
      <div class="admin-field-grid">
        ${group.fields
          .filter(field => group.id !== 'more_articles' || field.key !== 'more_articles_enabled')
          .map(field => renderField(config, field))
          .join('')}
      </div>
      ${group.id === 'more_articles' ? '<div data-role="more-articles-rule-editor"></div>' : ''}
    </section>
  `).join('');
}

function activePresetFromDoc(doc) {
  return (doc && doc.presets || []).find(p => p.id === doc.active_id) || (doc && doc.presets && doc.presets[0]) || null;
}

function presetByID(doc, id) {
  return (doc && doc.presets || []).find(p => p.id === id) || null;
}

export function initAdminPanel(options = {}) {
  const button = document.getElementById('adminLockBtn');
  if (!button) return;

  const getConfig = options.getConfig || (() => (window._activeConfig && window._activeConfig.config) || {});
  const getPreset = options.getPreset || (() => (window._activeConfig && window._activeConfig.preset) || null);
  const getBehavior = options.getBehavior || (() => (window._activeConfig && window._activeConfig.behavior) || DEFAULT_BEHAVIOR);
  const applyConfig = options.applyConfig || ((config, preset) => {
    window._activeConfig = { config: normalizeRuleConfig(config), preset, behavior: normalizeRuleBehavior((preset && preset.behavior) || workingBehavior) };
  });
  const rulePacks = Array.isArray(options.rulePacks) ? options.rulePacks : [];

  let overlay = null;
  let password = '';
  let doc = null;
  let selectedID = '';
  let draftPreset = null;
  let workingConfig = normalizeRuleConfig(getConfig());
  let workingBehavior = normalizeRuleBehavior(getBehavior());
  let workingFooter = null;
  let workingAssets = null;
  let workingBottomBlocks = null; const blockBinder = createBlockEditorBinder({ getModule: () => workingBottomBlocks || bottomBlocksFromPreset(selectedPreset()), setModule: (m) => { workingBottomBlocks = m; updateStatusInline('有未保存改动，点击「保存修改」后生效'); }, getChannel: () => (isFtDefaultPack() ? 'ft' : 'mp'), getScope: () => currentAccountID(), getAssetLibrary: () => workingAssets || defaultAssetLibraryData(), addAsset: (asset) => { const lib = workingAssets || defaultAssetLibraryData(); workingAssets = { ...lib, assets: [asset, ...(Array.isArray(lib.assets) ? lib.assets : [])] }; updateStatusInline('已上传并入库，点击「保存修改」生效'); } });
  const behaviorBinder = createBehaviorFormBinder({ getBehavior: () => workingBehavior, setBehavior: (b) => { workingBehavior = b; syncBehaviorText(); const ta = overlay && overlay.querySelector('[data-role="behavior-json"]'); if (ta) ta.value = behaviorText; updateStatusInline('有未保存改动，点击「保存修改」后生效'); } });
  let footerEditorInstance = null, assetEditorInstance = null;
  let workingName = '未命名规则';
  let workingDescription = '';
  let jsonText = '';
  let jsonDirty = false;
  let jsonSyncTimer = 0;
  let behaviorText = '';
  let behaviorDirty = false;
  let status = '';
  let busy = false;
  let activeView = 'assignments';
  let activeRuleTab = 'params';

  function setStatus(text) {
    status = text || '';
    render();
  }

  async function refreshDoc() {
    doc = await listRulePresets(password);
    const current = getPreset();
    selectedID = current && presetByID(doc, current.id) ? current.id : (doc.active_id || 'default');
    const selected = presetByID(doc, selectedID) || activePresetFromDoc(doc);
    draftPreset = null;
    workingConfig = normalizeRuleConfig(selected ? selected.config : getConfig());
    workingBehavior = normalizeRuleBehavior(selected ? selected.behavior : getBehavior());
    workingName = selected && selected.name ? selected.name : '未命名规则';
    workingDescription = selected && selected.description ? selected.description : '';
    workingFooter = footerFromPreset(selected);
    workingAssets = assetLibraryFromPreset(selected);
    workingBottomBlocks = bottomBlocksFromPreset(selected);
    syncJSONText();
    syncBehaviorText();
    applyConfig(workingConfig, presetWithBehavior(selected), { source: 'server' });
  }

  function close() {
    if (!overlay) return;
    window.clearTimeout(jsonSyncTimer);
    overlay.remove();
    overlay = null;
    document.removeEventListener('keydown', onKey);
  }

  function onKey(e) {
    if (e.key === 'Escape') close();
  }

  function open() {
    if (overlay) return;
    overlay = document.createElement('div');
    overlay.className = 'admin-overlay';
    document.body.appendChild(overlay);
    document.addEventListener('keydown', onKey);
    overlay.addEventListener('click', e => { if (e.target === overlay) close(); });
    render();
  }

  async function run(action, okText) {
    if (busy) return;
    busy = true;
    setStatus('处理中...');
    try {
      await action();
      setStatus(okText || '已保存');
    } catch (err) {
      setStatus(err.message || '操作失败');
    } finally {
      busy = false;
      render();
    }
  }

  function renderLogin() {
    overlay.innerHTML = `
      <div class="admin-dialog compact" role="dialog" aria-modal="true" aria-labelledby="adminTitle">
        <div class="admin-head">
          <div>
            <div class="admin-title" id="adminTitle">规则后台</div>
            <div class="admin-subtitle">${ADMIN_HINT}</div>
          </div>
          <button type="button" class="admin-close" aria-label="关闭">×</button>
        </div>
        <form class="admin-login-form">
          <label class="admin-login-field">
            <span>访问密码</span>
            <input type="password" autocomplete="current-password" autofocus>
          </label>
          <button type="submit" class="admin-primary">进入后台</button>
          <div class="admin-status">${esc(status)}</div>
        </form>
      </div>`;
    overlay.querySelector('.admin-close').addEventListener('click', close);
    const form = overlay.querySelector('.admin-login-form');
    const input = form.querySelector('input');
    form.addEventListener('submit', async e => {
      e.preventDefault();
      password = input.value.trim();
      await run(async () => {
        await adminLogin(password);
        if (typeof options.onAuthenticated === 'function') options.onAuthenticated(password);
        await refreshDoc();
      }, '已进入后台');
    });
  }

  function renderWarningsHTML() {
    const warnings = validateRuleConfig(workingConfig);
    if (warnings.length === 0) {
      return '<div class="admin-rule-ok">规则之间暂无明显冲突。</div>';
    }
    return '<ul class="admin-warnings">' + warnings.map(w => '<li>' + esc(w) + '</li>').join('') + '</ul>';
  }

  function refreshWarnings() {
    const el = overlay && overlay.querySelector('[data-role="rule-warnings"]');
    if (el) el.innerHTML = renderWarningsHTML();
  }

  function renderRuleTabs() {
    const tabs = [['params', '参数'], ['bottom', '底部']];
    return `
      <div class="admin-rule-tabs" role="tablist" aria-label="规则编辑分区">
        ${tabs.map(([id, label]) => `
          <button type="button" data-rule-tab="${id}" class="${activeRuleTab === id ? 'active' : ''}" aria-selected="${activeRuleTab === id ? 'true' : 'false'}">${label}</button>
        `).join('')}
      </div>`;
  }

  function selectedPreset() {
    return presetByID(doc, selectedID) ||
      (draftPreset && draftPreset.id === selectedID ? draftPreset : null) ||
      activePresetFromDoc(doc) ||
      getPreset();
  }

  function dirtyCount() { return adminDirtyCount({ draftPreset, selectedID, selected: selectedPreset(), workingName, workingDescription, workingConfig, workingBehavior, workingAssets, workingBottomBlocks, workingFooter, isFtDefault: isFtDefaultPack() }); }
  function refreshDirtyChip() { updateDirtyChip(overlay, dirtyCount()); }

  function presetWithBehavior(preset) {
    if (!preset) return { behavior: workingBehavior };
    return { ...preset, behavior: workingBehavior };
  }

  function syncJSONText() {
    jsonText = JSON.stringify(buildRuleConfigDocument(workingConfig, {
      name: workingName || '未命名规则',
      description: workingDescription || '',
    }), null, 2);
    jsonDirty = false;
  }

  function syncBehaviorText() {
    behaviorText = JSON.stringify(workingBehavior, null, 2);
    behaviorDirty = false;
  }

  function syncJSONEditorFromInputs() {
    if (jsonDirty || !overlay) return;
    const nameInput = overlay.querySelector('[data-role="preset-name"]');
    const descriptionInput = overlay.querySelector('[data-role="preset-description"]');
    workingName = nameInput ? nameInput.value : workingName;
    workingDescription = descriptionInput ? descriptionInput.value : workingDescription;
    syncJSONText();
    const textarea = overlay.querySelector('[data-role="rule-json"]');
    if (textarea) textarea.value = jsonText;
  }

  function updateStatusInline(text) {
    status = text || '';
    const el = overlay && overlay.querySelector('.admin-foot .admin-status');
    if (el) el.textContent = status;
    refreshDirtyChip(); }
  function refreshVisualEditorFromConfig() {
    syncRuleVisualEditor({ overlay, schema: RULE_SCHEMA, config: workingConfig, name: workingName, description: workingDescription, fieldValue });
    bindMoreArticlesRuleEditor(); refreshWarnings();
  }

  function applyJSONDraftToVisual(showStatus = false) {
    const parsed = parseRuleConfigDocument(jsonText);
    workingName = parsed.name || '未命名规则';
    workingDescription = parsed.description;
    workingConfig = parsed.config;
    applyConfig(workingConfig, presetWithBehavior(selectedPreset()));
    refreshVisualEditorFromConfig();
    if (showStatus) updateStatusInline('JSON 已同步到表单');
  }

  function syncDirtyJSONBeforePersist() {
    if (!jsonDirty) return;
    const parsed = parseRuleConfigDocument(jsonText);
    workingName = parsed.name || '未命名规则';
    workingDescription = parsed.description;
    workingConfig = parsed.config;
    jsonDirty = false;
    syncJSONText();
  }

  function parseBehaviorDocument(input) {
    let raw;
    try {
      raw = JSON.parse(input);
    } catch {
      throw new Error('代码化规则 JSON 格式不正确');
    }
    const result = validateRuleBehavior(raw);
    if (!result.ok) throw new Error(result.errors[0] || '代码化规则校验失败');
    return result.behavior;
  }

  function syncDirtyBehaviorBeforePersist() {
    if (!behaviorDirty) return;
    workingBehavior = parseBehaviorDocument(behaviorText);
    syncBehaviorText();
  }

  function isFtDefaultPack() {
    const pack = RULE_PACKS[currentAccountID()];
    return !!(pack && pack.templateId === 'foodtalks-site');
  }

  function visibleRulePacks() {
    return rulePacks.length ? rulePacks : [{ id: 'fbif', accountName: 'FBIF食品饮料创新' }];
  }

  function accountBindings() {
    const byID = new Map((doc && doc.accounts || []).map(account => [account.id, account]));
    return visibleRulePacks().map(pack => {
      const binding = byID.get(pack.id) || {};
      return {
        id: pack.id,
        name: pack.accountName,
        activeID: binding.active_id || binding.activeID || (doc && doc.active_id) || 'default',
      };
    });
  }

  function accountsUsingRule(ruleID) {
    return accountBindings().filter(account => account.activeID === ruleID);
  }

  function renderApplicationBadges(ruleID) {
    const accounts = accountsUsingRule(ruleID);
    if (accounts.length === 0) {
      return '<div class="admin-applied-empty">未应用</div>';
    }
    return `
      <div class="admin-applied-badges" aria-label="规则应用位置">
        <span class="admin-applied-label">已应用到：</span>
        ${accounts.map(account => `<span class="admin-applied-badge">${esc(account.name)}</span>`).join('')}
      </div>`;
  }

  function renderSideNav() {
    return `
      <nav class="admin-side-nav" aria-label="规则后台导航">
        <button type="button" data-view="assignments" class="${activeView === 'assignments' ? 'active' : ''}">账号</button>
        <button type="button" data-view="rules" class="${activeView === 'rules' ? 'active' : ''}">规则</button>
        <button type="button" data-view="assets" class="${activeView === 'assets' ? 'active' : ''}">素材库</button>
      </nav>`;
  }

  function renderPresetOptions(activeID) {
    const presets = (doc && doc.presets) || [];
    return presets.map(p => {
      const activeMark = p.id === (doc && doc.active_id) ? ' · 当前预览' : '';
      return `<option value="${esc(p.id)}" ${p.id === activeID ? 'selected' : ''}>${esc(p.name + activeMark)}</option>`;
    }).join('');
  }

  function currentAccountID() {
    return (window._activeRulePack && window._activeRulePack.id) || 'fbif';
  }

  function renderAssignments() {
    return `
      <section class="admin-assignment-panel">
        <h3>账号</h3>
        <p class="admin-panel-note">选择每个公众号当前应用的规则。</p>
        ${accountBindings().map(account => `
          <label class="admin-account-row">
            <span>${esc(account.name)}</span>
            <select data-account-rule="${esc(account.id)}">
              ${renderPresetOptions(account.activeID)}
            </select>
          </label>
        `).join('')}
      </section>`;
  }

  function renderAssetsLibraryView() {
    return `
      <section class="admin-rule-group admin-assets-view">
        <h3>素材库</h3>
        <p class="admin-panel-note">被底部区块引用的图片资产池：仅新增 / 删除 / 查看，不引用别处。</p>
        <div data-role="asset-library-editor"></div>
      </section>`;
  }

  function renderRuleTabContent() {
    if (activeRuleTab === 'bottom') {
      return renderRuleBottomTabHtml(isFtDefaultPack());
    }
    return `
      <section class="admin-visual-zone" aria-label="前端修改区">
        <div class="admin-form-col"><div data-role="rule-warnings">${renderWarningsHTML()}</div>${renderGroups(workingConfig)}</div>
        ${livePreviewHTML()}
      </section>`;
  }

  function renderRulesEditor(optionsHTML) {
    const isDefaultRule = selectedID === 'default';
    return `
      <div class="admin-rule-bar">
        <label>
          <span>规则预设</span>
          <span class="admin-preset-control">
            <select data-role="preset-select">${optionsHTML}</select>
            <button type="button" class="admin-icon-danger" data-action="delete" ${isDefaultRule ? 'disabled' : ''} title="删除当前规则" aria-label="删除当前规则">
              <svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true"><path d="M9 4h6l1 2h4" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/><path d="M4 6h16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/><path d="M7 9l1 10h8l1-10" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"/><path d="M10 11v6M14 11v6" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>
            </button>
          </span>
        </label>
        <button type="button" class="admin-secondary" data-action="new">新建规则</button>
        <button type="button" class="admin-secondary" data-action="set-active">应用到当前公众号</button>
        <button type="button" class="admin-secondary" data-action="save-as">复制为新规则</button>
        <button type="button" class="admin-primary" data-action="save">保存修改</button>
      </div>
      ${renderApplicationBadges(selectedID)}
      <div class="admin-meta-grid">
        <label>
          <span>规则名称</span>
          <input type="text" data-role="preset-name" value="${esc(workingName)}">
        </label>
        <label>
          <span>说明</span>
          <input type="text" data-role="preset-description" value="${esc(workingDescription)}">
        </label>
      </div>
      ${renderRuleTabs()}
      ${renderRuleTabContent()}`;
  }

  function renderMain() {
    const presets = (doc && doc.presets) || [];
    if (!jsonDirty) syncJSONText();
    const listPresets = draftPreset && !presetByID(doc, draftPreset.id)
      ? presets.concat([draftPreset])
      : presets;
    const optionsHTML = listPresets.map(p => {
      const activeMark = p.id === (doc && doc.active_id) ? ' · 当前' : '';
      return `<option value="${esc(p.id)}" ${p.id === selectedID ? 'selected' : ''}>${esc(p.name + activeMark)}</option>`;
    }).join('');
    const contentHTML = activeView === 'assignments'
      ? renderAssignments()
      : activeView === 'assets'
      ? renderAssetsLibraryView()
      : renderRulesEditor(optionsHTML);
    overlay.innerHTML = `
      <div class="admin-dialog" role="dialog" aria-modal="true" aria-labelledby="adminTitle">
        <div class="admin-head">
          <div>
            <div class="admin-title" id="adminTitle">规则后台</div>
            <div class="admin-subtitle">${ADMIN_HINT}</div>
          </div>
          <button type="button" class="admin-close" aria-label="关闭">×</button>
        </div>
        <div class="admin-layout">
          ${renderSideNav()}
          <div class="admin-body">${contentHTML}</div>
        </div>
        <div class="admin-foot">
          <div class="admin-status">${esc(status)}</div>
          <span class="admin-dirty-chip ${dirtyCount() ? 'dirty' : ''}" data-role="dirty-chip">${dirtyCount() ? dirtyCount() + ' 项未保存' : '无未保存改动'}</span>
        </div>
      </div>`;
    bindMain();
  }

  function updateWorkingField(key, value) {
    workingConfig = normalizeRuleConfig({ ...workingConfig, [key]: value });
    const selected = selectedPreset();
    jsonDirty = false;
    applyConfig(workingConfig, presetWithBehavior(selected));
    applyLivePreview(overlay, workingConfig, { tween: true, target: targetForKey(key) });
    refreshWarnings();
    syncJSONEditorFromInputs(); updateStatusInline('有未保存改动，点击「保存修改」后生效');
  }

  function bindMoreArticlesRuleEditor() {
    const mount = overlay.querySelector('[data-role="more-articles-rule-editor"]');
    if (!mount) return;
    const editor = createMoreArticlesRuleEditor({
      config: workingConfig,
      onChange(nextConfig) {
        workingConfig = normalizeRuleConfig({ ...workingConfig, ...nextConfig });
        const selected = selectedPreset();
        jsonDirty = false;
        applyConfig(workingConfig, selected);
        refreshWarnings();
        syncJSONEditorFromInputs(); updateStatusInline('有未保存改动，点击「保存修改」后生效');
      },
    });
    mount.replaceChildren(editor);
  }

  function bindFooterLibraryEditor() {
    const mount = overlay && overlay.querySelector('[data-role="footer-library-editor"]');
    if (!mount) return;
    if (footerEditorInstance) { footerEditorInstance.destroy(); footerEditorInstance = null; }
    footerEditorInstance = createFooterLibraryEditor({
      container: mount,
      footerData: workingFooter || defaultFooterData(),
      assetLibrary: workingAssets || defaultAssetLibraryData(),
      onChange(next) { workingFooter = next; updateStatusInline('有未保存改动，点击「保存修改」后生效'); },
    });
  }

  function bindAssetLibraryEditor() {
    const mount = overlay && overlay.querySelector('[data-role="asset-library-editor"]');
    if (!mount) return;
    if (assetEditorInstance) { assetEditorInstance.destroy(); assetEditorInstance = null; }
    assetEditorInstance = createAssetLibraryEditor({ container: mount, assetLibrary: workingAssets || defaultAssetLibraryData(), referencePresets: (doc && doc.presets) || [], currentScope: currentAccountID(), compact: true, onChange(next) { workingAssets = next; updateStatusInline('有未保存改动，点击「保存修改」后生效'); } });
  }

  function bindMain() {
    overlay.querySelector('.admin-close').addEventListener('click', close);
    overlay.querySelectorAll('[data-view]').forEach(btn => {
      btn.addEventListener('click', () => {
        activeView = btn.dataset.view;
        renderMain();
      });
    });
    overlay.querySelectorAll('[data-rule-tab]').forEach(btn => {
      btn.addEventListener('click', () => {
        if (footerEditorInstance) { footerEditorInstance.destroy(); footerEditorInstance = null; }
        if (assetEditorInstance) { assetEditorInstance.destroy(); assetEditorInstance = null; }
        blockBinder.destroy();
        behaviorBinder.destroy();
        activeRuleTab = btn.dataset.ruleTab;
        renderMain();
      });
    });
    overlay.querySelectorAll('[data-account-rule]').forEach(select => {
      select.addEventListener('change', () => {
        const accountID = select.dataset.accountRule;
        const ruleID = select.value;
        run(async () => {
          doc = await setActiveRulePreset(password, ruleID, accountID);
          const selected = presetByID(doc, ruleID);
          if (accountID === ((window._activeRulePack && window._activeRulePack.id) || 'fbif')) {
            workingConfig = normalizeRuleConfig(selected ? selected.config : workingConfig);
            workingBehavior = normalizeRuleBehavior(selected ? selected.behavior : workingBehavior);
            workingAssets = assetLibraryFromPreset(selected);
            workingBottomBlocks = bottomBlocksFromPreset(selected);
            selectedID = ruleID;
            syncBehaviorText();
            applyConfig(workingConfig, presetWithBehavior(selected), { source: 'server' });
          }
        }, '已更新规则配置');
      });
    });
    if (activeView === 'assets') { bindAssetLibraryEditor(); return; }
    if (activeView !== 'rules') return;
    bindMoreArticlesRuleEditor();
    bindFooterLibraryEditor();
    bindAssetLibraryEditor();
    blockBinder.bind(overlay);
    behaviorBinder.bind(overlay);
    overlay.querySelector('[data-role="preset-select"]').addEventListener('change', e => {
      selectedID = e.target.value;
      const selected = selectedPreset();
      workingConfig = normalizeRuleConfig(selected ? selected.config : getConfig());
      workingBehavior = normalizeRuleBehavior(selected ? selected.behavior : getBehavior());
      workingFooter = footerFromPreset(selected);
      workingAssets = assetLibraryFromPreset(selected);
      workingName = selected && selected.name ? selected.name : '未命名规则';
      workingDescription = selected && selected.description ? selected.description : '';
      syncJSONText();
      syncBehaviorText();
      applyConfig(workingConfig, presetWithBehavior(selected));
      status = '仅在预览中查看，未改变公众号应用规则';
      renderMain();
    });
    overlay.querySelector('[data-role="preset-name"]').addEventListener('input', e => {
      workingName = e.target.value;
      jsonDirty = false;
      syncJSONEditorFromInputs(); updateStatusInline('有未保存改动，点击「保存修改」后生效');
    });
    overlay.querySelector('[data-role="preset-description"]').addEventListener('input', e => {
      workingDescription = e.target.value;
      jsonDirty = false;
      syncJSONEditorFromInputs(); updateStatusInline('有未保存改动，点击「保存修改」后生效');
    });
    const ruleJSON = overlay.querySelector('[data-role="rule-json"]');
    if (ruleJSON) ruleJSON.addEventListener('input', e => {
      jsonText = e.target.value;
      jsonDirty = true;
      window.clearTimeout(jsonSyncTimer);
      jsonSyncTimer = window.setTimeout(() => {
        try {
          applyJSONDraftToVisual(true);
        } catch {
          updateStatusInline('JSON 尚未同步');
        }
      }, 450);
    });
    const behaviorJSON = overlay.querySelector('[data-role="behavior-json"]');
    if (behaviorJSON) behaviorJSON.addEventListener('input', e => {
      behaviorText = e.target.value;
      behaviorDirty = true; updateStatusInline('有未保存改动，点击「保存修改」后生效');
    });
    const validateJSONBtn = overlay.querySelector('[data-action="validate-json"]');
    if (validateJSONBtn) validateJSONBtn.addEventListener('click', () => {
      try {
        const parsed = parseRuleConfigDocument(jsonText);
        const warnings = validateRuleConfig(parsed.config);
        updateStatusInline(warnings.length ? 'JSON 可用，但存在规则提醒' : 'JSON 校验通过');
      } catch (err) {
        updateStatusInline(err.message || 'JSON 校验失败');
      }
    });
    const applyJSONBtn = overlay.querySelector('[data-action="apply-json"]');
    if (applyJSONBtn) applyJSONBtn.addEventListener('click', () => {
      try {
        applyJSONDraftToVisual(false);
        updateStatusInline('已应用 JSON 到当前预览，保存后才会持久生效');
      } catch (err) {
        updateStatusInline(err.message || 'JSON 应用失败');
      }
    });
    const validateBehaviorBtn = overlay.querySelector('[data-action="validate-behavior"]');
    if (validateBehaviorBtn) validateBehaviorBtn.addEventListener('click', () => {
      try {
        parseBehaviorDocument(behaviorText);
        updateStatusInline('代码化规则校验通过');
      } catch (err) {
        updateStatusInline(err.message || '代码化规则校验失败');
      }
    });
    const applyBehaviorBtn = overlay.querySelector('[data-action="apply-behavior"]');
    if (applyBehaviorBtn) applyBehaviorBtn.addEventListener('click', () => {
      try {
        workingBehavior = parseBehaviorDocument(behaviorText);
        syncBehaviorText();
        applyConfig(workingConfig, presetWithBehavior(selectedPreset()));
        updateStatusInline('已应用代码化规则到当前预览，保存后才会持久生效');
        renderMain();
      } catch (err) {
        updateStatusInline(err.message || '代码化规则应用失败');
      }
    });
    overlay.querySelector('[data-action="new"]').addEventListener('click', () => {
      draftPreset = {
        id: uniqueClientRuleID('新规则', doc),
        name: '新规则',
        description: '',
        config: normalizeRuleConfig(DEFAULT_RULE_CONFIG),
        behavior: normalizeRuleBehavior(DEFAULT_BEHAVIOR),
      };
      selectedID = draftPreset.id;
      workingConfig = normalizeRuleConfig(draftPreset.config);
      workingBehavior = normalizeRuleBehavior(draftPreset.behavior);
      workingName = draftPreset.name;
      workingDescription = draftPreset.description;
      workingAssets = defaultAssetLibraryData();
      workingBottomBlocks = bottomBlocksFromPreset(draftPreset);
      syncJSONText();
      syncBehaviorText();
      applyConfig(workingConfig, draftPreset);
      status = '已新建规则草稿，保存后才会持久生效';
      renderMain();
    });
    overlay.querySelectorAll('input[data-rule-key]').forEach(input => {
      input.addEventListener('input', () => {
        if (input.type === 'color') {
          const label = overlay.querySelector('[data-color-hex="' + input.dataset.ruleKey + '"]');
          if (label) label.textContent = input.value.toUpperCase();
        }
        updateWorkingField(input.dataset.ruleKey, input.value);
      });
    });
    overlay.querySelectorAll('.admin-toggle[data-rule-key]').forEach(btn => {
      btn.addEventListener('click', () => {
        const key = btn.dataset.ruleKey;
        const next = workingConfig[key] !== true;
        btn.classList.toggle('on', next);
        btn.setAttribute('aria-pressed', next ? 'true' : 'false');
        updateWorkingField(key, next);
      });
    });
    bindSteppers(overlay);
    applyLivePreview(overlay, workingConfig);
    async function savePresetWithFooter(preset) {
      const configDoc = buildRuleConfigDocument(preset.config, { name: preset.name, description: preset.description });
      const oc = (presetByID(doc, preset.id) || {}).config || {};
      const ocModules = oc.modules || {};
      configDoc.modules.asset_library = JSON.parse(JSON.stringify(workingAssets || ocModules.asset_library || defaultAssetLibraryData()));
      configDoc.modules.bottom_blocks = JSON.parse(JSON.stringify(workingBottomBlocks || ocModules.bottom_blocks || { enabled: false, order: [], blocks: {}, presets: [] }));
      if (isFtDefaultPack()) {
        // 保留 buildRuleConfigDocument 丢弃的网站结构化字段（顶层 image、mctoc、fbif_footer）
        if (oc.image) configDoc.image = oc.image;
        if (ocModules.mctoc) configDoc.modules.mctoc = ocModules.mctoc;
        configDoc.modules.fbif_footer = JSON.parse(JSON.stringify(workingFooter || defaultFooterData()));
      }
      const body = { id: preset.id, name: preset.name, description: preset.description, config: serverRuleConfigDocument(configDoc), behavior: preset.behavior };
      const resp = await fetch('/api/rules/admin/presets', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Admin-Password': String(password || '').trim() }, body: JSON.stringify(body) });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) throw new Error(data.error || ('HTTP ' + resp.status));
      return data;
    }
    overlay.querySelector('[data-action="save"]').addEventListener('click', () => {
      run(async () => {
        syncDirtyJSONBeforePersist();
        syncDirtyBehaviorBeforePersist();
        const selected = presetByID(doc, selectedID) || { id: selectedID };
        const name = workingName.trim() || selected.name || '未命名规则';
        const description = workingDescription.trim();
        doc = await savePresetWithFooter({
          id: selected.id,
          name,
          description,
          config: workingConfig,
          behavior: workingBehavior,
        });
        await setActiveRulePreset(password, selected.id, currentAccountID());
        doc = await listRulePresets(password);
        draftPreset = null;
        applyConfig(workingConfig, presetWithBehavior(presetByID(doc, selected.id)), { source: 'server' });
      }, '已保存到服务器');
    });
    overlay.querySelector('[data-action="save-as"]').addEventListener('click', () => {
      run(async () => {
        syncDirtyJSONBeforePersist();
        syncDirtyBehaviorBeforePersist();
        const name = workingName.trim() || '新规则';
        const id = uniqueClientRuleID(name, doc);
        const description = workingDescription.trim();
        doc = await savePresetWithFooter({ id, name, description, config: workingConfig, behavior: workingBehavior });
        await setActiveRulePreset(password, id, currentAccountID());
        doc = await listRulePresets(password);
        draftPreset = null;
        selectedID = id;
        applyConfig(workingConfig, presetWithBehavior(presetByID(doc, id)), { source: 'server' });
      }, '已复制为新规则，并应用到当前公众号');
    });
    overlay.querySelector('[data-action="set-active"]').addEventListener('click', () => run(async () => {
      if (draftPreset && draftPreset.id === selectedID) {
        throw new Error('新规则需要先保存');
      }
      const accountID = currentAccountID();
      await setActiveRulePreset(password, selectedID, accountID);
      doc = await listRulePresets(password);
      applyConfig(workingConfig, presetWithBehavior(presetByID(doc, selectedID)), { source: 'server' });
    }, '已应用到当前公众号'));
    const deleteBtn = overlay.querySelector('[data-action="delete"]');
    if (deleteBtn) deleteBtn.addEventListener('click', () => {
      const selectedName = workingName || '当前规则';
      if (!(draftPreset && draftPreset.id === selectedID)) {
        const ok = window.confirm('确定删除规则「' + selectedName + '」？删除后无法恢复，正在使用它的公众号会回到默认规则。');
        if (!ok) return;
      }
      run(async () => {
      if (draftPreset && draftPreset.id === selectedID) {
        draftPreset = null;
        selectedID = doc.active_id;
        const selected = activePresetFromDoc(doc);
        workingConfig = normalizeRuleConfig(selected ? selected.config : getConfig());
        workingBehavior = normalizeRuleBehavior(selected ? selected.behavior : getBehavior());
        workingName = selected && selected.name ? selected.name : '未命名规则';
        workingDescription = selected && selected.description ? selected.description : '';
        syncJSONText();
        syncBehaviorText();
        applyConfig(workingConfig, presetWithBehavior(selected), { source: 'server' });
        return;
      }
      doc = await deleteRulePreset(password, selectedID);
      draftPreset = null;
      selectedID = doc.active_id;
      const selected = activePresetFromDoc(doc);
      workingConfig = normalizeRuleConfig(selected ? selected.config : getConfig());
      workingBehavior = normalizeRuleBehavior(selected ? selected.behavior : getBehavior());
      workingName = selected && selected.name ? selected.name : '未命名规则';
      workingDescription = selected && selected.description ? selected.description : '';
      syncJSONText();
      syncBehaviorText();
      applyConfig(workingConfig, presetWithBehavior(selected), { source: 'server' });
      }, '已删除');
    });
  }

  function render() {
    if (!overlay) return;
    if (!password || !doc) renderLogin();
    else renderMain();
  }

  document.addEventListener('click', e => { const t = e.target && e.target.closest ? e.target : e.target && e.target.parentElement; if (t && t.closest('#adminLockBtn')) { e.preventDefault(); open(); } });
}
