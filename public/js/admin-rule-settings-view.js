import { normalizeRuleConfig } from './rule-presets.js';
import { numberField } from './admin-field-controls.js';

function esc(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function fieldValue(config, field) {
  return normalizeRuleConfig(config)[field.key];
}

function renderField(config, field) {
  const value = fieldValue(config, field);
  if (field.type === 'color') {
    return `<label class="admin-field"><span>${esc(field.label)}</span><span class="admin-color-field"><input type="color" data-rule-key="${esc(field.key)}" value="${esc(value)}"><span data-color-hex="${esc(field.key)}">${esc(value)}</span></span></label>`;
  }
  if (field.type === 'boolean') {
    return `<label class="admin-field"><span>${esc(field.label)}</span><button type="button" class="toggle-switch admin-toggle ${value === true ? 'on' : ''}" data-rule-key="${esc(field.key)}" aria-pressed="${value === true ? 'true' : 'false'}"><span class="toggle-knob"></span></button></label>`;
  }
  if (field.type === 'text') {
    return `<label class="admin-field"><span>${esc(field.label)}</span><input type="text" data-rule-key="${esc(field.key)}" value="${esc(value)}"></label>`;
  }
  return numberField(field, value, esc);
}

export function renderRuleSettingsGroups(config, schema) {
  return schema.map((group, index) => {
    const fields = group.fields.filter(field => group.id !== 'more_articles' || field.key !== 'more_articles_enabled');
    const settingCount = fields.length + (group.id === 'more_articles' ? 1 : 0);
    return `<details class="admin-settings-group" ${index === 0 ? 'open' : ''}><summary><span>${esc(group.label)}</span><small>${settingCount} 项设置</small></summary><div class="admin-settings-group-body"><div class="admin-field-grid">${fields.map(field => renderField(config, field)).join('')}</div>${group.id === 'more_articles' ? '<div data-role="more-articles-rule-editor"></div>' : ''}</div></details>`;
  }).join('');
}
