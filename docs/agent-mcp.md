# 在智能体里用排版工具（MCP）

同事在 Codex、Claude Code 这类智能体里连上排版工具，让智能体替自己排版、校对。智能体排好后给一个链接，打开就在排版工具里，接着改、点「复制全文」粘进后台，和自己操作一样。

只排版和校对：不存草稿、不发布，不碰 FoodTalks 和任何第三方平台。为什么这么做见 [ADR](adr/2026-11-18-agent-mcp.md)，规则见 CLAUDE.md 红线 51。

## 接上（每人一次）

地址：`https://fbifmp-layout.foodtalks.cn/mcp`

**Codex**

```sh
codex mcp add fbif-layout --url https://fbifmp-layout.foodtalks.cn/mcp
```

加完会直接打开浏览器登录。以后要重新登录：`codex mcp login fbif-layout`。

**Claude Code**

```sh
claude mcp add --transport http fbif-layout https://fbifmp-layout.foodtalks.cn/mcp
```

然后在 Claude Code 里输入 `/mcp`，选 fbif-layout 登录。

别的支持 MCP 的智能体（Cursor 等）：添加远程 MCP 服务器，填上面的地址。

登录时浏览器会打开 FBIF 登录页，用飞书登录，再在确认页点「允许」。之后智能体自己续期；30 天没用或满 90 天要再点一次。只有用飞书登录的 FBIF 同事能用；账号停用、离职，智能体下一次调用就进不来。

## 怎么用

直接跟智能体说，比如：

- 「用 fbif-layout 把这篇排成 FBIF食品饮料创新：https://mp.weixin.qq.com/s/…」
- 「把我刚写的这篇按 FoodTalks 网站排，再校对一下」
- 「把 ~/Downloads/稿子.docx 排成什么是系列」

能交的稿：公众号或网页链接、飞书云文档链接（用你自己的飞书权限读）、Markdown、文字、Word。

智能体回一个链接，打开就是排好的这一篇。校对结果在编辑器左栏「AI 校对」卡上，点「改正」才会改。链接只有本人能打开，留 7 天。

## 工具

| 工具 | 做什么 |
| --- | --- |
| `layout_article` | 按某个账号的样式排一篇；最多等 40 秒，没排完给 `result_id` |
| `get_layout_result` | 接着等没排完的那篇 |
| `check_article` | AI 校对，和编辑器「AI 校对」同一套；模型要想两三分钟，没查完再调一次 |
| `prepare_upload` | 排 Word 前拿一个 10 分钟内有效、只能用一次的上传地址 |

Markdown 的写法：开头一行 `# 标题` 是文章标题，`##` 起是小标题，`-` 列表、`>` 引用照常写，图片写 `![](https://…)` 公开地址。人上传 `.md` 文件也是这套认法。

## 维护

**怎么运转**

- 授权：`api/internal/mcpauth`，本服务自己当授权服务器（MCP 授权规范 2026-07-28），认人走统一登录。
  - 元数据：`/.well-known/oauth-protected-resource`、`/.well-known/oauth-authorization-server`。
  - 授权与令牌：`/oauth/authorize`、`/oauth/token`、`/oauth/register`、`/oauth/revoke`。
  - 授权记录存 `agent-grants.json`（和用户库同一个目录），令牌只存哈希。
- MCP：`api/agent_mcp.go`，用官方 Go SDK（`github.com/modelcontextprotocol/go-sdk`）。
  - 排版和排版接口同一条路（`layoutAPI.launch`，红线 46），成品与网页逐字一样。
  - 校对和网页「AI 校对」同一个 `articleChecker`（红线 49）。
- 交接：
  - 每篇记下交来的输入（链接、Markdown、文字、Word 原件），存 `agent-results/`，留 7 天、总量有上限，存新记录时顺手清。
  - 编辑器打开 `/<渠道>/<账号>?agent=<编号>` 时，由 `public/js/agent-handoff.js` 取回同一份输入重新导入：链接、文字走粘贴，Markdown、Word 走上传。
- Caddy：`/mcp`、`/oauth/*`、`/.well-known/oauth-*` 转给 Go（`deploy/caddy/fbifmp-layout.caddy`）。

**停掉某个人**：在登录中心停用这个人即可，智能体下一次调用、下一次续期都会被拒。

**坏了去哪看**：`journalctl -u fbif-api | grep -E 'agent-mcp|mcpauth'`。日志不打印令牌、授权码。

**测试**

- `api/internal/mcpauth/mcpauth_test.go`：授权流程。
- `api/agent_mcp_test.go`：工具、交接、只给本人、停用即停、不碰平台。
- `tests/agent-handoff.test.mjs`：编辑器交接。
- `tests/layout-parity.test.mjs`：`.md` 认结构。
