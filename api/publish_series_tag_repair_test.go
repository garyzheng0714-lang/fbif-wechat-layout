package main

import (
	"context"
	"strings"
	"testing"
)

type seriesRepairStub struct {
	newsTagStub
	live foodTalksNewsDetail
	puts []foodTalksNews
}

func (s *seriesRepairStub) GetNews(context.Context, int64) (foodTalksNewsDetail, error) {
	return s.live, nil
}

func (s *seriesRepairStub) UpdateNews(_ context.Context, n foodTalksNews) error {
	s.puts = append(s.puts, n)
	return nil
}

// TestRepairPublishedSeriesTagLink 63149：已发布稿文末系列声明仍链公众号合集，直接改网站现稿。
// 只换声明段的系列链，编辑在 CMS 改过的其他正文与字段原样写回；dry_run 不写。
func TestRepairPublishedSeriesTagLink(t *testing.T) {
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	env := newPublishTestEnv(t)
	submitted, _, err := env.pipeline.submit(testPublishSourceURL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	job, err := env.store.update(submitted.ID, func(j *publishJob) {
		j.Stage = publishStagePublished
		j.Author = "Mote莫特"
		j.BlocksJSON = `[{"k":"txt","text":"作者：Mote莫特"},{"k":"txt","text":"这是我茶叶系列的第38篇。"},{"k":"txt","text":"正文"}]`
	})
	if err != nil {
		t.Fatal(err)
	}
	album := `https://mp.weixin.qq.com/mp/appmsgalbum?album_id=4500314305401487362`
	tagLink := `<a href="https://www.foodtalks.cn/news/tag/114000" target="_blank" rel="noopener">茶叶系列</a>`
	liveContent := `<p>这是我` + tagLink + `的第38篇。</p><p>编辑手改的正文，提到<a href="` + album + `">茶叶系列</a>。</p><p>这是我<a href="` + album + `" target="_blank" rel="noopener">茶叶系列</a>的第38篇。</p>`
	stub := &seriesRepairStub{
		newsTagStub: newsTagStub{byKeyword: map[string][]foodTalksTag{
			"Mote莫特": {{ID: 99913, Name: "Mote莫特", TagTypeID: 6}},
			"茶叶系列":   {{ID: 114000, Name: "茶叶系列", TagTypeID: 6}},
		}},
		live: foodTalksNewsDetail{ID: job.FoodTalksNewsID, Title: "编辑改过的标题", Content: liveContent,
			PublishStatus: foodTalksPublishStatusPublished, Language: "ZH", NewsTags: []foodTalksNewsTag{{TagID: 114000}}},
	}
	env.pipeline.foodTalks = func() (foodTalksAPI, error) { return stub, nil }

	res, err := env.pipeline.repairPublishedSeriesTagLink(context.Background(), job.ID, true)
	if err != nil || !res.Changed || len(stub.puts) != 0 {
		t.Fatalf("dry_run 应报告需修且不写：res=%+v err=%v puts=%d", res, err, len(stub.puts))
	}
	res, err = env.pipeline.repairPublishedSeriesTagLink(context.Background(), job.ID, false)
	if err != nil || !res.Changed || len(stub.puts) != 1 {
		t.Fatalf("应写回一次：res=%+v err=%v puts=%d", res, err, len(stub.puts))
	}
	put := stub.puts[0]
	want := `<p>这是我` + tagLink + `的第38篇。</p><p>编辑手改的正文，提到<a href="` + album + `">茶叶系列</a>。</p><p>这是我` + tagLink + `的第38篇。</p>`
	if put.Content != want {
		t.Fatalf("只应替换文末声明段：\n got %s\nwant %s", put.Content, want)
	}
	if put.Title != "编辑改过的标题" || put.PublishStatus != foodTalksPublishStatusPublished || len(put.NewsTags) != 1 {
		t.Fatalf("其余字段必须按网站现稿原样写回：%+v", put)
	}

	stub.live.Content = put.Content
	res, err = env.pipeline.repairPublishedSeriesTagLink(context.Background(), job.ID, false)
	if err != nil || res.Changed || len(stub.puts) != 1 || !strings.Contains(stub.live.Content, tagLink) {
		t.Fatalf("已修好的稿应幂等不写：res=%+v err=%v puts=%d", res, err, len(stub.puts))
	}
}
