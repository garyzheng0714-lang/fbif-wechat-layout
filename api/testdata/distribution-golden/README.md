# 多平台分发样稿考卷

编辑手发在头条号、百家号上的真稿，是外发版式的标准答案（红线 43）。每篇稿两类文件：

- `<编号>.json`：这篇在 FoodTalks 的已发布正文（外发的输入）、正文里每张图的尺寸、各平台编辑手发的正文（只留标签结构和文字）。
- `<编号>.<平台>.txt`：用现在的规则组装出来的内容，和编辑稿还差哪几处。

`cd api && go test -run TestDistributionGolden .` 把每篇样稿在每个平台上都考一遍。差别一变测试就失败，并列出新出现的差别和消失的差别。看过确认后重写记录：

```bash
cd api && GOLDEN_UPDATE=1 go test -run TestDistributionGolden .
```

可以确认的情况有两种：差别变少了，或者新的差别是编辑稿那处的个例。

## 加样稿

只收编辑在平台上手发的稿，不收系统推的。

1. 在 A 机跑 `tools/distribution-golden/fetch_sources.py <编号…>`，在 `/tmp/golden/` 下得到 `ft-<编号>.json`（FoodTalks 正文）和 `tt-<编号>.json`（头条号正文）。
2. 把 `tools/distribution-golden/fetch_baijiahao.mjs` 拷进 B 机容器 `fbif-distribution-browser` 的 `/app/`，跑 `node /app/fetch_baijiahao.mjs '{"<编号>":"<标题>"}'`，得到 `/tmp/bjh.json`。跑完用 `docker exec -u 0` 删掉拷进去的脚本。
3. 把这几个文件拷到本机同一个目录，然后生成样稿。这一步要联网量图片尺寸：

   ```bash
   cd api && GOLDEN_BUILD_RAW=<目录> go test -run TestDistributionGoldenBuild .
   ```

4. 用 `GOLDEN_UPDATE=1` 生成差别记录，逐条看过再提交。

## 加平台

先在 `yunsuo_platform_profiles.go` 加好这个平台的 profile。跑 `cd api && SPEC_UPDATE=1 go test -run TestDistributionPlatformSpec .` 生成它的平台说明书 `docs/platforms/<Key>.md`，看一遍每种块写成什么对不对。然后在样稿 json 的 `platforms` 里加上编辑在这个平台手发的正文，key 用 profile 的 `Key`。抓取脚本照头条号、百家号的写法再写一个。
