// admin-panel.js — password-gated rule preset editor.
// Keeps UI orchestration separate from the rule schema and from the rendering
// engine, so adding a rule means updating rule-presets.js first.

import {
  DEFAULT_RULE_CONFIG,
  RULE_SCHEMA,
  adminLogin,
  buildRuleConfigDocument,
  deleteRulePreset,
  listRulePresets,
  normalizeRuleConfig,
  parseRuleConfigDocument,
  saveRulePreset,
  setActiveRulePreset,
  validateRuleConfig,
} from './rule-presets.js';
import {
  DEFAULT_BEHAVIOR,
  normalizeAuthorName,
  normalizeRuleBehavior,
  renderBehaviorTemplate,
  validateRuleBehavior,
} from './rule-behavior.js';

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
  return `
    <label class="admin-field">
      <span>${esc(field.label)}</span>
      <input type="number" data-rule-key="${esc(field.key)}" value="${esc(value)}" min="${field.min}" max="${field.max}" step="${field.step}">
    </label>`;
}

function renderGroups(config) {
  return RULE_SCHEMA.map(group => `
    <section class="admin-rule-group">
      <h3>${esc(group.label)}</h3>
      <div class="admin-field-grid">
        ${group.fields.map(field => renderField(config, field)).join('')}
      </div>
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
  let workingName = '未命名规则';
  let workingDescription = '';
  let jsonText = '';
  let jsonDirty = false;
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
    syncJSONText();
    syncBehaviorText();
    applyConfig(workingConfig, presetWithBehavior(selected), { source: 'server' });
  }

  function close() {
    if (!overlay) return;
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

  function renderBehaviorWarningsHTML() {
    const result = validateRuleBehavior(workingBehavior);
    if (result.ok) {
      return '<div class="admin-rule-ok">代码化规则校验通过。</div>';
    }
    return '<ul class="admin-warnings">' + result.errors.map(w => '<li>' + esc(w) + '</li>').join('') + '</ul>';
  }

  function sampleBehaviorContext() {
    return {
      article: {
        author: 'Mote莫特',
        source: 'FoodTalks',
        author_normalized: normalizeAuthorName('Mote莫特'),
      },
      account: { name: 'FBIF' },
    };
  }

  function renderBehaviorPreviewHTML() {
    const context = sampleBehaviorContext();
    const behavior = normalizeRuleBehavior(workingBehavior);
    const rows = [
      ['作者行', renderBehaviorTemplate(behavior.templates.author_line, context)],
      ['来源行', renderBehaviorTemplate(behavior.templates.source_line, context)],
      ['原创提示', renderBehaviorTemplate(behavior.templates.footer_original_notice, context)],
      ['转载提示', renderBehaviorTemplate(behavior.templates.footer_repost_notice, context)],
    ];
    const insertImage = behavior.actions.some(action => action.type === 'insert_image_before' && action.when === 'is_mote_author');
    return `
      <div class="admin-preview-grid">
        ${rows.map(([label, value]) => `
          <div class="admin-preview-row">
            <span>${esc(label)}</span>
            <code>${esc(value)}</code>
          </div>
        `).join('')}
        <div class="admin-preview-row">
          <span>Mote 署名图</span>
          <code>${insertImage ? '会在参考来源前插入' : '不会插入'}</code>
        </div>
      </div>`;
  }

  function renderRuleTabs() {
    const tabs = [
      ['params', '参数'],
      ['behavior', '代码化规则'],
      ['validation', '校验预览'],
    ];
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
    const el = overlay && overlay.querySelector('.admin-status');
    if (el) el.textContent = status;
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

  function visibleRulePacks() {
    return rulePacks.length ? rulePacks : [{ id: 'fbif', accountName: 'FBIF公众号' }];
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
        <button type="button" data-view="assignments" class="${activeView === 'assignments' ? 'active' : ''}">规则配置</button>
        <button type="button" data-view="rules" class="${activeView === 'rules' ? 'active' : ''}">规则</button>
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
        <h3>规则配置</h3>
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

  function renderRuleTabContent() {
    if (activeRuleTab === 'behavior') {
      return `
        <section class="admin-rule-group admin-json-panel">
          <div class="admin-json-head">
            <h3>代码化规则</h3>
            <div class="admin-json-actions">
              <button type="button" class="admin-secondary" data-action="validate-behavior">校验代码</button>
              <button type="button" class="admin-secondary" data-action="apply-behavior">应用到预览</button>
            </div>
          </div>
          <textarea data-role="behavior-json" spellcheck="false">${esc(behaviorText)}</textarea>
        </section>`;
    }
    if (activeRuleTab === 'validation') {
      return `
        <section class="admin-rule-group admin-validation-panel">
          <h3>参数校验</h3>
          <div data-role="rule-warnings">${renderWarningsHTML()}</div>
          <h3>代码化规则校验</h3>
          <div data-role="behavior-warnings">${renderBehaviorWarningsHTML()}</div>
          <h3>输出预览</h3>
          ${renderBehaviorPreviewHTML()}
        </section>`;
    }
    return `
      <div data-role="rule-warnings">${renderWarningsHTML()}</div>
      <section class="admin-rule-group admin-json-panel">
        <div class="admin-json-head">
          <h3>JSON 规则</h3>
          <div class="admin-json-actions">
            <button type="button" class="admin-secondary" data-action="validate-json">校验 JSON</button>
            <button type="button" class="admin-secondary" data-action="apply-json">应用到预览</button>
          </div>
        </div>
        <textarea data-role="rule-json" spellcheck="false">${esc(jsonText)}</textarea>
      </section>
      ${renderGroups(workingConfig)}`;
  }

  function renderRulesEditor(optionsHTML) {
    return `
      <div class="admin-rule-bar">
        <label>
          <span>规则预设</span>
          <select data-role="preset-select">${optionsHTML}</select>
        </label>
        <button type="button" class="admin-secondary" data-action="new">新建规则</button>
        <button type="button" class="admin-secondary" data-action="set-active">设为当前规则</button>
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
    const selected = selectedPreset();
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
          ${activeView === 'rules' ? `
            <button type="button" class="admin-secondary danger" data-action="delete" ${selectedID === 'default' ? 'disabled' : ''}>删除</button>
            <button type="button" class="admin-secondary" data-action="save-as">另存为规则</button>
            <button type="button" class="admin-primary" data-action="save">保存到服务器</button>
          ` : ''}
        </div>
      </div>`;
    bindMain();
  }

  function updateWorkingField(key, value) {
    workingConfig = normalizeRuleConfig({ ...workingConfig, [key]: value });
    const selected = selectedPreset();
    jsonDirty = false;
    applyConfig(workingConfig, presetWithBehavior(selected));
    refreshWarnings();
    syncJSONEditorFromInputs();
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
            selectedID = ruleID;
            syncBehaviorText();
            applyConfig(workingConfig, presetWithBehavior(selected), { source: 'server' });
          }
        }, '已更新规则配置');
      });
    });
    if (activeView !== 'rules') return;
    overlay.querySelector('[data-role="preset-select"]').addEventListener('change', e => {
      selectedID = e.target.value;
      const selected = selectedPreset();
      workingConfig = normalizeRuleConfig(selected ? selected.config : getConfig());
      workingBehavior = normalizeRuleBehavior(selected ? selected.behavior : getBehavior());
      workingName = selected && selected.name ? selected.name : '未命名规则';
      workingDescription = selected && selected.description ? selected.description : '';
      syncJSONText();
      syncBehaviorText();
      applyConfig(workingConfig, presetWithBehavior(selected));
      status = '已应用到当前预览';
      renderMain();
    });
    overlay.querySelector('[data-role="preset-name"]').addEventListener('input', e => {
      workingName = e.target.value;
      jsonDirty = false;
      syncJSONEditorFromInputs();
    });
    overlay.querySelector('[data-role="preset-description"]').addEventListener('input', e => {
      workingDescription = e.target.value;
      jsonDirty = false;
      syncJSONEditorFromInputs();
    });
    const ruleJSON = overlay.querySelector('[data-role="rule-json"]');
    if (ruleJSON) ruleJSON.addEventListener('input', e => {
      jsonText = e.target.value;
      jsonDirty = true;
    });
    const behaviorJSON = overlay.querySelector('[data-role="behavior-json"]');
    if (behaviorJSON) behaviorJSON.addEventListener('input', e => {
      behaviorText = e.target.value;
      behaviorDirty = true;
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
        const parsed = parseRuleConfigDocument(jsonText);
        const nameInput = overlay.querySelector('[data-role="preset-name"]');
        const descriptionInput = overlay.querySelector('[data-role="preset-description"]');
        workingName = parsed.name || '未命名规则';
        workingDescription = parsed.description;
        if (nameInput) nameInput.value = workingName;
        if (descriptionInput) descriptionInput.value = parsed.description;
        workingConfig = parsed.config;
        const selected = selectedPreset();
        jsonDirty = false;
        applyConfig(workingConfig, presetWithBehavior(selected));
        updateStatusInline('已应用 JSON 到当前预览，保存后才会持久生效');
        renderMain();
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
        id: clientRuleID('新规则'),
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
    overlay.querySelector('[data-action="save"]').addEventListener('click', () => {
      run(async () => {
        syncDirtyJSONBeforePersist();
        syncDirtyBehaviorBeforePersist();
        const selected = presetByID(doc, selectedID) || { id: selectedID };
        const name = workingName.trim() || selected.name || '未命名规则';
        const description = workingDescription.trim();
        doc = await saveRulePreset(password, {
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
        const id = clientRuleID(name);
        const description = workingDescription.trim();
        doc = await saveRulePreset(password, { id, name, description, config: workingConfig, behavior: workingBehavior });
        await setActiveRulePreset(password, id, currentAccountID());
        doc = await listRulePresets(password);
        draftPreset = null;
        selectedID = id;
        applyConfig(workingConfig, presetWithBehavior(presetByID(doc, id)), { source: 'server' });
      }, '已另存并设为当前规则');
    });
    overlay.querySelector('[data-action="set-active"]').addEventListener('click', () => run(async () => {
      if (draftPreset && draftPreset.id === selectedID) {
        throw new Error('新规则需要先保存');
      }
      const accountID = currentAccountID();
      await setActiveRulePreset(password, selectedID, accountID);
      doc = await listRulePresets(password);
      applyConfig(workingConfig, presetWithBehavior(presetByID(doc, selectedID)), { source: 'server' });
    }, '已设为当前公众号规则'));
    overlay.querySelector('[data-action="delete"]').addEventListener('click', () => run(async () => {
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
    }, '已删除'));
  }

  function render() {
    if (!overlay) return;
    if (!password || !doc) renderLogin();
    else renderMain();
  }

  button.addEventListener('click', open);
}
