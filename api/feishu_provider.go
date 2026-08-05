package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	feishuAccountsBaseURL = "https://accounts.feishu.cn"
	feishuOpenAPIBaseURL  = "https://open.feishu.cn"
)

var errFeishuInvalidDocument = errors.New("invalid Feishu document")
var errFeishuExportTimeout = errors.New("feishu export timed out")

type feishuExportTaskError struct {
	JobStatus int
	Message   string
}

func (e *feishuExportTaskError) Error() string {
	if e == nil {
		return "feishu export task failed"
	}
	if strings.TrimSpace(e.Message) != "" {
		return fmt.Sprintf("feishu export task failed with status %d: %s", e.JobStatus, e.Message)
	}
	return fmt.Sprintf("feishu export task failed with status %d", e.JobStatus)
}

func feishuExportStatusLabel(status int) string {
	switch status {
	case 0:
		return "success"
	case 1:
		return "new"
	case 2:
		return "processing"
	case 3:
		return "internal_error"
	case 107:
		return "export_size_limit"
	case 108:
		return "timeout"
	case 109:
		return "export_block_not_permitted"
	case 110:
		return "no_permission"
	case 111:
		return "document_deleted"
	case 122:
		return "copying_denied"
	case 123:
		return "document_not_found"
	case 6000:
		return "export_images_exceed_limit"
	default:
		return fmt.Sprintf("status_%d", status)
	}
}

type feishuUserToken struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
}

type feishuUserProfile struct {
	OpenID    string
	UnionID   string
	Name      string
	AvatarURL string
	Email     string
}

type feishuDocument struct {
	Token      string    `json:"token"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	URL        string    `json:"url,omitempty"`
	OwnerName  string    `json:"owner_name,omitempty"`
	ModifiedAt time.Time `json:"modified_at,omitempty"`
}

type feishuDocumentRef struct {
	Token string `json:"token"`
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
}

type feishuExportedFile struct {
	Filename string
	Content  []byte
	Metrics  feishuExportMetrics
}

type feishuExportMetrics struct {
	ResolveDuration  time.Duration
	CreateDuration   time.Duration
	PollDuration     time.Duration
	DownloadDuration time.Duration
	TotalDuration    time.Duration
	PollAttempts     int
}

type feishuProvider interface {
	ExchangeCode(ctx context.Context, code, redirectURI string) (feishuUserToken, error)
	RefreshUserToken(ctx context.Context, refreshToken string) (feishuUserToken, error)
	GetUserProfile(ctx context.Context, accessToken string) (feishuUserProfile, error)
	SearchDocuments(ctx context.Context, accessToken, query string, limit int) ([]feishuDocument, error)
	ExportDocument(ctx context.Context, accessToken string, ref feishuDocumentRef) (feishuExportedFile, error)
	ListChats(ctx context.Context, accessToken string) ([]feishuChat, error)
}

type httpFeishuProvider struct {
	client              *http.Client
	appID               string
	appSecret           string
	apiBaseURL          string
	exportPollAttempts  int
	exportPollInterval  time.Duration
	exportStatusTimeout time.Duration
	exportMaxPollErrors int
}

func newHTTPFeishuProvider(client *http.Client) *httpFeishuProvider {
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	return &httpFeishuProvider{
		client:              client,
		apiBaseURL:          feishuOpenAPIBaseURL,
		exportPollAttempts:  30,
		exportPollInterval:  250 * time.Millisecond,
		exportStatusTimeout: 8 * time.Second,
		exportMaxPollErrors: 3,
	}
}

func feishuExportPollDelay(base time.Duration, attempt int) time.Duration {
	if base <= 0 || attempt <= 0 {
		return 0
	}
	multiplier := 1
	switch {
	case attempt >= 5:
		multiplier = 4
	case attempt >= 3:
		multiplier = 2
	}
	delay := time.Duration(multiplier) * base
	if delay > time.Second {
		return time.Second
	}
	return delay
}

func (p *httpFeishuProvider) configure(appID, appSecret string) {
	p.appID = strings.TrimSpace(appID)
	p.appSecret = strings.TrimSpace(appSecret)
}

func (p *httpFeishuProvider) doJSON(ctx context.Context, method, endpoint, accessToken string, payload any, target any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("feishu API request failed: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := readBoundedBody(resp.Body, 4<<20)
	if err != nil {
		return fmt.Errorf("feishu API response too large: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("feishu API status %d", resp.StatusCode)
	}
	if err := json.Unmarshal(responseBody, target); err != nil {
		return fmt.Errorf("decode feishu API response: %w", err)
	}
	return nil
}

type feishuTokenResponse struct {
	Code                  int    `json:"code"`
	Msg                   string `json:"msg"`
	Error                 string `json:"error"`
	ErrorDescription      string `json:"error_description"`
	AccessToken           string `json:"access_token"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
}

func normalizeFeishuTokenResponse(resp feishuTokenResponse) (feishuUserToken, error) {
	if resp.Code != 0 || resp.Error != "" {
		return feishuUserToken{}, errors.New("feishu OAuth rejected the request")
	}
	if resp.AccessToken == "" || resp.ExpiresIn <= 0 {
		return feishuUserToken{}, errors.New("feishu OAuth response is incomplete")
	}
	if resp.RefreshToken == "" {
		return feishuUserToken{}, errors.New("feishu OAuth response has no refresh_token; offline_access must be granted")
	}
	return feishuUserToken{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ExpiresIn:    time.Duration(resp.ExpiresIn) * time.Second,
	}, nil
}

func (p *httpFeishuProvider) ExchangeCode(ctx context.Context, code, redirectURI string) (feishuUserToken, error) {
	var response feishuTokenResponse
	err := p.doJSON(ctx, http.MethodPost, p.apiBaseURL+"/open-apis/authen/v2/oauth/token", "", map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     p.appID,
		"client_secret": p.appSecret,
		"code":          code,
		"redirect_uri":  redirectURI,
	}, &response)
	if err != nil {
		return feishuUserToken{}, err
	}
	return normalizeFeishuTokenResponse(response)
}

func (p *httpFeishuProvider) RefreshUserToken(ctx context.Context, refreshToken string) (feishuUserToken, error) {
	var response feishuTokenResponse
	err := p.doJSON(ctx, http.MethodPost, p.apiBaseURL+"/open-apis/authen/v2/oauth/token", "", map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     p.appID,
		"client_secret": p.appSecret,
		"refresh_token": refreshToken,
	}, &response)
	if err != nil {
		return feishuUserToken{}, err
	}
	return normalizeFeishuTokenResponse(response)
}

func (p *httpFeishuProvider) GetUserProfile(ctx context.Context, accessToken string) (feishuUserProfile, error) {
	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			OpenID    string `json:"open_id"`
			UnionID   string `json:"union_id"`
			Name      string `json:"name"`
			AvatarURL string `json:"avatar_url"`
			AvatarBig string `json:"avatar_big"`
			Email     string `json:"email"`
		} `json:"data"`
	}
	if err := p.doJSON(ctx, http.MethodGet, p.apiBaseURL+"/open-apis/authen/v1/user_info", accessToken, nil, &response); err != nil {
		return feishuUserProfile{}, err
	}
	if response.Code != 0 || response.Data.OpenID == "" {
		return feishuUserProfile{}, errors.New("feishu user profile is unavailable")
	}
	avatar := response.Data.AvatarBig
	if avatar == "" {
		avatar = response.Data.AvatarURL
	}
	return feishuUserProfile{
		OpenID:    response.Data.OpenID,
		UnionID:   response.Data.UnionID,
		Name:      response.Data.Name,
		AvatarURL: avatar,
		Email:     response.Data.Email,
	}, nil
}

func normalizeFeishuDocumentType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "doc":
		return "doc"
	case "docx":
		return "docx"
	case "wiki":
		return "wiki"
	default:
		return ""
	}
}

func validFeishuDocumentToken(token string) bool {
	if len(token) < 4 || len(token) > 128 {
		return false
	}
	for _, char := range token {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '_' ||
			char == '-' {
			continue
		}
		return false
	}
	return true
}

func officialFeishuDocumentHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for _, suffix := range []string{"feishu.cn", "larkoffice.com", "larksuite.com"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func parseFeishuDocumentURL(raw string) (feishuDocumentRef, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil ||
		parsed.Scheme != "https" ||
		parsed.User != nil ||
		parsed.Port() != "" ||
		!officialFeishuDocumentHost(parsed.Hostname()) {
		return feishuDocumentRef{}, errFeishuInvalidDocument
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) != 2 {
		return feishuDocumentRef{}, errFeishuInvalidDocument
	}
	pathKind := strings.ToLower(segments[0])
	docType := normalizeFeishuDocumentType(pathKind)
	if pathKind == "docs" {
		docType = "doc"
	}
	token, unescapeErr := url.PathUnescape(segments[1])
	if unescapeErr != nil ||
		(docType != "doc" && docType != "docx" && docType != "wiki") ||
		!validFeishuDocumentToken(token) {
		return feishuDocumentRef{}, errFeishuInvalidDocument
	}
	return feishuDocumentRef{
		Token: token,
		Type:  docType,
		URL:   parsed.String(),
	}, nil
}

func (p *httpFeishuProvider) SearchDocuments(ctx context.Context, accessToken, query string, limit int) ([]feishuDocument, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []feishuDocument{}, nil
	}
	if limit < 1 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}
	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			DocsEntities []struct {
				Token      string `json:"docs_token"`
				Type       string `json:"docs_type"`
				Title      string `json:"title"`
				URL        string `json:"url"`
				OwnerName  string `json:"owner_name"`
				EditTime   string `json:"edit_time"`
				UpdateTime string `json:"update_time"`
			} `json:"docs_entities"`
		} `json:"data"`
	}
	err := p.doJSON(ctx, http.MethodPost, p.apiBaseURL+"/open-apis/suite/docs-api/search/object", accessToken, map[string]any{
		"search_key": query,
		"count":      limit,
		"offset":     0,
		"docs_types": []string{"doc"},
	}, &response)
	if err != nil {
		return nil, err
	}
	if response.Code != 0 {
		return nil, errors.New("feishu document search failed")
	}
	documents := make([]feishuDocument, 0, len(response.Data.DocsEntities))
	for _, entity := range response.Data.DocsEntities {
		docType := normalizeFeishuDocumentType(entity.Type)
		if docType == "" || entity.Token == "" {
			continue
		}
		modifiedAt := parseFeishuTimestamp(entity.EditTime)
		if modifiedAt.IsZero() {
			modifiedAt = parseFeishuTimestamp(entity.UpdateTime)
		}
		documents = append(documents, feishuDocument{
			Token:      entity.Token,
			Type:       docType,
			Title:      strings.TrimSpace(entity.Title),
			URL:        strings.TrimSpace(entity.URL),
			OwnerName:  strings.TrimSpace(entity.OwnerName),
			ModifiedAt: modifiedAt,
		})
	}
	return documents, nil
}

func parseFeishuTimestamp(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if unix, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if unix > 1_000_000_000_000 {
			unix /= 1000
		}
		return time.Unix(unix, 0).UTC()
	}
	parsed, _ := time.Parse(time.RFC3339, raw)
	return parsed.UTC()
}

func (p *httpFeishuProvider) resolveWikiDocument(ctx context.Context, accessToken string, ref feishuDocumentRef) (feishuDocumentRef, error) {
	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Node struct {
				ObjToken string `json:"obj_token"`
				ObjType  string `json:"obj_type"`
				Title    string `json:"title"`
			} `json:"node"`
		} `json:"data"`
	}
	endpoint := p.apiBaseURL + "/open-apis/wiki/v2/spaces/get_node?token=" + url.QueryEscape(ref.Token)
	if err := p.doJSON(ctx, http.MethodGet, endpoint, accessToken, nil, &response); err != nil {
		return ref, err
	}
	docType := normalizeFeishuDocumentType(response.Data.Node.ObjType)
	if response.Code != 0 || response.Data.Node.ObjToken == "" || (docType != "doc" && docType != "docx") {
		return ref, errors.New("the wiki node is not an exportable document")
	}
	ref.Token = response.Data.Node.ObjToken
	ref.Type = docType
	if ref.Title == "" {
		ref.Title = response.Data.Node.Title
	}
	return ref, nil
}

func validateFeishuDocumentRef(ref feishuDocumentRef) (feishuDocumentRef, error) {
	ref.Token = strings.TrimSpace(ref.Token)
	ref.Type = normalizeFeishuDocumentType(ref.Type)
	ref.Title = strings.TrimSpace(ref.Title)
	ref.URL = strings.TrimSpace(ref.URL)
	if ref.URL != "" {
		parsed, err := parseFeishuDocumentURL(ref.URL)
		if err != nil {
			return ref, errFeishuInvalidDocument
		}
		if ref.Token == "" {
			ref.Token = parsed.Token
		} else if ref.Token != parsed.Token {
			return ref, errFeishuInvalidDocument
		}
		if ref.Type == "" {
			ref.Type = parsed.Type
		} else if ref.Type != parsed.Type {
			return ref, errFeishuInvalidDocument
		}
		ref.URL = parsed.URL
	}
	if !validFeishuDocumentToken(ref.Token) || ref.Type == "" {
		return ref, errFeishuInvalidDocument
	}
	return ref, nil
}

func (p *httpFeishuProvider) ExportDocument(ctx context.Context, accessToken string, ref feishuDocumentRef) (feishuExportedFile, error) {
	startedAt := time.Now()
	metrics := feishuExportMetrics{}
	resolveStarted := time.Now()
	reportFeishuExportProgress(ctx, "resolve", 0, 1, "document")
	ref, err := validateFeishuDocumentRef(ref)
	if err != nil {
		return feishuExportedFile{}, err
	}
	if ref.Type == "wiki" {
		ref, err = p.resolveWikiDocument(ctx, accessToken, ref)
		if err != nil {
			return feishuExportedFile{}, err
		}
	}
	metrics.ResolveDuration = time.Since(resolveStarted)
	reportFeishuExportProgress(ctx, "resolve", 1, 1, "document")
	var createResponse struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Ticket string `json:"ticket"`
		} `json:"data"`
	}
	phaseStarted := time.Now()
	reportFeishuExportProgress(ctx, "create", 0, 1, "task")
	if err := p.doJSON(ctx, http.MethodPost, p.apiBaseURL+"/open-apis/drive/v1/export_tasks", accessToken, map[string]string{
		"file_extension": "docx",
		"token":          ref.Token,
		"type":           ref.Type,
	}, &createResponse); err != nil {
		return feishuExportedFile{}, err
	}
	metrics.CreateDuration = time.Since(phaseStarted)
	if createResponse.Code != 0 || createResponse.Data.Ticket == "" {
		return feishuExportedFile{}, errors.New("could not create feishu export task")
	}
	reportFeishuExportProgress(ctx, "create", 1, 1, "task")

	var fileToken string
	var fileName string
	var lastPollErr error
	successfulPolls := 0
	consecutivePollErrors := 0
	pollAttempts := p.exportPollAttempts
	if pollAttempts <= 0 {
		pollAttempts = 30
	}
	pollInterval := p.exportPollInterval
	if pollInterval < 0 {
		pollInterval = 0
	}
	maxPollErrors := p.exportMaxPollErrors
	if maxPollErrors <= 0 {
		maxPollErrors = 3
	}
	phaseStarted = time.Now()
	reportFeishuExportProgress(ctx, "export_wait", 0, 0, "")
	for attempt := 0; attempt < pollAttempts; attempt++ {
		metrics.PollAttempts = attempt + 1
		if attempt > 0 {
			if delay := feishuExportPollDelay(pollInterval, attempt); delay > 0 {
				select {
				case <-ctx.Done():
					return feishuExportedFile{}, ctx.Err()
				case <-time.After(delay):
				}
			}
		}
		var statusResponse struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Result struct {
					FileName    string `json:"file_name"`
					FileToken   string `json:"file_token"`
					JobErrorMsg string `json:"job_error_msg"`
					JobStatus   int    `json:"job_status"`
				} `json:"result"`
			} `json:"data"`
		}
		endpoint := p.apiBaseURL + "/open-apis/drive/v1/export_tasks/" +
			url.PathEscape(createResponse.Data.Ticket) + "?token=" + url.QueryEscape(ref.Token)
		statusCtx := ctx
		cancelStatus := func() {}
		if p.exportStatusTimeout > 0 {
			statusCtx, cancelStatus = context.WithTimeout(ctx, p.exportStatusTimeout)
		}
		statusErr := p.doJSON(statusCtx, http.MethodGet, endpoint, accessToken, nil, &statusResponse)
		cancelStatus()
		if statusErr != nil {
			// Feishu export is an asynchronous task. A short-lived status
			// lookup failure must not discard a healthy task that is still
			// running; keep the ticket alive and retry within the bounded poll
			// window, matching the official Lark CLI behavior.
			lastPollErr = statusErr
			consecutivePollErrors++
			if consecutivePollErrors >= maxPollErrors {
				return feishuExportedFile{}, fmt.Errorf("feishu export status unavailable: %w", lastPollErr)
			}
			continue
		}
		if statusResponse.Code != 0 {
			lastPollErr = fmt.Errorf(
				"feishu export status API rejected request: code=%d",
				statusResponse.Code,
			)
			consecutivePollErrors++
			if consecutivePollErrors >= maxPollErrors {
				return feishuExportedFile{}, fmt.Errorf("feishu export status unavailable: %w", lastPollErr)
			}
			continue
		}
		consecutivePollErrors = 0
		successfulPolls++
		result := statusResponse.Data.Result
		if result.FileToken != "" && result.JobStatus == 0 {
			fileToken = result.FileToken
			fileName = result.FileName
			reportFeishuExportProgress(ctx, "export_wait", 1, 1, "task")
			break
		}
		switch result.JobStatus {
		case 0, 1, 2:
			if result.JobErrorMsg != "" && !strings.EqualFold(result.JobErrorMsg, "success") {
				return feishuExportedFile{}, &feishuExportTaskError{
					JobStatus: result.JobStatus,
					Message:   result.JobErrorMsg,
				}
			}
		default:
			return feishuExportedFile{}, &feishuExportTaskError{
				JobStatus: result.JobStatus,
				Message:   feishuExportStatusLabel(result.JobStatus),
			}
		}
	}
	metrics.PollDuration = time.Since(phaseStarted)
	if fileToken == "" {
		if successfulPolls == 0 && lastPollErr != nil {
			return feishuExportedFile{}, fmt.Errorf("feishu export status unavailable: %w", lastPollErr)
		}
		return feishuExportedFile{}, errFeishuExportTimeout
	}

	downloadURL := p.apiBaseURL + "/open-apis/drive/v1/export_tasks/file/" + url.PathEscape(fileToken) + "/download"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return feishuExportedFile{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	phaseStarted = time.Now()
	resp, err := p.client.Do(req)
	if err != nil {
		return feishuExportedFile{}, fmt.Errorf("download Feishu export: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return feishuExportedFile{}, fmt.Errorf("feishu export download status %d", resp.StatusCode)
	}
	downloadTotal := resp.ContentLength
	if downloadTotal < 0 {
		downloadTotal = 0
	}
	if downloadTotal > 100<<20 {
		return feishuExportedFile{}, errors.New("feishu export is larger than 100 MB")
	}
	reportFeishuExportProgress(ctx, "download", 0, downloadTotal, "bytes")
	content, err := readBoundedBodyWithProgress(ctx, resp.Body, 100<<20, downloadTotal, func(done int64) {
		reportFeishuExportProgress(ctx, "download", done, downloadTotal, "bytes")
	})
	if err != nil {
		return feishuExportedFile{}, fmt.Errorf("feishu export is larger than 100 MB: %w", err)
	}
	if len(content) < 4 || !bytes.Equal(content[:4], []byte{'P', 'K', 3, 4}) {
		return feishuExportedFile{}, errors.New("feishu export did not return a DOCX file")
	}
	if strings.TrimSpace(fileName) == "" {
		fileName = ref.Title
	}
	fileName = sanitizeExportFilename(fileName)
	metrics.DownloadDuration = time.Since(phaseStarted)
	metrics.TotalDuration = time.Since(startedAt)
	return feishuExportedFile{Filename: fileName, Content: content, Metrics: metrics}, nil
}

func readBoundedBodyWithProgress(
	ctx context.Context,
	body io.Reader,
	maxBytes int64,
	total int64,
	report func(done int64),
) ([]byte, error) {
	if maxBytes < 0 {
		return nil, errOutboundResponseTooLarge
	}
	limited := &io.LimitedReader{R: body, N: maxBytes + 1}
	var out bytes.Buffer
	buffer := make([]byte, 64<<10)
	var done int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		read, readErr := limited.Read(buffer)
		if read > 0 {
			if _, err := out.Write(buffer[:read]); err != nil {
				return nil, err
			}
			done += int64(read)
			if done > maxBytes {
				return nil, errOutboundResponseTooLarge
			}
			if report != nil {
				report(done)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	if total > 0 && done != total {
		return nil, io.ErrUnexpectedEOF
	}
	return out.Bytes(), nil
}

func sanitizeExportFilename(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(raw, "\r", ""), "\n", ""))
	raw = path.Base(strings.ReplaceAll(raw, "\\", "/"))
	raw = strings.TrimSuffix(raw, ".docx")
	if raw == "" || raw == "." {
		raw = "飞书文档"
	}
	runes := []rune(raw)
	if len(runes) > 120 {
		raw = string(runes[:120])
	}
	return raw + ".docx"
}
