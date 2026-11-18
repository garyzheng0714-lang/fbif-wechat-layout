// 接入智能体窗口（public/js/agent-connect.js，红线 51）：身份菜单「接入智能体」→ 窗口给 MCP 地址和提示词，复制发给智能体。
// 提示词只在 agentConnectPrompt 写一份，README「在智能体里用」那段照它核对；改了提示词用
// `README_UPDATE=1 node --test tests/agent-connect.test.mjs` 重写 README。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, writeFileSync } from 'node:fs';
import { parseHTML } from 'linkedom';
import { agentConnectPrompt, openAgentConnect, AGENT_SERVER_NAME } from '../public/js/agent-connect.js';
import { mountUserIdentity } from '../public/js/auth-session.js';

const PROD_MCP_URL = 'https://fbifmp-layout.foodtalks.cn/mcp';
const README = new URL('../README.md', import.meta.url);
const START = '<!-- agent-connect-prompt：由 public/js/agent-connect.js 生成（README_UPDATE=1 node --test tests/agent-connect.test.mjs），别手改 -->';
const END = '<!-- /agent-connect-prompt -->';

function pageWithDialog() {
  const { document, window } = parseHTML('<!doctype html><html><body><div id="identity"></div></body></html>');
  // linkedom 没有 <dialog> 的 showModal/close：照浏览器的样子用 open 属性记开关。
  const proto = Object.getPrototypeOf(document.createElement('dialog'));
  Object.defineProperty(proto, 'open', { get() { return this.hasAttribute('open'); }, configurable: true });
  proto.showModal = function showModal() { this.setAttribute('open', ''); };
  proto.close = function close() { this.removeAttribute('open'); };
  return { document, window };
}

function metadataFetch(resource, calls) {
  return async url => {
    calls.push(url);
    if (resource instanceof Error) throw resource;
    return { ok: true, status: 200, json: async () => ({ resource }) };
  };
}

const settle = () => new Promise(resolve => setTimeout(resolve, 0));

test('提示词把地址、名字、各家客户端的加法、登录、确认、用法都交代给智能体', () => {
  const prompt = agentConnectPrompt(PROD_MCP_URL);
  assert.equal(AGENT_SERVER_NAME, 'fbif-layout');
  assert.ok(prompt.includes(`codex mcp add fbif-layout --url ${PROD_MCP_URL}`));
  assert.ok(prompt.includes(`claude mcp add --transport http --scope user fbif-layout ${PROD_MCP_URL}`), 'Claude Code 要加到用户级，别的项目里也能用');
  for (const client of ['Codex', 'Claude Code', 'Cursor', 'VS Code', 'Gemini CLI', 'Windsurf', 'Claude 桌面版', '别的客户端']) {
    assert.ok(prompt.includes(client), `提示词里没有 ${client}`);
  }
  assert.match(prompt, /飞书登录/);
  assert.match(prompt, /「允许」/);
  assert.match(prompt, /不要代填/, '登录只能本人来');
  assert.ok(prompt.includes(`"args": ["-y", "mcp-remote", "${PROD_MCP_URL}"]`), 'Claude 桌面版的自定义连接器从云端连、回不到本机，走 mcp-remote');
  assert.match(prompt, /redirect_uri/, '云端客户端注册会被拒，提示词要让智能体认得这个错');
  assert.match(prompt, /不存草稿、不发布/, '红线 51：只排版和校对');
  assert.doesNotMatch(prompt, /Bearer|flk_/, '不用钥匙，不能让智能体去找钥匙');
});

test('MCP 上的每个工具，提示词里都讲到了', () => {
  const source = readFileSync(new URL('../api/agent_mcp.go', import.meta.url), 'utf8');
  const tools = [...source.matchAll(/mcp\.AddTool\(server, &mcp\.Tool\{\s*Name:\s*"([a-z_]+)"/g)].map(m => m[1]);
  assert.deepEqual(tools.sort(), ['check_article', 'get_layout_result', 'layout_article', 'prepare_upload']);
  const prompt = agentConnectPrompt(PROD_MCP_URL);
  for (const tool of tools) assert.ok(prompt.includes(tool), `提示词里没讲 ${tool}`);
});

test('README 里的提示词和窗口里的一字不差', () => {
  const readme = readFileSync(README, 'utf8');
  const block = `${START}\n\n\`\`\`text\n${agentConnectPrompt(PROD_MCP_URL)}\n\`\`\`\n\n${END}`;
  const start = readme.indexOf(START);
  const end = readme.indexOf(END);
  assert.ok(start >= 0 && end > start, 'README 里要有「在智能体里用」那段提示词');
  const current = readme.slice(start, end + END.length);
  if (process.env.README_UPDATE && current !== block) {
    writeFileSync(README, readme.slice(0, start) + block + readme.slice(end + END.length));
    return;
  }
  assert.equal(current, block, '提示词改了，README 没跟上：README_UPDATE=1 node --test tests/agent-connect.test.mjs');
});

test('只有能接智能体的人（飞书登录的同事）在身份菜单里看到「接入智能体」', () => {
  for (const [capabilities, want] of [[{ feishu_documents: true, agent: true }, true], [{ feishu_documents: false, agent: false }, false]]) {
    const { document, window } = pageWithDialog();
    mountUserIdentity(document.getElementById('identity'), {
      id: 'x', name: '同事', auth_mode: capabilities.agent ? 'feishu' : 'password', feishu: 'ok', capabilities,
    }, { documentRef: document, windowRef: window });
    const item = document.querySelector('[data-agent-connect]');
    assert.equal(Boolean(item), want);
    if (item) {
      assert.equal(item.getAttribute('role'), 'menuitem');
      assert.match(item.textContent, /接入智能体/);
    }
  }
});

test('点「接入智能体」打开窗口：地址用服务器说的，复制提示词发给智能体', async () => {
  const { document, window } = pageWithDialog();
  const calls = [];
  const fetchImpl = metadataFetch('https://layout.example.test/mcp', calls);
  mountUserIdentity(document.getElementById('identity'), {
    id: 'x', name: '同事', auth_mode: 'feishu', feishu: 'ok', capabilities: { feishu_documents: true, agent: true },
  }, { documentRef: document, windowRef: window, fetchImpl });
  document.querySelector('.user-identity-trigger').click();
  document.querySelector('[data-agent-connect]').click();
  assert.equal(document.querySelector('.user-identity-menu').hidden, true, '点了菜单就收起');
  const dialog = document.querySelector('dialog.agent-connect');
  assert.ok(dialog?.open, '窗口打开');
  await dialog.ready;
  assert.deepEqual(calls, ['/.well-known/oauth-protected-resource/mcp']);
  assert.equal(dialog.querySelector('[data-agent-connect-url]').textContent, 'https://layout.example.test/mcp');
  assert.equal(dialog.querySelector('[data-agent-connect-prompt]').textContent, agentConnectPrompt('https://layout.example.test/mcp'));

  const copied = [];
  dialog.remove();
  const again = openAgentConnect({ documentRef: document, fetchImpl, clipboard: { writeText: async text => { copied.push(text); } } });
  await again.ready;
  const copyButton = again.querySelector('[data-agent-connect-copy]');
  assert.equal(copyButton.disabled, false);
  copyButton.click();
  await settle();
  assert.deepEqual(copied, [agentConnectPrompt('https://layout.example.test/mcp')]);
  assert.match(copyButton.textContent, /已复制/);
  again.querySelector('[data-agent-connect-copy-url]').click();
  await settle();
  assert.equal(copied[1], 'https://layout.example.test/mcp');

  again.querySelector('[data-agent-connect-close]').click();
  assert.equal(again.open, false);
  assert.equal(openAgentConnect({ documentRef: document, fetchImpl }), again, '再开是同一个窗口');
  assert.equal(again.open, true);
  assert.equal(calls.length, 2, '同一个窗口地址只问一次');
});

test('取不到地址就说出来，不给复制一段错的提示词', async () => {
  const { document } = pageWithDialog();
  const dialog = openAgentConnect({ documentRef: document, fetchImpl: metadataFetch(new Error('offline'), []) });
  await dialog.ready;
  const error = dialog.querySelector('[data-agent-connect-error]');
  assert.equal(error.hidden, false);
  assert.match(error.textContent, /取不到接入地址/);
  assert.equal(dialog.querySelector('[data-agent-connect-copy]').disabled, true);
  assert.equal(dialog.querySelector('[data-agent-connect-prompt]').textContent, '');

  const { document: other } = pageWithDialog();
  const relative = openAgentConnect({ documentRef: other, fetchImpl: metadataFetch('/mcp', []) });
  await relative.ready;
  assert.equal(relative.querySelector('[data-agent-connect-copy]').disabled, true, '不是完整地址也不认');
});
