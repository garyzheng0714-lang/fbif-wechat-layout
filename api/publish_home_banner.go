package main

// publish_home_banner.go — FoodTalks 官网首页轮播图自动换（2026-11-28 用户拍板，规矩来自 11-23 网站自动化会议纪要）。
//
// 每天一张：FBIF食品饮料创新当天的头条（source_url 里 idx=1 的那篇，转载的也算）发到 FoodTalks 的那一刻，
// 用它的封面、标题和官网链接建一张首页轮播图，再把首页上最旧的下架，首页一直保持 6 张。
// 头条当天没上 FoodTalks 就不换，之前几天的不补。
//
// 怎么做到只建一次、重试也安全：任务落为已发布的同一次写入里记下待办（home_banner.state=pending），
// 由 runHomeBanners 消化；建之前先按官网链接在后台找，有了就沿用，所以上次建了却没记上也不会建第二张。
// 失败按退避自己再试，过了当天还没成就放弃；连续 3 次没成或放弃了，监控报 home_banner_failed。
// 没有定时轮询：只在发布那一刻、开机恢复、退避到点时跑。规矩和核对记录见 docs/foodtalks-home-banner.md。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// homeBannerWechatBiz FBIF食品饮料创新公众号文章链接里的 __biz（2026-11 正式任务 #400–#411 都是它）。
	homeBannerWechatBiz = "MzA3ODEzMjg5Mg=="
	homeBannerPosition  = "home"
	homeBannerKeep      = 6
	// homeBannerAlertAfter 连续失败几次才找人。
	homeBannerAlertAfter = 3

	homeBannerPending  = "pending"  // 要建（或要沿用、要下旧图）
	homeBannerCreating = "creating" // 新建请求可能已发出：下一次先按链接找，找不到才再建
	homeBannerDone     = "done"
	homeBannerSkipped  = "skipped" // 按规矩不建（当天已有一张、文章已下线）
	homeBannerFailed   = "failed"  // 过了期限还没建成，放弃
)

// homeBannerBackoff 第 n 次失败后隔多久再试，最后一档一直用到期限。
var homeBannerBackoff = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute}

// publishHomeBanner 一篇头条的首页轮播图记录，存在任务上。
type publishHomeBanner struct {
	State string `json:"state,omitempty"`
	// Day 头条那天（北京时间，按任务进来的时间算；公众号一早发、几分钟内就进来）。每天只建一张。
	Day string `json:"day,omitempty"`
	// Until 过了这一刻还没建成就放弃：自动的到当天 24 点，管理员手动要的给 24 小时。
	Until       string `json:"until,omitempty"`
	Manual      bool   `json:"manual,omitempty"`
	RequestedAt string `json:"requested_at,omitempty"`
	BannerID    int64  `json:"banner_id,omitempty"`
	// RetiredIDs 为它下架的旧图。
	RetiredIDs []int64 `json:"retired_ids,omitempty"`
	Failures   int     `json:"failures,omitempty"`
	NextAt     string  `json:"next_at,omitempty"`
	Error      string  `json:"error,omitempty"`
	Note       string  `json:"note,omitempty"`
	FinishedAt string  `json:"finished_at,omitempty"`
}

func (b publishHomeBanner) open() bool {
	return b.State == homeBannerPending || b.State == homeBannerCreating
}

// homeBannerHeadlineDay 这篇是不是 FBIF食品饮料创新的头条；是的话交回头条那天。
// 只看这篇自己的公众号链接（source_url 和抓取后认出的同文长链），不看 original_source_url：那是被转载的原文。
func homeBannerHeadlineDay(job publishJob) (string, bool) {
	links := append([]string{job.SourceURL}, job.SourceAliases...)
	headline := false
	for _, link := range links {
		u, err := url.Parse(strings.TrimPrefix(strings.TrimSpace(link), "site:"))
		if err != nil || !strings.EqualFold(u.Hostname(), "mp.weixin.qq.com") {
			continue
		}
		q := u.Query()
		if q.Get("__biz") == homeBannerWechatBiz && q.Get("idx") == "1" {
			headline = true
			break
		}
	}
	created, err := time.Parse(time.RFC3339, job.CreatedAt)
	if !headline || err != nil {
		return "", false
	}
	return homeBannerDayOf(created), true
}

func homeBannerDayOf(t time.Time) string {
	return t.In(publishExternalAutoZone).Format("2006-01-02")
}

// homeBannerDayEnd 北京时间那天的 24 点。
func homeBannerDayEnd(day string) time.Time {
	start, err := time.ParseInLocation("2006-01-02", day, publishExternalAutoZone)
	if err != nil {
		return time.Time{}
	}
	return start.AddDate(0, 0, 1)
}

// markHomeBannerPending 在任务落为已发布的同一次写入里调用：是当天的头条、还没记过，就记待办。
func markHomeBannerPending(j *publishJob, now time.Time) {
	if j == nil || j.HomeBanner.State != "" {
		return
	}
	day, ok := homeBannerHeadlineDay(*j)
	if !ok || day != homeBannerDayOf(now) {
		return
	}
	j.HomeBanner = publishHomeBanner{
		State:       homeBannerPending,
		Day:         day,
		Until:       homeBannerDayEnd(day).UTC().Format(time.RFC3339),
		RequestedAt: now.UTC().Format(time.RFC3339),
	}
}

// markPublishedFollowUps 任务落为已发布时要顺带记下的待办（自动外发、首页轮播图），三个发布出口共用。
func markPublishedFollowUps(j *publishJob, now time.Time) {
	markExternalAutoDistributionPending(j)
	markHomeBannerPending(j, now)
}

func (p *publishPipeline) kickPublishedFollowUps() {
	p.kickExternalAutoDistribution()
	p.kickHomeBanners()
}

func (p *publishPipeline) kickHomeBanners() {
	if p == nil || p.homeBanners == nil {
		return
	}
	p.spawnExternalWork(p.runHomeBanners)
}

var errHomeBannerNotHeadline = errors.New("这篇不是 FBIF食品饮料创新的头条（公众号链接里 idx=1 的那篇），不建首页轮播图")

// requestHomeBanner 管理员手动要一张（上线当天补跑、失败后重跑）：不看是不是当天，其余规矩照旧。
// 已经建好的原样返回，不会建第二张。
func (p *publishPipeline) requestHomeBanner(jobID int64) (publishJob, error) {
	if p.homeBanners == nil {
		return publishJob{}, errors.New("首页轮播图没有接 FoodTalks")
	}
	now := pipelineNow(p)
	var reason error
	job, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		reason = nil
		if _, ok := homeBannerHeadlineDay(current); !ok {
			reason = errHomeBannerNotHeadline
		} else if current.Stage != publishStagePublished || current.FoodTalksNewsID <= 0 {
			reason = fmt.Errorf("这篇还没发到 FoodTalks（阶段 %s），发上去才能建首页轮播图", current.Stage)
		}
		// 建好了的、正在做的原样返回。
		return reason == nil && current.HomeBanner.State != homeBannerDone && !current.HomeBanner.open()
	}, func(j *publishJob) {
		day, _ := homeBannerHeadlineDay(*j)
		j.HomeBanner = publishHomeBanner{
			State:       homeBannerPending,
			Day:         day,
			Until:       now.Add(24 * time.Hour).UTC().Format(time.RFC3339),
			Manual:      true,
			RequestedAt: now.UTC().Format(time.RFC3339),
		}
	})
	if err != nil {
		return job, err
	}
	if reason != nil {
		return job, reason
	}
	if swapped || job.HomeBanner.open() {
		p.kickHomeBanners()
	}
	return job, nil
}

// runHomeBanners 把到点的待办一张张做完；同一时间只有一轮。
func (p *publishPipeline) runHomeBanners() {
	if p == nil || p.store == nil || p.homeBanners == nil || publishWorkersPaused() {
		return
	}
	p.homeBannerMu.Lock()
	defer p.homeBannerMu.Unlock()
	jobs, err := p.store.all()
	if err != nil {
		log.Printf("publish: 首页轮播图读取任务失败：%v", err)
		return
	}
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	now := pipelineNow(p)
	var wake time.Time
	for _, job := range jobs {
		if !job.HomeBanner.open() {
			continue
		}
		if due, err := time.Parse(time.RFC3339, job.HomeBanner.NextAt); err == nil && due.After(now) {
			wake = homeBannerEarlier(wake, due)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		retryAt := p.advanceHomeBanner(ctx, job, jobs, now)
		cancel()
		wake = homeBannerEarlier(wake, retryAt)
		if latest, err := p.store.all(); err == nil {
			jobs = latest
			sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
		}
	}
	p.scheduleHomeBanners(wake, now)
}

func homeBannerEarlier(a, b time.Time) time.Time {
	if b.IsZero() || (!a.IsZero() && a.Before(b)) {
		return a
	}
	return b
}

// scheduleHomeBanners 退避到点再跑一轮；已经约了更早的就不再约。
func (p *publishPipeline) scheduleHomeBanners(at, now time.Time) {
	if at.IsZero() || p.retryStageLater == nil {
		return
	}
	if !p.homeBannerWakeAt.IsZero() && p.homeBannerWakeAt.After(now) && !p.homeBannerWakeAt.After(at) {
		return
	}
	p.homeBannerWakeAt = at
	p.retryStageLater(at.Sub(now), p.kickHomeBanners)
}

// advanceHomeBanner 做一篇：按规矩该不该建 → 官网上有没有 → 建 → 首页留 6 张。失败交回下次什么时候再试。
func (p *publishPipeline) advanceHomeBanner(ctx context.Context, job publishJob, jobs []publishJob, now time.Time) time.Time {
	hb := job.HomeBanner
	if job.Stage != publishStagePublished || job.FoodTalksNewsID <= 0 {
		p.finishHomeBanner(job.ID, homeBannerSkipped, fmt.Sprintf("文章已不在 FoodTalks 上（阶段 %s），不建", job.Stage), now)
		return time.Time{}
	}
	if until, err := time.Parse(time.RFC3339, hb.Until); err == nil && !now.Before(until) {
		note := "过了期限还没建成，不再试"
		if hb.Error != "" {
			note += "（最后一次：" + hb.Error + "）"
		}
		p.finishHomeBanner(job.ID, homeBannerFailed, note, now)
		return time.Time{}
	}
	for _, other := range jobs {
		if other.ID == job.ID || other.HomeBanner.Day != hb.Day {
			continue
		}
		if other.HomeBanner.State == homeBannerDone || (other.HomeBanner.open() && other.ID < job.ID) {
			p.finishHomeBanner(job.ID, homeBannerSkipped, fmt.Sprintf("%s 已经有一张了（任务 #%d），每天只换一张", hb.Day, other.ID), now)
			return time.Time{}
		}
	}
	ft, err := p.homeBanners()
	if err != nil {
		return p.failHomeBanner(job.ID, err, now)
	}
	articleURL := siteFrontURL() + "/news/" + strconv.FormatInt(job.FoodTalksNewsID, 10)
	bannerID := hb.BannerID
	if bannerID <= 0 {
		found, err := findHomeBanner(ctx, ft, job.Title, articleURL)
		if err != nil {
			return p.failHomeBanner(job.ID, err, now)
		}
		if found > 0 {
			bannerID = found
			p.saveHomeBanner(job.ID, func(b *publishHomeBanner) {
				b.BannerID = found
				b.Note = "官网上已有这篇的轮播图，沿用"
			})
		}
	}
	if bannerID <= 0 {
		cover := strings.TrimSpace(job.CoverURL)
		if cover == "" || strings.TrimSpace(job.Title) == "" {
			p.finishHomeBanner(job.ID, homeBannerSkipped, "这篇没有封面或标题，建不了", now)
			return time.Time{}
		}
		// 先记下「可能已发出」再建：断在中间，下一次先按链接找，不会建第二张。
		if !p.saveHomeBanner(job.ID, func(b *publishHomeBanner) { b.State = homeBannerCreating }) {
			return time.Time{}
		}
		id, err := ft.CreateBanner(ctx, foodTalksNewBanner{
			Img: cover, ImgMobile: cover, Position: homeBannerPosition, Title: strings.TrimSpace(job.Title), URL: articleURL,
		})
		if err != nil {
			return p.failHomeBanner(job.ID, err, now)
		}
		bannerID = id
		p.saveHomeBanner(job.ID, func(b *publishHomeBanner) { b.BannerID = id })
		log.Printf("publish: 首页轮播图已建（任务 #%d，轮播图 %d，%s）", job.ID, id, articleURL)
	}
	retired, note, err := keepHomeBannersAt(ctx, ft, bannerID, homeBannerKeep)
	for _, id := range retired {
		log.Printf("publish: 首页轮播图下架最旧的一张 %d（为任务 #%d 腾位）", id, job.ID)
	}
	if len(retired) > 0 {
		p.saveHomeBanner(job.ID, func(b *publishHomeBanner) { b.RetiredIDs = append(b.RetiredIDs, retired...) })
	}
	if err != nil {
		return p.failHomeBanner(job.ID, err, now)
	}
	p.finishHomeBanner(job.ID, homeBannerDone, note, now)
	return time.Time{}
}

// findHomeBanner 官网后台里点进去就是这篇的首页轮播图（不管下没下架）；没有交回 0。
func findHomeBanner(ctx context.Context, ft foodTalksBannerAPI, title, articleURL string) (int64, error) {
	live, err := ft.ListBanners(ctx, foodTalksBannerQuery{Position: homeBannerPosition, OnlyLive: true})
	if err != nil {
		return 0, err
	}
	same, err := ft.ListBanners(ctx, foodTalksBannerQuery{Position: homeBannerPosition, Title: title, Size: 20})
	if err != nil {
		return 0, err
	}
	want := homeBannerLink(articleURL)
	for _, b := range append(live, same...) {
		if homeBannerLink(b.URL) == want && b.ID > 0 {
			return b.ID, nil
		}
	}
	return 0, nil
}

func homeBannerLink(raw string) string {
	s := strings.TrimRight(strings.TrimSpace(raw), "/")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	return strings.ToLower(s)
}

// keepHomeBannersAt 让这张排最前，首页上多出 keep 的从最旧的开始下架。交回下架了哪些。
func keepHomeBannersAt(ctx context.Context, ft foodTalksBannerAPI, bannerID int64, keep int) ([]int64, string, error) {
	live, err := ft.ListBanners(ctx, foodTalksBannerQuery{Position: homeBannerPosition, OnlyLive: true})
	if err != nil {
		return nil, "", err
	}
	var mine *foodTalksBanner
	var others []foodTalksBanner
	var topSort int64
	for i := range live {
		if live[i].ID == bannerID {
			mine = &live[i]
			continue
		}
		others = append(others, live[i])
		topSort = max(topSort, live[i].Sort)
	}
	if mine == nil {
		// 建好后不在首页上（多半是有人下架了），尊重人的操作，不动别的。
		return nil, "这张轮播图不在首页上（可能被人下架了），没有动别的", nil
	}
	if mine.Sort <= topSort {
		if err := ft.SortBanner(ctx, bannerID, topSort+1); err != nil {
			return nil, "", err
		}
	}
	sort.SliceStable(others, func(i, j int) bool {
		if others[i].CreatedTime != others[j].CreatedTime {
			return others[i].CreatedTime < others[j].CreatedTime
		}
		return others[i].ID < others[j].ID
	})
	var retired []int64
	for i := 0; i < len(others)+1-keep; i++ {
		if err := ft.BanBanner(ctx, others[i].ID); err != nil {
			return retired, "", err
		}
		retired = append(retired, others[i].ID)
	}
	return retired, "", nil
}

// saveHomeBanner 改这篇还没结案的轮播图记录；交回改没改上。
func (p *publishPipeline) saveHomeBanner(jobID int64, mutate func(*publishHomeBanner)) bool {
	_, swapped, err := p.store.updateIf(jobID, func(current publishJob) bool {
		return current.HomeBanner.open()
	}, func(j *publishJob) { mutate(&j.HomeBanner) })
	if err != nil {
		log.Printf("publish: 首页轮播图记录写不进任务 #%d：%v", jobID, err)
		return false
	}
	return swapped
}

func (p *publishPipeline) finishHomeBanner(jobID int64, state, note string, now time.Time) {
	p.saveHomeBanner(jobID, func(b *publishHomeBanner) {
		b.State = state
		b.Note = firstNonEmpty(note, b.Note)
		b.NextAt = ""
		b.FinishedAt = now.UTC().Format(time.RFC3339)
		if state == homeBannerDone || state == homeBannerSkipped {
			b.Failures = 0
			b.Error = ""
		}
	})
	if state != homeBannerDone {
		log.Printf("publish: 首页轮播图（任务 #%d）%s：%s", jobID, state, note)
	}
}

// failHomeBanner 记一次失败，按退避约下一次。
func (p *publishPipeline) failHomeBanner(jobID int64, cause error, now time.Time) time.Time {
	var next time.Time
	p.saveHomeBanner(jobID, func(b *publishHomeBanner) {
		b.Failures++
		b.Error = cause.Error()
		delay := homeBannerBackoff[min(b.Failures, len(homeBannerBackoff))-1]
		next = now.Add(delay)
		b.NextAt = next.UTC().Format(time.RFC3339)
	})
	log.Printf("publish: 首页轮播图（任务 #%d）没建成，稍后再试：%v", jobID, cause)
	return next
}

// homeBannerFailing 要找人的：连续失败到 homeBannerAlertAfter 次还在试，或 24 小时内放弃了的。
func homeBannerFailing(job publishJob, now time.Time) (bool, time.Time) {
	hb := job.HomeBanner
	if hb.open() && hb.Failures >= homeBannerAlertAfter {
		return true, time.Time{}
	}
	if hb.State != homeBannerFailed {
		return false, time.Time{}
	}
	finished, err := time.Parse(time.RFC3339, hb.FinishedAt)
	if err != nil {
		return true, time.Time{}
	}
	if clear := finished.Add(publishRecentFailureWindow); now.Before(clear) {
		return true, clear
	}
	return false, time.Time{}
}
