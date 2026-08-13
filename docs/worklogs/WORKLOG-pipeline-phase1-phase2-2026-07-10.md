# WORKLOG：内容发布流水线 Phase 1（FoodTalks API 冒烟）+ Phase 2（render 支持网站模板）

> 状态：Phase 1 / Phase 2 已完成。本文件是 2026-07-10 的阶段记录；其中“`freepublish` 拉到多少算多少 + 手动兜底”等发现策略已被 2026-07-15～20 的历史全量、独立已发布轮询与 P0 监控契约取代，不能作为当前上线口径。当前边界以 CLAUDE.md 红线 #28–#30、README 和现行 OpenSpec 为准。

## 2026-07-10 第二批用户构思（原话要点 + 裁定，Phase 3 开工同轮）

1. **架构人话讲解**：已交付 Artifact「内容发布流水线 · 全景图」（含模块地图/文章八站旅程/AI 质检铁闸/登录矩阵/M1-M6 路线）。
2. **排版工具加登录**：飞书登录（走 ~/.claude/skills/feishu-login-guide v4 单应用模式，生产锚点 feed.foodtalks.cn）+ 外部兼职账号密码（skill 的 account-password 场景；首个账号 mrfood，密码见用户会话原话，**只进服务端加密存储，不进 git/文档**）。权限分层：兼职只能排版，规则后台不可见。→ 排队为 M2，与 M1 并行。
3. **LLM 排版质检**（用户原构思「排版工具抽象为智能体」，经讨论收窄为「机器出口加 AI 质检岗」）：豆包 Doubao-Seed-2.1-pro（ARK responses API，多模态），**ARK_API_KEY env-only 不进 git**。铁律：模型只改排版不改内容，由代码三道铁闸强制（纯文本逐字符一致/图片清单一致/链接一致，违者弃用模型输出并告警）；质检失败不阻塞流水线（原样放行+审核消息标注）。一期读 HTML 结构审（M3），二期截图视觉审（M5，复用本仓库 Playwright 基建）。
4. Phase 3 已开工：internal/foodtalks 客户端（资讯订阅仓库）后台代理进行中。

- 日期：2026-07-10
- 依据：`openspec/changes/content-publish-pipeline/tasks.md` Phase 1 / Phase 2
- 用户授权：2026-07-10 用户回复「开跑」——批准 Phase 1 对 FoodTalks **测试环境** 的真实写入冒烟 + Phase 2 本仓库代码工作
- 安全边界（不可越）：只碰测试环境 `api-we-test.foodtalks.cn`；文章标题必须带 `[SYNC-TEST]`；全程保持隐藏态验证，测完下线/删除；不发飞书消息；不碰公众号写接口；token 不进 git/日志

## 目标

- Phase 1：实测 FoodTalks 发布 API 契约（创建隐藏→验证不可见→PUT 编辑→可见性双向翻转→清理），回填 design.md D3
- Phase 2：`/api/render` 支持 `packId=ft-default` 输出网站模板 HTML，fbif 输出逐字节不变

## 时间线

- [完成] lark-cli 读接口文档（wiki PM9EwLPrAiD6mQkuDYWcAOPUnWb → docx）：POST/PUT `api-we-test.foodtalks.cn/news/admin/news`，抓包 Bearer token，响应体/枚举文档空白
- [完成] 2.1 创建隐藏文章：初次 403（nginx 层拦无 Origin/Referer 的请求），补 `Origin`+`Referer`+浏览器 UA 后 200
- [完成] Phase 2 已派后台代理（render-service 支持 ft-default），与 Phase 1 并行
- [完成] 2.2 可见性验证——**发现 isPublic 语义与假设不符**（见下）
- [完成] 2.3 PUT 编辑：7240 就地更新（同 id、无新文章、全量覆盖语义、<3s 生效）
- [完成] 2.4 可见性翻转：1→3(404)→1(恢复)→2(下线)双向可用；**单向门：发布过回不到草稿 0**（code=1 中文报错）
- [完成] 补测「创建即隐藏」：7241 以 publishStatus=0 草稿进站（外界 404）→ PUT 翻 1 发布（可见）→ PUT 翻 2 下线——**流水线「隐藏进站→审核→公开」全流程走通**
- [完成] 2.5/2.6 契约入库 `docs/foodtalks-api-contract.md`（.gitignore 单独放行）+ design.md 三处回填 + tasks.md 勾选
- [完成] 清理复核：列表页无 [SYNC-TEST] 残留，7240/7241 均停在 publishStatus=2 下线态

## 实测记录（Phase 1）

### 网关行为（写进未来客户端）
- 不带 `Origin`/`Referer` → nginx 直接 403（HTML 错误页，非 JSON）；带上后才到应用层
- 应用层无 token → 401

### 创建（POST /news/admin/news）
- 请求：`id:""`, `publishStatus:1`, `isPublic:0`, title 带 [SYNC-TEST]
- 响应：`{"code":0,"msg":"success","data":7240}` —— **data 直接是资讯 ID（number）**

### 🚨 isPublic 不是可见性开关
- publishStatus=1 + isPublic=0 的文章，**未登录浏览器直链完整可见**：`https://test.foodtalks.cn/news/7240`（截图留档）
- 页面标题下出现「**有料**」标签 → isPublic 疑似控制「公开 vs 有料（付费/会员）」栏目属性
- 隐藏必须靠 publishStatus 非 1（待实测哪个枚举值）
- 前台路由：列表 `test.foodtalks.cn/news`，详情 `test.foodtalks.cn/news/<id>`；`we-test.foodtalks.cn` 会 302 到 `test.foodtalks.cn/wefood/...` 但 `/wefood` 前缀路由实际被弹回首页（`/wefood` 疑似历史路径）
- 列表页顶部未出现 7240（isPublic=0 时不进列表？还是列表缓存？待翻转后复核）

### 完整契约（唯一真相源）
后续全部实测结论（publishStatus 枚举与单向门、错误响应形态、PUT 全量覆盖语义、前台 URL、未决问题清单）已沉淀到 **`docs/foodtalks-api-contract.md`**，本 WORKLOG 不再重复维护。

## Phase 2 进度

- [完成] 3.1–3.5 后台代理交付，主线亲自验收：npm test 792/792 全绿、`git diff -- public/` 零字节、CHANNELS 参数表无 packId 散落分支——提交 2f201ac
- [完成] 3.6 真实文章同源验证（文章 62285「星巴克茶品牌」，公众号原文 → fetch-article 188 blocks → render ft-default）：
  - 方法：浏览器人工链路（本地 /ft/default 导入同 URL）取 `_captureSessionSnapshot` 复制层原料，页面内 dynamic import 同一份 processForCopy 复刻后处理；两侧比结构指纹（标签+属性名序列，规避安全过滤不外传内容）
  - 结果：正文中段 498 标签逐组指纹 100% 一致；API 头部多 Mote 作者行（p>em>span #888888 斜体）、尾部多版权声明段——**与已发布成品 62285.gt.html 头尾逐标签同构**（成品为真值：API 是对的）
  - 浏览器本地缺两段的归因：本地种子规则 ft-default 页脚关闭（tip 不出）+ 作者行被 UI 提取进 CMS 摘要/作者表单字段。属规则数据态与产品交互差异，非渲染代码不同源
  - 附带发现：网站渠道复制按钮有「mmbiz 未转存则阻止复制」守卫（本地无上传端点复制被拦），API 路径无此关卡——图片转存是 Phase 3 任务 4.3 发布方职责，与 agent 遗留 #3 一致

## 踩坑 / 决策

（待填）

## 待办

- [x] 读飞书接口文档，提取：endpoint 全集、字段、token
- [x] 2.1 创建 [SYNC-TEST] 隐藏文章
- [x] 2.2 验证隐藏态外部不可见（浏览器截图留档）
- [x] 2.3 PUT 编辑验证
- [x] 2.4 可见性双向翻转 + 清理
- [x] 2.5 实测契约回填 design.md
- [x] 2.6 冒烟脚本与记录入库 docs/（`docs/foodtalks-api-contract.md`）
- [x] Phase 2：3.1–3.5 代理交付+主线验收（792 全绿，提交 2f201ac）；3.6 同源验证通过；3.7 部署成功（version=7fad2fc）；3.8 生产重放真实文章 30078 字节与本地同长——**Phase 1 + Phase 2 全部完成**

## Phase 0 前置项落定（2026-07-10 用户答复）

- 飞书 App Secret：**用户裁定不重置、直接用**（仍 env-only）
- 图片转存：用户指路排版工具 OSS 接口 → 实测定型（下条）
- 当时设计曾写“freepublish 拉到多少算多少 + 手动兜底”；该口径已被 2026-07-16～20 的独立“发布”轮询、独立“群发”发现路径和 P0 非绿色缺口监控取代

## 图片链路实测（硬阻塞解除）

- 链路：`/api/oss-upload`（Go 代理）→ `WECHAT_UPLOAD_ENDPOINT=localhost:9000/api/wechat-upload`（python 服务）→ OSS 桶 `fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com`
- 生产实测：真实 mmbiz 图转存成功且公网可访问；文件名 hash 与已发布成品 static.foodtalks.cn 同图印证「CMS 粘贴才二次转存」
- 测试环境实测（文章 7240，测毕已收回下线态并浏览器复核 404）：API 提交带我方 OSS 图的 content，前台**原样引用并正常渲染**——FoodTalks API 不校验图片域名，不需要索要其上传接口
- 权衡记录：图片资产落我方 OSS，桶停用则文章图全挂（长期与网站后端商定归一，不阻塞 MVP）

## 交接要点（下一阶段 Phase 3 用）

- 生产 render 服务端口 **19096**（非 19091——19091 被 docker 占用；unit 里 Environment=RENDER_PORT=19096），RULES_API_ORIGIN=127.0.0.1:9001
- ft-default 模板对非契约形态 blocks 静默输出空 HTML（fbif 则宽容输出）；调用方必须用 fetch-article / html-to-blocks 的原生输出
- 生产 ft-default 激活规则当前对真实文章的输出与本地默认种子等效（30078 字节一致）；页脚显隐跟随服务端运营态，上线流水线前复核 bottom_blocks.enabled
- 图片转存（mmbiz → 网站存储）不在 render API 职责内，是资讯订阅仓库 Phase 3 任务 4.3；浏览器人工链路的「未转存阻止复制」守卫在 API 路径不存在
