package main

// publish_external_distribution.go — 已审核原创文章分发到第三方平台：百家号、头条号都经自己的分发服务
// 存草稿再发布（publish_external_robot.go）。云梭 2026-10-24 下线，它留下的旧 lane 只显示。
//
// 这是 publishJob 上的正交子状态机，不改变 FoodTalks 的 Stage：自有站允许原创和
// 转载，外部平台只允许原创。先把精确 payload 与 intent 落盘再外调；分发服务有幂等键，
// 结果不确定时按同一个键查。

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"regexp"
	"strings"
	"time"
)

const (
	publishExternalDistributionQueued                 = "queued"
	publishExternalDistributionPushing                = "pushing"
	publishExternalDistributionPolling                = "polling"    // 只有云梭旧 lane 有，读盘即转待核对
	publishExternalDistributionPublishing             = "publishing" // 草稿存好了，分发服务在点发布
	publishExternalDistributionReviewing              = "reviewing"  // 点完了，平台在审核（含定时待发），分发服务接着查
	publishExternalDistributionSucceeded              = "succeeded"  // 已发布，lane.URL 是文章链接
	publishExternalDistributionRejected               = "rejected"   // 平台审核没通过：终态，不再自动发
	publishExternalDistributionPartialFailed          = "partial_failed"
	publishExternalDistributionFailed                 = "failed"
	publishExternalDistributionReconciliationRequired = "reconciliation_required"

	publishExternalCardPending    = "pending"
	publishExternalCardSending    = "sending"
	publishExternalCardDeadLetter = "dead_letter"
	// publishExternalCardExpired 审核卡已过飞书 14 天可更新期限（补发九月旧稿时常见）：
	// 状态照常记在各平台 lane 上，只是不再同步到那张卡；不重试、不进死信告警。
	publishExternalCardExpired = "expired"

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

// publishExternalDistributionProcessing 系统正在送稿、点发布的 lane（平台审核中不算：那是在等平台）。
func publishExternalDistributionProcessing(state string) bool {
	switch strings.TrimSpace(state) {
	case publishExternalDistributionQueued, publishExternalDistributionPushing, publishExternalDistributionPolling,
		publishExternalDistributionPublishing:
		return true
	default:
		return false
	}
}

const publishExternalAdminRetryActor = "system:admin_retry"

// 管理员重发只允许已发布任务里待核对的平台：人在平台草稿箱里核对、删掉多出来的稿之后，
// 用当前正文再存一次。飞书卡片不得走这条路。
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
	if cfg, err := loadExternalTargets(); err == nil {
		for _, target := range cfg.forJob(job) {
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
		Drafts        []string `json:"drafts,omitempty"`
		Targets       []string `json:"targets"`
	}{
		ID: job.ID, Stage: job.Stage, ContentHash: job.ContentHash,
		Content: job.ContentVersion, FrozenContent: job.ContentFrozenVersion,
		ApprovedRisk: job.ApprovedRiskVersion, Provenance: provenance, Source: source,
		MaterialHash: fmt.Sprintf("sha256:%x", materialDigest[:]),
		State:        externalDistributionSummaryState(job), Lanes: externalDistributionLaneSignature(job),
		Drafts:  externalDistributionDraftSignature(job),
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

// externalDistributionNoTargets 这篇一个能发的号都没有：先说本来就不能外发的原因（如转载），再说表里没配。
func externalDistributionNoTargets(job publishJob) error {
	if err := externalDistributionEligibility(job); err != nil {
		return externalDistributionIneligible(err)
	}
	return externalDistributionIneligible(fmt.Errorf("多维表格「平台账号」里没有发「%s」文章的在用号",
		firstNonEmpty(strings.TrimSpace(job.SourceName), "这个公众号")))
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
		return errors.New("转载文章只发 FoodTalks，不同步到第三方平台")
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
		return job, externalDistributionIneligible(errors.New("转载文章只发 FoodTalks，不同步到第三方平台"))
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
	out.ImageMap = publicIdentityImageMap(out.BlocksJSON, out.CoverURL)
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

// errExternalLanesNeedFix 这次选中的平台都卡在推送前检查（标题、摘要超限或封面太小），
// 已记成待修改，等人在卡片或侧边栏里改；自动外发不据此清待办。
var errExternalLanesNeedFix = errors.New("要先改好标题、摘要或封面才能同步")

func isExternalDistributionIneligible(err error) bool {
	var target *externalDistributionIneligibleError
	return errors.As(err, &target)
}

// requestExternalDistribution 只受理一次人工动作：先按平台各自构造并持久化精确 payload
// 快照，再把实际 HTTP 交给 worker。卡片回调因此能在 3 秒内返回，不等分发服务。
func (p *publishPipeline) requestExternalDistribution(jobID int64, actor, expectedVersion string) (publishJob, error) {
	return p.queueExternalDistribution(jobID, actor, expectedVersion, externalQueueCard, nil)
}

const publishExternalAdminSyncActor = "system:admin_sync"

// syncExternalAccountAsAdmin 管理员替人点卡片上这个号的「同步至X」「重新同步至X」（2026-11-03）：运维和助手点不了
// 飞书卡片。和卡片按钮同一段受理（卡片模式、只推这一个号、按现在的卡片版本），已在平台上、正在同步的不重复同步。
func (p *publishPipeline) syncExternalAccountAsAdmin(jobID int64, accountID string) (publishJob, error) {
	job, err := p.store.byID(jobID)
	if err != nil {
		return job, err
	}
	cfg, err := loadExternalTargets()
	if err != nil {
		return job, err
	}
	target, ok := cfg.jobTarget(job, strings.TrimSpace(accountID))
	if !ok {
		return job, fmt.Errorf("这篇不能同步到 %s：不是这篇能发的号", strings.TrimSpace(accountID))
	}
	if lane, ok := externalLaneFor(job, target.ID); ok && !externalLaneCanSafeRetry(lane) {
		return job, fmt.Errorf("%s%s，不会重复同步", target.label(), externalPlatformStatus(job, target).Status)
	}
	return p.queueExternalDistribution(jobID, publishExternalAdminSyncActor, publishExternalDistributionCardVersion(job), externalQueueCard,
		map[string]bool{target.ID: true})
}

// replaceExternalDistributionAfterReconcile 管理员在平台草稿箱核对、删掉多出来的稿之后再存一次：
// 交过分发服务的按原来的冻结内容和幂等键再交（robotResendFrozenLane），云梭旧 lane 用当前代码重建。
// 不接受飞书卡片 callback。
func (p *publishPipeline) replaceExternalDistributionAfterReconcile(jobID int64, actor string) (publishJob, error) {
	return p.queueExternalDistribution(jobID, actor, "", externalQueueReplaceReconcile, nil)
}

// queueAutoExternalDistribution 系统按配置把原创稿推给还没推过的 auto 账号；only 限定本次
// 可推的账号（各平台当天还有配额的），nil 表示不限。推过的账号不论结果都不自动再推。
func (p *publishPipeline) queueAutoExternalDistribution(jobID int64, actor string, only map[string]bool) (publishJob, error) {
	return p.queueExternalDistribution(jobID, actor, "", externalQueueAuto, only)
}

// externalDistributionTargetsFor 选本次要推的账号，每个账号一条 lane（只在「来源公众号」是这篇的号里选，按表里的顺序）：
//   - 卡片：还没推过的账号 + 失败、可安全重推的账号（重推只推原账号，不扩到别的平台）；
//   - 自动：还没推过的 auto 账号（only 限定当天还有配额的）；
//   - 管理员对账重发：对账中的账号。
func externalDistributionTargetsFor(job publishJob, cfg externalTargets, mode externalDistributionQueueMode, only map[string]bool) []externalAccount {
	var chosen []externalAccount
	for _, target := range cfg.forJob(job) {
		if mode == externalQueueAuto && !target.Auto {
			continue
		}
		if only != nil && !only[strings.TrimSpace(target.ID)] {
			continue
		}
		if externalDistributionLaneEligible(job, target, cfg, mode) {
			chosen = append(chosen, target)
		}
	}
	return chosen
}

// externalDistributionLaneEligible 这个账号此刻能不能按 mode 开推（入队前与锁内各判一次）。
func externalDistributionLaneEligible(job publishJob, target externalAccount, cfg externalTargets, mode externalDistributionQueueMode) bool {
	lane, exists := externalLaneFor(job, target.ID)
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
	cfg, err := loadExternalTargets()
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
		if len(cfg.forJob(job)) == 0 {
			return job, externalDistributionNoTargets(job)
		}
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
	// 每个平台先按自己的规则做推送前检查（标题、摘要字数与封面尺寸）：有问题的平台不推、
	// 不占名额，记成待修改等人改；其余平台各自生成 payload，生成不了的只记这个平台受理前
	// 失败，其他平台照推。
	lanes := make([]publishExternalLane, 0, len(chosen))
	var queuedAccounts, failures, fixNames []string
	type preflightResult struct {
		target     externalAccount
		issues     []publishExternalIssue
		candidates []publishExternalCoverCandidate
		autoCover  string // 自动放大后换上的封面（publish_external_cover.go）
		// 超长图注缩成的（publish_external_captions.go）；captionsChanged 是和记下的不一样，要存回去。
		captions        map[string]string
		captionsChanged bool
	}
	var checked []preflightResult
	draftEditedAt := map[string]string{}
	material := externalDraftMaterial(job)
	for _, target := range chosen {
		// 交过分发服务的按原来的冻结内容和幂等键再交：管理员重发待核对的，卡片重试草稿已存好、发布没成的。
		// 冻结的图注超了平台现在的上限（发布时平台会拦）就不再按原来的交，往下按现在的内容重建、换键另存一篇。
		if old, ok := externalLaneFor(job, target.ID); ok && old.Driver == externalDriverRobot && strings.TrimSpace(old.IntentKey) != "" &&
			(mode == externalQueueReplaceReconcile || (mode == externalQueueCard && externalLaneDraftSaved(old) &&
				!robotFrozenCaptionsOver(old, platformForAccount(target)))) {
			lane, resendErr := robotResendFrozenLane(job.ID, old, actor, requestedAt)
			if resendErr != nil {
				failures = append(failures, target.label()+"："+resendErr.Error())
				continue
			}
			queuedAccounts = append(queuedAccounts, target.ID)
			lanes = append(lanes, lane)
			continue
		}
		draft, _ := externalDraftFor(job, target.ID)
		draftEditedAt[target.ID] = draft.EditedAt
		issues, candidates := externalPreflight(snapshot, target, draft)
		autoCover := ""
		if issue, ok := externalIssueFields(issues)[publishExternalIssueCover]; ok {
			// 原封面只差一点：放大一份换上再查一遍，不用人管。
			if cover, fixed := p.autoUpscaleExternalCover(job.ID, draft, issue); fixed {
				draft.Candidates = addExternalCoverCandidates(draft.Candidates, []publishExternalCoverCandidate{cover})
				draft.CoverURL = cover.URL
				issues, candidates = externalPreflight(snapshot, target, draft)
				candidates = addExternalCoverCandidates(candidates, []publishExternalCoverCandidate{cover})
				autoCover = cover.URL
			}
		}
		result := preflightResult{target: target, issues: issues, candidates: candidates, autoCover: autoCover}
		if len(issues) == 0 {
			result.captions = p.externalCaptionsFor(snapshot, platformForAccount(target), draft)
			result.captionsChanged = !maps.Equal(result.captions, draft.Captions)
			draft.Captions = result.captions
		}
		checked = append(checked, result)
		if len(issues) > 0 {
			fixNames = append(fixNames, target.label()+"："+externalIssueSummaryText(issues))
			continue
		}
		lane := publishExternalLane{
			AccountID: target.ID, Name: target.label(), State: publishExternalDistributionQueued,
			RequestedBy: strings.TrimSpace(actor), RequestedAt: requestedAt,
			ContentHash: job.ContentHash, ContentVersion: job.ContentFrozenVersion,
			Attribution: attribution, Source: attributionSource,
		}
		if lane.ContentVersion == 0 {
			lane.ContentVersion = job.ContentVersion
		}
		lane.Driver = externalDriverRobot
		payloadJSON, platformTitle, buildErr := buildExternalLanePayload(snapshot, target, draft)
		if buildErr != nil {
			lane.State = publishExternalDistributionFailed
			lane.Error = truncateRunes(strings.Join(strings.Fields(buildErr.Error()), " "), 500)
			lane.Remark = lane.Error
			lane.CompletedAt = requestedAt
			failures = append(failures, target.label()+"："+lane.Error)
		} else {
			digest := sha256.Sum256(payloadJSON)
			lane.Title = platformTitle
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
			target, ok := cfg.target(lane.AccountID)
			if !ok || !externalDistributionLaneEligible(current, target, cfg, mode) {
				return false
			}
		}
		// 检查期间有人改了这个平台的标题/摘要/封面：按旧值生成的结果作废，下一轮按新值重来。
		for accountID, editedAt := range draftEditedAt {
			if draft, _ := externalDraftFor(current, accountID); draft.EditedAt != editedAt {
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
		for _, result := range checked {
			if _, exists := externalDraftFor(*current, result.target.ID); exists || len(result.issues) > 0 || result.autoCover != "" || result.captionsChanged {
				recordExternalPreflight(current, result.target, result.issues, result.candidates, material, requestedAt)
			}
			if result.captionsChanged {
				draft, _ := externalDraftFor(*current, result.target.ID)
				draft.Captions = result.captions
				setExternalDraft(current, draft)
			}
			if result.autoCover != "" {
				draft, _ := externalDraftFor(*current, result.target.ID)
				draft.CoverURL = result.autoCover
				setExternalDraft(current, draft)
			}
		}
		noteExternalReminderProgress(current)
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
	p.scheduleExternalReminder(updated.ID)
	if len(fixNames) > 0 {
		p.spawnExternalWork(func() { p.prepareExternalDraftAssets(jobID) })
	}
	for _, accountID := range queuedAccounts {
		accountID := accountID
		p.spawnExternalWork(func() { p.runExternalLane(jobID, accountID) })
	}
	if len(queuedAccounts) == 0 {
		if len(fixNames) > 0 && len(failures) == 0 {
			return updated, fmt.Errorf("%w（%s）", errExternalLanesNeedFix, strings.Join(fixNames, "；"))
		}
		return updated, fmt.Errorf("%w：%s", errExternalLanesNotQueued, strings.Join(append(failures, fixNames...), "；"))
	}
	return updated, nil
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
	if lane.State == publishExternalDistributionQueued && lane.Driver == externalDriverRobot {
		p.pushRobotLane(job, lane)
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

// claimExternalLanePush 外调前落 intent：queued → pushing。抢不到（已被别的 worker 推、内容已换）就不外调。
func (p *publishPipeline) claimExternalLanePush(jobID int64, lane publishExternalLane, intentKey string) bool {
	intentAt := pipelineNow(p).Format(time.RFC3339)
	_, swapped, err := p.updateExternalLane(jobID, lane.AccountID, func(current publishExternalLane) bool {
		return current.State == publishExternalDistributionQueued && current.IntentKey == "" &&
			current.PayloadHash == lane.PayloadHash
	}, func(_ *publishJob, current *publishExternalLane) {
		current.State = publishExternalDistributionPushing
		current.IntentKey = intentKey
		current.IntentAt = intentAt
	})
	if err != nil {
		log.Printf("publish: persist external push intent for job %d/%s failed: %v", jobID, lane.AccountID, err)
	}
	return err == nil && swapped
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
		log.Printf("publish: record external push result for job %d/%s failed: %v", jobID, accountID, err)
		return
	}
	if swapped {
		p.scheduleExternalDistributionCardUpdate(jobID)
	}
}

// migrateExpiredExternalCard 读盘时把「卡片已过期」进的死信改记为 expired（幂等）：
// 改这个之前，过期卡会重试 10 次后进死信告警。
func migrateExpiredExternalCard(j *publishJob) {
	if j != nil && j.ExternalDistributionCardState == publishExternalCardDeadLetter &&
		strings.Contains(j.ExternalDistributionCardError, fmt.Sprintf("code=%d", feishuCodeMessageExpired)) {
		j.ExternalDistributionCardState = publishExternalCardExpired
		j.ExternalDistributionCardError = ""
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
	if errors.Is(err, errFeishuMessageExpired) {
		if _, _, persistErr := p.store.updateIf(jobID, func(current publishJob) bool {
			return current.ExternalDistributionCardSeq == claimed.ExternalDistributionCardSeq &&
				current.ExternalDistributionCardState == publishExternalCardSending
		}, func(current *publishJob) {
			current.ExternalDistributionCardState = publishExternalCardExpired
			current.ExternalDistributionCardError = ""
		}); persistErr != nil {
			log.Printf("publish: record expired external distribution card for job %d failed: %v", jobID, persistErr)
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
	p.recoverExternalReminder(job)
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
		if lane.Driver != externalDriverRobot {
			continue
		}
		switch lane.State {
		case publishExternalDistributionQueued:
			p.spawnExternalWork(func() { p.runExternalLane(job.ID, lane.AccountID) })
		case publishExternalDistributionPushing:
			p.recoverRobotLane(job, lane)
		case publishExternalDistributionPublishing, publishExternalDistributionReviewing:
			// 发布中退出的：分发服务那边没有这次发布就调一次，有就接着跟；审核中的接着跟。
			p.spawnExternalWork(func() { p.followRobotLaneNow(job.ID, lane.AccountID) })
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
	if cfg, err := loadExternalTargets(); err == nil && len(externalDistributionTargetsFor(job, cfg, externalQueueCard, nil)) > 0 {
		return reviewCardOutcome{}, false
	}
	switch externalDistributionSummaryState(job) {
	case publishExternalDistributionPolling:
		return reviewCardOutcome{toastType: "info", toastContent: "平台正在审核，结果出来会更新卡片"}, true
	case publishExternalDistributionSucceeded:
		text := "文章已同步至其他平台，无需重复操作"
		if externalDistributionAnyHeld(job) {
			text = "有平台停在草稿、没有发布，请看卡片"
		}
		return reviewCardOutcome{toastType: "info", toastContent: text}, true
	case publishExternalDistributionReconciliationRequired:
		return reviewCardOutcome{
			toastType: "error", toastContent: "外部分发结果待核对，系统不会重复同步",
		}, true
	case publishExternalDistributionFailed, publishExternalDistributionPartialFailed:
		return reviewCardOutcome{toastType: "error", toastContent: "外部分发有平台未完成，请查看卡片"}, true
	default:
		return reviewCardOutcome{
			toastType: "error", toastContent: "外部分发状态异常，请到管理后台核对",
		}, true
	}
}

// externalDistributionAnyHeld 有平台停在草稿、没发（读回与原文对不上，或 2026-10-30 之前只存草稿）。
func externalDistributionAnyHeld(job publishJob) bool {
	for _, lane := range job.ExternalDistributionLanes {
		if lane.State == publishExternalDistributionDraftSaved {
			return true
		}
	}
	return false
}

// refreshExternalDistributionResults 经分发服务、存草稿结果不明的 lane 每轮按幂等键查一次，把结果补回任务和审核卡
// （查到存好了照样转发布中再发）。发布中、审核中的由 followRobotLanes 每分钟跟。
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
	var robot distributionRobotAPI
	for _, job := range jobs {
		for _, lane := range job.ExternalDistributionLanes {
			if !robotLaneAwaitingResult(lane, now) {
				continue
			}
			if robot == nil {
				if robot, err = p.robotClient(); err != nil {
					log.Printf("publish: 外发结果补查无法创建分发工具客户端：%v", err)
					return
				}
			}
			p.resolveRobotLaneWithClient(job.ID, lane, robot)
		}
	}
}
