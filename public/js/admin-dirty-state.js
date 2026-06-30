import { normalizeRuleConfig } from './rule-presets.js';
import { DEFAULT_BEHAVIOR, normalizeRuleBehavior } from './rule-behavior.js';
import { defaultFooterData, footerFromPreset } from './admin-footer-data.js';
import { assetLibraryFromPreset, defaultAssetLibraryData } from './admin-asset-data.js';
import { bottomBlocksFromPreset } from './admin-block-data.js';
import { articleFooterDefaultsFromPreset } from './admin-article-footer-defaults.js';

const EMPTY_BOTTOM_BLOCKS = { schema_version: 2, enabled: false, presets: [] };
const sameJSON = (a, b) => JSON.stringify(a) === JSON.stringify(b);

export function adminDirtyCount(state) {
  const selected = state.selected || {};
  const oc = selected.config || {};
  let n = state.draftPreset && state.draftPreset.id === state.selectedID ? 1 : 0;
  if ((state.workingName || '') !== (selected.name || '未命名规则')) n++;
  if ((state.workingDescription || '') !== (selected.description || '')) n++;
  if (!sameJSON(state.workingConfig, normalizeRuleConfig(oc))) n++;
  if (!sameJSON(state.workingBehavior, normalizeRuleBehavior(selected.behavior || DEFAULT_BEHAVIOR))) n++;
  if (!sameJSON(state.workingAssets || defaultAssetLibraryData(), assetLibraryFromPreset(selected))) n++;
  if (!sameJSON(state.workingBottomBlocks || EMPTY_BOTTOM_BLOCKS, bottomBlocksFromPreset(selected))) n++;
  if (!sameJSON(state.workingArticleFooterDefaults || articleFooterDefaultsFromPreset(selected), articleFooterDefaultsFromPreset(selected))) n++;
  if (state.isFtDefault && !sameJSON(state.workingFooter || defaultFooterData(), footerFromPreset(selected))) n++;
  return n;
}

export function updateDirtyChip(overlay, count) {
  const el = overlay && overlay.querySelector('[data-role="dirty-chip"]');
  if (!el) return;
  el.textContent = count ? count + ' 项未保存' : '无未保存改动';
  el.classList.toggle('dirty', count > 0);
}
