package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// #340 头条号没发出去的两条图注之一（头条数法 58 字），说明以句号收尾、来源直接接在后面。
const testLongCaptionDesc = "奥地利New Originals Company，将豆腐和天贝制成精致Tapas，拓展了这两种食材在餐饮渠道应用场景的可能性。"

func testLongCaption() string { return testLongCaptionDesc + "图片来源：作者拍摄" }

// testCaptionHTML 网站上一张带说明和图片来源的图（FoodTalks 正文里图注写在 figcaption）。
func testCaptionHTML(desc string) string {
	return `<p>这是一篇用于验证外部分发流程的原创文章正文。</p><figure><img src="https://cdn.example.com/a.jpg" alt=""><figcaption><p>` +
		desc + `</p><p>图片来源：作者拍摄</p></figcaption></figure>`
}

// TestDistributionTextLengthMatchesService 和分发服务 limits.mjs 的 textLength 同样的例子、同样的数（两边测试各一份）。
func TestDistributionTextLengthMatchesService(t *testing.T) {
	for _, tc := range []struct {
		count, text string
		want        int
	}{
		{countBytesHalf, "图片来源：作者拍摄", 9},
		{countBytesHalf, "abc", 2},
		{countBytesHalf, " a b \n", 1},
		{countBytesHalf, "@toshiya #", 6},
		{countBytesHalf, "é", 1},
		{countBytesHalf, "😀", 2},
		{countBytesHalf, "a\u3000b\ufeff\u200b", 1},
		{titleCountASCIIHalf, "Whole Foods Market发布2027年8大食品趋势！苦味升级、豆类热潮…", 30},
		{titleCountChars, "abc字", 4},
	} {
		if got := distributionTextLength(tc.count, tc.text); got != tc.want {
			t.Errorf("%s 数「%s」应是 %d，得 %d", tc.count, tc.text, tc.want, got)
		}
	}
	if got := testPlatform(t, "toutiao").captionLength(testLongCaption()); got != 58 {
		t.Fatalf("#340 那条图注头条按 58 字数：%d", got)
	}
}

func TestDistributionPlatformsRejectUnknownCaptionCount(t *testing.T) {
	items, err := readTestDistributionPlatforms()
	if err != nil {
		t.Fatal(err)
	}
	for i := range items {
		if items[i].ID == "toutiao" {
			items[i].Caption.Count = "words"
		}
	}
	if err := validateDistributionPlatforms(items); err == nil || !strings.Contains(err.Error(), "图注上限 50、数法「words」排版工具不认得") {
		t.Fatalf("不认得的图注数法整份不要：%v", err)
	}
}

// TestShortenExternalCaption 来源原样留着，只缩说明；AI 缩超了告诉它再缩一次；还不成、或 AI 出错就在标点处截。
func TestShortenExternalCaption(t *testing.T) {
	toutiao := testPlatform(t, "toutiao")
	p := &publishPipeline{}
	var asked []string
	answer := func(replies ...string) {
		asked = nil
		p.externalCaptionAI = func(_ context.Context, desc string, max int, note string) (string, error) {
			asked = append(asked, note)
			if desc != testLongCaptionDesc || max != 40 {
				t.Fatalf("只把说明交给 AI、上限扣掉来源和「；」：%q %d", desc, max)
			}
			reply := replies[len(asked)-1]
			if reply == "" {
				return "", errors.New("MiMo 接口不可达")
			}
			return reply, nil
		}
	}

	answer("“奥地利New Originals Company把豆腐和天贝做成精致Tapas，拓展了餐饮渠道的吃法。”")
	short, by := p.shortenExternalCaption(context.Background(), 340, toutiao, testLongCaption())
	if short != "奥地利New Originals Company把豆腐和天贝做成精致Tapas，拓展了餐饮渠道的吃法。图片来源：作者拍摄" || by != "AI 缩写" ||
		!strings.Contains(asked[0], "英文字母和数字两个算一个字") {
		t.Fatalf("AI 缩好的去掉引号、接回来源：%q %s %v", short, by, asked)
	}

	answer(strings.Repeat("豆", 45), "奥地利公司把豆腐做成小吃Tapas")
	if short, by = p.shortenExternalCaption(context.Background(), 340, toutiao, testLongCaption()); short != "奥地利公司把豆腐做成小吃Tapas；图片来源：作者拍摄" ||
		by != "AI 缩写" || !strings.Contains(asked[1], "上一次缩成了 45 个字，超了") {
		t.Fatalf("缩超了告诉它再缩，没句号收尾的说明用「；」接来源：%q %s %v", short, by, asked)
	}

	answer(strings.Repeat("豆", 45), strings.Repeat("豆", 42))
	short, by = p.shortenExternalCaption(context.Background(), 340, toutiao, testLongCaption())
	if short != "奥地利New Originals Company，将豆腐和天贝制成精致Tapas；图片来源：作者拍摄" || by != "在标点处截" ||
		!toutiao.captionFits(short) {
		t.Fatalf("两次都超就在标点处截（%d 字）：%q %s", toutiao.captionLength(short), short, by)
	}

	answer("", "")
	if short, by = p.shortenExternalCaption(context.Background(), 340, toutiao, testLongCaption()); by != "在标点处截" || !toutiao.captionFits(short) ||
		!strings.HasSuffix(short, "图片来源：作者拍摄") {
		t.Fatalf("AI 出错也照样截好：%q %s", short, by)
	}

	if got := cutCaption(strings.Repeat("豆", 60), "图片来源：作者拍摄", toutiao.captionFits); got != strings.Repeat("豆", 40)+"…图片来源：作者拍摄" {
		t.Fatalf("没有标点能截就硬截加「…」：%q", got)
	}
}

// TestExternalCaptionsShortenedBeforePush 头条号图注上限 50 字（#340）：推之前把超了的缩好再推，缩好的记在头条号的
// 改稿记录里，再推同一份内容不再问 AI、还是同一段字；百家号不限，原样推。
func TestExternalCaptionsShortenedBeforePush(t *testing.T) {
	setTestDistributionAccounts(t)
	stubDistributionImageSize(t, map[string][2]int{})
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	calls := 0
	env.pipeline.externalCaptionAI = func(context.Context, string, int, string) (string, error) {
		calls++
		return "奥地利New Originals Company把豆腐和天贝做成精致Tapas，拓展了餐饮渠道的吃法。", nil
	}
	job := createPublishedDistributionJobWithKey(t, env, "site:long-caption", "FBIF食品饮料创新")
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID: testDistributionNewsID, Title: job.Title, CoverImg: job.CoverURL, Content: testCaptionHTML(testLongCaptionDesc),
		Summary: job.Summary, SourceName: "FBIF食品饮料创新", PublishStatus: foodTalksPublishStatusPublished,
	})
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	want := "奥地利New Originals Company把豆腐和天贝做成精致Tapas，拓展了餐饮渠道的吃法。图片来源：作者拍摄"
	captions := map[string]string{}
	for i, req := range robot.saves {
		for _, block := range req.Blocks {
			if block.K == "cap" {
				captions[robot.platforms[i]] = block.Text
			}
		}
	}
	if captions["toutiao"] != want || captions["baijiahao"] != testLongCaption() || calls != 1 {
		t.Fatalf("头条号推缩好的、百家号推原样，AI 问一次：%v calls=%d", captions, calls)
	}
	got := mustJob(t, env, job.ID)
	draft, _ := externalDraftFor(got, testToutiaoAccountID)
	if draft.Captions[testLongCaption()] != want || len(draft.Issues) != 0 || externalJobNeedsFix(got) {
		t.Fatalf("缩好的记在头条号的改稿记录里，不算待修改：%+v", draft)
	}
	snapshot, err := env.pipeline.publishedNewsSnapshotForDistribution(context.Background(), got)
	if err != nil {
		t.Fatal(err)
	}
	if again := env.pipeline.externalCaptionsFor(snapshot, testPlatform(t, "toutiao"), draft); again[testLongCaption()] != want || calls != 1 {
		t.Fatalf("同一份内容再推照用记下的、不再问 AI：%v calls=%d", again, calls)
	}
}

// TestCardRetryRebuildsDraftWhoseCaptionsPlatformBlocks #340：草稿存好了、发布时平台因图注超长不出「确认发布」。
// 按原来的键再交还是那篇发不出去的草稿，卡片「重新同步」要按现在的内容重建：图注缩好、换键另存一篇再发。
func TestCardRetryRebuildsDraftWhoseCaptionsPlatformBlocks(t *testing.T) {
	setTestExternalAccounts(t, testArticleAccount(testToutiaoAccountID, "toutiao", false))
	t.Setenv("FEISHU_REVIEWER_OPEN_IDS", "ou_reviewer_1")
	stubDistributionImageSize(t, map[string][2]int{})
	items, err := readTestDistributionPlatforms()
	if err != nil {
		t.Fatal(err)
	}
	unlimited := append([]distributionPlatformInfo(nil), items...)
	for i := range unlimited {
		if unlimited[i].ID == "toutiao" {
			unlimited[i].Caption.Max = 0 // 那时还不知道头条号图注有上限
		}
	}
	setTestDistributionPlatforms(t, unlimited...)
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	failedPublish := testRobotSavedRecord(testRobotArticleID)
	failedPublish.Action = &robotDraftAction{ID: "act-1", Kind: robotActionPublish, Status: robotActionFailed, Error: "头条号页面上没找到「确认发布」（已点了「预览并发布」）"}
	robot.publishRecord = &failedPublish
	env.pipeline.externalCaptionAI = func(context.Context, string, int, string) (string, error) {
		return "奥地利New Originals Company把豆腐和天贝做成精致Tapas，拓展了餐饮渠道的吃法。", nil
	}
	job := createPublishedDistributionJobWithKey(t, env, "site:340", "FBIF食品饮料创新")
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID: testDistributionNewsID, Title: job.Title, CoverImg: job.CoverURL, Content: testCaptionHTML(testLongCaptionDesc),
		Summary: job.Summary, SourceName: "FBIF食品饮料创新", PublishStatus: foodTalksPublishStatusPublished,
	})
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	failed := mustJob(t, env, job.ID)
	old := mustLane(t, failed, testToutiaoAccountID)
	if old.State != publishExternalDistributionFailed || !externalLaneDraftSaved(old) {
		t.Fatalf("草稿存好了、发布没做成：%+v", old)
	}

	setTestDistributionPlatforms(t, items...) // 分发服务给了图注上限 50 字
	robot.publishRecord = nil
	out := env.pipeline.reviewCardAction(context.Background(), "ou_reviewer_1", "", mustDistributeValue(t, failed, testToutiaoAccountID))
	if out.toastType != "success" {
		t.Fatalf("重新同步应受理：%+v", out)
	}
	if len(robot.saves) != 2 || robot.saves[1].IdempotencyKey == old.IntentKey {
		t.Fatalf("换键另存一篇，不按原来的键再交：%+v", robot.saves)
	}
	for _, block := range robot.saves[1].Blocks {
		if block.K == "cap" && block.Text != "奥地利New Originals Company把豆腐和天贝做成精致Tapas，拓展了餐饮渠道的吃法。图片来源：作者拍摄" {
			t.Fatalf("另存的那篇图注缩好了：%q", block.Text)
		}
	}
	retried := mustLane(t, mustJob(t, env, job.ID), testToutiaoAccountID)
	if retried.State != publishExternalDistributionSucceeded || retried.IntentKey != robot.saves[1].IdempotencyKey {
		t.Fatalf("另存的那篇发出去：%+v", retried)
	}
}
