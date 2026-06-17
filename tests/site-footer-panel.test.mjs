import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const siteHtml = readFileSync(new URL('../public/site.html', import.meta.url), 'utf8');

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
