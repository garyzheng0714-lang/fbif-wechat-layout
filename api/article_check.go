package main

// article_check.go — 网页排版时的校对。
//
// 2026-11-17 审核群里 Dudu：「可以在排版工具的页面加上这个错别字或者风险核查吗？这样在排版公众号的时候就可以
// 直接先修改了。」用户：「prompt这些都用一样的，模型也是」。
//
// 与发布流水线同一套提示词、同一档模型：先做风险检查（publish_risk.go），挑出的确定条目再复核出唯一改法
// （publish_proofread.go）。差别只在入口：这里吃网页排好的标题和纯文字，不建发布单、不发卡片、不 @ 人。
//
// 接口：POST /api/article-check {title, text, have, retry}（GET 同一个地址只答这个环境接没接模型，网页据此出不出校对卡）。同一份标题和正文只查一次（按内容指纹认，
// 谁交都是同一个结果，留 2 小时）。模型要想一两分钟，一次请求最多等 25 秒（服务器读超时 30 秒，过了请求会被掐掉，
// 与飞书导出的长等待同一个上限）：没完就回进行到哪一步，网页带着已拿到的那一步（have）再交同一份内容接着等，
// 有变化当场回。不建任务号、不另设查询接口，进程重启了再交就是重查。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	articleCheckChecking  = "checking"  // 初检中
	articleCheckVerifying = "verifying" // 初检完了、条目已能看，正在复核改法
	articleCheckDone      = "done"
	articleCheckFailed    = "failed"
	// articleCheckOff 这个环境没接模型（本地、端到端测试）：不开查，网页不出校对卡。照常回 200——这不是出错，
	// 回 5xx 浏览器会在控制台记一条错误，端到端测试把它当页面问题。
	articleCheckOff = "off"

	articleCheckMaxBody    = 1 << 20          // 线上最长稿 3.8 万字约 114KB；超了回 413，不截断
	articleCheckWait       = 25 * time.Second // 见文件头：必须小于服务器读超时
	articleCheckRunTimeout = 10 * time.Minute // 两次模型调用，各自最长 300 秒
	articleCheckKeep       = 2 * time.Hour    // 查完的结果留多久
	articleCheckKeepFailed = time.Minute      // 没查成的留多久：这段时间里再交同一份不会自己重查，要人点重试
	articleCheckMaxJobs    = 300
	articleCheckParallel   = 3 // 同时在跑的检查；多的排队
)

// articleCheckItem 一条要人看的提示：哪类问题、原句、说明。
type articleCheckItem struct {
	Label    string `json:"label"`
	Level    string `json:"level"` // warning / block
	Evidence string `json:"evidence"`
	Reason   string `json:"reason"`
}

// articleCheckView 给网页的结果。Fixes 是改法唯一的确定错误（「X」应为「Y」）；Issues 是确定有问题、但要人
// 判断怎么改的（两处对不上、阻断级）；Notes 是拿不准的待核提示。
type articleCheckView struct {
	Status string                    `json:"status"`
	Fixes  []publishProofreadFinding `json:"fixes"`
	Issues []articleCheckItem        `json:"issues"`
	Notes  []articleCheckItem        `json:"notes"`
	Error  string                    `json:"error,omitempty"`
}

func articleCheckRank(status string) int {
	switch status {
	case articleCheckVerifying:
		return 1
	case articleCheckDone, articleCheckFailed:
		return 2
	}
	return 0
}

type articleCheckJob struct {
	view     articleCheckView
	changed  chan struct{} // 每进一步关掉、换新的：等着的请求被叫醒
	finished time.Time     // 零值是还在查
}

type articleChecker struct {
	mu    sync.Mutex
	jobs  map[string]*articleCheckJob
	slots chan struct{}
	// starts 每人新开检查的频率（再交同一份内容不算）。
	starts *requestCostGuard

	// available 模型现在能不能用（没配密钥的环境不能）：不能就不开查，回 articleCheckOff。
	available func(ctx context.Context) bool
	assess    func(ctx context.Context, title, body string) (publishRiskAssessment, error)
	verify    func(ctx context.Context, title, body string, candidates []publishRiskItem) (string, error)
	now       func() time.Time
	wait      time.Duration
}

func newArticleChecker() *articleChecker {
	return &articleChecker{
		jobs:   map[string]*articleCheckJob{},
		slots:  make(chan struct{}, articleCheckParallel),
		starts: newRequestCostGuard(40, 10*time.Minute, 64),
		available: func(ctx context.Context) bool {
			_, _, err := aiCredential(ctx)
			return err == nil
		},
		assess: func(ctx context.Context, title, body string) (publishRiskAssessment, error) {
			return aiAssessRiskText(ctx, title, body, "网页校对")
		},
		verify: aiVerifyProofreadText,
		now:    time.Now,
		wait:   articleCheckWait,
	}
}

var errArticleCheckTooFrequent = errors.New("校对太频繁，请稍后再试")

func articleCheckKey(title, text string) string {
	digest := sha256.Sum256([]byte(title + "\x00" + text))
	return hex.EncodeToString(digest[:])
}

// check 交一份标题和正文：没查过就开查，查过或正在查就接上。等到比 have 更进一步的结果、或等满 c.wait 就回。
// retry 只对没查成的起作用：丢掉那次重新查。
func (c *articleChecker) check(ctx context.Context, userID, title, text, have string, retry bool) (articleCheckView, error) {
	key := articleCheckKey(title, text)
	c.mu.Lock()
	c.dropExpiredLocked()
	job := c.jobs[key]
	if job != nil && retry && job.view.Status == articleCheckFailed {
		delete(c.jobs, key)
		job = nil
	}
	if job == nil {
		if !c.available(ctx) {
			c.mu.Unlock()
			return articleCheckView{Status: articleCheckOff, Fixes: []publishProofreadFinding{}, Issues: []articleCheckItem{}, Notes: []articleCheckItem{}}, nil
		}
		release, reason := c.starts.acquire(userID, c.now())
		if reason != costGuardAllowed {
			c.mu.Unlock()
			return articleCheckView{}, errArticleCheckTooFrequent
		}
		release()
		job = &articleCheckJob{changed: make(chan struct{}), view: articleCheckView{Status: articleCheckChecking,
			Fixes: []publishProofreadFinding{}, Issues: []articleCheckItem{}, Notes: []articleCheckItem{}}}
		c.jobs[key] = job
		go c.run(job, key, title, text)
	}
	c.mu.Unlock()

	deadline := time.NewTimer(c.wait)
	defer deadline.Stop()
	for {
		c.mu.Lock()
		view, changed := job.view, job.changed
		c.mu.Unlock()
		if articleCheckRank(view.Status) > articleCheckRank(have) {
			return view, nil
		}
		select {
		case <-changed:
		case <-deadline.C:
			return view, nil
		case <-ctx.Done():
			return view, nil
		}
	}
}

func (c *articleChecker) run(job *articleCheckJob, key, title, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), articleCheckRunTimeout)
	defer cancel()
	started := c.now()
	fail := func(step string, err error) {
		log.Printf("article-check: %s %s失败（%s）：%v", key[:12], step, c.now().Sub(started).Round(time.Second), err)
		c.advance(job, articleCheckView{Status: articleCheckFailed, Error: "校对没完成"})
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-ctx.Done():
		fail("排队", ctx.Err())
		return
	}
	risk, err := c.assess(ctx, title, text)
	if err != nil {
		fail("初检", err)
		return
	}
	candidates := publishProofreadCandidates(risk.Items)
	view := articleCheckView{Status: articleCheckDone}
	view.Issues, view.Notes = articleCheckLayers(risk.Items, nil)
	if len(candidates) > 0 {
		view.Status = articleCheckVerifying
		c.advance(job, view)
		raw, err := c.verify(ctx, title, text, candidates)
		if err != nil {
			fail("复核", err)
			return
		}
		fixes, from, err := parsePublishProofreadIndexed(raw, candidates, title+"\n"+text)
		if err != nil {
			fail("复核", err)
			return
		}
		view = articleCheckView{Status: articleCheckDone, Fixes: fixes}
		view.Issues, view.Notes = articleCheckLayers(risk.Items, from)
	}
	log.Printf("article-check: %s 查完（%s）：%d 处确定错误、%d 条要核对、%d 条待核",
		key[:12], c.now().Sub(started).Round(time.Second), len(view.Fixes), len(view.Issues), len(view.Notes))
	c.advance(job, view)
}

func (c *articleChecker) advance(job *articleCheckJob, view articleCheckView) {
	// 空列表回 []，网页不用判空。
	if view.Fixes == nil {
		view.Fixes = []publishProofreadFinding{}
	}
	if view.Issues == nil {
		view.Issues = []articleCheckItem{}
	}
	if view.Notes == nil {
		view.Notes = []articleCheckItem{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	job.view = view
	if articleCheckRank(view.Status) == 2 {
		job.finished = c.now()
	}
	close(job.changed)
	job.changed = make(chan struct{})
}

// dropExpiredLocked 清掉过期的结果；还超数就从最早查完的丢起，正在查的不动。
func (c *articleChecker) dropExpiredLocked() {
	now := c.now()
	var finished []string
	for key, job := range c.jobs {
		if job.finished.IsZero() {
			continue
		}
		keep := articleCheckKeep
		if job.view.Status == articleCheckFailed {
			keep = articleCheckKeepFailed
		}
		if now.Sub(job.finished) >= keep {
			delete(c.jobs, key)
			continue
		}
		finished = append(finished, key)
	}
	if over := len(c.jobs) - articleCheckMaxJobs; over > 0 {
		sort.Slice(finished, func(i, j int) bool {
			return c.jobs[finished[i]].finished.Before(c.jobs[finished[j]].finished)
		})
		for _, key := range finished[:min(over, len(finished))] {
			delete(c.jobs, key)
		}
	}
}

// articleCheckLayers 把风险条目分成「要核对」和「待核」两层；fixed 是已经变成确定错误的候选（第几条候选，从 0 数），不再列。
//
// 要核对 = 模型确定有问题的条目（与校对提醒同一个候选口径 publishProofreadCandidate）里没能给出唯一改法的，
// 加上阻断级的；阻断级排最前。其余都是待核。
func articleCheckLayers(items []publishRiskItem, fixed []int) (issues, notes []articleCheckItem) {
	done := map[int]bool{}
	for _, index := range fixed {
		done[index] = true
	}
	issues, notes = []articleCheckItem{}, []articleCheckItem{}
	blocks := []articleCheckItem{}
	candidate := -1
	for _, item := range items {
		view := articleCheckItem{
			Label:    publishRiskCategoryLabel(item.Category),
			Level:    item.Level,
			Evidence: strings.Join(strings.Fields(item.Evidence), " "),
			Reason:   strings.Join(strings.Fields(item.Reason), " "),
		}
		isCandidate := publishProofreadCandidate(item)
		if isCandidate {
			candidate++
			if done[candidate] {
				continue
			}
		}
		switch {
		case item.Level == publishRiskBlock:
			blocks = append(blocks, view)
		case isCandidate:
			issues = append(issues, view)
		default:
			notes = append(notes, view)
		}
	}
	return append(blocks, issues...), notes
}

// registerArticleCheckRoute 只给登录的同事在网页上用：要会话，开查还要同源。
func registerArticleCheckRoute(mux *http.ServeMux, auth *authService, checker *articleChecker) {
	// 这个环境接没接模型：没接（本地、端到端测试）网页就不出校对卡。
	mux.HandleFunc("GET /api/article-check", noStoreHandler(func(w http.ResponseWriter, r *http.Request) {
		if _, err := auth.currentUser(r); err != nil {
			writeAuthError(w, http.StatusUnauthorized, "authentication_required", "请先登录")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": checker.available(r.Context())})
	}))
	mux.HandleFunc("POST /api/article-check", noStoreHandler(func(w http.ResponseWriter, r *http.Request) {
		if status, code, message := expensiveSessionDenial(auth, r); status != 0 {
			writeAuthError(w, status, code, message)
			return
		}
		user, err := auth.currentUser(r)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "authentication_required", "请先登录")
			return
		}
		var req struct {
			Title string `json:"title"`
			Text  string `json:"text"`
			Have  string `json:"have"`
			Retry bool   `json:"retry"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, articleCheckMaxBody)).Decode(&req); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, "正文太长，校对不了")
				return
			}
			writeError(w, http.StatusBadRequest, "请求格式不对")
			return
		}
		title, text := strings.TrimSpace(req.Title), strings.TrimSpace(req.Text)
		if text == "" {
			writeError(w, http.StatusBadRequest, "没有正文")
			return
		}
		view, err := checker.check(r.Context(), user.ID, title, text, req.Have, req.Retry)
		if errors.Is(err, errArticleCheckTooFrequent) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, view)
	}))
}
