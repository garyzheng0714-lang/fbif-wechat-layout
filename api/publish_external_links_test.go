package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestToutiaoGroupIDsCarryPublishTime(t *testing.T) {
	page := `<a href="https://www.toutiao.com/a7695597761408631323/">x</a>
		<a href="//m.toutiao.com/article/7695571299619357227/">y</a>
		<a href="https://toutiao.com/group/7695597761408631323/">dup</a>
		<a href="https://www.toutiao.com/a1643372495250446">old</a>`
	ids := toutiaoGroupIDs(page)
	if fmt.Sprint(ids) != "[7695597761408631323 7695571299619357227 1643372495250446]" {
		t.Fatalf("应按出现顺序去重：%v", ids)
	}
	// 2026-10-12 生产实测：#196 10:09 推送，头条文章 id 高 32 位正是这一刻。
	at, ok := toutiaoGroupIDTime("7695597761408631323")
	if !ok || at.Format(time.RFC3339) != "2026-10-12T02:09:40Z" {
		t.Fatalf("文章 id 高 32 位应为发文时间：%v %v", at, ok)
	}
}

func TestExternalTitlesMatch(t *testing.T) {
	for _, tc := range []struct {
		want, got string
		ok        bool
	}{
		{"三得利卖出100亿瓶绿茶，背后藏着一家236年的老茶铺", "三得利卖出100亿瓶绿茶,背后藏着一家236年的老茶铺", true},
		{"英国销售额第一的蛋白棒，亿滋CEO夸它“比美国所有蛋白棒都好吃”", "英国销售额第一的蛋白棒，亿滋CEO夸它比美国所有蛋白棒都好吃", true},
		// 平台截断的长标题互相包含也算。
		{"打破低GI“不可能三角”，它拿下两个第一，还要让“低GI”进入大众日常", "打破低GI不可能三角，它拿下两个第一", true},
		// 太短的包含不算：同名短语太容易撞。
		{"创新周报", "元气森林上新0糖啤酒 | 创新周报", false},
		{"三得利卖出100亿瓶绿茶", "伊藤园卖出100亿瓶绿茶", false},
		{"", "任意", false},
	} {
		if got := externalTitlesMatch(tc.want, tc.got); got != tc.ok {
			t.Fatalf("%q vs %q 应为 %v", tc.want, tc.got, tc.ok)
		}
	}
}

// stubToutiaoSearchAndTikHub 本地桩：头条搜索页返回给定的 id，TikHub 按 id 返回作者与标题
// （结构照 2026-10-12 生产实测：data.data.user_info.name / share_info.title 带「 - 今日头条」）。
func stubToutiaoSearchAndTikHub(t *testing.T, ids []string, articles map[string][2]string) *int {
	t.Helper()
	tikhubCalls := 0
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("keyword") == "" {
			http.Error(w, "missing keyword", http.StatusBadRequest)
			return
		}
		for _, id := range ids {
			fmt.Fprintf(w, `<a href="https://www.toutiao.com/article/%s/">r</a>`, id)
		}
	}))
	t.Cleanup(search.Close)
	tikhub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tikhubCalls++
		if r.Header.Get("Authorization") != "Bearer test-tikhub-key" || r.URL.Path != tikhubToutiaoArticlePath {
			http.Error(w, "bad request", http.StatusUnauthorized)
			return
		}
		article, ok := articles[r.URL.Query().Get("group_id")]
		if !ok {
			json.NewEncoder(w).Encode(map[string]any{"code": 400, "message_zh": "文章不存在"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"data": map[string]any{
			"source":     article[0],
			"user_info":  map[string]any{"name": article[0]},
			"share_info": map[string]any{"title": article[1] + " - 今日头条"},
		}}})
	}))
	t.Cleanup(tikhub.Close)
	previous := toutiaoSearchEndpoint
	toutiaoSearchEndpoint = search.URL
	t.Cleanup(func() { toutiaoSearchEndpoint = previous })
	t.Setenv("TIKHUB_API_BASE_URL", tikhub.URL)
	t.Setenv("TIKHUB_API_KEY", "test-tikhub-key")
	return &tikhubCalls
}

// TestLookupToutiaoArticleVerifiesAuthorAndTitle 同题转载很多：只认作者是自家号、标题对得上、
// 发文时间在窗口内的那篇；窗口外的不花 TikHub 调用。
func TestLookupToutiaoArticleVerifiesAuthorAndTitle(t *testing.T) {
	title := "三得利卖出100亿瓶绿茶，背后藏着一家236年的老茶铺"
	ours := "7695597761408631323"   // 2026-10-12 02:09:40Z
	repost := "7695571299619357227" // 2026-10-12 00:26Z，别人同题转载
	old := "1643372495250446"       // 2015 年，窗口外
	calls := stubToutiaoSearchAndTikHub(t, []string{old, repost, ours}, map[string][2]string{
		repost: {"某搬运号", title},
		ours:   {"FBIF食品饮料创新", title},
	})
	from := mustParseRFC3339(t, "2026-10-11T23:00:00Z")
	to := mustParseRFC3339(t, "2026-10-13T00:00:00Z")
	ref, err := defaultLookupToutiaoArticle(context.Background(), title, from, to)
	if err != nil || ref.URL != "https://www.toutiao.com/article/"+ours+"/" || ref.Title != title || ref.Author != "FBIF食品饮料创新" {
		t.Fatalf("应找到自家号那篇：%+v err=%v", ref, err)
	}
	if *calls != 2 {
		t.Fatalf("窗口外的候选不应调用 TikHub：calls=%d", *calls)
	}
	if _, err := defaultLookupToutiaoArticle(context.Background(), "完全不同的标题再写长一点点", from, to); err == nil {
		t.Fatalf("标题对不上不得认")
	}
	late := mustParseRFC3339(t, "2026-10-12T03:00:00Z")
	if _, err := defaultLookupToutiaoArticle(context.Background(), title, late, to); err == nil {
		t.Fatalf("推送之前发的文章不得认")
	}
}

func stubLookupToutiao(t *testing.T, fn func(title string, from, to time.Time) (externalArticleRef, error)) *int {
	t.Helper()
	calls := 0
	previous := lookupToutiaoArticle
	lookupToutiaoArticle = func(_ context.Context, title string, from, to time.Time) (externalArticleRef, error) {
		calls++
		return fn(title, from, to)
	}
	t.Cleanup(func() { lookupToutiaoArticle = previous })
	return &calls
}

// TestExternalDistributionLinksBackfillWithBackoff 推送成功后按退避间隔补头条链接，找到就
// 写进 lane 并刷卡；百家号不自动找。
func TestExternalDistributionLinksBackfillWithBackoff(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	clock := newCSTClock("2026-10-12 10:00")
	env.pipeline.now = func() time.Time { return clock.now }
	found := false
	var window [2]time.Time
	calls := stubLookupToutiao(t, func(title string, from, to time.Time) (externalArticleRef, error) {
		window = [2]time.Time{from, to}
		if !found {
			return externalArticleRef{}, errExternalArticleNotFound
		}
		return externalArticleRef{URL: "https://www.toutiao.com/article/7695597761408631323/", Title: title + "（头条）"}, nil
	})
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	done := func(id, name string) publishExternalLane {
		lane := testLane(id, name, publishExternalDistributionSucceeded)
		lane.ContentPK, lane.Title = "pk", job.Title
		lane.RequestedAt = clock.now.Add(-5 * time.Minute).UTC().Format(time.RFC3339)
		lane.CompletedAt = clock.now.UTC().Format(time.RFC3339)
		return lane
	}
	setTestLanes(t, env, job.ID, done(testYunsuoToutiaoID, "头条号"), done(testYunsuoBaijiaID, "百家号"))

	clock.set("2026-10-12 10:10")
	env.pipeline.refreshExternalDistributionLinks()
	if *calls != 0 {
		t.Fatalf("刚推完不查（平台收录要时间）")
	}
	clock.set("2026-10-12 10:25")
	env.pipeline.refreshExternalDistributionLinks()
	lane := mustLane(t, mustJob(t, env, job.ID), testYunsuoToutiaoID)
	if *calls != 1 || lane.URLAttempts != 1 || lane.URL != "" {
		t.Fatalf("第一次没找到应只记次数：calls=%d lane=%+v", *calls, lane)
	}
	if want := mustParseRFC3339(t, "2026-10-12T01:45:00Z"); !window[0].Equal(want) {
		t.Fatalf("应从推送前 10 分钟开始找：%v", window[0])
	}
	clock.set("2026-10-12 10:50")
	env.pipeline.refreshExternalDistributionLinks()
	if *calls != 1 {
		t.Fatalf("第二次至少隔 1 小时")
	}
	found = true
	clock.set("2026-10-12 11:30")
	env.pipeline.refreshExternalDistributionLinks()
	got := mustJob(t, env, job.ID)
	lane = mustLane(t, got, testYunsuoToutiaoID)
	if *calls != 2 || lane.URL != "https://www.toutiao.com/article/7695597761408631323/" || !strings.HasSuffix(lane.Title, "（头条）") {
		t.Fatalf("找到后应写入链接和平台标题：calls=%d lane=%+v", *calls, lane)
	}
	if baijia := mustLane(t, got, testYunsuoBaijiaID); baijia.URLAttempts != 0 {
		t.Fatalf("百家号不自动找：%+v", baijia)
	}
	body := distributionSectionJSON(t, buildReviewDoneCard(got))
	if !strings.Contains(body, "(https://www.toutiao.com/article/7695597761408631323/)") {
		t.Fatalf("卡片里头条号标题应链到文章: %s", body)
	}
	clock.set("2026-10-13 11:30")
	env.pipeline.refreshExternalDistributionLinks()
	if *calls != 2 {
		t.Fatalf("有链接后不再查")
	}

	// 查满 6 次或超过 7 天就停。
	other := createPublishedDistributionJobWithKey(t, env, "site:link-old", "FBIF食品饮料创新")
	stale := done(testYunsuoToutiaoID, "头条号")
	stale.CompletedAt = clock.now.Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339)
	exhausted := createPublishedDistributionJobWithKey(t, env, "site:link-exhausted", "FBIF食品饮料创新")
	tired := done(testYunsuoToutiaoID, "头条号")
	tired.URLAttempts = externalLinkMaxAttempts
	setTestLanes(t, env, other.ID, stale)
	setTestLanes(t, env, exhausted.ID, tired)
	env.pipeline.refreshExternalDistributionLinks()
	if *calls != 2 {
		t.Fatalf("超时限或超次数的不再查：calls=%d", *calls)
	}
}

// TestExternalDistributionManualRegistration 管理员把人工已发的头条号登记上：之后补发只推
// 百家号，头条号不会重复；链接只认平台域名；正在同步的平台不能改。
func TestExternalDistributionManualRegistration(t *testing.T) {
	setTestYunsuoBothAutoConfig(t)
	env := newPublishTestEnv(t)
	mux := newPublishTestMux(t, env)
	clock := newCSTClock("2026-10-12 10:00")
	env.pipeline.now = func() time.Time { return clock.now }
	fake := newBothAccountsYunsuo()
	env.pipeline.yunsuo = func() (yunsuoAPI, error) { return fake, nil }
	robot := useFakeRobot(env)
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	path := fmt.Sprintf("/api/publish/jobs/%d/external-distribution/manual", job.ID)

	if rec := doPublishReq(mux, http.MethodPost, path, `{"account_id":"`+testYunsuoToutiaoID+`","url":"https://www.toutiao.com/article/7535000000000000000/"}`, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("无管理员令牌应 401，实际 %d", rec.Code)
	}
	for _, bad := range []string{"http://www.toutiao.com/article/1/", "https://evil.example.com/article/1/", "https://baijiahao.baidu.com/s?id=1"} {
		rec := doPublishReq(mux, http.MethodPost, path, `{"account_id":"`+testYunsuoToutiaoID+`","url":"`+bad+`"}`, true)
		if rec.Code != http.StatusConflict {
			t.Fatalf("头条号不得登记 %s：%d %s", bad, rec.Code, rec.Body.String())
		}
	}
	if rec := doPublishReq(mux, http.MethodPost, path, `{"account_id":"`+testYunsuoBaijiaID+`","lookup":true,"dry_run":true}`, true); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "百家号暂不支持自动查找") {
		t.Fatalf("百家号不支持自动找：%d %s", rec.Code, rec.Body.String())
	}

	// 自动找：dry_run 只报告；按网站发布时间前后找。
	calls := stubLookupToutiao(t, func(title string, from, to time.Time) (externalArticleRef, error) {
		published := mustParseRFC3339(t, job.PublishedAt)
		if !from.Equal(published.Add(-externalLinkManualBefore)) || !to.Equal(published.Add(externalLinkManualAfter)) {
			t.Fatalf("人工登记应按网站发布时间前后找：%v ~ %v", from, to)
		}
		return externalArticleRef{URL: "https://www.toutiao.com/article/7535000000000000000/", Title: title, Author: "FBIF食品饮料创新"}, nil
	})
	rec := doPublishReq(mux, http.MethodPost, path, `{"account_id":"`+testYunsuoToutiaoID+`","lookup":true,"dry_run":true}`, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"action":"register_manual"`) || !strings.Contains(rec.Body.String(), "7535000000000000000") {
		t.Fatalf("dry run 应报告找到的文章：%d %s", rec.Code, rec.Body.String())
	}
	if len(mustJob(t, env, job.ID).ExternalDistributionLanes) != 0 || *calls != 1 {
		t.Fatalf("dry run 不得写入")
	}
	rec = doPublishReq(mux, http.MethodPost, path, `{"account_id":"`+testYunsuoToutiaoID+`","lookup":true}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("登记失败：%d %s", rec.Code, rec.Body.String())
	}
	lane := mustLane(t, mustJob(t, env, job.ID), testYunsuoToutiaoID)
	if lane.State != publishExternalLaneManual || lane.URL != "https://www.toutiao.com/article/7535000000000000000/" ||
		lane.RequestedBy != publishExternalManualActor || lane.ContentPK != "" {
		t.Fatalf("应登记为人工已发：%+v", lane)
	}

	// 补发只推百家号；人工登记的不占头条名额，也不会被推。
	if marked, err := env.pipeline.markExternalAutoBackfill(time.Date(2026, 8, 1, 0, 0, 0, 0, publishExternalAutoZone), false); err != nil || len(marked) != 1 {
		t.Fatalf("应只为百家号补发：n=%d err=%v", len(marked), err)
	}
	if len(fake.pushed) != 0 || fmt.Sprint(robot.platforms) != "[baijiahao]" {
		t.Fatalf("头条号人工已发，不得再推：%v robot=%v", fake.pushed, robot.platforms)
	}
	got := mustJob(t, env, job.ID)
	body := distributionSectionJSON(t, buildReviewDoneCard(got))
	if !strings.Contains(body, "<font color='green'>人工已发</font>") || !strings.Contains(body, "<font color='green'>草稿已存（未发布）</font>") {
		t.Fatalf("卡片应显示头条号人工已发、百家号草稿已存: %s", body)
	}

	// 百家号的草稿由人在后台发出去后，登记成人工已发并补上文章链接；草稿编号留着。
	rec = doPublishReq(mux, http.MethodPost, path, `{"account_id":"`+testYunsuoBaijiaID+`","url":"https://baijiahao.baidu.com/s?id=1846000000000000000"}`, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"action":"register_manual"`) {
		t.Fatalf("存了草稿的百家号应登记为人工已发：%d %s", rec.Code, rec.Body.String())
	}
	if baijia := mustLane(t, mustJob(t, env, job.ID), testYunsuoBaijiaID); baijia.State != publishExternalLaneManual ||
		baijia.URL != "https://baijiahao.baidu.com/s?id=1846000000000000000" || baijia.ContentPK != testRobotArticleID {
		t.Fatalf("登记后应是人工已发、带链接：%+v", baijia)
	}

	// 已发布的平台只补链接，不改状态。
	rec = doPublishReq(mux, http.MethodPost, path, `{"account_id":"`+testYunsuoBaijiaID+`","url":"https://baijiahao.baidu.com/s?id=1846000000000000009"}`, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"action":"set_link"`) {
		t.Fatalf("应只补百家号链接：%d %s", rec.Code, rec.Body.String())
	}
	if baijia := mustLane(t, mustJob(t, env, job.ID), testYunsuoBaijiaID); baijia.State != publishExternalLaneManual ||
		baijia.URL != "https://baijiahao.baidu.com/s?id=1846000000000000009" || baijia.RequestedBy != publishExternalManualActor {
		t.Fatalf("补链接不得改状态和留痕：%+v", baijia)
	}
	if rec := doPublishReq(mux, http.MethodPost, path, `{"account_id":"`+testYunsuoBaijiaID+`"}`, true); rec.Code != http.StatusConflict {
		t.Fatalf("已发布的平台不给链接应拒绝：%d", rec.Code)
	}

	// 正在同步的平台不能登记。
	busy := createPublishedDistributionJobWithKey(t, env, "site:manual-busy", "FBIF食品饮料创新")
	setTestLanes(t, env, busy.ID, testLane(testYunsuoToutiaoID, "头条号", publishExternalDistributionPolling))
	busyPath := fmt.Sprintf("/api/publish/jobs/%d/external-distribution/manual", busy.ID)
	if rec := doPublishReq(mux, http.MethodPost, busyPath, `{"account_id":"`+testYunsuoToutiaoID+`","url":"https://www.toutiao.com/article/7535000000000000001/"}`, true); rec.Code != http.StatusConflict ||
		mustLane(t, mustJob(t, env, busy.ID), testYunsuoToutiaoID).State != publishExternalDistributionPolling {
		t.Fatalf("同步中的平台不得登记：%d", rec.Code)
	}
	// 自动找不到：404，不写。
	stubLookupToutiao(t, func(string, time.Time, time.Time) (externalArticleRef, error) {
		return externalArticleRef{}, errExternalArticleNotFound
	})
	missing := createPublishedDistributionJobWithKey(t, env, "site:manual-missing", "FBIF食品饮料创新")
	missingPath := fmt.Sprintf("/api/publish/jobs/%d/external-distribution/manual", missing.ID)
	if rec := doPublishReq(mux, http.MethodPost, missingPath, `{"account_id":"`+testYunsuoToutiaoID+`","lookup":true}`, true); rec.Code != http.StatusNotFound ||
		len(mustJob(t, env, missing.ID).ExternalDistributionLanes) != 0 {
		t.Fatalf("找不到应 404 且不写：%d", rec.Code)
	}
}
