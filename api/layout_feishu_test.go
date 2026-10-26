package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

const layoutTestFeishuURL = "https://abc.feishu.cn/docx/AbCdEfGhIjKlMnOpQrSt"

func layoutFeishuReturning(doc preparedFeishuDocument, err error) *layoutFeishu {
	return newLayoutFeishu(
		func(context.Context) (string, error) { return "t-tenant", nil },
		func(context.Context, string, feishuDocumentRef, string) (preparedFeishuDocument, error) {
			return doc, err
		},
	)
}

func TestLayoutFeishuDocBecomesDocxCarrier(t *testing.T) {
	env := newLayoutTestEnv(t)
	docx := layoutTestDocx(t, false)
	var gotToken string
	var gotRef feishuDocumentRef
	env.api.feishu = newLayoutFeishu(
		func(context.Context) (string, error) { return "t-tenant", nil },
		func(_ context.Context, token string, ref feishuDocumentRef, _ string) (preparedFeishuDocument, error) {
			gotToken, gotRef = token, ref
			return preparedFeishuDocument{Filename: "飞书里的稿子.docx", Content: docx, ImageCount: 3}, nil
		},
	)
	result := decodeLayoutResultBody(t, env.postJSON(env.keys.active, map[string]any{"account": "fbif", "url": layoutTestFeishuURL}))
	if gotToken != "t-tenant" || gotRef.Token != "AbCdEfGhIjKlMnOpQrSt" || gotRef.Type != "docx" {
		t.Fatalf("用应用身份导出这篇：%q %+v", gotToken, gotRef)
	}
	req := env.node.lastRequest(t)
	decoded, _ := base64.StdEncoding.DecodeString(req.Input.DocxBase64)
	if req.Input.Kind != layoutCarrierDocx || string(decoded) != string(docx) || req.Input.Title != "飞书里的稿子" || req.Input.FileName != "飞书里的稿子.docx" {
		t.Fatalf("导出结果直接当 docx 载体，不再拆一遍：%+v", req.Input.Kind)
	}
	if result.Input != (layoutInputJSON{Kind: "feishu_doc", URL: layoutTestFeishuURL}) || result.Stats.ImagesMirrored != 3 || req.Repost != nil {
		t.Fatalf("%+v %+v", result.Input, result.Stats)
	}
}

// TestLayoutFeishuUsesEditorExportPipeline 走编辑器同一个 prepareFeishuDocumentExport：导出、拆图、转存 OSS。
func TestLayoutFeishuUsesEditorExportPipeline(t *testing.T) {
	env := newLayoutTestEnv(t)
	startLayoutUploadServer(t, http.StatusOK)
	provider := &fakeFeishuProvider{exportResult: feishuExportedFile{Filename: "带图的稿子.docx", Content: layoutTestDocx(t, true)}}
	auth := newTestAuthService(t, provider)
	env.api.feishu = newLayoutFeishu(func(context.Context) (string, error) { return "t-tenant", nil }, auth.prepareFeishuDocumentExport)
	env.node.html = layoutNodeEchoDocxImages(t)
	result := decodeLayoutResultBody(t, env.postJSON(env.keys.active, map[string]any{"account": "ft-default", "url": layoutTestFeishuURL}))
	decoded, _ := base64.StdEncoding.DecodeString(env.node.lastRequest(t).Input.DocxBase64)
	if layoutDocxHasMedia(decoded) || provider.exportCalls != 1 {
		t.Fatalf("交给排版程序的 docx 不带图：%d", provider.exportCalls)
	}
	if result.Stats.ImagesMirrored != 1 || len(result.Images) != 1 || result.Images[0].Host != layoutImageHostOwn {
		t.Fatalf("飞书文档里的图转存到 OSS：%+v %+v", result.Stats, result.Images)
	}
	// 编辑器的那份导出额度没被动过。
	if len(auth.feishuExportJobs.jobs) != 0 || env.api.feishu.jobs == auth.feishuExportJobs {
		t.Fatal("接口用自己的导出任务管理器")
	}
	if cap(env.api.feishu.jobs.slots) != layoutFeishuConcurrent || env.api.feishu.jobs.maxJobs != layoutFeishuMaxJobs ||
		env.api.feishu.jobs.maxResultBytes != layoutFeishuResultBytes {
		t.Fatal("接口的导出额度：同时 2 篇、最多 16 条、结果 64 MB")
	}
}

func TestLayoutFeishuErrorsAreClassified(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   layoutErrorCode
	}{
		{"没加应用（任务 109）", &feishuExportTaskError{JobStatus: 109}, 403, layoutErrFeishuNotShared},
		{"文档删了（任务 123）", &feishuExportTaskError{JobStatus: 123}, 403, layoutErrFeishuNotShared},
		{"建任务被拒 403", errors.New("feishu API status 403 (code=1069902)"), 403, layoutErrFeishuNotShared},
		{"建任务 400", errors.New("feishu API status 400 (code=99991672)"), 403, layoutErrFeishuNotShared},
		{"知识库看不到", &feishuWikiAccessError{code: 131006, msg: "x"}, 403, layoutErrFeishuNotShared},
		{"不是文档", errFeishuWikiNotDocument, 400, layoutErrFeishuNotDocument},
		{"太大", &feishuExportTaskError{JobStatus: 6000}, 413, layoutErrFeishuTooLarge},
		{"飞书导出超时", errFeishuExportTimeout, 504, layoutErrTimeout},
		{"飞书 500", errors.New("feishu API status 500"), 502, layoutErrFeishuUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newLayoutTestEnv(t)
			env.api.feishu = layoutFeishuReturning(preparedFeishuDocument{}, c.err)
			expectLayoutError(t, env.postJSON(env.keys.active, map[string]any{"account": "fbif", "url": layoutTestFeishuURL}), c.status, c.code)
		})
	}
	env := newLayoutTestEnv(t)
	env.api.feishu = newLayoutFeishu(
		func(context.Context) (string, error) { return "", errors.New("app secret invalid") },
		func(context.Context, string, feishuDocumentRef, string) (preparedFeishuDocument, error) {
			t.Fatal("拿不到凭证就别导")
			return preparedFeishuDocument{}, nil
		},
	)
	expectLayoutError(t, env.postJSON(env.keys.active, map[string]any{"account": "fbif", "url": layoutTestFeishuURL}), 502, layoutErrFeishuUnavailable)
}

func TestLayoutFeishuImagesMustBeMirrored(t *testing.T) {
	env := newLayoutTestEnv(t)
	env.api.feishu = layoutFeishuReturning(preparedFeishuDocument{Filename: "a.docx", Content: layoutTestDocx(t, false), ImagesDeferred: true}, nil)
	expectLayoutError(t, env.postJSON(env.keys.active, map[string]any{"account": "fbif", "url": layoutTestFeishuURL}), 502, layoutErrImageMirrorFailed)
	waitLayoutIdle(t, env.api, "c1")
	// 拆图失败时编辑器那边退回整份 docx（word/media 还在）：接口不交给排版程序。
	env.api.feishu = layoutFeishuReturning(preparedFeishuDocument{Filename: "a.docx", Content: layoutTestDocx(t, true)}, nil)
	expectLayoutError(t, env.postJSON(env.keys.active, map[string]any{"account": "fbif", "url": layoutTestFeishuURL}), 422, layoutErrDocxImagesUnreadable)
	if env.node.count() != 0 {
		t.Fatal("带图的 docx 不交给排版程序")
	}
	feishuShared := layoutErrorSpecByCode[layoutErrFeishuNotShared]
	if !strings.Contains(feishuShared.Fix, "添加文档应用") || !strings.Contains(feishuShared.Fix, layoutFeishuAppName) {
		t.Fatalf("没加应用的改法要写清在哪加、加哪个：%s", feishuShared.Fix)
	}
}

func TestLayoutFeishuNotConfigured(t *testing.T) {
	env := newLayoutTestEnv(t)
	expectLayoutError(t, env.postJSON(env.keys.active, map[string]any{"account": "fbif", "url": layoutTestFeishuURL}), 502, layoutErrFeishuUnavailable)
	if newLayoutFeishuFromEnv(nil) != nil {
		t.Fatal("没有登录服务就不接飞书")
	}
	auth := newTestAuthService(t, &fakeFeishuProvider{})
	if feishu := newLayoutFeishuFromEnv(auth); feishu == nil || feishu.token == nil {
		t.Fatal("登录应用配齐了就接上")
	}
	auth.config.FeishuSecret = ""
	if newLayoutFeishuFromEnv(auth) != nil {
		t.Fatal("缺密钥不接")
	}
}

func TestLayoutFeishuExportStopsAtDeadline(t *testing.T) {
	started := make(chan struct{})
	feishu := newLayoutFeishu(
		func(context.Context) (string, error) { return "t", nil },
		func(ctx context.Context, _ string, _ feishuDocumentRef, _ string) (preparedFeishuDocument, error) {
			close(started)
			<-ctx.Done()
			return preparedFeishuDocument{}, ctx.Err()
		},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ref, _ := parseFeishuDocumentURL(layoutTestFeishuURL)
	_, lerr := feishu.export(ctx, "op_test", "lr_test", ref)
	<-started
	if lerr == nil || lerr.Code != layoutErrTimeout {
		t.Fatalf("到点就停：%v", lerr)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		status, _, ok := feishu.jobs.status("layout-api:op_test", firstFeishuJobID(feishu))
		if ok && status.State == "canceled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("飞书导出任务也要取消：%+v", status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func firstFeishuJobID(feishu *layoutFeishu) string {
	feishu.jobs.mu.Lock()
	defer feishu.jobs.mu.Unlock()
	for id := range feishu.jobs.jobs {
		return id
	}
	return ""
}

func TestLayoutFeishuFinishedJobsAreCleared(t *testing.T) {
	feishu := layoutFeishuReturning(preparedFeishuDocument{Filename: "a.docx", Content: layoutTestDocx(t, false)}, nil)
	ref, _ := parseFeishuDocumentURL(layoutTestFeishuURL)
	for i := 0; i < layoutFeishuMaxJobs+4; i++ {
		if _, lerr := feishu.export(context.Background(), fmt.Sprintf("op_%d", i), "lr", ref); lerr != nil {
			t.Fatalf("第 %d 篇：%v", i, lerr)
		}
	}
	feishu.jobs.mu.Lock()
	defer feishu.jobs.mu.Unlock()
	if feishu.jobs.resultBytes > int64(len(layoutTestDocx(t, false))) {
		t.Fatalf("取走的结果要清掉，不占额度：%d", feishu.jobs.resultBytes)
	}
}
