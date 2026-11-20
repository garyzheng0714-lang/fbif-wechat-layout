// agent_blocklist.go — 智能体用的停用名单（离职、停用的人）。
//
// 浏览器里的人由入口（登录中心）挡，本项目只读 X-FBIF-User；智能体拿的是本项目发的令牌（/mcp、/oauth/token），
// 不经入口，所以停用名单要本项目自己拉、每次调用自己查。和换入口前登录模板里那一份同一个来源、同一套规矩：
//   - 问中台 POST <SSO_HUB_URL>/sso/blocklist（Bearer SSO_HUB_KEY），中台代读集成平台、全中台合成一次；
//   - 启动拉一次，之后每 SSO_RECONCILE_SECONDS 一轮（默认 6 小时，按 10 分钟～24 小时算）；一轮最多试 4 次、
//     5 秒起翻倍退避，都没成 5 分钟后再来一轮，直到成功；这期间用上次落盘的名单；
//   - 名单一变，集成平台给项目地址发 POST /internal/auth/blocklist-changed {revision}（只说变了、不带名单、不要钥匙）：
//     比手上的新就马上排一轮，带上这个版本问中台（中台跳过它 10 分钟的缓存去拉）；和定时那轮是同一个后台循环，
//     不并发拉，攒着的几次合成一次，两轮至少隔 30 秒（每次拉都算集成平台一次运行，假提醒再多也只多拉这么几次）；
//   - 从没拉到过、读不出来，一律不放（不按空名单放人）；中台回的名单没读全、有一条不对，整份不收；比落盘的旧不收。
//
// 名单落盘在用户库同目录的 agent-blocklist.json，重启照用。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	blocklistDefaultEvery = 6 * time.Hour
	blocklistMinEvery     = 10 * time.Minute
	blocklistMaxEvery     = 24 * time.Hour
	blocklistAttempts     = 4
	blocklistRetry        = 5 * time.Minute
	blocklistMinGap       = 30 * time.Second
	// 中台那头最多等 70 秒（全中台合并拉取，两次至少隔 30 秒），这一路给 90 秒。
	blocklistTimeout = 90 * time.Second
	maxSafeInteger   = 1<<53 - 1
)

var (
	blocklistHubURLFormat = regexp.MustCompile(`^(?:http://(?:127\.0\.0\.1|localhost|\[::1\]):[0-9]{1,5}|https://[^/?#@\s\v\pZ\x{FEFF}]+)/hub-auth/[a-z][a-z0-9-]{0,40}$`)
	blocklistHubKeyFormat = regexp.MustCompile(`^pqk_[A-Za-z0-9_-]{16,200}$`)
	// 调中台不跟跳转：钥匙只送到 SSO_HUB_URL。
	blocklistClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
)

type blockEntry struct {
	Reason string `json:"reason"`
	Since  string `json:"since,omitempty"`
}

type blocklistState struct {
	Synced   bool                  `json:"synced"`
	Revision int64                 `json:"revision"`
	SyncedAt time.Time             `json:"synced_at"`
	Entries  map[string]blockEntry `json:"entries"`
}

// agentBlocklist 是智能体每次调用查的停用名单。零值（没配中台）谁都不放。
type agentBlocklist struct {
	path   string
	hubURL string
	hubKey string
	every  time.Duration
	retry  time.Duration
	gap    time.Duration // 两轮至少隔这么久

	mu    sync.RWMutex
	state blocklistState

	wantMu sync.Mutex
	want   blocklistWant
	wake   chan struct{} // 容量 1：叫醒后台循环，叫几次都只排一轮
}

// blocklistWant 是攒着还没拉的名单请求。plain 是定时、启动那轮（带 {}，中台 10 分钟内拉过就给那份）；
// zero 是提醒带 0（要一份现拉的）；max 是提醒要的最低版本。
type blocklistWant struct {
	any, plain, zero bool
	max              int64
}

func (w blocklistWant) merge(o blocklistWant) blocklistWant {
	return blocklistWant{any: w.any || o.any, plain: w.plain || o.plain, zero: w.zero || o.zero, max: max(w.max, o.max)}
}

// body 是问中台带的：有 0 带 0，否则带最大的版本，只是定时就带 {}（和换入口前登录模板一样）。
func (w blocklistWant) body() []byte {
	switch {
	case w.zero:
		return []byte(`{"revision":0}`)
	case w.max > 0:
		return []byte(fmt.Sprintf(`{"revision":%d}`, w.max))
	}
	return []byte("{}")
}

// newAgentBlocklistFromEnv 读 SSO_HUB_URL、SSO_HUB_KEY、SSO_RECONCILE_SECONDS，读回上次落盘的名单。
// 中台没配或配错不挡服务启动（浏览器那边不靠它），只是智能体谁都用不了，启动日志写明。
func newAgentBlocklistFromEnv(path string) *agentBlocklist {
	every, note := blocklistInterval(os.Getenv("SSO_RECONCILE_SECONDS"))
	b := &agentBlocklist{
		path:   path,
		hubURL: strings.TrimSpace(os.Getenv("SSO_HUB_URL")),
		hubKey: strings.TrimSpace(os.Getenv("SSO_HUB_KEY")),
		every:  every,
		retry:  blocklistRetry,
		gap:    blocklistMinGap,
		wake:   make(chan struct{}, 1),
	}
	if raw, err := os.ReadFile(path); err == nil {
		var saved blocklistState
		if json.Unmarshal(raw, &saved) == nil && saved.Synced {
			b.state = saved
		} else {
			log.Printf("agent-blocklist: %s 读不懂，当作从没拉到过", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Printf("agent-blocklist: 读 %s：%v", path, err)
	}
	switch {
	case !blocklistHubURLFormat.MatchString(b.hubURL):
		log.Printf("agent-blocklist: SSO_HUB_URL 没配或不对，拉不到停用名单，智能体谁都用不了")
		b.hubURL = ""
	case !blocklistHubKeyFormat.MatchString(b.hubKey):
		log.Printf("agent-blocklist: SSO_HUB_KEY 没配或不对，拉不到停用名单，智能体谁都用不了")
		b.hubURL = ""
	default:
		log.Printf("agent-blocklist: 每 %ds 拉一次停用名单（SSO_RECONCILE_SECONDS%s）", every/time.Second, note)
	}
	return b
}

// Start 在后台拉名单：先拉一次，之后每 every 一轮，名单提醒（Changed）叫醒就提前拉；ctx 取消就停。
// 定时和提醒都在这一个循环里拉，不会两次同时问中台。
func (b *agentBlocklist) Start(ctx context.Context) {
	if b == nil || b.hubURL == "" {
		return
	}
	b.request(blocklistWant{any: true, plain: true})
	go b.loop(ctx)
}

func (b *agentBlocklist) loop(ctx context.Context) {
	timer := time.NewTimer(b.every)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			b.request(blocklistWant{any: true, plain: true})
			continue
		case <-b.wake:
		}
		b.wantMu.Lock()
		want := b.want
		b.want = blocklistWant{}
		b.wantMu.Unlock()
		if !want.any || b.alreadyHas(want) {
			continue
		}
		if err := b.syncWithRetry(ctx, want.body()); err != nil {
			wait := min(b.retry, b.every)
			log.Printf("agent-blocklist: 这一轮没拉到（%v），%s 后再来，先用上次的名单", err, wait)
			time.AfterFunc(wait, func() { b.request(want) })
		} else {
			timer.Reset(b.every)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(b.gap):
		}
	}
}

// request 把一个名单请求并进攒着的，叫醒后台循环（已经叫过就不再叫，只排一轮）。
func (b *agentBlocklist) request(w blocklistWant) {
	b.wantMu.Lock()
	b.want = b.want.merge(w)
	b.wantMu.Unlock()
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// alreadyHas 只是提醒、要的版本手上已经有了，就不用拉。
func (b *agentBlocklist) alreadyHas(w blocklistWant) bool {
	if w.plain || w.zero {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state.Synced && w.max <= b.state.Revision
}

// Changed 收集成平台「名单变了」的提醒（POST /internal/auth/blocklist-changed 的正文 {revision}，最多读 4KB）。
// 提醒不带名单、不要钥匙：假提醒最多让这里多问一次中台，名单只从中台读。比手上新的版本、或 0（要现拉的）才排一轮；
// 读不懂的提醒只在从没拉到过时排一轮。不等拉完、不回错。
func (b *agentBlocklist) Changed(body io.Reader) {
	if b == nil || b.hubURL == "" {
		return
	}
	var hint struct {
		Revision json.RawMessage `json:"revision"`
	}
	_ = json.NewDecoder(io.LimitReader(body, 4096)).Decode(&hint)
	var n float64
	valid := json.Unmarshal(hint.Revision, &n) == nil && n >= 0 && n <= maxSafeInteger && n == math.Trunc(n)
	b.mu.RLock()
	synced, local := b.state.Synced, b.state.Revision
	b.mu.RUnlock()
	switch {
	case valid && n == 0:
		b.request(blocklistWant{any: true, zero: true})
	case valid && (!synced || int64(n) > local):
		b.request(blocklistWant{any: true, max: int64(n)})
	case !valid && !synced:
		b.request(blocklistWant{any: true, plain: true})
	}
}

// handleBlocklistChanged 是提醒的入口：不过登录、不认 X-FBIF-User（programPathPrefixes），一律回 202。
func (s *authService) handleBlocklistChanged(w http.ResponseWriter, r *http.Request) {
	s.blocklist.Changed(r.Body)
	w.WriteHeader(http.StatusAccepted)
}

func (b *agentBlocklist) syncWithRetry(ctx context.Context, body []byte) error {
	var err error
	for attempt := range blocklistAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(b.retry / 60 << (attempt - 1)):
			}
		}
		if err = b.syncOnce(ctx, body); err == nil {
			return nil
		}
		log.Printf("agent-blocklist: 第 %d 次拉取：%v", attempt+1, err)
	}
	return err
}

// syncOnce 问中台要一份名单（body 见 blocklistWant.body），校验、落盘，再换到内存。
func (b *agentBlocklist) syncOnce(ctx context.Context, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, blocklistTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.hubURL+"/sso/blocklist", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.hubKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := blocklistClient.Do(req)
	if err != nil {
		return fmt.Errorf("hub sso/blocklist: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("hub sso/blocklist: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hub sso/blocklist: HTTP %d", resp.StatusCode)
	}
	next, err := parseBlocklist(raw)
	if err != nil {
		return err
	}
	return b.save(next)
}

// save 落盘再换内存；比手上的旧就不收（多个请求先后回来时晚到的旧名单不能盖掉新的）。
func (b *agentBlocklist) save(next blocklistState) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state.Synced && next.Revision < b.state.Revision {
		log.Printf("agent-blocklist: 中台回的版本 %d 比手上的 %d 旧，不收", next.Revision, b.state.Revision)
		return nil
	}
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if b.path != "" {
		if err := writeFileAtomic(b.path, data); err != nil {
			return fmt.Errorf("save blocklist: %w", err)
		}
	}
	b.state = next
	log.Printf("agent-blocklist: 版本 %d，停用 %d 人", next.Revision, len(next.Entries))
	return nil
}

// Check 这个飞书 union_id 能不能让智能体干活：名单从没拉到过不放，在名单上不放。
func (b *agentBlocklist) Check(unionID string) error {
	if b == nil {
		return errors.New("停用名单读不出来，过一会儿再试")
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.state.Synced {
		return errors.New("停用名单读不出来，过一会儿再试")
	}
	if _, blocked := b.state.Entries[unionID]; blocked {
		return errors.New("这个账号已停用")
	}
	return nil
}

// parseBlocklist 严格读中台回的名单，和中台的校验同一规则：没读全不收，有一条不对整份不收。
func parseBlocklist(raw []byte) (blocklistState, error) {
	malformed := errors.New("blocklist reply malformed")
	var b map[string]any
	if json.Unmarshal(raw, &b) != nil || b == nil {
		return blocklistState{}, malformed
	}
	revision, ok := safeInt(b["revision"])
	syncedAt, ok2 := safeInt(b["synced_at"])
	complete, ok3 := b["complete"].(bool)
	list, ok4 := b["entries"].([]any)
	if !ok || !ok2 || syncedAt == 0 || !ok3 || !ok4 {
		return blocklistState{}, malformed
	}
	if !complete {
		return blocklistState{}, errors.New("blocklist not complete, keeping the old one")
	}
	next := blocklistState{Synced: true, Revision: revision, SyncedAt: time.UnixMilli(syncedAt).UTC(), Entries: map[string]blockEntry{}}
	for _, v := range list {
		e, _ := v.(map[string]any)
		sub, ok := e["sub"].(string)
		reason, ok2 := e["reason"].(string)
		since, ok3 := e["since"].(string)
		if !ok || sub == "" || jsLen(sub) > 200 || !ok2 || jsLen(reason) > 500 || !ok3 || jsLen(since) > 100 {
			return blocklistState{}, malformed
		}
		next.Entries[sub] = blockEntry{Reason: reason, Since: since}
	}
	return next, nil
}

// blocklistInterval 把 SSO_RECONCILE_SECONDS 换成拉取间隔，note 接在启动日志的变量名后面。值是人在表里改的，
// 写错不能让服务起不来：看不懂用默认，超出 10 分钟～24 小时按边界算（每轮可能让中台拉一次集成平台，按运行次数计费）。
func blocklistInterval(raw string) (time.Duration, string) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return blocklistDefaultEvery, " not set, default"
	}
	n, err := strconv.ParseUint(v, 10, 64)
	switch {
	case err != nil || n == 0:
		return blocklistDefaultEvery, " is not a whole number of seconds, default"
	case n < uint64(blocklistMinEvery/time.Second):
		return blocklistMinEvery, " is under 10m, raised to it"
	case n > uint64(blocklistMaxEvery/time.Second):
		return blocklistMaxEvery, " is over 24h, lowered to it"
	}
	return time.Duration(n) * time.Second, ""
}

func safeInt(v any) (int64, bool) {
	n, ok := v.(float64)
	if !ok || n < 0 || n > maxSafeInteger || n != math.Trunc(n) {
		return 0, false
	}
	return int64(n), true
}

// jsLen 是字符串按 UTF-16 算的长度：中台是 Node，按这个数限长。
func jsLen(v string) int {
	n := 0
	for _, r := range v {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}
