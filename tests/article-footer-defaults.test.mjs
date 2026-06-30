import test from 'node:test';
import assert from 'node:assert/strict';

const {
  mergeArticleArtifactOverrides,
  resolveArticleArtifactDefaultsForArticle,
  resolveBottomBlocksForArticle,
} = await import('../public/js/article-footer-defaults.js?test=' + Date.now());

function fbifBottomBlocks() {
  const shared = (noteText) => ({
    note: { type: 'text', enabled: true, fields: { content: noteText } },
    byline: { type: 'image', display_name: 'Bobo 名片', enabled: true, fields: { image: '{{asset#fbif_bobo_card}}' } },
    more_articles: { type: 'more_articles', display_name: '更多文章', enabled: true, fields: { title: '/ 更多文章 /' } },
  });
  return {
    schema_version: 2,
    enabled: true,
    active_preset_id: 'original',
    presets: [
      { id: 'original', name: '原创', order: ['note', 'byline', 'more_articles'], blocks: shared('原创声明') },
      { id: 'repost', name: '转载', order: ['note', 'byline', 'more_articles'], blocks: shared('转载声明') },
    ],
  };
}

const footerDefaults = {
  enabled: true,
  author_aliases: {
    mote: ['Mote莫特', 'Mote 莫特'],
  },
  profiles: [
    {
      id: 'mote_author',
      name: 'Mote 作者默认',
      match: { author_alias_group: 'mote' },
      block_visibility: {
        byline: false,
        more_articles: true,
      },
      artifact_visibility: {
        mote_ref_author_card: false,
      },
    },
  ],
};

test('Mote 作者文章叠加可见性默认，同时保留转载 preset 内容', () => {
  const resolved = resolveBottomBlocksForArticle({
    module: fbifBottomBlocks(),
    presetId: 'repost',
    articleMeta: { author: 'Mote 莫特', author_normalized: 'mote莫特' },
    footerDefaults,
  });

  assert.equal(resolved.articleFooterProfile.id, 'mote_author');
  assert.equal(resolved.articleFooterProfile.name, 'Mote 作者默认');
  assert.deepEqual(resolved.articleFooterProfile.artifactVisibility, { mote_ref_author_card: false });
  assert.equal(resolved.blocks.find(b => b.id === 'note').fields.content.value, '转载声明');
  assert.equal(resolved.blocks.find(b => b.id === 'byline').enabled, false);
  assert.equal(resolved.blocks.find(b => b.id === 'more_articles').enabled, true);
});

test('Mote 作者默认方案也能声明文中元素默认隐藏', () => {
  const defaults = resolveArticleArtifactDefaultsForArticle({
    articleMeta: { author: 'Mote莫特', author_normalized: 'mote莫特' },
    footerDefaults,
  });

  assert.deepEqual(defaults, { mote_ref_author_card: false });
});

test('本篇文中元素例外可以覆盖作者默认隐藏', () => {
  const merged = mergeArticleArtifactOverrides(
    { mote_ref_author_card: false },
    { mote_ref_author_card: 'show' },
  );

  assert.deepEqual(merged, { mote_ref_author_card: 'show' });
});

test('普通作者不命中 Mote 默认，继续跟随后台 bottom_blocks', () => {
  const resolved = resolveBottomBlocksForArticle({
    module: fbifBottomBlocks(),
    presetId: 'original',
    articleMeta: { author: '张三', author_normalized: '张三' },
    footerDefaults,
  });

  assert.equal(resolved.articleFooterProfile, null);
  assert.equal(resolved.blocks.find(b => b.id === 'note').fields.content.value, '原创声明');
  assert.equal(resolved.blocks.find(b => b.id === 'byline').enabled, true);
});

test('作者默认方案支持用短别名包含匹配作者名', () => {
  const resolved = resolveBottomBlocksForArticle({
    module: fbifBottomBlocks(),
    presetId: 'original',
    articleMeta: { author: 'Mote莫特', author_normalized: 'mote莫特' },
    footerDefaults: {
      enabled: true,
      author_aliases: { mote: ['mote'] },
      profiles: [{
        id: 'mote_author',
        name: 'Mote 作者默认',
        match: { author_alias_group: 'mote' },
        block_visibility: { byline: false },
      }],
    },
  });

  assert.equal(resolved.articleFooterProfile.id, 'mote_author');
  assert.equal(resolved.blocks.find(b => b.id === 'byline').enabled, false);
});

test('本篇例外覆盖文章类型默认，但不改 bottom_blocks 基础结构', () => {
  const resolved = resolveBottomBlocksForArticle({
    module: fbifBottomBlocks(),
    presetId: 'original',
    articleMeta: { author: 'Mote莫特', author_normalized: 'mote莫特' },
    footerDefaults,
    visibilityOverride: {
      blocks: {
        byline: 'show',
        more_articles: 'hide',
      },
    },
  });

  assert.equal(resolved.articleFooterProfile.id, 'mote_author');
  assert.equal(resolved.blocks.find(b => b.id === 'byline').enabled, true);
  assert.equal(resolved.blocks.find(b => b.id === 'more_articles').enabled, false);
});

test('旧 content override 的显隐不能绕过作者默认', () => {
  const resolved = resolveBottomBlocksForArticle({
    module: fbifBottomBlocks(),
    presetId: 'original',
    articleMeta: { author: 'Mote莫特', author_normalized: 'mote莫特' },
    footerDefaults,
    contentOverride: {
      enabled: true,
      order: ['byline', 'note'],
      overrides: {
        byline: { enabled: true },
      },
    },
  });

  assert.equal(resolved.articleFooterProfile.id, 'mote_author');
  assert.equal(resolved.enabled, true);
  assert.equal(resolved.blocks.find(b => b.id === 'byline').enabled, false);
  assert.deepEqual(resolved.blocks.map(b => b.id), ['note', 'byline', 'more_articles']);
});

test('本篇例外可以隐藏整个底部区域', () => {
  const resolved = resolveBottomBlocksForArticle({
    module: fbifBottomBlocks(),
    presetId: 'original',
    articleMeta: { author: 'Mote莫特', author_normalized: 'mote莫特' },
    footerDefaults,
    visibilityOverride: { enabled: 'hide' },
  });

  assert.equal(resolved.articleFooterProfile.id, 'mote_author');
  assert.equal(resolved.enabled, false);
});
