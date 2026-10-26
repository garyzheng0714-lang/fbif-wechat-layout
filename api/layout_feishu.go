package main

// layout_feishu.go — 排版接口导飞书文档。
//
// 用「排版工具」登录应用（FEISHU_LOGIN_APP_ID/SECRET）自己的应用身份（tenant_access_token）导出，不借任何人的授权，
// 所以文档要先把这个应用加进来（文档右上角「…」→「更多」→「添加文档应用」）。导出、拆图、图片转存都是编辑器
// 那一套（authService.prepareFeishuDocumentExport）；排队用 feishuExportJobManager，但接口有自己的一份
// （同时 2 篇、最多 16 条、结果合计 64 MB），编辑器同事的导出额度不受影响。
// 导出时已经拆好图、转存到 OSS，结果直接当 docx 载体交给排版程序，不再拆一遍。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// layoutFeishuAppName 飞书里搜得到的应用名，说明书和 feishu_not_shared 的改法里都用它。
const layoutFeishuAppName = "排版工具"

const (
	layoutFeishuJobTTL   = time.Minute // 失败的导出留一分钟就清；成功的取走结果立即清
	layoutFeishuTokenTTL = 15 * time.Second
)

type layoutFeishuDoc struct {
	Title          string
	FileName       string
	Docx           []byte
	ImagesMirrored int
}

type layoutFeishu struct {
	jobs    *feishuExportJobManager
	token   func(ctx context.Context) (string, error)
	prepare func(ctx context.Context, accessToken string, ref feishuDocumentRef, requestID string) (preparedFeishuDocument, error)
}

// newLayoutFeishuFromEnv 登录应用没配齐就返回 nil，飞书链接一律 feishu_unavailable。
func newLayoutFeishuFromEnv(auth *authService) *layoutFeishu {
	if auth == nil || auth.config.FeishuAppID == "" || auth.config.FeishuSecret == "" {
		return nil
	}
	app := &feishuNotifier{
		baseURL:    defaultFeishuBaseURL,
		appID:      auth.config.FeishuAppID,
		appSecret:  auth.config.FeishuSecret,
		httpClient: &http.Client{Timeout: layoutFeishuTokenTTL},
	}
	feishu := newLayoutFeishu(app.tenantAccessToken, auth.prepareFeishuDocumentExport)
	feishu.jobs.startGC(time.Minute)
	return feishu
}

func newLayoutFeishu(
	token func(ctx context.Context) (string, error),
	prepare func(ctx context.Context, accessToken string, ref feishuDocumentRef, requestID string) (preparedFeishuDocument, error),
) *layoutFeishu {
	jobs := newFeishuExportJobManager(nil)
	jobs.slots = make(chan struct{}, layoutFeishuConcurrent)
	jobs.maxJobs = layoutFeishuMaxJobs
	jobs.maxResultBytes = layoutFeishuResultBytes
	jobs.ttl = layoutFeishuJobTTL
	jobs.consumedTTL = 0
	return &layoutFeishu{jobs: jobs, token: token, prepare: prepare}
}

// layoutFeishuOutcome 导出任务里真正出的错。任务管理器对外只留通用错误码，这里留原错误好分清「没加应用」还是「太大」。
type layoutFeishuOutcome struct {
	mu        sync.Mutex
	tokenErr  error
	exportErr error
	size      int
}

func (o *layoutFeishuOutcome) set(tokenErr, exportErr error, size int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.tokenErr, o.exportErr, o.size = tokenErr, exportErr, size
}

func (o *layoutFeishuOutcome) get() (size int, tokenErr, exportErr error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.size, o.tokenErr, o.exportErr
}

// export 导出一篇，等到导完或 ctx 到点。每篇用自己的任务主人（layout-api:操作编号），不会顶掉别的导出。
func (f *layoutFeishu) export(ctx context.Context, opID, requestID string, ref feishuDocumentRef) (layoutFeishuDoc, *layoutError) {
	owner := "layout-api:" + opID
	outcome := &layoutFeishuOutcome{}
	job, err := f.jobs.create(owner, func(jobCtx context.Context, _ feishuExportProgressReporter) (preparedFeishuDocument, error) {
		token, err := f.token(jobCtx)
		if err != nil {
			outcome.set(err, nil, 0)
			return preparedFeishuDocument{}, err
		}
		doc, err := f.prepare(jobCtx, token, ref, requestID)
		outcome.set(nil, err, len(doc.Content))
		return doc, err
	})
	if err != nil {
		return layoutFeishuDoc{}, newLayoutError(layoutErrFeishuUnavailable, "同时在导的飞书文档太多").
			withCause(err).withRetryAfter(5 * time.Second)
	}
	for {
		status, changed, ok := f.jobs.status(owner, job.id)
		if !ok {
			return layoutFeishuDoc{}, newLayoutError(layoutErrFeishuUnavailable, "飞书导出任务不见了")
		}
		switch status.State {
		case "completed":
			return f.collect(owner, job.id)
		case "failed", "canceled":
			size, tokenErr, exportErr := outcome.get()
			return layoutFeishuDoc{}, classifyLayoutFeishuError(tokenErr, exportErr, size, status.Code)
		}
		select {
		case <-changed:
		case <-ctx.Done():
			f.jobs.cancel(owner, job.id)
			return layoutFeishuDoc{}, layoutErr(layoutErrTimeout).withCause(ctx.Err())
		}
	}
}

func (f *layoutFeishu) collect(owner, jobID string) (layoutFeishuDoc, *layoutError) {
	_, result, ok := f.jobs.result(owner, jobID)
	f.jobs.markResultConsumed(owner, jobID)
	if !ok || result == nil {
		return layoutFeishuDoc{}, newLayoutError(layoutErrFeishuUnavailable, "飞书导出结果不见了")
	}
	if result.ImagesDeferred {
		// 拆出了图但没转存成：编辑器那边还能退回浏览器逐张取，接口不行，排版程序碰不到本地图。
		return layoutFeishuDoc{}, layoutErr(layoutErrImageMirrorFailed).withCause(errors.New("飞书文档的图片没转存成"))
	}
	if lerr := checkLayoutDocxCarrier(result.Content); lerr != nil {
		return layoutFeishuDoc{}, lerr
	}
	fileName := strings.TrimSpace(result.Filename)
	title := strings.TrimSuffix(fileName, path.Ext(fileName))
	return layoutFeishuDoc{Title: title, FileName: fileName, Docx: result.Content, ImagesMirrored: result.ImageCount}, nil
}

var layoutFeishuHTTPStatus = regexp.MustCompile(`feishu API status (\d{3})`)

// classifyLayoutFeishuError 应用身份读文档，被拒、找不到基本都是没加应用（也可能删了、链接错），统一说 feishu_not_shared。
func classifyLayoutFeishuError(tokenErr, exportErr error, size int, jobCode string) *layoutError {
	if tokenErr != nil {
		return newLayoutError(layoutErrFeishuUnavailable, "排版工具的飞书应用拿不到访问凭证").withCause(tokenErr)
	}
	if exportErr == nil {
		// 导出成功但任务管理器没收下：单篇超过结果上限就是太大，否则是同时导的结果太多。
		if size > layoutFeishuResultBytes {
			return newLayoutError(layoutErrFeishuTooLarge,
				fmt.Sprintf("飞书导出的 Word 有 %d MB", size>>20))
		}
		return newLayoutError(layoutErrFeishuUnavailable, "飞书导出结果暂时存不下（"+jobCode+"）").withRetryAfter(5 * time.Second)
	}
	var taskErr *feishuExportTaskError
	var wikiErr *feishuWikiAccessError
	switch {
	case errors.Is(exportErr, context.DeadlineExceeded), errors.Is(exportErr, errFeishuExportTimeout):
		return layoutErr(layoutErrTimeout).withCause(exportErr)
	case errors.Is(exportErr, context.Canceled):
		return layoutErr(layoutErrTimeout).withCause(exportErr)
	case errors.Is(exportErr, errFeishuWikiNotDocument):
		return layoutErr(layoutErrFeishuNotDocument).withCause(exportErr)
	case errors.As(exportErr, &wikiErr):
		return layoutErr(layoutErrFeishuNotShared).withCause(exportErr)
	case errors.As(exportErr, &taskErr):
		switch taskErr.JobStatus {
		case 107, 6000:
			return layoutErr(layoutErrFeishuTooLarge).withCause(exportErr)
		case 109, 110, 111, 122, 123:
			return layoutErr(layoutErrFeishuNotShared).withCause(exportErr)
		case 108:
			return layoutErr(layoutErrTimeout).withCause(exportErr)
		}
	}
	if match := layoutFeishuHTTPStatus.FindStringSubmatch(exportErr.Error()); match != nil {
		status, _ := strconv.Atoi(match[1])
		if status == http.StatusBadRequest || status == http.StatusForbidden || status == http.StatusNotFound {
			return layoutErr(layoutErrFeishuNotShared).withCause(exportErr)
		}
	}
	return layoutErr(layoutErrFeishuUnavailable).withCause(exportErr)
}
