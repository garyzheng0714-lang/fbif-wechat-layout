// 解析网站页脚库当前启用片段。兼容旧 active_segments 与新 presets 结构。

export function activeFooterSegments(footerCfg) {
  if (!footerCfg || typeof footerCfg !== 'object') return null;
  const presets = Array.isArray(footerCfg.presets) ? footerCfg.presets : [];
  if (presets.length) {
    const activeId = String(footerCfg.active_preset_id || '');
    const active = presets.find(p => p && p.id === activeId) || presets[0];
    if (active && active.segments && typeof active.segments === 'object') {
      return active.segments;
    }
  }
  if (footerCfg.active_segments && typeof footerCfg.active_segments === 'object') {
    return footerCfg.active_segments;
  }
  return null;
}
