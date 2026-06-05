import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const editorSrc = await readFile(new URL('../public/js/asset-library-rule-editor.js', import.meta.url), 'utf8');
const adminSrc = await readFile(new URL('../public/js/admin-panel.js', import.meta.url), 'utf8');
const blockEditorSrc = await readFile(new URL('../public/js/admin/block-editor.js', import.meta.url), 'utf8');
const blockPanelSrc = await readFile(new URL('../public/js/blocks/block-panel.js', import.meta.url), 'utf8');
const footerEditorSrc = await readFile(new URL('../public/js/footer-library-rule-editor.js', import.meta.url), 'utf8');
const uploaderSrc = await readFile(new URL('../public/js/uploader.js', import.meta.url), 'utf8');

test('素材库编辑器导出并复用上传入口', () => {
  assert.match(editorSrc, /export function createAssetLibraryEditor/);
  assert.match(editorSrc, /uploadAssetFile/);
  assert.match(editorSrc, /data-action="upload-new"/);
  assert.match(editorSrc, /data-action="upload-replace"/);
  assert.match(editorSrc, /modules\.asset_library|assetLibrary/);
});

test('素材库编辑器有 scope 和 usage 控件，并做安全 URL 校验', () => {
  assert.match(editorSrc, /listRulePacks/);
  assert.match(editorSrc, /scopeOptions/);
  assert.match(editorSrc, /USAGES/);
  assert.match(editorSrc, /tagOptions/);
  assert.match(editorSrc, /asset-lib-group-title/);
  assert.match(editorSrc, /data-action="filter-tag"/);
  assert.match(editorSrc, /isSafeURL/);
  assert.match(editorSrc, /https:/);
  assert.match(editorSrc, /startsWith\('\/'\)/);
  assert.match(editorSrc, /asset-lib-error/);
});

test('规则后台有素材库页签，并保存 workingAssets 到 asset_library', () => {
  assert.match(adminSrc, /createAssetLibraryEditor/);
  assert.match(adminSrc, /\['bottom', '底部'\]/);
  assert.match(adminSrc, /data-role="asset-library-editor"/);
  assert.match(adminSrc, /admin-bottom-layout/);
  assert.match(adminSrc, /compact:\s*true/);
  assert.match(adminSrc, /data-role="block-editor"/);
  assert.match(adminSrc, /workingAssets/);
  assert.match(adminSrc, /configDoc\.modules\.asset_library/);
  assert.match(adminSrc, /assetLibraryFromPreset\(selected\)/);
  assert.match(adminSrc, /adminDirtyCount/);
  assert.ok(adminSrc.split('\n').length <= 900, 'admin-panel.js 必须不超过 900 行');
});

test('素材库编辑器按引用索引阻断删除，并把素材 ID 设为只读', () => {
  assert.match(editorSrc, /export function buildAssetReferenceIndex/);
  assert.match(editorSrc, /被 \${refs\.length} 处引用/);
  assert.match(editorSrc, /readonly title="素材 ID 创建后不可改/);
  assert.match(editorSrc, /该素材仍被 /);
  assert.doesNotMatch(editorSrc, /if \(field === 'id'\) asset\.id/);
});

test('底部区块编辑器支持实例显示名，并在缺省时显示标题、说明或素材标签', () => {
  assert.match(blockEditorSrc, /function blockDisplayLabel/);
  assert.match(blockEditorSrc, /display_name/);
  assert.match(blockEditorSrc, /模块名称/);
  assert.match(blockEditorSrc, /KIND_LABELS/);
  assert.match(blockEditorSrc, /模块间默认空行/);
  assert.match(blockEditorSrc, /setBlockDisplayName/);
  assert.match(blockPanelSrc, /displayName/);
  assert.match(blockEditorSrc, /assetIDFromRef/);
  assert.match(blockEditorSrc, /block\.type === 'name_card'/);
  assert.match(blockEditorSrc, /getAssetLibrary \? getAssetLibrary\(\)/);
  assert.match(blockEditorSrc, /blockDisplayLabel\(block, type/);
  assert.match(blockPanelSrc, /block\.type === 'name_card'/);
});

test('素材库引用索引能扫描规则模块里的 #id 与旧 usage token', async () => {
  const { buildAssetReferenceIndex } = await import('../public/js/asset-library-rule-editor.js?refs=' + Date.now());
  const index = buildAssetReferenceIndex([{
    id: 'rule-a',
    name: '规则 A',
    config: {
      modules: {
        bottom_blocks: { blocks: { card: { fields: { image: '{{asset#card_a}}' } } } },
        fbif_footer: { segments: { logo: { html_lines: ['<img src="{{asset:logo}}">'] } } },
      },
    },
  }], [
    { id: 'card_a', usage: 'avatar' },
    { id: 'logo_a', usage: 'logo' },
  ]);

  assert.equal(index.card_a.length, 1);
  assert.equal(index.logo_a.length, 1);
  assert.equal(index.card_a[0].presetName, '规则 A');
  assert.match(index.logo_a[0].path, /fbif_footer/);
});

test('上传模块暴露加入素材库用的图片上传函数', () => {
  assert.match(uploaderSrc, /export async function uploadAssetFile/);
  assert.match(uploaderSrc, /请选择图片文件/);
  assert.match(uploaderSrc, /图片上传失败/);
});

test('页脚编辑器支持素材选择、拖拽排序和真实预览', () => {
  assert.match(footerEditorSrc, /assetLibrary/);
  assert.match(footerEditorSrc, /data-action="pick-asset"/);
  assert.match(footerEditorSrc, /draggable="true"/);
  assert.match(footerEditorSrc, /dragstart/);
  assert.match(footerEditorSrc, /buildFooterHtml/);
  assert.match(footerEditorSrc, /footer-lib-preview/);
  assert.match(footerEditorSrc, /data-action="toggle-enabled"/);
});
