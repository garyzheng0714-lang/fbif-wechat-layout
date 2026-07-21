import test from 'node:test';
import assert from 'node:assert/strict';
import { parseHTML } from 'linkedom';

import {
  ruleStateAllowsFinalOutput,
  setRuleReadinessState,
} from '../public/js/rule-readiness.js';

function fixture() {
  const { window, document } = parseHTML(`<!doctype html><html><body>
    <header class="global-channel-bar"><span class="topbar-spacer"></span><button id="copyBtn">复制全文</button></header>
  </body></html>`);
  return { window, document };
}

test('规则状态未 resolved 时禁用复制，缓存降级显示可访问重试提示', () => {
  const { window, document } = fixture();
  let retries = 0;
  setRuleReadinessState('loading', { win: window, doc: document, onRetry: () => retries++ });
  const copy = document.getElementById('copyBtn');
  assert.equal(copy.disabled, true);
  assert.equal(document.getElementById('ruleStatusBanner'), null, '正常短暂加载不制造噪音提示');
  assert.equal(ruleStateAllowsFinalOutput(window), false);

  setRuleReadinessState('stale-cache', {
    win: window, doc: document, onRetry: () => retries++, error: 'HTTP 503',
  });
  const banner = document.getElementById('ruleStatusBanner');
  assert.equal(banner.getAttribute('role'), 'alert');
  assert.match(banner.textContent, /本机缓存/);
  assert.match(banner.textContent, /HTTP 503/);
  banner.querySelector('[data-action="retry-rules"]').click();
  assert.equal(retries, 1);
});

test('服务端规则 resolved 后只解除本模块施加的复制锁并移除提示', () => {
  const { window, document } = fixture();
  setRuleReadinessState('failed', { win: window, doc: document, onRetry() {} });
  setRuleReadinessState('resolved', { win: window, doc: document });
  const copy = document.getElementById('copyBtn');
  assert.equal(copy.disabled, false);
  assert.equal(copy.hasAttribute('aria-disabled'), false);
  assert.equal(document.getElementById('ruleStatusBanner'), null);
  assert.equal(ruleStateAllowsFinalOutput(window), true);
});
