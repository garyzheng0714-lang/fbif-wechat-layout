package main

// flash_live_test.go — 快讯在 FoodTalks 测试环境真跑一遍（平时跳过）。怎么跑见 docs/foodtalks-flash.md「测试环境走一遍」。
// 用真代码：资讯订阅取原文、飞书读规范、MiMo 写稿和检查、FoodTalks 测试环境发布；卡片不发，存成文件看。
// 只许测试环境：FOODTALKS_BASE_URL、FOODTALKS_FRONT_URL 不是测试域名就直接失败。发上去的快讯最后一律下架。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type flashLiveCardFiles struct{ dir string }

func (c flashLiveCardFiles) chatID() string { return "live-test" }
func (c flashLiveCardFiles) send(_ context.Context, card map[string]any) (string, error) {
	return "om_live_" + strconv.FormatInt(time.Now().UnixNano(), 36), c.write("sent", card)
}
func (c flashLiveCardFiles) update(_ context.Context, id string, card map[string]any) error {
	return c.write("updated-"+id, card)
}
func (c flashLiveCardFiles) write(name string, card map[string]any) error {
	b, _ := json.MarshalIndent(card, "", " ")
	return os.WriteFile(filepath.Join(c.dir, fmt.Sprintf("%d-%s.json", time.Now().UnixNano(), name)), b, 0600)
}

type flashLiveResult struct {
	ArticleID     string        `json:"article_id"`
	JobID         int64         `json:"job_id"`
	Stage         string        `json:"stage"`
	Seconds       float64       `json:"seconds"`
	Job           flashJob      `json:"job"`
	Card          any           `json:"card,omitempty"`
	PublicVisible bool          `json:"public_visible"`
	Banned        bool          `json:"banned"`
	Notes         []string      `json:"notes,omitempty"`
	Usage         aiTokenUsage  `json:"usage"`
	PublishSecs   float64       `json:"publish_seconds,omitempty"`
	SpecRevision  int64         `json:"spec_revision"`
	Source        flashLiveFrom `json:"from"`
}

type flashLiveFrom struct {
	Title      string `json:"title"`
	SourceName string `json:"source_name"`
	URL        string `json:"url"`
	Lang       string `json:"lang"`
}

func TestFlashLiveTrialOnFoodTalksTest(t *testing.T) {
	if os.Getenv("FLASH_LIVE_TEST") != "1" {
		t.Skip("FLASH_LIVE_TEST=1 才跑")
	}
	if foodTalksURLTarget(os.Getenv(foodTalksBaseURLEnv)) != "test" || foodTalksURLTarget(os.Getenv(siteFrontURLEnv)) != "test" {
		t.Fatal("只许在 FoodTalks 测试环境跑")
	}
	out := os.Getenv("FLASH_LIVE_OUT")
	if out == "" {
		t.Fatal("要 FLASH_LIVE_OUT")
	}
	cardDir := filepath.Join(out, "cards")
	_ = os.MkdirAll(cardDir, 0700)
	var candidates struct {
		Candidates []struct {
			ArticleID string `json:"article_id"`
			URL       string `json:"url"`
		} `json:"candidates"`
	}
	raw, err := os.ReadFile(os.Getenv("FLASH_LIVE_CANDIDATES"))
	if err != nil || json.Unmarshal(raw, &candidates) != nil || len(candidates.Candidates) == 0 {
		t.Fatalf("候选读不了：%v", err)
	}

	store := newFlashJobStore(filepath.Join(out, "flash-jobs.json"))
	p := newFlashPipeline(store)
	defer p.stop()
	app := &feishuNotifier{baseURL: defaultFeishuBaseURL, appID: os.Getenv("FEISHU_LOGIN_APP_ID"),
		appSecret: os.Getenv("FEISHU_LOGIN_APP_SECRET"), httpClient: &http.Client{Timeout: 15 * time.Second}}
	spec := &flashSpecSource{doc: os.Getenv(flashSpecDocEnv), token: app.tenantAccessToken, baseURL: defaultFeishuBaseURL,
		client: &http.Client{Timeout: 30 * time.Second}, cachePath: flashSpecCachePath(store.path), now: time.Now}
	first, err := spec.load(context.Background())
	if err != nil {
		t.Fatalf("排版工具应用读不了规范：%v", err)
	}
	t.Logf("规范第 %d 版，%d 字，不推荐说法 %d 条", first.Revision, len([]rune(first.Text)), len(first.Avoid))
	_ = os.WriteFile(filepath.Join(out, "spec.txt"), []byte(first.Text), 0600)
	p.spec = spec.load
	p.cards = flashLiveCardFiles{dir: cardDir}
	p.frontURL = siteFrontURL()

	results := make([]flashLiveResult, 0, len(candidates.Candidates))
	for _, c := range candidates.Candidates {
		job, _, err := store.create(c.ArticleID, c.URL, "")
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		p.advance(job.ID) // 一步步走到要等人为止，同步跑
		job, _ = store.byID(job.ID)
		r := flashLiveResult{ArticleID: c.ArticleID, JobID: job.ID, Stage: job.Stage, Seconds: time.Since(start).Seconds(),
			Usage: job.Usage, SpecRevision: job.SpecRevision}
		if job.Source != nil {
			a := job.Source.Article
			r.Source = flashLiveFrom{Title: a.Title, SourceName: a.SourceName, URL: a.URL, Lang: a.Lang}
		}
		t.Logf("#%d %s %s %.0f 秒 %s %s", job.ID, c.ArticleID[:8], job.Stage, r.Seconds, job.SkipReason, job.Error)
		results = append(results, r)
	}

	if os.Getenv("FLASH_LIVE_PUBLISH") == "1" {
		site, err := newFoodTalksClientFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		for i := range results {
			r := &results[i]
			job, _ := store.byID(r.JobID)
			if job.Stage != flashStageAwaitingReview {
				continue
			}
			start := time.Now()
			if flashApproveBlocked(job, job.Version) != "" {
				r.Notes = append(r.Notes, "不能通过："+flashApproveBlocked(job, job.Version))
				continue
			}
			if _, _, err := store.updateIf(job.ID, func(j flashJob) bool { return j.Stage == flashStageAwaitingReview }, func(j *flashJob) {
				j.Stage = flashStagePublishing
				j.ReviewedBy = "live-test"
				j.ReviewedAt = time.Now().UTC().Format(time.RFC3339)
				j.PublishIntentAt = j.ReviewedAt
			}); err != nil {
				t.Fatal(err)
			}
			p.advance(job.ID)
			job, _ = store.byID(job.ID)
			r.PublishSecs = time.Since(start).Seconds()
			if job.FlashID == 0 {
				r.Notes = append(r.Notes, "没发上："+job.Error)
				continue
			}
			// 记录丢了再跑一遍发布：要认出已发的那条，不建第二条。
			if _, err := store.update(job.ID, func(j *flashJob) { j.Stage = flashStagePublishing; j.FlashID = 0 }); err == nil {
				p.advance(job.ID)
				again, _ := store.byID(job.ID)
				if again.FlashID != job.FlashID {
					r.Notes = append(r.Notes, fmt.Sprintf("重跑认成了另一条 %d（原来 %d）", again.FlashID, job.FlashID))
				}
				if found, err := site.SearchFlashes(context.Background(), job.Draft.Title, 20); err == nil {
					same := 0
					for _, f := range found {
						if f.Title == job.Draft.Title {
							same++
						}
					}
					if same != 1 {
						r.Notes = append(r.Notes, fmt.Sprintf("同题的有 %d 条", same))
					}
				}
			}
			r.PublicVisible = flashLivePublicVisible(site, job.FlashID)
			if err := site.BanFlash(context.Background(), job.FlashID); err != nil {
				r.Notes = append(r.Notes, "下架没成："+err.Error())
			} else if got, err := site.GetFlash(context.Background(), job.FlashID); err == nil && got.IsBanned == 1 {
				r.Banned = !flashLivePublicVisible(site, job.FlashID)
			}
			r.Stage = job.Stage
			t.Logf("#%d 发到测试环境 %d，前台看得见 %v，已下架 %v", job.ID, job.FlashID, r.PublicVisible, r.Banned)
		}
	}
	for i := range results {
		job, _ := store.byID(results[i].JobID)
		results[i].Job = job
		results[i].Card = buildFlashCard(job, time.Now())
		results[i].Usage = job.Usage
	}
	b, _ := json.MarshalIndent(results, "", " ")
	if err := os.WriteFile(filepath.Join(out, "results.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}

// flashLivePublicVisible 前台接口能不能读到这条（测试环境前台接口和后台同一个网关）。
func flashLivePublicVisible(c *foodTalksClient, id int64) bool {
	data, err := c.adminCall(context.Background(), "前台快讯", http.MethodGet, "/news/short/news/"+strconv.FormatInt(id, 10), nil)
	return err == nil && len(data) > 0 && !strings.EqualFold(strings.TrimSpace(string(data)), "null")
}
