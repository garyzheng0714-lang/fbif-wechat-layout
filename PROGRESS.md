# PROGRESS.md

状态：进行中（2026-08-06）

## 理解的目标
上线前把自动订阅链路查透：只许有亲手复现的结论，交 ≤12 条问题清单（含严重度），并用一次生产真实演练证明 201 建单 → awaiting_review + 审核卡 → 幂等 200 → 红线 400 → monitoring ready=true 全链路能走通。

## 任务顺序
0. 核对基线（已完成：go test 过、npm 1044/0/0、生产监控 ready=true queued=0 failed=1 非近期、journalctl 188 条、git 状态与任务书一致）
1. 链路代码审计（site-sync/official-sync → 回源 → 排版 → 风险 → 审核卡 → FoodTalks 草稿 → 监控）
2. 生产实态核查（failed=1 根因、8/5 后上游投递状态码）
3. 生产端到端演练（四段输出 + job id）
4. 遗留了结（PR 68/70 复核、文档提交、watchdog 清理）
5. 收尾：问题清单完整贴出并提交 PROGRESS/BLOCKED

## 最大风险
- 生产演练是唯一写操作：不点审核通过、不重复建稿；投错链接被 422 挡下（无害）。
- 提交即自动部署：提交前必须过完本地门禁（gofmt/vet/staticcheck/race/go test/npm）。
- 审计结论必须亲手复现，宁少勿凑；「没做成但说清了」合格。
- watchdog 清理不得误删 cleanup_legacy_watchdog 与红线守护测试/工作流。

## 任务 1 完成：问题清单（2026-08-06，全部本地复现）

1. **上线阻断｜failed 任务重投按 200 existing 收单，24h 后监控 ready=true，单篇失败对自动链路永久静默**
   - 位置：api/publish_pipeline.go:1539-1550（createSeeded 幂等直接返回 failed 旧单）、api/publish_http.go:292（existing→200）、api/publish_monitoring.go:15/96/157/231（failed 只按 24h 窗口告警）
   - 复现：本地测试重投 failed 任务 → 200 existing=true、job.stage=failed；25h 后 currentPublishMonitoring → ready=true failed=1 failed_recent=0 issues=[]。
   - 影响：上游按契约 §6「200 正常幂等命中，不要重投」收单后不再投；看门狗只看 ready → 该篇永远丢失且无信号。生产现场已有同形态：failed=1 + failed_recent=0 + ready=true。

2. **上线后修｜official-sync 缺 site-sync 的 409/rerender/refetch 行为，规则包切换请求被静默忽略**
   - 位置：api/publish_http.go:308-380（official-sync 无 explicitRulePack 冲突检查、无 RerenderLatest/RefetchLatest 字段），对照 site-sync api/publish_http.go:256-276
   - 复现：已有 ft-default 任务，official-sync 带 rule_pack_id=ft-series 重投 → 200 existing=true，job.RulePackID 仍 ft-default；同场景 site-sync → 409。
   - 契约 docs/publish-intake-contract.md §4/§5 声明两入口字段与 409 行为一致。

3. **上线后修｜契约 §4 称 official-sync classifier_version 缺省 wechat-official-api-v1，实际机器提交缺省被 422 挡下**
   - 位置：api/publish_http.go:355-361（evidence 校验先于 364-366 的缺省赋值）
   - 复现：official-sync 机器请求省略 classifier_version → 422 {"error":"machine sync requires a valid classifier version"}。
   - 影响：上游若照 §4 省略该字段，投递全线 422（无害但不进单）；需上游显式带版本号或修文档/入口。

4. **观察｜监控漏报 unpublish/republish 对账态**
   - 位置：api/publish_monitoring.go:99-102（reconciliation 计数只含 reconciliation_required / publicize_reconciliation_required / staging_creating / publicizing_updating，漏 unpublish_reconciliation_required / unpublishing_updating / republish_reconciliation_required / republishing_updating）
   - 复现：unpublish_reconciliation_required 任务 → ready=true reconciliation=0 issues=[] health=healthy；publicize_reconciliation_required 对照组 → 计数 1。
   - 影响：不在明天上线的主链（主链止于 awaiting_review），属生命周期监控盲区。

5. **观察｜风险检查系统性降级（ARK 故障 → fallback warning）监控无信号**
   - 位置：api/publish_risk.go:180-190（fallbackPublishRisk 恒 warning+system 条目）、api/publish_monitoring.go（无风险健康字段）
   - 复现：awaiting_review 任务带 system 类 warning 风险 → ready=true health=healthy。
   - 影响：审核卡上仍有「检查未完成」橙标给人看，但看门狗不知 ARK 整体故障。

> 开放 issue 影响评估：#8 复制抢跑（前端复制层，与自动订阅无关）；#9 漏转 mmecoa.qpic.cn 图（若发生，stageMirror 会 fail loud 落 failed，属可见失败，不入静默清单）；#10 转载文尾部清理（排版质量，不影响链路闭合）。

## 任务 2 完成：生产实态核查（2026-08-06）

- failed=1 = 任务 #4：created 2026-07-10T09:20Z，failed_stage=fetching，error=「未能从文章解析出标题」，updated 2026-07-10T15:54Z。服务器现场 curl 该 URL：`<title></title>`（首取）→ 第二次 0 字节（限流/失效），即该文链接现已取不到正文。7/10 属红线 33 上线前的旧任务，不会因自动订阅开闸重新触发；但若上游重投该 URL → 200 existing 且永远停在 failed（问题 1 的生产实例）。
- 8/5 红线后上游投递状态码：日志未见 401/400。任务 68–73（8/5、8/6 每日 3 篇）全部带 `classification=ordinary_confirmed`、`classifier=wechat-ordinary-v1`，说明官方线已适配新令牌与机器证据；Caddy 错误日志 8/6 18:32:10 有一条来自 101.133.154.40 的 `POST /api/publish/sync-status`（带新 `X-Publish-Sync-Token`）→ 502，发生在 fbif-api 部署重启窗口（连接拒绝），属瞬时；成功请求 Caddy 不落 error 级日志，无法从本端日志枚举全部状态码。
- 已确认 Caddy → 127.0.0.1:9001 → fbif-api（systemd ExecStart=-port 9001）。

## 任务 3 完成：生产端到端演练（2026-08-06）

演练目标：展会信息服务网 2026-08-02 文章《2026（秋）深圳国际家纺布艺暨家居装饰展览》
http://mp.weixin.qq.com/s?__biz=MzYzNzY0OTU3Mg%3D%3D&mid=2247487578&idx=3&sn=793e4bde3bc0f4f414e12895cbeb7134&chksm=f11698c0ebacac07187d07c3c829b9bbba8991fd0eb9d2909baceb325e1e589e3428fa1&scene=0

四段真实输出：
1. 201 建单：`{"existing":false,"job":{"id":74,...,"stage":"created",...}} HTTP_STATUS=201`（14:02:10Z）
2. awaiting_review：`stage":"awaiting_review","foodtalks_news_id":7320,"available_actions":["review_in_feishu"]`（14:07:29Z）；任务详情证据 `review_cover_img_key=img_v3_0214a_c9fa5305-...`、`review_card_msg_id=om_x100b687d1cdfa8a8c3336e8b5d57746`（群 oc_0e469b...）、`notification_sent_at=2026-08-06T14:07:29Z`、`review_cards_missing=0`
3. 幂等重投：`{"existing":true,"job":{"id":74,...,"stage":"awaiting_review"...}} HTTP_STATUS=200`
4. 红线 400：`{"error":"本接口只接收文章链接，请移除 content_html、title。..."} HTTP_STATUS=400`

收尾监控（14:10:02Z）：`ready=true health=healthy processing=0 queued=0 notification_pending=0 review_cards_missing=0 failed=1 failed_recent=0`。

演练 job id=74；任务停在 awaiting_review，等领导在飞书群手动驳回。演练总耗时 5 分 19 秒（fetch+render+mirror 约 2 秒，enriching 约 3 分钟，assessing 约 2 分钟，staging+卡即时）。

## 任务 4 完成：遗留了结（2026-08-06）

- PR 68/70 复核完成：diff 与当前实现一致（正文红线 400、终态卡 reviewCardHeroBlock、ReviewCoverImgKey、card-refresh 原位更新 + 管理员门）。PR 68 描述「两入口行为一致」与实际不符的点正是问题 2（official-sync 缺 409/rerender），属本次审计发现，非 PR 引入。
- watchdog 残留：`monitoring/watchdog.py`、`monitoring/test_watchdog.py`、`deploy/monitoring/fbif-monitor-watchdog.{service,timer}` 已由 cbabec9/ff4a68e 摘除（git ls-files 与磁盘均无）；`deploy-transaction.sh` 只保留 `cleanup_legacy_watchdog`，无任何安装逻辑（tests/deploy-transaction.test.mjs 锁死）；无需再删，重跑全部测试确认。
- HANDOFF-2026-08-06（边界与监控迁出）内容已确认落入 CLAUDE.md/README.md/docs/INDEX.md，已删除。
- HANDOFF-markdown-feishu-2026-07-28 原指向的 `docs/feishu-document-import.md` 在仓库不存在，已新建该权威文档（docs/feishu-document-import.md）承载其持久事实并在 docs/INDEX.md 登记，随后删除 HANDOFF。
- README.md / docs/INDEX.md 未提交差异与当前实现核对一致（红线 33 描述、三系统边界、watchdog 迁出、P0 job 名耦合），待提交。
- 来历不明仅列清单不动：`artifacts/`（design-qa 对比图 7 张，168K）、`prototypes/`（feishu-auth-import-demo.html + 2 张资源，80K）、`design-qa.md`（8K）。未触碰。

## 收尾：提交方式偏差（2026-08-06）

- 任务书假设「直接提交 main（推送即自动部署）」，实测 `git push origin main` 被 GitHub 分支保护拒绝（required status checks 3 项，protected branch hook declined）。改为从本地 main 建分支 `codex/audit-delivery-2026-08-06` 推 PR，合并后 deploy.yml 照常触发（推送 main 即部署的意图由 PR merge 等价达成）。
- 本地门禁已全过：gofmt -l 无输出、go vet、staticcheck、go test -race、go test ./...、npm 1044/0/0。
