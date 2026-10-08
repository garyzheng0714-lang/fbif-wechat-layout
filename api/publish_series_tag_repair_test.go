package main

import (
	"context"
	"testing"
	"time"
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

// TestRepairNewsSeriesTagLink 63149：已发布稿文末系列声明仍链公众号合集，按资讯 id 直接改网站现稿
// （编辑手工稿也一样）。只换声明段的系列链，其他正文与字段原样写回；dry_run 不写；重复执行不再写。
func TestRepairNewsSeriesTagLink(t *testing.T) {
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	album := `https://mp.weixin.qq.com/mp/appmsgalbum?album_id=4500314305401487362`
	tagLink := `<a href="https://www.foodtalks.cn/news/tag/114000" target="_blank" rel="noopener">茶叶系列</a>`
	liveContent := `<p>这是我` + tagLink + `的第38篇。</p><p>编辑手改的正文，提到<a href="` + album + `">茶叶系列</a>。</p><p>这是我<a href="` + album + `" target="_blank" rel="noopener">茶叶系列</a>的第38篇。</p>`
	stub := &seriesRepairStub{
		newsTagStub: newsTagStub{byKeyword: map[string][]foodTalksTag{
			"茶叶系列": {{ID: 114000, Name: "茶叶系列", TagTypeID: 6}},
		}},
		live: foodTalksNewsDetail{ID: 63149, Title: "编辑改过的标题", Content: liveContent,
			PublishStatus: foodTalksPublishStatusPublished, Language: "ZH", NewsTags: []foodTalksNewsTag{{TagID: 114000}}},
	}
	p := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return stub, nil }}

	res, err := p.repairNewsSeriesTagLink(context.Background(), 63149, true)
	if err != nil || !res.Changed || len(stub.puts) != 0 {
		t.Fatalf("dry_run 应报告需修且不写：res=%+v err=%v puts=%d", res, err, len(stub.puts))
	}
	res, err = p.repairNewsSeriesTagLink(context.Background(), 63149, false)
	if err != nil || !res.Changed || len(stub.puts) != 1 {
		t.Fatalf("应写回一次：res=%+v err=%v puts=%d", res, err, len(stub.puts))
	}
	put := stub.puts[0]
	want := `<p>这是我` + tagLink + `的第38篇。</p><p>编辑手改的正文，提到<a href="` + album + `">茶叶系列</a>。</p><p>这是我` + tagLink + `的第38篇。</p>`
	if put.Content != want {
		t.Fatalf("只应替换文末声明段：\n got %s\nwant %s", put.Content, want)
	}
	if put.ID != 63149 || put.Title != "编辑改过的标题" || put.PublishStatus != foodTalksPublishStatusPublished || len(put.NewsTags) != 1 {
		t.Fatalf("其余字段必须按网站现稿原样写回：%+v", put)
	}

	stub.live.Content = put.Content
	res, err = p.repairNewsSeriesTagLink(context.Background(), 63149, false)
	if err != nil || res.Changed || len(stub.puts) != 1 {
		t.Fatalf("已修好的稿应幂等不写：res=%+v err=%v puts=%d", res, err, len(stub.puts))
	}
}
