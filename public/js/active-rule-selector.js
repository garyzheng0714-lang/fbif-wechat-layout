// active-rule-selector.js — 顶部「当前规则」选择器（外壳共享模块，架构体检 S1）。
//
// 此前这一族 ~17 个函数在 app.html 与 site.html 各有一份复制粘贴（部分已开始分叉），
// 修一个 bug 要改两处、漏一处即两页行为分叉。现收敛为唯一实现，渠道差异全部经
// options 注入（ADR 四层模型 L4：操作交互全渠道统一，差异只允许是数据/钩子）：
//   - readonlyLabel / readonlyEditEntry：只读态视图口径（公众号纯展示；网站带
//     「已锁定」徽章 + 铅笔编辑入口）
//   - fallbackTransform：server 预设落本地缓存前的变换（公众号走 structuredFallbackConfig）
//   - afterHydrate：预设水合后的渠道钩子（公众号刷新账号能力 UI）
// 契约测试：tests/active-rule-selector.test.mjs（linkedom 行为测试——这一族此前零行为覆盖）。

import { rulePreviewSavedMessage } from './rule-preview-sync.js';

export function escText(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

export function createActiveRuleSelector({
  documentRef,
  windowRef,
  getPack,
  ruleModule,
  refreshRuleConsumers,
  fallbackTransform = (config) => config,
  afterHydrate = null,
  readonlyLabel = '当前排版规则',
  readonlyEditEntry = false,
  initialSource = 'default',
  initialPresetID = 'default',
}) {
  const {
    loadActiveRulePreset,
    loadLocalRuleConfig,
    loadLocalRulePreset,
    saveLocalRuleConfig,
    saveLocalRulePreset,
    applyActiveRuleConfig,
    adminLogin,
    listPublicRulePresets,
    setActiveRulePreset,
  } = ruleModule;

  let activeRuleSource = initialSource;
  let activeRuleCatalog = { presets: [] };
  let activeRuleSelectorEditing = false;
  let activeRuleSelectorOpen = false;
  let activeRuleSelectorDraftID = initialPresetID || 'default';
  let activeRuleSelectorStatus = '';
  let activeRuleAdminPassword = '';
  let activeRuleOutsideHandler = null;
  let accountHydrationGeneration = 0;
  let selectorUIGeneration = 0;
  let catalogGeneration = 0;
  let activeRuleSavePromise = null;
  let activeRuleSaveToken = null;
  let activeRuleSaving = false;

  function invalidateSelectorInteractions() {
    selectorUIGeneration++;
    catalogGeneration++;
    activeRuleSavePromise = null;
    activeRuleSaveToken = null;
    activeRuleSaving = false;
    activeRuleSelectorEditing = false;
    activeRuleSelectorOpen = false;
  }

  function currentPresetFromConfig() {
    return (windowRef._activeConfig && windowRef._activeConfig.preset) || {};
  }

  function cacheServerRuleFallback(config, preset, accountID = getPack().id) {
    const fallbackConfig = fallbackTransform(config, preset);
    if (typeof saveLocalRulePreset === 'function') {
      saveLocalRulePreset({ ...(preset || {}), config: fallbackConfig }, accountID);
    } else {
      saveLocalRuleConfig(fallbackConfig, accountID);
    }
  }

  function localActiveRulePreset(accountID) {
    if (typeof loadLocalRulePreset === 'function') {
      const preset = loadLocalRulePreset(accountID);
      if (preset) return preset;
    }
    return {
      id: 'local',
      name: '本机设置',
      rule_account_id: accountID,
      config: loadLocalRuleConfig(accountID),
    };
  }

  async function loadRuleCatalog() {
    try {
      const data = await listPublicRulePresets();
      return { presets: Array.isArray(data.presets) ? data.presets : [] };
    } catch {
      const preset = currentPresetFromConfig();
      return { presets: preset.id ? [{ id: preset.id, name: preset.name || preset.id }] : [] };
    }
  }

  async function ensureRuleAdminPassword() {
    if (activeRuleAdminPassword) return activeRuleAdminPassword;
    const input = windowRef.prompt('请输入规则后台密码');
    if (!input) throw new Error('需要规则后台密码');
    activeRuleAdminPassword = input.trim();
    await adminLogin(activeRuleAdminPassword);
    return activeRuleAdminPassword;
  }

  function ruleSelectorOptions(currentPreset) {
    const seen = new Set();
    const rules = [];
    const ruleFamily = getPack().templateId;
    for (const rule of activeRuleCatalog.presets || []) {
      if (!rule || !rule.id || seen.has(rule.id)) continue;
      if (rule.migration_required) continue;
      if (ruleFamily && rule.rule_family !== ruleFamily) continue;
      seen.add(rule.id);
      rules.push({ id: rule.id, name: rule.name || rule.id });
    }
    if (currentPreset && currentPreset.id && !seen.has(currentPreset.id)) {
      rules.unshift({ id: currentPreset.id, name: currentPreset.name || currentPreset.id });
    }
    return rules.length ? rules : [{ id: 'default', name: '默认规则' }];
  }

  function clearActiveRuleOutsideHandler() {
    if (!activeRuleOutsideHandler) return;
    documentRef.removeEventListener('pointerdown', activeRuleOutsideHandler);
    activeRuleOutsideHandler = null;
  }

  function syncActiveRuleOutsideHandler(card) {
    clearActiveRuleOutsideHandler();
    if (!activeRuleSelectorOpen) return;
    activeRuleOutsideHandler = event => {
      if (card.contains(event.target)) return;
      activeRuleSelectorOpen = false;
      renderActiveRuleCard();
    };
    setTimeout(() => documentRef.addEventListener('pointerdown', activeRuleOutsideHandler), 0);
  }

  function toggleActiveRuleMenu() {
    if (!activeRuleSelectorEditing || activeRuleSaving) return;
    activeRuleSelectorOpen = !activeRuleSelectorOpen;
    activeRuleSelectorStatus = '';
    renderActiveRuleCard();
  }

  function chooseActiveRuleOption(ruleID) {
    if (!ruleID || activeRuleSaving) return;
    activeRuleSelectorDraftID = ruleID;
    activeRuleSelectorOpen = false;
    activeRuleSelectorStatus = '';
    renderActiveRuleCard();
  }

  function handleActiveRuleOptionKeydown(event, currentButton) {
    const options = Array.from(documentRef.querySelectorAll('[data-rule-option]'));
    const index = options.indexOf(currentButton);
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      const direction = event.key === 'ArrowDown' ? 1 : -1;
      const next = options[(index + direction + options.length) % options.length];
      if (next) next.focus();
    }
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      chooseActiveRuleOption(currentButton.getAttribute('data-rule-option'));
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      activeRuleSelectorOpen = false;
      renderActiveRuleCard();
      const trigger = documentRef.getElementById('activeRuleSelect');
      if (trigger) trigger.focus();
    }
  }

  async function beginActiveRuleEdit() {
    if (activeRuleSaving) return false;
    const accountID = getPack().id;
    const generation = ++selectorUIGeneration;
    const catalogAtStart = ++catalogGeneration;
    const preset = currentPresetFromConfig();
    activeRuleSelectorDraftID = preset.id || 'default';
    activeRuleSelectorStatus = '';
    const catalog = await loadRuleCatalog();
    if (generation !== selectorUIGeneration || catalogAtStart !== catalogGeneration || accountID !== getPack().id) return false;
    activeRuleCatalog = catalog;
    activeRuleSelectorEditing = true;
    activeRuleSelectorOpen = false;
    renderActiveRuleCard();
    const trigger = documentRef.getElementById('activeRuleSelect');
    if (trigger) trigger.focus();
    return true;
  }

  function cancelActiveRuleEdit() {
    if (activeRuleSaving) return false;
    invalidateSelectorInteractions();
    const preset = currentPresetFromConfig();
    activeRuleSelectorDraftID = preset.id || 'default';
    activeRuleSelectorStatus = '';
    activeRuleSelectorEditing = false;
    activeRuleSelectorOpen = false;
    renderActiveRuleCard();
    return true;
  }

  async function saveActiveRuleSelection() {
    if (activeRuleSavePromise) return activeRuleSavePromise;
    const preset = currentPresetFromConfig();
    const nextID = activeRuleSelectorDraftID || preset.id || 'default';
    if (nextID === preset.id) {
      activeRuleSelectorEditing = false;
      activeRuleSelectorOpen = false;
      renderActiveRuleCard();
      return true;
    }
    const accountID = getPack().id;
    const generation = ++selectorUIGeneration;
    const catalogAtStart = ++catalogGeneration;
    const saveToken = {};
    activeRuleSaveToken = saveToken;
    activeRuleSaving = true;
    const stale = () => generation !== selectorUIGeneration ||
      accountID !== getPack().id ||
      activeRuleSaveToken !== saveToken;
    activeRuleSelectorStatus = '保存中...';
    activeRuleSelectorOpen = false;
    renderActiveRuleCard();
    const task = (async () => {
      try {
        const password = await ensureRuleAdminPassword();
        if (stale()) return false;
        await setActiveRulePreset(password, nextID, accountID);
        if (stale()) return false;
        const [loaded, catalog] = await Promise.all([
          loadActiveRulePreset(accountID),
          loadRuleCatalog(),
        ]);
        if (stale()) return false;
        activeRuleSource = loaded.source;
        applyActiveRuleConfig(loaded.preset.config, loaded.preset);
        if (activeRuleSource === 'server') cacheServerRuleFallback(loaded.preset.config, loaded.preset, accountID);
        if (catalogAtStart === catalogGeneration) activeRuleCatalog = catalog;
        const previewSync = await Promise.resolve(refreshRuleConsumers({ rerender: true }));
        if (stale()) return false;
        activeRuleSelectorEditing = false;
        activeRuleSelectorOpen = false;
        activeRuleSelectorDraftID = loaded.preset.id || nextID;
        activeRuleSelectorStatus = rulePreviewSavedMessage(previewSync);
        activeRuleSaving = false;
        renderActiveRuleCard();
        return true;
      } catch (err) {
        if (/password|密码|401|unauthorized/i.test(String(err && err.message))) activeRuleAdminPassword = '';
        if (stale()) return false;
        activeRuleSelectorStatus = err.message || '保存失败';
        activeRuleSaving = false;
        renderActiveRuleCard();
        return false;
      }
    })();
    activeRuleSavePromise = task;
    try {
      return await task;
    } finally {
      if (activeRuleSaveToken === saveToken) {
        activeRuleSavePromise = null;
        activeRuleSaveToken = null;
        activeRuleSaving = false;
      }
    }
  }

  async function hydrateActiveRulePreset(accountID = getPack().id) {
    invalidateSelectorInteractions();
    const generation = ++accountHydrationGeneration;
    const loaded = await loadActiveRulePreset(accountID);
    if (generation !== accountHydrationGeneration || accountID !== getPack().id) return loaded;
    activeRuleSource = loaded.source;
    applyActiveRuleConfig(loaded.preset.config, loaded.preset);
    if (activeRuleSource === 'server') cacheServerRuleFallback(loaded.preset.config, loaded.preset, accountID);
    activeRuleSelectorDraftID = loaded.preset.id || 'default';
    activeRuleSelectorStatus = '';
    if (afterHydrate) await afterHydrate(getPack());
    if (generation !== accountHydrationGeneration || accountID !== getPack().id) return loaded;
    const previewSync = await Promise.resolve(refreshRuleConsumers({ rerender: true }));
    if (generation !== accountHydrationGeneration || accountID !== getPack().id) return loaded;
    return { ...loaded, previewSync };
  }

  // 账号切换用：hydrate + 拉目录 + 复位选择器（app.html switchAccount 此前手工展开这段）。
  async function adoptAccount({ rerender = true } = {}) {
    invalidateSelectorInteractions();
    const accountID = getPack().id;
    const pack = getPack();
    const generation = ++accountHydrationGeneration;
    const catalogAtStart = catalogGeneration;
    const [loaded, catalog] = await Promise.all([
      loadActiveRulePreset(accountID),
      loadRuleCatalog(),
    ]);
    const stale = generation !== accountHydrationGeneration || accountID !== getPack().id;
    if (stale) return { ...loaded, accountID, stale: true };
    activeRuleSource = loaded.source;
    applyActiveRuleConfig(loaded.preset.config, loaded.preset);
    if (activeRuleSource === 'server') {
      cacheServerRuleFallback(loaded.preset.config, loaded.preset, accountID);
    }
    if (catalogAtStart === catalogGeneration) activeRuleCatalog = catalog;
    activeRuleSelectorEditing = false;
    activeRuleSelectorOpen = false;
    activeRuleSelectorDraftID = loaded.preset.id || 'default';
    activeRuleSelectorStatus = '';
    if (afterHydrate) await afterHydrate(pack);
    if (generation !== accountHydrationGeneration || accountID !== getPack().id) {
      return { ...loaded, accountID, stale: true };
    }
    const previewSync = await Promise.resolve(refreshRuleConsumers({ rerender }));
    if (generation !== accountHydrationGeneration || accountID !== getPack().id) {
      return { ...loaded, accountID, stale: true, previewSync };
    }
    return { ...loaded, accountID, stale: false, previewSync };
  }

  async function refreshActiveRuleCatalog(accountID = getPack().id) {
    const generation = ++catalogGeneration;
    const catalog = await loadRuleCatalog();
    if (generation !== catalogGeneration || accountID !== getPack().id) return catalog;
    activeRuleCatalog = catalog;
    renderActiveRuleCard();
    return catalog;
  }

  function renderActiveRuleCard() {
    const card = documentRef.getElementById('activeRuleCard');
    if (!card) return;
    const preset = currentPresetFromConfig();
    const currentID = preset.id || 'default';
    const selectedID = activeRuleSelectorEditing ? (activeRuleSelectorDraftID || currentID) : currentID;
    const options = ruleSelectorOptions(preset);
    const selectedRule = options.find(rule => rule.id === selectedID) || options[0];
    const canSave = activeRuleSelectorEditing && !activeRuleSaving && selectedID && selectedID !== currentID;
    const statusHTML = activeRuleSelectorStatus ? `<div class="active-rule-status">${escText(activeRuleSelectorStatus)}</div>` : '';
    const ruleName = escText(selectedRule.name || selectedRule.id);
    const menuHTML = activeRuleSelectorEditing && activeRuleSelectorOpen ? `
        <div class="active-rule-menu" id="activeRuleMenu" role="listbox" aria-label="当前规则">
          ${options.map(rule => `
            <button type="button" class="active-rule-option" role="option" aria-selected="${rule.id === selectedID ? 'true' : 'false'}" data-rule-option="${escText(rule.id)}">
              <span class="active-rule-option-name">${escText(rule.name || rule.id)}</span>
              <span class="active-rule-option-check" aria-hidden="true">✓</span>
            </button>
          `).join('')}
        </div>
      ` : '';
    // 只读态两种渠道口径：公众号纯展示；网站带「已锁定」徽章 + 铅笔编辑入口。
    const readonlyHTML = readonlyEditEntry ? `
    <div class="active-rule-readline">
      <span class="active-rule-readmeta">
        <span class="active-rule-label">${escText(readonlyLabel)}</span>
        <span class="active-rule-readvalue">${ruleName}</span>
        <span class="active-rule-badge">已锁定</span>
      </span>
      <button type="button" class="active-rule-icon" data-action="edit-active-rule" aria-label="修改当前规则" title="修改当前规则">
        <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true"><path d="M4 20h4l11-11a2.8 2.8 0 0 0-4-4L4 16v4z" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"/><path d="M13.5 6.5l4 4" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>
      </button>
    </div>
    ${statusHTML}
  ` : `
    <div class="active-rule-readline">
      <span class="active-rule-readmeta">
        <span class="active-rule-label">${escText(readonlyLabel)}</span>
        <span class="active-rule-readvalue">${ruleName}</span>
      </span>
    </div>
    ${statusHTML}
  `;
    card.className = 'active-rule-card' + (activeRuleSelectorEditing ? ' editing' : '') + (activeRuleSelectorOpen ? ' menu-open' : '');
    card.innerHTML = activeRuleSelectorEditing ? `
    <div class="active-rule-caption">
      <span class="active-rule-label">当前规则</span>
      <span class="active-rule-badge">编辑中</span>
    </div>
    <div class="active-rule-line">
      <span class="active-rule-select-wrap">
        <button type="button" id="activeRuleSelect" class="active-rule-trigger" data-value="${escText(selectedRule.id)}" aria-haspopup="listbox" aria-expanded="${activeRuleSelectorOpen ? 'true' : 'false'}" ${activeRuleSelectorEditing && !activeRuleSaving ? '' : 'disabled'}>
          <span class="active-rule-value">${ruleName}</span>
        </button>
        <svg class="active-rule-chevron" viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><path d="M4 6l4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/></svg>
        ${menuHTML}
      </span>
      <button type="button" class="active-rule-save" data-action="save-active-rule" ${canSave ? '' : 'disabled'}>保存</button>
      <button type="button" class="active-rule-cancel" data-action="cancel-active-rule" ${activeRuleSaving ? 'disabled' : ''}>取消</button>
    </div>
    ${statusHTML}
  ` : readonlyHTML;
    const trigger = card.querySelector('#activeRuleSelect');
    if (trigger) {
      trigger.addEventListener('click', () => toggleActiveRuleMenu());
      trigger.addEventListener('keydown', event => {
        if (!activeRuleSelectorEditing) return;
        if (event.key === 'Enter' || event.key === ' ' || event.key === 'ArrowDown') {
          event.preventDefault();
          activeRuleSelectorOpen = true;
          activeRuleSelectorStatus = '';
          renderActiveRuleCard();
        }
        if (event.key === 'Escape') {
          activeRuleSelectorOpen = false;
          renderActiveRuleCard();
        }
      });
    }
    card.querySelectorAll('[data-rule-option]').forEach(button => {
      button.addEventListener('click', () => chooseActiveRuleOption(button.getAttribute('data-rule-option')));
      button.addEventListener('keydown', event => handleActiveRuleOptionKeydown(event, button));
    });
    const editButton = card.querySelector('[data-action="edit-active-rule"]');
    if (editButton) editButton.addEventListener('click', () => beginActiveRuleEdit());
    const saveButton = card.querySelector('[data-action="save-active-rule"]');
    if (saveButton) saveButton.addEventListener('click', () => saveActiveRuleSelection());
    const cancelButton = card.querySelector('[data-action="cancel-active-rule"]');
    if (cancelButton) cancelButton.addEventListener('click', () => cancelActiveRuleEdit());
    syncActiveRuleOutsideHandler(card);
    if (activeRuleSelectorOpen) {
      const selectedOption = card.querySelector('[data-rule-option][aria-selected="true"]');
      if (selectedOption) selectedOption.focus();
    }
  }

  return {
    renderActiveRuleCard,
    hydrateActiveRulePreset,
    adoptAccount,
    refreshActiveRuleCatalog,
    beginActiveRuleEdit,
    cancelActiveRuleEdit,
    ensureRuleAdminPassword,
    cacheServerRuleFallback,
    localActiveRulePreset,
    loadRuleCatalog,
    getSource: () => activeRuleSource,
    setSource: (v) => { activeRuleSource = v; },
    setAdminPassword: (v) => { activeRuleAdminPassword = String(v || '').trim(); },
  };
}
