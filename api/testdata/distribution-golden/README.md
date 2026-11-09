# 多平台分发样稿考卷（前一半）

编辑手发在头条号、百家号、知乎号上的真稿，是外发版式的标准答案（红线 43）。考卷分两半：

- 前一半在这里（`api/distribution_golden_test.go` 的 `TestDistributionBlocksGolden`）：拿 FoodTalks 已发布正文，用现在的通用规则（`api/distribution_rules.go`）走一遍真实外发组装，写出交给分发服务的标题、封面与外发通用版正文块。
- 后一半在分发服务（FBIF-多平台分发工具 `test/golden/`）：拿同一份块按各平台写法写成标签，和编辑稿按读者看到的样子逐块比。

这里每篇稿两个文件：

- `<编号>.json`：这篇在 FoodTalks 的已发布正文（外发的输入）、正文里每张图的尺寸。
- `<编号>.blocks.json`：用现在的规则写出的标题、封面、正文块；分发服务 `test/golden/` 里有一份一样的。

`cd api && go test -run TestDistributionBlocksGolden .` 块一变就失败，并列出多了、少了哪几块。看过确认后重写，再拷到分发服务跑后一半：

```bash
cd api && GOLDEN_UPDATE=1 go test -run TestDistributionBlocksGolden .
cp testdata/distribution-golden/*.blocks.json <分发工具仓库>/test/golden/
cd <分发工具仓库> && npm test
```

两边是否一致（块和测试用的平台限制 `testdata/distribution-platforms.json`）：

```bash
cd api && DISTRIBUTION_REPO=<分发工具仓库> go test -run TestDistributionFixturesMatchService .
```

## 加样稿

只收编辑在平台上手发的稿，不收系统推的。

1. 在 A 机跑 `sudo python3 tools/distribution-golden/fetch_sources.py <编号…>`，在 `/tmp/golden/` 下得到 `ft-<编号>.json`（FoodTalks 正文）。
2. 把这些 `ft-*.json` 放进 B 机一个目录（容器里的 `node` 用户要能写），用分发服务的镜像另起一个用完即删的容器跑 `tools/distribution-golden/fetch_platforms.mjs <起始日期>`（命令在脚本开头）：它不登录、不碰分发服务的浏览器，翻 FBIF 头条号、百家号从那天起发的每一篇，按正文对上编号（编辑常改标题），在同一目录写出 `tt-<编号>.json`、`bjh.json`，和列出每一篇标题、封面、对上哪个编号的 `posts.json`。没对上的是不经排版工具的稿（老稿补发等），不收。
   知乎号另跑 `tools/distribution-golden/fetch_zhihu.mjs <编号>=<专栏文章链接> …`（同样的用完即删容器，命令在脚本开头），写出 `zh-<编号>.json`：知乎不登录不让搜、机构号的文章列表要登录，文章链接由人给（2026-11-09 按标题在百度 `site:zhuanlan.zhihu.com` 搜的）；只收作者是 FBIF 机构号的。
3. 只留要收的编号（系统会外发的原创、编辑在平台上手发的），把这几个文件拷到本机同一个目录，然后生成样稿。这一步要联网量图片尺寸，FoodTalks 正文写进这里的 `<编号>.json`，编辑稿（`tt-`、`bjh.json`、`zh-` 有哪个写哪个平台）写进分发服务的 `test/golden/<编号>.<平台>.json`：

   ```bash
   cd api && GOLDEN_BUILD_RAW=<目录> DISTRIBUTION_REPO=<分发工具仓库> go test -run TestDistributionGoldenBuild .
   ```

4. 用 `GOLDEN_UPDATE=1` 生成 `<编号>.blocks.json`，拷到分发服务，在那边用 `GOLDEN_UPDATE=1 npm test` 生成差别记录，逐条看过再提交两边。

## 加平台

平台在分发服务加（`service/platforms/` 一份配置，见它的 `test/golden/README.md`），这边不用改代码：排版工具经 `GET /v1/platforms` 取限制。分发服务改了平台文件，跑它的 `npm run gen:docs` 会同时重写 `test/fixtures/platforms.json`，拷到这边的 `api/testdata/distribution-platforms.json`。
