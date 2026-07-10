package main

// publish_jobs.go — 发布流水线任务的 JSON 文件存储（施工图 S1，WORKLOG-publish-pipeline-
// in-layout-2026-07-10.md）。贴仓库既有范式（rulePresetStore）：进程内 sync.Mutex 串行化
// 全部读写 + 临时文件原子替换写，零新依赖；进程重启后 failed / awaiting_review 任务不丢。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 状态机阶段全集（施工图 S2）：
//
//	created → fetching → rendering → mirroring → staging → awaiting_review
//	        → [review approve] → publicizing → published
//	        → [review reject]  → rejected（终态；FoodTalks 侧留草稿不动）
//	任一步失败 → failed（failed_stage 记录续跑起点，retry 端点续跑）
const (
	publishStageCreated        = "created"
	publishStageFetching       = "fetching"
	publishStageRendering      = "rendering"
	publishStageMirroring      = "mirroring"
	publishStageStaging        = "staging"
	publishStageAwaitingReview = "awaiting_review"
	publishStagePublicizing    = "publicizing"
	publishStagePublished      = "published"
	publishStageRejected       = "rejected"
	publishStageFailed         = "failed"
)

// publishJob 一条「公众号 → FoodTalks 网站」发布任务（字段清单来自施工图 S1）。
type publishJob struct {
	ID              int64  `json:"id"`
	SourceKey       string `json:"source_key"` // 归一化幂等键（normalizeSiteSourceKey）
	SourceURL       string `json:"source_url"` // 用户提交的原始公众号文章链接
	Stage           string `json:"stage"`
	FailedStage     string `json:"failed_stage,omitempty"` // stage=failed 时的续跑起点
	Error           string `json:"error,omitempty"`
	Title           string `json:"title,omitempty"`
	SourceName      string `json:"source_name,omitempty"` // 公众号名（fetching 阶段解析）
	BlocksJSON      string `json:"blocks_json,omitempty"` // 中立 Block[] 序列化（先落库再推进，链接过期不回源）
	ContentHTML     string `json:"content_html,omitempty"`
	CoverURL        string `json:"cover_url,omitempty"`
	ImageMap        string `json:"image_map,omitempty"` // {原图url: OSS url}，转存幂等锚点
	FoodTalksNewsID int64  `json:"foodtalks_news_id,omitempty"`
	ArticleURL      string `json:"article_url,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// terminal 终态判定：published / rejected 之后任务盖棺；failed 不算终态（可 retry 续跑），
// 因此同 source_key 再提交会幂等返回 failed 任务而不是建新单。
func (j publishJob) terminal() bool {
	return j.Stage == publishStagePublished || j.Stage == publishStageRejected
}

type publishJobDocument struct {
	NextID int64        `json:"next_id"`
	Jobs   []publishJob `json:"jobs"`
}

type publishJobStore struct {
	mu   sync.Mutex
	path string
}

// publishJobsPath 存储路径：env PUBLISH_JOBS_PATH 优先；否则跟随规则存储所在目录
// （与 rule-presets.json 同目录规则）；再否则默认 data/publish-jobs.json。
func publishJobsPath(rulesPath string) string {
	if v := strings.TrimSpace(os.Getenv("PUBLISH_JOBS_PATH")); v != "" {
		return v
	}
	if strings.TrimSpace(rulesPath) != "" {
		return filepath.Join(filepath.Dir(rulesPath), "publish-jobs.json")
	}
	return filepath.Join("data", "publish-jobs.json")
}

func newPublishJobStore(path string) *publishJobStore {
	if strings.TrimSpace(path) == "" {
		path = filepath.Join("data", "publish-jobs.json")
	}
	return &publishJobStore{path: path}
}

func (s *publishJobStore) readLocked() (publishJobDocument, error) {
	doc := publishJobDocument{NextID: 1}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return doc, err
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		// 存储损坏必须 fail loud：静默重置会丢掉 awaiting_review / failed 任务。
		return publishJobDocument{NextID: 1}, fmt.Errorf("发布任务存储损坏（%s）: %w", s.path, err)
	}
	if doc.NextID < 1 {
		doc.NextID = 1
	}
	for _, j := range doc.Jobs {
		if j.ID >= doc.NextID {
			doc.NextID = j.ID + 1
		}
	}
	return doc, nil
}

// writeLocked 临时文件 + rename 原子替换（同 rulePresetStore 模式），崩溃不会留半截文件。
func (s *publishJobStore) writeLocked(doc publishJobDocument) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".publish-jobs-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.path)
}

// create 建单 + source_key 幂等收敛：同 key 已有未终态任务时返回它（created=false），
// 判重与建单在同一把锁内完成，并发提交同一篇文章也不会建出重复单。
func (s *publishJobStore) create(sourceKey, sourceURL string) (publishJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return publishJob{}, false, err
	}
	for _, j := range doc.Jobs {
		if j.SourceKey == sourceKey && !j.terminal() {
			return j, false, nil
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	job := publishJob{
		ID:        doc.NextID,
		SourceKey: sourceKey,
		SourceURL: sourceURL,
		Stage:     publishStageCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}
	doc.NextID++
	doc.Jobs = append(doc.Jobs, job)
	if err := s.writeLocked(doc); err != nil {
		return publishJob{}, false, err
	}
	return job, true, nil
}

func (s *publishJobStore) byID(id int64) (publishJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return publishJob{}, err
	}
	for _, j := range doc.Jobs {
		if j.ID == id {
			return j, nil
		}
	}
	return publishJob{}, fmt.Errorf("发布任务 #%d 不存在", id)
}

// list 按 id 降序（新单在前）返回至多 limit 条摘要。大字段（blocks/content_html/image_map）
// 清空——列表是管理面速览，全文走 GET /api/publish/jobs/{id}。
func (s *publishJobStore) list(limit int) ([]publishJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	out := make([]publishJob, 0, limit)
	for i := len(doc.Jobs) - 1; i >= 0 && len(out) < limit; i-- {
		j := doc.Jobs[i]
		j.BlocksJSON = ""
		j.ContentHTML = ""
		j.ImageMap = ""
		out = append(out, j)
	}
	return out, nil
}

// update 锁内 read-modify-write：所有阶段推进都经这一个入口，mutate 只改业务字段，
// UpdatedAt 由存储统一盖章。
func (s *publishJobStore) update(id int64, mutate func(*publishJob)) (publishJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return publishJob{}, err
	}
	for i := range doc.Jobs {
		if doc.Jobs[i].ID != id {
			continue
		}
		mutate(&doc.Jobs[i])
		doc.Jobs[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := s.writeLocked(doc); err != nil {
			return publishJob{}, err
		}
		return doc.Jobs[i], nil
	}
	return publishJob{}, fmt.Errorf("发布任务 #%d 不存在", id)
}

// setStage 推进阶段（清空上次失败痕迹，避免旧 error 粘在推进后的任务上误导排障）。
func (s *publishJobStore) setStage(id int64, stage string) (publishJob, error) {
	return s.update(id, func(j *publishJob) {
		j.Stage = stage
		j.FailedStage = ""
		j.Error = ""
	})
}

// setFailed 显式落败：记录失败阶段与人话原因，retry 端点凭 FailedStage 续跑。
func (s *publishJobStore) setFailed(id int64, failedStage, errMsg string) (publishJob, error) {
	return s.update(id, func(j *publishJob) {
		j.Stage = publishStageFailed
		j.FailedStage = failedStage
		j.Error = errMsg
	})
}
