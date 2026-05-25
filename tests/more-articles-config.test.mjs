import test from 'node:test';
import assert from 'node:assert/strict';
import {
  SYSTEM_DEFAULT_RECOMMENDED_CARDS,
  emptyMoreArticleCard,
  normalizeMoreArticleDefaultCard,
  normalizeMoreArticleDefaultCards,
  moveMoreArticleDefaultCard,
} from '../public/js/more-articles-config.js';

test('system defaults keep the current three recommended articles', () => {
  assert.equal(SYSTEM_DEFAULT_RECOMMENDED_CARDS.length, 3);
  assert.equal(
    SYSTEM_DEFAULT_RECOMMENDED_CARDS[0].title,
    '没想到！英国人把豆腐做到英国第一，还像卖衣服一样出XXL超大号！'
  );
  assert.match(SYSTEM_DEFAULT_RECOMMENDED_CARDS[0].href, /^https:\/\/mp\.weixin\.qq\.com\//);
  assert.match(SYSTEM_DEFAULT_RECOMMENDED_CARDS[0].imgurl, /^https:\/\/mmbiz\.qpic\.cn\//);
});

test('default article card normalization keeps only editor-safe fields', () => {
  const card = normalizeMoreArticleDefaultCard({
    href: '  https://mp.weixin.qq.com/s/card-a  ',
    title: '  标题 A  ',
    imgurl: ' //mmbiz.qpic.cn/a.jpg ',
    image_urls: [' https://mmbiz.qpic.cn/body.jpg ', '', 'https://mmbiz.qpic.cn/body.jpg'],
    cover_data_url: 'data:image/jpeg;base64,custom',
    composite_url: 'https://oss.example.com/generated.png',
    composite_hash: 'hash',
  });

  assert.deepEqual(card, {
    href: 'https://mp.weixin.qq.com/s/card-a',
    title: '标题 A',
    imgurl: 'https://mmbiz.qpic.cn/a.jpg',
    image_urls: ['https://mmbiz.qpic.cn/body.jpg'],
    cover_data_url: null,
    crop: { x: 0.5, y: 0.5, scale: 1 },
  });
});

test('empty cards are removed from rule defaults', () => {
  assert.deepEqual(
    normalizeMoreArticleDefaultCards([
      emptyMoreArticleCard(),
      { href: 'https://mp.weixin.qq.com/s/keep', title: '保留' },
    ]),
    [{
      href: 'https://mp.weixin.qq.com/s/keep',
      title: '保留',
      imgurl: '',
      image_urls: [],
      cover_data_url: null,
      crop: { x: 0.5, y: 0.5, scale: 1 },
    }]
  );
});

test('card reorder helper moves one card without mutating the input', () => {
  const input = [
    { href: 'https://mp.weixin.qq.com/s/a' },
    { href: 'https://mp.weixin.qq.com/s/b' },
    { href: 'https://mp.weixin.qq.com/s/c' },
  ];
  const output = moveMoreArticleDefaultCard(input, 2, -1);

  assert.deepEqual(output.map(c => c.href), [
    'https://mp.weixin.qq.com/s/a',
    'https://mp.weixin.qq.com/s/c',
    'https://mp.weixin.qq.com/s/b',
  ]);
  assert.deepEqual(input.map(c => c.href), [
    'https://mp.weixin.qq.com/s/a',
    'https://mp.weixin.qq.com/s/b',
    'https://mp.weixin.qq.com/s/c',
  ]);
});
