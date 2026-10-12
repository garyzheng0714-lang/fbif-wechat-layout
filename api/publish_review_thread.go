package main

// publish_review_thread.go — 审核卡话题里直接发图换外发封面。
//
// 2026-10-12 用户：「我上传的时候……传上去之后直接绑定、自动同步过去了，像这种上传图片链接，
// 就没必要再打开我们文章排版工具的链接了」。飞书卡片没有上传控件，所以让负责人把图发到审核卡的
// 话题里（提醒就 @ 在这个话题里，publish_external_reminders.go）：长连接收到图片消息，确认是审核群、
// 这张审核卡的话题、卡里有平台在等封面，就取图、转存 OSS（和正文图同一个存储，链接长期有效）、
// 换成这些平台的封面，检查通过即推，并在话题里回一句结果。
//
// 前提：飞书开放平台给审核应用订阅「接收消息 v2.0」(im.message.receive_v1) 并开「获取群组中所有
// 消息」「获取与上传图片或文件资源」权限。收到第一条审核群消息后才算开通（ThreadMessagesApp），
// 提醒和卡片里才写「把图发到话题里」——没开通时不写，免得发了没反应。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"strings"
	"time"
)

// reviewThreadMessage 长连接收到的一条群消息里用得到的字段。
type reviewThreadMessage struct {
	MessageID    string
	ChatID       string
	RootID       string
	ParentID     string
	SenderOpenID string
	SenderType   string
	MessageType  string
	Content      string
}

// reviewThreadImagesReady 这个审核应用已能收到审核群消息（话题发图可用）。
func (p *publishPipeline) reviewThreadImagesReady(appProfileID string) bool {
	appProfileID = strings.TrimSpace(appProfileID)
	if p == nil || p.deliverySettings == nil || appProfileID == "" {
		return false
	}
	settings, err := p.deliverySettings.read()
	return err == nil && settings.ThreadMessagesApp == appProfileID
}

// feishuWSAppProfileID 长连接用的应用（FEISHU_APP_ID）在发布链路里的应用编号。
func feishuWSAppProfileID() string {
	appID := strings.TrimSpace(os.Getenv("FEISHU_APP_ID"))
	apps, err := notificationAppsFromEnvironment()
	if err == nil {
		for _, app := range apps {
			if strings.TrimSpace(app.AppID) == appID && appID != "" {
				return app.ID
			}
		}
	}
	return "default"
}

// reviewThreadImageKey 图片消息或带图的富文本消息里的第一张图。
func reviewThreadImageKey(messageType, content string) string {
	switch messageType {
	case "image":
		var body struct {
			ImageKey string `json:"image_key"`
		}
		if json.Unmarshal([]byte(content), &body) == nil {
			return strings.TrimSpace(body.ImageKey)
		}
	case "post":
		var body map[string]json.RawMessage
		if json.Unmarshal([]byte(content), &body) != nil {
			return ""
		}
		// 收到的富文本是 {"title","content":[[…]]}；兼容带语言层的写法。
		raw := body["content"]
		if raw == nil {
			for _, lang := range []string{"zh_cn", "en_us"} {
				var inner map[string]json.RawMessage
				if json.Unmarshal(body[lang], &inner) == nil && inner["content"] != nil {
					raw = inner["content"]
					break
				}
			}
		}
		var lines [][]struct {
			Tag      string `json:"tag"`
			ImageKey string `json:"image_key"`
		}
		if json.Unmarshal(raw, &lines) != nil {
			return ""
		}
		for _, line := range lines {
			for _, el := range line {
				if el.Tag == "img" && strings.TrimSpace(el.ImageKey) != "" {
					return strings.TrimSpace(el.ImageKey)
				}
			}
		}
	}
	return ""
}

// externalPendingCoverSlots 这篇还在等封面的平台位置（没推过、卡在推送前检查）。
func externalPendingCoverSlots(job publishJob, cfg yunsuoConfig) []int {
	var slots []int
	for _, draft := range externalPendingFixes(job, cfg) {
		if _, ok := externalIssueFields(draft.Issues)[publishExternalIssueCover]; !ok {
			continue
		}
		if slot := externalTargetSlot(cfg, draft.AccountID); slot >= 0 {
			slots = append(slots, slot)
		}
	}
	return slots
}

// handleReviewThreadMessage 审核群里的一条消息：只处理审核卡话题里、发给等封面的文章的图。
func (p *publishPipeline) handleReviewThreadMessage(ctx context.Context, appProfileID string, msg reviewThreadMessage) {
	if p == nil || p.deliverySettings == nil {
		return
	}
	settings, err := p.deliverySettings.read()
	if err != nil || strings.TrimSpace(settings.AppProfileID) != strings.TrimSpace(appProfileID) || !containsTrimmed(settings.ChatIDs, msg.ChatID) {
		return
	}
	if err := p.deliverySettings.markThreadMessages(appProfileID); err != nil {
		log.Printf("publish: 记录审核群消息事件已开通失败: %v", err)
	}
	if msg.SenderType != "user" || strings.TrimSpace(msg.RootID) == "" || strings.TrimSpace(msg.SenderOpenID) == "" {
		return
	}
	imageKey := reviewThreadImageKey(msg.MessageType, msg.Content)
	if imageKey == "" {
		return
	}
	cfg, err := loadYunsuoConfig()
	if err != nil {
		return
	}
	job, ambiguous := p.reviewThreadCoverJob(msg, cfg)
	if len(ambiguous) > 1 {
		ids := make([]string, 0, len(ambiguous))
		for _, candidate := range ambiguous {
			ids = append(ids, fmt.Sprintf("#%d", candidate.ID))
		}
		p.replyReviewThread(ctx, msg, fmt.Sprintf("这个话题里有 %d 篇在等封面（%s），请在对应文章的提醒消息下面回复这张图。",
			len(ambiguous), strings.Join(ids, "、")))
		return
	}
	if job.ID == 0 {
		return // 这张卡里没有在等封面的文章：可能只是聊天里的图，不打扰
	}
	// 与卡片按钮同一道权限闸（白名单，或群成员模式下的审核群）。
	if !p.feishuReviewActionAllowed(msg.SenderOpenID, msg.ChatID) {
		p.replyReviewThread(ctx, msg, "你没有发布操作权限，换不了封面；请联系管理员把你加进审核人。")
		return
	}
	reply := p.applyReviewThreadCover(ctx, job, cfg, msg, imageKey)
	if reply != "" {
		p.replyReviewThread(ctx, msg, reply)
	}
}

func containsTrimmed(list []string, value string) bool {
	value = strings.TrimSpace(value)
	for _, item := range list {
		if strings.TrimSpace(item) == value && value != "" {
			return true
		}
	}
	return false
}

// reviewThreadCoverJob 话题根是哪张审核卡、卡里哪篇在等封面。回复到某篇提醒消息下的，就是那篇；
// 聚合卡里不止一篇在等封面又分不清时返回它们，请人回复到对应提醒下面。
func (p *publishPipeline) reviewThreadCoverJob(msg reviewThreadMessage, cfg yunsuoConfig) (publishJob, []publishJob) {
	jobs, err := p.store.all()
	if err != nil {
		return publishJob{}, nil
	}
	var waiting []publishJob
	for _, job := range jobs {
		if reviewCardMessageID(job, msg.ChatID) != strings.TrimSpace(msg.RootID) &&
			job.ExternalDistributionReminder.MessageIDs[msg.ChatID] != strings.TrimSpace(msg.RootID) {
			continue
		}
		if len(externalPendingCoverSlots(job, cfg)) == 0 {
			continue
		}
		if parent := strings.TrimSpace(msg.ParentID); parent != "" && job.ExternalDistributionReminder.MessageIDs[msg.ChatID] == parent {
			return job, nil
		}
		waiting = append(waiting, job)
	}
	if len(waiting) == 1 {
		return waiting[0], nil
	}
	return publishJob{}, waiting
}

// applyReviewThreadCover 取图、量尺寸、转存、换成等封面的平台的封面。返回要回在话题里的话；
// 都改好且这一轮的开场提醒发过时返回空——收尾的「都改好了」由提醒 outbox 说，不说两遍。
func (p *publishPipeline) applyReviewThreadCover(ctx context.Context, job publishJob, cfg yunsuoConfig, msg reviewThreadMessage, imageKey string) string {
	for _, draft := range job.ExternalDistributionDrafts {
		for _, candidate := range draft.Candidates {
			if candidate.MessageID == msg.MessageID {
				return "" // 飞书重投的同一条消息
			}
		}
	}
	data, err := p.fetchReviewThreadImage(ctx, msg, imageKey)
	if err != nil {
		log.Printf("publish: 取话题图片 #%d 失败: %v", job.ID, err)
		return "图片没取到，请再发一次，或者在上面卡片里点选一张正文大图。"
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return "只支持 JPG、PNG 图片，请换一张再发。"
	}
	var fits []int
	var small []string
	for _, slot := range externalPendingCoverSlots(job, cfg) {
		profile := yunsuoProfileForTarget(cfg.Targets[slot])
		if config.Width < profile.CoverMinWidth || config.Height < profile.CoverMinHeight {
			small = append(small, fmt.Sprintf("%s封面至少 %d×%d", profile.Name, profile.CoverMinWidth, profile.CoverMinHeight))
			continue
		}
		fits = append(fits, slot)
	}
	if len(fits) == 0 {
		return fmt.Sprintf("这张图 %d×%d，%s，请换一张大一点的。", config.Width, config.Height, strings.Join(small, "，"))
	}
	actor := strings.TrimSpace(msg.SenderOpenID)
	candidate, err := p.storeExternalCoverBytes(ctx, job.ID, data, format, config.Width, config.Height, actor)
	if err != nil {
		log.Printf("publish: 话题图片 #%d 转存失败: %v", job.ID, err)
		return "图片转存失败，请再发一次。"
	}
	candidate.MessageID = msg.MessageID
	var applied, failed []string
	for _, slot := range fits {
		name := yunsuoProfileForTarget(cfg.Targets[slot]).Name
		url := candidate.URL
		if _, err := p.submitExternalDraftEdit(job.ID, slot, externalDraftEdit{CoverURL: &url, Candidates: []publishExternalCoverCandidate{candidate}}, actor); err != nil {
			failed = append(failed, name+"："+err.Error())
			continue
		}
		applied = append(applied, name)
	}
	if len(applied) == 0 {
		return "封面没换上：" + strings.Join(failed, "；")
	}
	latest, err := p.store.byID(job.ID)
	if err != nil {
		latest = job
	}
	remaining := externalPendingFixes(latest, cfg)
	parts := []string{fmt.Sprintf("%s封面已换成这张。", strings.Join(applied, "、"))}
	if len(small) > 0 {
		parts = append(parts, "这张图对"+strings.Join(small, "、")+"，还要另换一张。")
	}
	if len(failed) > 0 {
		parts = append(parts, "没换上："+strings.Join(failed, "；")+"。")
	}
	if len(remaining) > 0 {
		names := externalReminderPlatformNames(cfg, remaining)
		var left []string
		for i, draft := range remaining {
			left = append(left, names[i]+externalIssueSummaryText(draft.Issues))
		}
		parts = append(parts, fmt.Sprintf("还差：%s，在上面卡片里改好后点「保存并同步至%s」。", strings.Join(left, "；"), strings.Join(names, "」「保存并同步至")))
		return strings.Join(parts, "")
	}
	if latest.ExternalDistributionReminder.MessageIDs[msg.ChatID] != "" && len(small) == 0 && len(failed) == 0 {
		return ""
	}
	parts = append(parts, "正在同步至"+strings.Join(applied, "、")+"。")
	return strings.Join(parts, "")
}

func (p *publishPipeline) fetchReviewThreadImage(ctx context.Context, msg reviewThreadMessage, imageKey string) ([]byte, error) {
	if p.reviewThreadImageFetcher != nil {
		return p.reviewThreadImageFetcher(ctx, msg.ChatID, msg.MessageID, imageKey)
	}
	settings, apps, err := p.deliverySettings.snapshotWithApps()
	if err != nil {
		return nil, err
	}
	notifier, err := p.configuredReviewNotifier(settings, apps, msg.ChatID)
	if err != nil {
		return nil, err
	}
	return notifier.downloadMessageImage(ctx, msg.MessageID, imageKey)
}

// replyReviewThread 在话题里回复这条图片消息（普通富文本，不 @）。
func (p *publishPipeline) replyReviewThread(ctx context.Context, msg reviewThreadMessage, text string) {
	post := postMessage([][]any{{postText(text, false)}})
	if p.reviewThreadReplier != nil {
		if err := p.reviewThreadReplier(ctx, msg.ChatID, msg.MessageID, post); err != nil {
			log.Printf("publish: 话题回复失败: %v", err)
		}
		return
	}
	settings, apps, err := p.deliverySettings.snapshotWithApps()
	if err == nil {
		var notifier *feishuNotifier
		if notifier, err = p.configuredReviewNotifier(settings, apps, msg.ChatID); err == nil {
			_, err = notifier.replyPostWithID(ctx, msg.MessageID, post, true)
		}
	}
	if err != nil {
		log.Printf("publish: 话题回复失败: %v", err)
	}
}

// storeExternalCoverBytes 把一张封面转存 OSS（与正文图同一存储），返回带尺寸的候选。侧边栏上传
// 与话题发图共用。
func (p *publishPipeline) storeExternalCoverBytes(ctx context.Context, jobID int64, data []byte, format string, width, height int, actor string) (publishExternalCoverCandidate, error) {
	mime := map[string]string{"jpeg": "image/jpeg", "png": "image/png"}[format]
	ext := map[string]string{"jpeg": "jpg", "png": "png"}[format]
	if mime == "" {
		return publishExternalCoverCandidate{}, errors.New("只支持 JPG、PNG 图片")
	}
	digest := sha256.Sum256(data)
	name := fmt.Sprintf("distribution-cover-%d-%x.%s", jobID, digest[:8], ext)
	var stored string
	var err error
	if p.externalCoverStore != nil {
		stored, err = p.externalCoverStore(ctx, name, data)
	} else {
		var results map[string]string
		results, err = ossUploadByBase64Images(ctx, map[string]string{name: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)})
		stored = results[name]
	}
	if err != nil {
		return publishExternalCoverCandidate{}, fmt.Errorf("图片转存失败：%w", err)
	}
	stored = publicYunsuoMediaURL(strings.TrimSpace(stored))
	if stored == "" {
		return publishExternalCoverCandidate{}, errors.New("图片转存失败，请重试")
	}
	return publishExternalCoverCandidate{URL: stored, Width: width, Height: height, UploadedBy: actor}, nil
}

// reviewThreadWorkTimeout 话题图片处理的总时限（下载、转存、推送前检查）。
const reviewThreadWorkTimeout = 2 * time.Minute
