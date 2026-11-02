# FoodTalks 发布 API 实测契约（2026-07-10 冒烟，2026-08-14 补测）

> 来源：openspec change `content-publish-pipeline` Phase 1 冒烟测试，以及 2026-08-14 双环境补测。
> 环境：2026-07-10 的 CRUD 样本来自测试环境 `api-we-test.foodtalks.cn`（前台 `test.foodtalks.cn`）；2026-08-14 另核验了正式环境网关、标签、分类与同题查询契约。两类证据不可混写成同一环境的完整冒烟。
> 测试文章：测试环境 7240、7241（标题带 [SYNC-TEST]，测毕均已置 `publishStatus=2` 下线，前台 404）。
> 鉴权 token 为抓包个人 token，**不入库**（视为已泄露级别，正式上线换服务级凭证，见 tasks Phase 0）。

## 1. 网关（**两环境规则相反**；2026-07-10 测试环境、2026-08-14 正式环境实测）

| 环境 | 网关规则 | 客户端头策略 |
|---|---|---|
| **测试**（api-we-test） | 要求 `Origin`+`Referer`+浏览器形 UA，缺任一 → nginx 403（HTML 错误页非 JSON）；无 IP 白名单概念 | 带三头（浏览器头模式） |
| **正式**（api-we） | IP 白名单 + **禁止伪装浏览器**：白名单 IP 发纯 token 请求 → 放行直达应用层；同一 IP 带浏览器伪装头 → WAF 429 持续拦截（curl 的 TLS 指纹 + 浏览器 UA = 伪装特征） | 纯 token，**不带** Origin/Referer/UA（显式空 UA 压掉语言默认值） |

两环境通用：应用层无/错 token → 401（data 回显所传 token）。客户端实现：`FOODTALKS_ORIGIN` env 配置与否即为两模式开关（api/foodtalks.go，契约测试防回退）。

## 2. 响应契约

```json
{"code":0,"msg":"success","data":7240}
```

- `data` = 资讯 ID（number）。POST 返回新 id；PUT 返回同 id。
- **业务失败也是 HTTP 200**，靠 body 判断：`code != 0`，`msg` 为中文人话（例：`不允许将已发布的资讯改为草稿状态！`），`data: null`。
- **客户端必须判 `code` 字段，不能信 HTTP 状态码。**

## 3. publishStatus 枚举（实测）

| 值 | 语义 | 前台表现 | 转移限制 |
|---|---|---|---|
| 0 | 草稿 | 直链 404「你查看的内容不存在」，列表无 | **已发布(1) → 0 被拒**（code=1，单向门）；0 → 1 允许 |
| 1 | 已发布 | 直链完整可见，未登录可看 | 1 → 2/3 允许，2/3 → 1 允许（双向） |
| 2 | 下线（推测语义） | 直链 404，列表无 | — |
| 3 | 未知（推测待发布/审核） | 直链 404，列表无 | — |

- ⚠️ **API 不校验枚举范围**：传 2、3 等值一律 `code=0` 成功。客户端必须自律只用已确认语义的值（0/1/2），别的值行为未定义。
- 「删除」接口未提供；清理测试数据用 `publishStatus=2` 下线即可达到前台不可见。

## 4. 🚨 isPublic 不是可见性开关

- `publishStatus=1 + isPublic=0` 的文章**未登录直链完整可见**——「isPublic=0 即隐藏」的假设不成立。
- `isPublic` 0/1 之间前台可感知差异未观察到；页面上的「有料」标签在两种取值下都出现，**「有料」来自 `categoryIds`（本次用了示例值 52/58）**，与 `isPublic` 无关。
- `isPublic` 真实语义待网站后端确认（Phase 0 问题清单）。

## 5. 流水线状态走法（已验证可行）

```
POST publishStatus=0（草稿进站，外界不可见）
  → 人工审核
  → PUT publishStatus=1（公开）
  → 需要时 PUT publishStatus=2（下线，可再翻回 1）
```

- 「隐藏进站→审核→翻公开」全流程在测试环境走通（7241 全程验证）。
- 注意单向门：**一旦发布过（1），永远回不到草稿(0)**，下线只能用 2。job 状态机里「驳回后回炉」要用 2 不能用 0。

## 6. 前台 URL（测试环境）

- 列表：`https://test.foodtalks.cn/news`（客户端渲染 SPA；SSR 壳不含正文，可见性校验要跑真浏览器或调详情数据接口）
- 详情：`https://test.foodtalks.cn/news/<id>`
- ❌ `we-test.foodtalks.cn` 会 302 到 `test.foodtalks.cn/wefood/...`，而 `/wefood` 前缀路由一律被弹回首页——**不要用**。

## 7. 编辑（PUT）行为

- 传已有 `id` + 全量字段：就地更新，不产生新文章；标题/正文改动即时生效（实测 <3s 可见）。
- PUT 是**全量覆盖**语义（请求体和 POST 相同的完整字段集），不是增量 patch——客户端必须保存/回读完整字段再改。

## 8. 图片链路实测（2026-07-10 补测，硬阻塞已解除）

- **API 提交不转存图片、不校验图片域名**：PUT content 带外域 `<img src>`（排版工具 OSS `fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com`），前台原样引用并正常渲染（文章 7240 实测）。
- 已发布成品里的 `static.foodtalks.cn/image/post/*` 是 **CMS 编辑器粘贴时**的二次转存行为（TinyMCE 侧），API 路径不触发——同一张图内容 hash 前缀可互相印证（`388d9023bb0d`）。
- **流水线图片方案（定型）**：mmbiz → 排版工具既有 `POST /api/oss-upload`（`urls` 模式：服务器内网并发「取图+传 OSS」，实测 19 张 ~1.3s；上游 `WECHAT_UPLOAD_ENDPOINT=localhost:9000/api/wechat-upload`，python 服务）→ content 直接用 OSS URL 提交。零新开发。
- ⚠️ 记录在案的权衡：文章图片资产落在我方 OSS 而非 FoodTalks 存储——桶（fbif-gary-img-hz）若停用/欠费，已发布文章图片全挂。长期可与网站后端商定归一到 static.foodtalks.cn，不阻塞 MVP。

## 9. newsTags 实测与当前写入契约

下表是 2026-07-10 的历史故障矩阵，用来解释旧文章为何曾禁止写标签；2026-08-14 复测已确认后端修复，表中的 PUT 失败不再代表当前行为。

**结构已破解**：库表 `news_news_tag` 每行 `{news_id, tag_id, tag_name}` → API 要的是**对象数组** `[{"tagId": <int>, "tagName": <string>}]`，且 **tagId 必须真实存在**于标签库（`basic_tag`，新闻标签 `tag_type_id=6`）。字符串数组 500 的谜底就是结构不对。

| 操作 | newsTags 形态 | 结果 |
|---|---|---|
| POST | `[]` 空数组 | ✅ code=0 |
| POST | `[{"tagId":6609,"tagName":"FBIF"}]`（真实 ID） | ✅ code=0（7246/7247） |
| POST | `["字符串"]` | ❌ 500「服务器内部异常」 |
| PUT（任意文章） | 含非空 newsTags（真实 ID 也一样） | ❌ 500 |
| PUT | `[{"tagName":"饮料"}]` 缺 tagId | ❌「标签id不能为null」 |
| PUT | 伪 tagId 999999999 | ❌ 500（后端校验/联表失败） |
| PUT（**带过标签的文章**） | 空数组 / 完全省略字段 | ❌ **一律 500** |
| PUT（从未带标签的文章） | 空数组 / 省略 | ✅（7240/7241/7245 冒烟已证） |

**🚨 后端 bug 现象**：UPDATE 路径的 newsTags 处理疑似「先写后炸、非事务」——PUT 带标签必 500，且失败的 PUT 会把文章置成「带标签」脏态，此后该文章**任何 PUT 都 500**（连 publishStatus 都改不了）。
**2026-07-13 补充接口文档结论**：已提供标签类型、标签列表、标签分页查询、按 ID 批量取详情和创建标签接口，可用于把 AI 标签名称核验成真实 `tagId`；但文档没有提供安全的“资讯-标签关联写入”接口，并再次明确 PUT 缺陷修复前 `newsTags` 必须保持空数组。主链路因此继续不写标签；`foodTalksNews.NewsTags` 已收紧为 `[]foodTalksNewsTag`，从类型层禁止误发字符串数组。自动创建标签也不启用，避免把模型输出直接写入全局标签库。

**2026-08-14 复测：PUT 标签缺陷已修复，封印解除。** 测试环境 7321/7322 实测：PUT 带真实 tagId、重复 PUT、PUT 清空、POST 带标签后再 PUT 全部 `code=0`，GET 回读与写入一致；正式环境 62765 也已由运营带标签多次编辑。

当前流水线契约：

- 所有文章的 AI 预填 `job.Tags` 都参与解析；Mote 文章先放「Mote莫特」与正文声明的系列标签，再补 AI 标签。
- 总数最多 3 个，只在新闻标签库 `tag_type_id=6` 中按规范化名称等值解析（忽略大小写与全角/半角空格）；未命中就跳过，不自动创建全局标签。
- 栏目/活动标签双侧拦截（2026-08-17，排版规范「不添加首发栏目为标签」）：`publishSiteTagBlocked` 拦首发栏目专名（快讯/深度专题/创新周报/独家专栏二级等）与活动模式（`FBIF\d{4}`、会后报告、参展企业、Wow 奖）；发布解析时过滤 AI 标签，`editorial_norms.go` 注入 prompt 词表前同样过滤。品类词（乳品/饮料/咖啡…）与品牌名是合法标签不拦。
- `toc`（2026-08-17 对齐排版规范 + 用户裁定「所有文章一视同仁」；2026-08-24 目录只收标题1）：正文非空白字符 ≥3000 且 h2+h3 合计 ≥5 时，`buildSiteNews` 在 CMS payload 组装层注入确定性 h2 锚点并写 `[{id,level,title}]`（条目只收标题1 / h2；标题2 是「1、2、3」或「（一）」→ h3，不进目录，62765 金标准；Mote 文章例外：标题2 以 `level:2` 进目录并打锚点，前台 `.level-2` 缩进显示，2026-09-29 用户裁定）；不再区分原创/转载。交互式网站渲染器仍保持 `mctoc.enabled=false`。
- 守护：`api/publish_site_enhance_test.go`。

### 公司库关联（2026-08-17 生产实测）

- 检索：`GET /company/companies/search?current=1&size=10&keyword=`——前台公开接口，白名单 IP 纯 token/无 token 均可；返回字段 `id/cnName/shortCnName（逗号简称表）/urlCode`。文档记载的 `/company/admin/companies/page` 对当前服务账号返回 `Access is denied`（缺 company 模块权限），勿用。
- 前台公司页 = `https://www.foodtalks.cn/company/<urlCode>`；数字 id 路径打不开（实测「内容不存在」）。
- 流水线：AI 标签内容词与 cnName/shortCnName 规范化等值匹配（多条优先含「中国」，歧义放弃）；正文首次出现处注入 `mc-company-link`（`data-id/data-name/data-code/data-type=company`，对齐小工具箱成品 news/48190），标题/图注/已有链接内不注入；`relatedCompanyIds` 写命中 id。失败降级不 fail。
- 守护：`api/publish_company_links_test.go`、`api/company_match_smoke_test.go`。

## 10. 分类与 CMS 同题防重（2026-08-14）

### 分类

- 原创：`FOODTALKS_CATEGORY_IDS`，默认 `52,58`。
- 转载：`FOODTALKS_REPOST_CATEGORY_IDS`，默认 `53`。
- 两者必须显式分开，不能把转载稿继续写进原创专栏。

### 标题检索

出站请求：`GET /news/admin/news/page?current=1&size=20&title=<文章标题>`。2026-08-14 实测 `title` 能过滤，`keywords` 会被后端忽略；客户端仍会对返回项做二次核验：只认 `publishStatus=1`，标题去全部空白后精确相等，并排除任务自己的 `FoodTalksNewsID`。

同题保护分两层：

1. 发出或刷新待审核卡前 best effort 查询；命中时持久化 `site_duplicate_news_id` 并在卡片标红。查询失败只记 WARN，不阻断卡片。
2. 首次审核公开的 `stagePublicize` 在公开 intent 落盘前重新查询；命中或查询失败都 fail closed。此时尚未进入 FoodTalks 外调窗口，恢复后可以安全重试。

后台手工发布对账（`syncManualCMSPublications`）也走这个接口：默认北京时间 8:30–18:00 每 2 分钟、其余每 30 分钟一轮（时段和间隔在 FBIF 中台总账本「定时任务和回调」改，`CMS_RECONCILE_*`），每篇未发布稿查一次。2026-10-12 网站侧反馈分页查询过多（当时 39 篇 7–9 月积压稿每轮各查一次，约 1,170 次/小时），之后只对账 7 天内建单或发过审核卡的稿；更老的稿在后台手工发布后用 `POST /api/publish/jobs/{id}/ack-cms-published` 补记。

边界：这道硬闸目前只覆盖首次 `stagePublicize`，`stageRepublish` 尚未接入；不能把当前能力描述成“每次重新公开都防重”。运营在 CMS 手工发布或编辑过的稿件始终优先，批量修复不得用流水线库存覆盖。

## 11. 未决问题（转给网站后端 / Phase 0）

1. `isPublic`、`sourceType`、`isLatest`、`commentEnable` 的完整枚举与语义（sourceType 0/1=原创/转载已由 mc-fb 库 163 篇分布反推实锤，见 docs/editorial-norms-foodtalks.md §1）
2. `publishStatus=2` vs `3` 的官方语义区分
3. ~~图片上传接口~~ **已解除**（见 §8：API 不校验图片域名，走排版工具 OSS）
4. 正式环境服务级长效 token 与当前实例目标配置仍须从秘密配置和 readiness 核验；已验证的 Base URL 不再列为未知
5. 「有料」等全部 categoryId 的官方表；当前流水线只使用已验证的原创 `52,58` 与转载 `53`
6. 列表页排序规则（2026 年文章混在 2023 年之后，疑有置顶/权重逻辑，同步后运营可见性预期要对齐）
