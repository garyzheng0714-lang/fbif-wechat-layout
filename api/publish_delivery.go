package main

// publish_delivery.go — 发布审核通知的应用 / 群配置。
//
// 凭证始终来自服务端环境变量；持久化文件只保存“选了哪个应用、哪些群”。
// 群 ID 由当前登录用户的飞书群目录产生并在服务端校验，管理后台不提供
// 手填 ID/名称的入口。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	publishDeliverySettingsEnv = "PUBLISH_DELIVERY_SETTINGS_PATH"
	publishNotificationAppsEnv = "FEISHU_NOTIFICATION_APPS_JSON"
	publishDeliverySchema      = 1
	maxPublishDeliveryChats    = 200
)

type feishuNotificationApp struct {
	ID        string
	Name      string
	AppID     string
	AppSecret string
}

type publishDeliverySettings struct {
	SchemaVersion int      `json:"schema_version"`
	AppProfileID  string   `json:"app_profile_id"`
	ChatIDs       []string `json:"chat_ids"`
	AutoPublish   bool     `json:"auto_publish,omitempty"`
	// 分发负责人：外发待修改时在审核卡下 @ 的人。open_id 按应用区分，只对
	// DistributionOwnersApp 这个审核应用有效（publish_distribution_owners.go）。
	DistributionOwners    []publishDistributionOwner `json:"distribution_owners,omitempty"`
	DistributionOwnersApp string                     `json:"distribution_owners_app,omitempty"`
	// ThreadMessagesApp 已从这个审核应用收到过审核群的消息事件：飞书后台开了「接收消息」与
	// 群消息权限，话题里发图换封面才可用（publish_review_thread.go）。
	ThreadMessagesApp string `json:"thread_messages_app,omitempty"`
	UpdatedBy         string `json:"updated_by,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
}

type publishDeliverySelectionRequest struct {
	AppProfileID string   `json:"app_profile_id"`
	ChatIDs      []string `json:"chat_ids"`
	AutoPublish  bool     `json:"auto_publish"`
}

type publishDeliverySettingsStore struct {
	path string
	mu   sync.Mutex
}

func newPublishDeliverySettingsStore(path string) *publishDeliverySettingsStore {
	return &publishDeliverySettingsStore{path: strings.TrimSpace(path)}
}

func publishDeliverySettingsPath(rulesPath string) string {
	if explicit := strings.TrimSpace(os.Getenv(publishDeliverySettingsEnv)); explicit != "" {
		return explicit
	}
	if strings.TrimSpace(rulesPath) != "" {
		return filepath.Join(filepath.Dir(rulesPath), "publish-delivery.json")
	}
	return filepath.Join("data", "publish-delivery.json")
}

func safePublishDeliveryID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func normalizePublishChatIDs(ids []string) ([]string, error) {
	if len(ids) > maxPublishDeliveryChats {
		return nil, fmt.Errorf("最多选择 %d 个审核群", maxPublishDeliveryChats)
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if !safePublishDeliveryID(id) {
			return nil, fmt.Errorf("审核群选择无效")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func normalizePublishDeliverySettings(settings publishDeliverySettings) (publishDeliverySettings, error) {
	settings.SchemaVersion = publishDeliverySchema
	settings.AppProfileID = strings.TrimSpace(settings.AppProfileID)
	ids, err := normalizePublishChatIDs(settings.ChatIDs)
	if err != nil {
		return publishDeliverySettings{}, err
	}
	settings.ChatIDs = ids
	settings.DistributionOwners = normalizePublishDistributionOwners(settings.DistributionOwners)
	settings.DistributionOwnersApp = strings.TrimSpace(settings.DistributionOwnersApp)
	if len(settings.DistributionOwners) == 0 {
		settings.DistributionOwnersApp = ""
	}
	settings.ThreadMessagesApp = strings.TrimSpace(settings.ThreadMessagesApp)
	settings.UpdatedBy = strings.TrimSpace(settings.UpdatedBy)
	settings.UpdatedAt = strings.TrimSpace(settings.UpdatedAt)
	return settings, nil
}

func notificationAppsFromEnvironment() ([]feishuNotificationApp, error) {
	raw := strings.TrimSpace(os.Getenv(publishNotificationAppsEnv))
	if raw == "" {
		appID := strings.TrimSpace(os.Getenv("FEISHU_APP_ID"))
		secret := strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET"))
		if appID == "" || secret == "" {
			return []feishuNotificationApp{}, nil
		}
		return []feishuNotificationApp{{
			ID: "default", Name: "FBIF文章排版工具", AppID: appID, AppSecret: secret,
		}}, nil
	}
	var input []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		AppID     string `json:"app_id"`
		AppSecret string `json:"app_secret"`
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return nil, errors.New("FEISHU_NOTIFICATION_APPS_JSON 不是有效 JSON")
	}
	if len(input) == 0 || len(input) > 20 {
		return nil, errors.New("FEISHU_NOTIFICATION_APPS_JSON 必须包含 1 到 20 个应用")
	}
	seen := make(map[string]struct{}, len(input))
	apps := make([]feishuNotificationApp, 0, len(input))
	for _, item := range input {
		id := strings.TrimSpace(item.ID)
		name := strings.TrimSpace(item.Name)
		appID := strings.TrimSpace(item.AppID)
		secret := strings.TrimSpace(item.AppSecret)
		if !safePublishDeliveryID(id) || name == "" || len([]rune(name)) > 80 || appID == "" || secret == "" {
			return nil, errors.New("FEISHU_NOTIFICATION_APPS_JSON 包含无效应用")
		}
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("FEISHU_NOTIFICATION_APPS_JSON 存在重复应用 %q", id)
		}
		seen[id] = struct{}{}
		apps = append(apps, feishuNotificationApp{ID: id, Name: name, AppID: appID, AppSecret: secret})
	}
	return apps, nil
}

func (s *publishDeliverySettingsStore) read() (publishDeliverySettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked()
}

func (s *publishDeliverySettingsStore) readLocked() (publishDeliverySettings, error) {
	settings := publishDeliverySettings{SchemaVersion: publishDeliverySchema}
	if s == nil || strings.TrimSpace(s.path) == "" {
		return settings, nil
	}
	body, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		settings.ChatIDs = legacyPublishReviewChatIDs()
		return settings, nil
	}
	if err != nil {
		return settings, fmt.Errorf("读取发布通知配置失败: %w", err)
	}
	if len(body) == 0 {
		return settings, nil
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		return settings, fmt.Errorf("解析发布通知配置失败: %w", err)
	}
	return normalizePublishDeliverySettings(settings)
}

func legacyPublishReviewChatIDs() []string {
	chatID := strings.TrimSpace(os.Getenv("FEISHU_REVIEW_CHAT_ID"))
	if chatID == "" {
		return []string{}
	}
	return []string{chatID}
}

func (s *publishDeliverySettingsStore) write(settings publishDeliverySettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(settings)
}

// markThreadMessages 记下已从这个应用收到审核群消息事件（同一应用只写一次）。
func (s *publishDeliverySettingsStore) markThreadMessages(appProfileID string) error {
	appProfileID = strings.TrimSpace(appProfileID)
	if s == nil || appProfileID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.readLocked()
	if err != nil || settings.ThreadMessagesApp == appProfileID {
		return err
	}
	settings.ThreadMessagesApp = appProfileID
	return s.writeLocked(settings)
}

func (s *publishDeliverySettingsStore) writeLocked(settings publishDeliverySettings) error {
	settings, err := normalizePublishDeliverySettings(settings)
	if err != nil {
		return err
	}
	if strings.TrimSpace(s.path) == "" {
		return errors.New("发布通知配置路径为空")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("创建发布通知配置目录失败: %w", err)
	}
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("编码发布通知配置失败: %w", err)
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".publish-delivery-*.tmp")
	if err != nil {
		return fmt.Errorf("创建发布通知配置临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("替换发布通知配置失败: %w", err)
	}
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *publishDeliverySettingsStore) resolve() (publishDeliverySettings, []feishuNotificationApp, error) {
	settings, err := s.read()
	if err != nil {
		return publishDeliverySettings{}, nil, err
	}
	apps, err := notificationAppsFromEnvironment()
	if err != nil {
		return publishDeliverySettings{}, nil, err
	}
	if settings.AppProfileID == "" && len(apps) > 0 {
		settings.AppProfileID = apps[0].ID
	}
	if settings.AppProfileID != "" {
		found := false
		for _, app := range apps {
			if app.ID == settings.AppProfileID {
				found = true
				break
			}
		}
		if !found {
			return publishDeliverySettings{}, nil, fmt.Errorf("发布通知应用 %q 未配置凭证", settings.AppProfileID)
		}
	}
	return settings, apps, nil
}

func notificationAppByID(apps []feishuNotificationApp, id string) (feishuNotificationApp, bool) {
	for _, app := range apps {
		if app.ID == strings.TrimSpace(id) {
			return app, true
		}
	}
	return feishuNotificationApp{}, false
}

func (s *publishDeliverySettingsStore) snapshot() (publishDeliverySettings, error) {
	settings, _, err := s.resolve()
	if err != nil {
		return publishDeliverySettings{}, err
	}
	if settings.AppProfileID == "" || len(settings.ChatIDs) == 0 {
		return publishDeliverySettings{}, errors.New("发布审核通知尚未选择应用和审核群")
	}
	return settings, nil
}

func publishDeliverySettingsView(settings publishDeliverySettings, apps []feishuNotificationApp) map[string]any {
	views := make([]map[string]any, 0, len(apps))
	for _, app := range apps {
		views = append(views, map[string]any{"id": app.ID, "name": app.Name})
	}
	return map[string]any{
		"settings": map[string]any{
			"app_profile_id": settings.AppProfileID,
			"chat_ids":       settings.ChatIDs,
			"auto_publish":   settings.AutoPublish,
			"updated_by":     settings.UpdatedBy,
			"updated_at":     settings.UpdatedAt,
		},
		"distribution_owners": publishDistributionOwnersView(settings),
		"apps":                views,
	}
}

func publishDeliveryChatSet(chats []feishuChat) map[string]struct{} {
	set := make(map[string]struct{}, len(chats))
	for _, chat := range chats {
		if id := strings.TrimSpace(chat.ChatID); id != "" {
			set[id] = struct{}{}
		}
	}
	return set
}

func validatePublishDeliveryChatSelection(ids []string, chats []feishuChat) error {
	normalized, err := normalizePublishChatIDs(ids)
	if err != nil {
		return err
	}
	if len(normalized) == 0 {
		return errors.New("至少选择一个审核群")
	}
	available := publishDeliveryChatSet(chats)
	for _, id := range normalized {
		if _, ok := available[id]; !ok {
			return errors.New("所选审核群不在当前登录账号可见列表中，请刷新后重新选择")
		}
	}
	return nil
}

// enqueueReviewNotification 在待审核事件第一次落库时固化投递目标。后续即使
// 管理后台切换应用或群，已生成的审核卡仍会由原应用更新原群中的原消息。
func (p *publishPipeline) enqueueReviewNotification(job *publishJob) {
	if p != nil && p.deliverySettings != nil {
		if settings, err := p.deliverySettings.snapshot(); err == nil {
			job.ReviewDeliveryAppID = settings.AppProfileID
			job.ReviewDeliveryChatIDs = append([]string(nil), settings.ChatIDs...)
		} else {
			// 不把配置缺失伪装成成功；通知 outbox 仍然保留，管理员补齐
			// 配置后可通过通知重放恢复。
			job.NotificationError = err.Error()
		}
	}
	enqueuePublishNotification(job, publishNotificationReviewPending)
}

func (p *publishPipeline) reviewDeliveryForJob(job publishJob) (publishDeliverySettings, []feishuNotificationApp, error) {
	if p != nil && p.deliverySettings != nil {
		if strings.TrimSpace(job.ReviewDeliveryAppID) != "" && len(job.ReviewDeliveryChatIDs) > 0 {
			apps, err := notificationAppsFromEnvironment()
			if err != nil {
				return publishDeliverySettings{}, nil, err
			}
			if _, ok := notificationAppByID(apps, job.ReviewDeliveryAppID); !ok {
				return publishDeliverySettings{}, nil, fmt.Errorf("任务使用的审核应用 %q 当前未配置凭证", job.ReviewDeliveryAppID)
			}
			return publishDeliverySettings{
				SchemaVersion: publishDeliverySchema,
				AppProfileID:  strings.TrimSpace(job.ReviewDeliveryAppID),
				ChatIDs:       append([]string(nil), job.ReviewDeliveryChatIDs...),
			}, apps, nil
		}
		return p.deliverySettings.snapshotWithApps()
	}
	return publishDeliverySettings{}, nil, errors.New("发布审核投递配置未启用")
}

func (s *publishDeliverySettingsStore) snapshotWithApps() (publishDeliverySettings, []feishuNotificationApp, error) {
	settings, apps, err := s.resolve()
	if err != nil {
		return publishDeliverySettings{}, nil, err
	}
	if settings.AppProfileID == "" || len(settings.ChatIDs) == 0 {
		return publishDeliverySettings{}, nil, errors.New("发布审核通知尚未选择应用和审核群")
	}
	return settings, apps, nil
}

func reviewCardMessageID(job publishJob, chatID string) string {
	chatID = strings.TrimSpace(chatID)
	if chatID != "" && job.ReviewCardMsgIDs != nil {
		if id := strings.TrimSpace(job.ReviewCardMsgIDs[chatID]); id != "" {
			return id
		}
		// map 时代的任务：目标群在 map 里没有记录，就是没往这个群发过卡，必须发新卡。
		// 不得回落到旧单值字段——它不带群信息，切换审核群后回落等于把「更新旧群里的
		// 旧卡」误当成「已发到新群」，新群永远收不到卡（2026-08-14 切群实测：#78/#80
		// 重排后的审核卡被原位更新进旧群，新群零消息，发送记录却显示成功）。
		return ""
	}
	// 旧任务（map 从未建立）只有一个 message_id；目标群唯一时按历史行为沿用。
	if chatID == "" || len(job.ReviewDeliveryChatIDs) <= 1 {
		return strings.TrimSpace(job.ReviewCardMsgID)
	}
	return ""
}

func (p *publishPipeline) rememberReviewCardMessageIDForChat(job publishJob, chatID, messageID string) error {
	messageID = strings.TrimSpace(messageID)
	chatID = strings.TrimSpace(chatID)
	if messageID == "" {
		return errors.New("飞书审核卡发送成功但没有 message_id")
	}
	if chatID == "" {
		return p.rememberReviewCardMessageID(job, messageID)
	}
	expected := reviewCardMessageID(job, chatID)
	_, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
		currentID := reviewCardMessageID(current, chatID)
		return currentID == "" || currentID == expected || currentID == messageID
	}, func(current *publishJob) {
		if current.ReviewCardMsgIDs == nil {
			current.ReviewCardMsgIDs = map[string]string{}
		}
		current.ReviewCardMsgIDs[chatID] = messageID
		if strings.TrimSpace(current.ReviewCardMsgID) == "" {
			current.ReviewCardMsgID = messageID
		}
	})
	if err != nil {
		return fmt.Errorf("保存审核卡 message_id 失败: %w", err)
	}
	if swapped {
		return nil
	}
	current, loadErr := p.store.byID(job.ID)
	if loadErr != nil {
		return loadErr
	}
	if reviewCardMessageID(current, chatID) != "" {
		return nil
	}
	return errors.New("保存审核卡 message_id 未生效")
}

func (p *publishPipeline) configuredReviewNotifier(settings publishDeliverySettings, apps []feishuNotificationApp, chatID string) (*feishuNotifier, error) {
	app, ok := notificationAppByID(apps, settings.AppProfileID)
	if !ok {
		return nil, fmt.Errorf("审核应用 %q 未配置凭证", settings.AppProfileID)
	}
	notifier := newFeishuNotifierForProfile(app, chatID)
	if notifier == nil {
		return nil, errors.New("审核应用凭证或群配置不完整")
	}
	return notifier, nil
}

func (p *publishPipeline) updateConfiguredReviewCard(ctx context.Context, job publishJob, chatID, messageID string, card map[string]any) error {
	settings, apps, err := p.reviewDeliveryForJob(job)
	if err != nil {
		return err
	}
	notifier, err := p.configuredReviewNotifier(settings, apps, chatID)
	if err != nil {
		return err
	}
	return notifier.updateCard(ctx, messageID, card)
}

func (p *publishPipeline) sendConfiguredReviewCard(ctx context.Context, job publishJob, chatID string, card map[string]any) (string, error) {
	settings, apps, err := p.reviewDeliveryForJob(job)
	if err != nil {
		return "", err
	}
	notifier, err := p.configuredReviewNotifier(settings, apps, chatID)
	if err != nil {
		return "", err
	}
	return notifier.sendCardWithID(ctx, card)
}

func (p *publishPipeline) uploadReviewCoverForJob(ctx context.Context, job publishJob) (string, error) {
	if p != nil && p.deliverySettings != nil {
		settings, apps, err := p.reviewDeliveryForJob(job)
		if err != nil {
			return "", err
		}
		if len(settings.ChatIDs) == 0 {
			return "", errors.New("没有配置审核群")
		}
		notifier, err := p.configuredReviewNotifier(settings, apps, settings.ChatIDs[0])
		if err != nil {
			return "", err
		}
		return notifier.uploadImageByURL(ctx, job.CoverURL)
	}
	if p.coverImgKey == nil {
		return "", errors.New("封面上传器未配置")
	}
	return p.coverImgKey(ctx, job.CoverURL)
}

func samePublishDeliveryTargets(body publishDeliverySelectionRequest, current publishDeliverySettings) bool {
	if strings.TrimSpace(body.AppProfileID) != strings.TrimSpace(current.AppProfileID) {
		return false
	}
	left, err := normalizePublishChatIDs(body.ChatIDs)
	if err != nil || len(left) == 0 {
		return false
	}
	right, err := normalizePublishChatIDs(current.ChatIDs)
	if err != nil || len(right) != len(left) {
		return false
	}
	seen := make(map[string]struct{}, len(right))
	for _, id := range right {
		seen[id] = struct{}{}
	}
	for _, id := range left {
		if _, ok := seen[id]; !ok {
			return false
		}
	}
	return true
}

func publishDeliverySelectionFromRequest(body publishDeliverySelectionRequest, current publishDeliverySettings, apps []feishuNotificationApp, chats []feishuChat, actor string) (publishDeliverySettings, error) {
	appID := strings.TrimSpace(body.AppProfileID)
	if appID == "" {
		return publishDeliverySettings{}, errors.New("请选择审核应用")
	}
	if _, ok := notificationAppByID(apps, appID); !ok {
		return publishDeliverySettings{}, errors.New("所选审核应用未配置或已下线")
	}
	chatIDs, err := normalizePublishChatIDs(body.ChatIDs)
	if err != nil {
		return publishDeliverySettings{}, err
	}
	if err := validatePublishDeliveryChatSelection(chatIDs, chats); err != nil {
		return publishDeliverySettings{}, err
	}
	settings := publishDeliverySettings{
		SchemaVersion: publishDeliverySchema,
		AppProfileID:  appID,
		ChatIDs:       chatIDs,
		AutoPublish:   body.AutoPublish,
		UpdatedBy:     strings.TrimSpace(actor),
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	// 分发负责人的 open_id 只在原应用下有效：换了审核应用就要重新选。
	settings.ThreadMessagesApp = current.ThreadMessagesApp
	if current.DistributionOwnersApp == appID {
		settings.DistributionOwners = current.DistributionOwners
		settings.DistributionOwnersApp = current.DistributionOwnersApp
	}
	return settings, nil
}
