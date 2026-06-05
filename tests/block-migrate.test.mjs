// block-migrate.test.mjs — 旧 type → 4 原子结构迁移（切生产前置）。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { migrateLegacyBlock, migrateLegacyModule } from '../public/js/blocks/block-migrate.js';

test('rich_html → text{html}（html_lines join）', () => {
  const out = migrateLegacyBlock({ type: 'rich_html', enabled: true, fields: { html_lines: ['<p>a</p>', '<p>b</p>'] } });
  assert.equal(out.length, 1);
  assert.equal(out[0].type, 'text');
  assert.equal(out[0].fields.html, '<p>a</p>\n<p>b</p>');
});

test('declaration → text{html}（title 加粗 + lines）', () => {
  const out = migrateLegacyBlock({ type: 'declaration', fields: { title: '提示：', lines: ['<p>转载请联系</p>'] } });
  assert.equal(out[0].type, 'text');
  assert.match(out[0].fields.html, /<p><strong>提示：<\/strong><\/p>\n<p>转载请联系<\/p>/);
});

test('name_card 有 intro → text + image（一对多）', () => {
  const out = migrateLegacyBlock({ type: 'name_card', enabled: true, fields: { intro_text: '关注我们', intro_size: 14, intro_color: '#888888', image: '{{asset#bobo}}' } });
  assert.equal(out.length, 2);
  assert.equal(out[0].type, 'text');
  assert.match(out[0].fields.html, /关注我们/);
  assert.equal(out[1].type, 'image');
  assert.equal(out[1].fields.image, '{{asset#bobo}}');
});

test('qr_card 无 caption → 只 image', () => {
  const out = migrateLegacyBlock({ type: 'qr_card', fields: { caption: '', image: '{{asset#qr}}' } });
  assert.equal(out.length, 1);
  assert.equal(out[0].type, 'image');
  assert.equal(out[0].fields.image, '{{asset#qr}}');
});

test('titled_media → text(title 加粗) + image', () => {
  const out = migrateLegacyBlock({ type: 'titled_media', fields: { title: '/ 案例 /', title_size: 17, title_color: '#544545', center: true, image: '{{asset#promo}}' } });
  assert.equal(out.length, 2);
  assert.match(out[0].fields.html, /font-weight: bold/);
  assert.match(out[0].fields.html, /案例/);
  assert.equal(out[1].fields.image, '{{asset#promo}}');
});

test('已是原子（text/image/spacer）原样保留', () => {
  for (const t of ['text', 'image', 'more_articles', 'spacer']) {
    const b = { type: t, fields: {} };
    assert.deepEqual(migrateLegacyBlock(b), [b]);
  }
});

test('migrateLegacyModule：遍历 order，一对多重建 order/blocks，保留模块字段', () => {
  const mod = {
    enabled: true, schema_version: 1,
    order: ['promo', 'card'],
    blocks: {
      promo: { type: 'titled_media', fields: { title: 'T', image: '{{asset#p}}' } },
      card: { type: 'name_card', fields: { intro_text: '', image: '{{asset#c}}' } },
    },
  };
  const out = migrateLegacyModule(mod);
  // promo → text(promo) + image(promo_m1)；card 无 intro → image(card)
  assert.deepEqual(out.order, ['promo', 'promo_m1', 'card']);
  assert.equal(out.blocks.promo.type, 'text');
  assert.equal(out.blocks.promo_m1.type, 'image');
  assert.equal(out.blocks.card.type, 'image');
  assert.equal(out.enabled, true);       // 模块字段保留
  assert.equal(out.schema_version, 1);
});
