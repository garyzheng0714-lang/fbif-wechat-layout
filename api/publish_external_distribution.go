package main

// publish_external_distribution.go — 已审核原创文章经云梭分发到外部平台。
//
// 这是 publishJob 上的正交子状态机，不改变 FoodTalks 的 Stage：自有站允许原创和
// 转载，外部平台只允许原创。云梭 push 没有幂等键，所以精确 payload 与 intent 必须
// 先落盘；push 结果不确定时只能人工对账。拿到 content_pk 后只做安全的状态查询。

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

const (
	publishExternalDistributionQueued                 = "queued"
	publishExternalDistributionPushing                = "pushing"
	publishExternalDistributionPolling                = "polling"
	publishExternalDistributionSucceeded              = "succeeded"
	publishExternalDistributionPartialFailed          = "partial_failed"
	publishExternalDistributionFailed                 = "failed"
	publishExternalDistributionReconciliationRequired = "reconciliation_required"

	publishExternalTargetQueued      = "queued"
	publishExternalTargetPending     = "pending"
	publishExternalTargetSucceeded   = "succeeded"
	publishExternalTargetFailed      = "failed"
	publishExternalTargetUnsupported = "unsupported"

	publishExternalCardPending    = "pending"
	publishExternalCardSending    = "sending"
	publishExternalCardDeadLetter = "dead_letter"

	publishExternalCardMaxAttempts = 10

	publishExternalProvenanceOriginal   = "original_confirmed"
	publishExternalProvenanceRepost     = "repost"
	publishExternalProvenanceUnverified = "unverified"
)

var (
	publishExternalArticleSourceLine = regexp.MustCompile(`(?i)^(?:本文)?(?:转载自|转自|原文(?:来源|出处)|文章来源|来源|出处|来自(?:微信公众号)?)[：:\s]+(.{1,100})`)
	publishExternalOriginalLine      = regexp.MustCompile(`(?i)^本文为(.{1,80}?)原创(?:[《，,。.!！]|$)`)
	publishExternalAuthorizedRepost  = regexp.MustCompile(`(?i)(?:本文)?(?:经|获).{1,50}(?:授权)?转载|reprinted\s+with\s+permission|originally\s+published\s+by|^source\s*[:：]`)
)

// publishExternalDistributionTarget 按平台拆开之前一轮里各账号的回执，只在迁移旧数据时读。
type publishExternalDistributionTarget struct {
	AccountID        string `json:"account_id"`
	Name             string `json:"name"`
	State            string `json:"state"`
	ProviderRecordID string `json:"provider_record_id,omitempty"`
	PlatformName     string `json:"platform_name,omitempty"`
	PublishTime      string `json:"publish_time,omitempty"`
	RawStatus        int    `json:"raw_status,omitempty"`
	Remark           string `json:"remark,omitempty"`
	FailedCount      int    `json:"failed_count,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

// publishExternalDistributionRound 按平台拆开之前的补发存档，只在迁移旧数据时读。
type publishExternalDistributionRound struct {
	State       string                              `json:"state"`
	Targets     []publishExternalDistributionTarget `json:"targets"`
	RequestedBy string                              `json:"requested_by,omitempty"`
	RequestedAt string                              `json:"requested_at,omitempty"`
	CompletedAt string                              `json:"completed_at,omitempty"`
	ContentPK   string                              `json:"content_pk,omitempty"`
	PayloadHash string                              `json:"payload_hash,omitempty"`
}

func publishExternalDistributionProcessing(state string) bool {
	switch strings.TrimSpace(state) {
	case publishExternalDistributionQueued, publishExternalDistributionPushing, publishExternalDistributionPolling:
		return true
	default:
		return false
	}
}

const publishExternalAdminRetryActor = "system:admin_retry"

// publishExternalDistributionCanSafeRetry 有平台在云梭受理前就失败（没拿到 content_pk），
// 修正后可安全重推这些平台。拿到单号的平台失败不走这里，避免重复稿。
func publishExternalDistributionCanSafeRetry(job publishJob) bool {
	return externalDistributionHasSafeRetry(job)
}

// 管理员重发只允许已发布任务里对账中的平台：云梭已给单号或结果不明，本地确认上一份
// payload 字段错误、必须换正确正文再 POST 一次。飞书卡片不得走这条路。
func publishExternalDistributionCanReplaceReconcile(job publishJob) bool {
	if job.Stage != publishStagePublished {
		return false
	}
	for _, lane := range job.ExternalDistributionLanes {
		if lane.State == publishExternalDistributionReconciliationRequired {
			return true
		}
	}
	return false
}

// publishExternalDistributionProvenance 外发只看账号与正文，不把火山检查当门槛。
// 来源或作者命中自有号，且正文没有显式外部转载声明，即视为原创（2026-08-13 用户裁定）。
func publishExternalDistributionProvenance(job publishJob) (status, source, evidence string) {
	original, source := publishArticleProvenance(job)
	own := siteOwnAccounts()
	if !original {
		if author := strings.TrimSpace(job.Author); publishExternalSourceIsOwn(author, own) {
			source = author
		} else {
			return publishExternalProvenanceRepost, source, "文章来源已判定为转载"
		}
	}
	var blocks []Block
	if err := json.Unmarshal([]byte(job.BlocksJSON), &blocks); err == nil {
		for _, block := range blocks {
			if block.K == "cap" || block.K == "img" || block.K == "video" || block.K == "table" {
				continue
			}
			line := publishExternalDistributionBlockText(block)
			if line == "" {
				continue
			}
			compact := strings.Map(func(r rune) rune {
				if r == '*' || r == '＊' || r == '_' {
					return -1
				}
				return r
			}, line)
			compact = strings.TrimSpace(compact)
			if explicitSiteRepostDisclosure.MatchString(strings.ReplaceAll(compact, " ", "")) ||
				strings.Contains(compact, "本文为转载") || publishExternalAuthorizedRepost.MatchString(compact) {
				return publishExternalProvenanceRepost, source, compact
			}
			if match := publishExternalOriginalLine.FindStringSubmatch(compact); len(match) > 1 {
				declaredOwner := cleanPublishExternalSource(match[1])
				if publishExternalSourceIsOwn(declaredOwner, own) {
					continue
				}
				return publishExternalProvenanceRepost, declaredOwner, compact
			}
			if match := publishExternalArticleSourceLine.FindStringSubmatch(compact); len(match) > 1 {
				declaredSource := cleanPublishExternalSource(match[1])
				if declaredSource != "" && !publishExternalSourceIsOwn(declaredSource, own) {
					return publishExternalProvenanceRepost, declaredSource, compact
				}
			}
		}
	}
	return publishExternalProvenanceOriginal, source, "自有账号且未发现转载出处；由操作人最终确认原创"
}

func publishExternalDistributionBlockText(block Block) string {
	if len(block.Runs) == 0 {
		return strings.Join(strings.Fields(block.Text), " ")
	}
	var value strings.Builder
	for _, run := range block.Runs {
		if run.Type != "img" && run.Type != "br" {
			value.WriteString(run.Text)
		}
	}
	return strings.Join(strings.Fields(value.String()), " ")
}

func cleanPublishExternalSource(value string) string {
	value = strings.TrimSpace(value)
	if cut := strings.IndexAny(value, "，,。；;|｜\n\r"); cut >= 0 {
		value = value[:cut]
	}
	return strings.Trim(strings.TrimSpace(value), "《》【】()（）:：")
}

func publishExternalSourceIsOwn(value string, own map[string]bool) bool {
	normalize := func(raw string) string {
		return strings.ToLower(strings.Map(func(r rune) rune {
			switch r {
			case ' ', '\t', '\r', '\n', '（', '）', '(', ')', '：', ':', '，', ',', '。', '《', '》':
				return -1
			default:
				return r
			}
		}, raw))
	}
	want := normalize(value)
	if len([]rune(want)) < 4 {
		return false
	}
	for candidate := range own {
		got := normalize(candidate)
		if want == got || strings.Contains(want, got) || strings.Contains(got, want) {
			return true
		}
	}
	return false
}

// publishExternalDistributionCardVersion 把按钮绑定到网站终态、冻结正文、最终文章来源
// 判定和当前服务端目标目录。调用方不能在 callback value 中任意塞 account_id。
func publishExternalDistributionCardVersion(job publishJob) string {
	provenance, source, _ := publishExternalDistributionProvenance(job)
	material, _ := json.Marshal(struct {
		Title    string   `json:"title"`
		Summary  string   `json:"summary"`
		Tags     []string `json:"tags"`
		CoverURL string   `json:"cover_url"`
		ImageMap string   `json:"image_map"`
	}{job.Title, job.Summary, job.Tags, job.CoverURL, job.ImageMap})
	materialDigest := sha256.Sum256(material)
	targetIDs := []string{}
	if cfg, err := loadYunsuoConfig(); err == nil {
		for _, target := range cfg.Targets {
			targetIDs = append(targetIDs, strings.TrimSpace(target.ID))
		}
	}
	payload, _ := json.Marshal(struct {
		ID            int64    `json:"id"`
		Stage         string   `json:"stage"`
		ContentHash   string   `json:"content_hash"`
		Content       int64    `json:"content_version"`
		FrozenContent int64    `json:"frozen_content_version"`
		ApprovedRisk  string   `json:"approved_risk_version"`
		Provenance    string   `json:"provenance"`
		Source        string   `json:"source"`
		MaterialHash  string   `json:"material_hash"`
		State         string   `json:"state"`
		Lanes         []string `json:"lanes,omitempty"`
		Targets       []string `json:"targets"`
	}{
		ID: job.ID, Stage: job.Stage, ContentHash: job.ContentHash,
		Content: job.ContentVersion, FrozenContent: job.ContentFrozenVersion,
		ApprovedRisk: job.ApprovedRiskVersion, Provenance: provenance, Source: source,
		MaterialHash: fmt.Sprintf("sha256:%x", materialDigest[:]),
		State:        externalDistributionSummaryState(job), Lanes: externalDistributionLaneSignature(job),
		Targets: targetIDs,
	})
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("distribution-v1:%d:%x", job.ID, digest[:8])
}

func externalDistributionVersionMatches(expected string, job publishJob) bool {
	expected = strings.TrimSpace(expected)
	want := publishExternalDistributionCardVersion(job)
	return expected != "" && len(expected) == len(want) &&
		subtle.ConstantTimeCompare([]byte(expected), []byte(want)) == 1
}

func externalDistributionEligibility(job publishJob) error {
	if job.Stage != publishStagePublished {
		return fmt.Errorf("只有已在 FoodTalks 发布的文章才能同步外部平台，当前阶段为 %s", job.Stage)
	}
	if publishSourceResyncBlocksOtherActions(job) {
		return errors.New("文章正在重新同步到 FoodTalks，或更新结果待核对，暂时不能外发")
	}
	if !externalDistributionApprovalOK(job) {
		return errors.New("文章缺少与当前内容版本匹配的审核通过记录，不能同步外部平台")
	}
	if job.FoodTalksNewsID <= 0 {
		return errors.New("任务还没有对应的网站文章，不能同步外部平台")
	}
	provenance, _, evidence := publishExternalDistributionProvenance(job)
	if provenance == publishExternalProvenanceRepost {
		return errors.New("转载文章仅发布至 FoodTalks，不允许同步头条号、百家号等外部平台")
	}
	if provenance != publishExternalProvenanceOriginal {
		return fmt.Errorf("文章原创状态未完成核验，暂不允许同步外部平台：%s", evidence)
	}
	if strings.TrimSpace(job.BlocksJSON) == "" {
		return errors.New("文章缺少中立正文，不能生成外部分发内容")
	}
	return nil
}

// externalDistributionApprovalOK 外发仍要人审过；人工在后台公开的稿，
// 公开动作本身就是终审，不再要求流水线里另有一条 approve 记录。
func externalDistributionApprovalOK(job publishJob) bool {
	if validPersistedPublishApproval(job) {
		return true
	}
	return job.Stage == publishStagePublished &&
		job.PublicationAction == publishPublicationActionManualCMS
}

// publishedNewsSnapshotForDistribution 外发正文只取网站当前已发布稿。
// 任务里的 BlocksJSON / ContentHTML 可能是进站时的旧稿或排错过的历史版本，
// 人工在 CMS 改过之后那些库存不得再发出去。
func (p *publishPipeline) publishedNewsSnapshotForDistribution(ctx context.Context, job publishJob) (publishJob, error) {
	if job.FoodTalksNewsID <= 0 {
		return job, externalDistributionIneligible(errors.New("任务还没有对应的网站文章，不能同步外部平台"))
	}
	if p == nil || p.foodTalks == nil {
		return job, errors.New("无法读取网站已发布文章，不能同步外部平台")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ft, err := p.foodTalks()
	if err != nil {
		return job, fmt.Errorf("读取网站已发布文章失败，未分发: %w", err)
	}
	news, err := ft.GetNews(ctx, job.FoodTalksNewsID)
	if err != nil {
		return job, fmt.Errorf("读取网站已发布文章失败，未分发: %w", err)
	}
	if news.ID != job.FoodTalksNewsID {
		return job, externalDistributionIneligible(fmt.Errorf("网站返回的文章不是本任务对应的已发布稿（期望 #%d，实际 #%d）", job.FoodTalksNewsID, news.ID))
	}
	if news.PublishStatus != foodTalksPublishStatusPublished {
		return job, externalDistributionIneligible(errors.New("网站上这篇还不是已发布状态，不能同步外部平台"))
	}
	if news.SourceType == 1 {
		return job, externalDistributionIneligible(errors.New("转载文章仅发布至 FoodTalks，不允许同步头条号、百家号等外部平台"))
	}
	overlaid, err := overlayPublishJobWithPublishedNews(job, news)
	if err != nil {
		return job, externalDistributionIneligible(err)
	}
	return overlaid, nil
}

func overlayPublishJobWithPublishedNews(job publishJob, news foodTalksNewsDetail) (publishJob, error) {
	if strings.TrimSpace(news.Content) == "" {
		return job, errors.New("网站已发布文章没有正文，不能同步外部平台")
	}
	_, blocks, _ := extractArticleBlocks(news.Content)
	if len(blocks) == 0 {
		return job, errors.New("无法从网站已发布正文生成外发内容")
	}
	blocksJSON, err := json.Marshal(blocks)
	if err != nil {
		return job, fmt.Errorf("保存网站已发布正文失败: %w", err)
	}
	out := job
	if title := strings.TrimSpace(news.Title); title != "" {
		out.Title = title
	}
	if summary := strings.TrimSpace(news.Summary); summary != "" {
		out.Summary = summary
	}
	if cover := strings.TrimSpace(news.CoverImg); cover != "" {
		out.CoverURL = cover
	}
	if author := strings.TrimSpace(news.Author); author != "" {
		out.Author = author
	}
	if source := strings.TrimSpace(news.SourceName); source != "" {
		out.SourceName = source
	}
	out.BlocksJSON = string(blocksJSON)
	out.ImageMap = yunsuoPublicIdentityImageMap(out.BlocksJSON, out.CoverURL)
	out.ContentHTML = news.Content
	return out, nil
}

// externalDistributionQueueMode 区分三种发起方：审核卡人工按钮、管理员对账后重发、
// 原创公开后的系统自动外发。三者共用同一套 payload 冻结与 intent 状态机。
type externalDistributionQueueMode int

const (
	externalQueueCard externalDistributionQueueMode = iota
	externalQueueReplaceReconcile
	externalQueueAuto
)

// externalDistributionIneligibleError 表示这篇文章按规则就不该外发（未公开、转载、
// 缺审核、网站稿不可用等），重试也不会变；与读网站失败这类暂时错误区分开，
// 自动外发据此决定是放弃还是下一轮再试。Error() 保持原文案。
type externalDistributionIneligibleError struct{ err error }

func (e *externalDistributionIneligibleError) Error() string { return e.err.Error() }
func (e *externalDistributionIneligibleError) Unwrap() error { return e.err }

func externalDistributionIneligible(err error) error {
	if err == nil {
		return nil
	}
	return &externalDistributionIneligibleError{err: err}
}

// errExternalLanesNotQueued 这次选中的平台都没生成出 payload（已各自记为受理前失败，
// 修正后可从卡片重推）；不是整篇不能外发，自动外发不据此清待办。
var errExternalLanesNotQueued = errors.New("各平台都未能生成外发内容")

func isExternalDistributionIneligible(err error) bool {
	var target *externalDistributionIneligibleError
	return errors.As(err, &target)
}

// requestExternalDistribution 只受理一次人工动作：先按平台各自构造并持久化精确 payload
// 快照，再把实际 HTTP 交给 worker。卡片回调因此能在 3 秒内返回，不等待云梭。
func (p *publishPipeline) requestExternalDistribution(jobID int64, actor, expectedVersion string) (publishJob, error) {
	return p.queueExternalDistribution(jobID, actor, expectedVersion, externalQueueCard, nil)
}

// replaceExternalDistributionAfterReconcile 管理员在确认上一份云梭稿作废后，
// 用当前代码重建对账中平台的 payload 再推一次。不接受飞书卡片 callback。
func (p *publishPipeline) replaceExternalDistributionAfterReconcile(jobID int64, actor string) (publishJob, error) {
	return p.queueExternalDistribution(jobID, actor, "", externalQueueReplaceReconcile, nil)
}

// queueAutoExternalDistribution 系统按配置把原创稿推给还没推过的 auto 账号；only 限定本次
// 可推的账号（各平台当天还有配额的），nil 表示不限。推过的账号不论结果都不自动再推。
func (p *publishPipeline) queueAutoExternalDistribution(jobID int64, actor string, only map[string]bool) (publishJob, error) {
	return p.queueExternalDistribution(jobID, actor, "", externalQueueAuto, only)
}

// externalDistributionTargetsFor 选本次要推的账号，每个账号一条 lane（按配置顺序）：
//   - 卡片：还没推过的账号 + 受理前失败、可安全重推的账号（重推只推原账号，不扩到别的平台）；
//   - 自动：还没推过的 auto 账号（only 限定当天还有配额的）；
//   - 管理员对账重发：对账中的账号。
func externalDistributionTargetsFor(job publishJob, cfg yunsuoConfig, mode externalDistributionQueueMode, only map[string]bool) []yunsuoAccountTarget {
	candidates := cfg.Targets
	if mode == externalQueueAuto {
		candidates = cfg.autoTargets()
	}
	var chosen []yunsuoAccountTarget
	for _, target := range candidates {
		if only != nil && !only[strings.TrimSpace(target.ID)] {
			continue
		}
		if externalDistributionLaneEligible(job, target.ID, mode) {
			chosen = append(chosen, target)
		}
	}
	return chosen
}

// externalDistributionLaneEligible 这个账号此刻能不能按 mode 开推（入队前与锁内各判一次）。
func externalDistributionLaneEligible(job publishJob, accountID string, mode externalDistributionQueueMode) bool {
	lane, exists := externalLaneFor(job, accountID)
	if !exists {
		_, coversAll := externalDistributionCoveredAccounts(job)
		return mode != externalQueueReplaceReconcile && !coversAll
	}
	switch mode {
	case externalQueueCard:
		return externalLaneCanSafeRetry(lane)
	case externalQueueReplaceReconcile:
		return lane.State == publishExternalDistributionReconciliationRequired
	default:
		return false
	}
}

func (p *publishPipeline) queueExternalDistribution(jobID int64, actor, expectedVersion string, mode externalDistributionQueueMode, only map[string]bool) (publishJob, error) {
	if publishWorkersPaused() {
		job, _ := p.store.byID(jobID)
		return job, errPublishWorkersPaused
	}
	job, err := p.store.byID(jobID)
	if err != nil {
		return publishJob{}, err
	}
	cfg, err := loadYunsuoConfig()
	if err != nil {
		return job, err
	}
	switch mode {
	case externalQueueReplaceReconcile:
		if !publishExternalDistributionCanReplaceReconcile(job) {
			return job, fmt.Errorf("只有对账中的已发布任务才能由管理员重发外部分发，当前为 %s/%s", job.Stage, externalDistributionSummaryState(job))
		}
	case externalQueueAuto:
		if len(cfg.autoTargets()) == 0 {
			return job, externalDistributionIneligible(errors.New("没有配置自动外发账号"))
		}
		if len(externalDistributionTargetsFor(job, cfg, mode, only)) == 0 {
			return job, externalDistributionIneligible(errors.New("这篇文章已经提交过外部分发，系统不会重复发布"))
		}
	default:
		if len(externalDistributionTargetsFor(job, cfg, mode, nil)) == 0 {
			return job, errors.New("这篇文章已经提交过外部分发，系统不会重复发布")
		}
	}
	if strings.TrimSpace(actor) == "" {
		return job, errors.New("外部分发必须由审核人从飞书卡片确认原创后发起")
	}
	if mode == externalQueueCard && !externalDistributionVersionMatches(expectedVersion, job) {
		return job, errors.New("分发卡片已过期，请使用最新卡片")
	}
	if job.Stage != publishStagePublished {
		return job, externalDistributionIneligible(fmt.Errorf("只有已在 FoodTalks 发布的文章才能同步外部平台，当前阶段为 %s", job.Stage))
	}
	if !externalDistributionApprovalOK(job) {
		return job, externalDistributionIneligible(errors.New("文章缺少与当前内容版本匹配的审核通过记录，不能同步外部平台"))
	}
	snapshot, err := p.publishedNewsSnapshotForDistribution(context.Background(), job)
	if err != nil {
		return job, err
	}
	if err := externalDistributionEligibility(snapshot); err != nil {
		return job, externalDistributionIneligible(err)
	}
	chosen := externalDistributionTargetsFor(job, cfg, mode, only)
	if len(chosen) == 0 {
		return job, externalDistributionIneligible(errors.New("这篇文章已经提交过外部分发，系统不会重复发布"))
	}
	requestedAt := pipelineNow(p).Format(time.RFC3339)
	_, attributionSource, attributionEvidence := publishExternalDistributionProvenance(job)
	attribution := "original_human_confirmed: " + attributionEvidence
	if mode == externalQueueAuto {
		attribution = "original_auto_confirmed: " + attributionEvidence
	}
	// 每个平台各自按自己的规则生成 payload；某个平台生成不了（如封面不够头条下限）只记
	// 这个平台受理前失败，其他平台照推。
	lanes := make([]publishExternalLane, 0, len(chosen))
	var queuedAccounts, failures []string
	for _, target := range chosen {
		lane := publishExternalLane{
			AccountID: target.ID, Name: target.Name, State: publishExternalDistributionQueued,
			RequestedBy: strings.TrimSpace(actor), RequestedAt: requestedAt,
			ContentHash: job.ContentHash, ContentVersion: job.ContentFrozenVersion,
			Attribution: attribution, Source: attributionSource,
		}
		if lane.ContentVersion == 0 {
			lane.ContentVersion = job.ContentVersion
		}
		payload, buildErr := buildYunsuoPushPayload(snapshot, []yunsuoAccountTarget{target}, cfg.Categories)
		var payloadJSON []byte
		if buildErr == nil {
			payloadJSON, buildErr = json.Marshal(payload)
		}
		if buildErr != nil {
			lane.State = publishExternalDistributionFailed
			lane.Error = truncateRunes(strings.Join(strings.Fields(buildErr.Error()), " "), 500)
			lane.Remark = lane.Error
			lane.CompletedAt = requestedAt
			failures = append(failures, target.Name+"："+lane.Error)
		} else {
			digest := sha256.Sum256(payloadJSON)
			lane.Title = payload.Title
			lane.PayloadJSON = string(payloadJSON)
			lane.PayloadHash = fmt.Sprintf("sha256:%x", digest[:])
			queuedAccounts = append(queuedAccounts, target.ID)
		}
		lanes = append(lanes, lane)
	}
	updated, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		if current.Stage != publishStagePublished || !externalDistributionApprovalOK(current) ||
			current.FoodTalksNewsID != job.FoodTalksNewsID || current.FoodTalksNewsID <= 0 {
			return false
		}
		if mode == externalQueueCard && !externalDistributionVersionMatches(expectedVersion, current) {
			return false
		}
		for _, lane := range lanes {
			if !externalDistributionLaneEligible(current, lane.AccountID, mode) {
				return false
			}
		}
		return true
	}, func(current *publishJob) {
		for _, lane := range lanes {
			if i := externalLaneIndex(*current, lane.AccountID); i >= 0 {
				current.ExternalDistributionLanes[i] = lane
			} else {
				current.ExternalDistributionLanes = append(current.ExternalDistributionLanes, lane)
			}
		}
		// auto 账号都有 lane 了，自动外发待办就清掉；还有平台等配额的留着。
		if len(externalDistributionMissingAutoTargets(*current, cfg)) == 0 {
			current.ExternalDistributionAutoPending = ""
			current.ExternalDistributionAutoError = ""
		}
		enqueueExternalDistributionCardUpdate(current)
	})
	if err != nil {
		return job, err
	}
	if !swapped {
		if mode == externalQueueAuto {
			return updated, externalDistributionIneligible(errors.New("文章状态已变化或已提交过外部分发，自动外发跳过"))
		}
		return updated, errors.New("文章状态、内容版本或分发目标已变化，请使用最新卡片")
	}
	// 先让同一张卡进入“正在同步”状态，再启动外调；两个 outbox 的发送入口会
	// 串行并重读最新任务，即使 worker 更快完成也不会让旧卡覆盖终态。
	p.scheduleExternalDistributionCardUpdate(updated.ID)
	for _, accountID := range queuedAccounts {
		accountID := accountID
		p.spawnExternalWork(func() { p.runExternalLane(jobID, accountID) })
	}
	if len(queuedAccounts) == 0 {
		return updated, fmt.Errorf("%w：%s", errExternalLanesNotQueued, strings.Join(failures, "；"))
	}
	return updated, nil
}

// runExternalDistribution 推进这篇稿所有 lane（恢复与测试用）。
func (p *publishPipeline) runExternalDistribution(jobID int64) {
	job, err := p.store.byID(jobID)
	if err != nil {
		log.Printf("publish: load external distribution job %d failed: %v", jobID, err)
		return
	}
	for _, lane := range job.ExternalDistributionLanes {
		p.runExternalLane(jobID, lane.AccountID)
	}
}

func (p *publishPipeline) runExternalLane(jobID int64, accountID string) {
	if publishWorkersPaused() {
		return
	}
	job, err := p.store.byID(jobID)
	if err != nil {
		log.Printf("publish: load external distribution job %d failed: %v", jobID, err)
		return
	}
	lane, ok := externalLaneFor(job, accountID)
	if !ok {
		return
	}
	switch lane.State {
	case publishExternalDistributionQueued:
		p.pushExternalLane(job, lane)
	case publishExternalDistributionPolling:
		if strings.TrimSpace(lane.ContentPK) != "" {
			p.pollExternalLane(job, lane)
		}
	}
}

// updateExternalLane 锁内改一条 lane；cond 判这条 lane 此刻是否仍是预期状态。
func (p *publishPipeline) updateExternalLane(jobID int64, accountID string, cond func(publishExternalLane) bool, mutate func(*publishJob, *publishExternalLane)) (publishJob, bool, error) {
	return p.store.updateIf(jobID, func(current publishJob) bool {
		lane, ok := externalLaneFor(current, accountID)
		return ok && cond(lane)
	}, func(current *publishJob) {
		i := externalLaneIndex(*current, accountID)
		mutate(current, &current.ExternalDistributionLanes[i])
	})
}

func (p *publishPipeline) yunsuoClient() (yunsuoAPI, error) {
	if p.yunsuo != nil {
		return p.yunsuo()
	}
	return newYunsuoClientFromEnv()
}

func (p *publishPipeline) pushExternalLane(job publishJob, lane publishExternalLane) {
	var payload yunsuoPushPayload
	if err := json.Unmarshal([]byte(lane.PayloadJSON), &payload); err != nil {
		p.failExternalLaneBeforePush(job.ID, lane.AccountID, "已保存的云梭分发内容无法解析："+err.Error())
		return
	}
	// 冻结的 payload 只能发往这条 lane 的账号：带了别的账号就是串了，宁可不推。
	if len(payload.AccountIDs) != 1 || strings.TrimSpace(payload.AccountIDs[0]) != strings.TrimSpace(lane.AccountID) {
		p.failExternalLaneBeforePush(job.ID, lane.AccountID, "已保存的云梭分发内容与本平台账号不符，未推送")
		return
	}
	client, err := p.yunsuoClient()
	if err != nil {
		p.failExternalLaneBeforePush(job.ID, lane.AccountID, err.Error())
		return
	}
	intentAt := pipelineNow(p).Format(time.RFC3339)
	intentKey := fmt.Sprintf("yunsuo-push:%d:%s:%s", job.ID, lane.AccountID, strings.TrimPrefix(lane.PayloadHash, "sha256:"))
	_, swapped, err := p.updateExternalLane(job.ID, lane.AccountID, func(current publishExternalLane) bool {
		return current.State == publishExternalDistributionQueued && current.IntentKey == "" &&
			current.PayloadHash == lane.PayloadHash
	}, func(_ *publishJob, current *publishExternalLane) {
		current.State = publishExternalDistributionPushing
		current.IntentKey = intentKey
		current.IntentAt = intentAt
	})
	if err != nil || !swapped {
		if err != nil {
			log.Printf("publish: persist yunsuo intent for job %d/%s failed: %v", job.ID, lane.AccountID, err)
		}
		return
	}
	contentPK, pushErr := client.Push(context.Background(), payload)
	if pushErr != nil {
		if isYunsuoRejectedError(pushErr) {
			p.finishExternalLanePushFailure(job.ID, lane.AccountID, intentKey, publishExternalDistributionFailed, pushErr.Error())
			return
		}
		// 未分类错误也按不确定处理：进入 push 外调窗口后宁可人工核对，绝不重复 POST。
		p.finishExternalLanePushFailure(job.ID, lane.AccountID, intentKey, publishExternalDistributionReconciliationRequired, pushErr.Error())
		return
	}
	contentPK = strings.TrimSpace(contentPK)
	if contentPK == "" {
		p.finishExternalLanePushFailure(job.ID, lane.AccountID, intentKey, publishExternalDistributionReconciliationRequired, "云梭已响应但缺少 content_pk")
		return
	}
	polling, persisted, persistErr := p.updateExternalLane(job.ID, lane.AccountID, func(current publishExternalLane) bool {
		return current.State == publishExternalDistributionPushing && current.IntentKey == intentKey
	}, func(_ *publishJob, current *publishExternalLane) {
		current.State = publishExternalDistributionPolling
		current.ContentPK = contentPK
		current.Error = ""
	})
	if persistErr != nil || !persisted {
		// 远端已经接受而 content_pk 未可靠落盘；保留 pushing intent，启动恢复会转人工核对。
		log.Printf("publish: yunsuo accepted job %d/%s but content_pk persistence failed: %v", job.ID, lane.AccountID, persistErr)
		return
	}
	if current, ok := externalLaneFor(polling, lane.AccountID); ok {
		p.pollExternalLaneWithClient(polling, current, client)
	}
}

func (p *publishPipeline) failExternalLaneBeforePush(jobID int64, accountID, reason string) {
	_, swapped, err := p.updateExternalLane(jobID, accountID, func(current publishExternalLane) bool {
		return current.State == publishExternalDistributionQueued && current.IntentKey == ""
	}, func(job *publishJob, current *publishExternalLane) {
		current.State = publishExternalDistributionFailed
		current.Error = truncateRunes(strings.Join(strings.Fields(reason), " "), 500)
		current.Remark = current.Error
		current.CompletedAt = pipelineNow(p).Format(time.RFC3339)
		enqueueExternalDistributionCardUpdate(job)
	})
	if err != nil {
		log.Printf("publish: record pre-push failure for job %d/%s failed: %v", jobID, accountID, err)
		return
	}
	if swapped {
		p.scheduleExternalDistributionCardUpdate(jobID)
	}
}

func (p *publishPipeline) finishExternalLanePushFailure(jobID int64, accountID, intentKey, state, reason string) {
	_, swapped, err := p.updateExternalLane(jobID, accountID, func(current publishExternalLane) bool {
		return current.State == publishExternalDistributionPushing && current.IntentKey == intentKey
	}, func(job *publishJob, current *publishExternalLane) {
		current.State = state
		current.Error = truncateRunes(strings.Join(strings.Fields(reason), " "), 500)
		current.Remark = current.Error
		current.CompletedAt = pipelineNow(p).Format(time.RFC3339)
		enqueueExternalDistributionCardUpdate(job)
	})
	if err != nil {
		log.Printf("publish: record yunsuo push result for job %d/%s failed: %v", jobID, accountID, err)
		return
	}
	if swapped {
		p.scheduleExternalDistributionCardUpdate(jobID)
	}
}

func (p *publishPipeline) pollExternalLane(job publishJob, lane publishExternalLane) {
	client, err := p.yunsuoClient()
	if err != nil {
		p.finishExternalLanePolling(job.ID, lane, publishExternalDistributionReconciliationRequired, nil,
			"无法恢复云梭状态查询："+err.Error())
		return
	}
	p.pollExternalLaneWithClient(job, lane, client)
}

func (p *publishPipeline) pollExternalLaneWithClient(job publishJob, lane publishExternalLane, client yunsuoAPI) {
	interval := p.externalDistributionPollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	timeout := p.externalDistributionPollTimeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	startedAt, err := time.Parse(time.RFC3339, lane.IntentAt)
	if err != nil {
		startedAt, err = time.Parse(time.RFC3339, lane.RequestedAt)
	}
	if err != nil {
		startedAt = time.Now().UTC()
	}
	statuses, statusErr := client.Status(context.Background(), lane.ContentPK)
	nowTime := pipelineNow(p)
	now := nowTime.Format(time.RFC3339)
	if statusErr != nil && isYunsuoRejectedError(statusErr) {
		p.finishExternalLanePolling(job.ID, lane, publishExternalDistributionReconciliationRequired, nil,
			"云梭状态查询被明确拒绝，请人工核对："+statusErr.Error())
		return
	}
	if statusErr == nil {
		next, terminal := applyExternalLaneStatuses(lane, statuses, now)
		if terminal {
			p.finishExternalLanePolling(job.ID, lane, next.State, &next, "")
			return
		}
		updated, swapped, updateErr := p.updateExternalLane(job.ID, lane.AccountID, func(current publishExternalLane) bool {
			return current.State == publishExternalDistributionPolling && current.ContentPK == lane.ContentPK
		}, func(_ *publishJob, current *publishExternalLane) {
			copyExternalLaneProvider(current, next)
			current.PollAttempts++
			current.LastPolledAt = now
			current.Error = ""
		})
		if updateErr != nil || !swapped {
			return
		}
		job = updated
	} else {
		updated, swapped, updateErr := p.updateExternalLane(job.ID, lane.AccountID, func(current publishExternalLane) bool {
			return current.State == publishExternalDistributionPolling && current.ContentPK == lane.ContentPK
		}, func(_ *publishJob, current *publishExternalLane) {
			current.PollAttempts++
			current.LastPolledAt = now
			current.Error = truncateRunes(strings.Join(strings.Fields(statusErr.Error()), " "), 500)
		})
		if updateErr != nil || !swapped {
			return
		}
		job = updated
	}
	if current, ok := externalLaneFor(job, lane.AccountID); ok {
		lane = current
	}
	if !nowTime.Before(startedAt.Add(timeout)) {
		p.finishExternalLanePolling(job.ID, lane, publishExternalDistributionReconciliationRequired, nil,
			"云梭在轮询时限内未返回终态；系统不会重复发布，请人工核对")
		return
	}
	// 每次只占用 worker 做一次查询，延时器到点后再入队；多篇文章同时分发不会
	// 长时间占满四个发布 worker。状态已落盘，进程重启由 recoverJobs 继续查询。
	accountID := lane.AccountID
	next := func() { p.spawnExternalWork(func() { p.runExternalLane(job.ID, accountID) }) }
	if p.retryNotification != nil {
		p.retryNotification(interval, next)
		return
	}
	time.AfterFunc(interval, next)
}

// externalLaneProviderStatus 在云梭回执里找这条 lane 的账号：按账号 key，回执没带 key
// 时按账号名；只有一条且都没带账号的，就是这个账号（每条 lane 只推一个账号）。
func externalLaneProviderStatus(lane publishExternalLane, statuses []yunsuoAccountStatus) (yunsuoAccountStatus, bool) {
	withIDs := false
	for _, status := range statuses {
		if strings.TrimSpace(status.AccountPool.ID) != "" {
			withIDs = true
		}
		if strings.TrimSpace(status.AccountPool.ID) == strings.TrimSpace(lane.AccountID) {
			return status, true
		}
	}
	for _, status := range statuses {
		if strings.TrimSpace(status.AccountPool.ID) == "" && strings.TrimSpace(status.AccountPool.Name) == strings.TrimSpace(lane.Name) {
			return status, true
		}
	}
	if !withIDs && len(statuses) == 1 {
		return statuses[0], true
	}
	return yunsuoAccountStatus{}, false
}

// applyExternalLaneStatuses 把云梭回执写进 lane 的副本；terminal 表示这个账号已有终态
// （1 成功；2 失败、云梭会自动重试；3 平台不支持）。未知状态绝不能当成功。
func applyExternalLaneStatuses(lane publishExternalLane, statuses []yunsuoAccountStatus, now string) (publishExternalLane, bool) {
	status, ok := externalLaneProviderStatus(lane, statuses)
	if !ok {
		return lane, false
	}
	lane.ProviderRecordID = strings.TrimSpace(status.PK)
	lane.PlatformName = strings.TrimSpace(status.PlatformName)
	lane.PublishTime = strings.TrimSpace(status.PublishTime)
	lane.RawStatus = status.Status
	lane.Remark = truncateRunes(strings.Join(strings.Fields(status.Remark), " "), 300)
	lane.FailedCount = status.FailedCount
	lane.ProviderUpdatedAt = now
	switch status.Status {
	case 1:
		lane.State = publishExternalDistributionSucceeded
		return lane, true
	case 2, 3:
		lane.State = publishExternalDistributionFailed
		return lane, true
	default:
		return lane, false
	}
}

func copyExternalLaneProvider(dst *publishExternalLane, src publishExternalLane) {
	dst.ProviderRecordID = src.ProviderRecordID
	dst.PlatformName = src.PlatformName
	dst.PublishTime = src.PublishTime
	dst.RawStatus = src.RawStatus
	dst.Remark = src.Remark
	dst.FailedCount = src.FailedCount
	dst.ProviderUpdatedAt = src.ProviderUpdatedAt
}

func (p *publishPipeline) finishExternalLanePolling(jobID int64, lane publishExternalLane, state string, provider *publishExternalLane, reason string) {
	_, swapped, err := p.updateExternalLane(jobID, lane.AccountID, func(current publishExternalLane) bool {
		return current.State == publishExternalDistributionPolling && current.ContentPK == lane.ContentPK
	}, func(job *publishJob, current *publishExternalLane) {
		current.State = state
		if provider != nil {
			copyExternalLaneProvider(current, *provider)
		}
		current.Error = truncateRunes(strings.Join(strings.Fields(reason), " "), 500)
		current.CompletedAt = pipelineNow(p).Format(time.RFC3339)
		enqueueExternalDistributionCardUpdate(job)
	})
	if err != nil {
		log.Printf("publish: record yunsuo status for job %d/%s failed: %v", jobID, lane.AccountID, err)
		return
	}
	if swapped {
		p.scheduleExternalDistributionCardUpdate(jobID)
	}
}

func enqueueExternalDistributionCardUpdate(job *publishJob) {
	job.ExternalDistributionCardSeq++
	job.ExternalDistributionCardState = publishExternalCardPending
	job.ExternalDistributionCardAttempts = 0
	job.ExternalDistributionCardError = ""
}

func (p *publishPipeline) scheduleExternalDistributionCardUpdate(jobID int64) {
	p.spawn(func() { p.dispatchExternalDistributionCardUpdate(jobID) })
}

func (p *publishPipeline) dispatchExternalDistributionCardUpdate(jobID int64) {
	claimed, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.ExternalDistributionCardState == publishExternalCardPending
	}, func(current *publishJob) {
		current.ExternalDistributionCardState = publishExternalCardSending
		current.ExternalDistributionCardAttempts++
		current.ExternalDistributionCardError = ""
	})
	if err != nil || !swapped {
		return
	}
	err = p.sendTerminalCardNotification(context.Background(), claimed, fmt.Sprintf(
		"【外部分发】任务 #%d 状态已更新\n标题：%s", claimed.ID, claimed.Title))
	if err == nil {
		_, _, ackErr := p.store.updateIf(jobID, func(current publishJob) bool {
			return current.ExternalDistributionCardSeq == claimed.ExternalDistributionCardSeq &&
				current.ExternalDistributionCardState == publishExternalCardSending
		}, func(current *publishJob) {
			current.ExternalDistributionCardState = ""
			current.ExternalDistributionCardError = ""
			current.ExternalDistributionCardUpdatedAt = pipelineNow(p).Format(time.RFC3339)
		})
		if ackErr != nil {
			log.Printf("publish: ack external distribution card for job %d failed: %v", jobID, ackErr)
		}
		return
	}
	failed, restored, persistErr := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.ExternalDistributionCardSeq == claimed.ExternalDistributionCardSeq &&
			current.ExternalDistributionCardState == publishExternalCardSending
	}, func(current *publishJob) {
		if current.ExternalDistributionCardAttempts >= publishExternalCardMaxAttempts {
			current.ExternalDistributionCardState = publishExternalCardDeadLetter
		} else {
			current.ExternalDistributionCardState = publishExternalCardPending
		}
		current.ExternalDistributionCardError = truncateRunes(strings.Join(strings.Fields(err.Error()), " "), 500)
	})
	if persistErr != nil {
		return
	}
	if restored && failed.ExternalDistributionCardState == publishExternalCardPending && p.retryNotification != nil {
		delay := publishNotificationRetryDelay(failed.ExternalDistributionCardAttempts)
		p.retryNotification(delay, func() { p.scheduleExternalDistributionCardUpdate(jobID) })
	}
}

func (p *publishPipeline) recoverExternalDistribution(job publishJob) {
	p.recoverSourceResync(job)
	if job.ExternalDistributionCardState == publishExternalCardSending {
		reset, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
			return current.ExternalDistributionCardState == publishExternalCardSending
		}, func(current *publishJob) {
			current.ExternalDistributionCardState = publishExternalCardPending
		})
		if err == nil && swapped {
			p.scheduleExternalDistributionCardUpdate(reset.ID)
		}
	} else if job.ExternalDistributionCardState == publishExternalCardPending {
		p.scheduleExternalDistributionCardUpdate(job.ID)
	}

	for _, lane := range job.ExternalDistributionLanes {
		lane := lane
		switch lane.State {
		case publishExternalDistributionQueued:
			p.spawnExternalWork(func() { p.runExternalLane(job.ID, lane.AccountID) })
		case publishExternalDistributionPushing:
			// intent 已落盘，但进程可能在 POST 前后任一点退出；结果不可判定，禁止重放。
			p.finishExternalLanePushFailure(job.ID, lane.AccountID, lane.IntentKey, publishExternalDistributionReconciliationRequired,
				"进程在云梭推送窗口中断，系统不会重复发布，请人工核对")
		case publishExternalDistributionPolling:
			if strings.TrimSpace(lane.ContentPK) == "" {
				p.finishExternalLanePolling(job.ID, lane, publishExternalDistributionReconciliationRequired, nil,
					"云梭轮询状态缺少 content_pk，系统不会重复发布，请人工核对")
				continue
			}
			p.spawnExternalWork(func() { p.runExternalLane(job.ID, lane.AccountID) })
		}
	}
}

// externalDistributionCardStateOutcome 卡片按钮点下去时：还有能推的平台就受理，
// 否则按各平台现状给提示，不重复推。
func externalDistributionCardStateOutcome(job publishJob) (reviewCardOutcome, bool) {
	if !externalDistributionStarted(job) {
		return reviewCardOutcome{}, false
	}
	if externalDistributionAnyProcessing(job) {
		return reviewCardOutcome{
			toastType: "info", toastContent: "文章正在同步分发，请勿重复操作",
		}, true
	}
	if cfg, err := loadYunsuoConfig(); err == nil && len(externalDistributionTargetsFor(job, cfg, externalQueueCard, nil)) > 0 {
		return reviewCardOutcome{}, false
	}
	switch externalDistributionSummaryState(job) {
	case publishExternalDistributionSucceeded:
		return reviewCardOutcome{
			toastType: "info", toastContent: "文章已同步至其他平台，无需重复操作",
		}, true
	case publishExternalDistributionReconciliationRequired:
		return reviewCardOutcome{
			toastType: "error", toastContent: "外部分发结果待核对，系统不会重复发布",
		}, true
	case publishExternalDistributionFailed, publishExternalDistributionPartialFailed:
		return reviewCardOutcome{
			toastType: "error", toastContent: "外部分发有平台未完成，请查看卡片并到云梭后台处理",
		}, true
	default:
		return reviewCardOutcome{
			toastType: "error", toastContent: "外部分发状态异常，请到管理后台核对",
		}, true
	}
}

const (
	publishExternalLateRefreshEvery  = 30 * time.Minute
	publishExternalLateRefreshWithin = 45 * 24 * time.Hour
)

// publishExternalLaneNeedsLateRefresh 已拿到 content_pk、但本地结论还没落定的平台：
// 15 分钟轮询超时转对账的（云梭仍在排队），以及云梭标「异常、自动重试」的失败。
// 有单号只查询（红线 37），所以补查绝不会造成重复稿。
func publishExternalLaneNeedsLateRefresh(lane publishExternalLane, now time.Time) bool {
	if strings.TrimSpace(lane.ContentPK) == "" {
		return false
	}
	switch lane.State {
	case publishExternalDistributionReconciliationRequired, publishExternalDistributionFailed:
	default:
		return false
	}
	requestedAt, err := time.Parse(time.RFC3339, lane.RequestedAt)
	if err != nil || now.Sub(requestedAt) > publishExternalLateRefreshWithin {
		return false
	}
	if lastPolled, err := time.Parse(time.RFC3339, lane.LastPolledAt); err == nil &&
		now.Sub(lastPolled) < publishExternalLateRefreshEvery {
		return false
	}
	return true
}

// refreshExternalDistributionResults 云梭发布慢于轮询时限、或失败后自动重试成功时，
// 把各平台的最终结果补回任务和审核卡。状态没变只记查询时间，不刷卡。迁移来的旧稿
// 几个平台共用一个 content_pk，同一轮只查一次。
func (p *publishPipeline) refreshExternalDistributionResults() {
	if p == nil || p.store == nil || publishWorkersPaused() {
		return
	}
	jobs, err := p.store.all()
	if err != nil {
		log.Printf("publish: 外发结果补查读取任务失败：%v", err)
		return
	}
	now := pipelineNow(p)
	stamp := now.Format(time.RFC3339)
	var client yunsuoAPI
	type statusResult struct {
		statuses []yunsuoAccountStatus
		err      error
	}
	cache := map[string]statusResult{}
	for _, job := range jobs {
		for _, lane := range job.ExternalDistributionLanes {
			if !publishExternalLaneNeedsLateRefresh(lane, now) {
				continue
			}
			if client == nil {
				if client, err = p.yunsuoClient(); err != nil {
					log.Printf("publish: 外发结果补查无法创建云梭客户端：%v", err)
					return
				}
			}
			result, ok := cache[lane.ContentPK]
			if !ok {
				result.statuses, result.err = client.Status(context.Background(), lane.ContentPK)
				cache[lane.ContentPK] = result
			}
			unchanged := func(current publishExternalLane) bool {
				return current.State == lane.State && current.ContentPK == lane.ContentPK
			}
			if result.err != nil {
				log.Printf("publish: 外发结果补查失败（任务 #%d %s）：%v", job.ID, lane.Name, result.err)
				p.updateExternalLane(job.ID, lane.AccountID, unchanged, func(_ *publishJob, current *publishExternalLane) {
					current.LastPolledAt = stamp
				})
				continue
			}
			next, terminal := applyExternalLaneStatuses(lane, result.statuses, stamp)
			state := lane.State
			if terminal {
				state = next.State
			}
			changed := state != lane.State
			_, swapped, updateErr := p.updateExternalLane(job.ID, lane.AccountID, unchanged, func(current *publishJob, l *publishExternalLane) {
				copyExternalLaneProvider(l, next)
				l.LastPolledAt = stamp
				l.PollAttempts++
				if changed {
					l.State = state
					l.Error = ""
					l.CompletedAt = stamp
					enqueueExternalDistributionCardUpdate(current)
				}
			})
			if updateErr != nil {
				log.Printf("publish: 外发结果补查落盘失败（任务 #%d %s）：%v", job.ID, lane.Name, updateErr)
				continue
			}
			if swapped && changed {
				log.Printf("publish: 外发结果补查更新（任务 #%d %s）：%s → %s", job.ID, lane.Name, lane.State, state)
				p.scheduleExternalDistributionCardUpdate(job.ID)
			}
		}
	}
}
