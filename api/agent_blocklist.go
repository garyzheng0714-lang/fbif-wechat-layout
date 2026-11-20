// agent_blocklist.go — 智能体用的停用名单（离职、停用的人）。
//
// 浏览器里的人由入口（登录中心）挡，本项目只读 X-FBIF-User；智能体拿的是本项目发的令牌（/mcp、/oauth/token），
// 不经入口，所以停用名单要本项目自己拉、每次调用自己查。和换入口前登录模板里那一份同一个来源、同一套规矩：
//   - 问中台 POST <SSO_HUB_URL>/sso/blocklist（Bearer SSO_HUB_KEY），中台代读集成平台、全中台合成一次；
//   - 启动拉一次，之后每 SSO_RECONCILE_SECONDS 一轮（默认 6 小时，按 10 分钟～24 小时算）；一轮最多试 4 次、
//     5 秒起翻倍退避，都没成 5 分钟后再来一轮，直到成功；这期间用上次落盘的名单；
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

	mu    sync.RWMutex
	state blocklistState
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

// Start 在后台拉名单：先拉一次，之后每 every 一轮，ctx 取消就停。
func (b *agentBlocklist) Start(ctx context.Context) {
	if b == nil || b.hubURL == "" {
		return
	}
	go func() {
		for {
			wait := b.every
			if err := b.syncWithRetry(ctx); err != nil {
				log.Printf("agent-blocklist: 这一轮没拉到（%v），%s 后再来，先用上次的名单", err, b.retry)
				wait = min(b.retry, b.every)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}()
}

func (b *agentBlocklist) syncWithRetry(ctx context.Context) error {
	var err error
	for attempt := range blocklistAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(b.retry / 60 << (attempt - 1)):
			}
		}
		if err = b.syncOnce(ctx); err == nil {
			return nil
		}
		log.Printf("agent-blocklist: 第 %d 次拉取：%v", attempt+1, err)
	}
	return err
}

// syncOnce 问中台要一份名单（带 {}：中台 10 分钟内拉过就给那份），校验、落盘，再换到内存。
func (b *agentBlocklist) syncOnce(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, blocklistTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.hubURL+"/sso/blocklist", bytes.NewReader([]byte("{}")))
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
