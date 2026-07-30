# 飞书云文档导入契约

本文档是“飞书登录 → `@` 搜索云文档 → 导出 Word → 右侧排版 → 立即复制”链路的当前长期契约。历史调研和过程记录不能覆盖这里的现行行为。

## 用户路径

1. 用户必须使用飞书登录。账号密码登录只支持本地文件、正文和链接排版，不具备搜索、提取或生成飞书云文档的能力。
2. 在“正文 / 链接 / 飞书文档”输入框输入 `@关键词`，从当前飞书账号可访问的文档中选择一篇。
3. 工具显示一个总进度条，同时在后台把飞书文档导出为 DOCX，并复用现有 DOCX 解析和渠道渲染链路。
4. 导入期间保留右侧已存在的文章；新文章没有完整准备好以前，不清空、不隐藏旧预览。
5. 总进度到达 `100%` 时，新文章已经原子替换到右侧，正文图片已解码，复制产物已生成。此时点击“复制全文”不得再等待导出、解析、图片解码或复制层组装。

用户修改 `@` 查询、选择另一篇文档、切换账号或导入组件被销毁时，前端必须取消旧意图并尽力删除服务端任务。普通刷新、关闭标签页或地址栏离开不能保证浏览器成功发出取消请求，残留任务由服务端超时和 TTL 回收。无论任务是否仍在后台收尾，旧结果都不得覆盖新文章；失败或取消时保留原预览并显示可读错误。

## 总进度的真实性

总进度由 `public/js/feishu-doc-import.js` 定义的工作阶段和 `public/js/import-progress.js` 聚合。它是已经完成的可验证工作量，不是剩余时间估计。

- 服务端阶段使用飞书任务状态、下载字节、ZIP 条目和图片处理数量等 `done / total` 数据。
- 浏览器阶段使用结果下载字节、规则加载、DOCX 解析、渲染、图片物化、逐图解码、复制产物和 DOM 提交等真实完成门。
- 不知道总量的阶段保持不动，不按时间匀速增长，也不使用“假装快结束”的动画。
- 任一阶段都只能单调前进；所有必需阶段完成前总进度最高为 `99%`。
- `100%` 只由最终 `commit()` 产生。提交前必须同时满足：
  - 新文章已在连接到页面的离屏 staging 中完成构建；
  - 最终预览节点中的全部图片 `complete` 且 `naturalWidth > 0`；
  - 最终 HTML、页脚和当前复制会话一致；
  - 可直接写入剪贴板的复制产物已经生成；
  - 预览 DOM 和复制状态已同步提交。

阶段权重表达独立、可核验的工作单元，不表达秒数。调整阶段或权重时，必须同时更新单元测试和真实浏览器回归，不能为了让数字“看起来顺”而做时间插值。

## 前端原子提交

`public/js/preview-dom.js` 在页面内创建不可见但真实连接的 staging 根，`public/js/preview-image-loader.js` 在这里解码最终会提交的图片节点。完成后才一次性替换 `#contentArea`。

这解决两个问题：

- 导入期间旧预览仍然可见，不再出现整块白屏。
- 浏览器解码的是最终节点本身，不会发生“临时节点显示成功，换到正式节点又重新加载”的闪烁。

设置图片请求属性时必须先写 `referrerPolicy`、`crossOrigin` 等属性，最后再写 `src`，避免 Chromium 因属性顺序触发第二次请求。

复制走 `public/js/copy-session.js` 的当前会话产物。后台旧任务、旧快照和过期图片结果都不能覆盖新会话。

## 服务端任务 API

所有端点要求有效登录态；搜索和导出还要求飞书登录能力。任务按用户隔离，不能读取或取消其他用户的任务。响应使用 `Cache-Control: no-store`。使用 Cookie 会话时，创建和取消请求还要通过同源校验；带显式 `X-Session-Token` 的 WebView / 服务端 bearer 通道不依赖浏览器 Origin。

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| `GET` | `/api/feishu/docs/search?q=<关键词>&limit=8` | 搜索当前用户可访问的飞书云文档 |
| `POST` | `/api/feishu/docs/export/jobs` | 创建 DOCX 导出任务，返回 `202`、`Location` 和任务状态 |
| `GET` | `/api/feishu/docs/export/jobs/{job_id}?after=<version>&wait_ms=<毫秒>` | 读取进度；支持最长 25 秒的版本长轮询 |
| `GET` | `/api/feishu/docs/export/jobs/{job_id}/result` | 任务完成后下载 DOCX；未完成返回 `409` |
| `DELETE` | `/api/feishu/docs/export/jobs/{job_id}` | 取消仍在运行的任务 |
| `POST` | `/api/feishu/docs/export` | 兼容旧客户端的同步导出入口；新前端不使用 |

状态响应的核心字段：

```json
{
  "job_id": "opaque-id",
  "state": "queued|running|completed|failed|canceled",
  "phase": "download",
  "done": 1024,
  "total": 4096,
  "unit": "bytes",
  "version": 7,
  "status_url": "/api/feishu/docs/export/jobs/opaque-id",
  "result_url": "/api/feishu/docs/export/jobs/opaque-id/result"
}
```

任务结果有容量、并发、频率和生命周期限制：单用户最多 4 个存活任务，全局最多 4 个并发导出，单次飞书下载最多 100 MiB，进程内全部待领取结果合计最多 512 MiB；任务默认保留 15 分钟，结果被下载后缩短为 1 分钟。客户端遇到 `429` 或 `503` 时应尊重 `Retry-After`，不得并发轰炸重试。

## 运维与故障定位

飞书登录、搜索和导出使用 `FEISHU_LOGIN_APP_ID`、`FEISHU_LOGIN_APP_SECRET`、`FEISHU_LOGIN_REDIRECT_URI` 以及用户 OAuth 会话；它们与审核通知使用的 `FEISHU_APP_ID`、`FEISHU_APP_SECRET` 是两套用途。部署前必须在飞书开放平台登记与正式域名完全一致的 OAuth 回调地址，并确保登录应用拥有 `offline_access`、`drive:drive.search:readonly` 和 `drive:export:readonly`。

排查顺序：

1. `/api/health` 是否为 `200`，`version.json` 是否是预期 commit。
2. 当前用户的 `auth_mode` 是否为 `feishu`，`capabilities.feishu_documents` 是否为 `true`。
3. 搜索接口是否能返回目标文档；搜索为空先检查用户权限，不要伪造结果。
4. 导出状态中的 `state / phase / done / total / version` 是否持续推进。
5. `completed` 后结果下载是否成功，浏览器是否能解码所有最终图片。
6. 进度到 `100%` 时同时检查：预览可见、staging 已清理、所有图片 ready、复制按钮可立即反馈。

真实验收必须使用已经飞书登录的正式页面和用户可访问的真实文档。只上传本地 DOCX、mock 搜索结果或只看自动化测试，都不能证明飞书链路可用。

## 回归门禁

至少运行：

```bash
npm test
npm run test:e2e:ci
cd api && go test ./...
```

与本契约直接相关的测试包括：

- `tests/import-progress.test.mjs`
- `tests/feishu-doc-import.test.mjs`
- `tests/feishu-doc-ready.test.mjs`
- `tests/preview-dom.test.mjs`
- `tests/preview-image-loader.test.mjs`
- `tests/copy-session.test.mjs`
- `tests/e2e-regression.spec.mjs`
- `api/feishu_export_jobs_test.go`
- `api/feishu_provider_test.go`

浏览器回归至少覆盖：真实 `@` 搜索、导入期间旧预览持续可见、取消与 latest-wins、图片全部解码后才到 `100%`、完成后立即复制、剪贴板首尾内容完整、控制台无 error。

## 生产验收基线

2026-07-31 在正式版本 `a78ea0902baa41cd56cd03a0a790d01dd985261b` 上，使用真实飞书 OAuth 会话和真实云文档《中国豆腐的下一个10年》完成验收：

- 首次导入约 `5.7s` 到 `100%`；此时最终预览中的 `24/24` 张图片全部 ready，staging 已清理，复制按钮可用。
- 已有文章时再次导入约 `5.34s` 完成；以约 `250ms` 间隔连续采样 20 次，`0–99%` 期间旧预览始终为 `display:block`，内容长度从未归零。
- 到 `100%` 时状态为“排版完成，可以立即复制”；点击后按钮立即显示“✓ 已复制”。
- 剪贴板 HTML 为 23,475 字符，文章首段和末段均存在；正式页面当前构建的控制台 error 数为 0。

后续修改必须至少保持这组基线。文档内容、图片数量和飞书生成耗时会随输入变化，不能把上述秒数写成固定性能承诺。
