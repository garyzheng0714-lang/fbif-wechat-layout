package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type controlledFeishuProvider struct {
	export func(context.Context, string, feishuDocumentRef) (feishuExportedFile, error)
}

func (p *controlledFeishuProvider) ExchangeCode(context.Context, string, string) (feishuUserToken, error) {
	return feishuUserToken{}, nil
}

func (p *controlledFeishuProvider) RefreshUserToken(context.Context, string) (feishuUserToken, error) {
	return feishuUserToken{}, nil
}

func (p *controlledFeishuProvider) GetUserProfile(context.Context, string) (feishuUserProfile, error) {
	return feishuUserProfile{}, nil
}

func (p *controlledFeishuProvider) SearchDocuments(context.Context, string, string, int) ([]feishuDocument, error) {
	return nil, nil
}

func (p *controlledFeishuProvider) ExportDocument(
	ctx context.Context,
	accessToken string,
	ref feishuDocumentRef,
) (feishuExportedFile, error) {
	return p.export(ctx, accessToken, ref)
}

func feishuSessionForTestUser(t *testing.T, service *authService, id string) string {
	t.Helper()
	return mustSessionForUser(t, service, authUser{
		ID:                     id,
		Name:                   id,
		AuthMode:               authModeFeishu,
		Status:                 authUserActive,
		FeishuOpenID:           strings.TrimPrefix(id, "feishu:"),
		FeishuRefreshTokenSeal: "test-refresh-seal",
	})
}

func createFeishuExportJobForTest(
	t *testing.T,
	mux *http.ServeMux,
	session string,
	token string,
) feishuExportJobStatus {
	t.Helper()
	rec := httptest.NewRecorder()
	body := []byte(`{"token":"` + token + `","type":"docx","title":"测试文章"}`)
	mux.ServeHTTP(rec, requestWithSession(http.MethodPost, "/api/feishu/docs/export/jobs", session, body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var status feishuExportJobStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if status.JobID == "" || status.StatusURL == "" || status.ResultURL == "" || status.Version < 1 {
		t.Fatalf("incomplete create response: %+v", status)
	}
	if got := rec.Header().Get("Location"); got != status.StatusURL {
		t.Fatalf("location=%q status_url=%q", got, status.StatusURL)
	}
	return status
}

func requestWithSessionCookie(method, target, token string, body []byte, contentType, origin string) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	if token != "" {
		req.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: token, Path: "/"})
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}

type blockingResultWriter struct {
	header  http.Header
	status  int
	started chan struct{}
	unblock chan struct{}
	once    sync.Once
	body    bytes.Buffer
}

func newBlockingResultWriter() *blockingResultWriter {
	return &blockingResultWriter{
		header:  make(http.Header),
		started: make(chan struct{}),
		unblock: make(chan struct{}),
	}
}

func (w *blockingResultWriter) Header() http.Header {
	return w.header
}

func (w *blockingResultWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *blockingResultWriter) Write(payload []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.once.Do(func() { close(w.started) })
	<-w.unblock
	return w.body.Write(payload)
}

type shortResultWriter struct {
	header http.Header
	status int
}

func (w *shortResultWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *shortResultWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *shortResultWriter) Write(payload []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if len(payload) == 0 {
		return 0, nil
	}
	return len(payload) - 1, nil
}

func waitForManagerJobState(
	t *testing.T,
	manager *feishuExportJobManager,
	userID, jobID, want string,
) feishuExportJobStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, _, ok := manager.status(userID, jobID)
		if !ok {
			t.Fatalf("job %s disappeared while waiting for %s", jobID, want)
		}
		if status.State == want {
			return status
		}
		if status.State == "failed" || status.State == "canceled" || status.State == "completed" {
			t.Fatalf("job reached %s, want %s: %+v", status.State, want, status)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("job %s did not reach %s", jobID, want)
	return feishuExportJobStatus{}
}

func waitForFeishuJobState(
	t *testing.T,
	mux *http.ServeMux,
	session string,
	status feishuExportJobStatus,
	want string,
) feishuExportJobStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rec := httptest.NewRecorder()
		target := status.StatusURL + "?after=" + strconv.FormatInt(status.Version, 10) + "&wait_ms=250"
		mux.ServeHTTP(rec, requestWithSession(http.MethodGet, target, session, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status request=%d body=%s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
			t.Fatalf("decode status: %v", err)
		}
		if status.State == want {
			return status
		}
		if status.State == "failed" || status.State == "canceled" || status.State == "completed" {
			t.Fatalf("job reached %s, want %s: %+v", status.State, want, status)
		}
	}
	t.Fatalf("job did not reach %s: %+v", want, status)
	return feishuExportJobStatus{}
}

func TestFeishuExportMutationsRequireSameOriginCookieAndJSON(t *testing.T) {
	provider := &fakeFeishuProvider{
		exportResult: feishuExportedFile{
			Filename: "安全测试.docx",
			Content:  []byte{'P', 'K', 3, 4},
		},
	}
	service := newTestAuthService(t, provider)
	session := feishuSessionForTestUser(t, service, "feishu:ou_csrf")
	service.putAccessToken("feishu:ou_csrf", "u-test", service.now().Add(time.Hour))
	mux := registerTestAuthMux(service)
	body := []byte(`{"token":"doxcn_csrf","type":"docx","title":"安全测试"}`)

	crossOrigin := httptest.NewRecorder()
	mux.ServeHTTP(crossOrigin, requestWithSessionCookie(
		http.MethodPost,
		"/api/feishu/docs/export/jobs",
		session,
		body,
		"application/json",
		"http://evil.example.com",
	))
	if crossOrigin.Code != http.StatusForbidden || !strings.Contains(crossOrigin.Body.String(), `"code":"invalid_request_origin"`) {
		t.Fatalf("cross-origin create status=%d body=%s", crossOrigin.Code, crossOrigin.Body.String())
	}

	plainText := httptest.NewRecorder()
	mux.ServeHTTP(plainText, requestWithSessionCookie(
		http.MethodPost,
		"/api/feishu/docs/export/jobs",
		session,
		body,
		"text/plain",
		"http://example.com",
	))
	if plainText.Code != http.StatusUnsupportedMediaType || !strings.Contains(plainText.Body.String(), `"code":"invalid_content_type"`) {
		t.Fatalf("plain-text create status=%d body=%s", plainText.Code, plainText.Body.String())
	}

	created := httptest.NewRecorder()
	mux.ServeHTTP(created, requestWithSessionCookie(
		http.MethodPost,
		"/api/feishu/docs/export/jobs",
		session,
		body,
		"application/json; charset=utf-8",
		"http://example.com",
	))
	if created.Code != http.StatusAccepted {
		t.Fatalf("same-origin create status=%d body=%s", created.Code, created.Body.String())
	}
	var status feishuExportJobStatus
	if err := json.Unmarshal(created.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}

	crossOriginDelete := httptest.NewRecorder()
	mux.ServeHTTP(crossOriginDelete, requestWithSessionCookie(
		http.MethodDelete,
		status.StatusURL,
		session,
		nil,
		"",
		"http://evil.example.com",
	))
	if crossOriginDelete.Code != http.StatusForbidden {
		t.Fatalf("cross-origin delete status=%d body=%s", crossOriginDelete.Code, crossOriginDelete.Body.String())
	}

	sameOriginDelete := httptest.NewRecorder()
	mux.ServeHTTP(sameOriginDelete, requestWithSessionCookie(
		http.MethodDelete,
		status.StatusURL,
		session,
		nil,
		"",
		"http://example.com",
	))
	if sameOriginDelete.Code != http.StatusOK {
		t.Fatalf("same-origin delete status=%d body=%s", sameOriginDelete.Code, sameOriginDelete.Body.String())
	}
}

func TestFeishuExportJobLifecyclePreservesDownloadContract(t *testing.T) {
	docx := []byte{'P', 'K', 3, 4, 'j', 'o', 'b'}
	provider := &fakeFeishuProvider{
		exportResult: feishuExportedFile{
			Filename: "任务文档.docx",
			Content:  docx,
		},
	}
	service := newTestAuthService(t, provider)
	session := feishuSessionForTestUser(t, service, "feishu:ou_jobs")
	service.putAccessToken("feishu:ou_jobs", "u-test", service.now().Add(time.Hour))
	mux := registerTestAuthMux(service)

	status := createFeishuExportJobForTest(t, mux, session, "doxcn_jobs")
	status = waitForFeishuJobState(t, mux, session, status, "completed")
	if status.Phase != "finalize" || status.Done != 1 || status.Total != 1 || status.Unit != "document" {
		t.Fatalf("final status is not an atomic ready event: %+v", status)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, requestWithSession(http.MethodGet, status.ResultURL, session, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("result status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), docx) {
		t.Fatalf("result bytes=%q want=%q", rec.Body.Bytes(), docx)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(docx)) {
		t.Fatalf("content-length=%q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "filename*=UTF-8''") {
		t.Fatalf("content-disposition=%q", got)
	}
	for _, name := range []string{
		"X-FBIF-Docx-Original-Bytes",
		"X-FBIF-Docx-Optimized-Bytes",
		"X-FBIF-Images-Deferred",
		"X-FBIF-Images-Mirrored",
		"X-FBIF-Image-Count",
	} {
		if rec.Header().Get(name) == "" {
			t.Fatalf("missing result header %s", name)
		}
	}
}

func TestFeishuExportJobResultRateLimitHasStableRetryContract(t *testing.T) {
	provider := &fakeFeishuProvider{
		exportResult: feishuExportedFile{
			Filename: "rate-limit.docx",
			Content:  []byte{'P', 'K', 3, 4, 'r', 'a', 't', 'e'},
		},
	}
	service := newTestAuthService(t, provider)
	service.feishuExportJobs.resultRateLimit = 1
	session := feishuSessionForTestUser(t, service, "feishu:ou_result_rate")
	service.putAccessToken("feishu:ou_result_rate", "u-test", service.now().Add(time.Hour))
	mux := registerTestAuthMux(service)

	status := createFeishuExportJobForTest(t, mux, session, "doxcn_result_rate")
	waitForManagerJobState(t, service.feishuExportJobs, "feishu:ou_result_rate", status.JobID, "completed")

	first := httptest.NewRecorder()
	mux.ServeHTTP(first, requestWithSession(http.MethodGet, status.ResultURL, session, nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first result status=%d body=%s", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	mux.ServeHTTP(second, requestWithSession(http.MethodGet, status.ResultURL, session, nil))
	if second.Code != http.StatusTooManyRequests ||
		!strings.Contains(second.Body.String(), `"code":"feishu_export_result_rate_limited"`) {
		t.Fatalf("rate-limited result status=%d body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("Retry-After"); got == "" {
		t.Fatal("rate-limited result response is missing Retry-After")
	}
}

func TestFeishuExportJobResultGlobalConcurrencyLimit(t *testing.T) {
	content := []byte{'P', 'K', 3, 4, 'c', 'o', 'n', 'c', 'u', 'r', 'r', 'e', 'n', 't'}
	provider := &fakeFeishuProvider{
		exportResult: feishuExportedFile{Filename: "concurrent.docx", Content: content},
	}
	service := newTestAuthService(t, provider)
	service.feishuExportJobs.resultSlots = make(chan struct{}, 1)
	service.feishuExportJobs.resultRateLimit = 100
	firstSession := feishuSessionForTestUser(t, service, "feishu:ou_result_first")
	secondSession := feishuSessionForTestUser(t, service, "feishu:ou_result_second")
	service.putAccessToken("feishu:ou_result_first", "first-token", service.now().Add(time.Hour))
	service.putAccessToken("feishu:ou_result_second", "second-token", service.now().Add(time.Hour))
	mux := registerTestAuthMux(service)

	firstStatus := createFeishuExportJobForTest(t, mux, firstSession, "doxcn_result_first")
	secondStatus := createFeishuExportJobForTest(t, mux, secondSession, "doxcn_result_second")
	waitForManagerJobState(t, service.feishuExportJobs, "feishu:ou_result_first", firstStatus.JobID, "completed")
	waitForManagerJobState(t, service.feishuExportJobs, "feishu:ou_result_second", secondStatus.JobID, "completed")

	blocked := newBlockingResultWriter()
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		mux.ServeHTTP(
			blocked,
			requestWithSession(http.MethodGet, firstStatus.ResultURL, firstSession, nil),
		)
	}()
	var unblockOnce sync.Once
	unblock := func() {
		unblockOnce.Do(func() { close(blocked.unblock) })
	}
	defer unblock()
	select {
	case <-blocked.started:
	case <-time.After(time.Second):
		t.Fatal("first result transfer did not start")
	}

	second := httptest.NewRecorder()
	mux.ServeHTTP(second, requestWithSession(http.MethodGet, secondStatus.ResultURL, secondSession, nil))
	if second.Code != http.StatusServiceUnavailable ||
		!strings.Contains(second.Body.String(), `"code":"feishu_export_result_busy"`) {
		t.Fatalf("concurrent result status=%d body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("concurrent result Retry-After=%q want=1", got)
	}

	unblock()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first result transfer did not finish")
	}
	if blocked.status != http.StatusOK || !bytes.Equal(blocked.body.Bytes(), content) {
		t.Fatalf("first result status=%d body=%q", blocked.status, blocked.body.Bytes())
	}
	if got := len(service.feishuExportJobs.resultSlots); got != 0 {
		t.Fatalf("result transfer slot leaked: %d", got)
	}
}

func TestFeishuExportJobResultPerUserConcurrencyLimit(t *testing.T) {
	manager := newFeishuExportJobManager(time.Now)
	manager.resultSlots = make(chan struct{}, 4)
	manager.maxResultWritesPerUser = 2
	manager.resultRateLimit = 100
	const userID = "feishu:ou_result_fairness"
	jobIDs := make([]string, 0, 3)
	for index := 0; index < 3; index++ {
		job, err := manager.create(userID, func(context.Context, feishuExportProgressReporter) (preparedFeishuDocument, error) {
			return preparedFeishuDocument{Content: []byte{'P', 'K', 3, 4}}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		waitForManagerJobState(t, manager, userID, job.id, "completed")
		jobIDs = append(jobIDs, job.id)
	}

	_, first, releaseFirst, ok, err := manager.resultForRequest(userID, jobIDs[0])
	if err != nil || !ok || first == nil || releaseFirst == nil {
		t.Fatalf("first result acquisition result=%v ok=%t err=%v", first, ok, err)
	}
	defer releaseFirst()
	_, second, releaseSecond, ok, err := manager.resultForRequest(userID, jobIDs[1])
	if err != nil || !ok || second == nil || releaseSecond == nil {
		t.Fatalf("second result acquisition result=%v ok=%t err=%v", second, ok, err)
	}
	defer releaseSecond()
	if _, _, _, ok, err := manager.resultForRequest(userID, jobIDs[2]); !ok || !errors.Is(err, errFeishuResultBusy) {
		t.Fatalf("third same-user acquisition ok=%t err=%v", ok, err)
	}
	if got := len(manager.resultSlots); got != 2 {
		t.Fatalf("same user occupied %d global slots, want 2", got)
	}

	releaseFirst()
	_, third, releaseThird, ok, err := manager.resultForRequest(userID, jobIDs[2])
	if err != nil || !ok || third == nil || releaseThird == nil {
		t.Fatalf("third retry acquisition result=%v ok=%t err=%v", third, ok, err)
	}
	releaseThird()
	releaseSecond()
	manager.mu.Lock()
	activeForUser := manager.resultWritesByUser[userID]
	manager.mu.Unlock()
	if activeForUser != 0 || len(manager.resultSlots) != 0 {
		t.Fatalf("result transfer counters leaked user=%d global=%d", activeForUser, len(manager.resultSlots))
	}
}

func TestFeishuExportJobShortWriteIsNotConsumedAndCanRetry(t *testing.T) {
	content := []byte{'P', 'K', 3, 4, 'r', 'e', 't', 'r', 'y'}
	provider := &fakeFeishuProvider{
		exportResult: feishuExportedFile{Filename: "retry.docx", Content: content},
	}
	service := newTestAuthService(t, provider)
	session := feishuSessionForTestUser(t, service, "feishu:ou_result_retry")
	service.putAccessToken("feishu:ou_result_retry", "u-test", service.now().Add(time.Hour))
	mux := registerTestAuthMux(service)

	status := createFeishuExportJobForTest(t, mux, session, "doxcn_result_retry")
	waitForManagerJobState(t, service.feishuExportJobs, "feishu:ou_result_retry", status.JobID, "completed")
	service.feishuExportJobs.mu.Lock()
	originalExpiry := service.feishuExportJobs.jobs[status.JobID].expiresAt
	service.feishuExportJobs.mu.Unlock()

	short := &shortResultWriter{}
	mux.ServeHTTP(short, requestWithSession(http.MethodGet, status.ResultURL, session, nil))
	service.feishuExportJobs.mu.Lock()
	job := service.feishuExportJobs.jobs[status.JobID]
	consumedAfterShortWrite := job.resultConsumed
	expiryAfterShortWrite := job.expiresAt
	service.feishuExportJobs.mu.Unlock()
	if consumedAfterShortWrite {
		t.Fatal("short result write was marked consumed")
	}
	if !expiryAfterShortWrite.Equal(originalExpiry) {
		t.Fatalf("short result write changed expiry=%s want=%s", expiryAfterShortWrite, originalExpiry)
	}
	if got := len(service.feishuExportJobs.resultSlots); got != 0 {
		t.Fatalf("short result write leaked transfer slot: %d", got)
	}

	retry := httptest.NewRecorder()
	mux.ServeHTTP(retry, requestWithSession(http.MethodGet, status.ResultURL, session, nil))
	if retry.Code != http.StatusOK || !bytes.Equal(retry.Body.Bytes(), content) {
		t.Fatalf("retry result status=%d body=%q", retry.Code, retry.Body.Bytes())
	}
	service.feishuExportJobs.mu.Lock()
	consumedAfterRetry := service.feishuExportJobs.jobs[status.JobID].resultConsumed
	service.feishuExportJobs.mu.Unlock()
	if !consumedAfterRetry {
		t.Fatal("successful retry was not marked consumed")
	}
}

func TestFeishuExportJobsAreOpaqueToOtherUsers(t *testing.T) {
	provider := &fakeFeishuProvider{
		exportResult: feishuExportedFile{Filename: "secret.docx", Content: []byte{'P', 'K', 3, 4}},
	}
	service := newTestAuthService(t, provider)
	ownerSession := feishuSessionForTestUser(t, service, "feishu:ou_owner")
	otherSession := feishuSessionForTestUser(t, service, "feishu:ou_other")
	service.putAccessToken("feishu:ou_owner", "owner-token", service.now().Add(time.Hour))
	service.putAccessToken("feishu:ou_other", "other-token", service.now().Add(time.Hour))
	mux := registerTestAuthMux(service)

	status := createFeishuExportJobForTest(t, mux, ownerSession, "doxcn_secret")
	status = waitForFeishuJobState(t, mux, ownerSession, status, "completed")
	for _, request := range []struct {
		method string
		target string
	}{
		{http.MethodGet, status.StatusURL},
		{http.MethodGet, status.ResultURL},
		{http.MethodDelete, status.StatusURL},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, requestWithSession(request.method, request.target, otherSession, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s status=%d body=%s", request.method, request.target, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "ou_owner") {
			t.Fatalf("cross-user response leaked job data: %s", rec.Body.String())
		}
	}
}

func TestFeishuExportJobLatestWinsCancelsUpstreamContext(t *testing.T) {
	firstStarted := make(chan struct{})
	firstCanceled := make(chan struct{})
	var calls atomic.Int32
	provider := &controlledFeishuProvider{
		export: func(ctx context.Context, _ string, _ feishuDocumentRef) (feishuExportedFile, error) {
			if calls.Add(1) == 1 {
				close(firstStarted)
				<-ctx.Done()
				close(firstCanceled)
				return feishuExportedFile{}, ctx.Err()
			}
			return feishuExportedFile{
				Filename: "latest.docx",
				Content:  []byte{'P', 'K', 3, 4, 'o', 'k'},
			}, nil
		},
	}
	service := newTestAuthService(t, provider)
	session := feishuSessionForTestUser(t, service, "feishu:ou_latest")
	service.putAccessToken("feishu:ou_latest", "u-test", service.now().Add(time.Hour))
	mux := registerTestAuthMux(service)

	first := createFeishuExportJobForTest(t, mux, session, "doxcn_first")
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first export did not start")
	}
	second := createFeishuExportJobForTest(t, mux, session, "doxcn_second")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, requestWithSession(http.MethodGet, first.StatusURL, session, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", rec.Code, rec.Body.String())
	}
	var canceled feishuExportJobStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &canceled); err != nil {
		t.Fatal(err)
	}
	if canceled.State != "canceled" || canceled.Code != "feishu_export_canceled" {
		t.Fatalf("first job was not canceled: %+v", canceled)
	}
	select {
	case <-firstCanceled:
	case <-time.After(time.Second):
		t.Fatal("latest-wins did not cancel the first provider context")
	}
	second = waitForFeishuJobState(t, mux, session, second, "completed")
	if second.Phase != "finalize" {
		t.Fatalf("second job=%+v", second)
	}
}

func TestFeishuExportJobDeleteCancelsUpstreamAndResultStaysUnavailable(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	provider := &controlledFeishuProvider{
		export: func(ctx context.Context, _ string, _ feishuDocumentRef) (feishuExportedFile, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			return feishuExportedFile{}, ctx.Err()
		},
	}
	service := newTestAuthService(t, provider)
	session := feishuSessionForTestUser(t, service, "feishu:ou_delete")
	service.putAccessToken("feishu:ou_delete", "u-test", service.now().Add(time.Hour))
	mux := registerTestAuthMux(service)
	status := createFeishuExportJobForTest(t, mux, session, "doxcn_delete")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("export did not start")
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, requestWithSession(http.MethodDelete, status.StatusURL, session, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	var deleted feishuExportJobStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatal(err)
	}
	if deleted.State != "canceled" || deleted.Code != "feishu_export_canceled" {
		t.Fatalf("delete response=%+v", deleted)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("DELETE did not cancel the provider context")
	}

	resultRec := httptest.NewRecorder()
	mux.ServeHTTP(resultRec, requestWithSession(http.MethodGet, status.ResultURL, session, nil))
	if resultRec.Code != http.StatusConflict ||
		!strings.Contains(resultRec.Body.String(), `"code":"feishu_export_canceled"`) {
		t.Fatalf("canceled result status=%d body=%s", resultRec.Code, resultRec.Body.String())
	}
}

func TestFeishuExportJobStatusLongPollWaitsForNewVersion(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	provider := &controlledFeishuProvider{
		export: func(ctx context.Context, _ string, _ feishuDocumentRef) (feishuExportedFile, error) {
			close(started)
			select {
			case <-ctx.Done():
				return feishuExportedFile{}, ctx.Err()
			case <-release:
				return feishuExportedFile{
					Filename: "long-poll.docx",
					Content:  []byte{'P', 'K', 3, 4},
				}, nil
			}
		},
	}
	service := newTestAuthService(t, provider)
	session := feishuSessionForTestUser(t, service, "feishu:ou_poll")
	service.putAccessToken("feishu:ou_poll", "u-test", service.now().Add(time.Hour))
	mux := registerTestAuthMux(service)
	status := createFeishuExportJobForTest(t, mux, session, "doxcn_poll")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("export did not start")
	}

	currentRec := httptest.NewRecorder()
	mux.ServeHTTP(currentRec, requestWithSession(http.MethodGet, status.StatusURL, session, nil))
	if err := json.Unmarshal(currentRec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		target := status.StatusURL + "?after=" + strconv.FormatInt(status.Version, 10) + "&wait_ms=1000"
		mux.ServeHTTP(rec, requestWithSession(http.MethodGet, target, session, nil))
		done <- rec
	}()
	select {
	case <-done:
		t.Fatal("long poll returned before a new version existed")
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("long poll status=%d body=%s", rec.Code, rec.Body.String())
		}
		var updated feishuExportJobStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
			t.Fatal(err)
		}
		if updated.Version <= status.Version {
			t.Fatalf("long poll returned stale version: before=%d after=%d", status.Version, updated.Version)
		}
	case <-time.After(time.Second):
		t.Fatal("long poll did not wake for a progress update")
	}
}

func TestFeishuExportJobStatusRateAndWaiterLimits(t *testing.T) {
	started := make(chan struct{})
	releaseProvider := make(chan struct{})
	provider := &controlledFeishuProvider{
		export: func(ctx context.Context, _ string, _ feishuDocumentRef) (feishuExportedFile, error) {
			close(started)
			select {
			case <-ctx.Done():
				return feishuExportedFile{}, ctx.Err()
			case <-releaseProvider:
				return feishuExportedFile{
					Filename: "limited.docx",
					Content:  []byte{'P', 'K', 3, 4},
				}, nil
			}
		},
	}
	service := newTestAuthService(t, provider)
	session := feishuSessionForTestUser(t, service, "feishu:ou_limits")
	service.putAccessToken("feishu:ou_limits", "u-test", service.now().Add(time.Hour))
	service.feishuExportJobs.maxWaiters = 1
	service.feishuExportJobs.maxWaitersPerUser = 1
	service.feishuExportJobs.maxWaitersPerJob = 1
	mux := registerTestAuthMux(service)
	status := createFeishuExportJobForTest(t, mux, session, "doxcn_limits")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("export did not start")
	}
	status, _, _ = service.feishuExportJobs.status("feishu:ou_limits", status.JobID)

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	target := status.StatusURL + "?after=" + strconv.FormatInt(status.Version, 10) + "&wait_ms=1000"
	go func() {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, requestWithSession(http.MethodGet, target, session, nil))
		firstDone <- rec
	}()
	deadline := time.Now().Add(time.Second)
	waiterReady := false
	for time.Now().Before(deadline) {
		service.feishuExportJobs.mu.Lock()
		waiters := service.feishuExportJobs.waiters
		service.feishuExportJobs.mu.Unlock()
		if waiters == 1 {
			waiterReady = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !waiterReady {
		t.Fatal("first waiter did not acquire its bounded slot")
	}

	second := httptest.NewRecorder()
	mux.ServeHTTP(second, requestWithSession(http.MethodGet, target, session, nil))
	if second.Code != http.StatusTooManyRequests ||
		!strings.Contains(second.Body.String(), `"code":"feishu_export_status_busy"`) {
		t.Fatalf("second waiter status=%d body=%s", second.Code, second.Body.String())
	}

	close(releaseProvider)
	select {
	case first := <-firstDone:
		if first.Code != http.StatusOK {
			t.Fatalf("first waiter status=%d body=%s", first.Code, first.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("first waiter did not finish")
	}

	service.feishuExportJobs.mu.Lock()
	service.feishuExportJobs.statusWindows = make(map[string]feishuExportRateWindow)
	service.feishuExportJobs.statusRateLimit = 1
	service.feishuExportJobs.mu.Unlock()
	rateTarget := status.StatusURL
	firstPoll := httptest.NewRecorder()
	mux.ServeHTTP(firstPoll, requestWithSession(http.MethodGet, rateTarget, session, nil))
	if firstPoll.Code != http.StatusOK {
		t.Fatalf("first status poll=%d body=%s", firstPoll.Code, firstPoll.Body.String())
	}
	secondPoll := httptest.NewRecorder()
	mux.ServeHTTP(secondPoll, requestWithSession(http.MethodGet, rateTarget, session, nil))
	if secondPoll.Code != http.StatusTooManyRequests ||
		!strings.Contains(secondPoll.Body.String(), `"code":"feishu_export_status_rate_limited"`) {
		t.Fatalf("rate-limited poll=%d body=%s", secondPoll.Code, secondPoll.Body.String())
	}
}

func TestFeishuExportJobFailureHasStablePublicError(t *testing.T) {
	provider := &fakeFeishuProvider{
		exportErr: &feishuExportTaskError{JobStatus: 110, Message: "upstream secret diagnostics"},
	}
	service := newTestAuthService(t, provider)
	session := feishuSessionForTestUser(t, service, "feishu:ou_failure")
	service.putAccessToken("feishu:ou_failure", "u-test", service.now().Add(time.Hour))
	mux := registerTestAuthMux(service)
	status := createFeishuExportJobForTest(t, mux, session, "doxcn_denied")
	status = waitForFeishuJobState(t, mux, session, status, "failed")
	if status.Code != "feishu_document_permission_denied" || status.Error != "当前账号没有导出这篇文档的权限" {
		t.Fatalf("failure=%+v", status)
	}
	if strings.Contains(status.Error, "secret") {
		t.Fatalf("failure leaked upstream diagnostics: %+v", status)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, requestWithSession(http.MethodGet, status.ResultURL, session, nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"feishu_document_permission_denied"`) {
		t.Fatalf("failed result status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFeishuExportJobTTLExpiresResult(t *testing.T) {
	provider := &fakeFeishuProvider{
		exportResult: feishuExportedFile{Filename: "ttl.docx", Content: []byte{'P', 'K', 3, 4}},
	}
	service := newTestAuthService(t, provider)
	session := feishuSessionForTestUser(t, service, "feishu:ou_ttl")
	service.putAccessToken("feishu:ou_ttl", "u-test", service.now().Add(time.Hour))
	mux := registerTestAuthMux(service)
	status := createFeishuExportJobForTest(t, mux, session, "doxcn_ttl")
	status = waitForFeishuJobState(t, mux, session, status, "completed")

	expiredAt := service.now().Add(feishuExportJobTTL + time.Second)
	service.feishuExportJobs.mu.Lock()
	service.feishuExportJobs.now = func() time.Time { return expiredAt }
	service.feishuExportJobs.mu.Unlock()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, requestWithSession(http.MethodGet, status.ResultURL, session, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expired result status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.feishuExportJobs.resultBytes != 0 {
		t.Fatalf("expired result bytes retained=%d", service.feishuExportJobs.resultBytes)
	}
}

func TestFeishuExportCreateRateLimitDoesNotCancelCurrentJob(t *testing.T) {
	manager := newFeishuExportJobManager(time.Now)
	manager.createRateLimit = 2
	manager.maxJobs = 8
	manager.maxJobsPerUser = 4
	blockUntilCanceled := func(ctx context.Context, _ feishuExportProgressReporter) (preparedFeishuDocument, error) {
		<-ctx.Done()
		return preparedFeishuDocument{}, ctx.Err()
	}
	first, err := manager.create("rate-user", blockUntilCanceled)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.create("rate-user", blockUntilCanceled)
	if err != nil {
		t.Fatal(err)
	}
	third, err := manager.create("rate-user", blockUntilCanceled)
	var rateErr *feishuExportRateLimitError
	if !errors.As(err, &rateErr) {
		t.Fatalf("third create err=%v, want rate limit", err)
	}
	if third != nil {
		t.Fatalf("rate-limited create returned job=%+v", third)
	}
	status, _, ok := manager.status("rate-user", second.id)
	if !ok || status.State == "canceled" {
		t.Fatalf("rate-limited replacement canceled current job: %+v", status)
	}
	manager.cancel("rate-user", second.id)
	firstStatus, _, _ := manager.status("rate-user", first.id)
	if firstStatus.State != "canceled" {
		t.Fatalf("latest-wins did not cancel first job: %+v", firstStatus)
	}
}

func TestFeishuExportPerUserBoundNeverEvictsAnotherUsersResult(t *testing.T) {
	manager := newFeishuExportJobManager(time.Now)
	manager.maxJobs = 3
	manager.maxJobsPerUser = 2
	manager.createRateLimit = 100
	manager.slots = make(chan struct{}, 2)
	victim, err := manager.create("victim", func(context.Context, feishuExportProgressReporter) (preparedFeishuDocument, error) {
		return preparedFeishuDocument{Content: []byte("safe")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForManagerJobState(t, manager, "victim", victim.id, "completed")

	blockUntilCanceled := func(ctx context.Context, _ feishuExportProgressReporter) (preparedFeishuDocument, error) {
		<-ctx.Done()
		return preparedFeishuDocument{}, ctx.Err()
	}
	var latest *feishuExportJob
	for index := 0; index < 12; index++ {
		latest, err = manager.create("attacker", blockUntilCanceled)
		if err != nil {
			t.Fatalf("attacker create %d: %v", index, err)
		}
	}
	status, result, ok := manager.result("victim", victim.id)
	if !ok || status.State != "completed" || result == nil || string(result.Content) != "safe" {
		t.Fatalf("attacker churn evicted victim result: status=%+v result=%v ok=%t", status, result, ok)
	}
	if latest != nil {
		manager.cancel("attacker", latest.id)
	}
	manager.mu.Lock()
	attackerJobs := manager.userJobCountLocked("attacker")
	totalJobs := len(manager.jobs)
	manager.mu.Unlock()
	if attackerJobs > manager.maxJobsPerUser || totalJobs > manager.maxJobs {
		t.Fatalf("job bounds violated: attacker=%d total=%d", attackerJobs, totalJobs)
	}
}

func TestFeishuExportTimeoutCannotBecomeCompleted(t *testing.T) {
	manager := newFeishuExportJobManager(time.Now)
	manager.runTimeout = 20 * time.Millisecond
	job, err := manager.create("timeout-user", func(ctx context.Context, _ feishuExportProgressReporter) (preparedFeishuDocument, error) {
		<-ctx.Done()
		// A lower layer may try to degrade after cancellation. The manager must
		// still make the deadline authoritative.
		return preparedFeishuDocument{Content: []byte("late-success")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	status := waitForManagerJobState(t, manager, "timeout-user", job.id, "failed")
	if status.Code != "feishu_export_timeout" || status.State == "completed" {
		t.Fatalf("deadline became a success: %+v", status)
	}
}

func TestFeishuExportPeriodicGCCleansConsumedResult(t *testing.T) {
	var nowNanos atomic.Int64
	startedAt := time.Now().UTC()
	nowNanos.Store(startedAt.UnixNano())
	manager := newFeishuExportJobManager(func() time.Time {
		return time.Unix(0, nowNanos.Load()).UTC()
	})
	manager.ttl = time.Minute
	manager.consumedTTL = 10 * time.Second
	manager.startGC(2 * time.Millisecond)
	defer manager.stopGC()
	job, err := manager.create("gc-user", func(context.Context, feishuExportProgressReporter) (preparedFeishuDocument, error) {
		return preparedFeishuDocument{Content: []byte("temporary")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForManagerJobState(t, manager, "gc-user", job.id, "completed")
	if _, result, ok := manager.result("gc-user", job.id); !ok || result == nil {
		t.Fatal("completed result was not available")
	}
	manager.mu.Lock()
	unconsumedExpiry := manager.jobs[job.id].expiresAt
	manager.mu.Unlock()
	if !unconsumedExpiry.Equal(startedAt.Add(manager.ttl)) {
		t.Fatalf("reading result shortened TTL before a successful write: %s", unconsumedExpiry)
	}
	if !manager.markResultConsumed("gc-user", job.id) {
		t.Fatal("successful result consumption was not recorded")
	}
	manager.mu.Lock()
	expiresAt := manager.jobs[job.id].expiresAt
	manager.mu.Unlock()
	if !expiresAt.Equal(startedAt.Add(manager.consumedTTL)) {
		t.Fatalf("consumed expiry=%s want=%s", expiresAt, startedAt.Add(manager.consumedTTL))
	}

	nowNanos.Store(startedAt.Add(manager.consumedTTL + time.Second).UnixNano())
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		_, exists := manager.jobs[job.id]
		resultBytes := manager.resultBytes
		manager.mu.Unlock()
		if !exists {
			if resultBytes != 0 {
				t.Fatalf("GC left result bytes=%d", resultBytes)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("periodic GC did not remove consumed result")
}

func TestFeishuExportJobManagerBoundsActiveJobsAndResultBytes(t *testing.T) {
	manager := newFeishuExportJobManager(time.Now)
	manager.maxJobs = 1
	manager.maxResultBytes = 4
	manager.slots = make(chan struct{}, 1)
	manager.runTimeout = time.Second
	block := make(chan struct{})
	first, err := manager.create("user-1", func(ctx context.Context, _ feishuExportProgressReporter) (preparedFeishuDocument, error) {
		select {
		case <-ctx.Done():
			return preparedFeishuDocument{}, ctx.Err()
		case <-block:
			return preparedFeishuDocument{Content: []byte("1234")}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.create("user-2", func(context.Context, feishuExportProgressReporter) (preparedFeishuDocument, error) {
		return preparedFeishuDocument{}, nil
	}); err == nil {
		t.Fatal("manager accepted more active jobs than its bound")
	}
	close(block)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, _, ok := manager.status("user-1", first.id)
		if ok && status.State == "completed" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if manager.resultBytes != 4 {
		t.Fatalf("result bytes=%d want=4", manager.resultBytes)
	}

	manager.maxJobs = 2
	oversized, err := manager.create("user-2", func(context.Context, feishuExportProgressReporter) (preparedFeishuDocument, error) {
		return preparedFeishuDocument{Content: []byte("12345")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, _, ok := manager.status("user-2", oversized.id)
		if ok && status.State == "failed" {
			if status.Code != "feishu_export_result_unavailable" {
				t.Fatalf("oversized result status=%+v", status)
			}
			firstStatus, firstResult, firstOK := manager.result("user-1", first.id)
			if !firstOK || firstStatus.State != "completed" || firstResult == nil || string(firstResult.Content) != "1234" {
				t.Fatalf("new oversized result evicted existing result: status=%+v result=%v ok=%t", firstStatus, firstResult, firstOK)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("oversized result was not rejected")
}

func TestHTTPFeishuProviderReportsOnlyRealProgressSignals(t *testing.T) {
	docx := []byte{'P', 'K', 3, 4, 'd', 'o', 'c', 'x'}
	statusCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/wiki/v2/spaces/get_node":
			writeJSON(w, http.StatusOK, map[string]any{
				"code": 0,
				"data": map[string]any{"node": map[string]any{
					"obj_token": "doxcn_resolved",
					"obj_type":  "docx",
				}},
			})
		case r.URL.Path == "/open-apis/drive/v1/export_tasks" && r.Method == http.MethodPost:
			writeJSON(w, http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"ticket": "ticket"}})
		case r.URL.Path == "/open-apis/drive/v1/export_tasks/ticket":
			statusCalls++
			if statusCalls == 1 {
				writeJSON(w, http.StatusOK, map[string]any{
					"code": 0,
					"data": map[string]any{"result": map[string]any{"job_status": 2}},
				})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"code": 0,
				"data": map[string]any{"result": map[string]any{
					"file_token": "file",
					"job_status": 0,
				}},
			})
		case r.URL.Path == "/open-apis/drive/v1/export_tasks/file/file/download":
			w.Header().Set("Content-Length", strconv.Itoa(len(docx)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(docx[:4])
			_, _ = w.Write(docx[4:])
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer server.Close()
	provider := newHTTPFeishuProvider(server.Client())
	provider.apiBaseURL = server.URL
	provider.exportPollInterval = 0

	var mu sync.Mutex
	var events []feishuExportProgress
	ctx := withFeishuExportProgress(context.Background(), func(event feishuExportProgress) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	if _, err := provider.ExportDocument(ctx, "u-token", feishuDocumentRef{
		Token: "wikcn_test",
		Type:  "wiki",
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	assertProgressEvent(t, events, "resolve", 0, 1, "document")
	assertProgressEvent(t, events, "resolve", 1, 1, "document")
	assertProgressEvent(t, events, "create", 0, 1, "task")
	assertProgressEvent(t, events, "create", 1, 1, "task")
	assertProgressEvent(t, events, "export_wait", 0, 0, "")
	assertProgressEvent(t, events, "export_wait", 1, 1, "task")
	assertProgressEvent(t, events, "download", 0, int64(len(docx)), "bytes")
	assertProgressEvent(t, events, "download", int64(len(docx)), int64(len(docx)), "bytes")
	for _, event := range events {
		if event.Phase == "export_wait" && event.Done > 0 && event.Total == 0 {
			t.Fatalf("waiting time or poll count was presented as progress: %+v", event)
		}
	}
}

func TestImageMirrorReportsEnumerationBatchAndRewriteBoundaries(t *testing.T) {
	original := zipFixture(t, map[string][]byte{
		"word/document.xml":            []byte(`<w:document/>`),
		"word/_rels/document.xml.rels": []byte(`<Relationships/>`),
		"word/media/a.png":             {0x89, 0x50, 1},
		"word/media/b.jpg":             {0xff, 0xd8, 2},
		"[Content_Types].xml":          []byte(`<Types/>`),
		"_rels/.rels":                  []byte(`<Relationships/>`),
	})
	optimized, err := stripImagesFromDocx(original)
	if err != nil {
		t.Fatal(err)
	}
	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Base64Images map[string]string `json:"base64_images"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		results := make(map[string]string, len(payload.Base64Images))
		for key := range payload.Base64Images {
			results[key] = "https://oss.example.test/" + url.PathEscape(key)
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	}))
	defer uploadServer.Close()
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", uploadServer.URL)

	var events []feishuExportProgress
	ctx := withFeishuExportProgress(context.Background(), func(event feishuExportProgress) {
		events = append(events, event)
	})
	if _, count, err := mirrorCachedDocxImagesToOSS(ctx, original, optimized); err != nil {
		t.Fatal(err)
	} else if count != 2 {
		t.Fatalf("mirrored count=%d", count)
	}
	assertProgressEvent(t, events, "image_enumerate", 0, 2, "images")
	assertProgressEvent(t, events, "image_enumerate", 2, 2, "images")
	assertProgressEvent(t, events, "image_upload", 0, 2, "images")
	assertProgressEvent(t, events, "image_upload", 2, 2, "images")
	assertProgressEvent(t, events, "image_map", 0, 1, "document")
	assertProgressEvent(t, events, "image_map", 1, 1, "document")
}

func TestDocxOptimizeReportsRealOperationCountsAndHonorsCancellation(t *testing.T) {
	docx := zipFixture(t, map[string][]byte{
		"word/document.xml":            []byte(`<w:document/>`),
		"word/_rels/document.xml.rels": []byte(`<Relationships/>`),
		"word/media/a.png":             {0x89, 0x50, 1},
		"[Content_Types].xml":          []byte(`<Types/>`),
		"_rels/.rels":                  []byte(`<Relationships/>`),
	})
	var events []feishuExportProgress
	ctx := withFeishuExportProgress(context.Background(), func(event feishuExportProgress) {
		events = append(events, event)
	})
	if _, err := stripImagesFromDocxWithProgress(ctx, docx); err != nil {
		t.Fatal(err)
	}
	total := int64(7)
	assertProgressEvent(t, events, "optimize", 0, total, "operations")
	assertProgressEvent(t, events, "optimize", total, total, "operations")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := stripImagesFromDocxWithProgress(canceled, docx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled optimize err=%v", err)
	}
}

func assertProgressEvent(
	t *testing.T,
	events []feishuExportProgress,
	phase string,
	done int64,
	total int64,
	unit string,
) {
	t.Helper()
	for _, event := range events {
		if event.Phase == phase && event.Done == done && event.Total == total && event.Unit == unit {
			return
		}
	}
	t.Fatalf("missing progress event phase=%s done=%d total=%d unit=%s events=%+v", phase, done, total, unit, events)
}

func TestReadBoundedBodyWithProgressHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := readBoundedBodyWithProgress(ctx, strings.NewReader("data"), 10, 4, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
