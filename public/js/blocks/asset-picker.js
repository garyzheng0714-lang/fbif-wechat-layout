// asset-picker.js — 素材库选图弹层（前台单篇区块面板用）。
//
// 中性：只读 config.modules.asset_library 数据，不 import 任何渲染器（红线 #19）。
// 点选写回 {{asset#id}}（按 id 引用，替换全局生效）；运营永远看不到内部语法。

function isObj(v) { return !!v && typeof v === 'object' && !Array.isArray(v); }
function assetsOf(lib) {
  if (Array.isArray(lib)) return lib;
  if (isObj(lib) && Array.isArray(lib.assets)) return lib.assets;
  return [];
}
function inScope(a, scope) {
  if (!scope) return true; // 无 scope（后台管理员视图）：列出全部素材
  const s = (a && a.scope) || 'global';
  return s === 'global' || s === scope;
}
function esc(s) {
  return String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

const STYLE_ID = 'ap-style';
function ensureStyle(doc) {
  if (doc.getElementById(STYLE_ID)) return;
  const st = doc.createElement('style');
  st.id = STYLE_ID;
  st.textContent = `
.ap-mask{position:fixed;inset:0;background:rgba(0,0,0,.4);z-index:9999;display:flex;align-items:center;justify-content:center;}
.ap-modal{background:#fff;border-radius:8px;width:min(560px,92vw);max-height:80vh;display:flex;flex-direction:column;box-shadow:0 8px 32px rgba(0,0,0,.2);}
.ap-head{display:flex;align-items:center;justify-content:space-between;padding:14px 16px;border-bottom:1px solid #eee;font-weight:600;font-size:14px;}
.ap-close{border:none;background:none;font-size:20px;cursor:pointer;color:#888;line-height:1;padding:0 4px;}
.ap-body{padding:12px 16px;overflow:auto;}
.ap-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(120px,1fr));gap:10px;}
.ap-item{border:1px solid #e5e6eb;border-radius:6px;padding:6px;cursor:pointer;text-align:center;transition:border-color .15s;}
.ap-item:hover{border-color:#1456f0;}
.ap-item img{width:100%;height:72px;object-fit:contain;background:#f7f8fa;border-radius:4px;}
.ap-item .ap-label{font-size:11px;color:#646a73;margin-top:4px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;}
.ap-empty{color:#8f959e;font-size:13px;padding:20px 8px;text-align:center;line-height:1.6;}
.ap-foot{padding:8px 16px 14px;border-top:1px solid #f2f3f5;text-align:right;}
.ap-clear{background:none;border:none;color:#8f959e;cursor:pointer;text-decoration:underline;font-size:12px;}
.ap-clear:hover{color:#f54a45;}
`;
  (doc.head || doc.documentElement).appendChild(st);
}

// openAssetPicker 弹出选图层。onPick(assetId) 选中某图；onClear() 清空（不显示该图）。
export function openAssetPicker({ assetLibrary, scope = 'global', doc = document, onPick, onClear }) {
  ensureStyle(doc);
  const all = assetsOf(assetLibrary).filter(a => isObj(a) && a.url && inScope(a, scope));
  const mask = doc.createElement('div'); mask.className = 'ap-mask';
  const modal = doc.createElement('div'); modal.className = 'ap-modal';

  const head = doc.createElement('div'); head.className = 'ap-head';
  const title = doc.createElement('span'); title.textContent = '从素材库选择图片';
  const close = doc.createElement('button'); close.className = 'ap-close'; close.textContent = '×';
  close.onclick = () => mask.remove();
  head.appendChild(title); head.appendChild(close);
  modal.appendChild(head);

  const body = doc.createElement('div'); body.className = 'ap-body';
  if (!all.length) {
    const e = doc.createElement('div'); e.className = 'ap-empty';
    e.textContent = '该账号素材库暂无可选图片。请在规则后台「素材库」添加，或更换账号。';
    body.appendChild(e);
  } else {
    const grid = doc.createElement('div'); grid.className = 'ap-grid';
    for (const a of all) {
      const item = doc.createElement('div'); item.className = 'ap-item';
      item.innerHTML = `<img src="${esc(a.url)}" alt="" loading="lazy"><div class="ap-label" title="${esc(a.label || a.id)}">${esc(a.label || a.id)}</div>`;
      item.onclick = () => { if (onPick) onPick(a.id); mask.remove(); };
      grid.appendChild(item);
    }
    body.appendChild(grid);
  }
  modal.appendChild(body);

  if (onClear) {
    const foot = doc.createElement('div'); foot.className = 'ap-foot';
    const clr = doc.createElement('button'); clr.className = 'ap-clear'; clr.textContent = '清除图片（该区块不显示图）';
    clr.onclick = () => { onClear(); mask.remove(); };
    foot.appendChild(clr); modal.appendChild(foot);
  }

  mask.appendChild(modal);
  mask.onclick = (e) => { if (e.target === mask) mask.remove(); };
  (doc.body || doc.documentElement).appendChild(mask);
}

// resolveAssetThumb 把字段值（{{asset#id}} / {{asset:usage}} / 直接 url）解析成可显示的缩略图 url。
export function resolveAssetThumb(value, assetLibrary, scope = 'global') {
  const v = String(value == null ? '' : value).trim();
  if (!v) return '';
  const m = /^\{\{asset(?::([a-z0-9_-]+)|#([a-z0-9_-]+))\}\}$/i.exec(v);
  if (!m) return v; // 直接 url
  const byUsage = m[1];
  const byId = m[2];
  for (const a of assetsOf(assetLibrary)) {
    if (!inScope(a, scope)) continue;
    if (byId && a.id === byId) return a.url || '';
    if (byUsage && a.usage === byUsage) return a.url || '';
  }
  return '';
}
