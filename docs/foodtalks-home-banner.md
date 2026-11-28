# FoodTalks 官网首页轮播图自动换

2026-11-28 用户在 FoodTalks-资讯订阅的会话里拍板，规矩来自 11-23 网站自动化会议的纪要。放在排版工具做：文章在公众号里排第几条、封面、发到 FoodTalks 后的文章编号都在这里，FoodTalks 程序账号的凭证也是这里在用。

代码：`api/publish_home_banner.go`（什么时候建、建哪张、下哪张），`api/foodtalks_banner.go`（FoodTalks 轮播图接口）。测试：`api/publish_home_banner_test.go`，测试环境真跑一遍是 `api/publish_home_banner_live_test.go`（平时跳过）。

## 规矩

1. 每天一张。FBIF食品饮料创新公众号当天的头条，就是公众号文章链接里 `idx=1` 的那篇。只看这篇自己的链接（任务的 `source_url`，短链抓取后认出的同文长链也算），不看 `original_source_url`，那是被转载的原文。认号靠链接里的 `__biz=MzA3ODEzMjg5Mg==`。
2. 这篇发到 FoodTalks 的那一刻（审核通过或自动发布、人工核对确认已公开、后台人工发布后记为已发布，三个出口都算），新建一张首页轮播图：图片用封面 `cover_url`（电脑和手机两个图片都填它），标题用文章标题，点进去打开 `https://www.foodtalks.cn/news/<FoodTalks 编号>`。
3. 转载的头条照用。头条没审核通过、没上 FoodTalks 的，等它上了再换；头条那天（北京时间，按任务进来的时间算）一直没上就不换。之前几天的不补。
4. 首页一直保持 6 张。新的排最前（谁的 sort 比它大就把它的 sort 调到最大加一），首页上多出 6 张的从建得最早的开始下架。FoodTalks 没有删除接口，只能下架。
5. 同一篇只建一次。建之前先在 FoodTalks 后台按链接找（首页在架的，加上按标题查到的，不管下没下架），找到就沿用，不再建。建的那一刻先在任务上记下「可能已发出」，断在中间下一次也是先找再建，所以重试不会建第二张。同一天已经有一张的，别的头条不再建。
6. 建好后有人在 FoodTalks 后台把它下架了，不再动别的图。文章后来在 FoodTalks 下线了，轮播图不跟着下架（没要求，留给人看着办）。

## 怎么跑

- 任务落为已发布的同一次写入里记下待办（任务上的 `home_banner`），马上在后台做；没有定时轮询。
- 没做成的按 1、5、15、30 分钟退避，之后每 30 分钟再试，到头条那天的 24 点还没成就放弃。进程重启后接着做没做完的。
- 连续 3 次没成，或放弃后 24 小时内，`GET /api/publish/monitoring` 报告警 `home_banner_failed:<篇数>`，看门狗照提示找人；做成了告警自己消失。
- 任务上的记录：`state`（pending 要做、creating 新建请求可能已发出、done 好了、skipped 按规矩不建、failed 放弃了）、`day` 头条那天、`banner_id` 建的或沿用的轮播图编号、`retired_ids` 为它下架的旧图、`failures` 连续失败次数、`error` 最近一次原因、`note` 说明。

## 手动补跑、失败后重跑

```bash
# 在 A 机本机，排版接口在 9001 端口；钥匙是中台下发的 PUBLISH_ADMIN_SERVICE_TOKEN
curl -X POST -H "X-Publish-Admin-Token: $PUBLISH_ADMIN_SERVICE_TOKEN" http://127.0.0.1:9001/api/publish/jobs/<任务号>/home-banner
curl -H "X-Publish-Admin-Token: $PUBLISH_ADMIN_SERVICE_TOKEN" http://127.0.0.1:9001/api/publish/jobs/<任务号>   # 看 job.home_banner
```

手动要的不看是不是当天（给上线当天补跑、拖过零点的重跑用），给 24 小时去做，其余规矩照旧：不是 FBIF食品饮料创新的头条、还没发到 FoodTalks 的回 409；建好了的原样返回，不建第二张。

## FoodTalks 轮播图接口（2026-11-27 测试环境实测，11-28 正式环境读过）

群硕的接口文档是 `后台轮播图管理相关接口_20261123.md`（用户本机下载目录），文档有几处和实测不一样，以这里为准：

| 做什么 | 接口 | 实测要点 |
| --- | --- | --- |
| 新建 | `POST /basic/admin/advertisement` | 建好立刻上线，没有草稿；回 `data` 是新编号；没有幂等键 |
| 修改 | `PUT /basic/admin/advertisement` | 本功能不用 |
| 上下架 | `PUT /basic/admin/advertisement/banned?id=&bannedStatus=` | 参数只认网址里的，放请求体回参数错误；1 下架、0 上架 |
| 排序 | `PUT /basic/admin/advertisement/sort?id=&sort=` | sort 大的排前面（文档写反了）；默认 sort 等于 id，新建的本来就排最前 |
| 后台列表 | `GET /basic/admin/advertisement/page` | `position`、`isBanned` 精确筛，`title` 模糊查，按 sort 从大到小 |
| 详情 | `GET /basic/admin/advertisement/{id}` | |
| 前台列表 | `GET /basic/banner/list?position=home&size=` | 首页看到的就是它：在架、启用的，sort 从大到小 |

- 没有删除，`DELETE` 回 500。`position` 填 `news` 也收，但哪里都不显示；首页用 `home`。
- 图片可以直接填我们的存储桶 `fbif-gary-img-hz` 的地址，官网能正常显示。人工放的那几张图片在 `static.foodtalks.cn/image/banner/`，电脑和手机各裁一张，`tagId` 都是空。
- 请求头和发资讯一样：正式环境 `api-we.foodtalks.cn` 只带凭证和 Accept，带浏览器的 User-Agent、Origin、Referer 网关回 429；测试环境 `api-we-test.foodtalks.cn` 反过来要带这三个。凭证就是本服务发资讯用的 FoodTalks 凭证（中台 `foodtalks.cms.layout`），账号 ai_news 从 2026-11-27 起有轮播图新建、修改、上下架权限。

## 测试环境走一遍

测试环境的凭证从集成平台取凭证的回调拿（在 A 机上调，请求体不是 `"prod"` 就回测试环境的），只放在 A 机 root 才能读的临时目录里，不打印。本机 `GOOS=linux GOARCH=amd64 go test -c` 编出测试程序，连同 `api/testdata` 传到 A 机临时目录，用 `env -i` 只带测试环境的地址和凭证跑：

```bash
env -i PATH=/usr/bin:/bin HOME_BANNER_LIVE_TEST=1 HOME_BANNER_LIVE_NEWS_IDS=<测试环境两篇文章编号> \
  FOODTALKS_BASE_URL=https://api-we-test.foodtalks.cn FOODTALKS_ORIGIN=https://test.foodtalks.cn \
  FOODTALKS_FRONT_URL=https://test.foodtalks.cn FOODTALKS_TOKEN=<测试环境凭证> \
  ./api.test -test.run 'TestHomeBannerLiveOnFoodTalksTest$' -test.v
```

它用真代码建两张 `[SYNC-TEST]` 轮播图，把记录改回「可能已发出」再跑一遍、确认沿用不建第二张，确认新的排最前、字段和链接对，最后把这次建的都下架。跑完删掉临时目录。

## 上线记录

### 2026-11-28 测试环境

- 测试环境首页原有 1 张（1220，2023 年的测试内容）。
- 建了 1224（点进去 `test.foodtalks.cn/news/7346`）和 1225（`/news/7347`）；两次「记录丢了重跑」都沿用原来那张，没建第二张；1225 排最前，图片、手机图片、链接都对。
- 测完 1224、1225 都已下架。前台列表核对：首页又只剩 1220。11-27 那次实测留下的 1221、1222 本来就是下架的。
- 下架最旧那一步的挑法（按建的时间从早到晚）测试环境没真跑：首页只有别人的 1 张，不想动它；挑法由单元测试 `TestHomeBannerCreatesNewestAndRetiresOldest` 照正式首页 11-28 那 6 张考，下架接口本身测试环境真跑了。

### 2026-11-28 正式环境（提交 486d620）

- 上线前首页 6 张：5874 Tims 三季报（11-25）、5873 过气网红 6100+ 店（11-24）、5872 希腊酸奶（11-20）、5871 星巴克星现烤（11-19）、5870 韩国养乐多（11-18）、5869 奥乐齐等集体加姜（11-17，最旧）。
- 22:43 对任务 #410《杨洋推着小车给打工人送可口可乐，这波操作为何刷屏？》（FoodTalks 63487，公众号链接 `idx=1`、FBIF食品饮料创新的号，当天 08:48 进来）调手动补跑。建了轮播图 **5875**：图片和手机图片都是这篇封面 `fbif-gary-img-hz/wechat-layout/04c5c3684ac6.jpg`，标题同文章，链接 `https://www.foodtalks.cn/news/63487`。下架了 **5869**（奥乐齐那张）。任务记录 `state=done banner_id=5875 retired_ids=[5869]`。
- 再调一次补跑，原样回 done、5875，没建第二张；后台按标题「杨洋」查只有 5875 一张在架（另两张是 2022 年早就下架的别的图）。
- 前台列表 `GET /basic/banner/list?position=home` 核对：一共 6 张，第一张是 5875、链接 `/news/63487`；5869 在后台详情里是下架。
- 浏览器打开 www.foodtalks.cn 首页：轮播第一张是这篇的封面和标题，底下 6 个点，轮播里的 6 个链接没有 63422（奥乐齐）了；点第一张打开的是 `/news/63487` 这篇文章。
- 监控 `home_banner_failed` 为 0，没有告警。
- 测试环境凭证和测试程序跑完已从 A 机删掉。
- 没查的：明天起的自动那条路（当天头条一发布就自己建）没真跑过，等 11-29 头条发到 FoodTalks 时看任务记录和首页；本次部署往服务器传程序文件用了 15 分钟（平常 1 分钟），是那一刻网速慢，部署本身成功。
