// 规则工作台数字字段 ± 步进器（方案A「检视台」）。
// 包裹同一个 input[type=number][data-rule-key]——既有 onChange 绑定（admin-panel.js
// bindMain 的 input[data-rule-key] 监听）与 syncRuleVisualEditor 仍按原样工作，
// 11 个 rule-admin 测试零影响。按钮按 step 调值并派发 input 事件复用既有链路。

export function numberField(field, value, esc) {
  return `
    <label class="admin-field">
      <span>${esc(field.label)}</span>
      <span class="admin-stepper">
        <button type="button" class="admin-step" data-step-dir="-1" tabindex="-1" aria-hidden="true">−</button>
        <input type="number" data-rule-key="${esc(field.key)}" value="${esc(value)}" min="${field.min}" max="${field.max}" step="${field.step}">
        <button type="button" class="admin-step" data-step-dir="1" tabindex="-1" aria-hidden="true">+</button>
      </span>
    </label>`;
}

export function bindSteppers(overlay) {
  overlay.querySelectorAll('.admin-stepper').forEach(wrap => {
    const input = wrap.querySelector('input[data-rule-key]');
    if (!input) return;
    const step = parseFloat(input.step) || 1;
    const min = input.min === '' ? -Infinity : parseFloat(input.min);
    const max = input.max === '' ? Infinity : parseFloat(input.max);
    wrap.querySelectorAll('.admin-step').forEach(btn => {
      btn.addEventListener('click', () => {
        const dir = parseInt(btn.dataset.stepDir, 10) || 0;
        let v = (parseFloat(input.value) || 0) + dir * step;
        v = Math.min(max, Math.max(min, v));
        v = parseFloat(v.toFixed(4));
        input.value = v;
        input.dispatchEvent(new Event('input', { bubbles: true }));
      });
    });
  });
}
