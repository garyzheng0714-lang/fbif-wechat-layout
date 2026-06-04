// block-format-control.js — 标题字段「格式片 + 浮层」编辑器（方案A format-editor.html 复刻）。
//
// 把一个文本字段（title / intro_text / caption）与它的格式兄弟字段（<base>_size、<base>_color、
// 可选 center 对齐）收成一个紧凑控件：标题文字输入框右侧挂 [■ Aa] 格式片，点开弹出浮层，
// 内含【实时预览 + 字号步进 + 颜色色板/hex + 对齐切换】。取代「字号/颜色/居中」三行平铺。
//
// 关键：浮层内改值走 setField(quiet)——只持久化 + 刷正文预览，不重渲面板（否则每次输入重建 DOM
// 会关掉浮层、丢焦点）。控件自己就地更新预览/片/色板选中态。
//
// 中性模块：不 import 任何渠道渲染器（红线 #19）。宿主（block-panel）注入 setField/esc。

// 颜色色板快捷色（默认 #544545=正文色；其余为编辑常用色）。custom 走原生取色器，hex 输入兜底任意色。
const PALETTE = ['#544545', '#888888', '#1A1A1A', '#3F5C7A', '#0070C0', '#21A366', '#ED7D31', '#E03131', '#1F5995'];

// 已打开浮层的 close 回调集合：浮层挂在 body 上，面板重渲前需统一关掉（防孤儿节点 + 监听泄漏）。
const OPEN_POPS = new Set();
export function closeOpenFormatPopovers() { [...OPEN_POPS].forEach(fn => { try { fn(); } catch {} }); }

const ICON_PENCIL = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg>';
const ICON_ALIGN_LEFT = '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3 6h18M3 12h12M3 18h15"/></svg>';
const ICON_ALIGN_CENTER = '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3 6h18M6 12h12M4 18h16"/></svg>';

const STYLE_ID = 'bpfmt-style';
function ensureStyle(doc) {
  if (doc.getElementById(STYLE_ID)) return;
  const st = doc.createElement('style');
  st.id = STYLE_ID;
  st.textContent = `
.bpfmt-field{margin-top:10px;}
.bpfmt-labelrow{display:flex;align-items:center;justify-content:space-between;margin-bottom:4px;color:var(--fs-text-2,#646a73);font-size:12px;}
.bpfmt-hint{color:var(--fs-text-3,#8f959e);font-size:11px;}
.bpfmt-inputwrap{display:flex;align-items:center;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);background:#fff;transition:border-color .15s,box-shadow .15s;}
.bpfmt-inputwrap.is-active{border-color:var(--fs-brand,#1456f0);box-shadow:0 0 0 3px var(--fs-brand-soft,#ebf1ff);}
.bpfmt-text{flex:1;min-width:0;border:none;background:none;padding:6px 8px;font-size:13px;font-family:inherit;color:var(--fs-text-1,#1f2329);outline:none;}
.bpfmt-chip{display:inline-flex;align-items:center;gap:5px;border:none;border-left:1px solid var(--fs-divider,rgba(31,35,39,.08));background:none;padding:4px 9px;margin:2px;border-radius:var(--fs-r-xs,4px);cursor:pointer;font-family:inherit;transition:background .12s;}
.bpfmt-chip:hover{background:var(--fs-bg-hover,rgba(31,35,39,.045));}
.bpfmt-chip.is-open{background:var(--fs-brand-soft,#ebf1ff);}
.bpfmt-sw{width:16px;height:16px;border-radius:4px;flex:none;box-shadow:inset 0 0 0 1px rgba(0,0,0,.12);}
.bpfmt-csize{font-size:12px;color:var(--fs-brand,#1456f0);font-variant-numeric:tabular-nums;}
.bpfmt-aa{font-size:13px;font-weight:600;color:var(--fs-brand,#1456f0);line-height:1;}
.bpfmt-pop{position:fixed;z-index:1000;width:300px;max-width:calc(100vw - 24px);background:#fff;border:1px solid var(--fs-border,rgba(31,35,39,.12));border-radius:var(--fs-r-m,8px);box-shadow:var(--fs-sh-s3,0 8px 28px rgba(0,0,0,.14));padding:12px;}
.bpfmt-preview{background:var(--fs-bg-sunken,#f7f8fa);border-radius:var(--fs-r-s,6px);padding:14px 12px;margin-bottom:12px;overflow:hidden;word-break:break-word;}
.bpfmt-sec{margin-top:12px;}
.bpfmt-sec:first-child{margin-top:0;}
.bpfmt-sechead{display:flex;align-items:center;justify-content:space-between;margin-bottom:6px;color:var(--fs-text-2,#646a73);font-size:12px;}
.bpfmt-secval{color:var(--fs-text-3,#8f959e);font-variant-numeric:tabular-nums;}
.bpfmt-stepper{display:flex;align-items:center;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-s,6px);overflow:hidden;}
.bpfmt-step{width:38px;height:34px;border:none;background:var(--fs-bg-sunken,#f7f8fa);color:var(--fs-text-1,#1f2329);font-size:17px;cursor:pointer;flex:none;transition:background .12s;}
.bpfmt-step:hover{background:var(--fs-bg-hover,rgba(31,35,39,.06));}
.bpfmt-stepper input{flex:1;min-width:0;border:none;border-left:1px solid var(--fs-border-control,#d9dce0);border-right:1px solid var(--fs-border-control,#d9dce0);text-align:center;font-size:14px;font-family:inherit;padding:0;height:34px;outline:none;-moz-appearance:textfield;}
.bpfmt-stepper input::-webkit-outer-spin-button,.bpfmt-stepper input::-webkit-inner-spin-button{-webkit-appearance:none;margin:0;}
.bpfmt-palette{display:grid;grid-template-columns:repeat(7,1fr);gap:7px;}
.bpfmt-swatch{aspect-ratio:1;border-radius:var(--fs-r-s,6px);cursor:pointer;border:none;padding:0;box-shadow:inset 0 0 0 1px rgba(0,0,0,.1);transition:transform .1s;}
.bpfmt-swatch:hover{transform:scale(1.08);}
.bpfmt-swatch.is-sel{box-shadow:0 0 0 2px #fff,0 0 0 4px var(--fs-brand,#1456f0);}
.bpfmt-swatch.bpfmt-custom{display:flex;align-items:center;justify-content:center;background:#fff;color:var(--fs-text-3,#8f959e);box-shadow:inset 0 0 0 1px var(--fs-border-control,#d9dce0);}
.bpfmt-hexrow{display:flex;align-items:center;gap:8px;margin-top:8px;}
.bpfmt-hexrow input[type=color]{width:30px;height:30px;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);padding:0;background:none;cursor:pointer;flex:none;}
.bpfmt-hex{flex:1;min-width:0;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-xs,4px);padding:6px 8px;font-size:13px;font-family:inherit;outline:none;}
.bpfmt-seg{display:flex;gap:6px;}
.bpfmt-segbtn{flex:1;display:inline-flex;align-items:center;justify-content:center;gap:5px;height:34px;border:1px solid var(--fs-border-control,#d9dce0);border-radius:var(--fs-r-s,6px);background:#fff;color:var(--fs-text-2,#646a73);font-size:13px;font-family:inherit;cursor:pointer;transition:all .12s;}
.bpfmt-segbtn.is-sel{border-color:var(--fs-brand,#1456f0);background:var(--fs-brand-soft,#ebf1ff);color:var(--fs-brand,#1456f0);}
`;
  (doc.head || doc.documentElement).appendChild(st);
}

function normHex(v) { return String(v == null ? '' : v).trim(); }
function sameColor(a, b) { return normHex(a).toLowerCase() === normHex(b).toLowerCase(); }

// createTitleFormatControl：返回一个 .bpfmt-field 元素（标题文字 label + 输入框+格式片）。
//   opts.block：resolved 区块（fields[key] = {value, default, overridden}）
//   opts.textField / sizeField / colorField / alignField：注册表字段定义（后三者可缺）
//   opts.setField(key, val, same)：静默写覆盖（不重渲面板）
//   opts.esc：转义
export function createTitleFormatControl(opts) {
  const { doc, esc, block, textField, sizeField, colorField, alignField, setField } = opts;
  ensureStyle(doc);

  const cell = (f) => (f && block.fields[f.key]) || (f ? { value: f.default, default: f.default } : null);
  const valOf = (f) => { const c = cell(f); return c && c.value != null ? c.value : (f && f.default); };
  const defOf = (f) => { const c = cell(f); return c && c.default != null ? c.default : (f && f.default); };

  // 本地工作态：block.fields 是 render 时的快照，setFieldQuiet 不重渲面板，故浮层自持当前值供 sync 读。
  let curText = String(valOf(textField) == null ? '' : valOf(textField));
  let curSize = sizeField ? Number(valOf(sizeField)) : null;
  let curColor = colorField ? (normHex(valOf(colorField)) || '#544545') : null;
  let curAlign = alignField ? !!valOf(alignField) : null;

  const field = doc.createElement('div');
  field.className = 'bpfmt-field';

  // label 行：标题文字 …… 格式可调
  const labelRow = doc.createElement('div');
  labelRow.className = 'bpfmt-labelrow';
  labelRow.innerHTML = `<span>${esc(textField.label || '标题文字')}</span>` +
    ((sizeField || colorField || alignField) ? '<span class="bpfmt-hint">格式可调</span>' : '');
  field.appendChild(labelRow);

  // 输入框 + 格式片
  const wrap = doc.createElement('div');
  wrap.className = 'bpfmt-inputwrap';
  const input = doc.createElement('input');
  input.type = 'text'; input.className = 'bpfmt-text';
  input.value = String(valOf(textField) == null ? '' : valOf(textField));
  input.oninput = () => { curText = input.value; setField(textField.key, curText, curText === defOf(textField)); sync(); };
  wrap.appendChild(input);

  let chip = null, pop = null, open = false;
  const hasFormat = !!(sizeField || colorField || alignField);
  if (hasFormat) {
    chip = doc.createElement('button');
    chip.type = 'button'; chip.className = 'bpfmt-chip'; chip.title = '调整字号 / 颜色 / 对齐';
    chip.innerHTML =
      `<span class="bpfmt-sw"></span>` +
      (sizeField ? `<span class="bpfmt-csize"></span>` : '') +
      `<span class="bpfmt-aa">Aa</span>`;
    chip.onclick = (e) => { e.stopPropagation(); toggle(); };
    wrap.appendChild(chip);
  }
  field.appendChild(wrap);

  // 同步格式片上的色块 + 字号
  function syncChip() {
    if (!chip) return;
    const sw = chip.querySelector('.bpfmt-sw');
    if (sw && colorField) sw.style.background = curColor || '#888';
    const cs = chip.querySelector('.bpfmt-csize');
    if (cs && sizeField) cs.textContent = curSize;
  }
  syncChip();

  // ---- 浮层 ----
  function buildPop() {
    const p = doc.createElement('div');
    p.className = 'bpfmt-pop';

    // 实时预览
    const preview = doc.createElement('div');
    preview.className = 'bpfmt-preview';
    p.appendChild(preview);

    // 字号
    if (sizeField) {
      const sec = doc.createElement('div'); sec.className = 'bpfmt-sec';
      const head = doc.createElement('div'); head.className = 'bpfmt-sechead';
      head.innerHTML = `<span>字号</span><span class="bpfmt-secval" data-size-val></span>`;
      const stepper = doc.createElement('div'); stepper.className = 'bpfmt-stepper';
      const minus = doc.createElement('button'); minus.type = 'button'; minus.className = 'bpfmt-step'; minus.textContent = '−'; minus.tabIndex = -1;
      const num = doc.createElement('input'); num.type = 'number';
      if (sizeField.min != null) num.min = sizeField.min;
      if (sizeField.max != null) num.max = sizeField.max;
      num.value = valOf(sizeField);
      const plus = doc.createElement('button'); plus.type = 'button'; plus.className = 'bpfmt-step'; plus.textContent = '+'; plus.tabIndex = -1;
      const setSize = (n) => {
        const lo = sizeField.min == null ? -Infinity : Number(sizeField.min);
        const hi = sizeField.max == null ? Infinity : Number(sizeField.max);
        n = Math.min(hi, Math.max(lo, n));
        curSize = n; num.value = n;
        setField(sizeField.key, n, n === Number(defOf(sizeField)));
        sync();
      };
      minus.onclick = () => setSize((Number(num.value) || 0) - 1);
      plus.onclick = () => setSize((Number(num.value) || 0) + 1);
      num.oninput = () => { const n = Number(num.value); if (Number.isFinite(n)) { curSize = n; setField(sizeField.key, n, n === Number(defOf(sizeField))); sync(); } };
      stepper.appendChild(minus); stepper.appendChild(num); stepper.appendChild(plus);
      sec.appendChild(head); sec.appendChild(stepper); p.appendChild(sec);
    }

    // 颜色
    if (colorField) {
      const sec = doc.createElement('div'); sec.className = 'bpfmt-sec';
      const head = doc.createElement('div'); head.className = 'bpfmt-sechead';
      head.innerHTML = `<span>颜色</span><span class="bpfmt-secval" data-color-val></span>`;
      const pal = doc.createElement('div'); pal.className = 'bpfmt-palette';
      const setColor = (hex) => { curColor = normHex(hex); setField(colorField.key, hex, sameColor(hex, defOf(colorField))); sync(); };
      PALETTE.forEach(hex => {
        const b = doc.createElement('button'); b.type = 'button'; b.className = 'bpfmt-swatch';
        b.style.background = hex; b.dataset.hex = hex; b.title = hex;
        b.onclick = () => setColor(hex);
        pal.appendChild(b);
      });
      const custom = doc.createElement('button'); custom.type = 'button'; custom.className = 'bpfmt-swatch bpfmt-custom'; custom.title = '自定义颜色';
      custom.innerHTML = ICON_PENCIL;
      const picker = doc.createElement('input'); picker.type = 'color';
      picker.style.position = 'absolute'; picker.style.opacity = '0'; picker.style.width = '0'; picker.style.height = '0'; picker.style.pointerEvents = 'none';
      picker.value = /^#[0-9a-f]{6}$/i.test(normHex(valOf(colorField))) ? normHex(valOf(colorField)) : '#544545';
      picker.oninput = () => setColor(picker.value);
      custom.onclick = () => picker.click();
      custom.appendChild(picker);
      pal.appendChild(custom);
      sec.appendChild(head); sec.appendChild(pal);

      const hexRow = doc.createElement('div'); hexRow.className = 'bpfmt-hexrow';
      const hexPick = doc.createElement('input'); hexPick.type = 'color'; hexPick.className = 'bpfmt-hexpick';
      hexPick.value = /^#[0-9a-f]{6}$/i.test(normHex(valOf(colorField))) ? normHex(valOf(colorField)) : '#544545';
      hexPick.oninput = () => setColor(hexPick.value);
      const hexText = doc.createElement('input'); hexText.type = 'text'; hexText.className = 'bpfmt-hex'; hexText.value = normHex(valOf(colorField));
      hexText.oninput = () => setColor(hexText.value);
      hexRow.appendChild(hexPick); hexRow.appendChild(hexText);
      sec.appendChild(hexRow);
      p.appendChild(sec);
    }

    // 对齐
    if (alignField) {
      const sec = doc.createElement('div'); sec.className = 'bpfmt-sec';
      const head = doc.createElement('div'); head.className = 'bpfmt-sechead';
      head.innerHTML = `<span>对齐</span>`;
      const seg = doc.createElement('div'); seg.className = 'bpfmt-seg';
      const mk = (label, ico, centered) => {
        const b = doc.createElement('button'); b.type = 'button'; b.className = 'bpfmt-segbtn'; b.dataset.center = centered ? '1' : '0';
        b.innerHTML = ico + `<span>${label}</span>`;
        b.onclick = () => { curAlign = centered; setField(alignField.key, centered, centered === !!defOf(alignField)); sync(); };
        return b;
      };
      seg.appendChild(mk('居左', ICON_ALIGN_LEFT, false));
      seg.appendChild(mk('居中', ICON_ALIGN_CENTER, true));
      sec.appendChild(head); sec.appendChild(seg);
      p.appendChild(sec);
    }
    return p;
  }

  // 同步浮层内全部控件 + 预览 + 片，按当前 override 值
  function sync() {
    syncChip();
    if (!pop) return;
    const size = curSize == null ? 17 : curSize;
    const color = curColor == null ? '#544545' : curColor;
    const centered = curAlign == null ? true : curAlign;
    const preview = pop.querySelector('.bpfmt-preview');
    if (preview) {
      preview.textContent = curText || (textField.default || '预览文字');
      preview.style.fontSize = size + 'px';
      preview.style.color = color;
      preview.style.textAlign = centered ? 'center' : 'left';
      preview.style.fontWeight = '600';
    }
    const sv = pop.querySelector('[data-size-val]'); if (sv) sv.textContent = size;
    const cv = pop.querySelector('[data-color-val]'); if (cv) cv.textContent = color;
    const hx = pop.querySelector('.bpfmt-hex'); if (hx && doc.activeElement !== hx) hx.value = color;
    const hp = pop.querySelector('.bpfmt-hexpick'); if (hp && /^#[0-9a-f]{6}$/i.test(color)) hp.value = color;
    pop.querySelectorAll('.bpfmt-swatch[data-hex]').forEach(b => b.classList.toggle('is-sel', sameColor(b.dataset.hex, color)));
    pop.querySelectorAll('.bpfmt-segbtn').forEach(b => b.classList.toggle('is-sel', (b.dataset.center === '1') === centered));
  }

  function position() {
    if (!pop) return;
    const r = wrap.getBoundingClientRect();
    const vw = doc.defaultView.innerWidth, vh = doc.defaultView.innerHeight;
    const pw = 300;
    let left = Math.min(r.left, vw - pw - 12);
    left = Math.max(12, left);
    let top = r.bottom + 6;
    pop.style.left = left + 'px';
    pop.style.top = top + 'px';
    // 下方空间不足则翻到上方
    const ph = pop.offsetHeight;
    if (top + ph > vh - 12 && r.top - ph - 6 > 12) pop.style.top = (r.top - ph - 6) + 'px';
  }

  function onDocDown(e) { if (pop && !pop.contains(e.target) && e.target !== chip && !chip.contains(e.target)) close(); }
  function onScroll() { if (open) close(); }
  function onKey(e) { if (e.key === 'Escape') close(); }

  function openPop() {
    if (open) return;
    open = true;
    OPEN_POPS.add(close);
    pop = buildPop();
    doc.body.appendChild(pop);
    sync();
    position();
    wrap.classList.add('is-active');
    chip.classList.add('is-open');
    doc.addEventListener('pointerdown', onDocDown, true);
    doc.addEventListener('keydown', onKey, true);
    doc.defaultView.addEventListener('scroll', onScroll, true);
    if (window.gsap && window.gFrom) window.gFrom(pop, { y: -6, opacity: 0, duration: 0.18, ease: 'power2.out' });
  }
  function close() {
    if (!open) return;
    open = false;
    OPEN_POPS.delete(close);
    wrap.classList.remove('is-active');
    if (chip) chip.classList.remove('is-open');
    doc.removeEventListener('pointerdown', onDocDown, true);
    doc.removeEventListener('keydown', onKey, true);
    doc.defaultView.removeEventListener('scroll', onScroll, true);
    if (pop) { pop.remove(); pop = null; }
  }
  function toggle() { open ? close() : openPop(); }

  field._bpfmtClose = close; // 供面板重渲前清理
  return field;
}
