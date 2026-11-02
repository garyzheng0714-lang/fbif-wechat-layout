package main

// hub_secrets.go — MiMo 密钥从 FBIF 中台的密钥端点取（GET /api/config/secrets/layout，One-Api 仓库 src/hub-secrets.ts）。
//
// 配了 HUB_BUNDLE_URL 就只用中台发的 xiaomi-mimo：只放内存，换上新版本就回执（POST …/receipts）；
// 只发往这把密钥绑定的地址（origin + /v1），请求不跟跳转；env 里的 MIMO_API_KEY、MIMO_BASE_URL 不再用，也不顶上。
// 第一次用到时起一条长等待循环（hub_feed.go），换了密钥中台才回 200。长等待请求正挂着，或断开不到 1 小时，
// 都接着用上一次取到的；断开超过 1 小时才丢。中台明确拒绝（401/403、不发这把）立刻作废。
// 没配 HUB_BUNDLE_URL（本机开发、测试）照旧读 env。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	hubMimoSecret   = "xiaomi-mimo"
	hubSecretsGrace = time.Hour
)

var (
	errHubMimoMissing = errors.New("中台没发 MiMo 密钥")
	hubSecretVersion  = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

type mimoCredential struct {
	key, origin string
}

type hubSecretsClient struct {
	feed *hubFeed

	mu      sync.Mutex
	cred    *mimoCredential
	version string
	adopted map[string]bool
	note    string
}

func (c *hubSecretsClient) String() string {
	return c.feed.String()
}

func newHubSecretsClient(bundleURL, key string) *hubSecretsClient {
	return &hubSecretsClient{feed: newHubFeed(strings.Replace(bundleURL, "/config/bundle/", "/config/secrets/", 1), key), adopted: map[string]bool{}}
}

// httpsOrigin 只认 https、不带账号、路径、查询、片段的地址，回规范后的 origin；和中台 hub-secrets.ts 的 httpsOrigin 同一套。
func httpsOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return "https://" + strings.ToLower(u.Host)
}

// Credential 这次调用用的密钥；没有回 nil。开机第一轮还没问完就等它，最多等到 ctx 到点。
func (c *hubSecretsClient) Credential(ctx context.Context) *mimoCredential {
	c.feed.waitReady(ctx)
	now := c.feed.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cred == nil || now.Sub(c.feed.liveAt(now)) > hubSecretsGrace {
		return nil
	}
	cred := *c.cred
	return &cred
}

func (c *hubSecretsClient) say(note string) {
	if note != c.note {
		log.Printf("publish: %s", note)
		c.note = note
	}
}

// poll 问一次中台，换了版本就换上并回执。
func (c *hubSecretsClient) poll(ctx context.Context) hubOutcome {
	resp, raw, err := c.feed.ask(ctx)
	if err != nil {
		c.mu.Lock()
		c.say("连不上中台密钥端点，沿用上一次取到的 MiMo 密钥（断开最多 1 小时）")
		c.mu.Unlock()
		return hubFailed
	}
	if resp.StatusCode == http.StatusNotModified {
		return hubReplied(resp)
	}
	version, outcome := c.apply(resp.StatusCode, raw)
	if outcome != hubAnswered {
		return outcome
	}
	c.feed.setETag(resp.Header.Get("ETag"))
	if version != "" {
		receiptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if c.receipt(receiptCtx, version) {
			c.mu.Lock()
			c.adopted[version] = true
			c.mu.Unlock()
		}
		cancel()
	}
	return hubReplied(resp)
}

// apply 按中台的回包换上或作废密钥；回还没回执过的版本，和这一轮算回了话（hubAnswered）还是没问成。
func (c *hubSecretsClient) apply(status int, raw []byte) (string, hubOutcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		c.cred, c.version = nil, ""
		c.say(fmt.Sprintf("中台不给这把钥匙发密钥（HTTP %d），AI 预填和风险检查改走兜底；5 分钟后再问", status))
		return "", hubDenied
	case status == http.StatusNotFound:
		c.say("中台密钥端点没有这个系统（HTTP 404），沿用上一次取到的 MiMo 密钥（断开最多 1 小时）；5 分钟后再问")
		return "", hubDenied
	case status != http.StatusOK:
		c.say(fmt.Sprintf("中台密钥端点出错（HTTP %d），沿用上一次取到的 MiMo 密钥（断开最多 1 小时）", status))
		return "", hubFailed
	}
	var body struct {
		Secrets map[string]struct {
			Value   string `json:"value"`
			Origin  string `json:"origin"`
			Version string `json:"version"`
		} `json:"secrets"`
		Withheld []struct {
			Code   string `json:"code"`
			Reason string `json:"reason"`
		} `json:"withheld"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Secrets == nil {
		c.say("中台密钥端点的回包无法解析，沿用上一次取到的 MiMo 密钥（断开最多 1 小时）")
		return "", hubFailed
	}
	got, ok := body.Secrets[hubMimoSecret]
	origin := httpsOrigin(got.Origin)
	if !ok || strings.TrimSpace(got.Value) == "" || origin == "" || !hubSecretVersion.MatchString(got.Version) {
		c.cred, c.version = nil, ""
		reason := "没发"
		for _, w := range body.Withheld {
			if w.Code == hubMimoSecret {
				reason = w.Reason
			}
		}
		c.say(fmt.Sprintf("中台不发 MiMo 密钥（%s），AI 预填和风险检查改走兜底", reason))
		return "", hubAnswered
	}
	if got.Version != c.version {
		log.Printf("publish: 中台密钥 %s 换到版本 %s", hubMimoSecret, got.Version)
	}
	c.cred, c.version, c.note = &mimoCredential{key: strings.TrimSpace(got.Value), origin: origin}, got.Version, ""
	if c.adopted[got.Version] {
		return "", hubAnswered
	}
	return got.Version, hubAnswered
}

// receipt 告诉中台这一版已经换上；失败下次再报。
func (c *hubSecretsClient) receipt(ctx context.Context, version string) bool {
	payload, _ := json.Marshal(map[string]string{"code": hubMimoSecret, "version": version})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.feed.url+"/receipts", bytes.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+c.feed.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.feed.http.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode/100 == 2
}

var layoutHubSecrets struct {
	once   sync.Once
	client *hubSecretsClient
	err    error
}

// hubSecrets 配了 HUB_BUNDLE_URL 才算接了中台；接了但配错（缺钥匙、地址不对）回错误，不退回 env。
func hubSecrets() (*hubSecretsClient, bool, error) {
	if strings.TrimSpace(os.Getenv(hubBundleURLEnv)) == "" {
		return nil, false, nil
	}
	layoutHubSecrets.once.Do(func() {
		url, key, err := loadHubConfig()
		if err != nil {
			layoutHubSecrets.err = err
			return
		}
		client := newHubSecretsClient(url, key)
		client.feed.start(client.poll)
		layoutHubSecrets.client = client
	})
	return layoutHubSecrets.client, true, layoutHubSecrets.err
}
