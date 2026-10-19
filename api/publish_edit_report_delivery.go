package main

// publish_edit_report_delivery.go — 每周手改清单的生成、投递与接口（比对与归类见 publish_edit_report.go）。
//
// 每周一 10:00（北京时间）起，后台循环生成上一周（周一到周日，按发布时间）的清单，用审核应用私信发给
// 管理后台「发布链路 → 手改周报接收人」，不进审核群。每周只发一次，结果记在 publish-edit-report.json：
// 发之前先落「第几次」，每人发之前先落「什么时候开始发的」，发出后落回执，并带飞书去重键 uuid。
// 飞书 uuid 只在一小时内去重：某人发出去却没拿到结果（连接中断、进程退出），50 分钟内带同一个 uuid 重发，
// 过了就不再自动发，直接记死信等人确认（宁可漏一条周报，不让人收到两条）。
// 失败每 30 分钟重试，最多 8 次（约 4 小时，当天就收尾）后记死信；换周时上一周还没发成的也收尾成死信。
// 死信在监控里给 7 天 warning（周报不是发布链路，不算 P0）。
// 没设接收人只记不发、也不读 FoodTalks；之后设了人，当周照样补发。
// 只读 FoodTalks（每篇一次 GET，一周几十次），不改任何稿件。有手改的篇再用现在的规则重排一次（本地渲染服务，
// 不调付费接口）：现在已能自动排出编辑改的样子的那类手改不再列，只在私信末尾提一句。

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	publishEditReportFile        = "publish-edit-report.json"
	publishEditReportSendHour    = 10
	publishEditReportMaxAttempts = 8
	publishEditReportSweep       = 30 * time.Minute
	publishEditReportKeepRecords = 104 // 两年；一条只有几百字节
	// 飞书 uuid 去重期是一小时，留 10 分钟余量：没拿到结果的那次之后 50 分钟内才能带同一个 uuid 重发。
	publishEditReportDedupWindow = 50 * time.Minute
	publishEditReportWarnFor     = 7 * 24 * time.Hour
)

const (
	publishEditReportStateSending    = "sending" // 已落「这次要发」；进程中断后下一轮当失败重试
	publishEditReportStateSent       = "sent"
	publishEditReportStateSkipped    = "skipped"
	publishEditReportStateFailed     = "failed" // 下一轮重试
	publishEditReportStateDeadLetter = "dead_letter"
)

var (
	errPublishEditReportAlreadySent = errors.New("这一周的手改周报已经发过")
	errPublishEditReportWeekOpen    = errors.New("这一周还没过完，只能预览（dry_run=1）")
	errPublishEditReportWeekTooOld  = errors.New("只留最近两年的周报记录，更早的周只能预览（dry_run=1）")
	errPublishEditReportGaveUp      = errors.New("这一周的手改周报已试满 8 次，不再自动重发")
	errPublishEditReportUnconfirmed = errors.New("有人发出去后没拿到结果，已过飞书一小时去重期，为免重复不再自动重发")
)

// publishEditReportRecord 一周清单的投递记录。
type publishEditReportRecord struct {
	Week       string            `json:"week"`                 // 被统计那周的周一（北京时间）
	Generation int               `json:"generation,omitempty"` // force 重发一次加一，进飞书去重键
	State      string            `json:"state"`
	Attempts   int               `json:"attempts,omitempty"`
	Error      string            `json:"error,omitempty"`
	Skipped    string            `json:"skipped,omitempty"`
	Compared   int               `json:"compared,omitempty"`
	Edited     int               `json:"edited,omitempty"`
	Listed     int               `json:"listed,omitempty"`
	MessageIDs map[string]string `json:"message_ids,omitempty"` // 接收人 open_id → message_id
	// Unconfirmed 接收人 open_id → 最早一次「可能已到飞书、却没拿到结果」的发送时间（发之前先落，拿到回执或飞书明确拒收才清）。
	Unconfirmed map[string]string `json:"unconfirmed,omitempty"`
	SentAt      string            `json:"sent_at,omitempty"`
	UpdatedAt   string            `json:"updated_at,omitempty"`
	Actor       string            `json:"actor,omitempty"` // 手动触发的人；定时发送为空
}

type publishEditReportDocument struct {
	Records []publishEditReportRecord `json:"records"`
}

type publishEditReportStore struct {
	path string
	mu   sync.Mutex
}

func newPublishEditReportStore(path string) *publishEditReportStore {
	return &publishEditReportStore{path: strings.TrimSpace(path)}
}

// publishEditReportPath 与发布通知配置放在同一目录。
func publishEditReportPath(deliveryPath string) string {
	return filepath.Join(filepath.Dir(deliveryPath), publishEditReportFile)
}

func (s *publishEditReportStore) readLocked() (publishEditReportDocument, error) {
	var doc publishEditReportDocument
	body, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(body) == 0) {
		return doc, nil
	}
	if err != nil {
		return doc, fmt.Errorf("读取手改周报记录失败: %w", err)
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return doc, fmt.Errorf("解析手改周报记录失败: %w", err)
	}
	return doc, nil
}

func (s *publishEditReportStore) get(week string) (publishEditReportRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return publishEditReportRecord{}, false, err
	}
	for _, rec := range doc.Records {
		if rec.Week == week {
			return rec, true, nil
		}
	}
	return publishEditReportRecord{Week: week}, false, nil
}

// list 全部记录，按周倒序（监控与换周收尾用）。
func (s *publishEditReportStore) list() ([]publishEditReportRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	return doc.Records, err
}

// put 写入一周的记录：按周倒序，只留最近 104 周；先写临时文件再改名。
func (s *publishEditReportStore) put(rec publishEditReportRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return errors.New("手改周报记录路径为空")
	}
	doc, err := s.readLocked()
	if err != nil {
		return err
	}
	records := make([]publishEditReportRecord, 0, len(doc.Records)+1)
	for _, existing := range doc.Records {
		if existing.Week != rec.Week {
			records = append(records, existing)
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].Week > records[j].Week })
	// 只裁别的周：这次写的这条要是被裁掉，正在发的回执就丢了，下次会重发。
	if len(records) > publishEditReportKeepRecords-1 {
		records = records[:publishEditReportKeepRecords-1]
	}
	records = append(records, rec)
	sort.SliceStable(records, func(i, j int) bool { return records[i].Week > records[j].Week })
	body, err := json.MarshalIndent(publishEditReportDocument{Records: records}, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("创建手改周报目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".publish-edit-report-*.tmp")
	if err != nil {
		return fmt.Errorf("创建手改周报临时文件失败: %w", err)
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
	return os.Rename(tmpName, s.path)
}

// publishEditReportLastWeek now 之前的最后一整周：周一 0 点到下周一 0 点（北京时间）。
func publishEditReportLastWeek(now time.Time) (time.Time, time.Time) {
	local := now.In(publishSiteTimeZone)
	offset := (int(local.Weekday()) + 6) % 7 // 周一 = 0
	monday := time.Date(local.Year(), local.Month(), local.Day()-offset, 0, 0, 0, 0, publishSiteTimeZone)
	return monday.AddDate(0, 0, -7), monday
}

// publishEditReportWeekFromParam 接口里的 week=YYYY-MM-DD 必须是周一。
func publishEditReportWeekFromParam(value string) (time.Time, time.Time, error) {
	start, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(value), publishSiteTimeZone)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("week 要写成 YYYY-MM-DD")
	}
	if start.Weekday() != time.Monday {
		return time.Time{}, time.Time{}, errors.New("week 要填那一周的周一")
	}
	return start, start.AddDate(0, 0, 7), nil
}

// publishEditReportEligible 统计范围：这周发到 FoodTalks（当前环境）的流水线稿，有我们发出的正文。
func publishEditReportEligible(job publishJob, start, end time.Time) bool {
	if job.Stage != publishStagePublished || job.FoodTalksNewsID <= 0 || strings.TrimSpace(job.ContentHTML) == "" {
		return false
	}
	if publishStagingTargetMismatch(job) != nil {
		return false
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(job.PublishedAt))
	if err != nil {
		return false
	}
	return !at.Before(start) && at.Before(end)
}

// buildPublishEditReport 逐篇读 FoodTalks 上的正文与我们发出的比。凭证失效整份作废（等下一轮）；
// 个别读不到的记数跳过；已在 FoodTalks 下线的不算。
func (p *publishPipeline) buildPublishEditReport(ctx context.Context, start, end time.Time) (publishEditReport, error) {
	report := publishEditReport{
		WeekStart: start.In(publishSiteTimeZone).Format("2006-01-02"),
		WeekEnd:   end.In(publishSiteTimeZone).AddDate(0, 0, -1).Format("2006-01-02"),
	}
	jobs, err := p.store.all()
	if err != nil {
		return report, err
	}
	var eligible []publishJob
	for _, job := range jobs {
		if publishEditReportEligible(job, start, end) {
			eligible = append(eligible, job)
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool { return eligible[i].ID < eligible[j].ID })
	if len(eligible) == 0 {
		summarizePublishEditReport(&report)
		return report, nil
	}
	if p.foodTalks == nil {
		return report, errors.New("FoodTalks 客户端未配置")
	}
	ft, err := p.foodTalks()
	if err != nil {
		return report, err
	}
	for _, job := range eligible {
		live, err := ft.GetNews(ctx, job.FoodTalksNewsID)
		if err != nil {
			if errors.Is(err, errFoodTalksUnauthorized) || ctx.Err() != nil {
				return report, err
			}
			log.Printf("publish: 手改周报读取 FoodTalks 资讯 %d（任务 #%d）失败：%v", job.FoodTalksNewsID, job.ID, err)
			report.Unreadable++
			continue
		}
		if live.PublishStatus != foodTalksPublishStatusPublished {
			continue
		}
		report.Compared++
		article := publishEditReportArticle{
			JobID:   job.ID,
			NewsID:  job.FoodTalksNewsID,
			Title:   firstNonEmpty(strings.TrimSpace(live.Title), job.Title),
			URL:     fmt.Sprintf("%s/news/%d", siteFrontURL(), job.FoodTalksNewsID),
			Changes: compareEditReport(job.ContentHTML, live.Content),
		}
		if len(article.Changes) > 0 {
			if fresh, err := p.rerenderPublishEditReport(ctx, job); err != nil {
				if ctx.Err() != nil {
					return report, ctx.Err()
				}
				article.Recheck = editReportClip(err.Error(), 200)
			} else {
				article.Changes, article.Fixed = splitEditReportFixed(article.Changes, compareEditReport(fresh, live.Content))
			}
		}
		report.Articles = append(report.Articles, article)
	}
	if report.Compared == 0 && report.Unreadable > 0 {
		return report, fmt.Errorf("%d 篇都读不到 FoodTalks 上的正文", report.Unreadable)
	}
	summarizePublishEditReport(&report)
	return report, nil
}

// rerenderPublishEditReport 用现在的规则把这篇的原文块重排一次，图片换成发布时已转存的地址，好和 FoodTalks 现稿比。
// 只算不写：不落库、不传图，也不补查原文链接（那一步可能调付费的 TikHub），用任务里已记的出处与原文标题。
func (p *publishPipeline) rerenderPublishEditReport(ctx context.Context, job publishJob) (string, error) {
	if p.render == nil {
		return "", errors.New("渲染服务未配置")
	}
	if strings.TrimSpace(job.BlocksJSON) == "" {
		return "", errors.New("任务没存原文块，没法用现在的规则重排")
	}
	rulePackID, err := publishRulePackIDForJob(job)
	if err != nil {
		return "", err
	}
	result, err := p.render(publishRenderContext(ctx, rulePackID, job), job.Title, publishRenderSourceURL(job), job.BlocksJSON, true)
	if err != nil {
		return "", fmt.Errorf("用现在的规则重排失败: %w", err)
	}
	imageMap := map[string]string{}
	if strings.TrimSpace(job.ImageMap) != "" {
		_ = json.Unmarshal([]byte(job.ImageMap), &imageMap)
	}
	return rewriteImgSrcs(result.HTML, imageMap), nil
}

// publishEditReportTarget 一次读出的投递目标：接收人与发私信用的审核应用来自同一份配置，
// 中途换了应用也不会拿 B 应用的凭证去发 A 应用的 open_id。
type publishEditReportTarget struct {
	owners []publishDistributionOwner
	app    feishuNotificationApp
	appOK  bool
}

// publishEditReportTargetNow 手改周报接收人：open_id 只对选人时的审核应用有效。
func (p *publishPipeline) publishEditReportTargetNow() (publishEditReportTarget, error) {
	var target publishEditReportTarget
	if p == nil || p.deliverySettings == nil {
		return target, nil
	}
	settings, apps, err := p.deliverySettings.resolve()
	if err != nil {
		return target, err
	}
	owners, app := publishOwnersFor(settings, publishOwnerRoleEditReport)
	if strings.TrimSpace(app) == "" || strings.TrimSpace(app) != strings.TrimSpace(settings.AppProfileID) {
		return target, nil
	}
	target.owners = owners
	target.app, target.appOK = notificationAppByID(apps, settings.AppProfileID)
	return target, nil
}

func (p *publishPipeline) publishEditReportRecipients() ([]publishDistributionOwner, error) {
	target, err := p.publishEditReportTargetNow()
	return target.owners, err
}

// publishEditReportUUID 飞书去重键（≤50 字符）：同一周、同一轮重发、同一个人只会收到一条。
func publishEditReportUUID(week string, generation int, openID string) string {
	digest := sha256.Sum256([]byte(openID))
	return fmt.Sprintf("edit-report-%s-g%d-%x", week, generation, digest[:6])
}

func (p *publishPipeline) sendPublishEditReport(ctx context.Context, target publishEditReportTarget, openID, uuid string, post map[string]any) (string, error) {
	var messageID string
	var err error
	if p.editReportSender != nil {
		messageID, err = p.editReportSender(ctx, openID, post)
	} else {
		if !target.appOK {
			return "", errors.New("审核应用未配置凭证")
		}
		notifier := newFeishuNotifierForOpenID(target.app, openID)
		if notifier == nil {
			return "", errors.New("审核应用凭证或接收人不完整")
		}
		content, marshalErr := json.Marshal(post)
		if marshalErr != nil {
			return "", fmt.Errorf("feishu: 序列化手改周报: %w", marshalErr)
		}
		messageID, err = notifier.sendMessageWithUUID(ctx, "post", string(content), uuid)
	}
	if err != nil {
		return "", err
	}
	// 没拿到消息编号不能算送达（例如网关回了个空 JSON），下一轮带同一个去重键再发。
	if !feishuMessageIDSafe(strings.TrimSpace(messageID)) {
		return "", &feishuSendUncertainError{errors.New("飞书没有返回消息编号，未确认送达")}
	}
	return strings.TrimSpace(messageID), nil
}

// deliverPublishEditReport 生成一周清单并私信发给每位接收人（已经收到的不重发）。
// 只发已经过完的一周。force=true 时这周即使发过也重新发给所有人（手动接口）。
func (p *publishPipeline) deliverPublishEditReport(ctx context.Context, start, end time.Time, actor string, force bool) (publishEditReportRecord, publishEditReport, error) {
	p.editReportMu.Lock()
	defer p.editReportMu.Unlock()
	week := start.In(publishSiteTimeZone).Format("2006-01-02")
	nowT := pipelineNow(p)
	if nowT.Before(end) {
		// 提前发会把没过完的一周记成已发，到周一定时任务就跳过了，后半周的稿永远不统计。
		return publishEditReportRecord{Week: week}, publishEditReport{}, errPublishEditReportWeekOpen
	}
	if lastWeek, _ := publishEditReportLastWeek(nowT); start.Before(lastWeek.AddDate(0, 0, -7*(publishEditReportKeepRecords-1))) {
		return publishEditReportRecord{Week: week}, publishEditReport{}, errPublishEditReportWeekTooOld
	}
	rec, _, err := p.editReports.get(week)
	if err != nil {
		return rec, publishEditReport{}, err
	}
	if rec.State == publishEditReportStateSent && !force {
		return rec, publishEditReport{}, errPublishEditReportAlreadySent
	}
	if force {
		rec = publishEditReportRecord{Week: week, Generation: rec.Generation + 1}
	}
	rec.Actor = strings.TrimSpace(actor)
	now := nowT.Format(time.RFC3339)
	target, err := p.publishEditReportTargetNow()
	// 上次最后一人已经发成、只差写「已发」就中断了：回执齐全就补记已发，不重发也不误报死信。
	if !force && err == nil && publishEditReportDelivered(rec, target.owners) {
		rec.State, rec.Error, rec.UpdatedAt = publishEditReportStateSent, "", now
		if rec.SentAt == "" {
			rec.SentAt = now
		}
		return rec, publishEditReport{}, p.editReports.put(rec)
	}
	// 试满 8 次（含上次发到一半进程中断、没来得及收尾的）直接收尾成死信，不再外调；要再发用 force。
	if rec.Attempts >= publishEditReportMaxAttempts {
		if rec.State != publishEditReportStateDeadLetter {
			rec.State, rec.UpdatedAt = publishEditReportStateDeadLetter, now
			if rec.Error == "" {
				rec.Error = errPublishEditReportGaveUp.Error()
			}
			if putErr := p.editReports.put(rec); putErr != nil {
				return rec, publishEditReport{}, putErr
			}
		}
		return rec, publishEditReport{}, errPublishEditReportGaveUp
	}

	if err == nil && len(target.owners) == 0 {
		rec.State, rec.Skipped, rec.Error, rec.UpdatedAt = publishEditReportStateSkipped, "没有设置手改周报接收人，未发送", "", now
		return rec, publishEditReport{}, p.editReports.put(rec)
	}
	// 先落「这次要发、第几次」再动手：落不了盘就不发，免得发了却记不住、每轮重发，也保证 8 次后一定进死信。
	rec.Attempts++
	rec.State, rec.Skipped, rec.UpdatedAt = publishEditReportStateSending, "", now
	if putErr := p.editReports.put(rec); putErr != nil {
		return rec, publishEditReport{}, putErr
	}
	var report publishEditReport
	if err == nil {
		report, err = p.buildPublishEditReport(ctx, start, end)
	}
	if err == nil {
		rec.Compared, rec.Edited, rec.Listed = report.Compared, report.Edited, len(report.Listed)
		post := buildPublishEditReportPost(report)
		if rec.MessageIDs == nil {
			rec.MessageIDs = map[string]string{}
		}
		if rec.Unconfirmed == nil {
			rec.Unconfirmed = map[string]string{}
		}
		var failures, unconfirmed []string
		for _, owner := range target.owners {
			id := strings.TrimSpace(owner.OpenID)
			if id == "" || rec.MessageIDs[id] != "" {
				continue
			}
			name := firstNonEmpty(owner.Name, id)
			// 上次发给他可能已经到了飞书却没拿到结果：去重期内带同一个 uuid 重发，飞书只会留一条；过了就不再盲发。
			fresh := false
			if since, ok := rec.Unconfirmed[id]; ok {
				if at, parseErr := time.Parse(time.RFC3339, since); parseErr != nil || nowT.Sub(at) >= publishEditReportDedupWindow {
					unconfirmed = append(unconfirmed, name)
					continue
				}
			} else {
				fresh = true
				rec.Unconfirmed[id] = now
				if putErr := p.editReports.put(rec); putErr != nil {
					failures = append(failures, "记录发送时间失败："+putErr.Error())
					break
				}
			}
			messageID, sendErr := p.sendPublishEditReport(ctx, target, id, publishEditReportUUID(week, rec.Generation, id), post)
			if sendErr != nil {
				// 飞书明确拒收（返回了错误码）就是没发出去；这次之前也没有不确定的，下一轮照常重试。
				if fresh && !feishuSendUncertain(sendErr) {
					delete(rec.Unconfirmed, id)
				}
				failures = append(failures, name+"："+sendErr.Error())
				continue
			}
			// 每发出一人就落回执；落盘失败停下，下一轮带同一个去重键补发，飞书不会让他收到两条。
			rec.MessageIDs[id] = messageID
			delete(rec.Unconfirmed, id)
			if putErr := p.editReports.put(rec); putErr != nil {
				failures = append(failures, "记录回执失败："+putErr.Error())
				break
			}
		}
		if len(unconfirmed) > 0 {
			failures = append(failures, fmt.Sprintf("%s：%s；确认没收到再用 force=1 重发", strings.Join(unconfirmed, "、"), errPublishEditReportUnconfirmed.Error()))
		}
		if len(failures) > 0 {
			err = errors.New("发送手改周报失败：" + strings.Join(failures, "；"))
			if len(unconfirmed) > 0 {
				err = fmt.Errorf("%w：%s", errPublishEditReportUnconfirmed, err.Error())
			}
		}
	}
	rec.UpdatedAt = now
	if err != nil {
		rec.Error = editReportClip(strings.Join(strings.Fields(err.Error()), " "), 500)
		rec.State = publishEditReportStateFailed
		if rec.Attempts >= publishEditReportMaxAttempts || errors.Is(err, errPublishEditReportUnconfirmed) {
			rec.State = publishEditReportStateDeadLetter
		}
	} else {
		rec.State, rec.Error, rec.SentAt = publishEditReportStateSent, "", now
	}
	if putErr := p.editReports.put(rec); putErr != nil && err == nil {
		err = putErr
	}
	return rec, report, err
}

// runScheduledPublishEditReport 周一 10 点后发上一周的清单；发过、死信的不再动。
// 没设接收人记为 skipped，之后设了人在当周的下一轮补发。
func (p *publishPipeline) runScheduledPublishEditReport(ctx context.Context) {
	if p == nil || p.editReports == nil || p.store == nil || publishWorkersPaused() {
		return
	}
	now := pipelineNow(p)
	start, end := publishEditReportLastWeek(now)
	p.retirePublishEditReportLeftovers(start)
	if now.Before(end.Add(publishEditReportSendHour * time.Hour)) {
		return
	}
	rec, _, err := p.editReports.get(start.Format("2006-01-02"))
	if err != nil {
		log.Printf("publish: 读取手改周报记录失败：%v", err)
		return
	}
	switch rec.State {
	case publishEditReportStateSent, publishEditReportStateDeadLetter:
		return
	case publishEditReportStateSkipped:
		if owners, err := p.publishEditReportRecipients(); err != nil || len(owners) == 0 {
			return
		}
	}
	if _, _, err := p.deliverPublishEditReport(ctx, start, end, "", false); err != nil {
		log.Printf("publish: 手改周报（%s 那周）发送失败：%v", start.Format("2006-01-02"), err)
	}
}

// retirePublishEditReportLeftovers 换周后，更早那些周还停在「失败/发送中」的不再自动重发，收尾成死信、进监控。
// 例如周日深夜才设接收人、第一次就失败：周一起定时任务只管新的一周，旧的这条不收尾就永远没人知道。
func (p *publishPipeline) retirePublishEditReportLeftovers(current time.Time) {
	p.editReportMu.Lock()
	defer p.editReportMu.Unlock()
	records, err := p.editReports.list()
	if err != nil {
		log.Printf("publish: 读取手改周报记录失败：%v", err)
		return
	}
	week := current.In(publishSiteTimeZone).Format("2006-01-02")
	now := pipelineNow(p).Format(time.RFC3339)
	target, targetErr := p.publishEditReportTargetNow()
	for _, rec := range records {
		if rec.Week >= week || (rec.State != publishEditReportStateFailed && rec.State != publishEditReportStateSending) {
			continue
		}
		rec.UpdatedAt = now
		if targetErr == nil && publishEditReportDelivered(rec, target.owners) {
			rec.State, rec.Error = publishEditReportStateSent, ""
			if rec.SentAt == "" {
				rec.SentAt = now
			}
		} else {
			rec.State = publishEditReportStateDeadLetter
			rec.Error = editReportClip(strings.TrimSpace("换周时还没发出去，不再自动重发。"+rec.Error), 500)
		}
		if err := p.editReports.put(rec); err != nil {
			log.Printf("publish: 手改周报（%s 那周）收尾失败：%v", rec.Week, err)
		}
	}
}

// publishEditReportDelivered 现在的每位接收人都已有回执。
func publishEditReportDelivered(rec publishEditReportRecord, owners []publishDistributionOwner) bool {
	delivered := 0
	for _, owner := range owners {
		id := strings.TrimSpace(owner.OpenID)
		if id == "" {
			continue
		}
		if rec.MessageIDs[id] == "" {
			return false
		}
		delivered++
	}
	return delivered > 0
}

// startPublishEditReport 独立后台循环，每 30 分钟看一次是否该发、要不要重试。
func (p *publishPipeline) startPublishEditReport() {
	if p == nil || p.editReports == nil {
		return
	}
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			p.runScheduledPublishEditReport(ctx)
			cancel()
			time.Sleep(publishEditReportSweep)
		}
	}()
}

// applyPublishEditReportMonitoring 7 天内进了死信的周报给 warning（不影响 ready，不是 P0）。
func (p *publishPipeline) applyPublishEditReportMonitoring(status *publishMonitoringStatus) {
	if p == nil || p.editReports == nil || status == nil {
		return
	}
	records, err := p.editReports.list()
	if err != nil {
		return
	}
	now := pipelineNow(p)
	added := false
	for _, rec := range records {
		if rec.State != publishEditReportStateDeadLetter {
			continue
		}
		if at, err := time.Parse(time.RFC3339, rec.UpdatedAt); err == nil && now.Sub(at) > publishEditReportWarnFor {
			continue
		}
		status.Warnings = append(status.Warnings, "edit_report_dead_letter:"+rec.Week)
		added = true
	}
	if added {
		sort.Strings(status.Warnings)
	}
}

func registerPublishEditReportRoutes(mux *http.ServeMux, p *publishPipeline, auth *authService) {
	// POST /api/publish/edit-report?dry_run=1&week=YYYY-MM-DD&force=1
	// dry_run 只生成清单（含每篇的手改明细与私信预览），不发、不记；不带 week 是上一整周。
	mux.HandleFunc("POST /api/publish/edit-report", func(w http.ResponseWriter, r *http.Request) {
		if !requirePublishAdmin(w, r, auth) {
			return
		}
		if p.editReports == nil {
			writeError(w, http.StatusServiceUnavailable, "手改周报未启用")
			return
		}
		query := r.URL.Query()
		dryRun := query.Get("dry_run") == "1"
		if !dryRun && !requireDeploymentWritesActive(w) {
			return
		}
		start, end := publishEditReportLastWeek(pipelineNow(p))
		if week := strings.TrimSpace(query.Get("week")); week != "" {
			var err error
			if start, end, err = publishEditReportWeekFromParam(week); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
		defer cancel()
		if dryRun {
			report, err := p.buildPublishEditReport(ctx, start, end)
			if err != nil {
				writeError(w, http.StatusBadGateway, err.Error())
				return
			}
			owners, _ := p.publishEditReportRecipients()
			rec, _, _ := p.editReports.get(report.WeekStart)
			writeJSON(w, http.StatusOK, map[string]any{
				"dry_run": true, "report": report, "post": buildPublishEditReportPost(report),
				"recipients": len(owners), "record": rec,
			})
			return
		}
		rec, report, err := p.deliverPublishEditReport(ctx, start, end, publishDeliveryActor(r, auth), query.Get("force") == "1")
		switch {
		case errors.Is(err, errPublishEditReportAlreadySent):
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error() + "；要重发请加 force=1", "record": rec})
		case errors.Is(err, errPublishEditReportWeekOpen):
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "record": rec})
		case errors.Is(err, errPublishEditReportWeekTooOld):
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		case errors.Is(err, errPublishEditReportGaveUp), errors.Is(err, errPublishEditReportUnconfirmed):
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error() + "；确认没收到再加 force=1 重发", "record": rec})
		case err != nil:
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "record": rec})
		default:
			writeJSON(w, http.StatusOK, map[string]any{"record": rec, "listed": report.Listed})
		}
	})
}
