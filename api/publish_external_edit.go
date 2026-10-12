package main

// publish_external_edit.go — 外发平台改稿的侧边栏页面接口。
//
// 2026-10-12 用户裁定：卡片里改不了的（主要是上传封面），做一个页面，从卡片链接在飞书
// 侧边栏打开（AppLink mode=sidebar-semi），改完状态同步回卡片。页面 public/distribute.html；
// 写入与卡片表单共用 submitExternalDraftEdit，规则只有一份。
//
// 鉴权：必须飞书登录（记下是谁改的），且持有这篇文章的签名链接（从审核卡/提醒点进来）或是
// 规则管理员。签名与预览签名同法，由 AUTH_SESSION_SECRET 派生独立子密钥，不外泄。

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	_ "image/jpeg"
	_ "image/png"
)

const maxExternalCoverUploadBytes = 10 << 20

func externalDistributionEditSig(jobID int64) string {
	secret := strings.TrimSpace(os.Getenv("AUTH_SESSION_SECRET"))
	if secret == "" || jobID <= 0 {
		return ""
	}
	derived := sha256.Sum256(append([]byte("fbif-distribution-edit\x00"), []byte(secret)...))
	mac := hmac.New(sha256.New, derived[:])
	mac.Write([]byte("distribution-edit:" + strconv.FormatInt(jobID, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

func externalDistributionEditSigValid(jobID int64, token string) bool {
	want := externalDistributionEditSig(jobID)
	return want != "" && hmac.Equal([]byte(want), []byte(strings.TrimSpace(token)))
}

// externalDistributionEditURL 改稿页面地址（浏览器直接打开也能用）。
func externalDistributionEditURL(jobID int64) string {
	sig := externalDistributionEditSig(jobID)
	if sig == "" {
		return ""
	}
	return publishPreviewBaseURL() + "/distribute.html?job=" + strconv.FormatInt(jobID, 10) + "&t=" + sig
}

// externalDistributionSidebarLink 在飞书里以侧边栏打开改稿页面。
func externalDistributionSidebarLink(jobID int64) string {
	target := externalDistributionEditURL(jobID)
	if target == "" {
		return ""
	}
	return "https://applink.feishu.cn/client/web_url/open?mode=sidebar-semi&url=" + url.QueryEscape(target)
}

// externalPlatformStatus 一个平台现在的状态（卡片与侧边栏共用）。tone：green/blue/orange/red/grey。
type externalPlatformState struct {
	Status   string
	Tone     string
	Reason   string
	URL      string
	Title    string // 平台上的标题（推送时冻结）
	Editable bool   // 还能改标题/摘要/封面（没推过，或受理前失败）
	NeedsFix bool
}

func externalPlatformStatus(job publishJob, target yunsuoAccountTarget) externalPlatformState {
	if lane, ok := externalLaneFor(job, target.ID); ok {
		state := externalPlatformState{URL: reviewCardSafeURL(lane.URL), Title: strings.TrimSpace(lane.Title)}
		switch {
		case lane.State == publishExternalLaneManual:
			state.Status, state.Tone = "人工已发", "green"
		case lane.State == publishExternalDistributionSucceeded:
			state.Status, state.Tone = "已发布", "green"
		case publishExternalDistributionProcessing(lane.State):
			state.Status, state.Tone = "同步中", "blue"
		case lane.State == publishExternalDistributionReconciliationRequired:
			state.Status, state.Tone = "待核对", "orange"
			state.Reason = "结果未确认，请先到云梭后台核对；系统不会重复推送"
		default:
			state.Status, state.Tone = "失败", "red"
			state.Reason = firstNonEmpty(externalLaneFailureReason(lane), "请在云梭后台处理")
			state.Editable = externalLaneCanSafeRetry(lane)
		}
		return state
	}
	state := externalPlatformState{Status: "未同步", Tone: "grey", Editable: true}
	if draft, ok := externalDraftFor(job, target.ID); ok && len(draft.Issues) > 0 {
		state.Status, state.Tone, state.NeedsFix = "待修改", "orange", true
		state.Reason = externalIssueSummaryText(draft.Issues)
	} else if target.Auto && strings.TrimSpace(job.ExternalDistributionAutoPending) != "" {
		state.Status, state.Tone = "排队中", "grey"
	}
	return state
}

type externalEditImage struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Upload bool   `json:"uploaded,omitempty"`
}

type externalEditPlatform struct {
	Slot     int    `json:"slot"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Tone     string `json:"tone"`
	Reason   string `json:"reason,omitempty"`
	URL      string `json:"url,omitempty"`
	Editable bool   `json:"editable"`
	NeedsFix bool   `json:"needs_fix"`
	Auto     bool   `json:"auto"`

	Title          string `json:"title"`
	TitleMin       int    `json:"title_min"`
	TitleMax       int    `json:"title_max"`
	TitleSuggested string `json:"title_suggested,omitempty"`
	TitleIssue     bool   `json:"title_issue"`

	Summary          string `json:"summary"`
	SummaryMax       int    `json:"summary_max"`
	SummarySuggested string `json:"summary_suggested,omitempty"`
	SummaryIssue     bool   `json:"summary_issue"`

	Cover          *externalEditImage  `json:"cover,omitempty"`
	CoverChosen    bool                `json:"cover_chosen"`
	CoverIssue     string              `json:"cover_issue,omitempty"`
	CoverMinWidth  int                 `json:"cover_min_width"`
	CoverMinHeight int                 `json:"cover_min_height"`
	Candidates     []externalEditImage `json:"candidates"`

	EditedBy string `json:"edited_by,omitempty"`
	EditedAt string `json:"edited_at,omitempty"`
}

// externalEditView 侧边栏数据：每个平台的状态、现在推出去会用的标题/摘要/封面、上限与建议、
// 可选封面。会读正文图片尺寸（只在请求里算，不写库）。
func externalEditView(job publishJob, cfg yunsuoConfig) map[string]any {
	blocks, imageMap, decodeErr := decodeYunsuoDistributionContent(job.BlocksJSON, job.ImageMap)
	var bodyImages []publishExternalCoverCandidate
	if decodeErr == nil {
		bodyImages = yunsuoBodyImages(blocks, imageMap, publishExternalCoverScan)
	}
	platforms := make([]externalEditPlatform, 0, len(cfg.Targets))
	for slot, target := range cfg.Targets {
		profile := yunsuoProfileForTarget(target)
		draft, _ := externalDraftFor(job, target.ID)
		state := externalPlatformStatus(job, target)
		min, max := profile.titleLimits()
		view := externalEditPlatform{
			Slot: slot, Name: profile.Name, Status: state.Status, Tone: state.Tone, Reason: state.Reason,
			URL: state.URL, Editable: state.Editable, NeedsFix: state.NeedsFix, Auto: target.Auto,
			Title: externalEffectiveTitle(job, draft), TitleMin: min, TitleMax: max,
			Summary: externalEffectiveSummary(job, draft), SummaryMax: profile.SummaryMax,
			CoverMinWidth: profile.CoverMinWidth, CoverMinHeight: profile.CoverMinHeight,
			EditedBy: draft.EditedBy, EditedAt: draft.EditedAt, Candidates: []externalEditImage{},
		}
		if strings.TrimSpace(target.Name) != "" && target.Name != profile.Name {
			view.Name = target.Name
		}
		if !state.Editable && state.Title != "" {
			view.Title = state.Title
		}
		issues := externalIssueFields(draft.Issues)
		if issue, ok := issues[publishExternalIssueTitle]; ok && state.NeedsFix {
			view.TitleIssue, view.TitleSuggested = true, draft.SuggestedTitle
			_ = issue
		}
		if _, ok := issues[publishExternalIssueSummary]; ok && state.NeedsFix {
			view.SummaryIssue, view.SummarySuggested = true, draft.SuggestedSummary
		}
		if issue, ok := issues[publishExternalIssueCover]; ok && state.NeedsFix {
			view.CoverIssue = externalIssueDetail(issue)
			if issue.Current != "" {
				view.Cover = &externalEditImage{URL: issue.Current, Width: issue.Width, Height: issue.Height}
			}
		}
		if state.Editable {
			if chosen := strings.TrimSpace(draft.CoverURL); chosen != "" {
				view.CoverChosen = true
				view.Cover = &externalEditImage{URL: chosen}
				for _, candidate := range draft.Candidates {
					if candidate.URL == chosen {
						view.Cover.Width, view.Cover.Height = candidate.Width, candidate.Height
					}
				}
			} else if view.Cover == nil && decodeErr == nil {
				view.Cover = externalCurrentCover(job, profile, blocks, imageMap, bodyImages)
			}
			seen := map[string]bool{}
			add := func(candidate publishExternalCoverCandidate) {
				if seen[candidate.URL] || candidate.Width < profile.CoverMinWidth || candidate.Height < profile.CoverMinHeight {
					return
				}
				seen[candidate.URL] = true
				view.Candidates = append(view.Candidates, externalEditImage{URL: candidate.URL, Width: candidate.Width,
					Height: candidate.Height, Upload: candidate.UploadedBy != ""})
			}
			for _, candidate := range draft.Candidates {
				if candidate.UploadedBy != "" {
					add(candidate)
				}
			}
			for _, candidate := range bodyImages {
				add(candidate)
			}
		}
		platforms = append(platforms, view)
	}
	return map[string]any{
		"job": map[string]any{
			"id": job.ID, "title": job.Title, "summary": job.Summary,
			"site_url": externalEditSiteURL(job),
		},
		"platforms": platforms,
	}
}

func externalEditSiteURL(job publishJob) string {
	_, siteURL := reviewBatchSiteLink(job)
	return siteURL
}

// externalCurrentCover 没换过封面时推送会用哪张：与推送同一套规则（正文第一张够大的图，否则公众号封面）。
func externalCurrentCover(job publishJob, profile yunsuoPlatformProfile, blocks []yunsuoBlock, imageMap map[string]string, bodyImages []publishExternalCoverCandidate) *externalEditImage {
	src := prepareYunsuoDistributionBlocks(blocks, imageMap, profile).Lead
	if src == "" {
		src = publicYunsuoMediaURL(mapYunsuoMedia(job.CoverURL, imageMap))
	}
	if src == "" {
		return nil
	}
	cover := &externalEditImage{URL: src}
	for _, image := range bodyImages {
		if image.URL == src {
			cover.Width, cover.Height = image.Width, image.Height
			return cover
		}
	}
	if width, height, ok := yunsuoImageSize(src); ok {
		cover.Width, cover.Height = width, height
	}
	return cover
}

// requireExternalEditAccess 飞书登录 + （签名链接或规则管理员）。返回记录用的 actor。
func requireExternalEditAccess(w http.ResponseWriter, r *http.Request, auth *authService, jobID int64) (string, bool) {
	if auth == nil {
		writeError(w, http.StatusServiceUnavailable, "登录服务未配置")
		return "", false
	}
	user, err := auth.currentUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication_required")
		return "", false
	}
	// 签名链接只认飞书登录的人（审核群成员从卡片点进来）；其余只有规则管理员能改。
	if user.AuthMode != authModeFeishu || !externalDistributionEditSigValid(jobID, r.URL.Query().Get("t")) {
		if _, err := auth.requireRuleAdmin(r); err != nil {
			writeError(w, http.StatusForbidden, "请从审核卡里的链接打开")
			return "", false
		}
	}
	actor := strings.TrimSpace(user.Name)
	if id := strings.TrimSpace(user.FeishuOpenID); id != "" {
		actor = firstNonEmpty(actor, "飞书用户") + "（" + id + "）"
	} else {
		actor = firstNonEmpty(actor, user.ID)
	}
	return actor, true
}

func externalEditJobID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "文章不存在")
		return 0, false
	}
	return id, true
}

func registerExternalDistributionEditRoutes(mux *http.ServeMux, p *publishPipeline, auth *authService) {
	mux.HandleFunc("GET /api/publish/jobs/{id}/external-distribution/draft", func(w http.ResponseWriter, r *http.Request) {
		jobID, ok := externalEditJobID(w, r)
		if !ok {
			return
		}
		if _, ok := requireExternalEditAccess(w, r, auth, jobID); !ok {
			return
		}
		job, err := p.store.byID(jobID)
		if err != nil {
			writeError(w, http.StatusNotFound, "文章不存在")
			return
		}
		cfg, err := loadYunsuoConfig()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		if err := externalDistributionEligibility(job); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, externalEditView(job, cfg))
	})

	mux.HandleFunc("PUT /api/publish/jobs/{id}/external-distribution/draft", func(w http.ResponseWriter, r *http.Request) {
		jobID, ok := externalEditJobID(w, r)
		if !ok {
			return
		}
		actor, ok := requireExternalEditAccess(w, r, auth, jobID)
		if !ok {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		var body struct {
			Slot     *int    `json:"slot"`
			Title    *string `json:"title"`
			Summary  *string `json:"summary"`
			CoverURL *string `json:"cover_url"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.Slot == nil {
			writeError(w, http.StatusBadRequest, "缺少平台")
			return
		}
		edit := externalDraftEdit{Title: body.Title, Summary: body.Summary, CoverURL: body.CoverURL}
		if body.CoverURL != nil {
			// 正文里的图：在服务端重新量尺寸，作为可选封面带进去（不信浏览器给的尺寸）。
			job, err := p.store.byID(jobID)
			if err != nil {
				writeError(w, http.StatusNotFound, "文章不存在")
				return
			}
			if blocks, imageMap, err := decodeYunsuoDistributionContent(job.BlocksJSON, job.ImageMap); err == nil {
				for _, image := range yunsuoBodyImages(blocks, imageMap, publishExternalCoverScan) {
					if image.URL == strings.TrimSpace(*body.CoverURL) {
						edit.Candidates = append(edit.Candidates, image)
					}
				}
			}
		}
		updated, err := p.submitExternalDraftEdit(jobID, *body.Slot, edit, actor)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		cfg, err := loadYunsuoConfig()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, externalEditView(updated, cfg))
	})

	mux.HandleFunc("POST /api/publish/jobs/{id}/external-distribution/cover", func(w http.ResponseWriter, r *http.Request) {
		jobID, ok := externalEditJobID(w, r)
		if !ok {
			return
		}
		actor, ok := requireExternalEditAccess(w, r, auth, jobID)
		if !ok {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxExternalCoverUploadBytes+(1<<20))
		if err := r.ParseMultipartForm(maxExternalCoverUploadBytes); err != nil {
			writeError(w, http.StatusBadRequest, "图片不能超过 10 MB")
			return
		}
		slot, err := strconv.Atoi(strings.TrimSpace(r.FormValue("slot")))
		if err != nil {
			writeError(w, http.StatusBadRequest, "缺少平台")
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "没有收到图片")
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxExternalCoverUploadBytes+1))
		if err != nil || len(data) > maxExternalCoverUploadBytes {
			writeError(w, http.StatusBadRequest, "图片不能超过 10 MB")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		updated, uploaded, err := p.uploadExternalCover(ctx, jobID, slot, data, actor)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cfg, err := loadYunsuoConfig()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		view := externalEditView(updated, cfg)
		view["uploaded"] = externalEditImage{URL: uploaded.URL, Width: uploaded.Width, Height: uploaded.Height, Upload: true}
		writeJSON(w, http.StatusOK, view)
	})
}

// uploadExternalCover 侧边栏上传封面：校验格式与平台尺寸下限，转存 OSS，作为这个平台的封面。
func (p *publishPipeline) uploadExternalCover(ctx context.Context, jobID int64, slot int, data []byte, actor string) (publishJob, publishExternalCoverCandidate, error) {
	cfg, err := loadYunsuoConfig()
	if err != nil {
		return publishJob{}, publishExternalCoverCandidate{}, err
	}
	target, ok := externalTargetBySlot(cfg, slot)
	if !ok {
		return publishJob{}, publishExternalCoverCandidate{}, errors.New("分发平台配置已变化，请刷新")
	}
	profile := yunsuoProfileForTarget(target)
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return publishJob{}, publishExternalCoverCandidate{}, errors.New("只支持 JPG、PNG 图片")
	}
	mime := map[string]string{"jpeg": "image/jpeg", "png": "image/png"}[format]
	if mime == "" {
		return publishJob{}, publishExternalCoverCandidate{}, errors.New("只支持 JPG、PNG 图片")
	}
	if config.Width < profile.CoverMinWidth || config.Height < profile.CoverMinHeight {
		return publishJob{}, publishExternalCoverCandidate{}, fmt.Errorf("这张图 %d×%d，%s封面至少 %d×%d", config.Width, config.Height, profile.Name, profile.CoverMinWidth, profile.CoverMinHeight)
	}
	job, err := p.store.byID(jobID)
	if err != nil {
		return publishJob{}, publishExternalCoverCandidate{}, err
	}
	if err := externalDistributionEligibility(job); err != nil {
		return job, publishExternalCoverCandidate{}, err
	}
	digest := sha256.Sum256(data)
	name := fmt.Sprintf("distribution-cover-%d-%x.%s", jobID, digest[:8], map[string]string{"jpeg": "jpg", "png": "png"}[format])
	var stored string
	if p.externalCoverStore != nil {
		stored, err = p.externalCoverStore(ctx, name, data)
	} else {
		var results map[string]string
		results, err = ossUploadByBase64Images(ctx, map[string]string{name: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)})
		stored = results[name]
	}
	if err != nil {
		return job, publishExternalCoverCandidate{}, fmt.Errorf("图片转存失败：%w", err)
	}
	stored = publicYunsuoMediaURL(strings.TrimSpace(stored))
	if stored == "" {
		return job, publishExternalCoverCandidate{}, errors.New("图片转存失败，请重试")
	}
	candidate := publishExternalCoverCandidate{URL: stored, Width: config.Width, Height: config.Height, UploadedBy: actor}
	// 只加进可选封面，不当场推送：页面里还可能有没保存的标题改动，等人点「保存并同步」一起提交。
	updated, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		if externalDistributionEligibility(current) != nil {
			return false
		}
		lane, pushed := externalLaneFor(current, target.ID)
		return !pushed || externalLaneCanSafeRetry(lane)
	}, func(current *publishJob) {
		draft, _ := externalDraftFor(*current, target.ID)
		draft.Candidates = addExternalCoverCandidates(draft.Candidates, []publishExternalCoverCandidate{candidate})
		setExternalDraft(current, draft)
		enqueueExternalDistributionCardUpdate(current)
	})
	if err != nil {
		return updated, candidate, err
	}
	if !swapped {
		return updated, candidate, errExternalDraftNothingToEdit
	}
	p.scheduleExternalDistributionCardUpdate(jobID)
	p.spawnExternalWork(func() { p.prepareExternalDraftAssets(jobID) })
	return updated, candidate, nil
}
