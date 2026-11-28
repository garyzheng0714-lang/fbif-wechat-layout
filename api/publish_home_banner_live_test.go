package main

// 测试环境真跑一遍首页轮播图（平时跳过）：用真客户端、真流程在 api-we-test 新建两张、重跑不建第二张、
// 新的排最前，最后把这次建的都下架。跑法见 docs/foodtalks-home-banner.md「测试环境走一遍」。

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHomeBannerLiveOnFoodTalksTest(t *testing.T) {
	if os.Getenv("HOME_BANNER_LIVE_TEST") != "1" {
		t.Skip("只在测试环境手动跑：HOME_BANNER_LIVE_TEST=1")
	}
	if !strings.Contains(os.Getenv("FOODTALKS_BASE_URL"), "api-we-test.") {
		t.Fatal("只许打测试环境 api-we-test")
	}
	var newsIDs []int64
	for _, raw := range strings.Split(os.Getenv("HOME_BANNER_LIVE_NEWS_IDS"), ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && id > 0 {
			newsIDs = append(newsIDs, id)
		}
	}
	if len(newsIDs) != 2 {
		t.Fatal("要 HOME_BANNER_LIVE_NEWS_IDS：测试环境里两篇真文章的编号，逗号隔开")
	}
	ft, err := newFoodTalksClientFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := newPublishJobStore(filepath.Join(t.TempDir(), "publish-jobs.json"))
	now := time.Now()
	p := &publishPipeline{
		store:       store,
		homeBanners: func() (foodTalksBannerAPI, error) { return ft, nil },
		spawn:       func(f func()) { f() },
		now:         func() time.Time { return now },
	}
	before, err := ft.ListBanners(ctx, foodTalksBannerQuery{Position: homeBannerPosition, OnlyLive: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("测试环境首页原有 %d 张：%v", len(before), homeBannerIDs(before))

	var made []int64
	defer func() {
		for _, id := range made {
			if err := ft.BanBanner(ctx, id); err != nil {
				t.Errorf("下架测试轮播图 %d 失败：%v", id, err)
			} else {
				t.Logf("已下架测试轮播图 %d", id)
			}
		}
	}()
	stamp := now.Format("0102-150405")
	for i, mid := range []string{"9000000001", "9000000002"} {
		sourceURL := "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg%3D%3D&idx=1&mid=" + mid
		key, _ := normalizeSiteSourceKey(sourceURL)
		job, _, err := store.createSeeded(key, sourceURL, func(j *publishJob) {
			j.Stage = publishStagePublished
			j.Title = "[SYNC-TEST] 首页轮播图自动换 " + stamp + " 第" + strconv.Itoa(i+1) + "张"
			j.CoverURL = "https://fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com/wechat-layout/04c5c3684ac6.jpg"
			j.FoodTalksNewsID = newsIDs[i]
			// 两张算两天的头条，免得被「每天一张」拦下。
			j.CreatedAt = now.AddDate(0, 0, i-1).UTC().Format(time.RFC3339)
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.requestHomeBanner(job.ID); err != nil {
			t.Fatal(err)
		}
		got, _ := store.byID(job.ID)
		if got.HomeBanner.BannerID > 0 {
			made = append(made, got.HomeBanner.BannerID)
		}
		if got.HomeBanner.State != homeBannerDone {
			t.Fatalf("第 %d 张没建好：%+v", i+1, got.HomeBanner)
		}
		t.Logf("第 %d 张建好：轮播图 %d，下架 %v", i+1, got.HomeBanner.BannerID, got.HomeBanner.RetiredIDs)

		// 记录丢了再跑一遍：先按链接找到刚建的，不建第二张。
		if _, err := store.update(job.ID, func(j *publishJob) {
			j.HomeBanner = publishHomeBanner{State: homeBannerCreating, Day: j.HomeBanner.Day, Until: j.HomeBanner.Until, Manual: true}
		}); err != nil {
			t.Fatal(err)
		}
		p.runHomeBanners()
		again, _ := store.byID(job.ID)
		if again.HomeBanner.State != homeBannerDone || again.HomeBanner.BannerID != got.HomeBanner.BannerID {
			t.Fatalf("重跑应沿用 %d：%+v", got.HomeBanner.BannerID, again.HomeBanner)
		}
	}
	live, err := ft.ListBanners(ctx, foodTalksBannerQuery{Position: homeBannerPosition, OnlyLive: true})
	if err != nil {
		t.Fatal(err)
	}
	var top foodTalksBanner
	for _, b := range live {
		if b.Sort > top.Sort {
			top = b
		}
	}
	if len(made) != 2 || top.ID != made[1] {
		t.Fatalf("新建的应排最前：made=%v top=%+v", made, top)
	}
	if want := siteFrontURL() + "/news/" + strconv.FormatInt(newsIDs[1], 10); top.URL != want || top.Img == "" || top.ImgMobile != top.Img {
		t.Fatalf("字段不对：%+v，链接应为 %s", top, want)
	}
	t.Logf("首页现在 %d 张，最前是 %d「%s」→ %s", len(live), top.ID, top.Title, top.URL)
}

func homeBannerIDs(list []foodTalksBanner) []int64 {
	out := make([]int64, 0, len(list))
	for _, b := range list {
		out = append(out, b.ID)
	}
	return out
}
