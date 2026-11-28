package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 2026-11-28 正式任务 #410 的公众号链接（FBIF食品饮料创新当天头条）。
const homeBannerTestHeadlineURL = "http://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg%3D%3D&chksm=8588f25d&idx=1&mid=2652945469&scene=0&sn=a5fffea7"

// fakeBannerSite 内存里的官网轮播图后台：建好立刻上线、sort 默认等于 id、没有删除。
type fakeBannerSite struct {
	mu       sync.Mutex
	banners  []foodTalksBanner
	nextID   int64
	creates  []foodTalksNewBanner
	bans     []int64
	sorts    map[int64]int64
	clock    time.Time
	createOK bool  // false 时建上了但回错（像请求超时），考重试不建第二张
	listErr  error // 列表出错，考失败退避
}

func newFakeBannerSite(t *testing.T) *fakeBannerSite {
	t.Helper()
	site := &fakeBannerSite{nextID: 5875, sorts: map[int64]int64{}, createOK: true,
		clock: time.Date(2026, 11, 28, 9, 0, 0, 0, publishExternalAutoZone)}
	// 正式首页 2026-11-28 上的 6 张人工图（编号、建的时间照抄）。
	for i, created := range []string{"2026-11-17 11:04:28", "2026-11-18 09:01:36", "2026-11-19 09:23:04",
		"2026-11-20 10:49:49", "2026-11-24 09:19:13", "2026-11-25 08:55:02"} {
		id := int64(5869 + i)
		site.banners = append(site.banners, foodTalksBanner{
			ID: id, Title: fmt.Sprintf("人工 %d", id), URL: fmt.Sprintf("https://www.foodtalks.cn/news/%d", 63400+i),
			Position: "home", Sort: id, IsActive: 1, CreatedTime: created,
		})
	}
	// 早就下架的一张，考列表只看在架的。
	site.banners = append(site.banners, foodTalksBanner{ID: 5868, Title: "旧的", URL: "https://www.foodtalks.cn/news/63392",
		Position: "home", Sort: 5868, IsBanned: 1, IsActive: 1, CreatedTime: "2026-11-12 13:48:35"})
	return site
}

func (s *fakeBannerSite) CreateBanner(_ context.Context, b foodTalksNewBanner) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creates = append(s.creates, b)
	id := s.nextID
	s.nextID++
	s.clock = s.clock.Add(time.Minute)
	s.banners = append(s.banners, foodTalksBanner{ID: id, Title: b.Title, Img: b.Img, ImgMobile: b.ImgMobile, URL: b.URL,
		Position: b.Position, Sort: id, IsActive: 1, CreatedTime: s.clock.Format("2006-01-02 15:04:05")})
	if !s.createOK {
		return 0, errors.New("FoodTalks 接口不可达（POST /basic/admin/advertisement）: context deadline exceeded")
	}
	return id, nil
}

func (s *fakeBannerSite) ListBanners(_ context.Context, q foodTalksBannerQuery) ([]foodTalksBanner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	var out []foodTalksBanner
	for i := len(s.banners) - 1; i >= 0; i-- {
		b := s.banners[i]
		if (q.Position != "" && b.Position != q.Position) || (q.OnlyLive && !b.live()) ||
			(q.Title != "" && !strings.Contains(b.Title, q.Title)) {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

func (s *fakeBannerSite) BanBanner(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.banners {
		if s.banners[i].ID == id {
			s.banners[i].IsBanned = 1
			s.bans = append(s.bans, id)
			return nil
		}
	}
	return &foodTalksAPIError{Code: 1, Msg: "广告不存在"}
}

func (s *fakeBannerSite) SortBanner(_ context.Context, id, sort int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.banners {
		if s.banners[i].ID == id {
			s.banners[i].Sort = sort
			s.sorts[id] = sort
			return nil
		}
	}
	return &foodTalksAPIError{Code: 1, Msg: "广告不存在"}
}

// homepage 前台首页看到的：在架的，sort 大的在前。
func (s *fakeBannerSite) homepage() []foodTalksBanner {
	live, _ := s.ListBanners(context.Background(), foodTalksBannerQuery{Position: "home", OnlyLive: true})
	for i := 1; i < len(live); i++ {
		for j := i; j > 0 && live[j].Sort > live[j-1].Sort; j-- {
			live[j], live[j-1] = live[j-1], live[j]
		}
	}
	return live
}

type homeBannerTestEnv struct {
	pipeline *publishPipeline
	store    *publishJobStore
	site     *fakeBannerSite
	now      time.Time
}

func newHomeBannerTestEnv(t *testing.T) *homeBannerTestEnv {
	t.Helper()
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	t.Setenv(deploymentWritesPausedEnv, "")
	env := &homeBannerTestEnv{
		store: newPublishJobStore(filepath.Join(t.TempDir(), "publish-jobs.json")),
		site:  newFakeBannerSite(t),
		now:   time.Date(2026, 11, 28, 8, 50, 0, 0, publishExternalAutoZone),
	}
	env.pipeline = &publishPipeline{
		store:       env.store,
		homeBanners: func() (foodTalksBannerAPI, error) { return env.site, nil },
		spawn:       func(f func()) { f() },
		now:         func() time.Time { return env.now },
	}
	return env
}

// seedPublished 一篇已发到 FoodTalks 的稿，任务在 createdAt 进来。
func (env *homeBannerTestEnv) seedPublished(t *testing.T, sourceURL string, newsID int64, createdAt time.Time, mutate func(*publishJob)) publishJob {
	t.Helper()
	key, err := normalizeSiteSourceKey(sourceURL)
	if err != nil {
		t.Fatal(err)
	}
	job, created, err := env.store.createSeeded(key, sourceURL, func(j *publishJob) {
		j.Stage = publishStagePublished
		j.Title = "杨洋推着小车给打工人送可口可乐，这波操作为何刷屏？"
		j.SourceName = "FBIF食品饮料创新"
		j.CoverURL = "https://fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com/wechat-layout/04c5c3684ac6.jpg"
		j.FoodTalksNewsID = newsID
		j.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		if mutate != nil {
			mutate(j)
		}
	})
	if err != nil || !created {
		t.Fatalf("seed: created=%v err=%v", created, err)
	}
	return job
}

// markOnPublish 模拟落为已发布的那次写入（三个发布出口都走 markPublishedFollowUps）。
func (env *homeBannerTestEnv) markOnPublish(t *testing.T, id int64) publishJob {
	t.Helper()
	job, err := env.store.update(id, func(j *publishJob) { markPublishedFollowUps(j, env.now) })
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func (env *homeBannerTestEnv) job(t *testing.T, id int64) publishJob {
	t.Helper()
	job, err := env.store.byID(id)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestHomeBannerOnlyForTodaysFBIFHeadline(t *testing.T) {
	morning := time.Date(2026, 11, 28, 8, 48, 27, 0, publishExternalAutoZone)
	cases := []struct {
		name   string
		job    publishJob
		wanted bool
	}{
		{"当天头条", publishJob{SourceURL: homeBannerTestHeadlineURL}, true},
		{"转载的头条也算", publishJob{SourceURL: homeBannerTestHeadlineURL, SourceRepostedFrom: "惊蛰研究所"}, true},
		{"第二条不算", publishJob{SourceURL: strings.Replace(homeBannerTestHeadlineURL, "idx=1", "idx=2", 1)}, false},
		{"别的号的头条不算", publishJob{SourceURL: strings.Replace(homeBannerTestHeadlineURL, "MzA3ODEzMjg5Mg", "MzI0OTY5NzQ0MA", 1)}, false},
		{"只看自己的链接，不看被转载的原文", publishJob{
			SourceURL:         strings.Replace(homeBannerTestHeadlineURL, "idx=1", "idx=3", 1),
			OriginalSourceURL: homeBannerTestHeadlineURL,
		}, false},
		{"短链抓取后认出的长链也算", publishJob{SourceURL: "https://mp.weixin.qq.com/s/a5fffea7",
			SourceAliases: []string{"site:https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg%3D%3D&idx=1&mid=2652945469"}}, true},
	}
	for _, tc := range cases {
		job := tc.job
		job.CreatedAt = morning.UTC().Format(time.RFC3339)
		markHomeBannerPending(&job, morning.Add(time.Minute))
		if got := job.HomeBanner.State == homeBannerPending; got != tc.wanted {
			t.Errorf("%s：记待办=%v，应为 %v", tc.name, got, tc.wanted)
		}
		if tc.wanted && job.HomeBanner.Day != "2026-11-28" {
			t.Errorf("%s：头条那天=%q", tc.name, job.HomeBanner.Day)
		}
	}
	// 11-26 的头条（#406）拖到 11-28 才审核通过：当天没上就不换。
	late := publishJob{SourceURL: homeBannerTestHeadlineURL, CreatedAt: morning.AddDate(0, 0, -2).UTC().Format(time.RFC3339)}
	markHomeBannerPending(&late, morning)
	if late.HomeBanner.State != "" {
		t.Fatalf("前几天的头条今天才发上去不该换：%+v", late.HomeBanner)
	}
}

func TestHomeBannerCreatesNewestAndRetiresOldest(t *testing.T) {
	env := newHomeBannerTestEnv(t)
	job := env.seedPublished(t, homeBannerTestHeadlineURL, 63487, env.now.Add(-2*time.Minute), nil)
	env.markOnPublish(t, job.ID)
	env.pipeline.kickHomeBanners()

	got := env.job(t, job.ID).HomeBanner
	if got.State != homeBannerDone || got.BannerID != 5875 {
		t.Fatalf("应建好一张：%+v", got)
	}
	if len(env.site.creates) != 1 {
		t.Fatalf("应只建一张，实际 %d", len(env.site.creates))
	}
	want := foodTalksNewBanner{Img: job.CoverURL, ImgMobile: job.CoverURL, Position: "home", Title: job.Title, URL: "https://www.foodtalks.cn/news/63487"}
	if env.site.creates[0] != want {
		t.Fatalf("新建的字段不对：\n got %+v\nwant %+v", env.site.creates[0], want)
	}
	if len(got.RetiredIDs) != 1 || got.RetiredIDs[0] != 5869 {
		t.Fatalf("应下架最旧的 5869（11-17），实际 %v", got.RetiredIDs)
	}
	home := env.site.homepage()
	if len(home) != 6 || home[0].ID != 5875 {
		t.Fatalf("首页应仍是 6 张、新的排最前：%+v", home)
	}

	// 再跑、管理员再要一次都不建第二张。
	env.pipeline.kickHomeBanners()
	if _, err := env.pipeline.requestHomeBanner(job.ID); err != nil {
		t.Fatal(err)
	}
	if len(env.site.creates) != 1 || len(env.site.bans) != 1 {
		t.Fatalf("建好后不该再动：creates=%d bans=%v", len(env.site.creates), env.site.bans)
	}
}

func TestHomeBannerRetryAfterUncertainCreateDoesNotDuplicate(t *testing.T) {
	env := newHomeBannerTestEnv(t)
	job := env.seedPublished(t, homeBannerTestHeadlineURL, 63487, env.now.Add(-2*time.Minute), nil)
	env.markOnPublish(t, job.ID)
	env.site.createOK = false // 官网建上了，回包超时
	env.pipeline.kickHomeBanners()
	got := env.job(t, job.ID).HomeBanner
	if got.State != homeBannerCreating || got.Failures != 1 || got.NextAt == "" {
		t.Fatalf("回包超时应记失败、约下次：%+v", got)
	}

	env.site.createOK = true
	env.now = env.now.Add(2 * time.Minute)
	env.pipeline.kickHomeBanners()
	got = env.job(t, job.ID).HomeBanner
	if got.State != homeBannerDone || got.BannerID != 5875 || got.Note != "官网上已有这篇的轮播图，沿用" {
		t.Fatalf("重试应找到上次建上的那张沿用：%+v", got)
	}
	if len(env.site.creates) != 1 {
		t.Fatalf("重试不该建第二张，实际建了 %d 次", len(env.site.creates))
	}
	if home := env.site.homepage(); len(home) != 6 || home[0].ID != 5875 {
		t.Fatalf("首页应 6 张、新的排最前：%+v", home)
	}
}

func TestHomeBannerOnePerDay(t *testing.T) {
	env := newHomeBannerTestEnv(t)
	first := env.seedPublished(t, homeBannerTestHeadlineURL, 63487, env.now.Add(-2*time.Minute), nil)
	second := env.seedPublished(t, strings.Replace(homeBannerTestHeadlineURL, "mid=2652945469", "mid=2652945470", 1), 63490,
		env.now.Add(-time.Minute), func(j *publishJob) { j.Title = "同一天第二次群发的头条" })
	env.markOnPublish(t, first.ID)
	env.markOnPublish(t, second.ID)
	env.pipeline.kickHomeBanners()
	if got := env.job(t, first.ID).HomeBanner; got.State != homeBannerDone {
		t.Fatalf("第一篇应建好：%+v", got)
	}
	if got := env.job(t, second.ID).HomeBanner; got.State != homeBannerSkipped || !strings.Contains(got.Note, "每天只换一张") {
		t.Fatalf("同一天第二篇不该再建：%+v", got)
	}
	if len(env.site.creates) != 1 {
		t.Fatalf("一天只建一张，实际 %d", len(env.site.creates))
	}
}

func TestHomeBannerManualRequestForEarlierHeadline(t *testing.T) {
	env := newHomeBannerTestEnv(t)
	// 上线前就发上去的头条：发布那一刻还没有这项功能，管理员手动补这一篇。
	job := env.seedPublished(t, homeBannerTestHeadlineURL, 63487, env.now.Add(-2*time.Minute), nil)
	env.now = time.Date(2026, 11, 29, 0, 20, 0, 0, publishExternalAutoZone) // 拖过了零点
	got, err := env.pipeline.requestHomeBanner(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hb := env.job(t, got.ID).HomeBanner; hb.State != homeBannerDone || !hb.Manual || hb.Day != "2026-11-28" {
		t.Fatalf("手动补应按头条那天建好：%+v", hb)
	}

	second := env.seedPublished(t, strings.Replace(homeBannerTestHeadlineURL, "idx=1", "idx=2", 1), 63488, env.now, nil)
	if _, err := env.pipeline.requestHomeBanner(second.ID); !errors.Is(err, errHomeBannerNotHeadline) {
		t.Fatalf("不是头条的手动要也该拒：%v", err)
	}
}

func TestHomeBannerFailuresAlertAfterThreeAndGiveUpAtMidnight(t *testing.T) {
	env := newHomeBannerTestEnv(t)
	job := env.seedPublished(t, homeBannerTestHeadlineURL, 63487, env.now.Add(-2*time.Minute), nil)
	env.markOnPublish(t, job.ID)
	env.site.listErr = errors.New("FoodTalks 轮播图接口异常（HTTP 502）")
	for i := 0; i < 3; i++ {
		env.pipeline.kickHomeBanners()
		if status := currentPublishMonitoring(env.store, false, 0, env.now); i < 2 && status.HomeBannerFailed != 0 {
			t.Fatalf("第 %d 次失败还不该找人", i+1)
		}
		env.now = env.now.Add(time.Hour)
	}
	status := currentPublishMonitoring(env.store, false, 0, env.now)
	if status.HomeBannerFailed != 1 || !containsString(status.AlertIssues, "home_banner_failed:1") {
		t.Fatalf("连续 3 次没成应告警：%+v", status.AlertIssues)
	}
	if hint := status.AlertHints["home_banner_failed:1"]; hint.Meaning == "" || hint.Action == "" {
		t.Fatalf("告警要写清怎么办：%+v", hint)
	}
	env.now = time.Date(2026, 11, 29, 0, 1, 0, 0, publishExternalAutoZone)
	env.pipeline.kickHomeBanners()
	if hb := env.job(t, job.ID).HomeBanner; hb.State != homeBannerFailed || !strings.Contains(hb.Note, "HTTP 502") {
		t.Fatalf("过了当天应放弃并留原因：%+v", hb)
	}
	if len(env.site.creates) != 0 {
		t.Fatal("一直失败不该建")
	}
	if status := currentPublishMonitoring(env.store, false, 0, env.now.Add(25*time.Hour)); status.HomeBannerFailed != 0 {
		t.Fatalf("放弃 24 小时后不再告警：%+v", status.AlertIssues)
	}
}

func TestHomeBannerSkipsWhenArticleWentOffline(t *testing.T) {
	env := newHomeBannerTestEnv(t)
	job := env.seedPublished(t, homeBannerTestHeadlineURL, 63487, env.now.Add(-2*time.Minute), nil)
	env.markOnPublish(t, job.ID)
	if _, err := env.store.update(job.ID, func(j *publishJob) { j.Stage = publishStageOffline }); err != nil {
		t.Fatal(err)
	}
	env.pipeline.kickHomeBanners()
	if hb := env.job(t, job.ID).HomeBanner; hb.State != homeBannerSkipped || len(env.site.creates) != 0 {
		t.Fatalf("文章下线了不该建：%+v creates=%d", hb, len(env.site.creates))
	}
}

// 三个发布出口之一（后台已发布对账）真的会记待办：别的出口同走 markPublishedFollowUps。
func TestHomeBannerMarkedWhenManualCMSPublishAcked(t *testing.T) {
	env := newPublishTestEnv(t)
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	job := seedManualCMSJob(t, env, publishStageFailed, 63487, "杨洋推着小车给打工人送可口可乐，这波操作为何刷屏？")
	if _, err := env.store.update(job.ID, func(j *publishJob) { j.SourceURL = homeBannerTestHeadlineURL }); err != nil {
		t.Fatal(err)
	}
	env.ft.mu.Lock()
	env.ft.searchRecords = []foodTalksNewsListItem{{ID: 63487, Title: job.Title, PublishStatus: foodTalksPublishStatusPublished}}
	env.ft.mu.Unlock()
	got, err := env.pipeline.ackManualCMSPublication(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.HomeBanner.State != homeBannerPending {
		t.Fatalf("当天头条记为已发布时应记首页轮播图待办：%+v", got.HomeBanner)
	}
}

// 客户端：参数都在网址里（放请求体会回参数错误），正式环境不带浏览器头。
func TestFoodTalksBannerClientRequests(t *testing.T) {
	type call struct{ method, path, query, body string }
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, call{r.Method, r.URL.Path, r.URL.RawQuery, string(body)})
		if r.Header.Get("Origin") != "" || r.Header.Get("User-Agent") != "" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("正式环境只带凭证：Origin=%q UA=%q", r.Header.Get("Origin"), r.Header.Get("User-Agent"))
		}
		switch {
		case r.Method == http.MethodPost:
			fmt.Fprint(w, `{"code":0,"msg":"success","data":5875}`)
		case strings.HasSuffix(r.URL.Path, "/page"):
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "success", "data": map[string]any{"records": []map[string]any{
				{"id": 5875, "position": "home", "isBanned": 0, "isActive": 1, "url": "https://www.foodtalks.cn/news/63487"},
				{"id": 5868, "position": "home", "isBanned": 1, "isActive": 1},
				{"id": 99, "position": "news", "isBanned": 0, "isActive": 1},
			}}})
		default:
			fmt.Fprint(w, `{"code":0,"msg":"success","data":true}`)
		}
	}))
	defer srv.Close()
	t.Setenv("FOODTALKS_BASE_URL", srv.URL)
	t.Setenv("FOODTALKS_TOKEN", "tok")
	t.Setenv("FOODTALKS_ORIGIN", "")
	cli, err := newFoodTalksClientFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, err := cli.CreateBanner(ctx, foodTalksNewBanner{Img: "i", ImgMobile: "i", Position: "home", Title: "t", URL: "u"})
	if err != nil || id != 5875 {
		t.Fatalf("CreateBanner: %d %v", id, err)
	}
	live, err := cli.ListBanners(ctx, foodTalksBannerQuery{Position: "home", OnlyLive: true})
	if err != nil || len(live) != 1 || live[0].ID != 5875 {
		t.Fatalf("列表应只留首页在架的：%+v %v", live, err)
	}
	if err := cli.BanBanner(ctx, 5869); err != nil {
		t.Fatal(err)
	}
	if err := cli.SortBanner(ctx, 5875, 5880); err != nil {
		t.Fatal(err)
	}
	want := []call{
		{"POST", "/basic/admin/advertisement", "", `{"img":"i","imgMobile":"i","position":"home","title":"t","url":"u"}`},
		{"GET", "/basic/admin/advertisement/page", "current=1&isBanned=0&position=home&size=50", ""},
		{"PUT", "/basic/admin/advertisement/banned", "bannedStatus=1&id=5869", ""},
		{"PUT", "/basic/admin/advertisement/sort", "id=5875&sort=5880", ""},
	}
	if len(calls) != len(want) {
		t.Fatalf("calls=%+v", calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("第 %d 个请求：\n got %+v\nwant %+v", i+1, calls[i], want[i])
		}
	}
}
