package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 外发账号只在多维表格「平台账号」里，平台限制只在分发服务的平台文件里（2026-10-24 分发框架，
// docs/adr/2026-10-24-distribution-framework.md）。排版工具不直接读表（只走 HTTP、不共享存储）：
// 账号经 FBIF 中台的配置包读（hub_bundle.go，2026-10-25 起；中台换了版本才送来），平台限制经分发服务的
// GET /v1/platforms 读（distribution_platforms.go），各存一份在数据目录：连不上时接着用这份，卡片、改稿页
// 照常显示，推送在提交前失败。每个号的编号是表的 record_id，lane、改稿记录、卡片按钮、发给分发服务的 account 都用它。

const (
	externalAccountsFile = "distribution-accounts.json"

	// 表里「在用」的号才会被自动挑上；「测试」号只在指定时用（分发服务那边同一口径）。
	externalAccountLive    = "在用"
	externalCarrierArticle = "article"
)

type externalAccount struct {
	ID       string `json:"id"`
	Platform string `json:"platform"` // 分发服务的平台 id，平台限制见 distribution_platforms.go
	Carrier  string `json:"carrier"`  // article 文章、post 贴图
	Name     string `json:"name"`
	Status   string `json:"status"`
	// Source 这个号发哪个公众号的稿；NotifyChat 这类稿的卡片发到哪个群（还没用上）；Auto 表里「自动外发」。
	// 贴图号（carrier=post）排版工具一律不用：贴图线归贴图发布项目（2026-10-27）。
	Source     string `json:"source"`
	NotifyChat string `json:"notify_chat,omitempty"`
	Auto       bool   `json:"auto,omitempty"`
	// Login 分发服务器上这个号登着没有（中台送来的「服务器登录」：已登录、要登录；没送是空），LoginCheckedAt 是那次查的时间
	// （「最近检查」）。只用来判断因为没登录停着的 lane 能不能接着推（publish_external_retry.go）。
	Login          string    `json:"login,omitempty"`
	LoginCheckedAt time.Time `json:"login_checked_at,omitzero"`
	// Label 卡片、改稿页、提醒里叫它什么：平台名；同一平台不止一个号时用表里的账号名。不落盘，读出时算。
	Label string `json:"-"`
	// platform 取账号目录时一并记下的平台限制，一次操作里卡片、检查、推送都用这一份。不落盘。
	platform distributionPlatform
}

// label 卡片、改稿页、提醒里的叫法。
func (a externalAccount) label() string {
	return firstNonEmpty(strings.TrimSpace(a.Label), platformForAccount(a).Name, strings.TrimSpace(a.Name))
}

// distributionCache 一份清单（账号、平台限制）：先读本地那份，之后换上中台送来的（账号）或向分发服务取的（平台限制）；
// 网络上取到的、本地读出的都先校验，不对就不要，接着用上一份。
type distributionCache[T any] struct {
	mu          sync.RWMutex
	what        string // 日志与报错里叫它什么
	key         string // 本地文件里放清单的键
	path        string
	items       []T
	loaded      bool
	refreshedAt time.Time
	triedAt     time.Time // 上一次向来源取的时刻（成没成都算）
	lastErr     string
	fetch       func(context.Context) ([]T, error)
	validate    func([]T) error
}

var externalAccounts = &distributionCache[externalAccount]{
	what: "外发账号（多维表格「平台账号」）", key: "accounts", validate: validateExternalAccounts,
}

// startDistributionDirectory 先读本地那两份，趁任务恢复、后台 worker、HTTP 都还没起来就把账号与平台限制备好，
// 再起配置包的长等待循环：配置包换了版本就换上账号、取一次平台限制。没有本地账号时最多等中台 10 秒。
// 候选部署暂停态也照常取：只写这两份缓存，不碰任务。
func startDistributionDirectory(jobsPath string) {
	dir := filepath.Dir(jobsPath)
	externalAccounts.setSource(filepath.Join(dir, externalAccountsFile), nil)
	distributionPlatforms.setSource(filepath.Join(dir, platformsFile), fetchDistributionPlatforms)
	externalAccounts.prime()
	go watchDistributionPlatforms(!distributionPlatforms.prime())
	hub, err := layoutHubClient()
	if err != nil {
		log.Printf("publish: 不读中台配置包，外发账号只用本地那份：%v", err)
		return
	}
	hub.onChange = func(accounts []externalAccount, first bool) {
		if err := externalAccounts.store(accounts); err != nil {
			log.Printf("publish: 中台送来的%s不收，接着用上次的：%v", externalAccounts.what, err)
		}
		if !first {
			kickDistributionPlatforms()
		}
	}
	hub.feed.start(hub.poll)
	if _, err := externalAccounts.snapshot(); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		hub.feed.waitReady(ctx)
		cancel()
	}
}

func (d *distributionCache[T]) setSource(path string, fetch func(context.Context) ([]T, error)) {
	d.mu.Lock()
	d.path, d.fetch = path, fetch
	d.mu.Unlock()
}

// prime 启动时：先读本地那份；没有或读坏了、又有取的方法，就当场取一次（最多 10 秒）。回当场取过没有。
func (d *distributionCache[T]) prime() bool {
	if err := d.loadFile(); err != nil {
		log.Printf("publish: 读不到本地的%s（%s）：%v", d.what, d.path, err)
	}
	if _, err := d.snapshot(); err == nil || d.fetch == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.refresh(ctx); err != nil {
		log.Printf("publish: 启动时取%s失败：%v", d.what, err)
	}
	return true
}

func (d *distributionCache[T]) loadFile() error {
	d.mu.RLock()
	path, key := d.path, d.key
	d.mu.RUnlock()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	var items []T
	if err := json.Unmarshal(doc[key], &items); err != nil {
		return err
	}
	if err := d.accept(items); err != nil {
		return err
	}
	var refreshedAt string
	_ = json.Unmarshal(doc["refreshed_at"], &refreshedAt)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.items = items
	d.loaded = true
	d.refreshedAt, _ = time.Parse(time.RFC3339, refreshedAt)
	return nil
}

func (d *distributionCache[T]) refresh(ctx context.Context) error {
	d.mu.Lock()
	fetch := d.fetch
	d.triedAt = time.Now()
	d.mu.Unlock()
	if fetch == nil {
		return fmt.Errorf("没有取%s的方法", d.what)
	}
	items, err := fetch(ctx)
	if err != nil {
		d.mu.Lock()
		d.lastErr = err.Error()
		d.mu.Unlock()
		return err
	}
	return d.store(items)
}

// store 换上一份新取到的：校验过了才写本地文件、换进内存。
func (d *distributionCache[T]) store(items []T) error {
	err := d.accept(items)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		d.lastErr = err.Error()
		return err
	}
	now := time.Now().UTC()
	if d.path != "" {
		doc, _ := json.MarshalIndent(map[string]any{"refreshed_at": now.Format(time.RFC3339), d.key: items}, "", "  ")
		if writeErr := writeDistributionCacheFile(d.path, append(doc, '\n')); writeErr != nil {
			log.Printf("publish: %s写不进本地（%s）：%v", d.what, d.path, writeErr)
		}
	}
	d.items, d.loaded, d.refreshedAt, d.lastErr = items, true, now, ""
	return nil
}

// accept 网络上取到的、本地文件里读出的都过一遍：缺这一项或给了 null 不当「一个都没有」，整份不要、接着用上一份；
// 明确给了空数组才算没有。
func (d *distributionCache[T]) accept(items []T) error {
	if items == nil {
		return fmt.Errorf("取到的%s缺「%s」这一项", d.what, d.key)
	}
	return d.validate(items)
}

func (d *distributionCache[T]) snapshot() ([]T, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if !d.loaded {
		if d.lastErr != "" {
			return nil, fmt.Errorf("还没读到%s：%s", d.what, d.lastErr)
		}
		return nil, fmt.Errorf("还没读到%s", d.what)
	}
	return append([]T(nil), d.items...), nil
}

// idleFor 离上一次向来源取已过了 d 这么久（从没取过也算）。
func (d *distributionCache[T]) idleFor(wait time.Duration) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return time.Since(d.triedAt) >= wait
}

func (d *distributionCache[T]) status() (count int, refreshedAt time.Time, lastErr string) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.items), d.refreshedAt, d.lastErr
}

func validateExternalAccounts(accounts []externalAccount) error {
	seen := map[string]bool{}
	for i, account := range accounts {
		if !validExternalAccountID(account.ID) || strings.TrimSpace(account.Platform) == "" {
			return fmt.Errorf("取到的第 %d 个账号缺编号或平台", i+1)
		}
		if seen[account.ID] {
			return fmt.Errorf("取到了重复的账号编号 %s", account.ID)
		}
		seen[account.ID] = true
	}
	return nil
}

// validExternalAccountID 账号编号是表的 record_id。
func validExternalAccountID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// externalTargets 排版工具会往哪些号发文章：表里在用、载体是文章的号，按表里的顺序。
type externalTargets struct {
	Targets []externalAccount
}

func loadExternalTargets() (externalTargets, error) {
	accounts, err := externalAccounts.snapshot()
	if err != nil {
		return externalTargets{}, err
	}
	return externalTargetsFrom(accounts, distributionPlatformsByKey()), nil
}

// externalTargetsFrom 平台限制没拿到的号照样列出（卡片照常显示、旧 lane 照常认），推送前检查时才拦下。
func externalTargetsFrom(accounts []externalAccount, platforms map[string]distributionPlatform) externalTargets {
	var out externalTargets
	perPlatform := map[string]int{}
	for _, account := range accounts {
		if account.Carrier != externalCarrierArticle || account.Status != externalAccountLive {
			continue
		}
		account.platform = platforms[account.Platform]
		out.Targets = append(out.Targets, account)
		perPlatform[account.Platform]++
	}
	for i := range out.Targets {
		target := &out.Targets[i]
		target.Label = platformForAccount(*target).Name
		if perPlatform[target.Platform] > 1 && strings.TrimSpace(target.Name) != "" {
			target.Label = strings.TrimSpace(target.Name)
		}
	}
	return out
}

// forJob 这篇稿能发的号：表里「来源公众号」与这篇的公众号一致。
func (c externalTargets) forJob(job publishJob) []externalAccount {
	source := strings.TrimSpace(job.SourceName)
	var out []externalAccount
	for _, target := range c.Targets {
		if source != "" && strings.TrimSpace(target.Source) == source {
			out = append(out, target)
		}
	}
	return out
}

// target 按账号编号取在用的文章号（不限来源公众号：补链接、核对这些只认已有的 lane）。
func (c externalTargets) target(accountID string) (externalAccount, bool) {
	accountID = strings.TrimSpace(accountID)
	for _, target := range c.Targets {
		if accountID != "" && target.ID == accountID {
			return target, true
		}
	}
	return externalAccount{}, false
}

// jobTarget 这篇稿能发的号里按编号取一个（卡片按钮、改稿页提交带的就是它）。
func (c externalTargets) jobTarget(job publishJob, accountID string) (externalAccount, bool) {
	accountID = strings.TrimSpace(accountID)
	for _, target := range c.forJob(job) {
		if accountID != "" && target.ID == accountID {
			return target, true
		}
	}
	return externalAccount{}, false
}

// autoTargets 表里勾了「自动外发」的号；现在都没勾（2026-10-14 起暂停）。
func (c externalTargets) autoTargets() []externalAccount {
	var out []externalAccount
	for _, target := range c.Targets {
		if target.Auto {
			out = append(out, target)
		}
	}
	return out
}

func fetchDistributionPlatforms(ctx context.Context) ([]distributionPlatformInfo, error) {
	cfg, err := loadDistributionRobotConfig()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return newDistributionRobotClient(cfg).Platforms(ctx)
}

func writeDistributionCacheFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
