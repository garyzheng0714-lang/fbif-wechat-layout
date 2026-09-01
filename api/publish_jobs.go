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

// 状态机阶段全集（施工图 S2 + M5 的 enriching）：
//
//	created → fetching → rendering → mirroring → enriching → assessing → staging → awaiting_review
//	        → [review approve] → publicizing → publicizing_updating → published
//	                                      └→ publicize_reconciliation_required
//	        → [published] → unpublishing → unpublishing_updating → offline
//	                                      └→ unpublish_reconciliation_required
//	        → [offline] → republishing → republishing_updating → published
//	                                      └→ republish_reconciliation_required
//	        → [review reject]  → rejected（终态；FoodTalks 侧留草稿不动）
//	任一步失败 → failed（failed_stage 记录续跑起点，retry 端点续跑）
const (
	publishStageCreated   = "created"
	publishStageFetching  = "fetching"
	publishStageRendering = "rendering"
	publishStageMirroring = "mirroring"
	publishStageEnriching = "enriching" // M5：AI 预填发布参数（只产元数据，绝不改 content_html）
	publishStageAssessing = "assessing" // 发布红线风险检查（只产结构化提示，绝不改正文）
	// assessing_review 只用于历史 awaiting_review / 人工绑定已有草稿的补检；
	// 完成后直接回 awaiting_review，不再 PUT 草稿，避免丢审核态。
	publishStageAssessingReview = "assessing_review"
	publishStageStaging         = "staging"
	// staging_creating 表示 CreateNews 调用意图已持久化、但 foodtalks_news_id 尚未可靠落盘。
	// 该阶段绝不能自动重放 POST；重启后转 reconciliation_required 等人工核对。
	publishStageStagingCreating                 = "staging_creating"
	publishStageReconciliationRequired          = "reconciliation_required"
	publishStageAwaitingReview                  = "awaiting_review"
	publishStagePublicizing                     = "publicizing" // approve 已持久化，尚未发出外部 PUT
	publishStagePublicizingUpdating             = "publicizing_updating"
	publishStagePublicizeReconciliationRequired = "publicize_reconciliation_required"
	publishStagePublished                       = "published"
	publishStageUnpublishing                    = "unpublishing" // 人工下线已接受，尚未发出外部 PUT
	publishStageUnpublishingUpdating            = "unpublishing_updating"
	publishStageUnpublishReconciliationRequired = "unpublish_reconciliation_required"
	publishStageOffline                         = "offline"
	publishStageRepublishing                    = "republishing" // 人工重新发布已接受，尚未发出外部 PUT
	publishStageRepublishingUpdating            = "republishing_updating"
	publishStageRepublishReconciliationRequired = "republish_reconciliation_required"
	publishStageRejected                        = "rejected"
	publishStageSupersededDuplicate             = "superseded_duplicate"
	publishStageFailed                          = "failed"
	// abandoned 是失败单的结案终态：原文已失效、或测试站旧锚点不再处理。
	// 仍占 source_key，避免同一链接再建一文；不计入 failed_jobs_recent。
	publishStageAbandoned = "abandoned"
)

// publishJob 一条「公众号 → FoodTalks 网站」发布任务（字段清单来自施工图 S1）。
type publishJob struct {
	ID                int64    `json:"id"`
	SourceKey         string   `json:"source_key"`                    // 归一化幂等键（normalizeSiteSourceKey）
	SourceAliases     []string `json:"source_aliases,omitempty"`      // 抓取后解出的同文规范身份（短链↔长链去重）
	SourceURL         string   `json:"source_url"`                    // 用户提交的原始公众号文章链接
	OriginalSourceURL string   `json:"original_source_url,omitempty"` // 公众号「阅读原文」指向的原文地址（转载稿声明链接优先用它）
	Stage             string   `json:"stage"`
	FailedStage       string   `json:"failed_stage,omitempty"` // stage=failed 时的续跑起点
	Error             string   `json:"error,omitempty"`
	Title             string   `json:"title,omitempty"`
	SourceName        string   `json:"source_name,omitempty"`  // 公众号名（fetching 阶段解析）
	RulePackID        string   `json:"rule_pack_id,omitempty"` // 网站排版规则包 ID；空值兼容旧任务，按 ft-default 处理
	Classification    string   `json:"classification,omitempty"`
	Classifier        string   `json:"classifier_version,omitempty"`
	SupersededByJobID int64    `json:"superseded_by_job_id,omitempty"`
	BlocksJSON        string   `json:"blocks_json,omitempty"` // 中立 Block[] 序列化（先落库再推进，链接过期不回源）
	ContentHTML       string   `json:"content_html,omitempty"`
	RulePresetID      string   `json:"rule_preset_id,omitempty"`
	RuleRevision      int      `json:"rule_revision,omitempty"`
	RuleConfigHash    string   `json:"rule_config_hash,omitempty"`
	RuleSnapshotJSON  string   `json:"rule_snapshot_json,omitempty"` // 历史兼容字段；最新规则模式不再写入
	RendererBuildSHA  string   `json:"renderer_build_sha,omitempty"`
	RenderOutputHash  string   `json:"render_output_hash,omitempty"`
	CoverURL          string   `json:"cover_url,omitempty"`
	ImageMap          string   `json:"image_map,omitempty"` // {原图url: OSS url}，转存幂等锚点
	// 内容版本契约：权威顺序 official_api > supplier > web_fetch。高权威
	// 内容晚到时先持久化；可逆阶段重跑草稿，已进入不确定/公开窗口则只留冲突证据。
	ContentSource        string `json:"content_source,omitempty"`
	ContentHash          string `json:"content_hash,omitempty"`
	ContentVersion       int64  `json:"content_version,omitempty"`
	ContentFrozenVersion int64  `json:"content_frozen_version,omitempty"`
	ContentFrozenAt      string `json:"content_frozen_at,omitempty"`
	// SiteContentHash 是最近一次由流水线成功 PUT 的站点正文指纹。
	// 已发布稿回源更新前用它识别 CMS 人工改稿，命中时拒绝覆盖。
	SiteContentHash      string `json:"site_content_hash,omitempty"`
	PendingContentSource string `json:"pending_content_source,omitempty"`
	PendingContentHash   string `json:"pending_content_hash,omitempty"`
	PendingTitle         string `json:"pending_title,omitempty"`
	PendingSourceName    string `json:"pending_source_name,omitempty"`
	PendingAuthor        string `json:"pending_author,omitempty"`
	PendingBlocksJSON    string `json:"pending_blocks_json,omitempty"`
	PendingCoverURL      string `json:"pending_cover_url,omitempty"`
	PendingContentAt     string `json:"pending_content_at,omitempty"`
	ContentConflict      bool   `json:"content_conflict,omitempty"`
	ContentConflictNote  string `json:"content_conflict_note,omitempty"`
	FoodTalksNewsID      int64  `json:"foodtalks_news_id,omitempty"`
	// 草稿建在哪个 FoodTalks 环境（test/production，口径同 foodTalksURLTarget）。
	// 环境守卫据此拒绝拿着 A 环境的资讯 id 去 B 环境读写（见 publish_target_guard.go）。
	StagingTarget string `json:"staging_target,omitempty"`
	ArticleURL    string `json:"article_url,omitempty"`
	PublishedAt   string `json:"published_at,omitempty"`
	UnpublishedAt string `json:"unpublished_at,omitempty"`
	// 文章首次成功公开时从网站规则中固化的下线窗口。0 表示关闭下线能力。
	// 不跟随之后的规则修改，避免历史文章的可操作边界漂移。
	UnpublishWindowHours int    `json:"unpublish_window_hours"`
	UnpublishPolicySet   bool   `json:"unpublish_policy_set,omitempty"`
	UnpublishWindowUntil string `json:"unpublish_window_until,omitempty"`
	// 最近一次公开生命周期动作，用于同一张飞书卡片上的操作留痕。
	PublicationAction   string `json:"publication_action,omitempty"` // publish / unpublish / republish / manual_cms
	PublicationActor    string `json:"publication_actor,omitempty"`
	PublicationActionAt string `json:"publication_action_at,omitempty"`
	// 外部分发是 FoodTalks 主发布状态机之外的正交子流程。FoodTalks 允许转载；
	// 云梭目标只允许原创，并且只能在 FoodTalks 已审核公开后由卡片人工触发。
	// 目标账号必须在受理动作时快照，避免运行期配置变化把同一次意图发往别处。
	ExternalDistributionState          string                              `json:"external_distribution_state,omitempty"`
	ExternalDistributionTargets        []publishExternalDistributionTarget `json:"external_distribution_targets,omitempty"`
	ExternalDistributionRequestedBy    string                              `json:"external_distribution_requested_by,omitempty"`
	ExternalDistributionRequestedAt    string                              `json:"external_distribution_requested_at,omitempty"`
	ExternalDistributionContentHash    string                              `json:"external_distribution_content_hash,omitempty"`
	ExternalDistributionContentVersion int64                               `json:"external_distribution_content_version,omitempty"`
	ExternalDistributionAttribution    string                              `json:"external_distribution_attribution,omitempty"`
	ExternalDistributionSource         string                              `json:"external_distribution_source,omitempty"`
	ExternalDistributionPayloadJSON    string                              `json:"external_distribution_payload_json,omitempty"`
	ExternalDistributionPayloadHash    string                              `json:"external_distribution_payload_hash,omitempty"`
	ExternalDistributionIntentKey      string                              `json:"external_distribution_intent_key,omitempty"`
	ExternalDistributionIntentAt       string                              `json:"external_distribution_intent_at,omitempty"`
	ExternalDistributionContentPK      string                              `json:"external_distribution_content_pk,omitempty"`
	ExternalDistributionError          string                              `json:"external_distribution_error,omitempty"`
	ExternalDistributionCompletedAt    string                              `json:"external_distribution_completed_at,omitempty"`
	ExternalDistributionPollAttempts   int                                 `json:"external_distribution_poll_attempts,omitempty"`
	ExternalDistributionLastPolledAt   string                              `json:"external_distribution_last_polled_at,omitempty"`
	// 分发结果单独更新同一张审核卡，不占用网站发布的单槽 notification outbox。
	ExternalDistributionCardState     string `json:"external_distribution_card_state,omitempty"`
	ExternalDistributionCardSeq       int64  `json:"external_distribution_card_seq,omitempty"`
	ExternalDistributionCardAttempts  int    `json:"external_distribution_card_attempts,omitempty"`
	ExternalDistributionCardError     string `json:"external_distribution_card_error,omitempty"`
	ExternalDistributionCardUpdatedAt string `json:"external_distribution_card_updated_at,omitempty"`
	// 已发布稿的「重新同步至网站」是不改主 Stage 的正交子流程。
	// updating 表示已可能发出 FoodTalks PUT，之后的断线只能对账。
	SourceResyncState           string `json:"source_resync_state,omitempty"`
	SourceResyncRequestedBy     string `json:"source_resync_requested_by,omitempty"`
	SourceResyncRequestedAt     string `json:"source_resync_requested_at,omitempty"`
	SourceResyncCompletedAt     string `json:"source_resync_completed_at,omitempty"`
	SourceResyncError           string `json:"source_resync_error,omitempty"`
	SourceResyncIntentKey       string `json:"source_resync_intent_key,omitempty"`
	SourceResyncIntentAt        string `json:"source_resync_intent_at,omitempty"`
	SourceResyncApprovedBy      string `json:"source_resync_approved_by,omitempty"`
	SourceResyncApprovedAt      string `json:"source_resync_approved_at,omitempty"`
	SourceResyncApprovedVersion string `json:"source_resync_approved_version,omitempty"`
	// FoodTalks CreateNews 没有服务端幂等键：外调前先持久化本地 intent。
	// 调用结果不确定时凭此转人工核对，禁止自动 POST 第二次。
	StagingIntentKey string `json:"staging_intent_key,omitempty"`
	StagingIntentAt  string `json:"staging_intent_at,omitempty"`
	// 公开 PUT 同样先落意图：publicizing_updating 表示已可能向 FoodTalks
	// 发出不可逆请求，崩溃/断线后只能人工核对，不得盲目重放。
	PublicizeIntentKey string `json:"publicize_intent_key,omitempty"`
	PublicizeIntentAt  string `json:"publicize_intent_at,omitempty"`
	// 下线 / 重新发布同样先落 intent；进入 updating 后结果不确定只能人工对账。
	UnpublishIntentKey string `json:"unpublish_intent_key,omitempty"`
	UnpublishIntentAt  string `json:"unpublish_intent_at,omitempty"`
	RepublishIntentKey string `json:"republish_intent_key,omitempty"`
	RepublishIntentAt  string `json:"republish_intent_at,omitempty"`
	// M5 AI 预填发布参数（enriching 阶段落库；AI 失败时是规则兜底值，任务照常推进）。
	// 口径见 docs/editorial-norms-foodtalks.md（M7 从编辑部历史文章倒推）。
	Summary   string            `json:"summary,omitempty"`    // 摘要（一句话钩子 10~40 字；兜底=正文纯文本前 40 字）
	Tags      []string          `json:"tags,omitempty"`       // 标签（1~3 个，词表优先；兜底为空）
	Author    string            `json:"author,omitempty"`     // 正文识别的作者署名（宁空勿造；不回落 source_name）
	AISource  string            `json:"ai_source,omitempty"`  // 正文声明的原始出处（自家号转发他人文章时命中；决定 sourceType/sourceName）
	AINotes   string            `json:"ai_notes,omitempty"`   // AI 优化说明（人话 2-4 句；兜底="AI 预填未执行（原因）"）
	RiskLevel string            `json:"risk_level,omitempty"` // pass / warning / block；block 禁止 approve
	RiskItems []publishRiskItem `json:"risk_items,omitempty"` // 五类红线的证据与原因（含 certainty 分层）
	// RiskHeadline 审核卡首屏那句话（≤100 字）。旧任务为空时由
	// publishRiskHeadlineOrFallback 现场合成，不需要数据迁移。
	RiskHeadline string `json:"risk_headline,omitempty"`
	RiskNotes    string `json:"risk_notes,omitempty"`
	RiskRaw      string `json:"risk_raw,omitempty"` // 模型解析失败时留痕，最多 2000 字
	// 持久 notification outbox：状态迁移与待发送事件同一次 JSON 原子写；sending
	// 在进程启动时视为上次崩溃遗留并重置 pending。seq 防旧发送结果清掉新事件。
	NotificationKind     string `json:"notification_kind,omitempty"`
	NotificationState    string `json:"notification_state,omitempty"`
	NotificationSeq      int64  `json:"notification_seq,omitempty"`
	NotificationAttempts int    `json:"notification_attempts,omitempty"`
	NotificationError    string `json:"notification_error,omitempty"`
	NotificationSentAt   string `json:"notification_sent_at,omitempty"`
	ReviewedBy           string `json:"reviewed_by,omitempty"`           // 审核人 open_id（飞书卡片回调 operator；HTTP 审核为空）
	ReviewedAt           string `json:"reviewed_at,omitempty"`           // 审核时刻（RFC3339 UTC）
	ReviewAction         string `json:"review_action,omitempty"`         // approve / reject；publicizing 恢复的授权凭据
	ReviewForced         bool   `json:"review_forced,omitempty"`         // 阻断级风险下的强制发布（2026-08-07 用户裁定），终态卡与留痕据此标注
	ApprovedRiskVersion  string `json:"approved_risk_version,omitempty"` // approve 当刻与风险快照绑定的摘要
	// SiteDuplicateNewsID 同题防重标记：CMS 上与本任务同题且已发布的另一篇资讯 id
	//（多为运营手工发布），发卡/刷卡时 best effort 更新，卡片据此标红提醒。
	SiteDuplicateNewsID int64 `json:"site_duplicate_news_id,omitempty"`

	ReviewCardMsgID string `json:"review_card_msg_id,omitempty"` // 旧任务单卡片兼容
	// 新配置支持同一张审核卡投递到多个群；map 的 key 是 chat_id，value 是
	// 对应群里的 message_id。ReviewCardMsgID 保留给旧任务兼容。
	ReviewDeliveryAppID   string            `json:"review_delivery_app_id,omitempty"`
	ReviewDeliveryChatIDs []string          `json:"review_delivery_chat_ids,omitempty"`
	ReviewCardMsgIDs      map[string]string `json:"review_card_msg_ids,omitempty"`
	// ReviewCoverImgKey 待审核卡上传封面时拿到的飞书 img_key。终态卡复用它，
	// 才能在审核通过后仍然展示封面——卡片回调必须同步返回新卡，不能在那里
	// 现走一次图片上传。旧任务没有这个值时终态卡自动退化为无封面单列。
	ReviewCoverImgKey string `json:"review_cover_img_key,omitempty"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

// terminal 终态判定：published / rejected / abandoned 之后任务盖棺；failed 不算终态（可 retry 续跑），
// 因此同 source_key 再提交会幂等返回 failed 任务而不是建新单。
func (j publishJob) terminal() bool {
	return j.Stage == publishStagePublished || j.Stage == publishStageOffline ||
		j.Stage == publishStageRejected || j.Stage == publishStageSupersededDuplicate ||
		j.Stage == publishStageAbandoned
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
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName)
		return err
	}
	// rename 原子但不等于掉电持久；同步目录，确保「先落 intent、再外调」的顺序成立。
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// create 建单 + source_key 永久幂等收敛：同 key 只允许存在一条任务，包含 published /
// rejected 等终态。网站没有安全删除与服务端幂等 API，终态释放 key 会重复建文。
func (s *publishJobStore) create(sourceKey, sourceURL string) (publishJob, bool, error) {
	return s.createSeeded(sourceKey, sourceURL, nil)
}

// createSeeded 与 create 共用永久幂等语义，并把调用方提供的初始内容与任务创建
// 原子写入。官方 API 正文直入时不能先建空任务再补正文：进程若在两次写之间退出，
// 恢复 worker 会误走网页 fetching，破坏“正文只来自官方 API”的契约。
func (s *publishJobStore) createSeeded(sourceKey, sourceURL string, seed func(*publishJob)) (publishJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return publishJob{}, false, err
	}
	for _, j := range doc.Jobs {
		if publishJobHasSourceKey(j, sourceKey) {
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
	if seed != nil {
		seed(&job)
	}
	doc.NextID++
	doc.Jobs = append(doc.Jobs, job)
	if err := s.writeLocked(doc); err != nil {
		return publishJob{}, false, err
	}
	return job, true, nil
}

func publishJobHasSourceKey(job publishJob, sourceKey string) bool {
	if publishSourceKeysEqual(job.SourceKey, sourceKey) {
		return true
	}
	for _, alias := range job.SourceAliases {
		if publishSourceKeysEqual(alias, sourceKey) {
			return true
		}
	}
	return false
}

// publishSourceKeysEqual 让旧存储中曾把 sn 写进 key 的长链与新版
// biz/mid/idx 身份无缝兼容，避免上线后因 key 算法升级反而重复建文。
func publishSourceKeysEqual(a, b string) bool {
	return canonicalPublishSourceKey(a) == canonicalPublishSourceKey(b)
}

func canonicalPublishSourceKey(key string) string {
	if !strings.HasPrefix(key, "site:") {
		return key
	}
	if normalized, err := normalizeSiteSourceKey(strings.TrimPrefix(key, "site:")); err == nil {
		return normalized
	}
	return key
}

// claimSourceAlias 在抓取到公众号页面的 og:url / 最终 URL 后，原子声明
// 规范文章身份。两个不同短链或短/长链同时抓取时，只有一个 job 能抢到
// alias；输家在 CreateNews 之前显式停单，不会制造第二篇网站文章。
func (s *publishJobStore) claimSourceAlias(id int64, sourceKey string) (job publishJob, conflictingID int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return publishJob{}, 0, err
	}
	idx := -1
	for i := range doc.Jobs {
		if doc.Jobs[i].ID == id {
			idx = i
			continue
		}
		if publishJobHasSourceKey(doc.Jobs[i], sourceKey) {
			return doc.Jobs[i], doc.Jobs[i].ID, nil
		}
	}
	if idx < 0 {
		return publishJob{}, 0, fmt.Errorf("发布任务 #%d 不存在", id)
	}
	if publishJobHasSourceKey(doc.Jobs[idx], sourceKey) {
		return doc.Jobs[idx], 0, nil
	}
	doc.Jobs[idx].SourceAliases = append(doc.Jobs[idx].SourceAliases, sourceKey)
	doc.Jobs[idx].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.writeLocked(doc); err != nil {
		return publishJob{}, 0, err
	}
	return doc.Jobs[idx], 0, nil
}

// all 返回存储中的完整任务快照，供进程启动恢复扫描。调用方不得修改返回值中的切片。
func (s *publishJobStore) all() ([]publishJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	out := make([]publishJob, len(doc.Jobs))
	copy(out, doc.Jobs)
	return out, nil
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
		j.ExternalDistributionPayloadJSON = ""
		j.RiskRaw = ""
		out = append(out, j)
	}
	return out, nil
}

// update 锁内 read-modify-write：所有阶段推进都经这一个入口，mutate 只改业务字段，
// UpdatedAt 由存储统一盖章。
func (s *publishJobStore) update(id int64, mutate func(*publishJob)) (publishJob, error) {
	job, _, err := s.updateIf(id, func(publishJob) bool { return true }, mutate)
	return job, err
}

// updateIf 是存储层原子 compare-and-swap：predicate 与 mutate 在同一把锁、同一份
// 文档快照内执行。swapped=false 时返回当前任务且不写盘，供 review/retry 报冲突。
func (s *publishJobStore) updateIf(id int64, predicate func(publishJob) bool, mutate func(*publishJob)) (job publishJob, swapped bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return publishJob{}, false, err
	}
	for i := range doc.Jobs {
		if doc.Jobs[i].ID != id {
			continue
		}
		if !predicate(doc.Jobs[i]) {
			return doc.Jobs[i], false, nil
		}
		mutate(&doc.Jobs[i])
		doc.Jobs[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := s.writeLocked(doc); err != nil {
			return publishJob{}, false, err
		}
		return doc.Jobs[i], true, nil
	}
	return publishJob{}, false, fmt.Errorf("发布任务 #%d 不存在", id)
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
