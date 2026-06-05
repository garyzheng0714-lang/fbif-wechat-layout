// asset-upload.js — 上传素材并入库的公共逻辑（素材库后台 tab + 图片字段选图器共用，codex 条11）。
//
// 中性：不 import 任何渲染器、不顶层 import uploader（红线#19 + 可单测）。上传实现由调用方注入
// （uploadFn = uploader.js 的 uploadAssetFile），本模块只负责「上传 → 构造一个新 asset」这段纯逻辑。
// 调用方负责把返回的 asset 加入 config.modules.asset_library 并在 picker 中选中（默认入库，用户裁定不弹确认）。

function hashStr(s) {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return Math.abs(h).toString(36);
}

// uploadAssetIntoLibrary 上传 file 并构造一个新 asset（不修改任何外部状态）。
//   scope:    入库作用域（账号 id 或 'global'）
//   genID:    id 生成器（注入；缺省按 url 派生确定性 id）
//   nowISO:   时间戳（注入；缺省 new Date().toISOString()）
//   uploadFn: 上传实现（必需注入；返回素材 url）
export async function uploadAssetIntoLibrary({ file, scope = 'global', genID, nowISO, uploadFn } = {}) {
  if (!file) throw new Error('uploadAssetIntoLibrary: no file');
  if (typeof uploadFn !== 'function') throw new Error('uploadAssetIntoLibrary: uploadFn required');
  const url = await uploadFn(file);
  const id = genID ? genID('asset') : 'asset_' + hashStr(String(url));
  return {
    id,
    type: 'image',
    usage: 'custom',
    scope,
    label: String(file.name || '素材').replace(/\.[^.]+$/, ''),
    url,
    tags: ['上传'],
    updated_at: nowISO || new Date().toISOString(),
  };
}
