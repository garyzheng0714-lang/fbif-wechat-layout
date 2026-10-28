// 把当前规则（扁平参数 + 行为 + 模块开关）拼成排版引擎的渲染配置（engine.js setActiveConfig）。
// app.html / site.html 拿页面上的现状调；接口排版（render-service/layout.mjs）拿「新导入、没手改过」的状态调，
// 两边因此逐字一样。
import { pickActivePreset } from './blocks/block-merge.js';

// 公众号：扁平参数 + 服务端行为；页脚由模板按 bottom_blocks 自己出。
export function mpEngineConfig({ flat, behavior }) {
  return {
    ...flat,
    behavior: behavior || {},
  };
}

// 服务端激活规则的原始 config：含 normalizeRuleConfig 不认的账号专属参数（系列模板的 h1_font_size 等）
// 与结构化模块（页脚库、素材库、底部区块）。normalizeRuleConfig 只重建通用字段，这些会被丢掉。
export function ruleConfigOf(activeConfig) {
  const ac = activeConfig || {};
  let cfg = (ac.preset && (ac.preset.raw_config || ac.preset.config)) || ac.config || {};
  if (cfg && cfg.config && cfg.config.modules) cfg = cfg.config; // 兼容 schema_version 2 嵌套
  return (cfg && typeof cfg === 'object') ? cfg : {};
}

export function ruleModulesOf(activeConfig) {
  const cfg = ruleConfigOf(activeConfig);
  return (cfg.modules && typeof cfg.modules === 'object') ? cfg.modules : {};
}

function disabledFooterSegments(segments, order = []) {
  const next = {};
  for (const key of ['tip', ...order, ...Object.keys(segments || {})]) {
    if (key) next[key] = 0;
  }
  return next;
}

function footerSegmentEnabledForBlock(id, block, effectiveSegments, footerEnabled) {
  if (!footerEnabled) return false;
  if (effectiveSegments && Object.prototype.hasOwnProperty.call(effectiveSegments, id)) {
    return !!effectiveSegments[id];
  }
  return !(block && block.enabled === false);
}

// FoodTalks：命门是扁平参数（muted_color 等）必须并进去，否则网站模板读不到，
// 后台改颜色不生效（见 specs/2026-05-28-unified-rule-engine-design.md §3）。
//   footerState     site-footer-state.js readFooterPanelState 的结果（页脚开关、逐段开关、顺序、页脚库）
//   segmentFields   本篇换过的页脚图/文字（{段: fields}），没换过传 {}
//   applyArticleOverrides  把本篇手改（换图、换 BD 名片）套进页脚库；没手改时原样返回
export function siteEngineConfig({
  flat,
  prevModules = {},
  behavior,
  modules,
  footerState,
  segmentFields = {},
  applyArticleOverrides = lib => lib,
}) {
  const serverFooter = footerState.footerLib;
  const footerEnabled = footerState.enabled;
  const footerActiveSegments = footerState.segments;
  const footerOrder = footerState.order;
  const effectiveFooterSegments = footerEnabled ? footerActiveSegments : disabledFooterSegments(footerActiveSegments, footerOrder);
  const serverAssets = (modules.asset_library && typeof modules.asset_library === 'object') ? modules.asset_library : {};
  const serverBottomBlocks = modules.bottom_blocks;
  const baseFooterForRender = Array.isArray(footerOrder) && footerOrder.length
    ? { ...serverFooter, segment_order: footerOrder.filter(k => k !== 'tip') }
    : serverFooter;
  const footerForRender = applyArticleOverrides(baseFooterForRender);
  // 统一区块模型：面板内部状态已收敛为 bottom_blocks override（开关/排序/逐块启用）。
  // 逐块 enabled = !!active_segments[id]（缺失视为关），与旧 buildFooterHtml 的 `if(!seg[key])continue` 一致。
  let bottomBlocksOverride;
  if (serverBottomBlocks && typeof serverBottomBlocks === 'object') {
    // v2：order 在 active preset 内（复用 block-merge 的 pickActivePreset 选预设）。
    const bbActive = pickActivePreset(serverBottomBlocks);
    const bbOrder = Array.isArray(bbActive.order) ? bbActive.order : [];
    const bbBlocks = bbActive && typeof bbActive.blocks === 'object' ? bbActive.blocks : {};
    const overrides = {};
    for (const id of bbOrder) {
      if (id === 'tip') continue;
      overrides[id] = { enabled: footerSegmentEnabledForBlock(id, bbBlocks[id], effectiveFooterSegments, footerEnabled) };
      if (segmentFields[id] && Object.keys(segmentFields[id]).length) {
        overrides[id] = { ...overrides[id], fields: segmentFields[id] };
      }
    }
    bottomBlocksOverride = { enabled: footerEnabled, overrides };
    if (Array.isArray(footerOrder) && footerOrder.length) {
      bottomBlocksOverride.order = footerOrder.filter(k => k !== 'tip');
    }
  }
  return {
    ...flat,
    behavior: behavior || {},
    _bottomBlocksOverride: bottomBlocksOverride,
    modules: {
      ...prevModules,
      fbif_footer: {
        ...footerForRender,
        enabled: footerEnabled,
        active_preset_id: '__article_override__',
        presets: [{ id: '__article_override__', name: '本篇', segments: effectiveFooterSegments }],
        active_segments: effectiveFooterSegments,
      },
      asset_library: serverAssets,
      ...(serverBottomBlocks && typeof serverBottomBlocks === 'object' ? { bottom_blocks: serverBottomBlocks } : {}),
      // 网站渠道 h2 默认不输出 mctoc 锚点 id —— FoodTalks 后台生成目录时会自己加
      // （用户 2026-05-30 确认），基石 news/62285 的 h2 也不带 id。默认关，避免与后台冲突。
      mctoc: prevModules.mctoc || { enabled: false },
    },
  };
}
