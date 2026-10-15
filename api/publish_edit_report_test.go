package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 每周手改清单（红线 45）：编辑在 FoodTalks 后台改了我们发出去的稿，按类数篇数，3 篇以上才私信列出。

const (
	erNoteTmpl = `<p style="text-align: justify;"><em><span style="font-size: 14px; color: #888888;">%s</span></em></p>`
	erParaTmpl = `<p style="text-align: justify;">%s</p>`
	erImgTmpl  = `<figure class="image"><img style="display: block;" src="https://oss.example.com/wechat-layout/%s" width="600" class="mc-img"><figcaption><p style="text-align: center;"><span style="font-size: 12px; color: #888888;">%s</span></p></figcaption></figure>`
)

// editReportArticle 一篇典型的 FoodTalks 转载稿：开头署名、20 段正文夹两个小标题和一张图、文末转载声明与页脚。
func editReportArticle() string {
	parts := []string{fmt.Sprintf(erNoteTmpl, "作者：余梦莹"), `<p style="text-align: justify;"><br></p>`}
	for i := 1; i <= 20; i++ {
		switch i {
		case 4:
			parts = append(parts, "<h2>由“大而全”到“小而精”</h2>", "<h2>一次纯超市业态的试验</h2>")
		case 8:
			parts = append(parts, "<h2>“酸黄瓜之于Z世代，就像牛油果之于千禧一代。”</h2>")
		case 12:
			parts = append(parts, "<h2>1、先改善第一次吃的体验</h2>")
			parts = append(parts, fmt.Sprintf(erImgTmpl, "91222e0fda98.png", "图片来源：如皋城建论坛公众号"))
		}
		parts = append(parts, fmt.Sprintf(erParaTmpl, fmt.Sprintf("第%d段正文，奥乐齐如皋首店开业，首日销售额冲到146万，这是第%d段的独有内容。", i, i)))
	}
	parts = append(parts,
		fmt.Sprintf(erParaTmpl, "英文来源：Food Processing"),
		`<p style="text-align: justify;"><strong>提示：</strong></p>`,
		fmt.Sprintf(erParaTmpl, "*本文为新零售原创《首日卖146万！奥乐齐在苏中县城卖爆了》，不代表FoodTalks立场，转载请联系出处。"),
		fmt.Sprintf(erParaTmpl, "加入微信群"),
		fmt.Sprintf(erParaTmpl, "关注公众号"),
	)
	return strings.Join(parts, "\n")
}

func mustReplace(t *testing.T, s, old, new string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("样稿里没有 %q", old)
	}
	return strings.Replace(s, old, new, 1)
}

func editReportCategories(changes []editReportChange) map[string]string {
	out := map[string]string{}
	for _, c := range changes {
		if _, ok := out[c.Category]; !ok {
			out[c.Category] = c.Detail
		}
	}
	return out
}

// TestEditReportIgnoresAssemblyAndReuploads 组装层加的公司链接、目录锚点、编辑重新上传的同一张图都不算手改。
func TestEditReportIgnoresAssemblyAndReuploads(t *testing.T) {
	ours := editReportArticle()
	live := mustReplace(t, ours, "奥乐齐如皋首店开业", `<a class="mc-company-link" href="https://www.foodtalks.cn/company/aldi_china" data-id="1531">奥乐齐</a>如皋首店开业`)
	live = mustReplace(t, live, "<h2>一次纯超市业态的试验</h2>", `<h2 id="mctoc_ab12cd">一次纯超市业态的试验</h2>`)
	live = mustReplace(t, live, "91222e0fda98.png", "91222e0fda986a20c9891f0c70207438.png")
	live = strings.ReplaceAll(live, `<p style="text-align: justify;"><br></p>`, `<p style="text-align: justify;">&nbsp;</p>`)
	ours = mustReplace(t, ours, "首日销售额冲到146万，这是第3段", "<strong>首日销售额冲到146万</strong>，这是第3段")
	live = mustReplace(t, live, "首日销售额冲到146万，这是第3段", "<strong>首日销售额</strong><b>冲到146万</b>，这是第3段")
	if changes := compareEditReport(ours, live); len(changes) != 0 {
		t.Fatalf("只是组装与重传，不该算手改：%+v", changes)
	}
}

// TestEditReportClassifiesEditorChanges 编辑常见的手改各归一类，例子里带这篇自己的原文。
func TestEditReportClassifiesEditorChanges(t *testing.T) {
	ours := editReportArticle()
	cases := []struct {
		name     string
		edit     func(string) string
		category string
		detail   string
	}{
		{"开头署名扩写", func(s string) string {
			return mustReplace(t, s, "作者：余梦莹", "本文为投资界原创，作者：余梦莹，转载请联系出处。")
		}, editReportByline, "「作者：余梦莹」改成「本文为投资界原创，作者：余梦莹，转载请联系出处。」"},
		{"转载声明换原文标题", func(s string) string {
			return mustReplace(t, s, "《首日卖146万！奥乐齐在苏中县城卖爆了》", "《146万，奥乐齐在苏中县城卖爆了》")
		}, editReportRepostNotice, "《146万，奥乐齐在苏中县城卖爆了》"},
		{"引语小标题改回正文", func(s string) string {
			return mustReplace(t, s, "<h2>“酸黄瓜之于Z世代，就像牛油果之于千禧一代。”</h2>", fmt.Sprintf(erParaTmpl, "“酸黄瓜之于Z世代，就像牛油果之于千禧一代。”"))
		}, editReportHeadingDemote, "从小标题改成正文"},
		{"小标题降一级", func(s string) string {
			return mustReplace(t, s, "<h2>1、先改善第一次吃的体验</h2>", "<h3>1、先改善第一次吃的体验</h3>")
		}, editReportHeadingLevel, "「1、先改善第一次吃的体验」从标题1改成标题2"},
		{"正文升成小标题", func(s string) string {
			return mustReplace(t, s, fmt.Sprintf(erParaTmpl, "第15段正文，奥乐齐如皋首店开业，首日销售额冲到146万，这是第15段的独有内容。"),
				"<h3>第15段正文，奥乐齐如皋首店开业，首日销售额冲到146万，这是第15段的独有内容。</h3>")
		}, editReportHeadingAdd, "从正文改成小标题"},
		{"两行小标题合成一行", func(s string) string {
			return mustReplace(t, s, "<h2>由“大而全”到“小而精”</h2>\n<h2>一次纯超市业态的试验</h2>", "<h2>由“大而全”到“小而精”，一次纯超市业态的试验</h2>")
		}, editReportHeadingMerge, "合成「由“大而全”到“小而精”，一次纯超市业态的试验」"},
		{"小标题整句换掉", func(s string) string {
			return mustReplace(t, s, "<h2>1、先改善第一次吃的体验</h2>", "<h2>2、彩虹糖酸条火尖枪</h2>")
		}, editReportHeadingText, "改成「2、彩虹糖酸条火尖枪」"},
		{"文末删人员行", func(s string) string {
			return mustReplace(t, s, fmt.Sprintf(erParaTmpl, "英文来源：Food Processing")+"\n", "")
		}, editReportTailDeleted, "删了「英文来源：Food Processing」"},
		{"文末人员行改灰色小字", func(s string) string {
			return mustReplace(t, s, fmt.Sprintf(erParaTmpl, "英文来源：Food Processing"), fmt.Sprintf(erNoteTmpl, "英文来源：Food Processing"))
		}, editReportNoteSwap, "从正文改成灰色小字"},
		{"中间删一段", func(s string) string {
			return mustReplace(t, s, fmt.Sprintf(erParaTmpl, "第10段正文，奥乐齐如皋首店开业，首日销售额冲到146万，这是第10段的独有内容。")+"\n", "")
		}, editReportBodyDeleted, "删了「第10段正文"},
		{"加一段", func(s string) string {
			return mustReplace(t, s, `<p style="text-align: justify;"><strong>提示：</strong></p>`,
				fmt.Sprintf(erParaTmpl, "*更多排行榜信息请点击“全球食品饮料排行榜大全”")+`<p style="text-align: justify;"><strong>提示：</strong></p>`)
		}, editReportAdded, "加了「*更多排行榜信息请点击“全球食品饮料排行榜大全”」"},
		{"正文改错字", func(s string) string {
			return mustReplace(t, s, "第9段正文，奥乐齐如皋首店开业", "第9段正文，奥乐齐如皋首家店开业")
		}, editReportTextEdit, "首家店"},
		{"图注改成正文", func(s string) string {
			return mustReplace(t, s, `<figcaption><p style="text-align: center;"><span style="font-size: 12px; color: #888888;">图片来源：如皋城建论坛公众号</span></p></figcaption></figure>`,
				`</figure>`+fmt.Sprintf(erParaTmpl, "图片来源：如皋城建论坛公众号"))
		}, editReportCaptionSwap, "从图注改成正文"},
		{"局部加粗", func(s string) string {
			return mustReplace(t, s, "首日销售额冲到146万，这是第9段", "<strong>首日销售额冲到146万</strong>，这是第9段")
		}, editReportBold, "「首日销售额冲到146万」加了粗"},
		{"小标题改回正文顺手补了句号", func(s string) string {
			return mustReplace(t, s, "<h2>1、先改善第一次吃的体验</h2>", fmt.Sprintf(erParaTmpl, "1、先改善第一次吃的体验。"))
		}, editReportHeadingDemote, "从小标题改成正文"},
		{"换了一张图", func(s string) string {
			return mustReplace(t, s, "91222e0fda98.png", "66eeac57c4e25d46159a1a05d30b15124a75.png")
		}, editReportImageReplaced, "换了 1 张图"},
		{"删了图", func(s string) string {
			return mustReplace(t, s, `<img style="display: block;" src="https://oss.example.com/wechat-layout/91222e0fda98.png" width="600" class="mc-img">`, "")
		}, editReportImageRemoved, "删了 1 张图"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := editReportCategories(compareEditReport(ours, tc.edit(ours)))
			detail, ok := got[tc.category]
			if !ok || !strings.Contains(detail, tc.detail) {
				t.Fatalf("应归为 %s（含 %q），实际 %+v", tc.category, tc.detail, got)
			}
			if len(got) != 1 {
				t.Fatalf("一处手改只该归一类，实际 %+v", got)
			}
		})
	}
	t.Run("整篇重排", func(t *testing.T) {
		got := compareEditReport(ours, "<p>完全不同的一篇。</p><p>第二段。</p>")
		if len(got) != 1 || got[0].Category != editReportRewritten {
			t.Fatalf("对得上的不到一半应算整篇重排：%+v", got)
		}
	})
	t.Run("大半段落各改一个字不算整篇重排", func(t *testing.T) {
		live := ours
		for i := 1; i <= 16; i++ {
			live = mustReplace(t, live, fmt.Sprintf("第%d段正文，奥乐齐如皋首店开业", i), fmt.Sprintf("第%d段正文，奥乐齐如皋首家店开业", i))
		}
		got := editReportCategories(compareEditReport(ours, live))
		if _, ok := got[editReportTextEdit]; !ok || len(got) != 1 {
			t.Fatalf("改字配上对后不该算整篇重排：%+v", got)
		}
	})
	t.Run("超长稿只改首尾", func(t *testing.T) {
		var a, b []string
		for i := 0; i < 2100; i++ {
			line := fmt.Sprintf(erParaTmpl, fmt.Sprintf("超长稿第%d段，内容各不相同。", i))
			a, b = append(a, line), append(b, line)
		}
		b[0] = fmt.Sprintf(erParaTmpl, "超长稿第0段，内容各不相同！")
		b[2099] = fmt.Sprintf(erParaTmpl, "超长稿第2099段，内容各不相同！")
		got := compareEditReport(strings.Join(a, "\n"), strings.Join(b, "\n"))
		if len(got) != 2 || got[0].Category != editReportTextEdit || got[1].Category != editReportTextEdit {
			t.Fatalf("超长稿不建整张表也要对上中间段：%+v", got)
		}
	})
}

// TestEditReportListsOnlyRepeatedCategories 一类至少 3 篇才列；一篇同类多处只算一篇；例子带链接与原文。
func TestEditReportListsOnlyRepeatedCategories(t *testing.T) {
	report := publishEditReport{WeekStart: "2026-10-05", WeekEnd: "2026-10-11", Compared: 6}
	for id := int64(1); id <= 4; id++ {
		article := publishEditReportArticle{JobID: id, Title: fmt.Sprintf("第%d篇", id), URL: fmt.Sprintf("https://www.foodtalks.cn/news/%d", 60000+id)}
		article.Changes = []editReportChange{
			{Category: editReportByline, Detail: fmt.Sprintf("「作者：甲%d」改成「本文为X原创，作者：甲%d，转载请联系出处。」", id, id)},
			{Category: editReportByline, Detail: "删了「翻译：乙」"},
		}
		if id <= 2 {
			article.Changes = append(article.Changes, editReportChange{Category: editReportHeadingLevel, Detail: "「1、x」从标题1改成标题2"})
		}
		report.Articles = append(report.Articles, article)
	}
	report.Articles = append(report.Articles, publishEditReportArticle{JobID: 5, Title: "没改"})
	summarizePublishEditReport(&report)
	if report.Edited != 4 || len(report.Listed) != 1 || report.Listed[0].Key != editReportByline || report.Listed[0].Articles != 4 {
		t.Fatalf("只列开头署名（4 篇）：%+v", report)
	}
	if len(report.Listed[0].Examples) != publishEditReportMaxExamples || report.Listed[0].Examples[0].Detail != "「作者：甲1」改成「本文为X原创，作者：甲1，转载请联系出处。」" {
		t.Fatalf("每类取前 3 个例子、每篇取第一处：%+v", report.Listed[0].Examples)
	}
	body, _ := json.Marshal(buildPublishEditReportPost(report))
	text := string(body)
	for _, want := range []string{"FoodTalks 手改周报（10月5日–10月11日）", "6 篇里，编辑在后台手改了 4 篇", "1. 开头署名（4 篇）",
		`"href":"https://www.foodtalks.cn/news/60001"`, "#1《第1篇》", "还有 #4", "只改了 1–2 篇的零星情况不列"} {
		if !strings.Contains(text, want) {
			t.Fatalf("私信缺 %q：%s", want, text)
		}
	}
	if strings.Contains(text, "小标题级别") {
		t.Fatalf("只改了 2 篇的不列：%s", text)
	}

	quiet := publishEditReport{WeekStart: "2026-10-05", WeekEnd: "2026-10-11", Compared: 3,
		Articles: []publishEditReportArticle{{JobID: 1, Changes: []editReportChange{{Category: editReportTextEdit, Detail: "x"}}}}}
	summarizePublishEditReport(&quiet)
	if body, _ := json.Marshal(buildPublishEditReportPost(quiet)); !strings.Contains(string(body), "没有哪一类改了 3 篇以上，不用处理。") {
		t.Fatalf("没有要列的也要说一句：%s", body)
	}
	empty := publishEditReport{WeekStart: "2026-10-05", WeekEnd: "2026-10-11"}
	summarizePublishEditReport(&empty)
	if body, _ := json.Marshal(buildPublishEditReportPost(empty)); !strings.Contains(string(body), "上周没有流水线发到 FoodTalks 的文章。") {
		t.Fatalf("没有稿也说一句：%s", body)
	}
}

type editReportTestEnv struct {
	*publishTestEnv
	sent  []string // 收到私信的 open_id
	posts []map[string]any
	fail  map[string]bool
	now   time.Time
}

// newEditReportTestEnv 上一周（10/5–10/11）发了 4 篇，其中 3 篇编辑改过开头署名；另有本周稿、测试站稿、已下线稿不算。
func newEditReportTestEnv(t *testing.T, receivers ...publishDistributionOwner) *editReportTestEnv {
	t.Helper()
	env := &editReportTestEnv{publishTestEnv: newPublishTestEnv(t), fail: map[string]bool{}}
	env.pipeline.editReports = newPublishEditReportStore(filepath.Join(t.TempDir(), "publish-edit-report.json"))
	t.Setenv(publishNotificationAppsEnv, `[{"id":"review-app","name":"审核应用","app_id":"cli_review","app_secret":"secret-review"}]`)
	env.pipeline.deliverySettings = newPublishDeliverySettingsStore(filepath.Join(t.TempDir(), "delivery.json"))
	setEditReportReceivers(t, env, receivers...)
	env.now = time.Date(2026, 10, 12, 10, 1, 0, 0, publishSiteTimeZone)
	env.pipeline.now = func() time.Time { return env.now }
	env.pipeline.editReportSender = func(ctx context.Context, openID string, post map[string]any) (string, error) {
		if env.fail[openID] {
			return "", errors.New("飞书发消息失败 code=230013")
		}
		env.sent = append(env.sent, openID)
		env.posts = append(env.posts, post)
		return "om_" + openID, nil
	}
	ours := editReportArticle()
	edited := mustReplace(t, ours, "作者：余梦莹", "本文为投资界原创，作者：余梦莹，转载请联系出处。")
	seed := func(key string, publishedAt time.Time, target string, newsID int64, live string, status int) {
		job, _, err := env.store.create("site:edit-report:"+key, "https://mp.weixin.qq.com/s/"+key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.store.update(job.ID, func(j *publishJob) {
			j.Stage = publishStagePublished
			j.Title = "稿件" + key
			j.ContentHTML = ours
			j.FoodTalksNewsID = newsID
			j.StagingTarget = target
			j.PublishedAt = publishedAt.UTC().Format(time.RFC3339)
		}); err != nil {
			t.Fatal(err)
		}
		seedLivePublishedNews(t, env.publishTestEnv, foodTalksNewsDetail{ID: newsID, Title: "稿件" + key, Content: live, PublishStatus: status})
	}
	target := currentFoodTalksTarget()
	week := time.Date(2026, 10, 5, 9, 0, 0, 0, publishSiteTimeZone)
	seed("a", week, target, 70001, edited, foodTalksPublishStatusPublished)
	seed("b", week.AddDate(0, 0, 2), target, 70002, edited, foodTalksPublishStatusPublished)
	seed("c", week.AddDate(0, 0, 6).Add(14*time.Hour), target, 70003, edited, foodTalksPublishStatusPublished) // 周日 23:00
	seed("d", week.AddDate(0, 0, 3), target, 70004, ours, foodTalksPublishStatusPublished)
	seed("this-week", week.AddDate(0, 0, 7), target, 70005, edited, foodTalksPublishStatusPublished)
	seed("test-site", week.AddDate(0, 0, 1), "test-site-other", 70006, edited, foodTalksPublishStatusPublished)
	seed("offline", week.AddDate(0, 0, 1), target, 70007, edited, foodTalksPublishStatusDraft)
	return env
}

func setEditReportReceivers(t *testing.T, env *editReportTestEnv, receivers ...publishDistributionOwner) {
	t.Helper()
	settings := publishDeliverySettings{AppProfileID: "review-app", ChatIDs: []string{"oc_review"}}
	if len(receivers) > 0 {
		settings.EditReportOwners, settings.EditReportOwnersApp = receivers, "review-app"
	}
	if err := env.pipeline.deliverySettings.write(settings); err != nil {
		t.Fatal(err)
	}
}

func editReportRecord(t *testing.T, env *editReportTestEnv) publishEditReportRecord {
	t.Helper()
	rec, _, err := env.pipeline.editReports.get("2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestEditReportScheduledOncePerWeek 周一 10 点前不发；之后发一次上周的，再跑不重发；只算上周发到当前环境、仍在线的稿。
func TestEditReportScheduledOncePerWeek(t *testing.T) {
	env := newEditReportTestEnv(t, publishDistributionOwner{OpenID: "ou_gary", Name: "郑梽煌"})
	env.now = time.Date(2026, 10, 12, 9, 59, 0, 0, publishSiteTimeZone)
	env.pipeline.runScheduledPublishEditReport(context.Background())
	if len(env.sent) != 0 {
		t.Fatalf("周一 10 点前不发：%v", env.sent)
	}
	env.now = time.Date(2026, 10, 12, 10, 1, 0, 0, publishSiteTimeZone)
	env.pipeline.runScheduledPublishEditReport(context.Background())
	if len(env.sent) != 1 || env.sent[0] != "ou_gary" {
		t.Fatalf("应私信接收人一次：%v", env.sent)
	}
	body, _ := json.Marshal(env.posts[0])
	for _, want := range []string{"10月5日–10月11日", "4 篇里，编辑在后台手改了 3 篇", "1. 开头署名（3 篇）", "「作者：余梦莹」改成「本文为投资界原创"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("私信缺 %q：%s", want, body)
		}
	}
	rec := editReportRecord(t, env)
	if rec.State != publishEditReportStateSent || rec.Compared != 4 || rec.Edited != 3 || rec.Listed != 1 || rec.MessageIDs["ou_gary"] != "om_ou_gary" {
		t.Fatalf("记录应为已发送：%+v", rec)
	}
	env.now = env.now.Add(3 * time.Hour)
	env.pipeline.runScheduledPublishEditReport(context.Background())
	if len(env.sent) != 1 {
		t.Fatalf("同一周不重发：%v", env.sent)
	}
}

// TestEditReportRetriesThenDeadLetters 发送失败下一轮重试，只补发没收到的人；8 次都失败进死信，监控给 warning、不算 P0。
func TestEditReportRetriesThenDeadLetters(t *testing.T) {
	env := newEditReportTestEnv(t, publishDistributionOwner{OpenID: "ou_a"}, publishDistributionOwner{OpenID: "ou_b"})
	env.fail["ou_b"] = true
	env.pipeline.runScheduledPublishEditReport(context.Background())
	rec := editReportRecord(t, env)
	if rec.State != publishEditReportStateFailed || rec.Attempts != 1 || rec.MessageIDs["ou_a"] == "" || !strings.Contains(rec.Error, "230013") {
		t.Fatalf("部分失败应记 failed 并保留已发的：%+v", rec)
	}
	for i := 0; i < publishEditReportMaxAttempts+2; i++ {
		env.now = env.now.Add(publishEditReportSweep) // 飞书明确拒收的不受一小时去重期限制，照常试满 8 次
		env.pipeline.runScheduledPublishEditReport(context.Background())
	}
	if len(env.sent) != 1 {
		t.Fatalf("已收到的人不重发：%v", env.sent)
	}
	rec = editReportRecord(t, env)
	if rec.State != publishEditReportStateDeadLetter || rec.Attempts != publishEditReportMaxAttempts {
		t.Fatalf("8 次后进死信：%+v", rec)
	}
	status := publishMonitoringStatus{Ready: true}
	env.pipeline.applyPublishEditReportMonitoring(&status)
	if len(status.Warnings) != 1 || status.Warnings[0] != "edit_report_dead_letter:2026-10-05" || !status.Ready || len(status.AlertIssues) != 0 {
		t.Fatalf("死信只给 warning：%+v", status)
	}
}

// TestEditReportNeedsMessageIDAndDurableRecord 飞书没回消息编号不算送达；记录写不进盘就不发。
func TestEditReportNeedsMessageIDAndDurableRecord(t *testing.T) {
	env := newEditReportTestEnv(t, publishDistributionOwner{OpenID: "ou_gary"})
	env.pipeline.editReportSender = func(context.Context, string, map[string]any) (string, error) { return "", nil }
	env.pipeline.runScheduledPublishEditReport(context.Background())
	if rec := editReportRecord(t, env); rec.State != publishEditReportStateFailed || len(rec.MessageIDs) != 0 || !strings.Contains(rec.Error, "没有返回消息编号") {
		t.Fatalf("空回执应记失败、下一轮再发：%+v", rec)
	}

	blocked := newEditReportTestEnv(t, publishDistributionOwner{OpenID: "ou_gary"})
	blocked.pipeline.editReports = newPublishEditReportStore(filepath.Join(t.TempDir(), "not-a-dir", "x.json"))
	if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(blocked.pipeline.editReports.path)), "not-a-dir"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	blocked.pipeline.runScheduledPublishEditReport(context.Background())
	if len(blocked.sent) != 0 {
		t.Fatalf("记不下「这次要发」就不发，免得每轮重发：%v", blocked.sent)
	}
}

// TestEditReportWaitsForReceivers 没设接收人只记不发；当周设了人，下一轮补发。
func TestEditReportWaitsForReceivers(t *testing.T) {
	env := newEditReportTestEnv(t)
	env.pipeline.runScheduledPublishEditReport(context.Background())
	if rec := editReportRecord(t, env); rec.State != publishEditReportStateSkipped || rec.Attempts != 0 || len(env.sent) != 0 {
		t.Fatalf("没设接收人应记 skipped：%+v sent=%v", rec, env.sent)
	}
	setEditReportReceivers(t, env, publishDistributionOwner{OpenID: "ou_gary"})
	env.now = env.now.Add(time.Hour)
	env.pipeline.runScheduledPublishEditReport(context.Background())
	if rec := editReportRecord(t, env); rec.State != publishEditReportStateSent || len(env.sent) != 1 {
		t.Fatalf("设了人后当周补发：%+v sent=%v", rec, env.sent)
	}
}

// TestEditReportPausedDeploymentDoesNotSend 候选部署暂停态不发。
func TestEditReportPausedDeploymentDoesNotSend(t *testing.T) {
	env := newEditReportTestEnv(t, publishDistributionOwner{OpenID: "ou_gary"})
	t.Setenv(deploymentWritesPausedEnv, "1")
	env.pipeline.runScheduledPublishEditReport(context.Background())
	if len(env.sent) != 0 {
		t.Fatalf("暂停态不发：%v", env.sent)
	}
}

// TestEditReportEndpoint 管理员接口：dry_run 只看不发不记；正式发一次，再发要 force=1；week 必须是周一。
func TestEditReportEndpoint(t *testing.T) {
	env := newEditReportTestEnv(t, publishDistributionOwner{OpenID: "ou_gary"})
	mux := newPublishTestMux(t, env.publishTestEnv)
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/edit-report?dry_run=1", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("要管理员：%d", rec.Code)
	}
	rec := doPublishReq(mux, http.MethodPost, "/api/publish/edit-report?dry_run=1", "", true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"week_start":"2026-10-05"`) ||
		!strings.Contains(rec.Body.String(), `"edited":3`) || !strings.Contains(rec.Body.String(), `"recipients":1`) || len(env.sent) != 0 {
		t.Fatalf("dry_run 只返回清单：%d %s sent=%v", rec.Code, rec.Body.String(), env.sent)
	}
	if got := editReportRecord(t, env); got.State != "" {
		t.Fatalf("dry_run 不记：%+v", got)
	}
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/edit-report?dry_run=1&week=2026-10-06", "", true); rec.Code != http.StatusBadRequest {
		t.Fatalf("week 不是周一应 400：%d", rec.Code)
	}
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/edit-report?week=2026-10-12", "", true); rec.Code != http.StatusConflict || len(env.sent) != 0 {
		t.Fatalf("没过完的一周只能预览：%d %s", rec.Code, rec.Body.String())
	}
	if got, _, _ := env.pipeline.editReports.get("2026-10-12"); got.State != "" {
		t.Fatalf("没过完的一周不留记录，周一定时照常发：%+v", got)
	}
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/edit-report?week=2026-10-05", "", true); rec.Code != http.StatusOK || len(env.sent) != 1 {
		t.Fatalf("正式发送：%d %s", rec.Code, rec.Body.String())
	}
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/edit-report?week=2026-10-05", "", true); rec.Code != http.StatusConflict || len(env.sent) != 1 {
		t.Fatalf("发过再发应 409：%d %s", rec.Code, rec.Body.String())
	}
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/edit-report?week=2026-10-05&force=1", "", true); rec.Code != http.StatusOK || len(env.sent) != 2 {
		t.Fatalf("force=1 重发：%d %s", rec.Code, rec.Body.String())
	}
}

// TestEditReportOwnersSavedSeparately 周报接收人与分发、校对负责人分开存；换应用清掉。
func TestEditReportOwnersSavedSeparately(t *testing.T) {
	t.Setenv(publishNotificationAppsEnv, `[{"id":"review-app","name":"审核应用","app_id":"cli_review","app_secret":"secret"}]`)
	env := newPublishTestEnv(t)
	setTestProofreadOwners(t, env, publishDistributionOwner{OpenID: "ou_a", Name: "甲"})
	env.pipeline.distributionOwnerLister = func(context.Context, string, string) ([]publishDistributionOwner, error) {
		return []publishDistributionOwner{{OpenID: "ou_a", Name: "甲"}, {OpenID: "ou_b", Name: "乙"}}, nil
	}
	mux := newPublishTestMux(t, env)
	path := "/api/publish/delivery-settings/edit-report-owners"
	if rec := doPublishReq(mux, http.MethodPut, path, `{"open_ids":["ou_b"]}`, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("需要管理员：%d", rec.Code)
	}
	if rec := doPublishReq(mux, http.MethodPut, path, `{"open_ids":["ou_stranger"]}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("不在群里的人不能选：%d", rec.Code)
	}
	rec := doPublishReq(mux, http.MethodPut, path, `{"open_ids":["ou_b"]}`, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"edit_report_owners":{"owners":[{"open_id":"ou_b","name":"乙"}],"stale":false}`) {
		t.Fatalf("应保存周报接收人：%d %s", rec.Code, rec.Body.String())
	}
	if owners, _ := env.pipeline.publishEditReportRecipients(); len(owners) != 1 || owners[0].OpenID != "ou_b" {
		t.Fatalf("周报发给这位：%+v", owners)
	}
	if owners := env.pipeline.publishProofreadOwners("review-app"); len(owners) != 1 || owners[0].OpenID != "ou_a" {
		t.Fatalf("校对负责人不受影响：%+v", owners)
	}
	rec = doPublishReq(mux, http.MethodGet, "/api/publish/delivery-settings/distribution-owners", "", true)
	if !strings.Contains(rec.Body.String(), `"edit_report":{"owners":[{"open_id":"ou_b","name":"乙"}],"stale":false}`) {
		t.Fatalf("成员目录接口应带上周报接收人：%s", rec.Body.String())
	}
	settings, _ := env.pipeline.deliverySettings.read()
	kept, err := publishDeliverySelectionFromRequest(publishDeliverySelectionRequest{AppProfileID: "review-app", ChatIDs: []string{"oc_review"}},
		settings, []feishuNotificationApp{{ID: "review-app"}}, []feishuChat{{ChatID: "oc_review"}}, "admin")
	if err != nil || len(kept.EditReportOwners) != 1 {
		t.Fatalf("同应用改群应保留：%+v %v", kept, err)
	}
	moved, err := publishDeliverySelectionFromRequest(publishDeliverySelectionRequest{AppProfileID: "other-app", ChatIDs: []string{"oc_review"}},
		settings, []feishuNotificationApp{{ID: "other-app"}}, []feishuChat{{ChatID: "oc_review"}}, "admin")
	if err != nil || len(moved.EditReportOwners) != 0 {
		t.Fatalf("换应用应清掉：%+v %v", moved, err)
	}
}

// TestFeishuNotifierSendsPostToOpenID 周报用审核应用按 open_id 私信富文本。
func TestFeishuNotifierSendsPostToOpenID(t *testing.T) {
	var gotType, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"msg":"ok","tenant_access_token":"tok","expire":7200}`)
		case "/open-apis/im/v1/messages":
			gotType = r.URL.Query().Get("receive_id_type")
			body, _ := io.ReadAll(r.Body)
			gotBody = string(body)
			fmt.Fprint(w, `{"code":0,"msg":"ok","data":{"message_id":"om_1"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	notifier := newFeishuNotifierForOpenID(feishuNotificationApp{ID: "review-app", AppID: "cli", AppSecret: "secret"}, "ou_gary")
	if notifier == nil || newFeishuNotifierForOpenID(feishuNotificationApp{AppID: "cli", AppSecret: "secret"}, "ou bad") != nil {
		t.Fatal("open_id 不合法时不构造")
	}
	notifier.baseURL = srv.URL
	uuid := publishEditReportUUID("2026-10-05", 0, "ou_gary")
	id, err := notifier.sendMessageWithUUID(context.Background(), "post", `{"zh_cn":{"title":"t","content":[]}}`, uuid)
	if err != nil || id != "om_1" || gotType != "open_id" || !strings.Contains(gotBody, `"receive_id":"ou_gary"`) ||
		!strings.Contains(gotBody, `"msg_type":"post"`) || !strings.Contains(gotBody, `"uuid":"`+uuid+`"`) {
		t.Fatalf("应按 open_id 发 post 并带去重键：id=%q err=%v type=%q body=%s", id, err, gotType, gotBody)
	}
	if len(uuid) > 50 || uuid == publishEditReportUUID("2026-10-05", 1, "ou_gary") || uuid == publishEditReportUUID("2026-10-05", 0, "ou_other") {
		t.Fatalf("去重键≤50 字符，按周、重发轮次、接收人区分：%q", uuid)
	}
	if _, err := notifier.sendMessageWithID(context.Background(), "text", `{"text":"x"}`); err != nil || strings.Contains(gotBody, `"uuid"`) {
		t.Fatalf("其他通知不带去重键：%v %s", err, gotBody)
	}
}

func TestEditReportLastWeekBoundaries(t *testing.T) {
	for _, tc := range []struct {
		now  time.Time
		want string
	}{
		{time.Date(2026, 10, 12, 0, 0, 0, 0, publishSiteTimeZone), "2026-10-05"},
		{time.Date(2026, 10, 15, 13, 0, 0, 0, publishSiteTimeZone), "2026-10-05"},
		{time.Date(2026, 10, 18, 23, 59, 0, 0, publishSiteTimeZone), "2026-10-05"},
		{time.Date(2026, 10, 11, 16, 30, 0, 0, time.UTC), "2026-10-05"}, // 北京时间周一 00:30
	} {
		start, end := publishEditReportLastWeek(tc.now)
		if got := start.Format("2006-01-02"); got != tc.want || end.Sub(start) != 7*24*time.Hour {
			t.Fatalf("%s 的上一整周应从 %s 起，实际 %s–%s", tc.now, tc.want, got, end)
		}
	}
}

// TestEditReportStoreKeepsTwoYears 记录只留最近 104 周；换了审核应用后旧 open_id 作废，不拿新应用去发。
func TestEditReportStoreKeepsTwoYears(t *testing.T) {
	store := newPublishEditReportStore(filepath.Join(t.TempDir(), "edit-report.json"))
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, publishSiteTimeZone)
	for i := 0; i < publishEditReportKeepRecords+3; i++ {
		week := start.AddDate(0, 0, 7*i).Format("2006-01-02")
		if err := store.put(publishEditReportRecord{Week: week, State: publishEditReportStateSent}); err != nil {
			t.Fatal(err)
		}
	}
	records, err := store.list()
	if err != nil {
		t.Fatal(err)
	}
	newest := start.AddDate(0, 0, 7*(publishEditReportKeepRecords+2)).Format("2006-01-02")
	if len(records) != publishEditReportKeepRecords || records[0].Week != newest {
		t.Fatalf("留最近 104 周、按周倒序：%d %+v", len(records), records[0])
	}
	if old, _, _ := store.get(start.Format("2006-01-02")); old.State != "" {
		t.Fatalf("超过 104 周的旧记录应清掉：%+v", old)
	}
	if kept, _, _ := store.get(start.AddDate(0, 0, 7*3).Format("2006-01-02")); kept.State != publishEditReportStateSent {
		t.Fatalf("第 104 周以内的应保留：%+v", kept)
	}
	if err := store.put(publishEditReportRecord{Week: "2023-01-02", State: publishEditReportStateSending}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := store.get("2023-01-02"); got.State != publishEditReportStateSending {
		t.Fatalf("这次写的这条不能被裁掉，否则回执会丢：%+v", got)
	}

	env := newEditReportTestEnv(t, publishDistributionOwner{OpenID: "ou_gary"})
	t.Setenv(publishNotificationAppsEnv, `[{"id":"review-app","name":"审核应用","app_id":"cli_review","app_secret":"s"},{"id":"other-app","name":"新应用","app_id":"cli_other","app_secret":"s"}]`)
	settings, err := env.pipeline.deliverySettings.read()
	if err != nil {
		t.Fatal(err)
	}
	settings.AppProfileID = "other-app"
	if err := env.pipeline.deliverySettings.write(settings); err != nil {
		t.Fatal(err)
	}
	env.pipeline.runScheduledPublishEditReport(context.Background())
	if rec := editReportRecord(t, env); rec.State != publishEditReportStateSkipped || len(env.sent) != 0 {
		t.Fatalf("接收人是旧应用选的，换应用后要重选，不发：%+v sent=%v", rec, env.sent)
	}
}

// TestEditReportUnconfirmedSendStopsAfterDedupWindow 发出去没拿到结果：去重期内带同一个 uuid 重发，过了就记死信、不再盲发。
func TestEditReportUnconfirmedSendStopsAfterDedupWindow(t *testing.T) {
	env := newEditReportTestEnv(t, publishDistributionOwner{OpenID: "ou_gary", Name: "郑梽煌"})
	calls := 0
	env.pipeline.editReportSender = func(context.Context, string, map[string]any) (string, error) {
		calls++
		return "", &feishuSendUncertainError{errors.New("飞书接口不可达: timeout")}
	}
	env.pipeline.runScheduledPublishEditReport(context.Background())
	if rec := editReportRecord(t, env); rec.State != publishEditReportStateFailed || rec.Unconfirmed["ou_gary"] == "" || calls != 1 {
		t.Fatalf("没拿到结果先记下时间，下一轮再试：%+v calls=%d", rec, calls)
	}
	env.now = env.now.Add(publishEditReportSweep)
	env.pipeline.runScheduledPublishEditReport(context.Background())
	if calls != 2 {
		t.Fatalf("30 分钟后还在飞书去重期内，可以带同一个 uuid 重发：calls=%d", calls)
	}
	env.now = env.now.Add(publishEditReportSweep)
	env.pipeline.runScheduledPublishEditReport(context.Background())
	rec := editReportRecord(t, env)
	if calls != 2 || rec.State != publishEditReportStateDeadLetter || !strings.Contains(rec.Error, "郑梽煌") || !strings.Contains(rec.Error, "去重期") {
		t.Fatalf("过了去重期不再盲发，记死信等人确认：%+v calls=%d", rec, calls)
	}
	status := publishMonitoringStatus{Ready: true}
	env.pipeline.applyPublishEditReportMonitoring(&status)
	if len(status.Warnings) != 1 || !status.Ready {
		t.Fatalf("进监控 warning：%+v", status)
	}

	mux := newPublishTestMux(t, env.publishTestEnv)
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/edit-report?week=2026-10-05", "", true); rec.Code != http.StatusConflict || calls != 2 {
		t.Fatalf("不带 force 不重发：%d %s", rec.Code, rec.Body.String())
	}
	env.pipeline.editReportSender = func(_ context.Context, openID string, _ map[string]any) (string, error) {
		env.sent = append(env.sent, openID)
		return "om_" + openID, nil
	}
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/edit-report?week=2026-10-05&force=1", "", true); rec.Code != http.StatusOK || len(env.sent) != 1 {
		t.Fatalf("确认没收到后 force 重发：%d %s", rec.Code, rec.Body.String())
	}
	if got := editReportRecord(t, env); got.Generation != 1 || got.State != publishEditReportStateSent || len(got.Unconfirmed) != 0 {
		t.Fatalf("force 换新一轮去重键：%+v", got)
	}
}

// TestEditReportRecoversInterruptedSend 发到一半进程退出：没拿到回执的人过了去重期不再发；试满 8 次的直接收尾。
func TestEditReportRecoversInterruptedSend(t *testing.T) {
	env := newEditReportTestEnv(t, publishDistributionOwner{OpenID: "ou_gary"})
	started := env.now.UTC().Format(time.RFC3339)
	if err := env.pipeline.editReports.put(publishEditReportRecord{
		Week: "2026-10-05", State: publishEditReportStateSending, Attempts: 1, Unconfirmed: map[string]string{"ou_gary": started},
	}); err != nil {
		t.Fatal(err)
	}
	env.now = env.now.Add(75 * time.Minute)
	env.pipeline.runScheduledPublishEditReport(context.Background())
	if rec := editReportRecord(t, env); len(env.sent) != 0 || rec.State != publishEditReportStateDeadLetter {
		t.Fatalf("进程中断后超过去重期不能盲发：%+v sent=%v", rec, env.sent)
	}

	capped := newEditReportTestEnv(t, publishDistributionOwner{OpenID: "ou_gary"})
	if err := capped.pipeline.editReports.put(publishEditReportRecord{
		Week: "2026-10-05", State: publishEditReportStateSending, Attempts: publishEditReportMaxAttempts, Error: "上次失败",
	}); err != nil {
		t.Fatal(err)
	}
	capped.pipeline.runScheduledPublishEditReport(context.Background())
	if rec := editReportRecord(t, capped); len(capped.sent) != 0 || rec.State != publishEditReportStateDeadLetter || rec.Attempts != publishEditReportMaxAttempts {
		t.Fatalf("第 8 次中断后不能再试第 9 次：%+v sent=%v", rec, capped.sent)
	}
	done := newEditReportTestEnv(t, publishDistributionOwner{OpenID: "ou_gary"})
	for _, rec := range []publishEditReportRecord{
		{Week: "2026-10-05", State: publishEditReportStateSending, Attempts: publishEditReportMaxAttempts, MessageIDs: map[string]string{"ou_gary": "om_1"}},
		{Week: "2026-09-28", State: publishEditReportStateSending, Attempts: 1, MessageIDs: map[string]string{"ou_gary": "om_0"}},
	} {
		if err := done.pipeline.editReports.put(rec); err != nil {
			t.Fatal(err)
		}
	}
	done.pipeline.runScheduledPublishEditReport(context.Background())
	last, _, _ := done.pipeline.editReports.get("2026-09-28")
	if rec := editReportRecord(t, done); len(done.sent) != 0 || rec.State != publishEditReportStateSent || last.State != publishEditReportStateSent {
		t.Fatalf("回执齐全只差写「已发」就中断的，补记已发、不误报死信：%+v %+v sent=%v", rec, last, done.sent)
	}
	status := publishMonitoringStatus{Ready: true}
	done.pipeline.applyPublishEditReportMonitoring(&status)
	if len(status.Warnings) != 0 {
		t.Fatalf("不该有死信 warning：%+v", status)
	}
}

// TestEditReportRetiresLeftoverWeeks 换周后上一周还没发成的收尾成死信、报 7 天；更早两年的周不能发。
func TestEditReportRetiresLeftoverWeeks(t *testing.T) {
	env := newEditReportTestEnv(t, publishDistributionOwner{OpenID: "ou_gary"})
	if err := env.pipeline.editReports.put(publishEditReportRecord{Week: "2026-09-28", State: publishEditReportStateFailed, Attempts: 1, Error: "飞书发消息失败 code=230013"}); err != nil {
		t.Fatal(err)
	}
	env.pipeline.runScheduledPublishEditReport(context.Background())
	old, _, _ := env.pipeline.editReports.get("2026-09-28")
	if old.State != publishEditReportStateDeadLetter || !strings.Contains(old.Error, "换周时还没发出去") || old.Attempts != 1 {
		t.Fatalf("上一周遗留的失败应收尾：%+v", old)
	}
	if rec := editReportRecord(t, env); rec.State != publishEditReportStateSent || len(env.sent) != 1 {
		t.Fatalf("不挡新一周：%+v", rec)
	}
	status := publishMonitoringStatus{Ready: true}
	env.pipeline.applyPublishEditReportMonitoring(&status)
	if len(status.Warnings) != 1 || status.Warnings[0] != "edit_report_dead_letter:2026-09-28" {
		t.Fatalf("遗留的死信也要报：%+v", status)
	}
	env.now = env.now.Add(8 * 24 * time.Hour)
	status = publishMonitoringStatus{Ready: true}
	env.pipeline.applyPublishEditReportMonitoring(&status)
	if len(status.Warnings) != 0 {
		t.Fatalf("死信只报 7 天：%+v", status)
	}

	mux := newPublishTestMux(t, env.publishTestEnv)
	if rec := doPublishReq(mux, http.MethodPost, "/api/publish/edit-report?week=2024-09-30", "", true); rec.Code != http.StatusBadRequest {
		t.Fatalf("两年前的周不发：%d %s", rec.Code, rec.Body.String())
	}
}
