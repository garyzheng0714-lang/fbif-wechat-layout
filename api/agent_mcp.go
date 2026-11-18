package main

// agent_mcp.go — 智能体入口（MCP）：同事在 Codex、Claude Code 这类智能体里连上排版工具，让它替自己排版、校对，
// 排好给一个链接，人在排版工具编辑器里打开接着改、复制到后台。只排版和校对，不碰任何平台（不存草稿、不发布）。
//
//   - 登录：智能体先走 MCP 授权（internal/mcpauth，本服务自己当授权服务器），授权页认人走统一登录，
//     只放 FBIF 飞书登录的同事；每次调用、每次换令牌都核一遍还在不在、有没有被停用（agentAllowed）。
//   - 排版：和排版接口（红线 46）同一条路 layoutAPI.launch，成品与网页「导入→复制」逐字一样。
//   - 校对：和网页「AI 校对」同一个 articleChecker，送的标题和正文与编辑器里那张卡逐字一样，同一篇只查一次。
//   - 交接：每篇记下这次交的是什么（链接、Markdown、文字、Word 原件），编辑器打开 /<渠道>/<账号>?agent=<编号>
//     时按同一份输入重新导入（public/js/agent-handoff.js），和人自己粘贴、上传一模一样；记录留 7 天。

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"fbif-wechat-layout/api/internal/mcpauth"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// agentAuthorizePath 授权页：人在浏览器里点「允许」，登录接线（auth_login.go identifiedPath）要在这里认人。
const agentAuthorizePath = "/oauth/authorize"

const (
	// agentToolWait 一次工具调用最多等多久：Codex 默认一次调用 60 秒超时，留出余量，没完就给编号让它再来取。
	agentToolWait = 40 * time.Second
	// agentJobKeep 排好的成品在内存里留多久（重启就没了；编辑器链接不受影响，它按记下的输入重新导入）。
	agentJobKeep = 15 * time.Minute
	agentMaxJobs = 500
	// agentRecordKeep 交接记录（编辑器打开要用的输入）留多久；agentRecordsMaxBytes 全部记录合计上限，满了先丢最早的。
	agentRecordKeep      = 7 * 24 * time.Hour
	agentRecordsMaxBytes = 2 << 30
	// agentTextMaxBytes 交来的 Markdown、文字最长多少（线上最长稿 3.8 万字约 114KB）。
	agentTextMaxBytes = 4 << 20
	// 传 Word 的一次性地址：10 分钟内传一次；同时等着传的全站最多 agentMaxUploads 个、每人 agentUploadsPerPerson 个。
	agentUploadTTL        = 10 * time.Minute
	agentMaxUploads       = 50
	agentUploadsPerPerson = 5
)

var agentIDPattern = regexp.MustCompile(`^ag_[A-Za-z0-9_-]{22}$`)

func newAgentID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic("智能体编号生成失败：" + err.Error())
	}
	return "ag_" + base64.RawURLEncoding.EncodeToString(raw)[:22]
}

// ---- 装配 ----

type agentDeps struct {
	auth     *authService
	layout   *layoutAPI
	checker  *articleChecker
	stateDir string // 授权记录、交接记录放这里（和用户库同一个目录）
	issuer   string // 本服务对外地址：授权服务器 issuer，也是编辑器链接、上传地址的前缀
	now      func() time.Time
	wait     time.Duration // 一次工具调用最多等多久，不填是 agentToolWait（测试里调短）
}

// registerAgentEntry 挂上智能体入口的全部路由：授权（/.well-known/*、/oauth/*）、MCP（/mcp）、传 Word、编辑器取回输入。
func registerAgentEntry(mux *http.ServeMux, deps agentDeps) error {
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.wait <= 0 {
		deps.wait = agentToolWait
	}
	guard, err := mcpauth.New(mcpauth.Options{
		Issuer:       deps.issuer,
		ResourcePath: "/mcp",
		ResourceName: "排版工具",
		Scope:        "layout",
		StateFile:    filepath.Join(deps.stateDir, "agent-grants.json"),
		Person: func(r *http.Request) (mcpauth.Person, bool) {
			user, err := deps.auth.currentUser(r)
			if err != nil {
				return mcpauth.Person{}, false
			}
			return mcpauth.Person{ID: user.ID, Name: user.Name}, true
		},
		RequireLogin: deps.auth.Page,
		Allowed:      deps.auth.agentAllowed,
		Now:          deps.now,
	})
	if err != nil {
		return err
	}
	records, err := openAgentRecords(filepath.Join(deps.stateDir, "agent-results"), deps.now)
	if err != nil {
		return err
	}
	hub := &agentHub{
		layout:  deps.layout,
		checker: deps.checker,
		auth:    deps.auth,
		records: records,
		base:    strings.TrimRight(deps.issuer, "/"),
		now:     deps.now,
		wait:    deps.wait,
		jobs:    map[string]*agentJob{},
		uploads: map[string]*agentUpload{},
	}
	guard.Register(mux)
	mux.Handle("/mcp", guard.Protect(hub.mcpHandler()))
	mux.HandleFunc("POST /api/agent/uploads/{id}", hub.handleUpload)
	mux.HandleFunc("GET /api/agent/results/{id}", noStoreHandler(hub.handleRecord))
	mux.HandleFunc("GET /api/agent/results/{id}/file", noStoreHandler(hub.handleRecordFile))
	return nil
}

// agentAllowed 这个人现在能不能让智能体替他用排版工具：在用户库里、没停用，并且登录那边认（飞书登录、不在停用名单）。
func (s *authService) agentAllowed(ctx context.Context, userID string) error {
	user, ok, err := s.users.Get(userID)
	if err != nil {
		return errors.New("账号库读不出来，过一会儿再试")
	}
	if !ok || user.Status != authUserActive {
		return errors.New("这个账号已停用")
	}
	if s.login == nil {
		return nil
	}
	return s.login.agentCheck(ctx, user)
}

// ---- 本体 ----

type agentHub struct {
	layout  *layoutAPI
	checker *articleChecker
	auth    *authService
	records *agentRecordStore
	base    string
	now     func() time.Time
	wait    time.Duration

	mu      sync.Mutex
	jobs    map[string]*agentJob
	uploads map[string]*agentUpload
}

// agentJob 一篇正在排或刚排好的。
type agentJob struct {
	id       string
	personID string
	account  accountMeta
	created  time.Time
	done     chan struct{}

	// 下面两个在 done 关上之后才读。
	result   layoutResultJSON
	err      *layoutError
	finished time.Time
}

// agentUpload 一个传 Word 的一次性地址。busy 正在传，ready 传好了。
type agentUpload struct {
	personID string
	expires  time.Time
	busy     bool
	ready    bool
	fileName string
}

func (h *agentHub) editorURL(account accountMeta, id string) string {
	return h.base + "/" + account.Channel + "/" + account.ID + "?agent=" + id
}

func (h *agentHub) dropExpiredLocked() {
	now := h.now()
	for id, job := range h.jobs {
		select {
		case <-job.done:
			if now.Sub(job.finished) > agentJobKeep {
				delete(h.jobs, id)
			}
		default:
		}
	}
	for id, up := range h.uploads {
		if now.After(up.expires) {
			delete(h.uploads, id)
			_ = os.Remove(h.records.uploadPath(id))
		}
	}
}

// ---- MCP ----

const agentInstructions = `排版工具：把文章排成 FBIF 各公众号或 FoodTalks 网站的样式，再做 AI 校对（错别字、事实风险）。只排版和校对，不发布、不存草稿。
用法：layout_article 交一篇（链接、Markdown 或文字；Word 先调 prepare_upload 拿上传地址）。排好会给 editor_url，原样交给用户：用户在排版工具里打开，接着改、复制到后台。
要校对就调 check_article。没排完、没查完会给 result_id，再调 get_layout_result 或 check_article 接着等。`

func (h *agentHub) mcpHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "fbif-layout", Title: "FBIF 排版工具", Version: "1"},
		&mcp.ServerOptions{Instructions: agentInstructions})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "layout_article",
		Title:       "排版",
		Description: "按某个账号的样式排一篇文章。url、markdown、text、upload_id 只给一个。最多等 40 秒，排好给 editor_url；没排完给 result_id，用 get_layout_result 接着等。",
		InputSchema: agentLayoutInputSchema(),
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptrBool(true), DestructiveHint: ptrBool(false)},
	}, h.toolLayout)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_layout_result",
		Title:       "取排版结果",
		Description: "取 layout_article 没排完的那一篇，最多等 40 秒。include_html=true 时带上成品 HTML（很长，一般不用：给用户 editor_url 就行）。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, h.toolResult)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "check_article",
		Title:       "AI 校对",
		Description: "校对排好的那一篇：和排版工具编辑器里的「AI 校对」同一套检查。模型要想两三分钟，没查完回 status=checking 或 verifying，再调一次接着等。改法不会自动改进文章：用户在编辑器里点「改正」。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: ptrBool(false)},
	}, h.toolCheck)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "prepare_upload",
		Title:       "传 Word",
		Description: "要排 Word（.docx、.doc）时先调这个，拿一个 10 分钟内有效、只能用一次的上传地址，在终端里运行返回的 command（把 <本地文件路径> 换成文件路径），传完把 upload_id 交给 layout_article。",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: false, DestructiveHint: ptrBool(false), OpenWorldHint: ptrBool(false)},
	}, h.toolPrepareUpload)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// 本服务在 Caddy 后面听 127.0.0.1，Host 是公网域名：SDK 防 DNS 重绑定那一道会把每个请求都挡掉。
		DisableLocalhostProtection: true,
	})
}

func ptrBool(v bool) *bool { return &v }

type agentLayoutInput struct {
	Account  string `json:"account" jsonschema:"按哪个账号的样式排"`
	URL      string `json:"url,omitempty" jsonschema:"公众号文章、普通网页、飞书云文档的链接（飞书文档用你本人的飞书权限读）"`
	Markdown string `json:"markdown,omitempty" jsonschema:"整篇文章的 Markdown（带页脚）：开头一行 # 标题 是文章标题，## 起是小标题，- 列表、> 引用照常写；图片写 ![](https://…) 公开地址。你自己写的、改过的稿用它"`
	Text     string `json:"text,omitempty" jsonschema:"用户给的一段纯文字原样交，和在编辑器里粘贴文字一样"`
	UploadID string `json:"upload_id,omitempty" jsonschema:"prepare_upload 给的编号：Word 传上去之后填"`
	Title    string `json:"title,omitempty" jsonschema:"只对 markdown 有用：Markdown 开头没有 # 标题 时用它当标题"`
}

// agentLayoutInputSchema account 列出能选的账号（id 和名字），智能体不用猜。
func agentLayoutInputSchema() *jsonschema.Schema {
	schema, err := jsonschema.For[agentLayoutInput](nil)
	if err != nil {
		panic("layout_article 的参数表生成失败：" + err.Error())
	}
	account := schema.Properties["account"]
	var names []string
	for _, id := range catalogOrderIDs() {
		meta, _ := accountMetaByID(id)
		account.Enum = append(account.Enum, id)
		view := layoutAccountView(meta)
		names = append(names, id+"="+view.Name+"（贴进"+view.PasteInto+"）")
	}
	account.Description = "按哪个账号的样式排：" + strings.Join(names, "；")
	return schema
}

type agentResultInput struct {
	ResultID    string `json:"result_id" jsonschema:"layout_article 给的 result_id"`
	IncludeHTML bool   `json:"include_html,omitempty" jsonschema:"带上成品 HTML"`
}

type agentCheckInput struct {
	ResultID string `json:"result_id" jsonschema:"layout_article 给的 result_id"`
}

type agentLayoutOutput struct {
	ResultID  string   `json:"result_id" jsonschema:"这一篇的编号：get_layout_result、check_article 用它"`
	Status    string   `json:"status" jsonschema:"running 还在排；done 排好了；failed 没排成"`
	EditorURL string   `json:"editor_url,omitempty" jsonschema:"在排版工具里打开这一篇：原样交给用户，用户打开后接着改、复制到后台"`
	Account   string   `json:"account" jsonschema:"账号名"`
	PasteInto string   `json:"paste_into,omitempty" jsonschema:"成品粘进哪个后台"`
	Title     string   `json:"title,omitempty"`
	Summary   string   `json:"summary,omitempty" jsonschema:"摘要（FoodTalks 账号从开头导语取）"`
	Repost    *bool    `json:"repost,omitempty" jsonschema:"按转载（true）还是原创（false）排的"`
	Images    int      `json:"images,omitempty" jsonschema:"正文图片数"`
	Headings  int      `json:"headings,omitempty" jsonschema:"小标题数"`
	Warnings  []string `json:"warnings,omitempty" jsonschema:"排好了但要留意的事"`
	HTML      string   `json:"html,omitempty" jsonschema:"成品 HTML（include_html 才给）"`
	Error     string   `json:"error,omitempty" jsonschema:"没排成的原因"`
	Next      string   `json:"next" jsonschema:"下一步怎么做"`
}

type agentCheckFix struct {
	Wrong    string `json:"wrong" jsonschema:"写错的字"`
	Correct  string `json:"correct" jsonschema:"应改成"`
	Evidence string `json:"evidence" jsonschema:"原句"`
}

type agentCheckIssue struct {
	Label    string `json:"label" jsonschema:"哪类问题"`
	Level    string `json:"level" jsonschema:"warning；block 是阻断级"`
	Evidence string `json:"evidence" jsonschema:"原句"`
	Reason   string `json:"reason,omitempty" jsonschema:"说明"`
}

type agentCheckOutput struct {
	ResultID  string            `json:"result_id"`
	Status    string            `json:"status" jsonschema:"checking 通读全文中；verifying 条目已出、在复核改法；done 查完；failed 没查成；off 这个环境没接校对模型"`
	Fixes     []agentCheckFix   `json:"fixes" jsonschema:"改法唯一的确定错误"`
	Issues    []agentCheckIssue `json:"issues" jsonschema:"确定有问题、要人判断怎么改的"`
	Notes     []agentCheckIssue `json:"notes" jsonschema:"待核：不一定是错，人看一眼"`
	EditorURL string            `json:"editor_url,omitempty" jsonschema:"在排版工具里打开这一篇，校对卡上能一键改正"`
	Error     string            `json:"error,omitempty"`
	Next      string            `json:"next" jsonschema:"下一步怎么做"`
}

type agentUploadOutput struct {
	UploadID  string `json:"upload_id" jsonschema:"传完交给 layout_article"`
	UploadURL string `json:"upload_url"`
	Command   string `json:"command" jsonschema:"在终端里运行：把 <本地文件路径> 换成 Word 文件的路径"`
	ExpiresIn int    `json:"expires_in" jsonschema:"多少秒内有效"`
}

// agentPerson 取这次调用的人（mcpauth.Protect 认出来的）。
func agentPerson(req *mcp.CallToolRequest) (mcpauth.Person, error) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil || req.Extra.TokenInfo.UserID == "" {
		return mcpauth.Person{}, errors.New("没认出是谁：请在智能体里重新连接排版工具")
	}
	name, _ := req.Extra.TokenInfo.Extra["name"].(string)
	return mcpauth.Person{ID: req.Extra.TokenInfo.UserID, Name: name}, nil
}

func agentCaller(personID string) string { return "agent:" + personID }

// toolLayout 收一篇：认输入 → 记交接记录 → 拿限额 → 和排版接口同一条路后台排 → 最多等 agentToolWait。
func (h *agentHub) toolLayout(ctx context.Context, req *mcp.CallToolRequest, in agentLayoutInput) (*mcp.CallToolResult, agentLayoutOutput, error) {
	person, err := agentPerson(req)
	if err != nil {
		return nil, agentLayoutOutput{}, err
	}
	if h.layout.paused() {
		return nil, agentLayoutOutput{}, errors.New("排版工具正在重启，过一分钟再试")
	}
	lreq, rec, err := h.parseLayoutInput(person.ID, in)
	if err != nil {
		return nil, agentLayoutOutput{}, err
	}
	caller := agentCaller(person.ID)
	release, lerr := h.layout.limits.acquireRender(caller)
	if lerr == nil && lreq.Kind == layoutInputWechatURL {
		if lerr = h.layout.limits.allowWechatFetch(); lerr != nil {
			release()
		}
	}
	if lerr != nil {
		return nil, agentLayoutOutput{}, errors.New(agentLimitMessage(lerr))
	}
	id := newAgentID()
	rec.ID, lreq.RequestID = id, id
	lreq.Caller = layoutCaller{Code: caller, Name: person.Name}
	if lreq.Kind == layoutInputFeishuDoc {
		lreq.FeishuAsPerson = h.feishuTokenFor(person.ID)
	}
	if err := h.records.save(rec, lreq.File); err != nil {
		release()
		log.Printf("agent-mcp: %s 交接记录没存下来：%v", id, err)
		return nil, agentLayoutOutput{}, errors.New("排版工具这边没存下来，再试一次")
	}

	job := &agentJob{id: id, personID: person.ID, account: lreq.Account, created: h.now(), done: make(chan struct{})}
	h.mu.Lock()
	h.dropExpiredLocked()
	if len(h.jobs) >= agentMaxJobs {
		h.mu.Unlock()
		release()
		return nil, agentLayoutOutput{}, errors.New("排版工具这会儿太忙，过几分钟再试")
	}
	h.jobs[id] = job
	h.mu.Unlock()

	op := &layoutOperation{id: id, caller: caller, requestID: id, created: job.created, done: make(chan struct{}), state: layoutStateRunning}
	entry := layoutRequestLog{RequestID: id, Caller: caller, Account: lreq.Account.ID, Kind: string(lreq.Kind), Started: job.created}
	h.layout.launch(op, entry, lreq, release, func(result layoutResultJSON, lerr *layoutError) {
		h.finish(job, result, lerr)
	})
	return nil, h.waitLayout(ctx, job, false), nil
}

// parseLayoutInput 认输入，同时写好交接记录（编辑器按它重新导入）。输入种类和编辑器上的操作一一对应：
// 链接 = 粘贴链接，markdown = 上传 .md，text = 粘贴文字，Word = 上传 Word。repost 不让选，和编辑器一样按操作定。
func (h *agentHub) parseLayoutInput(personID string, in agentLayoutInput) (layoutRequest, agentRecord, error) {
	lreq, lerr := layoutRequestBase(in.Account, "", nil)
	if lerr != nil {
		return layoutRequest{}, agentRecord{}, errors.New(lerr.message())
	}
	text := in.Text
	if isSingleHTTPURL(text) {
		// 编辑器框里粘一个链接就是抓链接。
		in.URL, text = strings.TrimSpace(text), ""
	}
	given := 0
	for _, v := range []string{in.URL, in.Markdown, text, in.UploadID} {
		if strings.TrimSpace(v) != "" {
			given++
		}
	}
	if given != 1 {
		return layoutRequest{}, agentRecord{}, errors.New("url、markdown、text、upload_id 要给且只给一个")
	}
	rec := agentRecord{PersonID: personID, Account: lreq.Account.ID, CreatedAt: h.now()}
	switch {
	case strings.TrimSpace(in.URL) != "":
		out, lerr := layoutRequestForURL(lreq, in.URL)
		if lerr != nil {
			return layoutRequest{}, agentRecord{}, errors.New(lerr.message())
		}
		rec.Kind, rec.URL = agentKindURL, out.URL
		return out, rec, nil
	case strings.TrimSpace(in.Markdown) != "":
		if len(in.Markdown) > agentTextMaxBytes {
			return layoutRequest{}, agentRecord{}, fmt.Errorf("markdown 超过 %d MB", agentTextMaxBytes>>20)
		}
		// 编辑器上传 .md 时标题兜底取文件名：交接时就用这个文件名，两边的标题一样。
		lreq.Kind, lreq.Body, lreq.FileName = layoutInputMarkdown, in.Markdown, agentMarkdownFileName(in.Title)
		rec.Kind, rec.Text, rec.FileName = agentKindMarkdown, in.Markdown, lreq.FileName
		return lreq, rec, nil
	case strings.TrimSpace(text) != "":
		if len(text) > agentTextMaxBytes {
			return layoutRequest{}, agentRecord{}, fmt.Errorf("text 超过 %d MB", agentTextMaxBytes>>20)
		}
		lreq.Kind, lreq.Body = layoutInputText, text
		rec.Kind, rec.Text = agentKindText, text
		return lreq, rec, nil
	}
	name, data, err := h.takeUpload(personID, strings.TrimSpace(in.UploadID))
	if err != nil {
		return layoutRequest{}, agentRecord{}, err
	}
	lreq.Kind, lreq.FileName, lreq.File = layoutInputWord, name, data
	rec.Kind, rec.FileName = agentKindWord, name
	return lreq, rec, nil
}

func isSingleHTTPURL(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || strings.ContainsAny(text, " \t\r\n") {
		return false
	}
	lower := strings.ToLower(text)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// agentMarkdownFileName 交接给编辑器的 .md 文件名：标题去掉路径符号、控制字符，最多 80 个字；没给标题叫「文章」。
func agentMarkdownFileName(title string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`/\:*?"<>|`, r) {
			return -1
		}
		return r
	}, strings.TrimSpace(title))
	if r := []rune(clean); len(r) > 80 {
		clean = string(r[:80])
	}
	clean = strings.Trim(clean, ". ")
	if clean == "" {
		clean = "文章"
	}
	return clean + ".md"
}

func (h *agentHub) feishuTokenFor(personID string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		user, ok, err := h.auth.users.Get(personID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errAuthenticationRequired
		}
		return h.auth.userAccessToken(ctx, user)
	}
}

func agentLimitMessage(lerr *layoutError) string {
	switch lerr.Code {
	case layoutErrBusy:
		return fmt.Sprintf("你已经有 %d 篇在排，等排完再交", layoutPerKeyConcurrent)
	case layoutErrRateLimited:
		if lerr.retryAfterSeconds() > 0 {
			return fmt.Sprintf("交得太快了，%d 秒后再试", lerr.retryAfterSeconds())
		}
		return "交得太快了，过一分钟再试"
	}
	return lerr.message()
}

func (h *agentHub) finish(job *agentJob, result layoutResultJSON, lerr *layoutError) {
	if lerr == nil {
		title := result.Title
		if err := h.records.update(job.id, func(rec *agentRecord) {
			rec.Title, rec.CheckTitle, rec.CheckText = title, result.check.Title, result.check.Text
		}); err != nil {
			log.Printf("agent-mcp: %s 校对原文没记下：%v", job.id, err)
		}
		// 和编辑器一样，一排好就开查：智能体再来问、人打开编辑器，接上的都是这一次。
		if result.check.Text != "" {
			go func(personID string, source articleCheckSource) {
				_, _ = h.checker.check(context.Background(), personID, source.Title, source.Text, "", false)
			}(job.personID, result.check)
		}
	}
	h.mu.Lock()
	job.result, job.err, job.finished = result, lerr, h.now()
	h.mu.Unlock()
	close(job.done)
}

// waitLayout 等到排完或 agentToolWait 到点、调用方断开。
func (h *agentHub) waitLayout(ctx context.Context, job *agentJob, includeHTML bool) agentLayoutOutput {
	timer := time.NewTimer(h.wait)
	defer timer.Stop()
	select {
	case <-job.done:
	case <-timer.C:
	case <-ctx.Done():
	}
	view := layoutAccountView(job.account)
	out := agentLayoutOutput{ResultID: job.id, Account: view.Name, PasteInto: view.PasteInto}
	select {
	case <-job.done:
	default:
		out.Status = "running"
		out.Next = "还在排：用这个 result_id 调 get_layout_result 接着等"
		return out
	}
	h.mu.Lock()
	result, lerr := job.result, job.err
	h.mu.Unlock()
	if lerr != nil {
		out.Status, out.Error = "failed", agentLayoutErrorMessage(lerr)
		out.Next = "没排成：按 error 说的改了再调 layout_article"
		return out
	}
	repost := result.Repost
	out.Status = "done"
	out.EditorURL = h.editorURL(job.account, job.id)
	out.Title, out.Summary, out.Repost = result.Title, result.Summary, &repost
	out.Images, out.Headings = result.Stats.Images, result.Stats.Headings
	for _, w := range result.Warnings {
		out.Warnings = append(out.Warnings, w.Message)
	}
	if includeHTML {
		out.HTML = result.HTML
	}
	out.Next = "把 editor_url 原样交给用户，用户在排版工具里打开、接着改、复制到" + view.PasteInto + "；要校对调 check_article"
	return out
}

func agentLayoutErrorMessage(lerr *layoutError) string {
	if lerr.Code == layoutErrFeishuNotShared {
		return "这篇飞书文档你本人没有权限读，排版工具的飞书应用也没加进文档：请确认链接对、你能打开这篇文档"
	}
	return lerr.message()
}

func (h *agentHub) lookupJob(personID, id string) (*agentJob, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dropExpiredLocked()
	job := h.jobs[id]
	if job == nil || job.personID != personID {
		return nil, false
	}
	return job, true
}

func (h *agentHub) toolResult(ctx context.Context, req *mcp.CallToolRequest, in agentResultInput) (*mcp.CallToolResult, agentLayoutOutput, error) {
	person, err := agentPerson(req)
	if err != nil {
		return nil, agentLayoutOutput{}, err
	}
	id := strings.TrimSpace(in.ResultID)
	if job, ok := h.lookupJob(person.ID, id); ok {
		return nil, h.waitLayout(ctx, job, in.IncludeHTML), nil
	}
	// 内存里没了（过了 15 分钟或重启过），编辑器链接还能用：它按记下的输入重新导入。
	rec, ok, err := h.records.load(id)
	if err != nil || !ok || rec.PersonID != person.ID {
		return nil, agentLayoutOutput{}, errors.New("没有这一篇，或已超过 7 天：请重新调 layout_article")
	}
	meta, _ := accountMetaByID(rec.Account)
	view := layoutAccountView(meta)
	out := agentLayoutOutput{ResultID: id, Status: "done", EditorURL: h.editorURL(meta, id), Account: view.Name, PasteInto: view.PasteInto, Title: rec.Title,
		Next: "排好的成品已过期，editor_url 照样能打开（编辑器会按原输入重新排）；要 HTML 就重新调 layout_article"}
	if rec.CheckText == "" {
		out.Status, out.EditorURL = "failed", ""
		out.Error = "这一篇没排成或排的时候服务重启了"
		out.Next = "重新调 layout_article"
	}
	return nil, out, nil
}

// toolCheck 校对：和编辑器「AI 校对」卡送一样的标题、正文，同一篇只查一次；最多等 agentToolWait。
func (h *agentHub) toolCheck(ctx context.Context, req *mcp.CallToolRequest, in agentCheckInput) (*mcp.CallToolResult, agentCheckOutput, error) {
	person, err := agentPerson(req)
	if err != nil {
		return nil, agentCheckOutput{}, err
	}
	id := strings.TrimSpace(in.ResultID)
	source, account, err := h.checkSource(ctx, person.ID, id)
	if err != nil {
		return nil, agentCheckOutput{}, err
	}
	if strings.TrimSpace(source.Text) == "" {
		return nil, agentCheckOutput{}, errors.New("这一篇没有正文可校对")
	}
	if len(source.Text)+len(source.Title) > articleCheckMaxBody {
		return nil, agentCheckOutput{}, errors.New("正文太长，校对不了")
	}
	waitCtx, cancel := context.WithTimeout(ctx, h.wait)
	defer cancel()
	have, retry := "", true
	var view articleCheckView
	for {
		view, err = h.checker.check(waitCtx, person.ID, source.Title, source.Text, have, retry)
		if errors.Is(err, errArticleCheckTooFrequent) {
			return nil, agentCheckOutput{}, errors.New("校对太频繁，过几分钟再试")
		}
		if err != nil {
			return nil, agentCheckOutput{}, err
		}
		retry = false
		if articleCheckRank(view.Status) >= articleCheckRank(articleCheckDone) || view.Status == articleCheckOff || waitCtx.Err() != nil {
			break
		}
		have = view.Status
	}
	log.Printf("agent-mcp: check person=%s result=%s status=%s fixes=%d issues=%d", person.ID, id, view.Status, len(view.Fixes), len(view.Issues))
	out := agentCheckOutput{ResultID: id, Status: view.Status, Fixes: []agentCheckFix{}, Issues: []agentCheckIssue{}, Notes: []agentCheckIssue{}}
	if meta, ok := accountMetaByID(account); ok {
		out.EditorURL = h.editorURL(meta, id)
	}
	for _, f := range view.Fixes {
		out.Fixes = append(out.Fixes, agentCheckFix(f))
	}
	for _, item := range view.Issues {
		out.Issues = append(out.Issues, agentCheckIssue(item))
	}
	for _, item := range view.Notes {
		out.Notes = append(out.Notes, agentCheckIssue(item))
	}
	switch view.Status {
	case articleCheckChecking, articleCheckVerifying:
		out.Next = "还在查（模型要想两三分钟）：用同一个 result_id 再调 check_article 接着等"
	case articleCheckDone:
		out.Next = "把 fixes、issues 告诉用户；用户打开 editor_url，在左边「AI 校对」卡上点「改正」，预览和复制都跟着改"
	case articleCheckFailed:
		out.Error = firstNonEmpty(view.Error, "校对没完成")
		out.Next = "过一会儿再调 check_article 重查"
	case articleCheckOff:
		out.Next = "这个环境没接校对模型，查不了"
	}
	return nil, out, nil
}

// checkSource 送去校对的标题和正文：刚排好的在内存里，过了 15 分钟从交接记录取。
func (h *agentHub) checkSource(ctx context.Context, personID, id string) (articleCheckSource, string, error) {
	if job, ok := h.lookupJob(personID, id); ok {
		timer := time.NewTimer(h.wait)
		defer timer.Stop()
		select {
		case <-job.done:
		case <-timer.C:
			return articleCheckSource{}, "", errors.New("还没排完：先用 get_layout_result 等排好")
		case <-ctx.Done():
			return articleCheckSource{}, "", ctx.Err()
		}
		h.mu.Lock()
		result, lerr := job.result, job.err
		h.mu.Unlock()
		if lerr != nil {
			return articleCheckSource{}, "", errors.New("这一篇没排成，没法校对")
		}
		return result.check, job.account.ID, nil
	}
	rec, ok, err := h.records.load(id)
	if err != nil || !ok || rec.PersonID != personID {
		return articleCheckSource{}, "", errors.New("没有这一篇，或已超过 7 天：请重新调 layout_article")
	}
	if rec.CheckText == "" {
		return articleCheckSource{}, "", errors.New("这一篇没排成，没法校对")
	}
	return articleCheckSource{Title: rec.CheckTitle, Text: rec.CheckText}, rec.Account, nil
}

func (h *agentHub) toolPrepareUpload(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, agentUploadOutput, error) {
	person, err := agentPerson(req)
	if err != nil {
		return nil, agentUploadOutput{}, err
	}
	id := newAgentID()
	h.mu.Lock()
	h.dropExpiredLocked()
	mine := 0
	for _, up := range h.uploads {
		if up.personID == person.ID && !up.ready {
			mine++
		}
	}
	if len(h.uploads) >= agentMaxUploads || mine >= agentUploadsPerPerson {
		h.mu.Unlock()
		return nil, agentUploadOutput{}, errors.New("等着传的 Word 太多了，先把拿到的地址用掉或等 10 分钟")
	}
	h.uploads[id] = &agentUpload{personID: person.ID, expires: h.now().Add(agentUploadTTL)}
	h.mu.Unlock()
	url := h.base + "/api/agent/uploads/" + id
	return nil, agentUploadOutput{
		UploadID:  id,
		UploadURL: url,
		Command:   "curl -sS -F 'file=@<本地文件路径>' '" + url + "'",
		ExpiresIn: int(agentUploadTTL / time.Second),
	}, nil
}

// takeUpload 用掉一个传好的 Word：只认本人、10 分钟内、传过的；用一次就作废。
func (h *agentHub) takeUpload(personID, id string) (string, []byte, error) {
	if !agentIDPattern.MatchString(id) {
		return "", nil, errors.New("upload_id 不对：先调 prepare_upload")
	}
	h.mu.Lock()
	h.dropExpiredLocked()
	up := h.uploads[id]
	if up == nil || up.personID != personID {
		h.mu.Unlock()
		return "", nil, errors.New("upload_id 不对或已过期：重新调 prepare_upload")
	}
	if !up.ready {
		h.mu.Unlock()
		return "", nil, errors.New("还没传上来：先运行 prepare_upload 给的 command")
	}
	delete(h.uploads, id)
	h.mu.Unlock()
	file := h.records.uploadPath(id)
	data, err := os.ReadFile(file)
	_ = os.Remove(file)
	if err != nil {
		return "", nil, errors.New("传上来的文件没找到：重新调 prepare_upload 再传一次")
	}
	return up.fileName, data, nil
}

// ---- 传 Word（一次性地址，不用登录：地址本身就是凭证，10 分钟、只能传一次）----

func (h *agentHub) handleUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id := r.PathValue("id")
	if !agentIDPattern.MatchString(id) {
		writeError(w, http.StatusNotFound, "上传地址不对：回到智能体重新调 prepare_upload")
		return
	}
	h.mu.Lock()
	h.dropExpiredLocked()
	up := h.uploads[id]
	switch {
	case up == nil:
		h.mu.Unlock()
		writeError(w, http.StatusNotFound, "上传地址不对或已过期：回到智能体重新调 prepare_upload")
		return
	case up.busy || up.ready:
		h.mu.Unlock()
		writeError(w, http.StatusConflict, "这个地址已经传过一次：回到智能体重新调 prepare_upload")
		return
	}
	// 先占住，传完之前同一个地址再来一次进不来，也不会被 layout_article 取走半个文件。
	up.busy = true
	h.mu.Unlock()
	name, size, err := h.receiveUpload(w, r, id)
	h.mu.Lock()
	up.busy = false
	up.ready, up.fileName = err == nil, name
	h.mu.Unlock()
	if err != nil {
		var ue agentUploadError
		if errors.As(err, &ue) {
			writeError(w, ue.status, ue.message)
			return
		}
		log.Printf("agent-mcp: 上传 %s 没存下来：%v", id, err)
		writeError(w, http.StatusInternalServerError, "没存下来，再传一次")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"upload_id": id, "file_name": name, "bytes": size, "next": "回到智能体，把 upload_id 交给 layout_article"})
}

type agentUploadError struct {
	status  int
	message string
}

func (e agentUploadError) Error() string { return e.message }

// receiveUpload 和排版接口传 Word 一样：只放宽这一个请求的读写超时，边读边限大小。
func (h *agentHub) receiveUpload(w http.ResponseWriter, r *http.Request, id string) (string, int64, error) {
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Now().Add(layoutUploadReadTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		log.Printf("agent-mcp: 放宽上传读超时失败：%v", err)
	}
	if err := controller.SetWriteDeadline(time.Now().Add(layoutUploadReadTimeout + 30*time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		log.Printf("agent-mcp: 放宽上传写超时失败：%v", err)
	}
	r.Body = http.MaxBytesReader(w, r.Body, layoutFileMaxBytes+1<<20)
	reader, err := r.MultipartReader()
	if err != nil {
		return "", 0, agentUploadError{http.StatusBadRequest, "要用 multipart/form-data 传，文件放在 file 字段：curl -F 'file=@文件路径' 地址"}
	}
	tooLarge := agentUploadError{http.StatusRequestEntityTooLarge, fmt.Sprintf("文件超过 %d MB", layoutFileMaxBytes>>20)}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return "", 0, agentUploadError{http.StatusBadRequest, "表单里没有 file 字段：curl -F 'file=@文件路径' 地址"}
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return "", 0, tooLarge
			}
			return "", 0, agentUploadError{http.StatusBadRequest, "上传中断，再传一次"}
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		name := path.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
		ext := strings.ToLower(path.Ext(name))
		if ext != ".docx" && ext != ".doc" {
			_ = part.Close()
			return "", 0, agentUploadError{http.StatusUnsupportedMediaType, "只收 Word（.docx、.doc）"}
		}
		size, err := h.records.writeUpload(id, io.LimitReader(part, layoutFileMaxBytes+1))
		_ = part.Close()
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return "", 0, tooLarge
			}
			return "", 0, err
		}
		if size > layoutFileMaxBytes {
			_ = os.Remove(h.records.uploadPath(id))
			return "", 0, tooLarge
		}
		if size == 0 {
			_ = os.Remove(h.records.uploadPath(id))
			return "", 0, agentUploadError{http.StatusBadRequest, "文件是空的"}
		}
		return name, size, nil
	}
}

// ---- 编辑器取回输入（登录的同事都能打开：链接常被转给同事接着改）----

type agentHandoffJSON struct {
	Account  string `json:"account"`
	Channel  string `json:"channel"`
	Kind     string `json:"kind"`
	URL      string `json:"url,omitempty"`
	Text     string `json:"text,omitempty"`
	FileName string `json:"file_name,omitempty"`
}

func (h *agentHub) recordForEditor(w http.ResponseWriter, r *http.Request) (agentRecord, bool) {
	if _, err := h.auth.currentUser(r); err != nil {
		if errors.Is(err, errLoginUnavailable) {
			writeAuthError(w, http.StatusServiceUnavailable, "auth_unavailable", "账号服务暂不可用，请稍后重试")
			return agentRecord{}, false
		}
		writeAuthError(w, http.StatusUnauthorized, "authentication_required", "请先登录")
		return agentRecord{}, false
	}
	id := r.PathValue("id")
	rec, ok, err := h.records.load(id)
	if err != nil {
		log.Printf("agent-mcp: 读交接记录 %s 失败：%v", id, err)
		writeError(w, http.StatusInternalServerError, "读不出来，刷新再试")
		return agentRecord{}, false
	}
	if !ok {
		writeError(w, http.StatusNotFound, "这一篇已超过 7 天，请在智能体里重新排")
		return agentRecord{}, false
	}
	return rec, true
}

func (h *agentHub) handleRecord(w http.ResponseWriter, r *http.Request) {
	rec, ok := h.recordForEditor(w, r)
	if !ok {
		return
	}
	meta, _ := accountMetaByID(rec.Account)
	writeJSON(w, http.StatusOK, agentHandoffJSON{Account: rec.Account, Channel: meta.Channel, Kind: rec.Kind, URL: rec.URL, Text: rec.Text, FileName: rec.FileName})
}

func (h *agentHub) handleRecordFile(w http.ResponseWriter, r *http.Request) {
	rec, ok := h.recordForEditor(w, r)
	if !ok {
		return
	}
	if rec.Kind != agentKindWord {
		writeError(w, http.StatusNotFound, "这一篇不是 Word")
		return
	}
	f, err := os.Open(h.records.filePath(rec.ID))
	if err != nil {
		writeError(w, http.StatusNotFound, "Word 原件没找到，请在智能体里重新排")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, f)
}

// ---- 交接记录 ----

const (
	agentKindURL      = "url"
	agentKindMarkdown = "markdown"
	agentKindText     = "text"
	agentKindWord     = "word"
)

// agentRecord 一篇交来的输入（编辑器按它重新导入）和送去校对的原文。不存成品：成品随时能按输入重排。
type agentRecord struct {
	ID        string    `json:"id"`
	PersonID  string    `json:"person_id"`
	Account   string    `json:"account"`
	Kind      string    `json:"kind"`
	URL       string    `json:"url,omitempty"`
	Text      string    `json:"text,omitempty"`
	FileName  string    `json:"file_name,omitempty"`
	CreatedAt time.Time `json:"created_at"`

	Title      string `json:"title,omitempty"`
	CheckTitle string `json:"check_title,omitempty"`
	CheckText  string `json:"check_text,omitempty"`
}

// agentRecordStore 一篇一个 JSON（Word 另存原件），写盘先写临时文件再改名。过期的在新存一篇时顺手清掉，不另设定时。
type agentRecordStore struct {
	dir string
	now func() time.Time
	mu  sync.Mutex
}

func openAgentRecords(dir string, now func() time.Time) (*agentRecordStore, error) {
	if err := os.MkdirAll(filepath.Join(dir, "uploads"), 0o700); err != nil {
		return nil, err
	}
	s := &agentRecordStore{dir: dir, now: now}
	// 等着传的地址只在内存里：重启后旧的上传文件没人认了。
	if entries, err := os.ReadDir(filepath.Join(dir, "uploads")); err == nil {
		for _, e := range entries {
			_ = os.Remove(filepath.Join(dir, "uploads", e.Name()))
		}
	}
	s.mu.Lock()
	s.gcLocked()
	s.mu.Unlock()
	return s, nil
}

func (s *agentRecordStore) jsonPath(id string) string   { return filepath.Join(s.dir, id+".json") }
func (s *agentRecordStore) filePath(id string) string   { return filepath.Join(s.dir, id+".file") }
func (s *agentRecordStore) uploadPath(id string) string { return filepath.Join(s.dir, "uploads", id) }

func writeFileAtomic(name string, data []byte) error {
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}

func (s *agentRecordStore) save(rec agentRecord, file []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	if rec.Kind == agentKindWord {
		if err := writeFileAtomic(s.filePath(rec.ID), file); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.jsonPath(rec.ID), raw)
}

func (s *agentRecordStore) update(id string, fn func(*agentRecord)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok, err := s.loadLocked(id)
	if err != nil {
		return err
	}
	if !ok {
		return os.ErrNotExist
	}
	fn(&rec)
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.jsonPath(id), raw)
}

func (s *agentRecordStore) load(id string) (agentRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(id)
}

func (s *agentRecordStore) loadLocked(id string) (agentRecord, bool, error) {
	if !agentIDPattern.MatchString(id) {
		return agentRecord{}, false, nil
	}
	raw, err := os.ReadFile(s.jsonPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return agentRecord{}, false, nil
	}
	if err != nil {
		return agentRecord{}, false, err
	}
	var rec agentRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return agentRecord{}, false, err
	}
	if s.now().Sub(rec.CreatedAt) > agentRecordKeep {
		return agentRecord{}, false, nil
	}
	return rec, true, nil
}

func (s *agentRecordStore) writeUpload(id string, r io.Reader) (int64, error) {
	tmp := s.uploadPath(id) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return n, os.Rename(tmp, s.uploadPath(id))
}

// gcLocked 删过期的记录；全部合计超过上限时从最早的删起。
func (s *agentRecordStore) gcLocked() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	type item struct {
		id    string
		mod   time.Time
		bytes int64
	}
	byID := map[string]*item{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		id := strings.TrimSuffix(strings.TrimSuffix(name, ".tmp"), path.Ext(strings.TrimSuffix(name, ".tmp")))
		info, err := e.Info()
		if err != nil {
			continue
		}
		it := byID[id]
		if it == nil {
			it = &item{id: id}
			byID[id] = it
		}
		it.bytes += info.Size()
		if strings.HasSuffix(name, ".json") {
			it.mod = info.ModTime()
		}
	}
	now := s.now()
	var kept []*item
	var total int64
	for _, it := range byID {
		// 没有 .json 的（写到一半）按最早算，先被删。
		if !agentIDPattern.MatchString(it.id) || now.Sub(it.mod) > agentRecordKeep+time.Hour {
			s.removeLocked(it.id)
			continue
		}
		kept = append(kept, it)
		total += it.bytes
	}
	if total <= agentRecordsMaxBytes {
		return
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].mod.Before(kept[j].mod) })
	for _, it := range kept {
		if total <= agentRecordsMaxBytes {
			break
		}
		s.removeLocked(it.id)
		total -= it.bytes
	}
}

func (s *agentRecordStore) removeLocked(id string) {
	for _, name := range []string{id + ".json", id + ".file", id + ".json.tmp", id + ".file.tmp"} {
		_ = os.Remove(filepath.Join(s.dir, name))
	}
}
