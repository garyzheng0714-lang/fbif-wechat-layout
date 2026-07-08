import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const siteHtml = readFileSync(new URL('../public/site.html', import.meta.url), 'utf8');
const siteCss = readFileSync(new URL('../public/site.css', import.meta.url), 'utf8');
const removedStateAttr = ['data', 'footer', 'seg', 'state'].join('-');
const removedStateClass = ['ft', 'footer', 'seg', 'state'].join('-');

test('页脚面板：默认排版和什么是系列使用各自的片段标签', () => {
  // WHY：默认网站规则和「什么是系列」共用 site.html，但页脚片段含义不同；
  // 面板文案不能把默认排版显示成系列商务合作/饮料群。
  assert.match(siteHtml, /const FT_FOOTER_LABELS_DEFAULT = Object\.freeze/);
  assert.match(siteHtml, /biz_coop: '商务合作'/);
  assert.match(siteHtml, /join_group: '加入微信群（二维码）'/);
  assert.match(siteHtml, /const FT_FOOTER_LABELS_SERIES = Object\.freeze/);
  assert.match(siteHtml, /biz_coop: '什么是系列商务合作'/);
  assert.match(siteHtml, /join_group: '加入FBIF饮料群（二维码）'/);
  assert.match(siteHtml, /activeRulePack && activeRulePack\.id === 'ft-series'/);
  assert.match(siteHtml, /footerSegmentLabel\(key\)/);
  assert.doesNotMatch(siteHtml, /FT_FOOTER_LABELS\[key\]/);
});

test('页脚面板：网站 beta 将排版策略合并进文章底部拖尾', () => {
  assert.match(siteHtml, /data-site-section="footer-trailer"/);
  assert.match(siteHtml, /<span class="site-section-title">文章底部拖尾<\/span>/);
  assert.match(siteHtml, /data-site-section="footer-trailer"[\s\S]*class="site-footer-strategy-inline"/);
  assert.match(siteHtml, /data-site-section="footer-trailer"[\s\S]*data-site-strategy="original"[\s\S]{0,220}>[\s\S]*原创/);
  assert.match(siteHtml, /data-site-section="footer-trailer"[\s\S]*data-site-strategy="repost"[\s\S]{0,220}>[\s\S]*转载/);
  assert.doesNotMatch(siteHtml, /class="ft-footer-statusbar"/);
  assert.doesNotMatch(siteHtml, /id="footerStrategyPill"/);
  assert.doesNotMatch(siteHtml, /id="footerStatusText"/);
  assert.doesNotMatch(siteHtml, /data-footer-reset-default/);
  assert.doesNotMatch(siteHtml, /data-footer-manage-modules/);
  assert.match(siteHtml, /id="footerEnableToggle"[\s\S]{0,220}hidden[\s\S]{0,220}role="switch"/);
  assert.match(siteHtml, /id="footerPresets"[\s\S]{0,80}hidden/);
  assert.match(siteHtml, /id="footerOffNote"[\s\S]{0,80}hidden/);
  assert.match(siteHtml, /data-footer-area-switch="enabled"/);
  assert.doesNotMatch(siteHtml, /<input type="checkbox" id="footerEnableToggle"/);
  assert.doesNotMatch(siteHtml, /<input type="checkbox" data-seg=/);
});

test('页脚面板：模块卡片列表保留拖拽、预览、开关和图片替换能力', () => {
  assert.match(siteHtml, /import \{ createIcon \} from '\/js\/ui-icons\.js';/);
  assert.match(siteHtml, /function\s+hydrateFooterDragHandles\(\)/);
  assert.match(siteHtml, /createIcon\('gripVertical'\)/);
  assert.match(siteHtml, /class="ft-footer-table"/);
  assert.match(siteHtml, /class="ft-footer-segments" id="footerSegments" role="list" aria-label="文章底部拖尾模块"/);
  assert.match(siteHtml, /class="ft-footer-seg-row \$\{footerSegmentNeedsDetail\(key\) \? '' : 'ft-footer-seg-row--compact'\}" role="listitem"/);
  assert.doesNotMatch(siteHtml, /role="table"/);
  assert.doesNotMatch(siteHtml, /role="rowgroup"/);
  assert.doesNotMatch(siteHtml, /role="row"/);
  assert.doesNotMatch(siteHtml, /role="cell"/);
  assert.doesNotMatch(siteHtml, /role="columnheader"/);
  assert.doesNotMatch(siteHtml, /ft-footer-table-head/);
  assert.match(siteHtml, /data-footer-drag-slot/);
  assert.match(siteHtml, /data-footer-drag-handle/);
  assert.match(siteHtml, /class="ft-footer-cell ft-footer-cell-module"/);
  assert.match(siteHtml, /class="ft-footer-cell ft-footer-cell-preview"/);
  assert.match(siteHtml, /class="ft-footer-cell ft-footer-cell-status"/);
  assert.match(siteHtml, /class="ft-footer-cell ft-footer-cell-actions"/);
  assert.match(siteHtml, /footerSegmentNeedsDetail\(key\)/);
  assert.match(siteHtml, /data-footer-seg="\$\{escText\(key\)\}"[\s\S]{0,260}role="switch"/);
  assert.match(siteHtml, /footerSegmentPreviewHtml\(key\)/);
  assert.match(siteHtml, /data-footer-asset-pick/);
  assert.match(siteHtml, /data-footer-asset-reset/);
  assert.doesNotMatch(siteHtml, /ft-footer-muted-action/);
  assert.doesNotMatch(siteHtml, />默认<\/span>/);
  assert.equal(siteHtml.includes(removedStateAttr), false);
  assert.equal(siteHtml.includes(removedStateClass), false);
  assert.match(siteCss, /grid-template-areas:\s*["']module actions status["'][\s\S]{0,80}["']preview preview preview["']/);
  assert.match(siteCss, /\.ft-footer-cell-module\s*\{[\s\S]{0,120}grid-area:\s*module/);
  assert.match(siteCss, /\.ft-footer-cell-preview\s*\{[\s\S]{0,120}grid-area:\s*preview/);
  assert.match(siteCss, /\.ft-footer-cell-status\s*\{[\s\S]{0,120}grid-area:\s*status/);
  assert.match(siteCss, /\.ft-footer-cell-actions\s*\{[\s\S]{0,120}grid-area:\s*actions/);
  assert.match(siteCss, /\.ft-footer-seg-title\s*\{[\s\S]{0,160}white-space:\s*normal/);
  assert.match(siteCss, /\.ft-footer-preview-text\s*\{[\s\S]{0,160}white-space:\s*normal/);
  assert.match(siteCss, /\.site-footer-strategy-inline \.site-strategy-card\s*\{[\s\S]{0,120}min-height:\s*36px/);
  assert.match(siteCss, /\.site-footer-strategy-inline \.site-strategy-desc\s*\{[\s\S]{0,80}display:\s*none/);
  assert.match(siteCss, /\.ft-footer-table\s*\{[\s\S]{0,120}border:\s*0/);
  assert.match(siteCss, /\.ft-footer-segments\s*\{[\s\S]{0,120}display:\s*grid[\s\S]{0,80}gap:\s*8px/);
  assert.match(siteCss, /\.ft-footer-segments \.ft-footer-seg-row\s*\{[\s\S]{0,220}grid-template-columns:\s*minmax\(0,\s*1fr\) auto 44px/);
  assert.match(siteCss, /\.ft-footer-segments \.ft-footer-seg-row\s*\{[\s\S]{0,260}border:\s*1px solid var\(--fs-border-card\)/);
  assert.match(siteCss, /\.ft-footer-seg-row--compact\s*\{[\s\S]{0,120}grid-template-areas:\s*["']module actions status["']/);
  assert.match(siteCss, /\.ft-footer-seg-row--compact \.ft-footer-cell-preview\s*\{[\s\S]{0,80}display:\s*none/);
  assert.match(siteCss, /\.ft-footer-cell-preview\s*\{[\s\S]{0,180}margin:\s*8px 0 0 30px/);
  assert.match(siteCss, /\.ft-footer-cell-preview\s*\{[\s\S]{0,220}background:\s*transparent/);
  assert.match(siteCss, /\.ft-footer-cell-actions \.ft-footer-asset-actions\s*\{[\s\S]{0,120}justify-content:\s*flex-end/);
  assert.match(siteCss, /\.ft-footer-cell-actions \.ft-footer-asset-btn\s*\{[\s\S]{0,120}white-space:\s*nowrap/);
  assert.match(siteCss, /\.ft-footer-cell-actions:empty\s*\{[\s\S]{0,80}display:\s*none/);
});
