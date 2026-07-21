// 规则后台账号/基线选择的纯状态助手。服务端响应中的 rule_family 是绑定兼容性的真值。

import { rulePreviewSavedMessage } from './rule-preview-sync.js';

function clientRuleID(name) {
  const id = String(name || '').trim().toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-').replace(/^-+|-+$/g, '');
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
  const used = new Set(ruleListFromDoc(doc).map(rule => rule && rule.id).filter(Boolean));
  if (!used.has(base)) return base;
  let suffix = 2;
  while (used.has(base + '-' + suffix)) suffix++;
  return base + '-' + suffix;
}

export async function applyPersistedRuleConfig(applyConfig, config, preset, meta = {}) {
  try {
    const applied = await Promise.resolve(applyConfig(config, preset, meta));
    const previewSync = applied && applied.previewSync;
    if (!previewSync) return '';
    const message = rulePreviewSavedMessage(previewSync);
    return message === '已保存' ? '' : message;
  } catch {
    return '规则已保存，但当前预览刷新失败；请重试刷新或重新导入文章';
  }
}

export function createAdminOperationGate(getAccountID, getOverlay) {
  let generation = 0;
  const current = operation => !!operation && operation.generation === generation &&
    operation.accountID === getAccountID() && operation.overlay === getOverlay();
  return {
    invalidate() { generation++; },
    capture() { return { generation: ++generation, accountID: getAccountID(), overlay: getOverlay() }; },
    current,
    commit(operation, value, persistedMessage = '') {
      if (current(operation)) return value;
      const err = new Error(persistedMessage || '操作已取消：规则后台已关闭或当前账号已变化');
      err.adminOperationStale = true;
      err.adminOperationPersisted = !!persistedMessage;
      throw err;
    },
  };
}

export function migrationBaselinePresets(presets, ruleFamily) {
  const target = String(ruleFamily || '').trim();
  return (Array.isArray(presets) ? presets : []).filter(preset =>
    preset && !preset.migration_required && preset.id && preset.id === preset.baseline_id &&
    (!target || String(preset.rule_family || '') === target)
  );
}

export function compatibleRulePresets(presets, ruleFamily) {
  const target = String(ruleFamily || '').trim();
  return (Array.isArray(presets) ? presets : []).filter(preset =>
    preset && (preset.migration_required || !target || String(preset.rule_family || '') === target)
  );
}

export function presetForAccount(doc, accountID) {
  const binding = (doc && Array.isArray(doc.accounts) ? doc.accounts : [])
    .find(account => account && account.id === accountID);
  const activeID = binding && (binding.active_id || binding.activeID);
  if (!activeID) return null;
  return (doc && Array.isArray(doc.presets) ? doc.presets : [])
    .find(preset => preset && preset.id === activeID) || null;
}

export function accountBindingRevision(doc, accountID) {
  const binding = (doc && Array.isArray(doc.accounts) ? doc.accounts : [])
    .find(account => account && account.id === accountID);
  const revision = Number(binding && binding.active_revision);
  return Number.isInteger(revision) && revision >= 0 ? revision : 0;
}

export function baselinePresetForNewRule(doc, selected) {
  const baselineID = selected && selected.baseline_id;
  if (!baselineID) return null;
  return (doc && Array.isArray(doc.presets) ? doc.presets : [])
    .find(preset => preset && preset.id === baselineID) || null;
}
