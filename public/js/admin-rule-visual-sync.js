// admin-rule-visual-sync.js — keep the visual rule form aligned with JSON edits.

export function syncRuleVisualEditor({ overlay, schema, config, name, description, fieldValue }) {
  if (!overlay) return;
  const nameInput = overlay.querySelector('[data-role="preset-name"]');
  const descriptionInput = overlay.querySelector('[data-role="preset-description"]');
  if (nameInput) nameInput.value = name;
  if (descriptionInput) descriptionInput.value = description;
  for (const group of schema) {
    for (const field of group.fields) {
      const value = fieldValue(config, field);
      const input = overlay.querySelector(`input[data-rule-key="${field.key}"]`);
      const toggle = overlay.querySelector(`.admin-toggle[data-rule-key="${field.key}"]`);
      if (input) input.value = field.type === 'color' ? String(value).toUpperCase() : value;
      if (toggle) {
        toggle.classList.toggle('on', value === true);
        toggle.setAttribute('aria-pressed', value === true ? 'true' : 'false');
      }
      const hex = overlay.querySelector(`[data-color-hex="${field.key}"]`);
      if (hex) hex.textContent = String(value).toUpperCase();
    }
  }
}
