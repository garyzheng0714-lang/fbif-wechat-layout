// admin-block-data.js — 规则后台「底部区块」编辑器的数据助手 + 实例生命周期绑定。
// 从 admin-panel.js 抽出以守 900 行红线（CLAUDE.md：单文件 ≤900，超出必须模块化拆分）。

import { createBlockEditor } from './admin/block-editor.js';

// bottomBlocksFromPreset 从预设取 bottom_blocks 模块（缺省给空模块，避免编辑器空指针）。
export function bottomBlocksFromPreset(preset) {
  const raw = preset && preset.config && preset.config.modules && preset.config.modules.bottom_blocks;
  if (raw && typeof raw === 'object') return JSON.parse(JSON.stringify(raw));
  return { enabled: false, order: [], blocks: {}, presets: [] };
}

// createBlockEditorBinder 管理后台区块编辑器实例的挂载/销毁。
//   deps: { getModule():module, setModule(module), getChannel():'mp'|'ft' }
export function createBlockEditorBinder(deps) {
  let instance = null;
  return {
    bind(overlay) {
      const mount = overlay && overlay.querySelector('[data-role="block-editor"]');
      if (!mount) return;
      if (instance) { instance.destroy(); instance = null; }
      instance = createBlockEditor({ container: mount, module: deps.getModule(), channel: deps.getChannel(), onChange: deps.setModule });
    },
    destroy() { if (instance) { instance.destroy(); instance = null; } },
  };
}
