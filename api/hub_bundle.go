package main

// hub_bundle.go — 从 FBIF 中台读排版工具的配置包（GET /api/config/bundle/layout，One-Api 仓库 src/hub.ts）。
//
// 中台在内部工具服务器上，主力服务器经 SSH 通道在本机 127.0.0.1:13199 访问它（One-Api docs/hub/tools/tunnel-up.sh）。
// 包里有「平台账号」的几列（登录账号、密码不在包里）和「接口调用方」（排版接口的钥匙指纹，layout_callers.go）。
// 一条长等待循环（hub_feed.go）挂在中台上，换了版本才回 200：平台账号交给 distribution_accounts.go 校验、
// 写进 distribution-accounts.json，调用方名单留在这里由 layout_callers.go 现读。中台连不上就用最后一份。

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	hubBundleURLEnv = "HUB_BUNDLE_URL"
	hubKeyEnv       = "HUB_KEY"
)

type hubBundleClient struct {
	feed *hubFeed
	// onChange 配置包换了版本：平台账号（包里缺这一项是 nil）；first 是开机后拿到的第一版。
	onChange func(accounts []externalAccount, first bool)

	mu         sync.Mutex
	note       string // 最近一次没问成的原因，换了才记日志
	generation uint64 // 每换一版加一；0 是开机还没拿到过
	callers    []layoutCallerRow
	present    bool // 配置包里有「接口调用方」这一项
	// sourceAt 名单内容最近一次被来源确认的时刻：收到回包的时刻减 X-Hub-Source-Age，只往后走。
	// ageZero 上一次回包说来源刚确认过（年龄 0）：中台的飞书事件通道连着，来源一变旧中台会马上回话。
	sourceAt time.Time
	ageZero  bool
}

func (c *hubBundleClient) String() string {
	return c.feed.String()
}

// hubAccountRow 配置包 items.platform_accounts 的一行。
type hubAccountRow struct {
	Code         string `json:"code"`
	PlatformCode string `json:"platform_code"`
	Carrier      string `json:"carrier"`
	DisplayName  string `json:"display_name"`
	Status       string `json:"status"`
	Source       string `json:"source_wechat_account"`
	NotifyChat   string `json:"notify_chat"`
	Auto         bool   `json:"auto_distribute"`
}

// loadHubConfig 配置包地址和钥匙；错误信息只说缺哪个变量，绝不回显钥匙。
func loadHubConfig() (url, key string, err error) {
	url = strings.TrimSpace(os.Getenv(hubBundleURLEnv))
	key = strings.TrimSpace(os.Getenv(hubKeyEnv))
	if url == "" {
		return "", "", fmt.Errorf("中台未配置：缺少 %s", hubBundleURLEnv)
	}
	if !configuredHTTPBaseURL(url) {
		return "", "", fmt.Errorf("%s 必须是 HTTPS 地址（仅本机回环地址允许 HTTP）", hubBundleURLEnv)
	}
	if key == "" {
		return "", "", fmt.Errorf("中台未配置：缺少 %s", hubKeyEnv)
	}
	return url, key, nil
}

func newHubBundleClient(url, key string) *hubBundleClient {
	return &hubBundleClient{feed: newHubFeed(url, key)}
}

// LayoutCallers 配置包里最近一份接口调用方，和名单内容新到什么时候（约定第 5 条）：
// 上次回包来源年龄是 0、长等待请求正挂着，就是现在；挂着的请求断开了，新到断开那一刻；其余是 sourceAt。
func (c *hubBundleClient) LayoutCallers(now time.Time) layoutCallerSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	checkedAt := c.sourceAt
	if live := c.feed.liveAt(now); c.ageZero && live.After(checkedAt) {
		checkedAt = live
	}
	return layoutCallerSnapshot{
		Rows: append([]layoutCallerRow(nil), c.callers...), Present: c.present, Generation: c.generation, CheckedAt: checkedAt,
	}
}

// noteSource 按回包头续名单的核验时间。调用方持锁。
func (c *hubBundleClient) noteSource(header http.Header, receivedAt time.Time) {
	if at, ok := sourceCheckedAt(header, receivedAt); ok && at.After(c.sourceAt) {
		c.sourceAt = at
	}
	c.ageZero = strings.TrimSpace(header.Get("X-Hub-Source-Age")) == "0"
}

// sourceCheckedAt 这份内容最近一次被来源同步确认的时刻。中台给的是秒数（X-Hub-Source-Age）不是时刻，
// 从本机收到回包的时刻往回减，两台机器的钟差不进来。没带或读不懂就不续。
func sourceCheckedAt(header http.Header, receivedAt time.Time) (time.Time, bool) {
	seconds, err := strconv.ParseInt(header.Get("X-Hub-Source-Age"), 10, 64)
	if err != nil || seconds < 0 || seconds > math.MaxInt64/int64(time.Second) {
		return time.Time{}, false
	}
	return receivedAt.Add(-time.Duration(seconds) * time.Second), true
}

func (c *hubBundleClient) say(note string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case note != "" && note != c.note:
		log.Printf("publish: %s", note)
	case note == "" && c.note != "":
		log.Printf("publish: 中台配置包又问到了")
	}
	c.note = note
}

// poll 问一次中台，换了版本就换上。
func (c *hubBundleClient) poll(ctx context.Context) hubOutcome {
	resp, raw, err := c.feed.ask(ctx)
	if err != nil {
		c.say(fmt.Sprintf("连不上中台配置包，接着用上一份：%v", err))
		return hubFailed
	}
	receivedAt := c.feed.now()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotModified:
		c.mu.Lock()
		c.noteSource(resp.Header, receivedAt)
		c.mu.Unlock()
		c.say("")
		return hubReplied(resp)
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		log.Printf("publish: 中台不让这把钥匙读配置包（HTTP %d）：%s；5 分钟后再试，接着用上一份", resp.StatusCode, hubProblem(raw))
		return hubDenied
	default:
		c.say(fmt.Sprintf("中台读配置包失败（HTTP %d）：%s，接着用上一份", resp.StatusCode, hubProblem(raw)))
		return hubFailed
	}
	var body struct {
		Version int `json:"version"`
		Items   struct {
			PlatformAccounts []hubAccountRow   `json:"platform_accounts"`
			APICallers       []layoutCallerRow `json:"api_callers"`
		} `json:"items"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		c.say("中台配置包无法解析，接着用上一份")
		return hubFailed
	}
	changed := c.feed.setETag(resp.Header.Get("ETag"))
	c.mu.Lock()
	first := c.generation == 0
	if changed {
		c.generation++
		c.callers, c.present = body.Items.APICallers, body.Items.APICallers != nil
	}
	c.noteSource(resp.Header, receivedAt)
	c.mu.Unlock()
	c.say("")
	if !changed {
		return hubReplied(resp)
	}
	log.Printf("publish: 中台配置包换到第 %d 版", body.Version)
	for _, warning := range body.Warnings {
		log.Printf("publish: 中台配置包提醒：%s", warning)
	}
	if c.onChange != nil {
		var accounts []externalAccount
		if body.Items.PlatformAccounts != nil {
			accounts = make([]externalAccount, 0, len(body.Items.PlatformAccounts))
			for _, row := range body.Items.PlatformAccounts {
				accounts = append(accounts, externalAccount{
					ID: row.Code, Platform: row.PlatformCode, Carrier: row.Carrier, Name: row.DisplayName, Status: row.Status,
					Source: row.Source, NotifyChat: row.NotifyChat, Auto: row.Auto,
				})
			}
		}
		c.onChange(accounts, first)
	}
	return hubReplied(resp)
}

// hubProblem 中台错误回包里的 message。
func hubProblem(raw []byte) string {
	var problem struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &problem)
	return problem.Message
}

var layoutHub struct {
	once   sync.Once
	client *hubBundleClient
	err    error
}

// layoutHubClient 进程里唯一的配置包客户端：外发账号和接口调用方共用。
func layoutHubClient() (*hubBundleClient, error) {
	layoutHub.once.Do(func() {
		url, key, err := loadHubConfig()
		if err != nil {
			layoutHub.err = err
			return
		}
		layoutHub.client = newHubBundleClient(url, key)
	})
	return layoutHub.client, layoutHub.err
}
