package main

// flash_card.go — 快讯审核卡（飞书卡片 JSON 2.0，和文章审核卡同一套规矩：不用 emoji、通过按钮要二次确认、
// update_multi 让群里所有人看到同一张）。只发 FLASH_REVIEW_CHAT_ID 这个群。
//
// 卡上照规范的交付格式摆：标题、正文、来源、编辑核实提示；再往下是机器查出的三类问题（对原文、不推荐说法、风险）。
// 待审核时三个按钮：通过并发布、改字、驳回。点改字整张卡换成编辑框（同一张卡原位换），改好提交后版本加一、重新查。
// 按钮里带 flash_id 和 version，卡片不是最新一版就不认。

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	flashCardActionApprove = "flash_approve"
	flashCardActionReject  = "flash_reject"
	flashCardActionEdit    = "flash_edit"   // 换成编辑框
	flashCardActionSave    = "flash_save"   // 编辑框提交
	flashCardActionCancel  = "flash_cancel" // 不改了
)

type flashCardActionValue struct {
	Action  string `json:"action"`
	FlashID any    `json:"flash_id"`
	Version int    `json:"version"`
}

func buildFlashCard(job flashJob, now time.Time) map[string]any {
	return composeFlashCard(job, false)
}

func buildFlashEditCard(job flashJob) map[string]any {
	return composeFlashCard(job, true)
}

func composeFlashCard(job flashJob, editing bool) map[string]any {
	var elements []any
	md := func(content string) {
		elements = append(elements, map[string]any{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": content}})
	}
	plain := func(content string) {
		elements = append(elements, map[string]any{"tag": "div", "text": map[string]any{"tag": "plain_text", "content": content}})
	}
	grey := func(content string) { md("<font color='grey'>" + content + "</font>") }

	draft := job.Draft
	switch {
	case draft == nil:
		title := "#" + strconv.FormatInt(job.ID, 10)
		if job.Source != nil {
			title += " " + reviewCardSafeText(job.Source.Article.Title, 80)
		}
		md("**" + title + "**")
	case editing:
		elements = append(elements, flashEditForm(job))
	default:
		md("**" + reviewCardSafeText(draft.Title, 100) + "**")
		for _, p := range draft.Body {
			plain(p)
		}
		src := draft.Source
		line := "来源：" + reviewCardSafeOrDash(src.Name, 40)
		if src.Title != "" {
			label := "《" + reviewCardSafeText(src.Title, 80) + "》"
			if link := reviewCardSafeURL(src.URL); link != "" {
				label = "[" + label + "](" + link + ")"
			}
			line += " " + label
		}
		if src.PublishedAt != "" {
			line += " " + reviewCardSafeText(src.PublishedAt, 20)
		}
		grey(line)
	}

	if draft != nil && !editing {
		if len(draft.Hints) > 0 {
			md("**编辑核实提示**\n" + flashCardList(draft.Hints))
		}
		if c := job.Checks; c != nil && c.Version == job.Version {
			if len(c.Unsupported) > 0 {
				md("**对原文没对上**\n" + flashCardList(c.Unsupported))
			}
			if len(c.Avoid) > 0 {
				md("**规范里不推荐的说法**\n" + flashCardList(c.Avoid))
			}
			md(flashCardRisk(c.Risk))
		}
	}
	if job.SpecNote != "" {
		grey(reviewCardSafeText(job.SpecNote, 160))
	}
	if status := flashCardStatus(job); status != "" {
		md(status)
	}
	if !editing {
		if row := flashCardButtons(job); row != nil {
			elements = append(elements, row)
		}
	}
	subtitle := flashStageLabel(job.Stage) + " #" + strconv.FormatInt(job.ID, 10)
	if job.Version > 1 {
		subtitle += " 第 " + strconv.Itoa(job.Version) + " 版"
	}
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"update_multi": true},
		"header": map[string]any{
			"template": "wathet",
			"title":    feishuPlainText("FoodTalks 快讯"),
			"subtitle": feishuPlainText(subtitle),
		},
		"body": map[string]any{"elements": elements},
	}
}

func flashCardList(items []string) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, "- "+reviewCardSafeText(item, 200))
	}
	return strings.Join(lines, "\n")
}

// flashCardRisk 风险一行结论，加上证实了的几条（口径只用 splitPublishRiskItems，红线 35）。
func flashCardRisk(r publishRiskAssessment) string {
	head := "**风险检查** " + publishRiskLevelLabel(r.Level)
	if r.Headline != "" {
		head += "：" + reviewCardSafeText(r.Headline, 100)
	}
	confirmed, deferred := splitPublishRiskItems(r.Items)
	var lines []string
	for i, item := range confirmed {
		if i == 3 {
			break
		}
		lines = append(lines, "- "+publishRiskCategoryLabel(item.Category)+"："+reviewCardSafeText(firstNonEmpty(item.Reason, item.Evidence), 160))
	}
	if len(deferred) > 0 {
		lines = append(lines, fmt.Sprintf("- 另有 %d 条待核，见风险检查记录", len(deferred)))
	}
	if len(lines) == 0 {
		return head
	}
	return head + "\n" + strings.Join(lines, "\n")
}

func flashCardStatus(job flashJob) string {
	who := ""
	if feishuOpenIDSafe(job.ReviewedBy) {
		who = "<at id=" + job.ReviewedBy + "></at> "
	}
	switch job.Stage {
	case flashStageWriting, flashStageQueued:
		return "<font color='grey'>写稿中</font>"
	case flashStageChecking:
		return "<font color='grey'>改过了，正在重新检查</font>"
	case flashStagePublishing:
		return "<font color='grey'>" + who + "通过了，正在发到 FoodTalks</font>"
	case flashStagePublished:
		line := who + "通过，已发布"
		if link := reviewCardSafeURL(job.FlashURL); link != "" {
			line += " [在 FoodTalks 查看](" + link + ")"
		}
		return line
	case flashStageRejected:
		return "<font color='grey'>" + who + "驳回了，不发</font>"
	case flashStageSkipped:
		return "<font color='grey'>不写：" + reviewCardSafeText(job.SkipReason, 120) + "</font>"
	case flashStageFailed:
		return "<font color='red'>没发成：" + reviewCardSafeText(job.Error, 160) + "</font>"
	}
	return ""
}

func flashCardButton(label, kind, action string, job flashJob) map[string]any {
	return map[string]any{
		"tag": "button", "text": feishuPlainText(label), "type": kind, "size": "small", "width": "default",
		"behaviors": []any{map[string]any{"type": "callback", "value": map[string]any{
			"action": action, "flash_id": job.ID, "version": job.Version,
		}}},
	}
}

func flashCardButtons(job flashJob) map[string]any {
	if job.Draft == nil {
		return nil
	}
	switch job.Stage {
	case flashStageAwaitingReview:
		var buttons []map[string]any
		if flashApproveBlocked(job, job.Version) == "" {
			approve := flashCardButton("通过并发布", "primary_filled", flashCardActionApprove, job)
			approve["confirm"] = map[string]any{
				"title": feishuPlainText("发布这条快讯？"),
				"text":  feishuPlainText("会立刻公开在 FoodTalks 网站上。"),
			}
			buttons = append(buttons, approve)
		}
		buttons = append(buttons, flashCardButton("改字", "default", flashCardActionEdit, job),
			flashCardButton("驳回", "danger", flashCardActionReject, job))
		return reviewBatchInlineButtons(buttons...)
	case flashStageFailed:
		return reviewBatchInlineButtons(flashCardButton("驳回", "danger", flashCardActionReject, job))
	}
	return nil
}

// flashEditForm 编辑框：标题一行、正文一段一行，提交后版本加一、重新查。
func flashEditForm(job flashJob) map[string]any {
	title := map[string]any{
		"tag": "input", "name": "flash_title", "width": "fill", "required": true,
		"label": feishuPlainText("标题"), "label_position": "top",
		"default_value": job.Draft.Title, "max_length": 100,
	}
	body := map[string]any{
		"tag": "input", "name": "flash_body", "width": "fill", "required": true, "input_type": "multiline_text",
		"label": feishuPlainText("正文，一行一段"), "label_position": "top",
		"default_value": strings.Join(job.Draft.Body, "\n"), "max_length": 1000,
		"rows": 8, "auto_resize": true, "max_rows": 16,
	}
	save := flashCardButton("改好了", "primary_filled", flashCardActionSave, job)
	save["name"] = fmt.Sprintf("flash_save_%d", job.ID)
	save["form_action_type"] = "submit"
	cancel := flashCardButton("不改了", "default", flashCardActionCancel, job)
	cancel["name"] = fmt.Sprintf("flash_cancel_%d", job.ID)
	return map[string]any{
		"tag": "form", "name": fmt.Sprintf("flash_form_%d", job.ID),
		"elements": []any{title, body, reviewBatchInlineButtons(save, cancel)},
	}
}

// flashCardSender 用审核卡那个飞书应用往快讯审核群发卡。
type flashCardSender struct {
	chat     string
	notifier func() (*feishuNotifier, error)
}

func (s flashCardSender) chatID() string { return s.chat }

func (s flashCardSender) send(ctx context.Context, card map[string]any) (string, error) {
	n, err := s.notifier()
	if err != nil {
		return "", err
	}
	return n.sendCardWithID(ctx, card)
}

func (s flashCardSender) update(ctx context.Context, messageID string, card map[string]any) error {
	n, err := s.notifier()
	if err != nil {
		return err
	}
	return n.updateCard(ctx, messageID, card)
}

// cardAction 快讯卡上的按钮；不是快讯的按钮回 handled=false，交给文章审核卡。
func (p *flashPipeline) cardAction(operatorOpenID, chatID string, rawValue json.RawMessage, formValue map[string]any) (reviewCardOutcome, bool) {
	var value flashCardActionValue
	if len(rawValue) == 0 || json.Unmarshal(rawValue, &value) != nil || !strings.HasPrefix(value.Action, "flash_") {
		return reviewCardOutcome{}, false
	}
	id := coerceFeishuJobID(value.FlashID)
	if id <= 0 {
		return reviewCardOutcome{toastType: "error", toastContent: "按钮数据不完整，找不到是哪条快讯"}, true
	}
	if !p.actionAllowed(operatorOpenID, chatID) {
		return reviewCardOutcome{toastType: "error", toastContent: "你没有快讯审核权限：请在快讯审核群里操作，或联系管理员把你加进审核人名单"}, true
	}
	var (
		job flashJob
		err error
		ok  string
	)
	switch value.Action {
	case flashCardActionApprove:
		job, err = p.approve(id, value.Version, operatorOpenID)
		ok = "通过了，正在发到 FoodTalks"
	case flashCardActionReject:
		job, err = p.reject(id, value.Version, operatorOpenID)
		ok = "驳回了"
	case flashCardActionEdit:
		job, err = p.store.byID(id)
		if err == nil && (job.Stage != flashStageAwaitingReview || job.Version != value.Version || job.Draft == nil) {
			return reviewCardOutcome{toastType: "info", toastContent: "卡片不是最新的", card: buildFlashCard(job, p.now())}, true
		}
		if err == nil {
			return reviewCardOutcome{toastType: "info", toastContent: "改好点「改好了」", card: buildFlashEditCard(job)}, true
		}
	case flashCardActionCancel:
		job, err = p.store.byID(id)
		ok = "没改"
	case flashCardActionSave:
		title, _ := formValue["flash_title"].(string)
		body, _ := formValue["flash_body"].(string)
		job, err = p.edit(id, value.Version, title, body, operatorOpenID)
		ok = "改好了，正在重新检查"
	default:
		return reviewCardOutcome{toastType: "error", toastContent: "不认识的按钮"}, true
	}
	if err != nil {
		out := reviewCardOutcome{toastType: "error", toastContent: err.Error()}
		if job.ID != 0 {
			out.card = buildFlashCard(job, p.now())
		}
		return out, true
	}
	return reviewCardOutcome{toastType: "success", toastContent: ok, card: buildFlashCard(job, p.now())}, true
}

// actionAllowed 审核人名单里的人，或开了群成员模式时快讯审核群里的人。
func (p *flashPipeline) actionAllowed(operatorOpenID, chatID string) bool {
	if feishuReviewerAllowed(operatorOpenID) {
		return true
	}
	if !feishuReviewerChatModeEnabled() || !feishuOpenIDSafe(strings.TrimSpace(operatorOpenID)) {
		return false
	}
	return p.cards != nil && chatID != "" && chatID == p.cards.chatID()
}
