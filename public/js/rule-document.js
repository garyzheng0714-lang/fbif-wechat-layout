// rule-document.js — Rule document interface for configurable layout rules.
// This module owns the editable rule field catalog, module grouping, and
// default flat config used by callers and tests. Server-only modules (for
// example fbif_footer) still live behind /api/rules and use code fallbacks.

import {
  SYSTEM_DEFAULT_RECOMMENDED_CARDS,
  normalizeMoreArticleDefaultCards,
} from './more-articles-config.js';

export const RULE_SCHEMA = Object.freeze([
  {
    id: 'article',
    label: '文章排版',
    fields: Object.freeze([
      { key: 'font_size', label: '正文字号', type: 'number', min: 12, max: 24, step: 1, default: '15' },
      { key: 'heading_size', label: '标题字号', type: 'number', min: 14, max: 32, step: 1, default: '18' },
      { key: 'line_height', label: '行高', type: 'number', min: 1, max: 3, step: 0.05, default: '1.75' },
      { key: 'letter_spacing', label: '字间距', type: 'number', min: 0, max: 0.2, step: 0.002, default: '0.034' },
      { key: 'paragraph_margin_x', label: '段落横向边距', type: 'number', min: 0, max: 28, step: 1, default: '8' },
      { key: 'paragraph_gap', label: '段后间距', type: 'number', min: 0, max: 48, step: 1, default: '20' },
      { key: 'font_family', label: '字体', type: 'text', default: 'PingFang SC' },
      { key: 'text_color', label: '正文颜色', type: 'color', default: '#544545' },
      { key: 'link_color', label: '链接颜色', type: 'color', default: '#0070C0' },
      { key: 'muted_color', label: '辅助文字颜色', type: 'color', default: '#888888' },
      { key: 'justify_paragraphs', label: '段落两端对齐', type: 'boolean', default: true },
    ]),
  },
  {
    id: 'supporting',
    label: '说明与引用',
    fields: Object.freeze([
      { key: 'caption_font_size', label: '图片说明字号', type: 'number', min: 10, max: 18, step: 1, default: '12' },
      { key: 'attribution_font_size', label: '作者来源字号', type: 'number', min: 10, max: 20, step: 1, default: '15' },
      { key: 'blockquote_padding_left', label: '引用左缩进', type: 'number', min: 0, max: 32, step: 1, default: '12' },
      { key: 'blockquote_border_width', label: '引用线宽', type: 'number', min: 0, max: 8, step: 1, default: '3' },
      { key: 'md_heading_max_chars', label: 'Markdown 粗体标题字数上限', type: 'number', min: 10, max: 120, step: 1, default: '60' },
      { key: 'decorative_image_max_px', label: '小图保持原宽阈值', type: 'number', min: 120, max: 1000, step: 10, default: '640' },
    ]),
  },
  {
    id: 'more_articles',
    label: '更多文章',
    fields: Object.freeze([
      { key: 'more_articles_enabled', label: '启用底部更多文章', type: 'boolean', default: true },
      { key: 'more_articles_slots', label: '新文章默认卡片数', type: 'number', min: 0, max: 8, step: 1, default: '3' },
      { key: 'banner_overlay_alpha', label: '封面遮罩透明度', type: 'number', min: 0, max: 1, step: 0.01, default: '0.47058823529411764' },
      { key: 'banner_title_x', label: '标题 X', type: 'number', min: 0, max: 400, step: 1, default: '61' },
      { key: 'banner_title_y', label: '标题 Y', type: 'number', min: 0, max: 220, step: 1, default: '92' },
      { key: 'banner_title_width', label: '标题宽度', type: 'number', min: 300, max: 1000, step: 1, default: '878' },
      { key: 'banner_title_box_height', label: '标题框高度', type: 'number', min: 40, max: 220, step: 1, default: '116' },
      { key: 'banner_title_font_size', label: '标题字号', type: 'number', min: 24, max: 80, step: 1, default: '48' },
      { key: 'banner_title_line_height', label: '标题行高', type: 'number', min: 32, max: 100, step: 1, default: '70' },
      { key: 'banner_title_max_lines', label: '标题最多行数', type: 'number', min: 1, max: 4, step: 1, default: '2' },
    ]),
  },
  {
    id: 'table',
    label: '表格样式',
    fields: Object.freeze([
      { key: 'table_header_bg', label: '表头底色', type: 'color', default: '#01396a' },
      { key: 'table_header_text_color', label: '表头文字色', type: 'color', default: '#ffffff' },
      { key: 'table_header_bold', label: '表头加粗', type: 'boolean', default: true },
      { key: 'table_border_color', label: '边框颜色', type: 'color', default: '#e5e7eb' },
      { key: 'table_font_size', label: '表格字号', type: 'number', min: 12, max: 24, step: 1, default: '15' },
      { key: 'table_cell_padding', label: '单元格内距', type: 'number', min: 0, max: 24, step: 1, default: '8' },
    ]),
  },
  {
    id: 'workflow',
    label: '工作流',
    fields: Object.freeze([
      { key: 'footer_enabled', label: '默认显示页脚', type: 'boolean', default: true },
      { key: 'skip_upload', label: 'DOCX 跳过上传', type: 'boolean', default: true },
      { key: 'unpublish_window_hours', label: '发布后可下线时限（小时）', type: 'number', min: 0, max: 720, step: 1, default: '8' },
    ]),
  },
]);

export const FIELD_BY_KEY = new Map(
  RULE_SCHEMA.flatMap(group => group.fields.map(field => [field.key, field]))
);

export const DEFAULT_RULE_CONFIG = Object.freeze(
  {
    ...Object.fromEntries(Array.from(FIELD_BY_KEY.values()).map(field => [field.key, field.default])),
    more_articles_default_cards: normalizeMoreArticleDefaultCards(SYSTEM_DEFAULT_RECOMMENDED_CARDS),
  }
);

export const RULE_MODULE_GROUPS = Object.freeze([
  { id: 'theme', groupIds: Object.freeze(['article']) },
  { id: 'supporting', groupIds: Object.freeze(['supporting']) },
  { id: 'more_articles', groupIds: Object.freeze(['more_articles']) },
  { id: 'table', groupIds: Object.freeze(['table']) },
  { id: 'workflow', groupIds: Object.freeze(['workflow']) },
]);

export const RULE_MODULE_FIELDS = new Map(
  RULE_MODULE_GROUPS.map(moduleGroup => {
    const keys = new Set();
    for (const groupID of moduleGroup.groupIds) {
      const group = RULE_SCHEMA.find(item => item.id === groupID);
      if (!group) continue;
      for (const field of group.fields) keys.add(field.key);
    }
    return [moduleGroup.id, keys];
  })
);

export const SAFE_FONT_FAMILY_RE = /^[A-Za-z0-9\u4e00-\u9fff -]{1,40}$/;

export function listEditableRuleFields() {
  return Array.from(FIELD_BY_KEY.values());
}
