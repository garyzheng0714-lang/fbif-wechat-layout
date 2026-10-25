package main

// hub_bundle.go — 从 FBIF 中台读排版工具的配置包（GET /api/config/bundle/layout，One-Api 仓库 src/hub.ts）。
//
// 中台在内部工具服务器上，主力服务器经 SSH 通道在本机 127.0.0.1:13199 访问它（One-Api docs/hub/tools/tunnel-up.sh）。
// 包里只有「平台账号」的几列，登录账号、密码不在包里。每 60 秒带上一版的 ETag 问一次，没变中台回 304；
// 取到的账号照旧过 distributionCache 的校验、写进 distribution-accounts.json，中台连不上就用那一份。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	hubBundleURLEnv = "HUB_BUNDLE_URL"
	hubKeyEnv       = "HUB_KEY"

	hubRefreshEvery = time.Minute
	hubMaxResponse  = 1 << 20
)

type hubBundleClient struct {
	url  string
	key  string
	http *http.Client

	mu       sync.Mutex
	etag     string
	accounts []externalAccount
}

func (c *hubBundleClient) String() string {
	return fmt.Sprintf("{URL:%q Key:[REDACTED]}", c.url)
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

// loadHubBundleClient 错误信息只说缺哪个变量，绝不回显钥匙。
func loadHubBundleClient() (*hubBundleClient, error) {
	url := strings.TrimSpace(os.Getenv(hubBundleURLEnv))
	key := strings.TrimSpace(os.Getenv(hubKeyEnv))
	if url == "" {
		return nil, fmt.Errorf("中台未配置：缺少 %s", hubBundleURLEnv)
	}
	if !configuredHTTPBaseURL(url) {
		return nil, fmt.Errorf("%s 必须是 HTTPS 地址（仅本机回环地址允许 HTTP）", hubBundleURLEnv)
	}
	if key == "" {
		return nil, fmt.Errorf("中台未配置：缺少 %s", hubKeyEnv)
	}
	return newHubBundleClient(url, key), nil
}

func newHubBundleClient(url, key string) *hubBundleClient {
	return &hubBundleClient{url: url, key: key, http: &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Accounts 配置包里的平台账号。没变（304）就回上一次的那份；包里缺这一项回 nil，由缓存整份不收。
func (c *hubBundleClient) Accounts(ctx context.Context) ([]externalAccount, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	c.mu.Lock()
	if c.etag != "" {
		req.Header.Set("If-None-Match", c.etag)
	}
	c.mu.Unlock()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上中台：%w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, hubMaxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("读中台配置包失败：%w", err)
	}
	if len(raw) > hubMaxResponse {
		return nil, errors.New("中台配置包太大")
	}
	if resp.StatusCode == http.StatusNotModified {
		c.mu.Lock()
		defer c.mu.Unlock()
		return append([]externalAccount(nil), c.accounts...), nil
	}
	if resp.StatusCode != http.StatusOK {
		var problem struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &problem)
		return nil, fmt.Errorf("中台读配置包失败（HTTP %d）：%s", resp.StatusCode, problem.Message)
	}
	var body struct {
		Version int `json:"version"`
		Items   struct {
			PlatformAccounts []hubAccountRow `json:"platform_accounts"`
		} `json:"items"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, errors.New("中台配置包无法解析")
	}
	if body.Items.PlatformAccounts == nil {
		return nil, nil
	}
	accounts := make([]externalAccount, 0, len(body.Items.PlatformAccounts))
	for _, row := range body.Items.PlatformAccounts {
		accounts = append(accounts, externalAccount{
			ID: row.Code, Platform: row.PlatformCode, Carrier: row.Carrier, Name: row.DisplayName, Status: row.Status,
			Source: row.Source, NotifyChat: row.NotifyChat, Auto: row.Auto,
		})
	}
	c.mu.Lock()
	c.etag, c.accounts = resp.Header.Get("ETag"), accounts
	c.mu.Unlock()
	log.Printf("publish: 中台配置包换到第 %d 版", body.Version)
	for _, warning := range body.Warnings {
		log.Printf("publish: 中台配置包提醒：%s", warning)
	}
	return append([]externalAccount(nil), accounts...), nil
}

var layoutHub struct {
	once   sync.Once
	client *hubBundleClient
	err    error
}

func fetchExternalAccounts(ctx context.Context) ([]externalAccount, error) {
	layoutHub.once.Do(func() { layoutHub.client, layoutHub.err = loadHubBundleClient() })
	if layoutHub.err != nil {
		return nil, layoutHub.err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return layoutHub.client.Accounts(ctx)
}
