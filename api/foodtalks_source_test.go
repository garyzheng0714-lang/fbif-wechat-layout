package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseFoodTalksTagList(t *testing.T) {
	arrayBody := []byte(`{"code":0,"msg":"success","data":[{"id":8801,"name":"窄播","tagTypeId":10,"sort":1}]}`)
	tags, err := parseFoodTalksTagList(arrayBody)
	if err != nil || len(tags) != 1 || tags[0].ID != 8801 || tags[0].Name != "窄播" {
		t.Fatalf("数组 data 解析失败：%v %+v", err, tags)
	}

	recordsBody := []byte(`{"code":0,"msg":"success","data":{"records":[{"id":12,"name":"FBIF食品饮料创新"}]}}`)
	tags, err = parseFoodTalksTagList(recordsBody)
	if err != nil || len(tags) != 1 || tags[0].ID != 12 {
		t.Fatalf("records 包一层也应能解析：%v %+v", err, tags)
	}

	emptyBody := []byte(`{"code":0,"msg":"success","data":null}`)
	tags, err = parseFoodTalksTagList(emptyBody)
	if err != nil || len(tags) != 0 {
		t.Fatalf("null data 应得到空列表：%v %+v", err, tags)
	}
}

func TestMatchFoodTalksSourceID(t *testing.T) {
	tags := []foodTalksTag{
		{ID: 8801, Name: "窄播"},
		{ID: 12, Name: "FBIF食品饮料创新"},
	}
	if got := matchFoodTalksSourceID(tags, "窄播"); got != 8801 {
		t.Fatalf("精确匹配：got %d", got)
	}
	if got := matchFoodTalksSourceID(tags, " FBIF 食品饮料创新 "); got != 12 {
		t.Fatalf("去空格匹配：got %d", got)
	}
	if got := matchFoodTalksSourceID(tags, "未知来源"); got != 0 {
		t.Fatalf("未命中必须返回 0，got %d", got)
	}
}

func TestListTagsByType(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "msg": "success",
			"data": map[string]any{
				"records": []map[string]any{{"id": 8801, "name": "窄播"}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FOODTALKS_BASE_URL", srv.URL)
	t.Setenv("FOODTALKS_TOKEN", "tok")
	t.Setenv("FOODTALKS_ORIGIN", "https://layout.test")
	cli, err := newFoodTalksClientFromEnv()
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	tags, err := cli.ListTagsByType(context.Background(), foodTalksTagTypeArticleSource, "窄播")
	if err != nil {
		t.Fatalf("ListTagsByType: %v", err)
	}
	if gotPath != "/basic/tag/page" {
		t.Fatalf("路径=%q，应为 /basic/tag/page", gotPath)
	}
	if !strings.Contains(gotQuery, "type=10") || !strings.Contains(gotQuery, "keywords=") {
		t.Fatalf("查询参数=%q，应含 type=10 与 keywords", gotQuery)
	}
	if len(tags) != 1 || tags[0].ID != 8801 || tags[0].Name != "窄播" {
		t.Fatalf("标签列表：%+v", tags)
	}
}

func TestCreateTagByName(t *testing.T) {
	var gotPath string
	var gotBody foodTalksCreateTagBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code":0,"msg":"success","data":115677}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FOODTALKS_BASE_URL", srv.URL)
	t.Setenv("FOODTALKS_TOKEN", "tok")
	t.Setenv("FOODTALKS_ORIGIN", "https://layout.test")
	cli, err := newFoodTalksClientFromEnv()
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	id, err := cli.CreateTagByName(context.Background(), foodTalksTagTypeArticleSource, " 凤凰网《风暴眼》 ")
	if err != nil {
		t.Fatalf("CreateTagByName: %v", err)
	}
	if gotPath != "/basic/admin/tag/by-name" {
		t.Fatalf("路径=%q", gotPath)
	}
	if gotBody.Name != "凤凰网《风暴眼》" || gotBody.TypeID != 10 {
		t.Fatalf("请求体=%+v", gotBody)
	}
	if id != 115677 {
		t.Fatalf("id=%d", id)
	}
}
