// table-renderer.js — neutral inline table renderer shared by isolated templates.
// It imports no account/channel renderer, so FBIF / FoodTalks / Series stay physically separated.

export function renderTableCellRuns(runs, renderers = {}) {
  const segs = [[]];
  for (const r of (runs || [])) {
    if (r.type === 'br') segs.push([]);
    else segs[segs.length - 1].push(r);
  }
  return segs.map(seg => seg.map(r => {
    if (r.type === 'img') return renderers.image ? renderers.image(r) : '';
    if (r.type === 'txt') return renderers.text ? renderers.text(r) : '';
    if (r.type === 'link') return renderers.link ? renderers.link(r) : '';
    return '';
  }).join('')).join('<br>');
}

function colgroupHTML(elem, colCount) {
  const cw = (Array.isArray(elem.colWidths) ? elem.colWidths : []).map(n => Number(n));
  if (cw.length !== colCount || colCount <= 1 || !cw.every(n => Number.isFinite(n) && n > 0)) return '';
  const total = cw.reduce((a, b) => a + b, 0);
  const pcts = cw.map(w => w / total * 100);
  const ints = pcts.map(p => Math.round(p));
  ints[ints.length - 1] += 100 - ints.reduce((a, b) => a + b, 0);
  return '<colgroup>' + pcts.map((p, i) => `<col width="${ints[i]}%" style="width: ${p.toFixed(2)}%;" />`).join('') + '</colgroup>';
}

export function renderTable(elem, opts = {}) {
  const rows = elem.rows || [];
  const colCount = rows.reduce((m, r) => Math.max(m, (r || []).length), 0);
  const headerBg = opts.headerBg;
  const headerColor = opts.headerColor;
  const borderColor = opts.borderColor;
  const fs = opts.fontSize;
  const pad = opts.padding;
  const headerBold = opts.headerBold !== false;
  const bodyColor = opts.bodyColor || 'rgba(0, 0, 0, 0.9)';
  const vertical = opts.verticalAlignMiddle ? ' vertical-align: middle;' : '';
  const cellBase = `border: 1px solid ${borderColor}; padding: ${pad}px; font-size: ${fs}px; text-align: center; word-break: break-word;${vertical}`;
  const cellHtml = cell => renderTableCellRuns(cell && cell.runs, opts.renderers);
  const isBanner = row => Array.isArray(row) && row.length >= 1 && (cellHtml(row[0]) || '').trim() &&
    row.slice(1).every(c => !(cellHtml(c) || '').trim());
  const headStyle = extraBorder => `padding: ${pad}px; font-size: ${fs}px; text-align: center; background-color: ${headerBg}; color: ${headerColor};` +
    (headerBold ? ' font-weight: bold;' : '') + ` border: 1px solid ${extraBorder};`;
  const body = rows.map((row, ri) => {
    if (opts.bannerHeader && ri === 0 && colCount > 1 && isBanner(row)) {
      return `<tr><td colspan="${colCount}" style="${headStyle(headerBg)}">${cellHtml(row[0]) || '<br>'}</td></tr>`;
    }
    return '<tr>' + (row || []).map(cell => {
      const style = cell && cell.header
        ? cellBase + ` background-color: ${headerBg}; color: ${headerColor};${headerBold ? ' font-weight: bold;' : ''}`
        : cellBase + ` color: ${bodyColor};`;
      return `<td style="${style}">${cellHtml(cell) || '<br>'}</td>`;
    }).join('') + '</tr>';
  }).join('');
  // outerBorder(网站渠道):CMS 富文本编辑器把无 border 属性的表格判为"无边框表格"并加虚线
  // 辅助线,FoodTalks 发布链路会把虚线带上线。border="1" + 同色内联外框让编辑器不再误判;
  // border-collapse: collapse 下外框与 td 边框合并,视觉零变化。公众号不传此开关,输出字节不变。
  const tableStyle = 'border-collapse: collapse; width: 100%; table-layout: fixed;' +
    (opts.outerBorder ? ` border: 1px solid ${borderColor};` : '') + (opts.marginAuto ? ' margin: 0 auto;' : '');
  const colgroup = opts.useColgroup === false ? '' : colgroupHTML(elem, colCount);
  return `<table${opts.outerBorder ? ' border="1"' : ''} style="${tableStyle}">${colgroup}<tbody>${body}</tbody></table>`;
}
