import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const editorSrc = await readFile(new URL('../public/js/asset-library-rule-editor.js', import.meta.url), 'utf8');
const adminSrc = await readFile(new URL('../public/js/admin-panel.js', import.meta.url), 'utf8');
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
  assert.match(editorSrc, /SCOPES/);
  assert.match(editorSrc, /USAGES/);
  assert.match(editorSrc, /isSafeURL/);
  assert.match(editorSrc, /https:/);
  assert.match(editorSrc, /startsWith\('\/'\)/);
  assert.match(editorSrc, /asset-lib-error/);
});

test('规则后台有素材库页签，并保存 workingAssets 到 asset_library', () => {
  assert.match(adminSrc, /createAssetLibraryEditor/);
  assert.match(adminSrc, /\['assets', '素材库'\]/);
  assert.match(adminSrc, /data-role="asset-library-editor"/);
  assert.match(adminSrc, /workingAssets/);
  assert.match(adminSrc, /configDoc\.modules\.asset_library/);
  assert.match(adminSrc, /assetLibraryFromPreset\(selected\)/);
  assert.ok(adminSrc.split('\n').length <= 900, 'admin-panel.js 必须不超过 900 行');
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
