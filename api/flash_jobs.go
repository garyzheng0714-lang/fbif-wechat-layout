package main

// flash_jobs.go — 快讯任务库：一篇资讯订阅文章（article_id）一条，永久去重。和文章任务分开存（flash-jobs.json，
// 在文章任务库旁边），写法一样：整份读、改、临时文件替换再同步目录（writeDurableJSON）。
// 状态推给资讯订阅走同一个发送器，事件类型 publish.flash.status（docs/publish-status-callback.md）。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	flashStageQueued         = "queued"          // 收下了，等取原文
	flashStageWriting        = "writing"         // 取原文、照规范写
	flashStageChecking       = "checking"        // 对原文、查风险、查不推荐说法
	flashStageAwaitingReview = "awaiting_review" // 等人在卡片上点
	flashStagePublishing     = "publishing"      // 人点了通过，正在发到 FoodTalks
	flashStagePublished      = "published"       // FoodTalks 上看得见了
	flashStageRejected       = "rejected"        // 人驳回
	flashStageSkipped        = "skipped"         // 按规矩不写：资讯订阅判了不该写、网站已经发过同一件事、原文不够写
	flashStageFailed         = "failed"          // 重试到头还没成，等人看

	flashStatusCallbackEventType = "publish.flash.status"
)

// flashDraft 一版快讯，字段照规范「三、标准交付格式」：标题、正文 2–3 段、来源、编辑核实提示（有才写）。
type flashDraft struct {
	Title  string           `json:"title"`
	Body   []string         `json:"body"`
	Source flashDraftSource `json:"source"`
	Hints  []string         `json:"hints,omitempty"`
}

type flashDraftSource struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	PublishedAt string `json:"published_at"`
}

// flashChecks 对某一版（Version）查出的问题。
type flashChecks struct {
	Version     int                   `json:"version"`
	Risk        publishRiskAssessment `json:"risk"`
	Unsupported []string              `json:"unsupported,omitempty"` // 原文里找不到依据的说法
	Avoid       []string              `json:"avoid,omitempty"`       // 规范表格里不推荐的说法
	CheckedAt   string                `json:"checked_at"`
}

type flashJob struct {
	ID        int64  `json:"id"`
	ArticleID string `json:"article_id"`
	SourceURL string `json:"source_url,omitempty"` // 资讯订阅交来时带的链接，只记着
	EventID   string `json:"event_id,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`

	Stage       string `json:"stage"`
	FailedStage string `json:"failed_stage,omitempty"`
	Error       string `json:"error,omitempty"`
	Failures    int    `json:"failures,omitempty"`     // 这一阶段连着失败几次
	RetryAt     string `json:"retry_at,omitempty"`     // 下次自己再试的时间
	SkipReason  string `json:"skip_reason,omitempty"`  // 为什么不写
	DuplicateOf string `json:"duplicate_of,omitempty"` // 网站上已有的同一件事（标题或链接）

	Source       *flashSourceDoc `json:"source,omitempty"`        // 取到的原文
	Earliest     *flashSourceDoc `json:"earliest,omitempty"`      // 同一件事更早的别家报道（取得到才有）
	SpecRevision int64           `json:"spec_revision,omitempty"` // 照第几版规范写的
	SpecNote     string          `json:"spec_note,omitempty"`     // 没用上最新版规范的原因

	Version  int          `json:"version,omitempty"` // 稿子第几版：AI 写的是 1，人每改一次加一
	Draft    *flashDraft  `json:"draft,omitempty"`
	EditedBy string       `json:"edited_by,omitempty"`
	Checks   *flashChecks `json:"checks,omitempty"`
	Usage    aiTokenUsage `json:"usage"` // 这一篇一共用掉的 token

	CardMessageID string `json:"card_message_id,omitempty"`
	ReviewedBy    string `json:"reviewed_by,omitempty"`
	ReviewedAt    string `json:"reviewed_at,omitempty"`

	// 发布：先记 PublishIntentAt 再调 FoodTalks；断在中间下次先按标题找，找到就认，不建第二条。
	PublishIntentAt string `json:"publish_intent_at,omitempty"`
	PublishAttempts int    `json:"publish_attempts,omitempty"` // 调过几次新建（调之前先记）
	FlashID         int64  `json:"foodtalks_flash_id,omitempty"`
	FlashURL        string `json:"flash_url,omitempty"`
	PublishedAt     string `json:"published_at,omitempty"`

	StatusCallback publishStatusCallback `json:"status_callback,omitempty"`
}

func (j flashJob) terminal() bool {
	switch j.Stage {
	case flashStagePublished, flashStageRejected, flashStageSkipped:
		return true
	}
	return false
}

// flashJobSummary 推给资讯订阅、接口回的那一份。
type flashJobSummary struct {
	ID          int64  `json:"id"`
	ArticleID   string `json:"article_id"`
	SourceURL   string `json:"source_url,omitempty"`
	Stage       string `json:"stage"`
	FailedStage string `json:"failed_stage,omitempty"`
	Error       string `json:"error,omitempty"`
	SkipReason  string `json:"skip_reason,omitempty"`
	DuplicateOf string `json:"duplicate_of,omitempty"`
	Title       string `json:"title,omitempty"`
	FlashID     int64  `json:"foodtalks_flash_id,omitempty"`
	FlashURL    string `json:"flash_url,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	ReviewedBy  string `json:"reviewed_by,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func newFlashJobSummary(j flashJob) flashJobSummary {
	s := flashJobSummary{ID: j.ID, ArticleID: j.ArticleID, SourceURL: j.SourceURL, Stage: j.Stage, FailedStage: j.FailedStage,
		Error: j.Error, SkipReason: j.SkipReason, DuplicateOf: j.DuplicateOf, FlashID: j.FlashID, FlashURL: j.FlashURL,
		PublishedAt: j.PublishedAt, ReviewedBy: j.ReviewedBy, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt}
	if j.Draft != nil {
		s.Title = j.Draft.Title
	}
	return s
}

func stampFlashStatusCallback(j *flashJob) bool {
	if j.StatusCallback.URL == "" {
		return false
	}
	summary := newFlashJobSummary(*j)
	fpSource := summary
	fpSource.UpdatedAt = ""
	b, _ := json.Marshal(fpSource)
	sum := sha256.Sum256(b)
	return stampStatusCallback(&j.StatusCallback, summary, hex.EncodeToString(sum[:16]), j.UpdatedAt)
}

type flashJobDocument struct {
	NextID int64      `json:"next_id"`
	Jobs   []flashJob `json:"jobs"`
}

type flashJobStore struct {
	path      string
	mu        sync.Mutex
	callbacks *publishStatusCallbacks
	now       func() time.Time
	written   func() // 写完盘叫一下（监控告警重算），在启动时装好
}

func newFlashJobStore(path string) *flashJobStore {
	s := &flashJobStore{path: path, now: time.Now}
	s.callbacks = newStatusCallbackSender(flashStatusCallbackEventType, "layout-flash")
	s.callbacks.ledger = s
	return s
}

// flashJobsPath 在文章任务库旁边。
func flashJobsPath(publishJobsPath string) string {
	if v := strings.TrimSpace(os.Getenv("FLASH_JOBS_PATH")); v != "" {
		return v
	}
	return filepath.Join(filepath.Dir(publishJobsPath), "flash-jobs.json")
}

func (s *flashJobStore) readLocked() (flashJobDocument, error) {
	doc := flashJobDocument{NextID: 1}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return doc, err
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return flashJobDocument{NextID: 1}, fmt.Errorf("快讯任务存储损坏（%s）: %w", s.path, err)
	}
	for _, j := range doc.Jobs {
		if j.ID >= doc.NextID {
			doc.NextID = j.ID + 1
		}
	}
	if doc.NextID < 1 {
		doc.NextID = 1
	}
	return doc, nil
}

func (s *flashJobStore) writeLocked(doc flashJobDocument) error {
	if err := writeDurableJSON(s.path, ".flash-jobs-*.json", doc); err != nil {
		return err
	}
	if s.written != nil {
		s.written()
	}
	return nil
}

func (s *flashJobStore) stamp() string { return s.now().UTC().Format(time.RFC3339) }

// create 按 article_id 去重：有就原样返回（existing=true），没有就建一条排队。
func (s *flashJobStore) create(articleID, sourceURL, callbackURL string) (flashJob, bool, error) {
	s.mu.Lock()
	doc, err := s.readLocked()
	if err != nil {
		s.mu.Unlock()
		return flashJob{}, false, err
	}
	for i := range doc.Jobs {
		if doc.Jobs[i].ArticleID != articleID {
			continue
		}
		job := doc.Jobs[i]
		// 后来交的带了回调地址：登记上并马上推一次当前状态。规矩同文章任务（registerStatusCallback）：
		// 同一个地址重复登记不动，除非上次已放弃或对方回过 410；版本号接着往上加，不清零。
		if cb := job.StatusCallback; callbackURL != "" && (cb.URL != callbackURL || cb.State != "") {
			cb.URL, cb.State, cb.StateAt, cb.Error, cb.Fingerprint = callbackURL, "", "", "", ""
			doc.Jobs[i].StatusCallback = cb
			doc.Jobs[i].UpdatedAt = s.stamp()
			queued := stampFlashStatusCallback(&doc.Jobs[i])
			if err := s.writeLocked(doc); err != nil {
				s.mu.Unlock()
				return flashJob{}, false, err
			}
			job = doc.Jobs[i]
			s.mu.Unlock()
			if queued {
				s.callbacks.enqueue(job.ID, job.StatusCallback)
			}
			return job, true, nil
		}
		s.mu.Unlock()
		return job, true, nil
	}
	now := s.stamp()
	job := flashJob{ID: doc.NextID, ArticleID: articleID, SourceURL: sourceURL, Stage: flashStageQueued, CreatedAt: now, UpdatedAt: now}
	job.StatusCallback.URL = callbackURL
	queued := stampFlashStatusCallback(&job)
	doc.NextID++
	doc.Jobs = append(doc.Jobs, job)
	if err := s.writeLocked(doc); err != nil {
		s.mu.Unlock()
		return flashJob{}, false, err
	}
	s.mu.Unlock()
	if queued {
		s.callbacks.enqueue(job.ID, job.StatusCallback)
	}
	return job, false, nil
}

func (s *flashJobStore) byID(id int64) (flashJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return flashJob{}, err
	}
	for _, j := range doc.Jobs {
		if j.ID == id {
			return j, nil
		}
	}
	return flashJob{}, fmt.Errorf("快讯任务 #%d 不存在", id)
}

// list 从新到旧。
func (s *flashJobStore) list() ([]flashJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	jobs := append([]flashJob(nil), doc.Jobs...)
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].ID > jobs[b].ID })
	return jobs, nil
}

// updateIf cond 成立才改；改完写盘，状态摘要变了就排上推送。
func (s *flashJobStore) updateIf(id int64, cond func(flashJob) bool, mutate func(*flashJob)) (flashJob, bool, error) {
	s.mu.Lock()
	doc, err := s.readLocked()
	if err != nil {
		s.mu.Unlock()
		return flashJob{}, false, err
	}
	for i := range doc.Jobs {
		if doc.Jobs[i].ID != id {
			continue
		}
		if cond != nil && !cond(doc.Jobs[i]) {
			job := doc.Jobs[i]
			s.mu.Unlock()
			return job, false, nil
		}
		mutate(&doc.Jobs[i])
		doc.Jobs[i].UpdatedAt = s.stamp()
		queued := stampFlashStatusCallback(&doc.Jobs[i])
		if err := s.writeLocked(doc); err != nil {
			s.mu.Unlock()
			return flashJob{}, false, err
		}
		job := doc.Jobs[i]
		s.mu.Unlock()
		if queued {
			s.callbacks.enqueue(job.ID, job.StatusCallback)
		}
		return job, true, nil
	}
	s.mu.Unlock()
	return flashJob{}, false, fmt.Errorf("快讯任务 #%d 不存在", id)
}

func (s *flashJobStore) update(id int64, mutate func(*flashJob)) (flashJob, error) {
	job, _, err := s.updateIf(id, nil, mutate)
	return job, err
}

// updateStatusCallback 推送发送器记账（statusCallbackLedger）。
func (s *flashJobStore) updateStatusCallback(id int64, cond func(publishStatusCallback) bool, mutate func(*publishStatusCallback)) error {
	_, _, err := s.updateIf(id, func(j flashJob) bool { return cond(j.StatusCallback) }, func(j *flashJob) { mutate(&j.StatusCallback) })
	return err
}

// requeueStatusCallbacks 启动时把没送达的排回去。
func (s *flashJobStore) requeueStatusCallbacks() {
	jobs, err := s.list()
	if err != nil {
		return
	}
	for _, j := range jobs {
		s.callbacks.enqueue(j.ID, j.StatusCallback)
	}
}
