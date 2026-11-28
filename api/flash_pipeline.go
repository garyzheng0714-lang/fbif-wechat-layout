package main

// flash_pipeline.go — 快讯这条线（2026-11-29，用户在资讯订阅会话里定：FoodTalks 每天自动发 10 条 AI 快讯）：
//
//	资讯订阅交来文章编号 → 取原文事实 → 照飞书上最新的规范写 → 对原文、查风险、查不推荐说法
//	→ 审核群出卡片（通过并发布、驳回、改字）→ 人点通过才调 FoodTalks 快讯后台建一条公开的 → 状态推回资讯订阅
//
// 怎么跑：收下、人点按钮、重启时叫一下（kick），一篇一个 goroutine 往前走到要等人或到头为止；没成的按
// 1、5、15、30 分钟退避，之后每 30 分钟，24 小时还没成就落 failed 等人看。没有定时轮询。
// 发布没有幂等键：建之前先记 PublishAttempts，再按标题在快讯后台找；记过一次以后找到同题的就认是自己建的，
// 不建第二条（红线 26 的同一个道理：外调前落记录、结果不确定先找再建）。
// 开关 FLASH_ENABLED（中台）没开时这条线不装、入口回 503。卡片只发 FLASH_REVIEW_CHAT_ID 这个群，没配就不发卡、
// 稿子停在待审核。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	flashEnabledEnv    = "FLASH_ENABLED"
	flashReviewChatEnv = "FLASH_REVIEW_CHAT_ID"
	flashGiveUpAfter   = 24 * time.Hour
	flashConcurrent    = 2
	flashSiteRecent    = 50 // 写稿时给模型看的网站最近快讯、资讯各几条
)

var flashRetryDelays = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute}

func flashEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(flashEnabledEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// flashCards 往审核群发卡、改卡；nil 表示不发卡。
type flashCards interface {
	chatID() string
	send(ctx context.Context, card map[string]any) (string, error)
	update(ctx context.Context, messageID string, card map[string]any) error
}

type flashPipeline struct {
	store    *flashJobStore
	source   func(ctx context.Context, articleID string) (flashSourceDoc, error)
	spec     func(ctx context.Context) (flashSpec, error)
	writer   flashWriter
	risk     func(ctx context.Context, title, body, who string) (publishRiskAssessment, error)
	site     func() (foodTalksFlashAPI, error)
	cards    flashCards
	frontURL string
	now      func() time.Time

	mu      sync.Mutex
	running map[int64]bool
	again   map[int64]bool
	timers  map[int64]*time.Timer
	slots   chan struct{}
	stopped bool
}

func newFlashPipeline(store *flashJobStore) *flashPipeline {
	return &flashPipeline{
		store:   store,
		source:  fetchFlashSource,
		writer:  flashWriter{ai: mimoFlashAI, now: time.Now},
		risk:    aiAssessRiskText,
		now:     time.Now,
		running: map[int64]bool{},
		again:   map[int64]bool{},
		timers:  map[int64]*time.Timer{},
		slots:   make(chan struct{}, flashConcurrent),
		site: func() (foodTalksFlashAPI, error) {
			c, err := newFoodTalksClientFromEnv()
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		frontURL: siteFrontURL(),
	}
}

// submit 收下一篇；同一篇（article_id）只建一条。
func (p *flashPipeline) submit(articleID, sourceURL, callbackURL string) (flashJob, bool, error) {
	job, existing, err := p.store.create(articleID, sourceURL, callbackURL)
	if err != nil {
		return flashJob{}, false, err
	}
	if !existing {
		p.kick(job.ID)
	}
	return job, existing, nil
}

// recover 启动时接着做没做完的，没送达的状态补推。
func (p *flashPipeline) recover() {
	if p == nil {
		return
	}
	p.store.requeueStatusCallbacks()
	p.store.callbacks.startSending()
	jobs, err := p.store.list()
	if err != nil {
		log.Printf("flash: WARN 读快讯任务失败：%v", err)
		return
	}
	for _, j := range jobs {
		if j.terminal() || j.Stage == flashStageFailed {
			continue
		}
		if j.Stage == flashStageAwaitingReview && (p.cards == nil || j.CardMessageID != "") {
			continue
		}
		if at, err := time.Parse(time.RFC3339, j.RetryAt); err == nil && at.After(p.now()) {
			p.scheduleAt(j.ID, at)
			continue
		}
		p.kick(j.ID)
	}
}

func (p *flashPipeline) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopped = true
	for id, t := range p.timers {
		t.Stop()
		delete(p.timers, id)
	}
}

// kick 让这一篇往前走；正在走就等它这一步走完再看一眼。
func (p *flashPipeline) kick(id int64) {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	if p.running[id] {
		p.again[id] = true
		p.mu.Unlock()
		return
	}
	p.running[id] = true
	p.mu.Unlock()
	go func() {
		for {
			p.slots <- struct{}{}
			p.advance(id)
			<-p.slots
			p.mu.Lock()
			if p.again[id] && !p.stopped {
				delete(p.again, id)
				p.mu.Unlock()
				continue
			}
			delete(p.running, id)
			p.mu.Unlock()
			return
		}
	}()
}

func (p *flashPipeline) scheduleAt(id int64, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	if t := p.timers[id]; t != nil {
		t.Stop()
	}
	p.timers[id] = time.AfterFunc(time.Until(at), func() {
		p.mu.Lock()
		delete(p.timers, id)
		p.mu.Unlock()
		p.kick(id)
	})
}

// advance 一步一步走，直到要等人、到头、或排了重试。
func (p *flashPipeline) advance(id int64) {
	for i := 0; i < 8; i++ {
		job, err := p.store.byID(id)
		if err != nil {
			log.Printf("flash: WARN #%d %v", id, err)
			return
		}
		var stepErr error
		switch job.Stage {
		case flashStageQueued, flashStageWriting:
			stepErr = p.stepWrite(job)
		case flashStageChecking:
			stepErr = p.stepCheck(job)
		case flashStageAwaitingReview:
			p.stepCard(job)
			return
		case flashStagePublishing:
			stepErr = p.stepPublish(job)
		default:
			return
		}
		if stepErr != nil {
			p.fail(job, stepErr)
			return
		}
	}
}

// fail 这一步没成：没到 24 小时就排下一次，到了就落 failed。
func (p *flashPipeline) fail(job flashJob, cause error) {
	stage := job.Stage
	if stage == flashStageQueued {
		stage = flashStageWriting
	}
	now := p.now()
	started := now
	if t, err := time.Parse(time.RFC3339, job.CreatedAt); err == nil {
		started = t
	}
	if job.PublishIntentAt != "" {
		if t, err := time.Parse(time.RFC3339, job.PublishIntentAt); err == nil {
			started = t
		}
	}
	giveUp := errors.Is(cause, errFlashSourceNotFound) || now.Sub(started) >= flashGiveUpAfter
	delay := flashRetryDelays[len(flashRetryDelays)-1]
	if job.Failures < len(flashRetryDelays) {
		delay = flashRetryDelays[job.Failures]
	}
	retryAt := now.Add(delay)
	updated, err := p.store.update(job.ID, func(j *flashJob) {
		j.Failures++
		j.Error = truncateRunes(cause.Error(), 500)
		j.FailedStage = stage
		j.RetryAt = ""
		if giveUp {
			j.Stage = flashStageFailed
		} else {
			j.RetryAt = retryAt.UTC().Format(time.RFC3339)
		}
	})
	if err != nil {
		log.Printf("flash: WARN #%d 记失败也没记上：%v（原因：%v）", job.ID, err, cause)
		return
	}
	log.Printf("flash: #%d %s 没成（第 %d 次）：%v", job.ID, stage, updated.Failures, cause)
	if giveUp {
		p.refreshCard(updated)
		return
	}
	p.scheduleAt(job.ID, retryAt)
}

// stepWrite 取原文 → 判该不该写 → 照规范写。
func (p *flashPipeline) stepWrite(job flashJob) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if job.Stage == flashStageQueued {
		if _, err := p.store.update(job.ID, func(j *flashJob) { j.Stage = flashStageWriting }); err != nil {
			return err
		}
	}
	src, err := p.source(ctx, job.ArticleID)
	if err != nil {
		return fmt.Errorf("取原文：%w", err)
	}
	eventID := ""
	if src.Event != nil {
		eventID = src.Event.ID
	}
	if reason := flashSkipReason(src); reason != "" {
		return p.skip(job.ID, src, eventID, reason, "")
	}
	if dup, ok := p.sameEventJob(job.ID, eventID); ok {
		return p.skip(job.ID, src, eventID, "同一件事已经有快讯任务 #"+strconv.FormatInt(dup, 10), "")
	}
	var earliest *flashSourceDoc
	if r, ok := src.earliestOtherReport(); ok {
		if doc, err := p.source(ctx, r.ArticleID); err == nil {
			earliest = &doc
		} else {
			log.Printf("flash: #%d 取更早的那篇（%s）没成，只照这篇写：%v", job.ID, r.SourceName, err)
		}
	}
	spec, err := p.spec(ctx)
	if err != nil {
		return err
	}
	recent := p.siteRecent(ctx)
	usage := &aiUsage{}
	result, err := p.writer.write(withAIUsage(ctx, usage), spec, src, earliest, recent)
	spent := usage.snapshot()
	if err != nil {
		p.addUsage(job.ID, spent)
		return err
	}
	if result.Skip != "" || result.DuplicateOf != "" {
		reason := result.Skip
		if result.DuplicateOf != "" {
			reason = "FoodTalks 网站已经发过同一件事"
		}
		p.addUsage(job.ID, spent)
		return p.skip(job.ID, src, eventID, reason, result.DuplicateOf)
	}
	if src.hasFlag("verify") || src.hasFlag("controversy") {
		if len(result.Draft.Hints) == 0 {
			result.Draft.Hints = []string{"资讯订阅判这篇的说法有待核实或有争议，发布前请对一下原文"}
		}
	}
	_, err = p.store.update(job.ID, func(j *flashJob) {
		j.Source = &src
		j.Earliest = earliest
		j.EventID = eventID
		j.SpecRevision = spec.Revision
		j.SpecNote = spec.Stale
		j.Version = 1
		j.Draft = &result.Draft
		j.Checks = &flashChecks{Version: 1, Unsupported: result.Unsupported, Avoid: result.Avoid}
		j.Usage = addTokenUsage(j.Usage, spent)
		j.Stage = flashStageChecking
		j.Failures, j.Error, j.FailedStage, j.RetryAt = 0, "", "", ""
	})
	return err
}

// flashSkipReason 资讯订阅已经判了不该写的。
func flashSkipReason(src flashSourceDoc) string {
	if src.Judgment != nil && strings.TrimSpace(src.Judgment.HardFilter) != "" {
		return "资讯订阅判定不写：" + src.Judgment.HardFilter
	}
	if src.hasFlag("sponsored") {
		return "资讯订阅判定是推广稿，不写"
	}
	return ""
}

func (p *flashPipeline) skip(id int64, src flashSourceDoc, eventID, reason, duplicateOf string) error {
	job, err := p.store.update(id, func(j *flashJob) {
		j.Source = &src
		j.EventID = eventID
		j.Stage = flashStageSkipped
		j.SkipReason = reason
		j.DuplicateOf = duplicateOf
		j.Failures, j.Error, j.FailedStage, j.RetryAt = 0, "", "", ""
	})
	if err == nil {
		log.Printf("flash: #%d 不写：%s %s", job.ID, reason, duplicateOf)
	}
	return err
}

// sameEventJob 同一件事（资讯订阅的事件编号）已有一条没被驳回、没失败的快讯任务。
func (p *flashPipeline) sameEventJob(id int64, eventID string) (int64, bool) {
	if eventID == "" {
		return 0, false
	}
	jobs, err := p.store.list()
	if err != nil {
		return 0, false
	}
	for _, j := range jobs {
		if j.ID == id || j.EventID != eventID {
			continue
		}
		switch j.Stage {
		case flashStageRejected, flashStageFailed, flashStageSkipped:
			continue
		}
		if j.ID < id {
			return j.ID, true
		}
	}
	return 0, false
}

// siteRecent 网站最近的快讯和资讯标题，给模型判同一件事。读不到就不给（发布前还会按标题查一次）。
func (p *flashPipeline) siteRecent(ctx context.Context) []string {
	if p.site == nil {
		return nil
	}
	site, err := p.site()
	if err != nil {
		return nil
	}
	// 只算前台看得到的：下架的快讯、没公开的资讯草稿（文章审核前先存的隐藏稿）不算网站发过。多取一倍再筛。
	var out []string
	if flashes, err := site.SearchFlashes(ctx, "", 2*flashSiteRecent); err == nil {
		n := 0
		for _, f := range flashes {
			if f.live() && n < flashSiteRecent {
				out = append(out, "快讯 "+flashDay(f.PublishTime)+" "+f.Title)
				n++
			}
		}
	} else {
		log.Printf("flash: 读网站最近快讯没成：%v", err)
	}
	if news, err := site.RecentNewsTitles(ctx, 2*flashSiteRecent); err == nil {
		n := 0
		for _, item := range news {
			if item.PublishStatus == foodTalksPublishStatusPublished && n < flashSiteRecent {
				out = append(out, "资讯 "+flashDay(item.PublishTime)+" "+item.Title)
				n++
			}
		}
	} else {
		log.Printf("flash: 读网站最近资讯没成：%v", err)
	}
	return out
}

func flashDay(raw string) string {
	if len(raw) >= 10 {
		return raw[:10]
	}
	return raw
}

func addTokenUsage(a, b aiTokenUsage) aiTokenUsage {
	return aiTokenUsage{Calls: a.Calls + b.Calls, InputTokens: a.InputTokens + b.InputTokens,
		CachedInputTokens: a.CachedInputTokens + b.CachedInputTokens, OutputTokens: a.OutputTokens + b.OutputTokens,
		ReasoningTokens: a.ReasoningTokens + b.ReasoningTokens}
}

func (p *flashPipeline) addUsage(id int64, spent aiTokenUsage) {
	if spent.Calls == 0 {
		return
	}
	_, _ = p.store.update(id, func(j *flashJob) { j.Usage = addTokenUsage(j.Usage, spent) })
}

// stepCheck 对当前这一版查：人改过的重新对原文、查不推荐说法；每一版都查风险（发布线同一套）。
func (p *flashPipeline) stepCheck(job flashJob) error {
	if job.Draft == nil || job.Source == nil {
		return errors.New("没有稿子可查")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	usage := &aiUsage{}
	ctx = withAIUsage(ctx, usage)
	checks := flashChecks{Version: job.Version}
	if job.Checks != nil && job.Checks.Version == job.Version {
		checks = *job.Checks
	} else {
		spec, err := p.spec(ctx)
		if err != nil {
			return err
		}
		unsupported, err := p.writer.verify(ctx, spec, *job.Draft, *job.Source, job.Earliest)
		if err != nil {
			p.addUsage(job.ID, usage.snapshot())
			return fmt.Errorf("对原文：%w", err)
		}
		checks.Unsupported = unsupported
		checks.Avoid = flashAvoidHits(*job.Draft, spec)
	}
	who := "快讯 #" + strconv.FormatInt(job.ID, 10)
	risk, err := p.risk(ctx, job.Draft.Title, strings.Join(job.Draft.Body, "\n\n"), who)
	if err != nil {
		// 和发布线一样：检查没做成不当成没问题，卡片上写明「检查未完成」，人看了再定。
		risk = fallbackPublishRisk(risk.Raw, err)
	}
	checks.Risk = risk
	checks.CheckedAt = p.now().UTC().Format(time.RFC3339)
	spent := usage.snapshot()
	version := job.Version
	_, swapped, err := p.store.updateIf(job.ID, func(j flashJob) bool {
		return j.Stage == flashStageChecking && j.Version == version
	}, func(j *flashJob) {
		j.Checks = &checks
		j.Usage = addTokenUsage(j.Usage, spent)
		j.Stage = flashStageAwaitingReview
		j.Failures, j.Error, j.FailedStage, j.RetryAt = 0, "", "", ""
	})
	if err == nil && !swapped {
		p.addUsage(job.ID, spent) // 查的时候人又改了一版：这一轮作废，下一轮查新的
	}
	return err
}

// stepCard 待审核：没发过卡就发，发过就原位更新。发卡失败不挡流程，过会儿再发。
func (p *flashPipeline) stepCard(job flashJob) {
	if p.cards == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	card := buildFlashCard(job, p.now())
	if job.CardMessageID != "" {
		if err := p.cards.update(ctx, job.CardMessageID, card); err != nil {
			log.Printf("flash: WARN #%d 更新卡片没成：%v", job.ID, err)
		}
		return
	}
	msgID, err := p.cards.send(ctx, card)
	if err != nil {
		log.Printf("flash: WARN #%d 发卡没成：%v", job.ID, err)
		p.scheduleAt(job.ID, p.now().Add(5*time.Minute))
		return
	}
	_, _ = p.store.update(job.ID, func(j *flashJob) { j.CardMessageID = msgID })
}

// refreshCard 状态变了把卡片原位改掉（发过卡才改）。
func (p *flashPipeline) refreshCard(job flashJob) {
	if p.cards == nil || job.CardMessageID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.cards.update(ctx, job.CardMessageID, buildFlashCard(job, p.now())); err != nil {
		log.Printf("flash: WARN #%d 更新卡片没成：%v", job.ID, err)
	}
}

// approve 人点了通过：这一版查过、没有挡发布的风险，才进发布。
func (p *flashPipeline) approve(id int64, version int, operator string) (flashJob, error) {
	job, err := p.store.byID(id)
	if err != nil {
		return flashJob{}, err
	}
	if reason := flashApproveBlocked(job, version); reason != "" {
		return job, errors.New(reason)
	}
	now := p.now().UTC().Format(time.RFC3339)
	updated, swapped, err := p.store.updateIf(id, func(j flashJob) bool { return flashApproveBlocked(j, version) == "" }, func(j *flashJob) {
		j.Stage = flashStagePublishing
		j.ReviewedBy = operator
		j.ReviewedAt = now
		j.PublishIntentAt = now
		j.Failures, j.Error, j.FailedStage, j.RetryAt = 0, "", "", ""
	})
	if err != nil {
		return flashJob{}, err
	}
	if !swapped {
		return updated, errors.New("卡片不是最新的，请看最新一版")
	}
	p.kick(id)
	return updated, nil
}

func flashApproveBlocked(j flashJob, version int) string {
	switch {
	case j.Stage == flashStagePublishing || j.Stage == flashStagePublished:
		return "已经通过了"
	case j.Stage != flashStageAwaitingReview:
		return "这条现在不能发（" + flashStageLabel(j.Stage) + "）"
	case j.Version != version:
		return "稿子改过了，请看最新一版再点"
	case j.Checks == nil || j.Checks.Version != j.Version:
		return "这一版还没查完"
	case j.Checks.Risk.Level == publishRiskBlock:
		return "风险检查判了不能发，先改字"
	}
	return ""
}

func (p *flashPipeline) reject(id int64, version int, operator string) (flashJob, error) {
	now := p.now().UTC().Format(time.RFC3339)
	updated, swapped, err := p.store.updateIf(id, func(j flashJob) bool {
		return (j.Stage == flashStageAwaitingReview || j.Stage == flashStageChecking || j.Stage == flashStageFailed) && j.Version == version
	}, func(j *flashJob) {
		j.Stage = flashStageRejected
		j.ReviewedBy = operator
		j.ReviewedAt = now
		j.RetryAt = ""
	})
	if err != nil {
		return flashJob{}, err
	}
	if !swapped {
		return updated, errors.New("这条现在不能驳回（" + flashStageLabel(updated.Stage) + "）")
	}
	return updated, nil
}

// edit 人改了标题、正文：版本加一，重新查。
func (p *flashPipeline) edit(id int64, version int, title, body, operator string) (flashJob, error) {
	title = strings.TrimSpace(title)
	paragraphs := splitFlashParagraphs(body)
	if title == "" || len(paragraphs) == 0 {
		return flashJob{}, errors.New("标题和正文都要有")
	}
	updated, swapped, err := p.store.updateIf(id, func(j flashJob) bool {
		return (j.Stage == flashStageAwaitingReview || j.Stage == flashStageChecking) && j.Version == version && j.Draft != nil
	}, func(j *flashJob) {
		draft := *j.Draft
		draft.Title = title
		draft.Body = paragraphs
		j.Draft = &draft
		j.Version++
		j.EditedBy = operator
		j.Checks = nil
		j.Stage = flashStageChecking
	})
	if err != nil {
		return flashJob{}, err
	}
	if !swapped {
		return updated, errors.New("卡片不是最新的，请看最新一版再改")
	}
	p.kick(id)
	return updated, nil
}

// splitFlashParagraphs 改字框里一行一段（空行也算分段）。
func splitFlashParagraphs(body string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// flashCreatedSince 这条快讯的发布时间在点通过（intent）之后；留 2 分钟余量给两边时钟差。读不出时间的不认。
func flashCreatedSince(f foodTalksFlash, intent string) bool {
	at, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(f.PublishTime), beijing)
	if err != nil {
		return false
	}
	since, err := time.Parse(time.RFC3339, intent)
	if err != nil {
		return false
	}
	return !at.Before(since.Add(-2 * time.Minute))
}

// stepPublish 发到 FoodTalks：先找再建，建了马上记编号，再读回确认看得见。
func (p *flashPipeline) stepPublish(job flashJob) error {
	if job.Draft == nil {
		return errors.New("没有稿子可发")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	site, err := p.site()
	if err != nil {
		return fmt.Errorf("FoodTalks 没配：%w", err)
	}
	id := job.FlashID
	if id == 0 {
		found, err := site.SearchFlashes(ctx, job.Draft.Title, 20)
		if err != nil {
			return fmt.Errorf("发之前按标题查 FoodTalks 快讯：%w", err)
		}
		for _, f := range found {
			if strings.TrimSpace(f.Title) != strings.TrimSpace(job.Draft.Title) {
				continue
			}
			if job.PublishAttempts == 0 {
				if !f.live() {
					continue // 下架了的不算网站上有
				}
				// 一次都没建过，网站上却有同题的在架：是别人发过的同一件事，不发第二条。
				_, err := p.store.update(job.ID, func(j *flashJob) {
					j.Stage = flashStageSkipped
					j.SkipReason = "FoodTalks 快讯里已经有同题的一条"
					j.DuplicateOf = p.flashURL(f.ID)
				})
				if err == nil {
					p.refreshCardByID(job.ID)
				}
				return err
			}
			// 建过但没记上编号：认点通过之后才建出来的那条（别的任务早先发过的同题旧快讯不认），列表新的在前。
			if flashCreatedSince(f, job.PublishIntentAt) {
				id = f.ID
				break
			}
		}
	}
	if id == 0 {
		if _, err := p.store.update(job.ID, func(j *flashJob) { j.PublishAttempts++ }); err != nil {
			return err
		}
		created, err := site.CreateFlash(ctx, foodTalksNewFlash{
			Title:     job.Draft.Title,
			Content:   foodTalksFlashHTML(job.Draft.Body),
			SourceURL: job.Draft.Source.URL,
			Publish:   true,
		})
		if err != nil {
			return fmt.Errorf("FoodTalks 建快讯：%w（%s）", err, flashCreateOutcome(err))
		}
		id = created
		if _, err := p.store.update(job.ID, func(j *flashJob) { j.FlashID = id }); err != nil {
			return err
		}
	}
	got, err := site.GetFlash(ctx, id)
	if err != nil {
		if _, uerr := p.store.update(job.ID, func(j *flashJob) { j.FlashID = id }); uerr != nil {
			return uerr
		}
		return fmt.Errorf("建好了 %d，读回没成：%w", id, err)
	}
	if !got.live() {
		return fmt.Errorf("FoodTalks 快讯 %d 读回不是公开状态（发布 %d、下架 %d）", id, got.PublishStatus, got.IsBanned)
	}
	published, err := p.store.update(job.ID, func(j *flashJob) {
		j.FlashID = id
		j.FlashURL = p.flashURL(id)
		j.PublishedAt = p.now().UTC().Format(time.RFC3339)
		j.Stage = flashStagePublished
		j.Failures, j.Error, j.FailedStage, j.RetryAt = 0, "", "", ""
	})
	if err != nil {
		return err
	}
	log.Printf("flash: #%d 已发到 FoodTalks 快讯 %d", job.ID, id)
	p.refreshCard(published)
	return nil
}

// flashCreateOutcome 建快讯失败时说清楚建没建上：网络断在中间的不知道，下次先找再建。
func flashCreateOutcome(err error) string {
	var apiErr *foodTalksAPIError
	if errors.As(err, &apiErr) {
		return "FoodTalks 拒了，没建上"
	}
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) {
		return "不知道建没建上，下次先按标题找"
	}
	return "下次先按标题找再建"
}

func (p *flashPipeline) refreshCardByID(id int64) {
	if job, err := p.store.byID(id); err == nil {
		p.refreshCard(job)
	}
}

func (p *flashPipeline) flashURL(id int64) string {
	return strings.TrimRight(p.frontURL, "/") + "/flash/" + strconv.FormatInt(id, 10)
}

// retry 人要求重跑一条失败的：从失败那一步接着做。
func (p *flashPipeline) retry(id int64) (flashJob, error) {
	updated, swapped, err := p.store.updateIf(id, func(j flashJob) bool { return j.Stage == flashStageFailed }, func(j *flashJob) {
		stage := j.FailedStage
		if stage == "" {
			stage = flashStageWriting
		}
		if stage == flashStagePublishing {
			j.PublishIntentAt = p.now().UTC().Format(time.RFC3339)
		} else {
			j.CreatedAt = p.now().UTC().Format(time.RFC3339) // 重新给 24 小时
		}
		j.Stage = stage
		j.Failures, j.RetryAt = 0, ""
	})
	if err != nil {
		return flashJob{}, err
	}
	if !swapped {
		return updated, errors.New("只有失败的能重跑，这条是" + flashStageLabel(updated.Stage))
	}
	p.kick(id)
	return updated, nil
}

func flashStageLabel(stage string) string {
	switch stage {
	case flashStageQueued:
		return "排队中"
	case flashStageWriting:
		return "写稿中"
	case flashStageChecking:
		return "检查中"
	case flashStageAwaitingReview:
		return "待审核"
	case flashStagePublishing:
		return "发布中"
	case flashStagePublished:
		return "已发布"
	case flashStageRejected:
		return "已驳回"
	case flashStageSkipped:
		return "不写"
	case flashStageFailed:
		return "失败"
	}
	return stage
}

// flashMonitoringFailures 监控口径：落了 failed 的，加上连着失败 3 次还在重试的。
func (p *flashPipeline) flashMonitoringFailures() int {
	if p == nil {
		return 0
	}
	jobs, err := p.store.list()
	if err != nil {
		return 1
	}
	cutoff := p.now().Add(-7 * 24 * time.Hour)
	n := 0
	for _, j := range jobs {
		updated, _ := time.Parse(time.RFC3339, j.UpdatedAt)
		if j.Stage == flashStageFailed && updated.After(cutoff) {
			n++
		} else if !j.terminal() && j.Stage != flashStageFailed && j.Failures >= 3 {
			n++
		}
	}
	return n
}
