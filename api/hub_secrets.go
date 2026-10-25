package main

// hub_secrets.go — MiMo 密钥从 FBIF 中台的密钥端点取（GET /api/config/secrets/layout，One-Api 仓库 src/hub-secrets.ts）。
//
// 配了 HUB_BUNDLE_URL 就只用中台发的 xiaomi-mimo：只放内存，换上新版本就回执（POST …/receipts）；
// 只发往这把密钥绑定的地址（origin + /v1），请求不跟跳转；env 里的 MIMO_API_KEY、MIMO_BASE_URL 不再用，也不顶上。
// 每分钟最多问一次中台；连不上沿用上一次取到的，最多 1 小时；中台明确拒绝（401/403、不发这把）立刻作废。
// 没配 HUB_BUNDLE_URL（本机开发、测试）照旧读 env。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	hubSecretsEvery = time.Minute
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
	url  string
	key  string
	http *http.Client
	now  func() time.Time

	refreshMu sync.Mutex // 同一时刻只问一次中台
	mu        sync.Mutex
	cred      *mimoCredential
	version   string
	gotAt     time.Time // 上次成功取到的时间
	askedAt   time.Time // 上次问中台的时间
	adopted   map[string]bool
	note      string
}

func (c *hubSecretsClient) String() string {
	return fmt.Sprintf("{URL:%q Key:[REDACTED]}", c.url)
}

func newHubSecretsClient(bundleURL, key string) *hubSecretsClient {
	return &hubSecretsClient{
		url: strings.Replace(bundleURL, "/config/bundle/", "/config/secrets/", 1), key: key, now: time.Now, adopted: map[string]bool{},
		http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

// httpsOrigin 只认 https、不带账号、路径、查询、片段的地址，回规范后的 origin；和中台 hub-secrets.ts 的 httpsOrigin 同一套。
func httpsOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return "https://" + strings.ToLower(u.Host)
}

// Credential 这次调用用的密钥；没有回 nil。
func (c *hubSecretsClient) Credential(ctx context.Context) *mimoCredential {
	c.mu.Lock()
	due := c.now().Sub(c.askedAt) >= hubSecretsEvery
	c.mu.Unlock()
	if due {
		c.refresh(ctx)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cred == nil || c.now().Sub(c.gotAt) > hubSecretsGrace {
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

func (c *hubSecretsClient) refresh(ctx context.Context) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	c.mu.Lock()
	if c.now().Sub(c.askedAt) < hubSecretsEvery {
		c.mu.Unlock()
		return
	}
	c.askedAt = c.now()
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		c.mu.Lock()
		c.say("连不上中台密钥端点，沿用上一次取到的 MiMo 密钥（最多 1 小时）")
		c.mu.Unlock()
		return
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, hubMaxResponse+1))
	if version := c.apply(resp.StatusCode, raw, err); version != "" && c.receipt(ctx, version) {
		c.mu.Lock()
		c.adopted[version] = true
		c.mu.Unlock()
	}
}

// apply 按中台的回包换上或作废密钥；回还没回执过的版本。
func (c *hubSecretsClient) apply(status int, raw []byte, err error) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		c.cred, c.version = nil, ""
		c.say(fmt.Sprintf("中台不给这把钥匙发密钥（HTTP %d），AI 预填和风险检查改走兜底", status))
		return ""
	}
	if err != nil || len(raw) > hubMaxResponse || status != http.StatusOK {
		c.say(fmt.Sprintf("中台密钥端点出错（HTTP %d），沿用上一次取到的 MiMo 密钥（最多 1 小时）", status))
		return ""
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
		c.say("中台密钥端点的回包无法解析，沿用上一次取到的 MiMo 密钥（最多 1 小时）")
		return ""
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
		return ""
	}
	if got.Version != c.version {
		log.Printf("publish: 中台密钥 %s 换到版本 %s", hubMimoSecret, got.Version)
	}
	c.cred, c.version, c.gotAt, c.note = &mimoCredential{key: strings.TrimSpace(got.Value), origin: origin}, got.Version, c.now(), ""
	if c.adopted[got.Version] {
		return ""
	}
	return got.Version
}

// receipt 告诉中台这一版已经换上；失败下次再报。
func (c *hubSecretsClient) receipt(ctx context.Context, version string) bool {
	payload, _ := json.Marshal(map[string]string{"code": hubMimoSecret, "version": version})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/receipts", bytes.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
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
		bundle, err := loadHubBundleClient()
		if err != nil {
			layoutHubSecrets.err = err
			return
		}
		layoutHubSecrets.client = newHubSecretsClient(bundle.url, bundle.key)
	})
	return layoutHubSecrets.client, true, layoutHubSecrets.err
}
