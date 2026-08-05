import {
  DEFAULT_RULE_CONFIG,
  RULE_SCHEMA,
  buildRuleConfigDocument,
  deleteRulePreset,
  listRulePresets,
  normalizeRuleConfig,
  normalizeRuleBehaviorForRule,
  parseRuleConfigDocument,
  setActiveRulePreset,
  validateRuleConfig,
} from './rule-presets.js';
import { authFetch } from './auth-session.js';
import { DEFAULT_BEHAVIOR, validateBehaviorRegexDocument, validateRuleBehavior } from './rule-behavior.js';
import { createMoreArticlesRuleEditor } from './more-articles-rule-editor.js';
import { createFooterLibraryEditor } from './footer-library-rule-editor.js';
import { defaultFooterData, footerFromPreset } from './admin-footer-data.js';
import { createAssetLibraryEditor } from './asset-library-rule-editor.js';
import { assetLibraryFromPreset, defaultAssetLibraryData } from './admin-asset-data.js';
import { bottomBlocksFromPreset, createBlockEditorBinder, renderRuleBottomTabHtml } from './admin-block-data.js';
import { articleFooterDefaultsSeedForAccount, articleFooterDefaultsFromPreset, bindArticleFooterDefaultsEditor } from './admin-article-footer-defaults.js';
import { createBehaviorFormBinder } from './admin-behavior-data.js';
import { preserveUneditableRuleConfig, serverRuleConfigDocument } from './admin-rule-payload.js';
import { accountBindingRevision, applyPersistedRuleConfig, baselinePresetForNewRule, compatibleRulePresets, createAdminOperationGate, migrationBaselinePresets, presetForAccount, uniqueClientRuleID, uniqueClientRuleName } from './admin-rule-state.js';
import { syncRuleVisualEditor } from './admin-rule-visual-sync.js';
import { adminDirtyCount, updateDirtyChip } from './admin-dirty-state.js';
import { bindSteppers } from './admin-field-controls.js';
import { livePreviewHTML, applyLivePreview, targetForKey } from './admin-live-preview.js';
import { renderRuleSettingsGroups } from './admin-rule-settings-view.js';
import { bindRovingTabs, createAdminDialogA11y } from './admin-dialog-a11y.js';
import { RULE_PACKS } from './rule-packs/registry.js';
import { createAdminDeliveryController } from './admin-delivery.js';
import { createAdminAccessController, renderRuleAdminAccessDenied, renderRuleAdminAccessLoading } from './admin-access.js';
export { uniqueClientRuleID } from './admin-rule-state.js';
const ADMIN_ACCESS_HINT = '仅限已登录的超级管理员、管理员或已授权账号';
const UNSAVED_HINT = '有未保存改动，请使用底部保存按钮';
const SAVED_AFTER_CONTEXT_CHANGE = '规则已保存，但未应用：当前账号已变化；请重新打开后台确认', APPLIED_AFTER_CONTEXT_CHANGE = '规则已应用到原账号，但当前账号已变化；请重新打开后台确认', DELETED_AFTER_CONTEXT_CHANGE = '规则已在服务端删除，但当前账号已变化；请重新打开后台确认';
function esc(s) {
  return String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;')
    .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
function fieldValue(config, field) { return normalizeRuleConfig(config)[field.key]; }
function presetByID(doc, id) {
  return (doc && doc.presets || []).find(p => p.id === id) || null;
}
export function initAdminPanel(options = {}) {
  const button = document.getElementById('adminLockBtn');
  if (!button) return;
  const getConfig = options.getConfig || (() => (window._activeConfig && window._activeConfig.config) || {});
  const getPreset = options.getPreset || (() => (window._activeConfig && window._activeConfig.preset) || null);
  const getBehavior = options.getBehavior || (() => (window._activeConfig && window._activeConfig.behavior) || DEFAULT_BEHAVIOR);
  const normalizedBehavior = (raw, preset, accountID = currentAccountID()) => normalizeRuleBehaviorForRule(raw, { ...(preset || {}), rule_account_id: accountID });
  const applyConfig = options.applyConfig || ((config, preset) => {
    window._activeConfig = { config: normalizeRuleConfig(config), preset, behavior: normalizedBehavior((preset && preset.behavior) || workingBehavior, preset) };
  });
  const applyPersistedConfig = (config, preset, meta) => applyPersistedRuleConfig(applyConfig, config, preset, meta);
  const rulePacks = Array.isArray(options.rulePacks) ? options.rulePacks : [];
  const currentAuthUser = options.authUser || (typeof window !== 'undefined' && window.__AUTH_USER) || {};

  let overlay = null;
  let dialogA11y = null;
  let doc = null;
  let selectedID = '';
  let draftPreset = null;
  let workingConfig = normalizeRuleConfig(getConfig());
  let workingBehavior = normalizedBehavior(getBehavior(), getPreset());
  let workingFooter = null;
  let workingAssets = null;
  let workingBottomBlocks = null;
  let workingArticleFooterDefaults = null;
  const blockBinder = createBlockEditorBinder({ getModule: () => workingBottomBlocks || bottomBlocksFromPreset(selectedPreset()), setModule: (m) => { workingBottomBlocks = m; updateStatusInline(UNSAVED_HINT); }, getChannel: () => (isFtDefaultPack() ? 'ft' : 'mp'), getScope: () => currentAccountID(), getAssetLibrary: () => workingAssets || defaultAssetLibraryData(), addAsset: (asset) => { const lib = workingAssets || defaultAssetLibraryData(); workingAssets = { ...lib, assets: [asset, ...(Array.isArray(lib.assets) ? lib.assets : [])] }; updateStatusInline('已上传并入库，请使用底部保存按钮'); } });
  const behaviorBinder = createBehaviorFormBinder({ getBehavior: () => workingBehavior, setBehavior: (b) => { workingBehavior = b; syncBehaviorText(); const ta = overlay && overlay.querySelector('[data-role="behavior-json"]'); if (ta) ta.value = behaviorText; updateStatusInline(UNSAVED_HINT); } });
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
  let busyOperation = null, pendingOperationNotice = '';
  const operationGate = createAdminOperationGate(currentAccountID, () => overlay);
  const deliveryController = createAdminDeliveryController({ getOverlay: () => overlay, rerender: () => renderMain(), onMessage: text => { status = text || ''; const el = overlay && overlay.querySelector('.admin-foot .admin-status'); if (el) el.textContent = status; } });
  const accessController = createAdminAccessController({
    getOverlay: () => overlay,
    getAuthUser: () => currentAuthUser,
    rerender: () => renderMain(),
    onMessage: text => { status = text || ''; const el = overlay && overlay.querySelector('.admin-foot .admin-status'); if (el) el.textContent = status; },
  });
  function setStatus(text) { status = text || ''; render(); }
  async function refreshDoc(operation) {
    const nextDoc = operationGate.commit(operation, await listRulePresets());
    const selected = presetForAccount(nextDoc, operation.accountID);
    if (!selected) throw new Error('当前账号没有可用的规则绑定');
    doc = nextDoc;
    selectedID = selected.id;
    draftPreset = null;
    adoptWorkingPreset(selected, operation.accountID);
    operationGate.commit(operation, await Promise.resolve(applyConfig(workingConfig, presetWithBehavior(selected), { source: 'server' })));
  }
  function close() {
    if (!overlay) return;
    let dc = 0;
    try { dc = dirtyCount(); } catch { dc = 0; }
    if (dc > 0 && !window.confirm('有 ' + dc + ' 项未保存改动，关闭后不会应用到账号。确定关闭吗？')) return;
    operationGate.invalidate(); busy = false; busyOperation = null;
    deliveryController.reset();
    accessController.reset();
    window.clearTimeout(jsonSyncTimer);
    overlay.remove();
    overlay = null;
    dialogA11y?.release();
    dialogA11y = null;
  }
  function open() {
    if (overlay) return;
    operationGate.invalidate();
    deliveryController.reset();
    if (pendingOperationNotice) status = pendingOperationNotice;
    const opener = document.activeElement;
    overlay = document.createElement('div');
    overlay.className = 'admin-overlay';
    document.body.appendChild(overlay);
    dialogA11y = createAdminDialogA11y({ document, overlay, opener, onEscape: close });
    overlay.addEventListener('click', e => { if (e.target === overlay) close(); });
    const operation = operationGate.capture();
    busy = true;
    busyOperation = operation;
    status = '正在验证当前登录账号…';
    render();
    refreshDoc(operation).then(() => {
      if (!operationGate.current(operation)) return;
      busy = false;
      busyOperation = null;
      status = '';
      render();
    }).catch(err => {
      if (!operationGate.current(operation)) return;
      busy = false;
      busyOperation = null;
      doc = null;
      status = err.message || '当前账号没有进入规则后台的权限';
      render();
    });
  }

  async function run(action, okText) {
    if (busy) return;
    const operation = operationGate.capture();
    busyOperation = operation; busy = true; pendingOperationNotice = '';
    setStatus('处理中...');
    try {
      const resultText = operationGate.commit(operation, await action(operation));
      setStatus(typeof resultText === 'string' && resultText ? resultText : (okText || '已保存'));
    } catch (err) {
      if (err.adminOperationStale) {
        status = err.message || '操作已取消';
        if (err.adminOperationPersisted) pendingOperationNotice = status;
        const statusEl = overlay && overlay.querySelector('.admin-foot .admin-status');
        if (statusEl) statusEl.textContent = status;
      } else if (operationGate.current(operation)) setStatus(err.message || '操作失败');
    } finally {
      if (busyOperation === operation) { busy = false; busyOperation = null; if (operationGate.current(operation)) render(); }
    }
  }
  function renderAccessState() {
    const focusSelector = dialogA11y?.focusSelectorFor(document.activeElement) || '';
    overlay.innerHTML = renderRuleAdminAccessDenied(status, ADMIN_ACCESS_HINT);
    overlay.querySelector('.admin-close').addEventListener('click', close);
    dialogA11y?.focusAfterRender(focusSelector, '.admin-close');
  }
  function renderAccessLoading() {
    overlay.innerHTML = renderRuleAdminAccessLoading(ADMIN_ACCESS_HINT);
    overlay.querySelector('.admin-close').addEventListener('click', close);
    dialogA11y?.focusAfterRender('', '.admin-close');
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
          <button type="button" id="admin-rule-tab-${id}" role="tab" data-rule-tab="${id}" class="${activeRuleTab === id ? 'active' : ''}" aria-controls="admin-rule-panel-${id}" aria-selected="${activeRuleTab === id ? 'true' : 'false'}" tabindex="${activeRuleTab === id ? '0' : '-1'}">${label}</button>
        `).join('')}
      </div>`;
  }
  function selectedPreset() {
    return presetByID(doc, selectedID) ||
      (draftPreset && draftPreset.id === selectedID ? draftPreset : null) ||
      presetForAccount(doc, currentAccountID()) ||
      getPreset();
  }
  function adoptWorkingPreset(selected, accountID = currentAccountID()) {
    workingConfig = normalizeRuleConfig(selected ? selected.config : getConfig());
    workingBehavior = normalizedBehavior(selected ? selected.behavior : getBehavior(), selected, accountID);
    workingName = selected?.name || '未命名规则';
    workingDescription = selected?.description || '';
    workingFooter = footerFromPreset(selected);
    workingAssets = assetLibraryFromPreset(selected);
    workingBottomBlocks = bottomBlocksFromPreset(selected);
    workingArticleFooterDefaults = articleFooterDefaultsFromPreset(selected);
    syncJSONText(); syncBehaviorText();
  }
  function dirtyCount() { return adminDirtyCount({ draftPreset, selectedID, selected: selectedPreset(), workingName, workingDescription, workingConfig, workingBehavior, workingAssets, workingBottomBlocks, workingArticleFooterDefaults, workingFooter, isFtDefault: isFtDefaultPack(), accountID: currentAccountID() }); }
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
    if (isFtDefaultPack()) { if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new Error('网站行为规则必须是对象'); validateBehaviorRegexDocument(raw); return normalizedBehavior(raw, selectedPreset()); }
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

  function currentPack() {
    return RULE_PACKS[currentAccountID()] || visibleRulePacks().find(pack => pack.id === currentAccountID()) || {};
  }

  function currentAccountName() {
    return currentPack().accountName || currentAccountID();
  }

  function adminContextSubtitle() {
    const viewLabels = { assignments: '账号绑定', rules: '规则编辑', assets: '素材库', delivery: '发布链路', access: '权限管理' };
    return `${currentAccountName()} · ${viewLabels[activeView] || '规则管理'}`;
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
        channel: pack.channel === 'ft' ? 'ft' : 'mp',
        activeID: binding.active_id || binding.activeID || (pack.id === currentAccountID() && doc && doc.active_id) || pack.defaultPresetId || 'default',
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
        <button type="button" data-view="assignments" class="${activeView === 'assignments' ? 'active' : ''}"${activeView === 'assignments' ? ' aria-current="page"' : ''}>账号</button>
        <button type="button" data-view="rules" class="${activeView === 'rules' ? 'active' : ''}"${activeView === 'rules' ? ' aria-current="page"' : ''}>规则</button>
        <button type="button" data-view="assets" class="${activeView === 'assets' ? 'active' : ''}"${activeView === 'assets' ? ' aria-current="page"' : ''}>素材库</button>
        <button type="button" data-view="delivery" class="${activeView === 'delivery' ? 'active' : ''}"${activeView === 'delivery' ? ' aria-current="page"' : ''}>发布链路</button>
        ${accessController.canManage() ? `<button type="button" data-view="access" class="${activeView === 'access' ? 'active' : ''}"${activeView === 'access' ? ' aria-current="page"' : ''}>权限管理</button>` : ''}
      </nav>`;
  }

  function renderPresetOptions(activeID, accountID) {
    const pack = RULE_PACKS[accountID];
    const presets = compatibleRulePresets((doc && doc.presets) || [], pack && pack.templateId);
    return presets.map(p => {
      const activeMark = p.id === activeID ? ' · 正在使用' : '';
      const migrationMark = p.migration_required ? ' · 需确认基线' : '';
      return `<option value="${esc(p.id)}" ${p.id === activeID ? 'selected' : ''} ${p.migration_required ? 'disabled' : ''}>${esc(p.name + activeMark + migrationMark)}</option>`;
    }).join('');
  }

  function currentAccountID() { return (typeof window !== 'undefined' && window._activeRulePack && window._activeRulePack.id) || 'fbif'; }

  function defaultArticleFooterDefaultsForCurrentAccount() { return articleFooterDefaultsSeedForAccount(currentAccountID()); }

  function renderAssignments() {
    const bindings = accountBindings();
    const groups = [
      ['mp', '公众号'],
      ['ft', '网站'],
    ];
    return `
      <section class="admin-assignment-panel">
        <div class="admin-section-heading">
          <h3>账号绑定</h3>
          <p class="admin-panel-note">为每个排版账号选择默认规则。切换会立即保存并生效，也可以随时改回。</p>
        </div>
        ${groups.map(([channel, label]) => {
          const accounts = bindings.filter(account => account.channel === channel);
          if (!accounts.length) return '';
          return `<section class="admin-account-group" aria-labelledby="admin-account-group-${channel}">
            <h4 id="admin-account-group-${channel}">${label}<span>${accounts.length}</span></h4>
            <div class="admin-account-list">${accounts.map(account => `
              <label class="admin-account-row">
                <span class="admin-account-copy"><strong>${esc(account.name)}</strong>${account.id === currentAccountID() ? '<small>当前页面</small>' : ''}</span>
                <select data-account-rule="${esc(account.id)}" aria-label="${esc(account.name)}使用的规则" ${busy ? 'disabled' : ''}>
                  ${renderPresetOptions(account.activeID, account.id)}
                </select>
              </label>
            `).join('')}</div>
          </section>`;
        }).join('')}
      </section>`;
  }

  function renderAssetsLibraryView() {
    return `
      <section class="admin-rule-group admin-assets-view">
        <div class="admin-section-heading">
          <h3>素材库</h3>
          <p class="admin-panel-note">默认显示「${esc(currentAccountName())}」可用的账号素材和共享素材；需要时可筛选其他范围。</p>
        </div>
        <div data-role="asset-library-editor"></div>
      </section>`;
  }

  function renderRuleTabContent() {
    if (activeRuleTab === 'bottom') {
      return renderRuleBottomTabHtml(isFtDefaultPack());
    }
    return `
      <section class="admin-visual-zone" aria-label="前端修改区">
        <div class="admin-form-col">
          <p class="admin-panel-note admin-rule-help">展开需要调整的分组。修改会先反映在右侧样例，保存并应用后才会更新当前账号。</p>
          <div data-role="rule-warnings">${renderWarningsHTML()}</div>
          ${renderRuleSettingsGroups(workingConfig, RULE_SCHEMA)}
        </div>
        ${livePreviewHTML()}
      </section>`;
  }

  function renderRulesEditor(optionsHTML) {
    const isDefaultRule = selectedID === 'default';
    const migrationRule = selectedPreset()?.migration_required === true;
    const activeRuleID = accountBindings().find(account => account.id === currentAccountID())?.activeID || '';
    const selectedIsApplied = selectedID === activeRuleID;
    const disabled = busy ? 'disabled' : '';
    const baselineOptions = migrationBaselinePresets((doc && doc.presets) || [], RULE_PACKS[currentAccountID()]?.templateId).map(p =>
      `<option value="${esc(p.id)}">${esc(p.name)}（${esc(p.rule_family || p.channel || p.id)}）</option>`
    ).join('');
    return `
      <div class="admin-section-heading admin-rule-heading">
        <h3>规则编辑</h3>
        <p class="admin-panel-note">当前账号：${esc(currentAccountName())}。先选择规则，再调整参数或底部结构。</p>
      </div>
      <div class="admin-rule-bar">
        <label>
          <span>正在编辑</span>
          <span class="admin-preset-control">
            <select data-role="preset-select" ${disabled}>${optionsHTML}</select>
            <button type="button" class="admin-icon-danger" data-action="delete" ${isDefaultRule || busy ? 'disabled' : ''} title="删除当前规则" aria-label="删除当前规则">
              <svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true"><path d="M9 4h6l1 2h4" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/><path d="M4 6h16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/><path d="M7 9l1 10h8l1-10" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"/><path d="M10 11v6M14 11v6" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>
            </button>
          </span>
        </label>
        <div class="admin-rule-actions" aria-label="规则操作">
          <button type="button" class="admin-secondary" data-action="new" ${migrationRule || busy ? 'disabled' : ''}>新建规则</button>
          <button type="button" class="admin-secondary" data-action="save-as" ${migrationRule || busy ? 'disabled' : ''}>复制当前规则</button>
          ${selectedIsApplied || migrationRule ? '' : `<button type="button" class="admin-secondary" data-action="set-active" ${disabled}>应用这条规则</button>`}
        </div>
      </div>
      ${renderApplicationBadges(selectedID)}
      ${migrationRule ? `<section class="admin-rule-group">
        <h3>确认历史规则基线</h3>
        <p class="admin-panel-note">${esc(selectedPreset()?.migration_note || '历史数据无法推断唯一基线')}。本次只确认基线；确认后再编辑并保存规则。</p>
        <label class="admin-field"><span>规则基线</span><select data-role="migration-baseline"><option value="">请选择…</option>${baselineOptions}</select></label>
      </section>` : `<section class="admin-meta-section">
        <h3>基本信息</h3>
        <div class="admin-meta-grid">
          <label>
            <span>规则名称</span>
            <input type="text" data-role="preset-name" value="${esc(workingName)}" ${disabled}>
          </label>
          <label>
            <span>说明</span>
            <input type="text" data-role="preset-description" value="${esc(workingDescription)}" ${disabled}>
          </label>
        </div>
      </section>
      ${renderRuleTabs()}
      <div id="admin-rule-panel-${activeRuleTab}" role="tabpanel" aria-labelledby="admin-rule-tab-${activeRuleTab}">
        ${renderRuleTabContent()}
      </div>`}`;
  }

  function renderMain() {
    const focusSelector = dialogA11y?.focusSelectorFor(document.activeElement) || '';
    const presets = compatibleRulePresets((doc && doc.presets) || [], RULE_PACKS[currentAccountID()]?.templateId);
    if (!jsonDirty) syncJSONText();
    const listPresets = draftPreset && !presetByID(doc, draftPreset.id)
      ? presets.concat([draftPreset])
      : presets;
    const currentActiveID = accountBindings().find(account => account.id === currentAccountID())?.activeID || '';
    const optionsHTML = listPresets.map(p => {
      const activeMark = p.id === currentActiveID ? ' · 正在使用' : '';
      const migrationMark = p.migration_required ? ' · 需确认基线' : '';
      return `<option value="${esc(p.id)}" ${p.id === selectedID ? 'selected' : ''}>${esc(p.name + activeMark + migrationMark)}</option>`;
    }).join('');
    const contentHTML = activeView === 'assignments'
      ? renderAssignments()
      : activeView === 'assets'
      ? renderAssetsLibraryView()
      : activeView === 'delivery' ? deliveryController.render()
      : activeView === 'access' && accessController.canManage() ? accessController.render()
      : renderRulesEditor(optionsHTML);
    const changes = dirtyCount();
    const tracksDraft = activeView === 'rules' || activeView === 'assets';
    const showPrimarySave = activeView === 'rules' || activeView === 'assets';
    const deliveryFooter = activeView === 'delivery' ? deliveryController.footerState() : null;
    const migrationRule = selectedPreset()?.migration_required === true;
    const saveLabel = migrationRule ? '确认基线' : activeView === 'assets' ? '保存素材库' : '保存并应用';
    const footerMessage = deliveryFooter ? deliveryFooter.message : pendingOperationNotice || status;
    const footerAction = deliveryFooter
      ? `<button type="button" class="admin-primary admin-save-primary" data-delivery-action="save" ${deliveryFooter.disabled ? 'disabled' : ''}>${esc(deliveryFooter.label)}</button>`
      : showPrimarySave
        ? `<button type="button" class="admin-primary admin-save-primary" data-action="save" ${busy || (activeView === 'assets' && migrationRule) ? 'disabled' : ''}>${busy ? '保存中…' : saveLabel}</button>`
        : '';
    overlay.innerHTML = `
      <div class="admin-dialog" role="dialog" aria-modal="true" aria-labelledby="adminTitle">
        <div class="admin-head">
          <div>
            <div class="admin-title" id="adminTitle">规则后台</div>
            <div class="admin-subtitle">${esc(adminContextSubtitle())}</div>
          </div>
          <button type="button" class="admin-close" aria-label="关闭">×</button>
        </div>
        <div class="admin-layout">
          ${renderSideNav()}
          <div class="admin-body">${contentHTML}</div>
        </div>
        <div class="admin-foot">
          <div class="admin-status" role="status" aria-live="polite">${esc(footerMessage)}</div>
          ${tracksDraft ? `<span class="admin-dirty-chip ${changes ? 'dirty' : ''}" data-role="dirty-chip">${changes ? changes + ' 项未保存' : '已保存'}</span>` : ''}
          ${footerAction}
        </div>
      </div>`;
    bindMain();
    dialogA11y?.focusAfterRender(focusSelector, `[data-view="${activeView}"]`);
  }

  function updateWorkingField(key, value) {
    workingConfig = normalizeRuleConfig({ ...workingConfig, [key]: value });
    const selected = selectedPreset();
    jsonDirty = false;
    applyConfig(workingConfig, presetWithBehavior(selected));
    applyLivePreview(overlay, workingConfig, { tween: true, target: targetForKey(key) });
    refreshWarnings();
    syncJSONEditorFromInputs(); updateStatusInline(UNSAVED_HINT);
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
        syncJSONEditorFromInputs(); updateStatusInline(UNSAVED_HINT);
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
      onChange(next) { workingFooter = next; updateStatusInline(UNSAVED_HINT); },
    });
  }

  function bindAssetLibraryEditor() {
    const mount = overlay && overlay.querySelector('[data-role="asset-library-editor"]');
    if (!mount) return;
    if (assetEditorInstance) { assetEditorInstance.destroy(); assetEditorInstance = null; }
    assetEditorInstance = createAssetLibraryEditor({ container: mount, assetLibrary: workingAssets || defaultAssetLibraryData(), referencePresets: (doc && doc.presets) || [], currentScope: currentAccountID(), compact: activeView !== 'assets', onChange(next) { workingAssets = next; updateStatusInline(UNSAVED_HINT); } });
  }

  function bindArticleFooterDefaultsEditorPanel() {
    bindArticleFooterDefaultsEditor({
      overlay,
      getModule: () => workingArticleFooterDefaults || articleFooterDefaultsFromPreset(selectedPreset()),
      setModule(next) {
        workingArticleFooterDefaults = next;
        updateStatusInline(UNSAVED_HINT);
      },
      getBottomBlocks: () => workingBottomBlocks || bottomBlocksFromPreset(selectedPreset()),
    });
  }

  async function savePresetWithFooter(preset, operation) {
    operationGate.commit(operation);
    let configDoc = buildRuleConfigDocument(preset.config, { name: preset.name, description: preset.description });
    const sourcePreset = presetByID(doc, preset.id) || (draftPreset && draftPreset.id === preset.id ? draftPreset : null) || presetByID(doc, selectedID) || {};
    const oldConfig = sourcePreset.config || {};
    const oldModules = oldConfig.modules || {};
    configDoc.modules.asset_library = JSON.parse(JSON.stringify(workingAssets || oldModules.asset_library || defaultAssetLibraryData()));
    configDoc.modules.bottom_blocks = JSON.parse(JSON.stringify(workingBottomBlocks || oldModules.bottom_blocks || { schema_version: 2, enabled: false, presets: [] }));
    const articleFooterDefaults = workingArticleFooterDefaults || oldModules.article_footer_defaults || articleFooterDefaultsSeedForAccount(operation.accountID);
    if (articleFooterDefaults) configDoc.modules.article_footer_defaults = JSON.parse(JSON.stringify(articleFooterDefaults));
    configDoc = preserveUneditableRuleConfig(configDoc, oldConfig);
    if (RULE_PACKS[operation.accountID]?.templateId === 'foodtalks-site') {
      configDoc.modules.fbif_footer = JSON.parse(JSON.stringify(workingFooter || defaultFooterData()));
    }
    const persistedPreset = presetByID(doc, preset.id), baseRevision = Number(persistedPreset && persistedPreset.version);
    const body = { id: preset.id, name: preset.name, description: preset.description, baseline_id: preset.baseline_id || sourcePreset.baseline_id, migration_confirmed: preset.migration_confirmed === true, base_revision: Number.isInteger(baseRevision) && baseRevision >= 0 ? baseRevision : 0, config: serverRuleConfigDocument(configDoc), behavior: preset.behavior };
    const resp = await authFetch('/api/rules/admin/presets', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) throw new Error(data.error || ('HTTP ' + resp.status));
    return data;
  }

  function bindPrimarySave() {
    const saveButton = overlay.querySelector('[data-action="save"]');
    if (!saveButton) return;
    saveButton.addEventListener('click', () => {
      const migrationBaselineID = overlay.querySelector('[data-role="migration-baseline"]')?.value || '';
      const targetName = currentAccountName();
      run(async operation => {
        const selected = selectedPreset() || { id: selectedID };
        if (selected.migration_required) {
          if (!migrationBaselineID) throw new Error('请先选择并确认历史规则基线');
          doc = operationGate.commit(operation, await savePresetWithFooter({ ...selected, baseline_id: migrationBaselineID, migration_confirmed: true, config: workingConfig, behavior: workingBehavior }, operation), SAVED_AFTER_CONTEXT_CHANGE);
          await refreshDoc(operation);
          return '基线已确认；现在可以编辑并再次保存规则';
        }
        syncDirtyJSONBeforePersist();
        syncDirtyBehaviorBeforePersist();
        const savedConfig = workingConfig, savedBehavior = workingBehavior;
        const name = workingName.trim() || selected.name || '未命名规则';
        const description = workingDescription.trim();
        doc = operationGate.commit(operation, await savePresetWithFooter({ id: selected.id, name, description, baseline_id: selected.baseline_id, config: savedConfig, behavior: savedBehavior }, operation), SAVED_AFTER_CONTEXT_CHANGE);
        operationGate.commit(operation, await setActiveRulePreset(selected.id, operation.accountID, accountBindingRevision(doc, operation.accountID)), APPLIED_AFTER_CONTEXT_CHANGE);
        doc = operationGate.commit(operation, await listRulePresets(), APPLIED_AFTER_CONTEXT_CHANGE);
        draftPreset = null;
        const persisted = presetByID(doc, selected.id);
        adoptWorkingPreset(persisted, operation.accountID);
        return operationGate.commit(operation, await applyPersistedConfig(workingConfig, presetWithBehavior(persisted), { source: 'server' }), APPLIED_AFTER_CONTEXT_CHANGE);
      }, activeView === 'assets' ? `素材库已保存到「${targetName}」` : `已保存并应用到「${targetName}」`);
    });
  }

  function bindMain() {
    overlay.querySelector('.admin-close').addEventListener('click', close);
    overlay.querySelectorAll('[data-view]').forEach(btn => {
      btn.addEventListener('click', () => {
        activeView = btn.dataset.view;
        renderMain();
      });
    });
    if (activeView === 'access' && accessController.canManage()) { accessController.bind(); return; }
    bindRovingTabs([...overlay.querySelectorAll('[data-rule-tab]')], btn => {
      if (footerEditorInstance) { footerEditorInstance.destroy(); footerEditorInstance = null; }
      if (assetEditorInstance) { assetEditorInstance.destroy(); assetEditorInstance = null; }
      blockBinder.destroy();
      behaviorBinder.destroy();
      activeRuleTab = btn.dataset.ruleTab;
      renderMain();
    });
    overlay.querySelectorAll('[data-account-rule]').forEach(select => {
      select.addEventListener('change', () => {
        const accountID = select.dataset.accountRule;
        const ruleID = select.value;
        run(async operation => {
          doc = operationGate.commit(operation, await setActiveRulePreset(ruleID, accountID, accountBindingRevision(doc, accountID)), '规则绑定已在服务端更新，但当前账号已变化；请重新打开后台确认');
          const selected = presetByID(doc, ruleID);
          if (accountID === operation.accountID) {
            selectedID = ruleID;
            adoptWorkingPreset(selected, accountID);
            return operationGate.commit(operation, await applyPersistedConfig(workingConfig, presetWithBehavior(selected), { source: 'server' }), '规则绑定已在服务端更新，但当前账号已变化；请重新打开后台确认');
          }
        }, `已更新「${RULE_PACKS[accountID]?.accountName || accountID}」使用的规则`);
      });
    });
    bindPrimarySave();
    if (activeView === 'delivery') { deliveryController.bind(); return; }
    if (activeView === 'assets') { bindAssetLibraryEditor(); return; }
    if (activeView !== 'rules') return;
    bindMoreArticlesRuleEditor();
    bindFooterLibraryEditor();
    bindAssetLibraryEditor();
    blockBinder.bind(overlay);
    bindArticleFooterDefaultsEditorPanel();
    behaviorBinder.bind(overlay);
    overlay.querySelector('[data-role="preset-select"]').addEventListener('change', e => {
      selectedID = e.target.value;
      const selected = selectedPreset();
      adoptWorkingPreset(selected);
      applyConfig(workingConfig, presetWithBehavior(selected));
      status = `仅在预览中查看，未改变「${currentAccountName()}」正在使用的规则`;
      renderMain();
    });
    overlay.querySelector('[data-role="preset-name"]')?.addEventListener('input', e => {
      workingName = e.target.value;
      jsonDirty = false;
      syncJSONEditorFromInputs(); updateStatusInline(UNSAVED_HINT);
    });
    overlay.querySelector('[data-role="preset-description"]')?.addEventListener('input', e => {
      workingDescription = e.target.value;
      jsonDirty = false;
      syncJSONEditorFromInputs(); updateStatusInline(UNSAVED_HINT);
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
      behaviorDirty = true; updateStatusInline(UNSAVED_HINT);
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
      const current = selectedPreset();
      const seed = baselinePresetForNewRule(doc, current);
      const draftName = uniqueClientRuleName('新规则', doc);
      draftPreset = {
        id: uniqueClientRuleID(draftName, doc),
        name: draftName,
        description: '',
        baseline_id: current?.baseline_id,
        channel: RULE_PACKS[currentAccountID()]?.channel,
        rule_family: RULE_PACKS[currentAccountID()]?.templateId,
        config: seed?.config || {},
        behavior: normalizedBehavior(seed?.behavior || {}, seed),
      };
      selectedID = draftPreset.id;
      workingConfig = normalizeRuleConfig(draftPreset.config);
      workingBehavior = normalizedBehavior(draftPreset.behavior, draftPreset);
      workingName = draftPreset.name;
      workingDescription = draftPreset.description;
      const baseline = seed || draftPreset;
      workingFooter = footerFromPreset(baseline);
      workingAssets = assetLibraryFromPreset(baseline);
      workingBottomBlocks = bottomBlocksFromPreset(baseline);
      workingArticleFooterDefaults = articleFooterDefaultsFromPreset(baseline) || defaultArticleFooterDefaultsForCurrentAccount();
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
    overlay.querySelector('[data-action="save-as"]').addEventListener('click', () => {
      run(async operation => {
        syncDirtyJSONBeforePersist();
        syncDirtyBehaviorBeforePersist();
        const name = uniqueClientRuleName(workingName.trim() || '新规则', doc);
        const id = uniqueClientRuleID(name, doc);
        const description = workingDescription.trim();
        const savedConfig = workingConfig, savedBehavior = workingBehavior;
        doc = operationGate.commit(operation, await savePresetWithFooter({ id, name, description, baseline_id: selectedPreset()?.baseline_id, config: savedConfig, behavior: savedBehavior }, operation), SAVED_AFTER_CONTEXT_CHANGE);
        operationGate.commit(operation, await setActiveRulePreset(id, operation.accountID, accountBindingRevision(doc, operation.accountID)), APPLIED_AFTER_CONTEXT_CHANGE);
        doc = operationGate.commit(operation, await listRulePresets(), APPLIED_AFTER_CONTEXT_CHANGE);
        draftPreset = null;
        selectedID = id;
        const persisted = presetByID(doc, id);
        adoptWorkingPreset(persisted, operation.accountID);
        return operationGate.commit(operation, await applyPersistedConfig(workingConfig, presetWithBehavior(persisted), { source: 'server' }), APPLIED_AFTER_CONTEXT_CHANGE);
      }, `已复制为新规则，并应用到「${currentAccountName()}」`);
    });
    overlay.querySelector('[data-action="set-active"]')?.addEventListener('click', () => run(async operation => {
      if (draftPreset && draftPreset.id === selectedID) {
        throw new Error('新规则需要先保存');
      }
      const ruleID = selectedID;
      operationGate.commit(operation, await setActiveRulePreset(ruleID, operation.accountID, accountBindingRevision(doc, operation.accountID)), APPLIED_AFTER_CONTEXT_CHANGE);
      doc = operationGate.commit(operation, await listRulePresets(), APPLIED_AFTER_CONTEXT_CHANGE);
      const persisted = presetByID(doc, ruleID);
      adoptWorkingPreset(persisted, operation.accountID);
      return operationGate.commit(operation, await applyPersistedConfig(workingConfig, presetWithBehavior(persisted), { source: 'server' }), APPLIED_AFTER_CONTEXT_CHANGE);
    }, `已应用到「${currentAccountName()}」`));
    const deleteBtn = overlay.querySelector('[data-action="delete"]');
    if (deleteBtn) deleteBtn.addEventListener('click', () => {
      const selectedName = workingName || '当前规则';
      if (!(draftPreset && draftPreset.id === selectedID)) {
        const ok = window.confirm('确定删除规则「' + selectedName + '」？删除后无法恢复，正在使用它的账号会回到默认规则。');
        if (!ok) return;
      }
      run(async operation => {
      if (draftPreset && draftPreset.id === selectedID) {
        draftPreset = null;
        const selected = presetForAccount(doc, operation.accountID);
        selectedID = selected && selected.id;
        adoptWorkingPreset(selected, operation.accountID);
        return operationGate.commit(operation, await applyPersistedConfig(workingConfig, presetWithBehavior(selected), { source: 'server' }));
      }
      const deletedID = selectedID;
      doc = operationGate.commit(operation, await deleteRulePreset(deletedID), DELETED_AFTER_CONTEXT_CHANGE);
      draftPreset = null;
      const selected = presetForAccount(doc, operation.accountID);
      selectedID = selected && selected.id;
      adoptWorkingPreset(selected, operation.accountID);
      return operationGate.commit(operation, await applyPersistedConfig(workingConfig, presetWithBehavior(selected), { source: 'server' }), DELETED_AFTER_CONTEXT_CHANGE);
      }, '已删除');
    });
  }

  function render() {
    if (!overlay) return;
    if (busy && !doc) renderAccessLoading();
    else if (!doc) renderAccessState();
    else renderMain();
  }

  document.addEventListener('click', e => { const t = e.target && e.target.closest ? e.target : e.target && e.target.parentElement; if (t && t.closest('#adminLockBtn')) { e.preventDefault(); open(); } });
}
