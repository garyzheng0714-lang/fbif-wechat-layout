package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type seriesRepairStub struct {
	newsTagStub
	live    foodTalksNewsDetail
	puts    []foodTalksNews
	byTitle map[string][]foodTalksNewsListItem
}

func (s *seriesRepairStub) GetNews(context.Context, int64) (foodTalksNewsDetail, error) {
	return s.live, nil
}

func (s *seriesRepairStub) UpdateNews(_ context.Context, n foodTalksNews) error {
	s.puts = append(s.puts, n)
	return nil
}

func (s *seriesRepairStub) SearchNewsByTitle(_ context.Context, title string) ([]foodTalksNewsListItem, error) {
	return s.byTitle[title], nil
}

// TestRepairNewsLinks 63149：已发布稿文末系列声明仍链公众号合集、Mote 互推链仍是公众号长链。
// 按资讯 id 直接改网站现稿（编辑手工稿也一样）：只换这两类链接，找不到站内稿的公众号链保留；
// 其他正文与字段原样写回；dry_run 不写；重复执行不再写。
func TestRepairNewsLinks(t *testing.T) {
	t.Setenv("FOODTALKS_FRONT_URL", "https://www.foodtalks.cn")
	album := `https://mp.weixin.qq.com/mp/appmsgalbum?album_id=4500314305401487362`
	twg := `https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&amp;mid=2652931174&amp;idx=1&amp;sn=e2949c&amp;scene=21#wechat_redirect`
	lost := `https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&amp;mid=2650000001&amp;idx=1&amp;sn=zzz`
	tagLink := `<a href="https://www.foodtalks.cn/news/tag/114000" target="_blank" rel="noopener">茶叶系列</a>`
	liveContent := `<p>这是我` + tagLink + `的第38篇。</p>` +
		`<p>海外品牌可以与T9竞争的应是<a href="` + twg + `" target="_blank" rel="noopener">TWG</a>和<a href="` + lost + `">某品牌</a>。</p>` +
		`<p>这是我<a href="` + album + `" target="_blank" rel="noopener">茶叶系列</a>的第38篇。</p>`
	stub := &seriesRepairStub{
		newsTagStub: newsTagStub{byKeyword: map[string][]foodTalksTag{
			"茶叶系列": {{ID: 114000, Name: "茶叶系列", TagTypeID: 6}},
		}},
		live: foodTalksNewsDetail{ID: 63149, Title: "编辑改过的标题", Author: "Mote莫特", Content: liveContent,
			PublishStatus: foodTalksPublishStatusPublished, Language: "ZH", NewsTags: []foodTalksNewsTag{{TagID: 114000}}},
		byTitle: map[string][]foodTalksNewsListItem{
			"新加坡TWG，如何打造出全球最大的奢侈品茶叶连锁？": {{ID: 62980, Title: "新加坡TWG，如何打造出全球最大的奢侈品茶叶连锁？", PublishStatus: foodTalksPublishStatusPublished}},
		},
	}
	p := &publishPipeline{now: time.Now, foodTalks: func() (foodTalksAPI, error) { return stub, nil },
		fetchWechatTitle: func(_ context.Context, u string) (string, error) {
			if strings.Contains(u, "mid=2652931174") {
				return "新加坡TWG，如何打造出全球最大的奢侈品茶叶连锁？", nil
			}
			return "站内没有的文章", nil
		}}

	res, err := p.repairNewsLinks(context.Background(), 63149, true)
	if err != nil || !res.Changed || !res.Mote || len(stub.puts) != 0 {
		t.Fatalf("dry_run 应报告需修且不写：res=%+v err=%v puts=%d", res, err, len(stub.puts))
	}
	res, err = p.repairNewsLinks(context.Background(), 63149, false)
	if err != nil || !res.Changed || len(stub.puts) != 1 {
		t.Fatalf("应写回一次：res=%+v err=%v puts=%d", res, err, len(stub.puts))
	}
	put := stub.puts[0]
	want := `<p>这是我` + tagLink + `的第38篇。</p>` +
		`<p>海外品牌可以与T9竞争的应是<a href="https://www.foodtalks.cn/news/62980" target="_blank" rel="noopener">TWG</a>和<a href="` + lost + `">某品牌</a>。</p>` +
		`<p>这是我` + tagLink + `的第38篇。</p>`
	if put.Content != want {
		t.Fatalf("只应换系列声明链和找得到的公众号链：\n got %s\nwant %s", put.Content, want)
	}
	if put.ID != 63149 || put.Title != "编辑改过的标题" || put.PublishStatus != foodTalksPublishStatusPublished || len(put.NewsTags) != 1 {
		t.Fatalf("其余字段必须按网站现稿原样写回：%+v", put)
	}

	stub.live.Content = put.Content
	res, err = p.repairNewsLinks(context.Background(), 63149, false)
	if err != nil || res.Changed || len(stub.puts) != 1 {
		t.Fatalf("已修好的稿应幂等不写：res=%+v err=%v puts=%d", res, err, len(stub.puts))
	}

	// 非 Mote 稿只补系列标签链，不碰公众号链
	stub.live.Author = "张三"
	stub.live.Content = liveContent
	stub.puts = nil
	if _, err := p.repairNewsLinks(context.Background(), 63149, false); err != nil {
		t.Fatal(err)
	}
	if len(stub.puts) != 1 || !strings.Contains(stub.puts[0].Content, twg) {
		t.Fatalf("非 Mote 稿的公众号链不得改：%+v", stub.puts)
	}
}

// TestFetchWechatTitleTikhub 直连只拿到验证页时走 TikHub 文章详情取标题；同一链接进程内只查一次。
func TestFetchWechatTitleTikhub(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if r.URL.Path != tikhubArticleDetailPath || !strings.Contains(body["url"].(string), "mid=2652931174") || r.Header.Get("Authorization") != "Bearer ak_test" {
			t.Fatalf("path=%s body=%s auth=%q", r.URL.Path, raw, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"url":"x","content":{"title":"新加坡TWG，如何打造出全球最大的奢侈品茶叶连锁？","nick_name":"FBIF食品饮料创新"}}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TIKHUB_API_BASE_URL", srv.URL)
	t.Setenv("TIKHUB_API_KEY", "ak_test")
	u := "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=2652931174&idx=1&sn=e2949c&scene=21#wechat_redirect"
	title, err := fetchWechatTitleTikhub(context.Background(), u)
	if err != nil || title != "新加坡TWG，如何打造出全球最大的奢侈品茶叶连锁？" {
		t.Fatalf("title=%q err=%v", title, err)
	}

	key, err := normalizeSiteSourceKey(u)
	if err != nil {
		t.Fatal(err)
	}
	wechatTitleCache.Store(key, "缓存里的标题")
	t.Cleanup(func() { wechatTitleCache.Delete(key) })
	if got, err := defaultFetchWechatTitle(context.Background(), u); err != nil || got != "缓存里的标题" || calls != 1 {
		t.Fatalf("命中缓存不应再请求：got=%q err=%v calls=%d", got, err, calls)
	}
}
