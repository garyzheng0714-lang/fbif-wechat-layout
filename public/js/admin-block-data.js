// admin-block-data.js — 规则后台「底部区块」编辑器的数据助手 + 实例生命周期绑定。
// 从 admin-panel.js 抽出以守 900 行红线（CLAUDE.md：单文件 ≤900，超出必须模块化拆分）。

import { createBlockEditor } from './admin/block-editor.js';
import { uploadAssetIntoLibrary } from './blocks/asset-upload.js';
import { uploadAssetFile } from './uploader.js';

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
      // 上传新图（默认入库）：上传 file → 构造 asset → deps.addAsset 写工作副本 → 返回 id 供 picker 选中。
      const onUploadAsset = deps.addAsset
        ? async (file, scope) => { const asset = await uploadAssetIntoLibrary({ file, scope, uploadFn: uploadAssetFile }); deps.addAsset(asset); return asset.id; }
        : undefined;
      instance = createBlockEditor({ container: mount, module: deps.getModule(), channel: deps.getChannel(), getAssetLibrary: deps.getAssetLibrary, getScope: deps.getScope, onChange: deps.setModule, onUploadAsset });
    },
    destroy() { if (instance) { instance.destroy(); instance = null; } },
  };
}

// renderRuleBottomTabHtml 渲染「底部」与「素材库」两个规则 tab（素材库独立成 tab，用户裁定 spec §9）。
// 素材库是「被引用」的资产池，独立 tab；底部区块 + 网站页脚库在「底部」tab。
export function renderRuleBottomTabHtml(isFt) {
  return `<section class="admin-rule-group"><h3>底部区块默认</h3><div data-role="block-editor"></div>${isFt ? '<h3>网站页脚库</h3><div data-role="footer-library-editor"></div>' : ''}</section>`;
}
