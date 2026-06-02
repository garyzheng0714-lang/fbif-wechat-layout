// admin-behavior-data.js — 规则后台「代码化规则」表单编辑器的生命周期绑定。
// 从 admin-panel.js 抽出以守 900 行红线（CLAUDE.md：单文件 ≤900）。

import { createBehaviorFormEditor } from './admin/behavior-form-editor.js';

// createBehaviorFormBinder 管理 behavior 表单编辑器实例。
//   deps: { getBehavior():behavior, setBehavior(behavior) }
export function createBehaviorFormBinder(deps) {
  let instance = null;
  return {
    bind(overlay) {
      const mount = overlay && overlay.querySelector('[data-role="behavior-editor"]');
      if (!mount) return;
      if (instance) { instance.destroy(); instance = null; }
      instance = createBehaviorFormEditor({ container: mount, behavior: deps.getBehavior(), onChange: deps.setBehavior });
    },
    destroy() { if (instance) { instance.destroy(); instance = null; } },
  };
}
