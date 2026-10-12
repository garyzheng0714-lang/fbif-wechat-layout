package main

// publish_external_drafts.go — 外发推送前检查与平台改稿。
//
// 2026-10-12 用户裁定：「标题超限（比如头条标题限制 30 个字，但我们写了 31 或 32 个字），
// 应该支持在群里 @ 对应的人，提醒去更换标题和封面图」「摘要字数超限也是同理」「能否直接在
// 卡片里改好？不过这里一定要做好引导，提示清楚要把什么改成什么」。
//
// 云梭受理后就改不了（没有改稿接口），所以检查放在推送前：每个平台按自己的规则
// （yunsuo_platform_profiles.go）查标题、摘要字数与封面尺寸，有问题这个平台先不推、不占
// 名额，记成「待修改」；审核卡（feishu_review_card_distribution.go）在这个平台的块里直接给出
// 现在是什么、要求多少、建议改成什么，并 @ 分发负责人（publish_external_reminders.go）。
// 改的只是这个平台推出去的版本，公众号和网站上的文章不动。改好后马上重新检查，通过即推
// （auto 账号仍按平台配额排队）。

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	publishExternalIssueTitle   = "title"
	publishExternalIssueSummary = "summary"
	publishExternalIssueCover   = "cover"

	// 封面候选：只看正文前 15 张图，最多给 6 张（卡片里三张一行）。
	publishExternalCoverScan       = 15
	publishExternalCoverCandidates = 6
	// 上传的封面最多留几张（侧边栏可以多次上传）。
	publishExternalUploadedCovers = 3
)

// publishExternalDraft 一个平台推送前的待改项与人改过的值，按账号一条。
type publishExternalDraft struct {
	AccountID string `json:"account_id"`
	// 人给这个平台改过的值；空表示用文章原值。
	Title    string `json:"title,omitempty"`
	Summary  string `json:"summary,omitempty"`
	CoverURL string `json:"cover_url,omitempty"`
	EditedBy string `json:"edited_by,omitempty"`
	EditedAt string `json:"edited_at,omitempty"`
	// 最近一次推送前检查发现的问题；空表示可以推。
	Issues    []publishExternalIssue `json:"issues,omitempty"`
	CheckedAt string                 `json:"checked_at,omitempty"`
	// 检查时文章的标题/摘要/封面/正文摘要；文章变了就要重查，没变就不必每轮都去网站取稿。
	CheckedMaterial string `json:"checked_material,omitempty"`
	// 建议改成的标题/摘要（先在标点处截短，AI 缩写好了再换上）。
	SuggestedTitle   string `json:"suggested_title,omitempty"`
	SuggestedSummary string `json:"suggested_summary,omitempty"`
	SuggestedBy      string `json:"suggested_by,omitempty"` // cut / ai
	// PreparedFor 后台已为哪一次检查（CheckedAt）准备过 AI 建议与卡片图；同一次检查只准备一回。
	PreparedFor string `json:"prepared_for,omitempty"`
	// 可换的封面：正文里够大的图，加上侧边栏上传的图。
	Candidates []publishExternalCoverCandidate `json:"candidates,omitempty"`
}

type publishExternalIssue struct {
	Field   string `json:"field"`
	Current string `json:"current,omitempty"`
	Length  int    `json:"length,omitempty"`
	Min     int    `json:"min,omitempty"`
	Max     int    `json:"max,omitempty"`
	// 封面：现在的尺寸与平台下限；Missing 表示没有可用的封面。
	Width     int  `json:"width,omitempty"`
	Height    int  `json:"height,omitempty"`
	MinWidth  int  `json:"min_width,omitempty"`
	MinHeight int  `json:"min_height,omitempty"`
	Missing   bool `json:"missing,omitempty"`
}

type publishExternalCoverCandidate struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// 飞书卡片里展示用的图（按审核应用上传，换应用后重传）。
	ImgKey     string `json:"img_key,omitempty"`
	ImgApp     string `json:"img_app,omitempty"`
	UploadedBy string `json:"uploaded_by,omitempty"`
	// MessageID 话题里发来这张图的消息（飞书重投同一事件时不重复处理）。
	MessageID string `json:"message_id,omitempty"`
}

func externalDraftIndex(job publishJob, accountID string) int {
	accountID = strings.TrimSpace(accountID)
	for i, draft := range job.ExternalDistributionDrafts {
		if strings.TrimSpace(draft.AccountID) == accountID {
			return i
		}
	}
	return -1
}

func externalDraftFor(job publishJob, accountID string) (publishExternalDraft, bool) {
	if i := externalDraftIndex(job, accountID); i >= 0 {
		return job.ExternalDistributionDrafts[i], true
	}
	return publishExternalDraft{AccountID: strings.TrimSpace(accountID)}, false
}

func setExternalDraft(job *publishJob, draft publishExternalDraft) {
	if i := externalDraftIndex(*job, draft.AccountID); i >= 0 {
		job.ExternalDistributionDrafts[i] = draft
		return
	}
	job.ExternalDistributionDrafts = append(job.ExternalDistributionDrafts, draft)
}

// externalDraftMaterial 文章层面会影响检查结果的字段。
func externalDraftMaterial(job publishJob) string {
	material, _ := json.Marshal([]string{job.Title, job.Summary, job.CoverURL, job.ContentHash,
		strconv.FormatInt(job.ContentVersion, 10), strconv.FormatInt(job.ContentFrozenVersion, 10)})
	digest := sha256.Sum256(material)
	return fmt.Sprintf("sha256:%x", digest[:12])
}

// externalDraftBlocked 这个平台卡在待修改、文章也没变：等人改，不必再去网站取稿重查。
func externalDraftBlocked(job publishJob, accountID string) bool {
	draft, ok := externalDraftFor(job, accountID)
	return ok && len(draft.Issues) > 0 && draft.CheckedMaterial == externalDraftMaterial(job)
}

// externalJobNeedsFix 有平台待修改（还没推、卡在推送前检查）。
func externalJobNeedsFix(job publishJob) bool {
	for _, draft := range job.ExternalDistributionDrafts {
		if len(draft.Issues) > 0 {
			if _, pushed := externalLaneFor(job, draft.AccountID); !pushed {
				return true
			}
		}
	}
	return false
}

func runeLen(value string) int { return len([]rune(value)) }

// externalEffectiveTitle 推给这个平台的标题（人改过的优先）。
func externalEffectiveTitle(job publishJob, draft publishExternalDraft) string {
	if title := compactYunsuoText(draft.Title); title != "" {
		return title
	}
	return compactYunsuoText(job.Title)
}

func externalEffectiveSummary(job publishJob, draft publishExternalDraft) string {
	if summary := compactYunsuoText(draft.Summary); summary != "" {
		return summary
	}
	return compactYunsuoText(job.Summary)
}

// externalTextIssues 标题、摘要的字数检查（纯计算，卡片回调里也能当场重算）。
func externalTextIssues(job publishJob, profile yunsuoPlatformProfile, draft publishExternalDraft) []publishExternalIssue {
	var issues []publishExternalIssue
	title := externalEffectiveTitle(job, draft)
	min, max := profile.titleLimits()
	if runeLen(title) > max {
		issues = append(issues, publishExternalIssue{Field: publishExternalIssueTitle, Current: title, Length: runeLen(title), Min: min, Max: max})
	}
	if profile.SummaryMax > 0 {
		if summary := externalEffectiveSummary(job, draft); runeLen(summary) > profile.SummaryMax {
			issues = append(issues, publishExternalIssue{Field: publishExternalIssueSummary, Current: summary, Length: runeLen(summary), Max: profile.SummaryMax})
		}
	}
	return issues
}

// externalCoverIssue 封面检查：人换过封面就查那张；否则与推送同口径——正文第一张图够大就用它，
// 不够退回公众号封面。读不到尺寸的不拦（交给平台判断）。
func externalCoverIssue(job publishJob, profile yunsuoPlatformProfile, draft publishExternalDraft, blocks []yunsuoBlock, imageMap map[string]string) *publishExternalIssue {
	if profile.CoverMinWidth <= 0 && profile.CoverMinHeight <= 0 {
		return nil
	}
	tooSmall := func(src string) *publishExternalIssue {
		width, height, ok := yunsuoImageSize(src)
		if !ok || (width >= profile.CoverMinWidth && height >= profile.CoverMinHeight) {
			return nil
		}
		return &publishExternalIssue{Field: publishExternalIssueCover, Current: src, Width: width, Height: height,
			MinWidth: profile.CoverMinWidth, MinHeight: profile.CoverMinHeight}
	}
	if chosen := publicYunsuoMediaURL(strings.TrimSpace(draft.CoverURL)); chosen != "" {
		for _, candidate := range draft.Candidates {
			if candidate.URL == chosen && candidate.Width >= profile.CoverMinWidth && candidate.Height >= profile.CoverMinHeight {
				return nil
			}
		}
		return tooSmall(chosen)
	}
	if dist := prepareYunsuoDistributionBlocks(blocks, imageMap, profile); dist.Lead != "" {
		return nil
	}
	fallback := publicYunsuoMediaURL(mapYunsuoMedia(job.CoverURL, imageMap))
	if fallback == "" {
		return &publishExternalIssue{Field: publishExternalIssueCover, Missing: true,
			MinWidth: profile.CoverMinWidth, MinHeight: profile.CoverMinHeight}
	}
	return tooSmall(fallback)
}

// externalPreflight 推送前按这个平台的规则检查（会读图片尺寸，只在后台调用）。返回问题、
// 以及有封面问题时可换的封面（正文里够大的图 + 之前上传过的图）。
func externalPreflight(job publishJob, target yunsuoAccountTarget, draft publishExternalDraft) ([]publishExternalIssue, []publishExternalCoverCandidate) {
	profile := yunsuoProfileForTarget(target)
	issues := externalTextIssues(job, profile, draft)
	blocks, imageMap, err := decodeYunsuoDistributionContent(job.BlocksJSON, job.ImageMap)
	if err != nil {
		// 正文都解不开，交给构造 payload 那一步报错（受理前失败），这里不另记。
		return issues, nil
	}
	cover := externalCoverIssue(job, profile, draft, blocks, imageMap)
	if cover == nil {
		return issues, draft.Candidates
	}
	issues = append(issues, *cover)
	candidates := yunsuoCoverCandidates(blocks, imageMap, profile, publishExternalCoverScan, publishExternalCoverCandidates)
	return issues, mergeExternalCoverCandidates(candidates, draft.Candidates)
}

// mergeExternalCoverCandidates 新算的正文候选在前，保留旧的 img_key 与上传的图。
func mergeExternalCoverCandidates(fresh, previous []publishExternalCoverCandidate) []publishExternalCoverCandidate {
	byURL := map[string]publishExternalCoverCandidate{}
	for _, candidate := range previous {
		byURL[candidate.URL] = candidate
	}
	out := make([]publishExternalCoverCandidate, 0, len(fresh)+len(previous))
	seen := map[string]bool{}
	for _, candidate := range fresh {
		if old, ok := byURL[candidate.URL]; ok {
			candidate.ImgKey, candidate.ImgApp, candidate.UploadedBy = old.ImgKey, old.ImgApp, old.UploadedBy
		}
		seen[candidate.URL] = true
		out = append(out, candidate)
	}
	for _, candidate := range previous {
		if candidate.UploadedBy != "" && !seen[candidate.URL] {
			seen[candidate.URL] = true
			out = append(out, candidate)
		}
	}
	return out
}

// addExternalCoverCandidates 并进新量过的图；上传的图只留最近几张。
func addExternalCoverCandidates(current, added []publishExternalCoverCandidate) []publishExternalCoverCandidate {
	out := append([]publishExternalCoverCandidate(nil), current...)
	for _, candidate := range added {
		replaced := false
		for i := range out {
			if out[i].URL == candidate.URL {
				if candidate.UploadedBy == "" {
					candidate.UploadedBy = out[i].UploadedBy
				}
				candidate.ImgKey, candidate.ImgApp = out[i].ImgKey, out[i].ImgApp
				out[i], replaced = candidate, true
			}
		}
		if !replaced {
			out = append(out, candidate)
		}
	}
	uploaded := 0
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].UploadedBy == "" {
			continue
		}
		if uploaded++; uploaded > publishExternalUploadedCovers {
			out = append(out[:i], out[i+1:]...)
		}
	}
	return out
}

// externalFallbackSuggestions 建议值的保底：在标点处截到平台上限。AI 缩写好了再换（publish_external_reminders.go）。
func externalFallbackSuggestions(draft *publishExternalDraft, profile yunsuoPlatformProfile) {
	for _, issue := range draft.Issues {
		switch issue.Field {
		case publishExternalIssueTitle:
			if draft.SuggestedTitle == "" || runeLen(draft.SuggestedTitle) > issue.Max {
				draft.SuggestedTitle = truncateYunsuoTitle(issue.Current, issue.Min, issue.Max)
				draft.SuggestedBy = "cut"
			}
		case publishExternalIssueSummary:
			if draft.SuggestedSummary == "" || runeLen(draft.SuggestedSummary) > issue.Max {
				draft.SuggestedSummary = truncateYunsuoTitle(issue.Current, 1, issue.Max)
				draft.SuggestedBy = "cut"
			}
		}
	}
	_ = profile
}

// recordExternalPreflight 把一次推送前检查的结果写进 draft（锁内调用）。
func recordExternalPreflight(job *publishJob, target yunsuoAccountTarget, issues []publishExternalIssue, candidates []publishExternalCoverCandidate, material, now string) {
	draft, _ := externalDraftFor(*job, target.ID)
	if draft.CheckedMaterial != material {
		// 文章改过：旧建议是按旧标题/摘要写的，作废重来。
		draft.SuggestedTitle, draft.SuggestedSummary, draft.SuggestedBy = "", "", ""
	}
	draft.Issues = issues
	draft.CheckedAt = now
	draft.CheckedMaterial = material
	draft.Candidates = candidates
	if len(issues) == 0 {
		draft.SuggestedTitle, draft.SuggestedSummary, draft.SuggestedBy = "", "", ""
	} else {
		externalFallbackSuggestions(&draft, yunsuoProfileForTarget(target))
	}
	setExternalDraft(job, draft)
}

func externalIssueFields(issues []publishExternalIssue) map[string]publishExternalIssue {
	out := map[string]publishExternalIssue{}
	for _, issue := range issues {
		out[issue.Field] = issue
	}
	return out
}

// externalDraftEdit 一次人工修改（卡片表单或侧边栏）。nil 字段表示这次没改。
type externalDraftEdit struct {
	Title    *string
	Summary  *string
	CoverURL *string
	// Candidates 服务端刚量过尺寸的图（侧边栏选的正文图、上传的图），并进可选封面。
	Candidates []publishExternalCoverCandidate
}

var errExternalDraftNothingToEdit = errors.New("这个平台已经推送，内容不能再改；如需改动请到云梭后台处理")

// applyExternalDraftEdit 校验并写入修改，当场重算标题、摘要与封面问题（不读网络：封面只认
// 候选里记过尺寸的图）。返回修改后的 draft。
func applyExternalDraftEdit(job publishJob, target yunsuoAccountTarget, edit externalDraftEdit, actor string, now time.Time) (publishExternalDraft, error) {
	if lane, pushed := externalLaneFor(job, target.ID); pushed && !externalLaneCanSafeRetry(lane) {
		return publishExternalDraft{}, errExternalDraftNothingToEdit
	}
	profile := yunsuoProfileForTarget(target)
	draft, _ := externalDraftFor(job, target.ID)
	if edit.Title != nil {
		title := compactYunsuoText(*edit.Title)
		min, max := profile.titleLimits()
		switch n := runeLen(title); {
		case title == "":
			return draft, fmt.Errorf("%s标题不能为空", profile.Name)
		case n > max:
			return draft, fmt.Errorf("%s标题最多 %d 字，现在 %d 字，还要删 %d 字", profile.Name, max, n, n-max)
		case n < min:
			return draft, fmt.Errorf("%s标题至少 %d 字，现在 %d 字", profile.Name, min, n)
		}
		if title == compactYunsuoText(job.Title) {
			title = ""
		}
		draft.Title = title
	}
	if edit.Summary != nil {
		summary := compactYunsuoText(*edit.Summary)
		if profile.SummaryMax > 0 && runeLen(summary) > profile.SummaryMax {
			n := runeLen(summary)
			return draft, fmt.Errorf("%s摘要最多 %d 字，现在 %d 字，还要删 %d 字", profile.Name, profile.SummaryMax, n, n-profile.SummaryMax)
		}
		if summary == compactYunsuoText(job.Summary) {
			summary = ""
		}
		draft.Summary = summary
	}
	if len(edit.Candidates) > 0 {
		draft.Candidates = addExternalCoverCandidates(draft.Candidates, edit.Candidates)
	}
	issues := externalTextIssues(job, profile, draft)
	coverIssue, hadCoverIssue := externalIssueFields(draft.Issues)[publishExternalIssueCover]
	if edit.CoverURL != nil {
		chosen := strings.TrimSpace(*edit.CoverURL)
		var picked *publishExternalCoverCandidate
		for i := range draft.Candidates {
			if draft.Candidates[i].URL == chosen {
				picked = &draft.Candidates[i]
				break
			}
		}
		if picked == nil {
			return draft, errors.New("只能从给出的图片里选封面，或在侧边栏上传一张")
		}
		if picked.Width < profile.CoverMinWidth || picked.Height < profile.CoverMinHeight {
			return draft, fmt.Errorf("这张图 %d×%d，%s封面至少 %d×%d", picked.Width, picked.Height, profile.Name, profile.CoverMinWidth, profile.CoverMinHeight)
		}
		draft.CoverURL = chosen
	} else if hadCoverIssue && strings.TrimSpace(draft.CoverURL) == "" {
		issues = append(issues, coverIssue)
	}
	draft.Issues = issues
	draft.EditedBy = strings.TrimSpace(actor)
	draft.EditedAt = now.UTC().Format(time.RFC3339)
	return draft, nil
}

// externalTargetsByProfile 卡片按钮只带平台位置（配置里的第几个账号），不带账号 id（红线 37）。
func externalTargetBySlot(cfg yunsuoConfig, slot int) (yunsuoAccountTarget, bool) {
	if slot < 0 || slot >= len(cfg.Targets) {
		return yunsuoAccountTarget{}, false
	}
	return cfg.Targets[slot], true
}

func externalTargetSlot(cfg yunsuoConfig, accountID string) int {
	for i, target := range cfg.Targets {
		if strings.TrimSpace(target.ID) == strings.TrimSpace(accountID) {
			return i
		}
	}
	return -1
}

// submitExternalDraftEdit 卡片表单与侧边栏共用的唯一修改入口：锁内校验并写入，问题都清了就
// 立即安排推送——auto 账号挂回自动外发待办（按配额推），其余账号直接按卡片发起推送。
func (p *publishPipeline) submitExternalDraftEdit(jobID int64, slot int, edit externalDraftEdit, actor string) (publishJob, error) {
	if publishWorkersPaused() {
		job, _ := p.store.byID(jobID)
		return job, errPublishWorkersPaused
	}
	cfg, err := loadYunsuoConfig()
	if err != nil {
		return publishJob{}, err
	}
	target, ok := externalTargetBySlot(cfg, slot)
	if !ok {
		return publishJob{}, errors.New("分发平台配置已变化，请使用最新卡片")
	}
	if strings.TrimSpace(actor) == "" {
		return publishJob{}, errors.New("修改必须由审核群成员或分发负责人发起")
	}
	var editErr error
	var saved publishExternalDraft
	updated, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		if eligibility := externalDistributionEligibility(current); eligibility != nil {
			editErr = eligibility
			return false
		}
		saved, editErr = applyExternalDraftEdit(current, target, edit, actor, pipelineNow(p))
		return editErr == nil
	}, func(current *publishJob) {
		setExternalDraft(current, saved)
		ready := len(saved.Issues) == 0
		if ready && target.Auto {
			if _, pushed := externalLaneFor(*current, target.ID); !pushed && current.ExternalDistributionAutoPending == "" {
				current.ExternalDistributionAutoPending = publishExternalAutoKindPublish
				current.ExternalDistributionAutoError = ""
			}
		}
		noteExternalReminderProgress(current)
		enqueueExternalDistributionCardUpdate(current)
	})
	if err != nil {
		return updated, err
	}
	if !swapped {
		if editErr != nil {
			return updated, editErr
		}
		return updated, errors.New("文章状态已变化，请使用最新卡片")
	}
	p.scheduleExternalDistributionCardUpdate(jobID)
	p.scheduleExternalReminder(jobID)
	if len(saved.Issues) > 0 {
		return updated, nil
	}
	if target.Auto {
		if _, pushed := externalLaneFor(updated, target.ID); !pushed {
			p.kickExternalAutoDistribution()
			return updated, nil
		}
	}
	// 非 auto 账号，或受理前失败的 auto 账号：人已经确认过，直接推这一个平台。
	pushed, pushErr := p.queueExternalDistribution(jobID, actor, publishExternalDistributionCardVersion(updated), externalQueueCard,
		map[string]bool{strings.TrimSpace(target.ID): true})
	if pushErr != nil && !errors.Is(pushErr, errExternalLanesNotQueued) && !errors.Is(pushErr, errExternalLanesNeedFix) {
		return pushed, pushErr
	}
	return pushed, nil
}

// externalDistributionDraftSignature 卡片版本里各平台待改项与改过的值：别人先改了，旧卡的表单就失效。
func externalDistributionDraftSignature(job publishJob) []string {
	out := make([]string, 0, len(job.ExternalDistributionDrafts))
	for _, draft := range job.ExternalDistributionDrafts {
		fields := make([]string, 0, len(draft.Issues))
		for _, issue := range draft.Issues {
			fields = append(fields, issue.Field)
		}
		material, _ := json.Marshal([]string{draft.AccountID, strings.Join(fields, ","), draft.Title, draft.Summary, draft.CoverURL, draft.EditedAt})
		digest := sha256.Sum256(material)
		out = append(out, fmt.Sprintf("%x", digest[:6]))
	}
	return out
}
