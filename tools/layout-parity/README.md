# 排版接口与网页逐字一样：样稿怎么抓

`tests/layout-parity.test.mjs` 拿网页编辑器真抓的复制结果当标准答案，核对排版接口（`render-service/layout.mjs`）6 个账号 × 5 种输入逐字一样。样稿在 `tests/fixtures/layout-parity/`：

| 文件 | 是什么 |
| --- | --- |
| `<账号>--<输入>.html` | 网页上导入、点复制，剪贴板里的 HTML；图片地址统一换成 `IMG` |
| `rules--<账号>.json` | 抓的时候这个账号的激活规则（`/api/rules/active` 原样） |
| `url.fetch.json` | 链接那篇的抓取结果（`/api/fetch-article` 原样，5 个账号一样，只留一份） |

输入：`docx`=`tests/fixtures/tea-pyramid.docx`，`docx-list`=`feishu-ordered-list.docx`，`markdown`=`layout-sample.md`，`text`=测试里那三行字，`url`=`https://mp.weixin.qq.com/s/US1JYCoxRt-3WRgchINBDQ`。

什么时候重抓：改了排版规则默认值、模板、渲染器，这个测试变红，**先在网页上确认新样子是对的**再重抓；不要改测试去迁就。

## 步骤

1. 起采集小服务（给浏览器发输入文件、收样稿，顺带当假的图片转存上游）：
   ```bash
   PARITY_OUT=/tmp/layout-parity/new node tools/layout-parity/server.mjs
   ```
2. 起一个空库的本地排版工具，**端口必须是 19191**（模板用页面域名补全 `/assets` 图，样稿和测试都按这个地址；换端口所有样稿都变）。Go 跑在 19193，前面用 feishu-login-guide 技能自带的假入口占 19191（顶替线上入口，把「这是谁」写进 `X-FBIF-User`）：
   ```bash
   cd api && AUTH_SESSION_SECRET=local-e2e-session-secret-for-tests RULES_STORE_PATH=/tmp/layout-parity/rules.json WECHAT_UPLOAD_ENDPOINT=http://127.0.0.1:19192/api/oss-upload go run . -port 19193
   python3 ~/.claude/skills/feishu-login-guide/templates/gateway/dev-gateway.py --app 19193 --port 19191 --name e2e --account
   ```
   空库启动会写入内置规则，样稿就是按内置规则排的；规则库里有手改过的规则就不是同一份答案。
3. 用 Claude 内置浏览器打开 `http://127.0.0.1:19191`，假入口已经当你登录了。**不要用 Playwright**。
4. 每一篇单独开一次页面（上一篇的本篇状态会留在本地存储里，必须清掉）。一篇三步：
   ```js
   // ① 清本地存储（并让这一页别再写回去）
   Storage.prototype.setItem = function () {}; sessionStorage.clear(); localStorage.clear();
   // ② 打开账号页：公众号 /mp/<账号>，FoodTalks /ft/<账号>
   // ③ 抓
   await import('http://127.0.0.1:19192/harness.js?v=1');
   window.__layoutCaptureStart({ account: 'fbif', input: { id: 'docx', kind: 'docx', file: 'tea-pyramid.docx' },
     footerVisibilityOverride: { blocks: { more_articles: false } } });
   for (let i = 0; i < 84 && !window.__layoutCaptureResult; i++) await new Promise(r => setTimeout(r, 500));
   window.__layoutCaptureResult
   ```
   - `input`：Word/Markdown 用 `{ id, kind: 'docx' | 'markdown', file }`；文字、链接用 `{ id, kind: 'text' | 'url', value }`。
   - **公众号三个账号必须带 `footerVisibilityOverride: { blocks: { more_articles: false } }`**：接口不出「更多文章」，网页上用本篇显隐开关关掉再复制才可比。FoodTalks 两个账号不带。
   - `browser_batch` 一次最多 25 步，按账号分批（一个账号 5 篇 × 3 步 = 15 步）。
   - 抓的时候浏览器在后台也行，harness 会把 `requestAnimationFrame`、`image.decode` 换成定时器。
5. 抓完：`<账号>--url.fetch.json` 5 份应该一样，留一份改名 `url.fetch.json`，其余删掉；把 `.html` 和 `rules--*.json` 拷进 `tests/fixtures/layout-parity/`。
6. 跑 `node --test tests/layout-parity.test.mjs tests/layout-server.test.mjs`，全绿再提交；关掉两个本地服务。
