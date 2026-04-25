// admin-panel.js — password-gated rule preset editor.
// Keeps UI orchestration separate from the rule schema and from the rendering
// engine, so adding a rule means updating rule-presets.js first.

import {
  RULE_SCHEMA,
  adminLogin,
  deleteRulePreset,
  listRulePresets,
  normalizeRuleConfig,
  saveRulePreset,
  setActiveRulePreset,
  validateRuleConfig,
} from './rule-presets.js';

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
  const applyConfig = options.applyConfig || ((config, preset) => {
    window._activeConfig = { config: normalizeRuleConfig(config), preset };
  });

  let overlay = null;
  let password = '';
  let doc = null;
  let selectedID = '';
  let workingConfig = normalizeRuleConfig(getConfig());
  let status = '';
  let busy = false;

  function setStatus(text) {
    status = text || '';
    render();
  }

  async function refreshDoc() {
    doc = await listRulePresets(password);
    const current = getPreset();
    selectedID = current && presetByID(doc, current.id) ? current.id : (doc.active_id || 'default');
    const selected = presetByID(doc, selectedID) || activePresetFromDoc(doc);
    workingConfig = normalizeRuleConfig(selected ? selected.config : getConfig());
    applyConfig(workingConfig, selected, { source: 'server' });
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

  function renderMain() {
    const presets = (doc && doc.presets) || [];
    const selected = presetByID(doc, selectedID) || activePresetFromDoc(doc) || getPreset();
    const name = selected && selected.name ? selected.name : '未命名规则';
    const description = selected && selected.description ? selected.description : '';
    const optionsHTML = presets.map(p => {
      const activeMark = p.id === (doc && doc.active_id) ? ' · 当前' : '';
      return `<option value="${esc(p.id)}" ${p.id === selectedID ? 'selected' : ''}>${esc(p.name + activeMark)}</option>`;
    }).join('');
    overlay.innerHTML = `
      <div class="admin-dialog" role="dialog" aria-modal="true" aria-labelledby="adminTitle">
        <div class="admin-head">
          <div>
            <div class="admin-title" id="adminTitle">规则后台</div>
            <div class="admin-subtitle">${ADMIN_HINT}</div>
          </div>
          <button type="button" class="admin-close" aria-label="关闭">×</button>
        </div>
        <div class="admin-body">
          <div class="admin-rule-bar">
            <label>
              <span>规则预设</span>
              <select data-role="preset-select">${optionsHTML}</select>
            </label>
            <button type="button" class="admin-secondary" data-action="set-active">设为当前规则</button>
          </div>
          <div class="admin-meta-grid">
            <label>
              <span>规则名称</span>
              <input type="text" data-role="preset-name" value="${esc(name)}">
            </label>
            <label>
              <span>说明</span>
              <input type="text" data-role="preset-description" value="${esc(description)}">
            </label>
          </div>
          <div data-role="rule-warnings">${renderWarningsHTML()}</div>
          ${renderGroups(workingConfig)}
        </div>
        <div class="admin-foot">
          <div class="admin-status">${esc(status)}</div>
          <button type="button" class="admin-secondary danger" data-action="delete" ${selectedID === 'default' ? 'disabled' : ''}>删除</button>
          <button type="button" class="admin-secondary" data-action="save-as">另存为规则</button>
          <button type="button" class="admin-primary" data-action="save">保存到服务器</button>
        </div>
      </div>`;
    bindMain();
  }

  function updateWorkingField(key, value) {
    workingConfig = normalizeRuleConfig({ ...workingConfig, [key]: value });
    const selected = presetByID(doc, selectedID) || getPreset();
    applyConfig(workingConfig, selected);
    refreshWarnings();
  }

  function bindMain() {
    overlay.querySelector('.admin-close').addEventListener('click', close);
    overlay.querySelector('[data-role="preset-select"]').addEventListener('change', e => {
      selectedID = e.target.value;
      const selected = presetByID(doc, selectedID);
      workingConfig = normalizeRuleConfig(selected ? selected.config : getConfig());
      applyConfig(workingConfig, selected);
      status = '已应用到当前预览';
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
      const selected = presetByID(doc, selectedID) || { id: selectedID };
      const name = overlay.querySelector('[data-role="preset-name"]').value.trim() || selected.name || '未命名规则';
      const description = overlay.querySelector('[data-role="preset-description"]').value.trim();
      run(async () => {
        doc = await saveRulePreset(password, {
          id: selected.id,
          name,
          description,
          config: workingConfig,
        });
        await setActiveRulePreset(password, selected.id);
        doc = await listRulePresets(password);
        applyConfig(workingConfig, presetByID(doc, selected.id), { source: 'server' });
      }, '已保存到服务器');
    });
    overlay.querySelector('[data-action="save-as"]').addEventListener('click', () => {
      const name = overlay.querySelector('[data-role="preset-name"]').value.trim() || '新规则';
      const id = clientRuleID(name);
      const description = overlay.querySelector('[data-role="preset-description"]').value.trim();
      run(async () => {
        doc = await saveRulePreset(password, { id, name, description, config: workingConfig });
        await setActiveRulePreset(password, id);
        doc = await listRulePresets(password);
        selectedID = id;
        applyConfig(workingConfig, presetByID(doc, id), { source: 'server' });
      }, '已另存并设为当前规则');
    });
    overlay.querySelector('[data-action="set-active"]').addEventListener('click', () => run(async () => {
      await setActiveRulePreset(password, selectedID);
      doc = await listRulePresets(password);
      applyConfig(workingConfig, presetByID(doc, selectedID), { source: 'server' });
    }, '已设为当前规则'));
    overlay.querySelector('[data-action="delete"]').addEventListener('click', () => run(async () => {
      doc = await deleteRulePreset(password, selectedID);
      selectedID = doc.active_id;
      const selected = activePresetFromDoc(doc);
      workingConfig = normalizeRuleConfig(selected ? selected.config : getConfig());
      applyConfig(workingConfig, selected, { source: 'server' });
    }, '已删除'));
  }

  function render() {
    if (!overlay) return;
    if (!password || !doc) renderLogin();
    else renderMain();
  }

  button.addEventListener('click', open);
}
