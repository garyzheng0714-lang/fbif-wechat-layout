# WORKLOG：放宽 Mote 作者识别（含 mote/莫特 变体）+ 一次性迁移（2026-07-11）

## 用户需求

Mote 作者识别要覆盖：`mote`、`莫特`、`mote 莫特`、`Mote莫特`、`MOTE莫特`（含大小写/空格/单独出现）。
即「作者名规范化后包含 mote 或 莫特」就算 Mote 作者。补规则后上线。

## 现状（读代码 + curl 生产确认）

Mote 识别有**两个**匹配点，且都太严：
1. **footer profile 匹配**（`article-footer-defaults.js aliasGroupMatches`，用 includes）：
   `author_aliases.mote = ["Mote莫特","Mote 莫特"]`，normalize 后都是 `mote莫特`。
   `actual.includes("mote莫特")` → 单独的 `mote` 或 `莫特` 匹配不到。
2. **署名图 behavior 条件**（`rule-behavior.js` condition `is_mote_author`）：
   `all:[{path:article.author_normalized, op:eq, value:"mote莫特"}]` → **精确等于**，
   `mote`/`莫特` 单独都不触发署名图注入。

**持久化情况（关键）**：生产 `/api/rules/active` 两处值都 = 旧内置默认。核对存储：
- `behavior.conditions` 已持久化（本地 store raw 有 `eq mote莫特`）→ 改代码默认被 `mergeRuleBehavior` 覆盖
- `config.modules.article_footer_defaults` 已持久化（生产返回 enabled 完整版，`mergeRuleDefaults`
  §151-153 只有显式配置才非 disabled）→ 改代码默认被覆盖
⇒ **只改代码默认不够，必须更新持久化数据**。无生产 admin 密码，走一次性迁移。

## 方案

**新匹配值**（includes 覆盖全变体）：
- `author_aliases.mote = ["mote", "莫特"]`
- condition `is_mote_author = any([includes "mote", includes "莫特"])`

**一次性迁移**（内存，加载时；只命中「等于旧内置默认」，自定义过的不动）：
- `mergeRuleBehavior`：持久化 conditions == 旧 `is_mote_author`(eq mote莫特) → 用 baseline 新默认
- `mergeRuleDefaults`：`article_footer_defaults.author_aliases.mote == ["Mote莫特","Mote 莫特"]`
  → 就地替换为 `["mote","莫特"]`

**改动清单**：
- Go `rule_behavior.go`：defaultRuleBehavior conditions 新值 + mergeRuleBehavior 迁移 + isLegacyMoteConditions
- Go `rule_defaults.go`：defaultArticleFooterDefaultsModule author_aliases 新值
- Go `rule_presets.go`：mergeRuleDefaults 调 migrateLegacyMoteAliases
- 前端 `rule-behavior.js`：DEFAULT_BEHAVIOR conditions 新值（离线 fallback）
- 前端 `admin-article-footer-defaults.js`：author_aliases 新值（fallback）
- 前端 evaluateCondition/compareCondition 已支持 any + includes（无需改）
- 选择器 profileAuthorNormalized 取 aliases[0]="mote"→注入身份"mote"→condition includes mote 触发（一致）

## 验收（全部完成）

- [x] Go 单测（rule_mote_alias_migration_test.go 6 条）：conditions/aliases 旧默认→新；自定义保留；内置默认新值
- [x] 前端测试：署名图 6 变体命中 + 非 Mote 不误伤；profile 匹配 6 变体 + 非 Mote 不误伤
- [x] go test 全绿；npm test 877 全绿
- [x] 本地起服务：store 持久化的旧 `eq mote莫特` 加载即迁移为 `any/includes`（curl 确认）
- [x] 本地浏览器：导入「莫特」「MOTE莫特」「Mote」「mote 莫特」均注入署名图，张三对照不误伤
- [x] 部署 success（run 29146551686）；生产 curl：conditions=any/includes、author_aliases=[mote,莫特]（迁移生效）
- [x] 生产真机端到端：三种写法署名图 + 面板 Mote profile + 署名图行全部识别

## 结论

Mote 识别已放宽为「作者名规范化后含 mote 或 莫特」，覆盖用户所有写法。生产两处持久化的
旧值经一次性迁移自动升级，无需 admin 密码。迁移只命中「等于旧内置默认」的值，后台自定义
过的条件/别名不受影响。教训：改规则数据前先分清「代码默认 vs 持久化」——merge 会用持久化
覆盖代码默认，光改默认对已持久化的账号不生效。

## 时间线

- 2026-07-11 需求 + 现状调研（两处匹配、都持久化）+ 迁移方案，开工。
