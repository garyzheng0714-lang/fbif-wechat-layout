package main

// 凭证不进日志契约（2026-09-28 事故：FoodTalks 401 响应 data 原样回显所传 token，
// 「响应片段」把正式 token 写进 journald 每天约 2.5 万次）：
// 任何 FoodTalks 错误信息都不得含 token 原文或其片段；401/JSON 403 一律归
// errFoodTalksUnauthorized，给运维「换 FOODTALKS_TOKEN」的明确指路。

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const redactionProbeToken = "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"

func foodTalksRedactionClient(t *testing.T, status int, body string) *foodTalksClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FOODTALKS_BASE_URL", srv.URL)
	t.Setenv("FOODTALKS_TOKEN", redactionProbeToken)
	t.Setenv("FOODTALKS_ORIGIN", "")
	cli, err := newFoodTalksClientFromEnv()
	if err != nil {
		t.Fatalf("newFoodTalksClientFromEnv: %v", err)
	}
	return cli
}

// foodTalksRedactionCalls 覆盖所有会把响应片段写进错误的客户端入口。
var foodTalksRedactionCalls = map[string]func(context.Context, *foodTalksClient) error{
	"CreateNews": func(ctx context.Context, c *foodTalksClient) error {
		_, err := c.CreateNews(ctx, foodTalksNews{Title: "t"})
		return err
	},
	"ListTagsByType": func(ctx context.Context, c *foodTalksClient) error {
		_, err := c.ListTagsByType(ctx, 6, "")
		return err
	},
	"CreateTagByName": func(ctx context.Context, c *foodTalksClient) error {
		_, err := c.CreateTagByName(ctx, 10, "来源")
		return err
	},
	"GetNews": func(ctx context.Context, c *foodTalksClient) error {
		_, err := c.GetNews(ctx, 1)
		return err
	},
	"SearchNewsByTitle": func(ctx context.Context, c *foodTalksClient) error {
		_, err := c.SearchNewsByTitle(ctx, "标题")
		return err
	},
	"SearchCompanies": func(ctx context.Context, c *foodTalksClient) error {
		_, err := c.SearchCompanies(ctx, "公司")
		return err
	},
	"PreviewToken": func(ctx context.Context, c *foodTalksClient) error {
		_, err := c.PreviewToken(ctx, 1)
		return err
	},
}

func assertNoTokenFragment(t *testing.T, msg string) {
	t.Helper()
	// 任意 8 字符以上的 token 片段都算泄露（防截断切在 token 中间漏出前半截）。
	for i := 0; i+8 <= len(redactionProbeToken); i++ {
		if frag := redactionProbeToken[i : i+8]; strings.Contains(msg, frag) {
			t.Fatalf("错误信息泄露 token 片段 %q：%s", frag, msg)
		}
	}
}

func TestFoodTalksUnauthorizedEchoNeverLeaksToken(t *testing.T) {
	// 生产实测形态：{"code":401,"msg":"error","data":"<所传 token>"}
	body := `{"code":401,"msg":"error","data":"` + redactionProbeToken + `"}`
	for name, call := range foodTalksRedactionCalls {
		t.Run(name, func(t *testing.T) {
			err := call(context.Background(), foodTalksRedactionClient(t, http.StatusUnauthorized, body))
			if err == nil {
				t.Fatal("401 必须返回错误")
			}
			if !errors.Is(err, errFoodTalksUnauthorized) {
				t.Fatalf("401 必须归为凭证失效，得到：%v", err)
			}
			assertNoTokenFragment(t, err.Error())
		})
	}
}

func TestFoodTalksJSON403IsUnauthorizedEverywhere(t *testing.T) {
	body := `{"code":403,"msg":"forbidden","data":"` + redactionProbeToken + `"}`
	for name, call := range foodTalksRedactionCalls {
		t.Run(name, func(t *testing.T) {
			err := call(context.Background(), foodTalksRedactionClient(t, http.StatusForbidden, body))
			if !errors.Is(err, errFoodTalksUnauthorized) {
				t.Fatalf("JSON 403 必须归为凭证失效，得到：%v", err)
			}
			assertNoTokenFragment(t, err.Error())
		})
	}
}

func TestFoodTalksOtherErrorSnippetsRedactToken(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"500 回显":      {http.StatusInternalServerError, `{"code":500,"data":"Bearer ` + redactionProbeToken + `"}`},
		"200 非 JSON":  {http.StatusOK, `<html>token=` + redactionProbeToken + `</html>`},
		"token 跨截断边界": {http.StatusBadGateway, strings.Repeat("x", 190) + redactionProbeToken + strings.Repeat("y", 50)},
	}
	for caseName, tc := range cases {
		for name, call := range foodTalksRedactionCalls {
			t.Run(caseName+"/"+name, func(t *testing.T) {
				err := call(context.Background(), foodTalksRedactionClient(t, tc.status, tc.body))
				if err == nil {
					t.Fatal("异常响应必须返回错误")
				}
				assertNoTokenFragment(t, err.Error())
			})
		}
	}
}

func TestFoodTalksSnippetEmptyTokenKeepsBody(t *testing.T) {
	c := &foodTalksClient{}
	if got := c.snippet([]byte("  plain body  ")); got != "plain body" {
		t.Fatalf("空 token 不得改写响应片段，得到 %q", got)
	}
}
