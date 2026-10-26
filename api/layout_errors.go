package main

// layout_errors.go — 排版接口（/api/layout/v1/*）对外的错误码只来自 layoutErrorSpecs 这一张表。
//
// 处理链只能用 newLayoutError 造错误，code 不在表里直接 panic（测试会走到）；说明书「出错」一节、
// OpenAPI 的错误码枚举都从这张表生成。message 写「出了什么事」，fix 写「你该怎么办」，
// 调用方的 AI 助手按 code 分支、把 fix 原话转告给人。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type layoutErrorCode string

const (
	layoutErrUnauthorized          layoutErrorCode = "unauthorized"
	layoutErrKeyDisabled           layoutErrorCode = "key_disabled"
	layoutErrAuthUnavailable       layoutErrorCode = "auth_unavailable"
	layoutErrServiceRestarting     layoutErrorCode = "service_restarting"
	layoutErrBadRequest            layoutErrorCode = "bad_request"
	layoutErrRequestTooLarge       layoutErrorCode = "request_too_large"
	layoutErrUnknownAccount        layoutErrorCode = "unknown_account"
	layoutErrTooManyInputs         layoutErrorCode = "too_many_inputs"
	layoutErrInvalidURL            layoutErrorCode = "invalid_url"
	layoutErrUnsupportedFile       layoutErrorCode = "unsupported_file"
	layoutErrFileTooLarge          layoutErrorCode = "file_too_large"
	layoutErrRateLimited           layoutErrorCode = "rate_limited"
	layoutErrBusy                  layoutErrorCode = "busy"
	layoutErrOperationNotFound     layoutErrorCode = "operation_not_found"
	layoutErrOperationLost         layoutErrorCode = "operation_lost"
	layoutErrOperationExpired      layoutErrorCode = "operation_expired"
	layoutErrFetchFailed           layoutErrorCode = "fetch_failed"
	layoutErrWechatVerifyPage      layoutErrorCode = "wechat_verify_page"
	layoutErrPageHasNoBody         layoutErrorCode = "page_has_no_body"
	layoutErrPageTooLarge          layoutErrorCode = "page_too_large"
	layoutErrFeishuNotShared       layoutErrorCode = "feishu_not_shared"
	layoutErrFeishuNotDocument     layoutErrorCode = "feishu_not_document"
	layoutErrFeishuTooLarge        layoutErrorCode = "feishu_too_large"
	layoutErrFeishuUnavailable     layoutErrorCode = "feishu_unavailable"
	layoutErrDocConvertUnavailable layoutErrorCode = "doc_convert_unavailable"
	layoutErrDocConvertFailed      layoutErrorCode = "doc_convert_failed"
	layoutErrDocxImagesUnreadable  layoutErrorCode = "docx_images_unreadable"
	layoutErrWordUnreadable        layoutErrorCode = "word_unreadable"
	layoutErrEmptyArticle          layoutErrorCode = "empty_article"
	layoutErrImageMirrorFailed     layoutErrorCode = "image_mirror_failed"
	layoutErrImageNotAllowed       layoutErrorCode = "image_not_allowed"
	layoutErrRulesUnavailable      layoutErrorCode = "rules_unavailable"
	layoutErrRenderUnavailable     layoutErrorCode = "render_unavailable"
	layoutErrRenderFailed          layoutErrorCode = "render_failed"
	layoutErrTimeout               layoutErrorCode = "timeout"
)

// layoutErrorSpec 一种错误：HTTP 状态、原样重发有没有用、建议等多久、默认说明、给人看的改法。
type layoutErrorSpec struct {
	Code       layoutErrorCode
	Status     int
	Retryable  bool
	RetryAfter time.Duration
	Message    string
	Fix        string
}

// layoutErrorSpecs 说明书「出错」一节就是这张表，按调用方遇到的先后排。
var layoutErrorSpecs = []layoutErrorSpec{
	{layoutErrUnauthorized, http.StatusUnauthorized, false, 0,
		"没带钥匙，或钥匙不对",
		"检查请求头 Authorization: Bearer flk_…，钥匙要整串复制，前后不能有空格。没有钥匙找排版工具管理员要一把"},
	{layoutErrKeyDisabled, http.StatusForbidden, false, 0,
		"这把钥匙已经停用",
		"找排版工具管理员问清楚，需要的话再发一把新的"},
	{layoutErrAuthUnavailable, http.StatusServiceUnavailable, true, 30 * time.Second,
		"排版工具暂时核对不了钥匙（读不到 FBIF 中台的调用方名单）",
		"等 retry_after_seconds 秒再发；超过 10 分钟还这样，把 request_id 发给排版工具维护者"},
	{layoutErrServiceRestarting, http.StatusServiceUnavailable, true, 30 * time.Second,
		"排版工具正在更新上线，这段时间不接新稿",
		"等 retry_after_seconds 秒再发，一般一两分钟就好"},
	{layoutErrBadRequest, http.StatusBadRequest, false, 0,
		"请求读不懂",
		"照说明书「请求」一节改：JSON 用 Content-Type: application/json，Word 用 multipart/form-data；message 里写了具体哪一项不对"},
	{layoutErrRequestTooLarge, http.StatusRequestEntityTooLarge, false, 0,
		"请求体太大",
		"JSON 请求体不能超过说明书「限额」里的大小；正文太长就拆成几篇，或改用 Word 上传"},
	{layoutErrUnknownAccount, http.StatusBadRequest, false, 0,
		"account 不认识",
		"account 只能是说明书「账号」表里的 id，大小写要一致"},
	{layoutErrTooManyInputs, http.StatusBadRequest, false, 0,
		"url、html、markdown、text、file 只能给一个",
		"一次只交一样内容；同一篇有链接就只给 url"},
	{layoutErrInvalidURL, http.StatusBadRequest, false, 0,
		"url 不是能公开打开的 http(s) 链接",
		"换成浏览器里能直接打开的公开链接；内网地址、带账号密码的链接都不收"},
	{layoutErrUnsupportedFile, http.StatusUnsupportedMediaType, false, 0,
		"文件不是 Word（.docx 或 .doc）",
		"用 Word 打开后另存为 .docx 再传；PDF、WPS、表格、PPT 都不收"},
	{layoutErrFileTooLarge, http.StatusRequestEntityTooLarge, false, 0,
		"文件太大",
		"上传的 Word 不能超过说明书「限额」里的大小；去掉图片后还太大，多半是文档里嵌了附件、视频或别的对象，删掉嵌入的附件、对象后重传"},
	{layoutErrRateLimited, http.StatusTooManyRequests, true, 0,
		"发得太快，超过了每分钟的次数",
		"等 retry_after_seconds 秒再发；批量排版请排队一篇一篇发"},
	{layoutErrBusy, http.StatusServiceUnavailable, true, 5 * time.Second,
		"同时在排的稿子太多",
		"等手上的稿子排完（或等 retry_after_seconds 秒）再发"},
	{layoutErrOperationNotFound, http.StatusNotFound, false, 0,
		"找不到这个排版编号",
		"确认编号是这把钥匙提交时拿到的；拿不准就重新提交"},
	{layoutErrOperationLost, http.StatusNotFound, true, 10 * time.Second,
		"排版工具重启过，这篇在排的稿子丢了",
		"重新提交同一个请求。最多重提 3 次，依次等 10 秒、30 秒、60 秒；3 次都这样就把 request_id 发给排版工具维护者"},
	{layoutErrOperationExpired, http.StatusNotFound, true, 0,
		"排好的结果已经过期清掉了",
		"重新提交同一个请求。排好的结果只留一段时间（见「限额」），拿到 200 后请自己存好"},
	{layoutErrFetchFailed, http.StatusBadGateway, true, 30 * time.Second,
		"原文暂时抓不到（网络不通，或公众号临时限流）",
		"等 retry_after_seconds 秒重发；一直不行就在浏览器打开原文，把正文用 html 或 text 交过来"},
	{layoutErrWechatVerifyPage, http.StatusBadGateway, true, 60 * time.Second,
		"公众号没给正文，回的是「环境异常」验证页（也可能文章已被删除）",
		"过几分钟再发；一直这样就在浏览器打开原文确认还在，再把正文用 html 或 text 交过来"},
	{layoutErrPageHasNoBody, http.StatusUnprocessableEntity, false, 0,
		"这个网页没给出正文，可能开了反爬验证",
		"在浏览器打开原文，把正文复制出来用 html 或 text 交；或者贴进飞书云文档，用飞书链接交"},
	{layoutErrPageTooLarge, http.StatusUnprocessableEntity, false, 0,
		"原网页太大，抓不下来",
		"在浏览器打开原文，把正文复制出来用 html 或 text 交"},
	{layoutErrFeishuNotShared, http.StatusForbidden, false, 0,
		"排版工具读不到这篇飞书文档：文档没加「" + layoutFeishuAppName + "」应用，或文档已删除、链接不对",
		"打开飞书文档，右上角「…」→「更多」→「添加文档应用」，搜「" + layoutFeishuAppName + "」加上（知识库文档也可以把应用加成知识库成员），再重发"},
	{layoutErrFeishuNotDocument, http.StatusBadRequest, false, 0,
		"这个飞书链接不是云文档（可能是表格、多维表格或目录）",
		"只收飞书云文档（docx、知识库里的文档）；表格请先整理成文档"},
	{layoutErrFeishuTooLarge, http.StatusRequestEntityTooLarge, false, 0,
		"文档或图片太多，飞书导不出来",
		"把文档拆成几篇，或删掉一些图片后重发"},
	{layoutErrFeishuUnavailable, http.StatusBadGateway, true, 30 * time.Second,
		"飞书导出失败或超时",
		"等 retry_after_seconds 秒重发；一直不行把 request_id 发给排版工具维护者"},
	{layoutErrDocConvertUnavailable, http.StatusServiceUnavailable, false, 0,
		".doc 转换服务没开",
		"用 Word 打开后另存为 .docx 再传"},
	{layoutErrDocConvertFailed, http.StatusUnprocessableEntity, false, 0,
		".doc 转换失败",
		"用 Word 打开后另存为 .docx 再传"},
	{layoutErrDocxImagesUnreadable, http.StatusUnprocessableEntity, false, 0,
		"Word 里的图片拆不出来（文件结构异常，或图片太大、太多）",
		"用 Word 打开后另存为 .docx 再传；单张图不要超过 15 MB、一篇不要超过 100 张"},
	{layoutErrWordUnreadable, http.StatusUnprocessableEntity, false, 0,
		"Word 文件读不出来",
		"用 Word 打开后另存为 .docx 再传"},
	{layoutErrEmptyArticle, http.StatusUnprocessableEntity, false, 0,
		"交过来的内容里没有正文",
		"检查交的内容：html 要有正文段落，markdown、text 不能只有空行"},
	{layoutErrImageMirrorFailed, http.StatusBadGateway, true, 30 * time.Second,
		"有图片转存失败",
		"等 retry_after_seconds 秒重发；一直失败把 message 里的图片地址和 request_id 发给排版工具维护者"},
	{layoutErrImageNotAllowed, http.StatusUnprocessableEntity, false, 0,
		"排好的稿子里有不能直接用的图片地址",
		"html、markdown 里的图片要写成公开的 https 绝对地址；blob:、data:、相对地址都不收。message 里写了是哪张"},
	{layoutErrRulesUnavailable, http.StatusServiceUnavailable, true, 30 * time.Second,
		"读不到这个账号当前生效的排版规则",
		"等 retry_after_seconds 秒重发"},
	{layoutErrRenderUnavailable, http.StatusServiceUnavailable, true, 30 * time.Second,
		"排版程序没起来",
		"等 retry_after_seconds 秒重发；一直这样把 request_id 发给排版工具维护者"},
	{layoutErrRenderFailed, http.StatusInternalServerError, false, 0,
		"排版时出错",
		"把 request_id 发给排版工具维护者"},
	{layoutErrTimeout, http.StatusGatewayTimeout, true, 30 * time.Second,
		"排版超过了时限还没完",
		"重发一次；飞书长文档、图多的稿子本来就慢，带 wait_seconds 并按 poll_url 取结果"},
}

var layoutErrorSpecByCode = func() map[layoutErrorCode]layoutErrorSpec {
	out := make(map[layoutErrorCode]layoutErrorSpec, len(layoutErrorSpecs))
	for _, spec := range layoutErrorSpecs {
		if _, dup := out[spec.Code]; dup {
			panic("排版接口错误码重复：" + string(spec.Code))
		}
		out[spec.Code] = spec
	}
	return out
}()

// layoutError 处理链里的错误。Message 空就用表里的默认说明；cause 只进日志，不回给调用方。
type layoutError struct {
	Code       layoutErrorCode
	Message    string
	RetryAfter time.Duration
	cause      error
}

func newLayoutError(code layoutErrorCode, message string) *layoutError {
	if _, ok := layoutErrorSpecByCode[code]; !ok {
		panic("排版接口错误码不在 layoutErrorSpecs 表里：" + string(code))
	}
	return &layoutError{Code: code, Message: message}
}

func layoutErr(code layoutErrorCode) *layoutError { return newLayoutError(code, "") }

func (e *layoutError) withCause(err error) *layoutError {
	e.cause = err
	return e
}

func (e *layoutError) withRetryAfter(d time.Duration) *layoutError {
	e.RetryAfter = d
	return e
}

func (e *layoutError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.message(), e.cause)
	}
	return string(e.Code) + ": " + e.message()
}

func (e *layoutError) spec() layoutErrorSpec { return layoutErrorSpecByCode[e.Code] }

func (e *layoutError) message() string {
	if e.Message != "" {
		return e.Message
	}
	return e.spec().Message
}

func (e *layoutError) retryAfter() time.Duration {
	if e.RetryAfter > 0 {
		return e.RetryAfter
	}
	return e.spec().RetryAfter
}

// retryAfterSeconds 往上取整到秒；不建议等待时为 0（JSON 里省掉）。
func (e *layoutError) retryAfterSeconds() int {
	d := e.retryAfter()
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

func (e *layoutError) responseBody(requestID string) layoutErrorJSON {
	spec := e.spec()
	return layoutErrorJSON{
		Error: layoutErrorBodyJSON{
			Code:              string(e.Code),
			Message:           e.message(),
			Fix:               spec.Fix,
			Retryable:         spec.Retryable,
			RetryAfterSeconds: e.retryAfterSeconds(),
		},
		RequestID: requestID,
	}
}

// layoutStoredResponse 一次排版的最终回答：状态码和逐字节的 body。POST 直接回的、GET 轮询取到的都是这一份。
type layoutStoredResponse struct {
	Status     int
	Body       []byte
	RetryAfter int
}

func layoutErrorResponse(requestID string, e *layoutError) layoutStoredResponse {
	return layoutStoredResponse{
		Status:     e.spec().Status,
		Body:       layoutMarshal(e.responseBody(requestID)),
		RetryAfter: e.retryAfterSeconds(),
	}
}

func writeLayoutStored(w http.ResponseWriter, resp layoutStoredResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if resp.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(resp.RetryAfter))
	}
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
}

func writeLayoutError(w http.ResponseWriter, requestID string, e *layoutError) {
	writeLayoutStored(w, layoutErrorResponse(requestID, e))
}

// layoutMarshal 对外 JSON 一律不转义 <>&（html 字段里全是标签），末尾带换行。
func layoutMarshal(v any) []byte {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		panic("排版接口返回值序列化失败：" + err.Error())
	}
	return buf.Bytes()
}
