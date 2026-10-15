package main

// publish_distribution_owners.go — 分发负责人、校对负责人与手改周报接收人（2026-10-12 用户裁定：@ 固定负责人；
// 2026-10-13 校对提醒「@对应的人」；2026-10-15 手改周报私发）。
//
// 外发平台的标题、摘要或封面不合要求时，系统在审核卡下回复一条提醒并 @ 分发负责人
// （publish_external_reminders.go）；风险检查发现确定无疑的错误时 @ 校对负责人（publish_proofread.go）；
// 每周一把上周编辑手改最多的问题私信发给手改周报接收人（publish_edit_report_delivery.go）。
// 人只能从审核群成员里选：成员目录由审核应用实时读取，存的是这个应用下的 open_id，换审核应用后要重新选。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const maxPublishDistributionOwners = 10

const (
	publishOwnerRoleDistribution = "distribution"
	publishOwnerRoleProofread    = "proofread"
	publishOwnerRoleEditReport   = "edit_report"
)

// publishOwnersFor 某类负责人与他们的 open_id 所属的审核应用。
func publishOwnersFor(settings publishDeliverySettings, role string) ([]publishDistributionOwner, string) {
	switch role {
	case publishOwnerRoleProofread:
		return settings.ProofreadOwners, settings.ProofreadOwnersApp
	case publishOwnerRoleEditReport:
		return settings.EditReportOwners, settings.EditReportOwnersApp
	}
	return settings.DistributionOwners, settings.DistributionOwnersApp
}

func setPublishOwners(settings *publishDeliverySettings, role string, owners []publishDistributionOwner, app string) {
	switch role {
	case publishOwnerRoleProofread:
		settings.ProofreadOwners, settings.ProofreadOwnersApp = owners, app
	case publishOwnerRoleEditReport:
		settings.EditReportOwners, settings.EditReportOwnersApp = owners, app
	default:
		settings.DistributionOwners, settings.DistributionOwnersApp = owners, app
	}
}

type publishDistributionOwner struct {
	OpenID string `json:"open_id"`
	Name   string `json:"name,omitempty"`
}

func normalizePublishDistributionOwners(owners []publishDistributionOwner) []publishDistributionOwner {
	seen := map[string]bool{}
	out := make([]publishDistributionOwner, 0, len(owners))
	for _, owner := range owners {
		id := strings.TrimSpace(owner.OpenID)
		if !safePublishDeliveryID(id) || seen[id] || len(out) >= maxPublishDistributionOwners {
			continue
		}
		seen[id] = true
		out = append(out, publishDistributionOwner{OpenID: id, Name: truncateRunes(strings.TrimSpace(owner.Name), 64)})
	}
	return out
}

func publishDistributionOwnersView(settings publishDeliverySettings) map[string]any {
	return publishOwnersView(settings, publishOwnerRoleDistribution)
}

func publishOwnersView(settings publishDeliverySettings, role string) map[string]any {
	owners, app := publishOwnersFor(settings, role)
	if owners == nil {
		owners = []publishDistributionOwner{}
	}
	return map[string]any{
		"owners": owners,
		// 换了审核应用后原来的 open_id @ 不到人：后台提示重新选。
		"stale": len(owners) > 0 && app != settings.AppProfileID,
	}
}

// listChatMembers 审核应用读取一个群的成员（open_id + 名字；机器人不在其中）。
func (n *feishuNotifier) listChatMembers(ctx context.Context, chatID string) ([]publishDistributionOwner, error) {
	token, err := n.tenantAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var out []publishDistributionOwner
	pageToken := ""
	for page := 0; page < 20; page++ {
		values := url.Values{"member_id_type": {"open_id"}, "page_size": {"100"}}
		if pageToken != "" {
			values.Set("page_token", pageToken)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			n.baseURL+"/open-apis/im/v1/chats/"+url.PathEscape(chatID)+"/members?"+values.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("feishu: 构造群成员请求: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := n.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("飞书群成员接口不可达: %w", err)
		}
		body, err := readBoundedBody(resp.Body, 2<<20)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("飞书群成员响应读取失败: %w", err)
		}
		var env struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Items []struct {
					MemberID string `json:"member_id"`
					Name     string `json:"name"`
				} `json:"items"`
				HasMore   bool   `json:"has_more"`
				PageToken string `json:"page_token"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("飞书群成员响应异常（HTTP %d）: %w", resp.StatusCode, err)
		}
		if env.Code != 0 {
			return nil, fmt.Errorf("读取群成员失败 code=%d msg=%s", env.Code, env.Msg)
		}
		for _, item := range env.Data.Items {
			out = append(out, publishDistributionOwner{OpenID: item.MemberID, Name: item.Name})
		}
		if !env.Data.HasMore || env.Data.PageToken == "" {
			break
		}
		pageToken = env.Data.PageToken
	}
	return out, nil
}

// publishDistributionOwnerCandidates 当前审核应用、当前审核群的全部成员（去重，按名字排序）。
func (p *publishPipeline) publishDistributionOwnerCandidates(ctx context.Context) ([]publishDistributionOwner, publishDeliverySettings, error) {
	if p.deliverySettings == nil {
		return nil, publishDeliverySettings{}, errors.New("发布通知配置存储未初始化")
	}
	settings, apps, err := p.deliverySettings.snapshotWithApps()
	if err != nil {
		return nil, settings, err
	}
	var members []publishDistributionOwner
	for _, chatID := range settings.ChatIDs {
		var list []publishDistributionOwner
		if p.distributionOwnerLister != nil {
			list, err = p.distributionOwnerLister(ctx, settings.AppProfileID, chatID)
		} else {
			var notifier *feishuNotifier
			notifier, err = p.configuredReviewNotifier(settings, apps, chatID)
			if err == nil {
				list, err = notifier.listChatMembers(ctx, chatID)
			}
		}
		if err != nil {
			return nil, settings, err
		}
		members = append(members, list...)
	}
	members = normalizePublishDistributionOwnersAll(members)
	sort.SliceStable(members, func(i, j int) bool { return members[i].Name < members[j].Name })
	return members, settings, nil
}

// normalizePublishDistributionOwnersAll 候选去重，不截数量。
func normalizePublishDistributionOwnersAll(owners []publishDistributionOwner) []publishDistributionOwner {
	seen := map[string]bool{}
	out := make([]publishDistributionOwner, 0, len(owners))
	for _, owner := range owners {
		id := strings.TrimSpace(owner.OpenID)
		if !safePublishDeliveryID(id) || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, publishDistributionOwner{OpenID: id, Name: truncateRunes(strings.TrimSpace(owner.Name), 64)})
	}
	return out
}

func registerPublishDistributionOwnerRoutes(mux *http.ServeMux, p *publishPipeline, auth *authService) {
	mux.HandleFunc("GET /api/publish/delivery-settings/distribution-owners", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		candidates, settings, err := p.publishDistributionOwnerCandidates(ctx)
		if err != nil && p.deliverySettings == nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		// 读不到群成员（还没配推送应用、飞书暂时不通）不算接口失败：已选的人照样返回，
		// 原因放在 candidates_error，后台在这一步里单独说明，不影响其他设置。
		// 几类负责人从同一份审核群成员里选：一次读成员，校对负责人放在 proofread、周报接收人放在 edit_report 里。
		view := publishDistributionOwnersView(settings)
		view["proofread"] = publishOwnersView(settings, publishOwnerRoleProofread)
		view["edit_report"] = publishOwnersView(settings, publishOwnerRoleEditReport)
		view["candidates"] = candidates
		if err != nil {
			view["candidates"] = []publishDistributionOwner{}
			view["candidates_error"] = err.Error()
		}
		writeJSON(w, http.StatusOK, view)
	})

	mux.HandleFunc("PUT /api/publish/delivery-settings/distribution-owners", putPublishOwners(p, auth, publishOwnerRoleDistribution, "分发负责人"))
	mux.HandleFunc("PUT /api/publish/delivery-settings/proofread-owners", putPublishOwners(p, auth, publishOwnerRoleProofread, "校对负责人"))
	mux.HandleFunc("PUT /api/publish/delivery-settings/edit-report-owners", putPublishOwners(p, auth, publishOwnerRoleEditReport, "手改周报接收人"))
}

// putPublishOwners 保存某类负责人：只收审核群成员的 open_id，服务端按当前成员目录校验。
func putPublishOwners(p *publishPipeline, auth *authService, role, label string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if !requireDeploymentWritesActive(w) {
			return
		}
		var body struct {
			OpenIDs []string `json:"open_ids"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if len(body.OpenIDs) > maxPublishDistributionOwners {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("最多选 %d 位%s", maxPublishDistributionOwners, label))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		candidates, settings, err := p.publishDistributionOwnerCandidates(ctx)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		byID := map[string]publishDistributionOwner{}
		for _, candidate := range candidates {
			byID[candidate.OpenID] = candidate
		}
		owners := make([]publishDistributionOwner, 0, len(body.OpenIDs))
		for _, id := range body.OpenIDs {
			owner, ok := byID[strings.TrimSpace(id)]
			if !ok {
				writeError(w, http.StatusBadRequest, "所选的人不在审核群里，请刷新后重新选择")
				return
			}
			owners = append(owners, owner)
		}
		current, apps, err := p.deliverySettings.resolve()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		if current.AppProfileID != settings.AppProfileID {
			writeError(w, http.StatusConflict, "审核应用刚刚变了，请刷新后重新选择")
			return
		}
		setPublishOwners(&current, role, owners, current.AppProfileID)
		current.UpdatedBy = publishDeliveryActor(r, auth)
		current.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := p.deliverySettings.write(current); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		written, _ := p.deliverySettings.read()
		writeJSON(w, http.StatusOK, publishDeliverySettingsView(written, apps))
	}
}
