# 标题考卷

小标题认错了，先把这篇收进考卷，再改规则。考卷在 `api/testdata/heading-exam/`，每篇三到四个文件：

| 文件 | 是什么 |
| --- | --- |
| `<编号>.html.gz` | 公众号原文（去掉脚本、样式） |
| `<编号>.answer.txt` | 答案：一行一个小标题，`h2 标题` / `h3 标题`；`#` 开头是说明 |
| `<编号>.blocks.json` | 现在的抓取规则抓出的正文块（`api/heading_exam_test.go` 生成） |
| `<编号>.gap.txt` | 网站排出来的小标题和答案还差什么（`tests/heading-exam.test.mjs` 生成），空文件是全对 |

编号用发布任务号（#292 就写 `292`）；不是发布任务的用好认的拼音，比如 `lingshouquan-sam`。

## 收一篇

1. 有 FoodTalks 改定稿的（编辑在 FoodTalks 上改好的就是答案），先在服务器上读正文（只读，一次请求）：
   ```bash
   command ssh aliyun-prod-real 'set -a; for f in /var/www/api/publish.env /var/www/api/.env /etc/fbif-hub/env/layout.env; do . $f >/dev/null 2>&1; done; set +a; curl -s -H "Authorization: Bearer $FOODTALKS_TOKEN" "$FOODTALKS_BASE_URL/news/admin/news/<FoodTalks 编号>"' > /tmp/ft-<FoodTalks 编号>.json
   ```
2. 收原文（任务库里的长链接直接贴）：
   ```bash
   node tools/heading-exam/add.mjs <编号> '<公众号原文链接>' /tmp/ft-<FoodTalks 编号>.json
   ```
   没有 FoodTalks 改定稿的不给第三个参数，照用户说的样子手写 `<编号>.answer.txt`，第一行 `#` 写清是谁哪天说的。
   FoodTalks 上还是旧规则排法、编辑没改过的，答案按现行裁定手改，同样在 `#` 行写清。
3. 生成块和差别，逐个看过再提交：
   ```bash
   cd api && GOLDEN_UPDATE=1 go test -run TestHeadingExamBlocks && cd ..
   GOLDEN_UPDATE=1 node --test tests/heading-exam.test.mjs
   ```

## 改了规则

`go test ./...`、`npm test` 会跑整份考卷：抓出的块变了、或哪篇的差别变了，测试就失败，并列出哪些小标题新出现、哪些没了。看过确认是变好了（或是无关的变化），再用上面第 3 步的两条命令重写。差别变多不能直接重写，先弄清是哪条规则弄坏的。
