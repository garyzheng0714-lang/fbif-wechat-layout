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

// ---- 作者规则手动选择（2026-07-11 用户裁定：选择器覆盖自动识别）----
// WHY：自动识别失败（作者行未识别）时整套作者规则曾静默消失且无法补救；
// 手选 override 必须能强制套用 profile（含行为身份注入），也必须能强制回通用默认。
const { resolveArticleFooterSelection, listArticleFooterProfiles, normalizeArticleFooterProfileOverride } =
  await import('../public/js/article-footer-defaults.js?selection=' + Date.now());

test('选择器：无 override 时自动匹配（现状不变）', () => {
  const s = resolveArticleFooterSelection({
    articleMeta: { author: 'Mote莫特' },
    footerDefaults,
  });
  assert.equal(s.profile && s.profile.id, 'mote_author');
  assert.equal(s.autoProfile && s.autoProfile.id, 'mote_author');
  assert.equal(s.authorNormalizedOverride, undefined, '自动路径不覆盖行为身份');
});

test('选择器：作者未识别时手选 profile 强制套用并注入行为身份', () => {
  const s = resolveArticleFooterSelection({
    articleMeta: { author: '' },
    footerDefaults,
    profileOverride: 'mote_author',
  });
  assert.equal(s.profile && s.profile.id, 'mote_author', '识别失败也能手动套用');
  assert.equal(s.authorNormalizedOverride, 'mote莫特', '注入别名组身份以触发署名图等行为');
});

test('选择器：手选「通用默认」中和作者特殊规则（识别到 Mote 也不套）', () => {
  const s = resolveArticleFooterSelection({
    articleMeta: { author: 'Mote莫特' },
    footerDefaults,
    profileOverride: 'none',
  });
  assert.equal(s.profile, null);
  assert.equal(s.authorNormalizedOverride, '', '身份中和，按作者触发的行为全关');
});

test('选择器：override 指向已删 profile 时回退自动（规则改名不炸旧会话）', () => {
  const s = resolveArticleFooterSelection({
    articleMeta: { author: 'Mote莫特' },
    footerDefaults,
    profileOverride: 'deleted_profile',
  });
  assert.equal(s.profile && s.profile.id, 'mote_author');
});

test('选择器：列表只含已配置 profiles；defaults 关闭时为空', () => {
  assert.deepEqual(listArticleFooterProfiles(footerDefaults), [{ id: 'mote_author', name: 'Mote 作者默认' }]);
  assert.deepEqual(listArticleFooterProfiles({ ...footerDefaults, enabled: false }), []);
  assert.deepEqual(listArticleFooterProfiles(null), []);
});

test('选择器：resolveBottomBlocksForArticle 透传 profileOverride', () => {
  const noAuthor = resolveBottomBlocksForArticle({
    module: fbifBottomBlocks(),
    articleMeta: { author: '' },
    footerDefaults,
    profileOverride: 'mote_author',
  });
  assert.equal(noAuthor.articleFooterProfile && noAuthor.articleFooterProfile.id, 'mote_author');
  const neutralized = resolveBottomBlocksForArticle({
    module: fbifBottomBlocks(),
    articleMeta: { author: 'Mote莫特' },
    footerDefaults,
    profileOverride: 'none',
  });
  assert.equal(neutralized.articleFooterProfile, null);
});

test('选择器：normalize 空值→undefined（自动）', () => {
  assert.equal(normalizeArticleFooterProfileOverride(''), undefined);
  assert.equal(normalizeArticleFooterProfileOverride(null), undefined);
  assert.equal(normalizeArticleFooterProfileOverride('  mote_author '), 'mote_author');
});
