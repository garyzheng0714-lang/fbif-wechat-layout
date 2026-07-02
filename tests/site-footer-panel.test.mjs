import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const siteHtml = readFileSync(new URL('../public/site.html', import.meta.url), 'utf8');
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

test('页脚面板：网站侧使用底部区域胶囊开关，不再暴露 checkbox', () => {
  // WHY：网站侧和公众号侧应共用“底部区域 → 子模块”的低摩擦心智；
  // 普通编辑不应该在前台看到复选框式页脚库。
  assert.match(siteHtml, /<summary class="ft-adv-summary ft-footer-summary">/);
  assert.match(siteHtml, /<span class="ft-footer-summary-title">底部区域<\/span>/);
  assert.match(siteHtml, /id="footerEnableToggle"[\s\S]{0,160}role="switch"/);
  assert.match(siteHtml, /data-footer-area-switch="enabled"/);
  assert.match(siteHtml, /data-footer-seg="\$\{escText\(key\)\}"[\s\S]{0,260}role="switch"/);
  assert.doesNotMatch(siteHtml, /<input type="checkbox" id="footerEnableToggle"/);
  assert.doesNotMatch(siteHtml, /<input type="checkbox" data-seg=/);
});

test('页脚面板：片段行左侧是拖拽占位和标题，右侧是开关，且不显示状态标签', () => {
  // WHY：面板和公众号区块面板统一为“左标题、右开关”的行结构；
  // 显示/隐藏文案会重复开关状态，只保留 aria 状态。
  assert.match(siteHtml, /import \{ createIcon \} from '\/js\/ui-icons\.js';/);
  assert.match(siteHtml, /function\s+hydrateFooterDragHandles\(\)/);
  assert.match(siteHtml, /createIcon\('gripVertical'\)/);
  assert.match(siteHtml, /data-footer-drag-slot/);
  assert.match(siteHtml, /data-footer-drag-handle/);
  assert.match(siteHtml, /<span class="ft-footer-seg-title">\$\{escText\(footerSegmentLabel\(key\)\)\}<\/span>[\s\S]{0,220}<button type="button" class="ft-switch" data-footer-seg=/);
  assert.equal(siteHtml.includes(removedStateAttr), false);
  assert.equal(siteHtml.includes(removedStateClass), false);
});
