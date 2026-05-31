import { FOOTER_SEGMENTS_DEFAULT, FOOTER_SEGMENT_ORDER_DEFAULT, FOOTER_PRESETS_DEFAULT } from './footer-segments-defaults.js';

export function defaultFooterData() {
  return {
    enabled: false,
    active_preset_id: 'fbif-original',
    segment_order: [...FOOTER_SEGMENT_ORDER_DEFAULT],
    segments: Object.fromEntries(Object.entries(FOOTER_SEGMENTS_DEFAULT).map(([k, v]) => [k, { html_lines: [...v.html_lines] }])),
    presets: FOOTER_PRESETS_DEFAULT.map(p => ({ ...p, segments: { ...p.segments } })),
  };
}

export function footerFromPreset(preset) {
  const raw = preset && preset.config && preset.config.modules && preset.config.modules.fbif_footer;
  return raw ? JSON.parse(JSON.stringify(raw)) : defaultFooterData();
}
