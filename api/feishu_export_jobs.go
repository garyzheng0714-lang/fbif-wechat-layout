package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	feishuExportJobTTL               = 15 * time.Minute
	feishuExportConsumedResultTTL    = time.Minute
	feishuExportJobRunTimeout        = 5 * time.Minute
	feishuExportJobGCInterval        = time.Minute
	feishuExportCreateRateWindow     = time.Minute
	feishuExportStatusRateWindow     = time.Minute
	feishuExportResultRateWindow     = time.Minute
	minFeishuStatusLongPollWait      = 250 * time.Millisecond
	maxFeishuStatusLongPollWait      = 25 * time.Second
	maxFeishuExportJobs              = 128
	maxFeishuExportJobsPerUser       = 4
	maxFeishuExportCreatesPerWindow  = 12
	maxFeishuStatusRequestsPerWindow = 600
	maxFeishuResultRequestsPerWindow = 12
	maxConcurrentFeishuExports       = 4
	maxConcurrentFeishuResultWrites  = 4
	maxFeishuResultWritesPerUser     = 2
	maxFeishuExportResultBytes       = 512 << 20
	maxFeishuStatusWaiters           = 64
	maxFeishuStatusWaitersPerUser    = 2
	maxFeishuStatusWaitersPerJob     = 2
)

var (
	errFeishuExportCapacity = errors.New("too many active Feishu export jobs")
	errFeishuStatusBusy     = errors.New("too many concurrent Feishu status waiters")
	errFeishuResultBusy     = errors.New("too many concurrent Feishu result transfers")
)

type feishuExportRateLimitError struct {
	scope      string
	retryAfter time.Duration
}

func (e *feishuExportRateLimitError) Error() string {
	return "Feishu export " + e.scope + " rate limit exceeded"
}

func (e *feishuExportRateLimitError) RetryAfter() time.Duration {
	if e == nil || e.retryAfter <= 0 {
		return time.Second
	}
	return e.retryAfter
}

type feishuExportRateWindow struct {
	started time.Time
	count   int
}

type feishuExportProgress struct {
	Phase string `json:"phase"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Unit  string `json:"unit"`
}

type feishuExportProgressReporter func(feishuExportProgress)

type feishuExportProgressContextKey struct{}

func withFeishuExportProgress(ctx context.Context, reporter feishuExportProgressReporter) context.Context {
	if reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, feishuExportProgressContextKey{}, reporter)
}

func reportFeishuExportProgress(ctx context.Context, phase string, done, total int64, unit string) {
	reporter, _ := ctx.Value(feishuExportProgressContextKey{}).(feishuExportProgressReporter)
	if reporter == nil {
		return
	}
	if done < 0 {
		done = 0
	}
	if total < 0 {
		total = 0
	}
	if total > 0 && done > total {
		done = total
	}
	reporter(feishuExportProgress{
		Phase: strings.TrimSpace(phase),
		Done:  done,
		Total: total,
		Unit:  strings.TrimSpace(unit),
	})
}

type preparedFeishuDocument struct {
	Filename         string
	Content          []byte
	Metrics          feishuExportMetrics
	OriginalBytes    int
	OptimizedBytes   int
	ImagesDeferred   bool
	ImagesMirrored   bool
	ImageCount       int
	OptimizeDuration time.Duration
	MirrorDuration   time.Duration
}

type feishuExportJobStatus struct {
	JobID     string `json:"job_id"`
	State     string `json:"state"`
	Phase     string `json:"phase"`
	Done      int64  `json:"done"`
	Total     int64  `json:"total"`
	Unit      string `json:"unit"`
	Version   int64  `json:"version"`
	Code      string `json:"code,omitempty"`
	Error     string `json:"error,omitempty"`
	StatusURL string `json:"status_url,omitempty"`
	ResultURL string `json:"result_url,omitempty"`
}

type feishuExportJob struct {
	id             string
	userID         string
	state          string
	progress       feishuExportProgress
	version        int64
	code           string
	message        string
	result         *preparedFeishuDocument
	resultConsumed bool
	createdAt      time.Time
	expiresAt      time.Time
	cancel         context.CancelFunc
	changed        chan struct{}
}

func (j *feishuExportJob) terminal() bool {
	return j.state == "completed" || j.state == "failed" || j.state == "canceled"
}

func (j *feishuExportJob) status() feishuExportJobStatus {
	base := "/api/feishu/docs/export/jobs/" + j.id
	return feishuExportJobStatus{
		JobID:     j.id,
		State:     j.state,
		Phase:     j.progress.Phase,
		Done:      j.progress.Done,
		Total:     j.progress.Total,
		Unit:      j.progress.Unit,
		Version:   j.version,
		Code:      j.code,
		Error:     j.message,
		StatusURL: base,
		ResultURL: base + "/result",
	}
}

type feishuExportJobManager struct {
	mu                     sync.Mutex
	jobs                   map[string]*feishuExportJob
	latestByUser           map[string]string
	createWindows          map[string]feishuExportRateWindow
	statusWindows          map[string]feishuExportRateWindow
	resultWindows          map[string]feishuExportRateWindow
	resultWritesByUser     map[string]int
	waitersByUser          map[string]int
	waitersByJob           map[string]int
	slots                  chan struct{}
	resultSlots            chan struct{}
	ttl                    time.Duration
	consumedTTL            time.Duration
	runTimeout             time.Duration
	createRateWindow       time.Duration
	statusRateWindow       time.Duration
	resultRateWindow       time.Duration
	maxJobs                int
	maxJobsPerUser         int
	createRateLimit        int
	statusRateLimit        int
	resultRateLimit        int
	maxResultWritesPerUser int
	maxResultBytes         int64
	resultBytes            int64
	waiters                int
	maxWaiters             int
	maxWaitersPerUser      int
	maxWaitersPerJob       int
	now                    func() time.Time
	gcStartOnce            sync.Once
	gcStopOnce             sync.Once
	gcStop                 chan struct{}
	gcDone                 chan struct{}
}

func newFeishuExportJobManager(now func() time.Time) *feishuExportJobManager {
	if now == nil {
		now = time.Now
	}
	return &feishuExportJobManager{
		jobs:                   make(map[string]*feishuExportJob),
		latestByUser:           make(map[string]string),
		createWindows:          make(map[string]feishuExportRateWindow),
		statusWindows:          make(map[string]feishuExportRateWindow),
		resultWindows:          make(map[string]feishuExportRateWindow),
		resultWritesByUser:     make(map[string]int),
		waitersByUser:          make(map[string]int),
		waitersByJob:           make(map[string]int),
		slots:                  make(chan struct{}, maxConcurrentFeishuExports),
		resultSlots:            make(chan struct{}, maxConcurrentFeishuResultWrites),
		ttl:                    feishuExportJobTTL,
		consumedTTL:            feishuExportConsumedResultTTL,
		runTimeout:             feishuExportJobRunTimeout,
		createRateWindow:       feishuExportCreateRateWindow,
		statusRateWindow:       feishuExportStatusRateWindow,
		resultRateWindow:       feishuExportResultRateWindow,
		maxJobs:                maxFeishuExportJobs,
		maxJobsPerUser:         maxFeishuExportJobsPerUser,
		createRateLimit:        maxFeishuExportCreatesPerWindow,
		statusRateLimit:        maxFeishuStatusRequestsPerWindow,
		resultRateLimit:        maxFeishuResultRequestsPerWindow,
		maxResultWritesPerUser: maxFeishuResultWritesPerUser,
		maxResultBytes:         maxFeishuExportResultBytes,
		maxWaiters:             maxFeishuStatusWaiters,
		maxWaitersPerUser:      maxFeishuStatusWaitersPerUser,
		maxWaitersPerJob:       maxFeishuStatusWaitersPerJob,
		now:                    now,
	}
}

func randomFeishuExportJobID() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func (m *feishuExportJobManager) signalLocked(job *feishuExportJob) {
	close(job.changed)
	job.changed = make(chan struct{})
}

func (m *feishuExportJobManager) cancelLocked(job *feishuExportJob) {
	if job == nil || job.terminal() {
		return
	}
	job.state = "canceled"
	job.progress = feishuExportProgress{Phase: "canceled", Done: 0, Total: 0, Unit: ""}
	job.code = "feishu_export_canceled"
	job.message = "导出已取消"
	job.result = nil
	job.version++
	job.expiresAt = m.now().UTC().Add(m.ttl)
	if job.cancel != nil {
		job.cancel()
		job.cancel = nil
	}
	m.signalLocked(job)
}

func (m *feishuExportJobManager) removeLocked(jobID string) {
	job := m.jobs[jobID]
	if job == nil {
		return
	}
	delete(m.jobs, jobID)
	if job.result != nil {
		m.resultBytes -= int64(len(job.result.Content))
		if m.resultBytes < 0 {
			m.resultBytes = 0
		}
	}
	if m.latestByUser[job.userID] == jobID {
		delete(m.latestByUser, job.userID)
	}
}

func (m *feishuExportJobManager) cleanLocked(now time.Time) {
	for id, job := range m.jobs {
		if job.terminal() && !job.expiresAt.IsZero() && !now.Before(job.expiresAt) {
			m.removeLocked(id)
		}
	}
	m.cleanRateWindowsLocked(m.createWindows, now, m.createRateWindow)
	m.cleanRateWindowsLocked(m.statusWindows, now, m.statusRateWindow)
	m.cleanRateWindowsLocked(m.resultWindows, now, m.resultRateWindow)
}

func (m *feishuExportJobManager) cleanRateWindowsLocked(
	windows map[string]feishuExportRateWindow,
	now time.Time,
	window time.Duration,
) {
	for key, entry := range windows {
		if now.Before(entry.started) || now.Sub(entry.started) >= window {
			delete(windows, key)
		}
	}
}

func (m *feishuExportJobManager) consumeRateLocked(
	windows map[string]feishuExportRateWindow,
	key, scope string,
	now time.Time,
	window time.Duration,
	limit int,
) error {
	if limit < 1 {
		return &feishuExportRateLimitError{scope: scope, retryAfter: window}
	}
	entry := windows[key]
	if entry.started.IsZero() || now.Before(entry.started) || now.Sub(entry.started) >= window {
		entry = feishuExportRateWindow{started: now}
	}
	if entry.count >= limit {
		retryAfter := window - now.Sub(entry.started)
		if retryAfter <= 0 {
			retryAfter = time.Second
		}
		return &feishuExportRateLimitError{scope: scope, retryAfter: retryAfter}
	}
	entry.count++
	windows[key] = entry
	return nil
}

func (m *feishuExportJobManager) reserveCreate(userID string) error {
	now := m.now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanLocked(now)
	return m.consumeRateLocked(
		m.createWindows,
		userID,
		"create",
		now,
		m.createRateWindow,
		m.createRateLimit,
	)
}

func (m *feishuExportJobManager) userJobCountLocked(userID string) int {
	count := 0
	for _, job := range m.jobs {
		if job.userID == userID {
			count++
		}
	}
	return count
}

func (m *feishuExportJobManager) evictOldestUserTerminalLocked(userID string) bool {
	var oldest *feishuExportJob
	for _, job := range m.jobs {
		if job.userID != userID || !job.terminal() {
			continue
		}
		if oldest == nil || job.createdAt.Before(oldest.createdAt) {
			oldest = job
		}
	}
	if oldest == nil {
		return false
	}
	m.removeLocked(oldest.id)
	return true
}

func (m *feishuExportJobManager) create(
	userID string,
	run func(context.Context, feishuExportProgressReporter) (preparedFeishuDocument, error),
) (*feishuExportJob, error) {
	now := m.now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), m.runTimeout)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanLocked(now)
	if err := m.consumeRateLocked(
		m.createWindows,
		userID,
		"create",
		now,
		m.createRateWindow,
		m.createRateLimit,
	); err != nil {
		cancel()
		return nil, err
	}
	for m.userJobCountLocked(userID) >= m.maxJobsPerUser && m.evictOldestUserTerminalLocked(userID) {
	}
	for len(m.jobs) >= m.maxJobs && m.evictOldestUserTerminalLocked(userID) {
	}
	if m.userJobCountLocked(userID) >= m.maxJobsPerUser || len(m.jobs) >= m.maxJobs {
		cancel()
		return nil, errFeishuExportCapacity
	}
	if previousID := m.latestByUser[userID]; previousID != "" {
		m.cancelLocked(m.jobs[previousID])
	}
	var id string
	for attempts := 0; attempts < 4; attempts++ {
		generated, err := randomFeishuExportJobID()
		if err != nil {
			cancel()
			return nil, err
		}
		if m.jobs[generated] == nil {
			id = generated
			break
		}
	}
	if id == "" {
		cancel()
		return nil, errors.New("could not allocate Feishu export job ID")
	}
	job := &feishuExportJob{
		id:        id,
		userID:    userID,
		state:     "queued",
		progress:  feishuExportProgress{Phase: "queued", Done: 0, Total: 0, Unit: ""},
		version:   1,
		createdAt: now,
		cancel:    cancel,
		changed:   make(chan struct{}),
	}
	m.jobs[id] = job
	m.latestByUser[userID] = id
	go m.run(ctx, job, run)
	return job, nil
}

func (m *feishuExportJobManager) run(
	ctx context.Context,
	job *feishuExportJob,
	run func(context.Context, feishuExportProgressReporter) (preparedFeishuDocument, error),
) {
	release, err := m.acquireExportSlot(ctx)
	if err != nil {
		m.finish(job.id, preparedFeishuDocument{}, err)
		return
	}
	defer release()

	m.mu.Lock()
	current := m.jobs[job.id]
	if current == nil || current.terminal() {
		m.mu.Unlock()
		return
	}
	current.state = "running"
	current.version++
	m.signalLocked(current)
	m.mu.Unlock()

	reporter := func(progress feishuExportProgress) {
		m.updateProgress(job.id, progress)
	}
	result, err := run(withFeishuExportProgress(ctx, reporter), reporter)
	if contextErr := ctx.Err(); contextErr != nil {
		result = preparedFeishuDocument{}
		err = contextErr
	}
	m.finish(job.id, result, err)
}

func (m *feishuExportJobManager) acquireExportSlot(ctx context.Context) (func(), error) {
	select {
	case m.slots <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-m.slots })
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *feishuExportJobManager) updateProgress(jobID string, progress feishuExportProgress) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[jobID]
	if job == nil || job.terminal() {
		return
	}
	job.state = "running"
	job.progress = progress
	job.version++
	m.signalLocked(job)
}

func (m *feishuExportJobManager) finish(jobID string, result preparedFeishuDocument, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[jobID]
	if job == nil || job.terminal() {
		return
	}
	if job.cancel != nil {
		job.cancel()
		job.cancel = nil
	}
	job.expiresAt = m.now().UTC().Add(m.ttl)
	job.version++
	if err == nil {
		resultBytes := int64(len(result.Content))
		if resultBytes > m.maxResultBytes || m.resultBytes > m.maxResultBytes-resultBytes {
			job.state = "failed"
			job.progress = feishuExportProgress{Phase: "failed", Done: 0, Total: 0, Unit: ""}
			job.result = nil
			job.code = "feishu_export_result_unavailable"
			job.message = "导出结果暂时无法保存，请稍后重试"
		} else {
			job.state = "completed"
			job.progress = feishuExportProgress{Phase: "finalize", Done: 1, Total: 1, Unit: "document"}
			job.result = &result
			m.resultBytes += resultBytes
			job.code = ""
			job.message = ""
		}
	} else if errors.Is(err, context.Canceled) {
		m.cancelLocked(job)
		return
	} else {
		code, message := feishuExportPublicError(err)
		job.state = "failed"
		job.progress = feishuExportProgress{Phase: "failed", Done: 0, Total: 0, Unit: ""}
		job.result = nil
		job.code = code
		job.message = message
	}
	m.signalLocked(job)
}

func (m *feishuExportJobManager) status(userID, jobID string) (feishuExportJobStatus, <-chan struct{}, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanLocked(m.now().UTC())
	job := m.jobs[jobID]
	if job == nil || job.userID != userID {
		return feishuExportJobStatus{}, nil, false
	}
	return job.status(), job.changed, true
}

func (m *feishuExportJobManager) statusForRequest(
	userID, jobID string,
) (feishuExportJobStatus, <-chan struct{}, bool, error) {
	now := m.now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanLocked(now)
	if err := m.consumeRateLocked(
		m.statusWindows,
		userID,
		"status",
		now,
		m.statusRateWindow,
		m.statusRateLimit,
	); err != nil {
		return feishuExportJobStatus{}, nil, false, err
	}
	job := m.jobs[jobID]
	if job == nil || job.userID != userID {
		return feishuExportJobStatus{}, nil, false, nil
	}
	return job.status(), job.changed, true, nil
}

func (m *feishuExportJobManager) acquireStatusWaiter(userID, jobID string) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[jobID]
	if job == nil || job.userID != userID {
		return nil, errFeishuExportCapacity
	}
	if m.waiters >= m.maxWaiters ||
		m.waitersByUser[userID] >= m.maxWaitersPerUser ||
		m.waitersByJob[jobID] >= m.maxWaitersPerJob {
		return nil, errFeishuStatusBusy
	}
	m.waiters++
	m.waitersByUser[userID]++
	m.waitersByJob[jobID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.waiters > 0 {
				m.waiters--
			}
			if m.waitersByUser[userID] <= 1 {
				delete(m.waitersByUser, userID)
			} else {
				m.waitersByUser[userID]--
			}
			if m.waitersByJob[jobID] <= 1 {
				delete(m.waitersByJob, jobID)
			} else {
				m.waitersByJob[jobID]--
			}
		})
	}, nil
}

func (m *feishuExportJobManager) result(userID, jobID string) (feishuExportJobStatus, *preparedFeishuDocument, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	m.cleanLocked(now)
	job := m.jobs[jobID]
	if job == nil || job.userID != userID {
		return feishuExportJobStatus{}, nil, false
	}
	if job.result == nil {
		return job.status(), nil, true
	}
	result := *job.result
	return job.status(), &result, true
}

func (m *feishuExportJobManager) resultForRequest(
	userID, jobID string,
) (feishuExportJobStatus, *preparedFeishuDocument, func(), bool, error) {
	now := m.now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanLocked(now)
	if err := m.consumeRateLocked(
		m.resultWindows,
		userID,
		"result",
		now,
		m.resultRateWindow,
		m.resultRateLimit,
	); err != nil {
		return feishuExportJobStatus{}, nil, nil, false, err
	}
	job := m.jobs[jobID]
	if job == nil || job.userID != userID {
		return feishuExportJobStatus{}, nil, nil, false, nil
	}
	status := job.status()
	if job.result == nil {
		return status, nil, nil, true, nil
	}
	if m.maxResultWritesPerUser < 1 ||
		m.resultWritesByUser[userID] >= m.maxResultWritesPerUser {
		return status, nil, nil, true, errFeishuResultBusy
	}
	slots := m.resultSlots
	select {
	case slots <- struct{}{}:
		m.resultWritesByUser[userID]++
		var once sync.Once
		release := func() {
			once.Do(func() {
				m.mu.Lock()
				defer m.mu.Unlock()
				if m.resultWritesByUser[userID] <= 1 {
					delete(m.resultWritesByUser, userID)
				} else {
					m.resultWritesByUser[userID]--
				}
				<-slots
			})
		}
		result := *job.result
		return status, &result, release, true, nil
	default:
		return status, nil, nil, true, errFeishuResultBusy
	}
}

func (m *feishuExportJobManager) markResultConsumed(userID, jobID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	m.cleanLocked(now)
	job := m.jobs[jobID]
	if job == nil || job.userID != userID || job.result == nil {
		return false
	}
	if !job.resultConsumed {
		job.resultConsumed = true
		consumedExpiry := now.Add(m.consumedTTL)
		if job.expiresAt.IsZero() || consumedExpiry.Before(job.expiresAt) {
			job.expiresAt = consumedExpiry
		}
	}
	return true
}

func (m *feishuExportJobManager) cancel(userID, jobID string) (feishuExportJobStatus, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanLocked(m.now().UTC())
	job := m.jobs[jobID]
	if job == nil || job.userID != userID {
		return feishuExportJobStatus{}, false
	}
	m.cancelLocked(job)
	return job.status(), true
}

func (m *feishuExportJobManager) sweepExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanLocked(m.now().UTC())
}

func (m *feishuExportJobManager) startGC(interval time.Duration) {
	if interval <= 0 {
		interval = feishuExportJobGCInterval
	}
	m.gcStartOnce.Do(func() {
		m.gcStop = make(chan struct{})
		m.gcDone = make(chan struct{})
		go func() {
			defer close(m.gcDone)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					m.sweepExpired()
				case <-m.gcStop:
					return
				}
			}
		}()
	})
}

func (m *feishuExportJobManager) stopGC() {
	m.gcStopOnce.Do(func() {
		if m.gcStop != nil {
			close(m.gcStop)
		}
	})
	if m.gcDone != nil {
		<-m.gcDone
	}
}

func parseFeishuStatusWait(r *http.Request) (int64, time.Duration, error) {
	after := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("after")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("invalid status version")
		}
		after = parsed
	}
	wait := time.Duration(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("wait_ms")); raw != "" {
		milliseconds, err := strconv.Atoi(raw)
		if err != nil || milliseconds < 0 {
			return 0, 0, errors.New("invalid wait duration")
		}
		wait = time.Duration(milliseconds) * time.Millisecond
		if wait > 0 && wait < minFeishuStatusLongPollWait {
			wait = minFeishuStatusLongPollWait
		}
		if wait > maxFeishuStatusLongPollWait {
			wait = maxFeishuStatusLongPollWait
		}
	}
	return after, wait, nil
}

func writePreparedFeishuDocument(w http.ResponseWriter, prepared preparedFeishuDocument) error {
	filename := sanitizeExportFilename(prepared.Filename)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", "attachment; filename=\"feishu-document.docx\"; filename*=UTF-8''"+url.PathEscape(filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(prepared.Content)))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-FBIF-Docx-Original-Bytes", strconv.Itoa(prepared.OriginalBytes))
	w.Header().Set("X-FBIF-Docx-Optimized-Bytes", strconv.Itoa(prepared.OptimizedBytes))
	w.Header().Set("X-FBIF-Images-Deferred", strconv.FormatBool(prepared.ImagesDeferred))
	w.Header().Set("X-FBIF-Images-Mirrored", strconv.FormatBool(prepared.ImagesMirrored))
	w.Header().Set("X-FBIF-Image-Count", strconv.Itoa(prepared.ImageCount))
	w.Header().Set("Server-Timing", fmt.Sprintf(
		"feishu_resolve;dur=%.1f, feishu_create;dur=%.1f, feishu_poll;dur=%.1f, feishu_download;dur=%.1f, docx_optimize;dur=%.1f, image_mirror;dur=%.1f",
		float64(prepared.Metrics.ResolveDuration.Microseconds())/1000,
		float64(prepared.Metrics.CreateDuration.Microseconds())/1000,
		float64(prepared.Metrics.PollDuration.Microseconds())/1000,
		float64(prepared.Metrics.DownloadDuration.Microseconds())/1000,
		float64(prepared.OptimizeDuration.Microseconds())/1000,
		float64(prepared.MirrorDuration.Microseconds())/1000,
	))
	w.WriteHeader(http.StatusOK)
	written, err := w.Write(prepared.Content)
	if err != nil {
		return err
	}
	if written != len(prepared.Content) {
		return io.ErrShortWrite
	}
	return nil
}
