package main

// flash_spec.go — 快讯写法规范：飞书知识库《FoodTalks网站快讯 AI编辑规范》是唯一正本（2026-11-28 用户：
// 程序每次生成前读最新版），代码里不抄规范的字。
//
//   - 文档在中台 FLASH_SPEC_DOC（知识库或云文档的链接，也可以只给 token），用「排版工具」登录应用自己的身份读，
//     文档要先加这个应用（文档右上角「…」→「更多」→「添加文档应用」）。
//   - 每写一篇都重新取一次文档（一天十来篇，一次一两秒）；一分钟内连着来的几篇共用刚取到的那份。
//   - 取到的 Markdown 去掉划掉的字（~~…~~，那是作废的段落）和图片；正文里嵌的电子表格（不推荐的说法和改法）
//     取值换成表格放回原处，表里第一列另存一份，写完逐词查。
//   - 取不到：用上次取到的（存在任务库旁边），任务上记下用的是第几版、为什么没用最新版；一次都没取到就失败、过会儿再试。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	flashSpecDocEnv = "FLASH_SPEC_DOC"
	// flashSpecReuse 一批几篇连着进来时共用一份，不每篇都去飞书取。
	flashSpecReuse = time.Minute
)

// flashSpec 一版规范。
type flashSpec struct {
	Revision  int64             `json:"revision"`
	Text      string            `json:"text"`  // 给模型看的全文
	Avoid     []flashSpecPhrase `json:"avoid"` // 规范表格里列的不推荐说法
	FetchedAt string            `json:"fetched_at"`
	// Stale 非空：这次没取到最新版，用的是上次存下的，写的是原因。
	Stale string `json:"-"`
}

type flashSpecPhrase struct {
	Avoid   string `json:"avoid"`
	Instead string `json:"instead,omitempty"`
}

// flashSpecSource 从飞书取规范。token 是「排版工具」登录应用的 tenant_access_token。
type flashSpecSource struct {
	doc       string
	token     func(ctx context.Context) (string, error)
	baseURL   string
	client    *http.Client
	cachePath string
	now       func() time.Time

	mu     sync.Mutex
	last   *flashSpec
	lastAt time.Time
	docxID string // 知识库节点解析出的云文档 id
}

func newFlashSpecSourceFromEnv(auth *authService, cachePath string) *flashSpecSource {
	doc := strings.TrimSpace(os.Getenv(flashSpecDocEnv))
	s := &flashSpecSource{doc: doc, baseURL: defaultFeishuBaseURL, client: &http.Client{Timeout: 30 * time.Second},
		cachePath: cachePath, now: time.Now}
	if auth != nil && auth.config.FeishuAppID != "" && auth.config.FeishuSecret != "" {
		app := &feishuNotifier{baseURL: defaultFeishuBaseURL, appID: auth.config.FeishuAppID, appSecret: auth.config.FeishuSecret,
			httpClient: &http.Client{Timeout: 15 * time.Second}}
		s.token = app.tenantAccessToken
	}
	return s
}

// load 取最新一版；取不到就用存下的上一版（Stale 写原因）。
func (s *flashSpecSource) load(ctx context.Context) (flashSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last != nil && s.now().Sub(s.lastAt) < flashSpecReuse {
		return *s.last, nil
	}
	spec, err := s.fetch(ctx)
	if err == nil {
		s.last, s.lastAt = &spec, s.now()
		s.save(spec)
		return spec, nil
	}
	cached := s.last
	if cached == nil {
		cached = s.read()
	}
	if cached == nil {
		return flashSpec{}, fmt.Errorf("读不到快讯写法规范，也没有存下的旧版：%w", err)
	}
	out := *cached
	out.Stale = "没取到最新版（" + truncateRunes(err.Error(), 120) + "），用的是 " + out.FetchedAt + " 取的第 " + strconv.FormatInt(out.Revision, 10) + " 版"
	return out, nil
}

func (s *flashSpecSource) fetch(ctx context.Context) (flashSpec, error) {
	if s.doc == "" {
		return flashSpec{}, errors.New("没配规范文档（" + flashSpecDocEnv + "）")
	}
	if s.token == nil {
		return flashSpec{}, errors.New("没配排版工具登录应用，读不了飞书文档")
	}
	token, err := s.token(ctx)
	if err != nil {
		return flashSpec{}, fmt.Errorf("取飞书应用凭证：%w", err)
	}
	docID, err := s.documentID(ctx, token)
	if err != nil {
		return flashSpec{}, err
	}
	content, revision, err := s.fetchMarkdown(ctx, token, docID)
	if err != nil {
		return flashSpec{}, err
	}
	text, avoid, err := s.render(ctx, token, content)
	if err != nil {
		return flashSpec{}, err
	}
	if len([]rune(text)) < 200 {
		return flashSpec{}, fmt.Errorf("规范文档只读到 %d 个字，不像是全文", len([]rune(text)))
	}
	return flashSpec{Revision: revision, Text: text, Avoid: avoid, FetchedAt: s.now().UTC().Format(time.RFC3339)}, nil
}

// documentID 配的是知识库链接就先换成云文档 id（换一次记住）。
func (s *flashSpecSource) documentID(ctx context.Context, token string) (string, error) {
	if s.docxID != "" {
		return s.docxID, nil
	}
	ref := feishuDocumentRef{Token: s.doc, Type: "wiki"}
	if strings.Contains(s.doc, "://") {
		parsed, err := parseFeishuDocumentURL(s.doc)
		if err != nil {
			return "", fmt.Errorf("规范文档地址认不出：%w", err)
		}
		ref = parsed
	}
	if ref.Type == "wiki" {
		provider := newHTTPFeishuProvider(s.client)
		provider.apiBaseURL = s.baseURL
		resolved, err := provider.resolveWikiDocument(ctx, token, ref)
		if err != nil {
			return "", fmt.Errorf("知识库节点换不成文档（应用加进文档了吗）：%w", err)
		}
		ref = resolved
	}
	s.docxID = ref.Token
	return s.docxID, nil
}

func (s *flashSpecSource) fetchMarkdown(ctx context.Context, token, docID string) (string, int64, error) {
	body, _ := json.Marshal(map[string]any{
		"format": "markdown", "lang": "zh_cn",
		"export_option": map[string]any{"export_block_id": false, "export_cite_extra_data": false, "export_style_attrs": false},
	})
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Document struct {
				Content    string      `json:"content"`
				RevisionID json.Number `json:"revision_id"`
			} `json:"document"`
		} `json:"data"`
	}
	if err := s.call(ctx, http.MethodPost, "/open-apis/docs_ai/v1/documents/"+docID+"/fetch", token, body, &out); err != nil {
		return "", 0, fmt.Errorf("读规范文档：%w", err)
	}
	if out.Code != 0 {
		return "", 0, fmt.Errorf("读规范文档：飞书回 %d %s", out.Code, out.Msg)
	}
	revision, _ := out.Data.Document.RevisionID.Int64()
	return out.Data.Document.Content, revision, nil
}

var (
	flashSpecStrikeRe = regexp.MustCompile(`(?s)~~.*?~~`)
	flashSpecImageRe  = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	flashSpecCiteRe   = regexp.MustCompile(`<cite\b[^>]*\btitle="([^"]*)"[^>]*>\s*</cite>`)
	flashSpecSheetRe  = regexp.MustCompile(`<sheet\b[^>]*\bsheet-id="([^"]+)"[^>]*\btoken="([^"]+)"[^>]*>\s*</sheet>`)
	flashSpecEmptyH   = regexp.MustCompile(`(?m)^#+\s*$\n?`)
	flashSpecBlankRe  = regexp.MustCompile(`\n{3,}`)
)

// render 文档 Markdown → 给模型的规范全文和不推荐说法表。
func (s *flashSpecSource) render(ctx context.Context, token, content string) (string, []flashSpecPhrase, error) {
	var avoid []flashSpecPhrase
	var sheetErr error
	content = flashSpecSheetRe.ReplaceAllStringFunc(content, func(tag string) string {
		m := flashSpecSheetRe.FindStringSubmatch(tag)
		rows, err := s.sheetRows(ctx, token, m[2], m[1])
		if err != nil {
			sheetErr = err
			return ""
		}
		avoid = append(avoid, flashSpecAvoidRows(rows)...)
		return flashSpecTable(rows)
	})
	if sheetErr != nil {
		return "", nil, fmt.Errorf("读规范里的表格：%w", sheetErr)
	}
	return cleanFlashSpecMarkdown(content), avoid, nil
}

func cleanFlashSpecMarkdown(content string) string {
	content = flashSpecStrikeRe.ReplaceAllString(content, "")
	content = flashSpecImageRe.ReplaceAllString(content, "")
	content = flashSpecCiteRe.ReplaceAllString(content, "《$1》")
	content = strings.ReplaceAll(content, `\*`, "*")
	content = flashSpecEmptyH.ReplaceAllString(content, "")
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	content = flashSpecBlankRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	return strings.TrimSpace(content)
}

func (s *flashSpecSource) sheetRows(ctx context.Context, token, spreadsheet, sheetID string) ([][]string, error) {
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ValueRange struct {
				Values [][]any `json:"values"`
			} `json:"valueRange"`
		} `json:"data"`
	}
	path := "/open-apis/sheets/v2/spreadsheets/" + spreadsheet + "/values/" + sheetID + "!A1:H200?valueRenderOption=ToString"
	if err := s.call(ctx, http.MethodGet, path, token, nil, &out); err != nil {
		return nil, err
	}
	if out.Code != 0 {
		return nil, fmt.Errorf("飞书回 %d %s", out.Code, out.Msg)
	}
	rows := make([][]string, 0, len(out.Data.ValueRange.Values))
	for _, raw := range out.Data.ValueRange.Values {
		row := make([]string, 0, len(raw))
		empty := true
		for _, cell := range raw {
			text := strings.TrimSpace(flashSheetCellText(cell))
			if text != "" {
				empty = false
			}
			row = append(row, text)
		}
		if !empty {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// flashSheetCellText 单元格可能是字、数，也可能是带格式的一串 {text}。
func flashSheetCellText(cell any) string {
	switch v := cell.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case []any:
		var b strings.Builder
		for _, seg := range v {
			if m, ok := seg.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					b.WriteString(t)
				}
			}
		}
		return b.String()
	case map[string]any:
		if t, ok := v["text"].(string); ok {
			return t
		}
	}
	return fmt.Sprint(cell)
}

func flashSpecTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	for i, row := range rows {
		b.WriteString("| " + strings.Join(row, " | ") + " |\n")
		if i == 0 {
			b.WriteString("|" + strings.Repeat(" --- |", len(row)) + "\n")
		}
	}
	return b.String()
}

// flashSpecAvoidRows 表头第一格写着「不推荐」的表：第一列是不推荐的说法（一格里用顿号分开几个），第二列是改法。
func flashSpecAvoidRows(rows [][]string) []flashSpecPhrase {
	if len(rows) < 2 || len(rows[0]) == 0 || !strings.Contains(rows[0][0], "不推荐") {
		return nil
	}
	var out []flashSpecPhrase
	for _, row := range rows[1:] {
		if len(row) == 0 {
			continue
		}
		instead := ""
		if len(row) > 1 {
			instead = row[1]
		}
		for _, phrase := range strings.FieldsFunc(row[0], func(r rune) bool { return r == '、' || r == '，' || r == ',' || r == '/' }) {
			if phrase = strings.TrimSpace(phrase); phrase != "" {
				out = append(out, flashSpecPhrase{Avoid: phrase, Instead: instead})
			}
		}
	}
	return out
}

func (s *flashSpecSource) call(ctx context.Context, method, path, token string, body []byte, out any) error {
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := readBoundedBody(resp.Body, 4<<20)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("HTTP %d，回复读不懂：%s", resp.StatusCode, truncateRunes(string(raw), 160))
	}
	return nil
}

func (s *flashSpecSource) save(spec flashSpec) {
	if s.cachePath == "" {
		return
	}
	_ = writeDurableJSON(s.cachePath, ".flash-spec-*.json", spec)
}

func (s *flashSpecSource) read() *flashSpec {
	if s.cachePath == "" {
		return nil
	}
	raw, err := os.ReadFile(s.cachePath)
	if err != nil {
		return nil
	}
	var spec flashSpec
	if json.Unmarshal(raw, &spec) != nil || spec.Text == "" {
		return nil
	}
	return &spec
}

// flashSpecCachePath 存在任务库旁边。
func flashSpecCachePath(jobsPath string) string {
	return filepath.Join(filepath.Dir(jobsPath), "flash-spec-cache.json")
}
