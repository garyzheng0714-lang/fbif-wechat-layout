// 左栏「AI 校对」卡：文章一排好就自动查错别字和风险（POST /api/article-check，与发布流水线同一套提示词和模型），
// 结果列在卡里、在预览里画波浪线，点「改正」把这一处换掉并重排，复制出去的就是改过的。
//
// 2026-11-17 审核群里 Dudu：「可以在排版工具的页面加上这个错别字或者风险核查吗？这样在排版公众号的时候就可以
// 直接先修改了。」用户：「prompt这些都用一样的，模型也是」。
//
// 这篇是不是查过，只看没改过的标题和正文（引擎留在 session.result.sourceLines）：点了改正、换了规则重排都不重查；
// 改了哪几处记在引擎的 input.textFixes，新导入一篇自然清空。预览里的线用 CSS Custom Highlight 画，不动预览的
// DOM，所以不会进复制的内容：红波浪线是还没改的确定错误，橙波浪线是要核对的，灰虚线是待核的（展开那一栏才画）。

import { articleText, fixHtmlLines, fixPlainText, locate, sameFix } from './text-fixes.js';

const ENDPOINT = '/api/article-check';
// 几个等待（毫秒）。start：导入后紧跟着的规则重排、连着粘贴，只查最后一次；retries：断网、发版重启自己再试两次；
// gap：服务端没等就回了也不连着问。
const TIMING = { start: 1500, retries: [2000, 5000], gap: 1000 };
const GIVE_UP_AFTER = 12 * 60 * 1000;
const BLOCK_TAGS = 'p,div,section,h1,h2,h3,h4,h5,h6,li,td,th,blockquote,figcaption';
const FOOTER_MARKER = 'fbif-preview-footer';
const ERROR_TEXT = '校对没完成';

const CHECK_ICON = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>';

function esc(text) {
  return String(text == null ? '' : text).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// 原句里把出错的那几个字标出来。
function evidenceHtml(evidence, wrong) {
  const spot = wrong ? locate(evidence, wrong) : null;
  if (!spot) return esc(evidence);
  return esc(evidence.slice(0, spot.start)) + '<mark>' + esc(evidence.slice(spot.start, spot.end)) + '</mark>' + esc(evidence.slice(spot.end));
}

// 这篇没改过的标题和正文；贴图、粘贴的纯文字没有标题。
export function sessionSource(session) {
  if (!session) return null;
  const result = session.result || {};
  const lines = result.sourceLines || result.lines || [session.articleHtml || ''];
  const title = session.bodyOnly ? '' : String(result.sourceTitle != null ? result.sourceTitle : (session.title || ''));
  const text = articleText(lines);
  return text ? { title, text, lines } : null;
}

// 预览里的文字和每个字所在的节点：标题在前，正文到页脚标记为止；换了段、遇到 <br> 算换行。
function readPreview(titleEl, contentEl) {
  let text = '';
  const nodes = [];
  const offsets = [];
  const roots = [];
  if (titleEl) roots.push(titleEl);
  for (const child of (contentEl ? contentEl.childNodes : [])) {
    if (child.nodeType === 8 && child.nodeValue === FOOTER_MARKER) break;
    roots.push(child);
  }
  const breakLine = () => { text += '\n'; nodes.push(null); offsets.push(0); };
  let lastBlock = null;
  const take = node => {
    if (node.nodeType === 3) {
      const block = node.parentElement && node.parentElement.closest(BLOCK_TAGS);
      if (block !== lastBlock && text) breakLine();
      lastBlock = block;
      for (let i = 0; i < node.nodeValue.length; i++) { text += node.nodeValue[i]; nodes.push(node); offsets.push(i); }
    } else if (node.nodeType === 1) {
      if (node.tagName === 'BR') breakLine();
      node.childNodes.forEach(take);
    }
  };
  roots.forEach(root => { if (text) breakLine(); lastBlock = null; take(root); });
  return { text, nodes, offsets };
}

export function mountArticleCheck({
  mount,
  titleEl = null,
  contentEl = null,
  getSession,
  setTextFixes,
  fetchImpl = (...args) => fetch(...args),
  windowRef = window,
  timing = {},
}) {
  const { start: startDelay, retries: retryDelays, gap } = { ...TIMING, ...timing };
  const doc = mount.ownerDocument;
  const highlights = windowRef.CSS && windowRef.CSS.highlights && typeof windowRef.Highlight === 'function' ? windowRef.CSS.highlights : null;
  // job：{ key, title, text, lines, view, retry, abort }；view 是服务端回的那份，null 是还没回。
  let job = null;
  let startTimer = 0;
  let focusTimer = 0;
  let busy = false; // 改正、撤销正在重排

  const sleep = ms => new Promise(resolve => windowRef.setTimeout(resolve, ms));

  function appliedFixes() {
    const session = getSession();
    return (session && session.result && session.result.appliedTextFixes) || [];
  }

  function isApplied(fix) {
    return appliedFixes().some(done => sameFix(done, fix));
  }

  function stop() {
    windowRef.clearTimeout(startTimer);
    if (job && job.abort) job.abort.abort();
  }

  function clear() {
    stop();
    job = null;
    render();
  }

  // 文章换了才重查；同一篇只刷新卡片和预览里的线。
  function sync() {
    const source = sessionSource(getSession());
    if (!source) { clear(); return; }
    const key = source.title + '\u0000' + source.text;
    if (job && job.key === key) { job.lines = source.lines; render(); return; }
    stop();
    job = { key, ...source, view: null, retry: false, abort: new AbortController() };
    render();
    const mine = job;
    startTimer = windowRef.setTimeout(() => { run(mine); }, startDelay);
  }

  function fail(target, message) {
    if (target !== job) return;
    target.view = { status: 'failed', fixes: [], issues: [], notes: [], error: message || ERROR_TEXT };
    render();
  }

  async function run(target) {
    const started = Date.now();
    let have = '';
    let failures = 0;
    while (target === job) {
      const asked = Date.now();
      let response;
      try {
        response = await fetchImpl(ENDPOINT, {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          signal: target.abort.signal,
          body: JSON.stringify({ title: target.title, text: target.text, have, retry: target.retry }),
        });
      } catch {
        response = null;
      }
      if (target !== job) return;
      if (!response || !response.ok) {
        const retryable = !response || response.status >= 502;
        if (retryable && failures < retryDelays.length) { await sleep(retryDelays[failures++]); continue; }
        const body = response ? await response.json().catch(() => null) : null;
        fail(target, response && response.status === 429 && body && body.error);
        return;
      }
      const view = await response.json().catch(() => null);
      if (target !== job) return;
      if (!view || !view.status) { fail(target); return; }
      // 这个环境没接模型：不出卡。
      if (view.status === 'off') { clear(); return; }
      failures = 0;
      target.retry = false;
      target.view = view;
      render();
      if (view.status === 'done' || view.status === 'failed') return;
      if (Date.now() - started > GIVE_UP_AFTER) { fail(target); return; }
      have = view.status;
      const spent = Date.now() - asked;
      if (spent < gap) await sleep(gap - spent);
    }
  }

  function retry() {
    if (!job) return;
    stop();
    job = { ...job, view: null, retry: true, abort: new AbortController() };
    render();
    run(job);
  }

  // changed：这次动的那一处，重排完在预览里闪一下，让人看到改上了（撤销了）。
  async function applyFixes(next, changed) {
    if (busy) return;
    busy = true;
    render();
    try { await setTextFixes(next); } finally { busy = false; render(); }
    const index = changed && job && job.view ? job.view.fixes.findIndex(fix => sameFix(fix, changed)) : -1;
    if (index >= 0) showInPreview(ranges.fixes[index]);
  }

  // 每条在预览里的位置：没改的确定错误、要核对的原句、已改正的字。
  function findRanges(view) {
    const preview = readPreview(titleEl, contentEl);
    const rangeOf = spot => {
      if (!spot) return null;
      const range = doc.createRange();
      range.setStart(preview.nodes[spot.start], preview.offsets[spot.start]);
      range.setEnd(preview.nodes[spot.end - 1], preview.offsets[spot.end - 1] + 1);
      return range;
    };
    return {
      fixes: view.fixes.map(fix => isApplied(fix)
        ? rangeOf(locate(preview.text, fix.correct, fixPlainText(fix.evidence, [{ ...fix, evidence: '' }]).text))
        : rangeOf(locate(preview.text, fix.wrong, fix.evidence))),
      issues: view.issues.map(item => rangeOf(locate(preview.text, item.evidence))),
      notes: view.notes.map(item => rangeOf(locate(preview.text, item.evidence))),
    };
  }

  let ranges = { fixes: [], issues: [], notes: [] };

  function paintPreview(view) {
    ranges = view ? findRanges(view) : { fixes: [], issues: [], notes: [] };
    if (!highlights) return;
    highlights.delete('proof-focus');
    const set = (name, list) => {
      const live = list.filter(Boolean);
      if (live.length) highlights.set(name, new windowRef.Highlight(...live));
      else highlights.delete(name);
    };
    set('proof-error', view ? ranges.fixes.filter((_, i) => !isApplied(view.fixes[i])) : []);
    set('proof-issue', ranges.issues);
    // 待核的平时不画，展开那一栏才画（灰色虚线）。
    set('proof-note', mount.querySelector('.proof-notes[open]') ? ranges.notes : []);
  }

  function showInPreview(range) {
    if (!range) return;
    const anchor = range.startContainer.parentElement;
    if (anchor && anchor.scrollIntoView) anchor.scrollIntoView({ block: 'center', behavior: 'smooth' });
    if (!highlights) return;
    highlights.set('proof-focus', new windowRef.Highlight(range));
    windowRef.clearTimeout(focusTimer);
    focusTimer = windowRef.setTimeout(() => highlights.delete('proof-focus'), 1800);
  }

  function itemRow(kind, index, item) {
    const block = item.level === 'block';
    return '<li class="proof-row' + (block ? ' is-block' : '') + '">' +
      '<button class="proof-where" type="button" data-show="' + kind + ':' + index + '">' +
      '<span class="proof-line">' + (block ? '<span class="proof-flag">阻断</span>' : '') + esc(item.reason) + '</span>' +
      '<span class="proof-evidence">' + esc(item.evidence) + '</span>' +
      '</button></li>';
  }

  function render() {
    const view = job && job.view;
    mount.hidden = !job;
    if (!job) { mount.innerHTML = ''; paintPreview(null); return; }
    const status = view ? view.status : 'checking';
    const working = status === 'checking' || status === 'verifying';
    const fixes = view ? view.fixes : [];
    const issues = view ? view.issues : [];
    const notes = view ? view.notes : [];
    // 哪几处能在这篇里找到、点了能改上（与引擎套用时同一个找法）。
    const inBody = fixHtmlLines(job.lines, fixes).applied;
    const inTitle = fixPlainText(job.title, fixes).applied;
    const done = fixes.map(isApplied);
    const open = fixes.filter((_, i) => !done[i] && (inBody[i] || inTitle[i]));

    let state = '';
    if (working) state = '<span class="proof-state"><span class="proof-spinner" aria-hidden="true"></span>' + (status === 'verifying' ? '正在复核改法' : '正在通读全文，约 3 分钟') + '</span>';
    else if (status === 'failed') state = '<span class="proof-state is-failed">' + esc(view.error || ERROR_TEXT) + '</span><button class="proof-button" type="button" data-retry>重试</button>';
    else if (!fixes.length && !issues.length) state = '<span class="proof-state is-clean">' + CHECK_ICON + '没发现错误</span>';
    else if (open.length > 1) state = '<button class="proof-button" type="button" data-fix-all' + (busy ? ' disabled' : '') + '>全部改正</button>';

    const fixRows = fixes.map((fix, i) => {
      const action = done[i]
        ? '<button class="proof-button is-quiet" type="button" data-undo="' + i + '"' + (busy ? ' disabled' : '') + '>撤销</button>'
        : (inBody[i] || inTitle[i])
          ? '<button class="proof-button" type="button" data-fix="' + i + '"' + (busy ? ' disabled' : '') + '>改正</button>'
          : '';
      return '<li class="proof-row' + (done[i] ? ' is-done' : '') + '">' +
        '<button class="proof-where" type="button" data-show="fixes:' + i + '">' +
        '<span class="proof-line"><span class="proof-wrong">' + esc(fix.wrong) + '</span><span class="proof-arrow" aria-hidden="true">→</span><span class="proof-arrow-text">应为</span>' + esc(fix.correct) + '</span>' +
        '<span class="proof-evidence">' + evidenceHtml(fix.evidence, fix.wrong) + '</span>' +
        '</button>' + action + '</li>';
    }).join('');
    const issueRows = issues.map((item, i) => itemRow('issues', i, item)).join('');
    const noteRows = notes.map((item, i) => itemRow('notes', i, item)).join('');
    const wasOpen = !!mount.querySelector('.proof-notes[open]');

    mount.innerHTML =
      '<div class="proof-head"><span class="proof-title">AI 校对</span>' + state + '</div>' +
      (fixRows || issueRows ? '<ul class="proof-list">' + fixRows + issueRows + '</ul>' : '') +
      (noteRows ? '<details class="proof-notes"' + (wasOpen ? ' open' : '') + '><summary>待核 ' + notes.length + ' 条</summary><ul class="proof-list">' + noteRows + '</ul></details>' : '');
    paintPreview(view);
  }

  mount.classList.add('proof-card');
  mount.setAttribute('role', 'region');
  mount.setAttribute('aria-label', 'AI 校对');
  mount.setAttribute('aria-live', 'polite');
  // toggle 不冒泡，在捕获阶段接：展开、收起待核时重画预览里的线。
  mount.addEventListener('toggle', () => paintPreview(job && job.view), true);
  mount.addEventListener('click', event => {
    const target = event.target.closest('button');
    if (!target || !job || !job.view) return;
    const fixes = job.view.fixes;
    if (target.hasAttribute('data-retry')) { retry(); return; }
    if (target.hasAttribute('data-fix-all')) {
      applyFixes([...appliedFixes(), ...fixes.filter(fix => !isApplied(fix))]);
      return;
    }
    if (target.hasAttribute('data-fix')) {
      const fixed = fixes[Number(target.getAttribute('data-fix'))];
      applyFixes([...appliedFixes(), fixed], fixed);
      return;
    }
    if (target.hasAttribute('data-undo')) {
      const undone = fixes[Number(target.getAttribute('data-undo'))];
      applyFixes(appliedFixes().filter(fix => !sameFix(fix, undone)), undone);
      return;
    }
    const show = target.getAttribute('data-show');
    if (show) {
      const [kind, index] = show.split(':');
      showInPreview((ranges[kind] || [])[Number(index)]);
    }
  });

  // 先问一句这个环境接没接模型：没接就整个不出卡（本地、端到端测试）；问不到当接了，开查时服务端还会再答一次。
  const ready = Promise.resolve()
    .then(() => fetchImpl(ENDPOINT, { credentials: 'same-origin' }))
    .then(response => (response.ok ? response.json() : null))
    .catch(() => null)
    .then(answer => {
      if (answer && answer.enabled === false) return false;
      for (const name of ['fbif:article-session-activated', 'fbif:article-restored', 'fbif:article-text-ready']) {
        windowRef.addEventListener(name, sync);
      }
      for (const name of ['fbif:article-cleared', 'fbif:plain-text-ready']) {
        windowRef.addEventListener(name, clear);
      }
      sync();
      return true;
    });

  return { ready };
}
