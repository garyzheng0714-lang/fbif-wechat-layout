package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testFidelityToutiaoURL = "https://www.toutiao.com/article/7696340002422243903/"

// fidelityTestPayloadHTML 用外发渲染器真跑一遍：两个标题1（h2）、一段整段加粗（h3）、
// 一张带图注的图、一张不带图注的图、文末转载声明。
func fidelityTestPayloadHTML(t *testing.T) string {
	t.Helper()
	stubYunsuoImageSize(t, nil)
	job := publishJob{
		Title:      "估值超151亿元！蛋白棒赛道创业，他连赢了两次",
		SourceName: "FBIF食品饮料创新",
		Summary:    "竞业协议到期后，明星创业者重走“老路”。",
		CoverURL:   "https://cdn.example/wechat-cover.jpg",
		BlocksJSON: `[
			{"k":"txt","text":"作者：Sage，编辑：Bobo。"},
			{"k":"txt","text":"一个人，可以在同一赛道成功2次吗？"},
			{"k":"h","level":2,"text":"第一次创业，他把蛋白棒做简单"},
			{"k":"txt","text":"RXBAR的配料表只有几样东西。"},
			{"k":"img","src":"https://cdn.example/photo.png"},
			{"k":"cap","text":"蛋白棒RXBAR"},
			{"k":"cap","text":"图片来源：RXBAR"},
			{"k":"txt","runs":[{"type":"txt","text":"整段加粗的导语。","bold":true}]},
			{"k":"img","src":"https://cdn.example/plain.png"},
			{"k":"h","level":2,"text":"结语"},
			{"k":"txt","text":"下一篇，我继续写蛋白棒。"}
		]`,
		ImageMap: `{"https://cdn.example/photo.png":"https://cdn.example/photo.png","https://cdn.example/plain.png":"https://cdn.example/plain.png",
			"https://cdn.example/wechat-cover.jpg":"https://cdn.example/wechat-cover.jpg"}`,
	}
	payload, err := buildYunsuoPushPayload(job, []yunsuoAccountTarget{{ID: "a-1", Name: "头条号"}}, []string{"美食", "饮食文化", "财经", "消费", "公司"})
	if err != nil {
		t.Fatal(err)
	}
	return payload.HTML
}

// fidelityToutiaoPage 头条线上正文的写法（照 2026-10-20 m.toutiao.com 实取：加粗段是
// <p><strong>，图是 div.pgc-img，图注在里面的 p.pgc-img-caption，没有图注时是空的）。
func fidelityToutiaoPage(edit func(string) string) string {
	page := `<p data-track="1">一个人，可以在同一赛道成功2次吗？</p>` +
		`<p data-track="2"><strong>一、第一次创业，他把蛋白棒做简单</strong></p>` +
		`<p data-track="3">RXBAR的配料表只有几样东西。</p>` +
		`<div class="pgc-img"><img src="https://p3-sign.toutiaoimg.com/tos-cn-i-6w9my0ksvp/a~tplv-tt-large.image" img_width="640" img_height="360" inline="0" alt="估值超151亿元" onerror="javascript:errorimg.call(this);"><p class="pgc-img-caption">蛋白棒RXBAR；图片来源：RXBAR</p></div>` +
		`<p data-track="5"><strong>整段加粗的导语。</strong></p>` +
		`<div class="pgc-img"><img src="https://p3-sign.toutiaoimg.com/tos-cn-i-6w9my0ksvp/b~tplv-tt-large.image" img_width="640" img_height="360" inline="0" alt="估值超151亿元"><p class="pgc-img-caption"></p></div>` +
		`<p data-track="7"><strong>二、结语</strong></p>` +
		`<p data-track="8">下一篇，我继续写蛋白棒。</p>` +
		`<p data-track="9">本文为FBIF食品饮料创新原创，作者：Sage，编辑：Bobo，转载请联系授权。</p>`
	if edit != nil {
		page = edit(page)
	}
	return page
}

const fidelityToutiaoCover = `<div class="pgc-img"><img src="https://p3-sign.toutiaoimg.com/tos-cn-i-axegupay5k/cover~tplv-tt-large.image" img_width="640" img_height="334" inline="0" alt="估值超151亿元"><p class="pgc-img-caption"></p></div>`

// TestExternalFidelityComparesPayloadWithPlatformPage 推出去的正文与平台上的正文写法不同，
// 按看上去的结构比：一致不报；小标题被打成普通段、封面插到开头、图注拆成正文、声明丢了都报。
func TestExternalFidelityComparesPayloadWithPlatformPage(t *testing.T) {
	payloadHTML := fidelityTestPayloadHTML(t)
	toutiao := yunsuoPlatformProfiles["toutiao"]
	expected := externalFidelityExpected(payloadHTML, toutiao)
	if want := (externalFidelityShape{Headings: 3, Images: 2, CaptionedImages: 1, Statement: true}); expected != want {
		t.Fatalf("渲染器的写法应数出 3 处小标题、2 张图、1 处图注、有声明：%+v\n%s", expected, payloadHTML)
	}
	for _, tc := range []struct {
		name string
		edit func(string) string
		note string
	}{
		{"结构一致", nil, ""},
		{"h2 丢成普通段", func(page string) string {
			return strings.Replace(page, `<p data-track="2"><strong>一、第一次创业，他把蛋白棒做简单</strong></p>`, `<p data-track="2">一、第一次创业，他把蛋白棒做简单</p>`, 1)
		}, "平台上小标题少了 1 处"},
		{"封面被插到开头", func(page string) string { return fidelityToutiaoCover + page }, "平台上开头多了一张封面图"},
		{"图注拆成正文段", func(page string) string {
			return strings.Replace(page, `<p class="pgc-img-caption">蛋白棒RXBAR；图片来源：RXBAR</p></div>`, `<p class="pgc-img-caption"></p></div><p data-track="4">蛋白棒RXBAR；图片来源：RXBAR</p>`, 1)
		}, "平台上图注少了 1 处"},
		{"文末声明丢了、图少一张", func(page string) string {
			page = page[:strings.Index(page, `<p data-track="9">`)]
			return strings.Replace(page, `<div class="pgc-img"><img src="https://p3-sign.toutiaoimg.com/tos-cn-i-6w9my0ksvp/b~tplv-tt-large.image" img_width="640" img_height="360" inline="0" alt="估值超151亿元"><p class="pgc-img-caption"></p></div>`, "", 1)
		}, "平台上图片少了 1 张、文末转载声明没了"},
	} {
		got := externalFidelityCompare(expected, externalFidelityShapeOf(fidelityToutiaoPage(tc.edit)))
		if got != tc.note {
			t.Errorf("%s：应为 %q，得到 %q", tc.name, tc.note, got)
		}
	}
	// 云梭会把封面插到正文最前面的平台（百家号经云梭时就是），开头多一张图是应有的样子。
	prepended := yunsuoPlatformProfiles["toutiao"]
	prepended.CoverPrepended = true
	if note := externalFidelityCompare(externalFidelityExpected(payloadHTML, prepended), externalFidelityShapeOf(fidelityToutiaoCover+fidelityToutiaoPage(nil))); note != "" {
		t.Fatalf("会插封面的平台，开头的封面不算对不上：%q", note)
	}
}

func stubFetchToutiaoContent(t *testing.T, fn func(groupID string) (string, error)) *[]string {
	t.Helper()
	var calls []string
	previous := fetchToutiaoArticleContent
	fetchToutiaoArticleContent = func(_ context.Context, groupID string) (string, error) {
		calls = append(calls, groupID)
		return fn(groupID)
	}
	t.Cleanup(func() { fetchToutiaoArticleContent = previous })
	return &calls
}

func fidelityTestLane(t *testing.T, accountID, name, articleURL, payloadHTML, requestedAt string) publishExternalLane {
	t.Helper()
	encoded, err := json.Marshal(yunsuoPushPayload{Title: "估值超151亿元", AccountIDs: []string{accountID}, HTML: payloadHTML})
	if err != nil {
		t.Fatal(err)
	}
	lane := testLane(accountID, name, publishExternalDistributionSucceeded)
	lane.ContentPK, lane.URL, lane.PayloadJSON = "pk-"+accountID, articleURL, string(encoded)
	lane.RequestedAt, lane.CompletedAt = requestedAt, requestedAt
	return lane
}

// TestExternalFidelityCheckedOnceAndShownOnCardAndMonitoring 拿到头条链接后取一次线上正文，
// 对不上就在卡片这个平台的行里写灰字、monitoring 给 warning（不是 P0）；核过不再取。
// 百家号不取；10-20 改写法之前推的旧稿不核。
func TestExternalFidelityCheckedOnceAndShownOnCardAndMonitoring(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	clock := newCSTClock("2026-10-21 10:00")
	env.pipeline.now = func() time.Time { return clock.now }
	payloadHTML := fidelityTestPayloadHTML(t)
	calls := stubFetchToutiaoContent(t, func(string) (string, error) {
		return fidelityToutiaoPage(func(page string) string {
			return strings.Replace(page, `<p data-track="2"><strong>一、第一次创业，他把蛋白棒做简单</strong></p>`, `<p data-track="2">一、第一次创业，他把蛋白棒做简单</p>`, 1)
		}), nil
	})
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	pushed := "2026-10-21T01:00:00Z"
	setTestLanes(t, env, job.ID,
		fidelityTestLane(t, testYunsuoToutiaoID, "头条号", testFidelityToutiaoURL, payloadHTML, pushed),
		fidelityTestLane(t, testYunsuoBaijiaID, "百家号", "https://baijiahao.baidu.com/s?id=1846000000000000000", payloadHTML, pushed))
	old := createPublishedDistributionJobWithKey(t, env, "site:fidelity-old", "FBIF食品饮料创新")
	setTestLanes(t, env, old.ID, fidelityTestLane(t, testYunsuoToutiaoID, "头条号", "https://www.toutiao.com/article/7696345385349972514/", payloadHTML, "2026-10-14T02:30:00Z"))
	seq := mustJob(t, env, job.ID).ExternalDistributionCardSeq

	env.pipeline.refreshExternalDistributionFidelity()
	if fmt.Sprint(*calls) != "[7696340002422243903]" {
		t.Fatalf("只取拿到链接的新头条稿，按文章 id 取：%v", *calls)
	}
	got := mustJob(t, env, job.ID)
	check := mustLane(t, got, testYunsuoToutiaoID).Fidelity
	if check.CheckedAt == "" || check.Note != "平台上小标题少了 1 处" || check.Expected.Headings != 3 || check.Online.Headings != 2 {
		t.Fatalf("应记下核对结果：%+v", check)
	}
	if baijia := mustLane(t, got, testYunsuoBaijiaID).Fidelity; baijia != (publishExternalFidelity{}) {
		t.Fatalf("百家号有验证码，不取：%+v", baijia)
	}
	if stale := mustLane(t, mustJob(t, env, old.ID), testYunsuoToutiaoID).Fidelity; stale != (publishExternalFidelity{}) {
		t.Fatalf("改写法之前推的旧稿不核：%+v", stale)
	}
	if got.ExternalDistributionCardSeq <= seq {
		t.Fatalf("对不上应刷卡")
	}
	body := distributionSectionJSON(t, buildReviewDoneCard(got))
	if !strings.Contains(body, "<font color='grey'>平台上小标题少了 1 处</font>") || !strings.Contains(body, "("+testFidelityToutiaoURL+")") {
		t.Fatalf("卡片里头条号这一行应有文章链接和灰字原因: %s", body)
	}

	status := currentPublishMonitoring(env.store, false, clock.now)
	if !publishMonitoringHasIssue(publishMonitoringStatus{Issues: status.Warnings}, "external_fidelity_mismatch:1") ||
		publishMonitoringHasIssue(publishMonitoringStatus{Issues: status.AlertIssues}, "external_fidelity") ||
		publishMonitoringHasIssue(status, "external_fidelity") || status.ExternalFidelityMismatches != 1 {
		t.Fatalf("对不上只给 warning：%+v", status)
	}

	clock.set("2026-10-22 10:00")
	env.pipeline.refreshExternalDistributionFidelity()
	if len(*calls) != 1 {
		t.Fatalf("核过的不再取：%v", *calls)
	}
	clock.set("2026-10-29 10:00")
	if status := currentPublishMonitoring(env.store, false, clock.now); status.ExternalFidelityMismatches != 0 {
		t.Fatalf("7 天后不再报：%+v", status)
	}
}

// TestExternalFidelityConsistentDoesNotTouchCard 一致的只记结果，不刷卡、不报。
func TestExternalFidelityConsistentDoesNotTouchCard(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	clock := newCSTClock("2026-10-21 10:00")
	env.pipeline.now = func() time.Time { return clock.now }
	payloadHTML := fidelityTestPayloadHTML(t)
	stubFetchToutiaoContent(t, func(string) (string, error) { return fidelityToutiaoPage(nil), nil })
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	setTestLanes(t, env, job.ID, fidelityTestLane(t, testYunsuoToutiaoID, "头条号", testFidelityToutiaoURL, payloadHTML, "2026-10-21T01:00:00Z"))
	seq := mustJob(t, env, job.ID).ExternalDistributionCardSeq

	env.pipeline.refreshExternalDistributionFidelity()
	got := mustJob(t, env, job.ID)
	if check := mustLane(t, got, testYunsuoToutiaoID).Fidelity; check.CheckedAt == "" || check.Note != "" {
		t.Fatalf("一致也要记下核过：%+v", check)
	}
	if got.ExternalDistributionCardSeq != seq {
		t.Fatalf("一致不刷卡")
	}
	if body := distributionSectionJSON(t, buildReviewDoneCard(got)); strings.Contains(body, "平台上") {
		t.Fatalf("一致不写灰字: %s", body)
	}
	if status := currentPublishMonitoring(env.store, false, clock.now); status.ExternalFidelityMismatches != 0 || status.ExternalFidelityUnavailable != 0 {
		t.Fatalf("一致不报：%+v", status)
	}
}

// TestExternalFidelityRetriesThenWarns 取不到（平台还在审、网络错）隔 30 分钟、2 小时再试，
// 三次都取不到就停，monitoring 给 warning：核对本身坏了也要有人知道。
func TestExternalFidelityRetriesThenWarns(t *testing.T) {
	setTestYunsuoConfig(t)
	env := newPublishTestEnv(t)
	clock := newCSTClock("2026-10-21 10:00")
	env.pipeline.now = func() time.Time { return clock.now }
	calls := stubFetchToutiaoContent(t, func(string) (string, error) { return "", errors.New("头条正文接口没有返回正文") })
	job := createPublishedDistributionJob(t, env, "FBIF食品饮料创新")
	setTestLanes(t, env, job.ID, fidelityTestLane(t, testYunsuoToutiaoID, "头条号", testFidelityToutiaoURL, fidelityTestPayloadHTML(t), "2026-10-21T01:00:00Z"))

	for _, step := range []struct {
		at    string
		calls int
	}{
		{"2026-10-21 10:00", 1},
		{"2026-10-21 10:20", 1}, // 不到 30 分钟
		{"2026-10-21 10:31", 2},
		{"2026-10-21 12:00", 2}, // 不到 2 小时
		{"2026-10-21 12:31", 3},
		{"2026-10-22 12:31", 3}, // 取满三次就停
	} {
		clock.set(step.at)
		env.pipeline.refreshExternalDistributionFidelity()
		if len(*calls) != step.calls {
			t.Fatalf("%s 应共取 %d 次，实际 %d", step.at, step.calls, len(*calls))
		}
	}
	check := mustLane(t, mustJob(t, env, job.ID), testYunsuoToutiaoID).Fidelity
	if check.CheckedAt != "" || check.Attempts != externalFidelityMaxAttempts || check.Error == "" {
		t.Fatalf("取不到应记次数与原因：%+v", check)
	}
	status := currentPublishMonitoring(env.store, false, clock.now)
	if !publishMonitoringHasIssue(publishMonitoringStatus{Issues: status.Warnings}, "external_fidelity_unavailable:1") ||
		publishMonitoringHasIssue(publishMonitoringStatus{Issues: status.AlertIssues}, "external_fidelity") {
		t.Fatalf("取不到只给 warning：%+v", status)
	}
}

// TestFetchToutiaoArticleContentReadsInfoAPI 免费的 m.toutiao.com 文章接口：手机 UA，正文在 data.content。
func TestFetchToutiaoArticleContentReadsInfoAPI(t *testing.T) {
	var path, agent string
	content := `<p data-track="1">正文</p>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, agent = r.URL.Path, r.Header.Get("User-Agent")
		switch r.URL.Path {
		case "/i7696340002422243903/info/v2/":
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"gid": "7696340002422243903", "content": content}})
		case "/i1/info/v2/":
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"content": ""}})
		default:
			http.Error(w, "forbidden", http.StatusForbidden)
		}
	}))
	t.Cleanup(server.Close)
	previous := toutiaoArticleInfoBase
	toutiaoArticleInfoBase = server.URL
	t.Cleanup(func() { toutiaoArticleInfoBase = previous })

	got, err := fetchExternalArticleContent(context.Background(), yunsuoPlatformProfiles["toutiao"], testFidelityToutiaoURL)
	if err != nil || got != content || path != "/i7696340002422243903/info/v2/" || !strings.Contains(agent, "iPhone") {
		t.Fatalf("应按文章 id 用手机 UA 取 data.content：got=%q err=%v path=%s ua=%s", got, err, path, agent)
	}
	if _, err := defaultFetchToutiaoArticleContent(context.Background(), "1"); err == nil {
		t.Fatalf("没有正文应报错（平台还在审）")
	}
	if _, err := defaultFetchToutiaoArticleContent(context.Background(), "2"); err == nil {
		t.Fatalf("HTTP 错误应报错")
	}
	if _, err := fetchExternalArticleContent(context.Background(), yunsuoPlatformProfiles["baijiahao"], "https://baijiahao.baidu.com/s?id=1"); err == nil {
		t.Fatalf("百家号不取")
	}
}
