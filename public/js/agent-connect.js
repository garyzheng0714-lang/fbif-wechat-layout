// 接入智能体（红线 51）：编辑器身份菜单「接入智能体」打开的窗口，给一段提示词和 MCP 地址。
// 同事把提示词发给自己的智能体（Codex、Claude Code、Cursor……），智能体照着把排版工具加上、带人用飞书登录、教他怎么用。
// 提示词只在 agentConnectPrompt 写一份：README「在智能体里用」那段由 tests/agent-connect.test.mjs 照它核对，
// 改了提示词跑 `README_UPDATE=1 node --test tests/agent-connect.test.mjs` 重写 README。

export const AGENT_SERVER_NAME = 'fbif-layout';

// MCP 地址以服务器说的为准（受保护资源元数据里的 resource，客户端也拿它核对），不按页面地址猜。
const RESOURCE_METADATA_PATH = '/.well-known/oauth-protected-resource/mcp';

export function agentConnectPrompt(mcpURL) {
  const name = AGENT_SERVER_NAME;
  return `请帮我把「FBIF 排版工具」接进来。接好后，我就能让你按 FBIF 各个公众号和 FoodTalks 网站的样式排版、做 AI 校对。

它是一个远程 MCP 服务器：
- 名称：${name}
- 地址：${mcpURL}
- 传输方式：Streamable HTTP
- 登录：标准 MCP OAuth（自动发现、动态注册），不用钥匙。配置里不要填 headers、token、client_id。

请按下面做：

1. 加服务器。先认清你是哪个客户端，按对应的一条加到用户级（所有项目都能用）。已经有 ${name} 且地址一样就跳过；地址不一样就先删掉旧的再加。
   - Codex（命令行、IDE 插件、桌面版共用 ~/.codex/config.toml）：codex mcp add ${name} --url ${mcpURL}
   - Claude Code：claude mcp add --transport http --scope user ${name} ${mcpURL}
   - Cursor：在 ~/.cursor/mcp.json 的 "mcpServers" 里加 "${name}": {"url": "${mcpURL}"}
   - VS Code（Copilot）：code --add-mcp '{"name":"${name}","type":"http","url":"${mcpURL}"}'
   - Gemini CLI：gemini mcp add --scope user --transport http ${name} ${mcpURL}
   - Windsurf（现名 Devin Desktop）：在它的 mcp_config.json（~/.codeium/windsurf/ 或 ~/.config/devin/ 下）的 "mcpServers" 里加 "${name}": {"serverUrl": "${mcpURL}"}
   - Claude 桌面版：不要用「添加自定义连接器」（那是从云端连过来，登录回不到这台电脑）。在 claude_desktop_config.json（设置 → 开发者 → 编辑配置）的 "mcpServers" 里加 "${name}": {"command": "npx", "args": ["-y", "mcp-remote", "${mcpURL}"]}，要装了 Node.js；改完让我完全退出再打开 Claude 桌面版。
   - 别的客户端：照它自己的文档，加一个上面这样的远程 MCP 服务器。
   要写你自己的配置文件或连网，需要权限就向我申请；还是做不了（没有终端、改不了文件），就告诉我在哪里点、填什么，我自己来。

2. 登录。触发 ${name} 的 OAuth 登录：
   - Codex：加完会自己打开浏览器；没打开就运行 codex mcp login ${name}。IDE 插件、桌面版加完要重启（点 Restart）才连上。
   - Claude Code：让我在 Claude Code 里输入 /mcp，选 ${name}，再选登录；也可以在终端运行 claude mcp login ${name}
   - Cursor、VS Code、Windsurf：让我在 MCP 设置里点 ${name} 旁边的登录（Connect / Login / Start），弹窗问要不要登录就选允许
   - Gemini CLI：让我输入 /mcp auth ${name}
   - Claude 桌面版：重新打开后会自己打开浏览器
   浏览器会打开 FBIF 登录页：我用飞书登录，再在确认页点「允许」。这一步只能我自己来，你不要代填任何东西。
   如果报回调地址不对（redirect_uri），说明这个客户端是从云端连过来的，这里接不了，请告诉我改用上面列的客户端。

3. 确认接好。用客户端自己的办法看 ${name} 已连上、已登录（比如 codex mcp list、claude mcp list）。多数客户端要新开一个对话才看得到新工具，需要的话告诉我。工具有四个：layout_article（排版）、get_layout_result（取没排完的那篇）、check_article（AI 校对）、prepare_upload（交 Word 前拿上传地址）。

4. 用一两句话告诉我接好了，再教我这样用：
   - 把稿子交给你：公众号或网页链接、飞书文档链接、Markdown、文字、Word 都行，说要排成哪个账号。你调 layout_article，把回来的编辑器链接给我；我打开链接，在排版工具里接着改，点「复制全文」粘进后台。
   - 要校对就让你调 check_article，你把要改的地方列给我。
   - 它只排版和校对，不存草稿、不发布，不碰任何平台。

如果你是网页里的 ChatGPT、claude.ai 这类不在我电脑上运行的智能体，接不了（登录只能回到我自己的电脑），请直接告诉我改用上面列的客户端。`;
}

const COPY_RESET_MS = 2000;

async function writeClipboardText(text, { clipboard, documentRef }) {
  try {
    if (clipboard?.writeText) {
      await clipboard.writeText(text);
      return true;
    }
  } catch {
    // 没给剪贴板权限（非 https、浏览器拦了）就退回选中复制。
  }
  const area = documentRef.createElement('textarea');
  area.value = text;
  area.setAttribute('readonly', '');
  area.style.position = 'fixed';
  area.style.left = '-99999px';
  documentRef.body.append(area);
  try {
    area.select();
    return !!documentRef.execCommand?.('copy');
  } finally {
    area.remove();
  }
}

function copyButton(button, getText, { clipboard, documentRef, doneLabel }) {
  const idle = button.textContent;
  let timer = 0;
  button.addEventListener('click', async () => {
    const text = getText();
    if (!text) return;
    const ok = await writeClipboardText(text, { clipboard, documentRef });
    clearTimeout(timer);
    button.textContent = ok ? doneLabel : '复制没成，手动选中复制';
    button.classList.toggle('is-copied', ok);
    timer = setTimeout(() => {
      button.textContent = idle;
      button.classList.remove('is-copied');
    }, COPY_RESET_MS);
  });
}

function buildDialog(documentRef) {
  const dialog = documentRef.createElement('dialog');
  dialog.className = 'agent-connect';
  dialog.setAttribute('aria-labelledby', 'agentConnectTitle');
  dialog.innerHTML = `
    <div class="modal-head">
      <h2 class="modal-title" id="agentConnectTitle">接入智能体</h2>
      <button type="button" class="modal-close" data-agent-connect-close aria-label="关闭">
        <svg viewBox="0 0 24 24" width="16" height="16" fill="none" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>
      </button>
    </div>
    <div class="modal-body">
      <pre class="agent-connect-prompt" data-agent-connect-prompt tabindex="0" aria-label="提示词"></pre>
      <div class="agent-connect-url">
        <span class="agent-connect-url-label">MCP 地址</span>
        <code class="agent-connect-url-value" data-agent-connect-url></code>
        <button type="button" class="btn-sm" data-agent-connect-copy-url>复制</button>
      </div>
      <p class="agent-connect-error" data-agent-connect-error role="alert" hidden></p>
    </div>
    <div class="modal-foot">
      <button type="button" class="btn btn-primary" data-agent-connect-copy>复制提示词</button>
    </div>`;
  return dialog;
}

// openAgentConnect 打开「接入智能体」窗口；已经开过就把同一个再拿出来，地址只问一次。
export function openAgentConnect({
  documentRef = globalThis.document,
  fetchImpl = globalThis.fetch,
  clipboard = globalThis.navigator?.clipboard,
} = {}) {
  let dialog = documentRef.querySelector('dialog.agent-connect');
  if (!dialog) {
    dialog = buildDialog(documentRef);
    documentRef.body.append(dialog);
    const prompt = dialog.querySelector('[data-agent-connect-prompt]');
    const url = dialog.querySelector('[data-agent-connect-url]');
    const error = dialog.querySelector('[data-agent-connect-error]');
    const copyPrompt = dialog.querySelector('[data-agent-connect-copy]');
    const copyURL = dialog.querySelector('[data-agent-connect-copy-url]');
    copyPrompt.disabled = true;
    copyURL.disabled = true;
    copyButton(copyPrompt, () => prompt.textContent, { clipboard, documentRef, doneLabel: '已复制，发给智能体' });
    copyButton(copyURL, () => url.textContent, { clipboard, documentRef, doneLabel: '已复制' });
    dialog.querySelector('[data-agent-connect-close]').addEventListener('click', () => dialog.close());
    // 点窗口外面的遮罩关掉（点到的是 dialog 自己、不在内容框里）。
    dialog.addEventListener('click', event => {
      if (event.target !== dialog) return;
      const box = dialog.getBoundingClientRect?.();
      if (!box) return;
      const inside = event.clientX >= box.left && event.clientX <= box.right && event.clientY >= box.top && event.clientY <= box.bottom;
      if (!inside) dialog.close();
    });
    dialog.ready = (async () => {
      try {
        const res = await fetchImpl(RESOURCE_METADATA_PATH, { cache: 'no-store' });
        const meta = res.ok ? await res.json() : null;
        const resource = typeof meta?.resource === 'string' ? meta.resource : '';
        if (!/^https?:\/\//.test(resource)) throw new Error('no resource');
        url.textContent = resource;
        prompt.textContent = agentConnectPrompt(resource);
        copyPrompt.disabled = false;
        copyURL.disabled = false;
        // 第一次打开时按钮还不能点，焦点落在了关闭上；能复制了再交给主动作。
        if (dialog.open && documentRef.activeElement !== copyURL) copyPrompt.focus?.();
      } catch {
        error.textContent = '取不到接入地址，过一会儿再打开试试';
        error.hidden = false;
      }
    })();
  }
  if (!dialog.open) dialog.showModal();
  // 焦点给「复制提示词」，回车即复制（原生 <dialog> 默认给第一个能点的，也就是关闭）。
  const copyPrompt = dialog.querySelector('[data-agent-connect-copy]');
  if (!copyPrompt.disabled) copyPrompt.focus?.();
  return dialog;
}
