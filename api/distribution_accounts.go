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

// 外发账号只在多维表格「平台账号」里（2026-10-24 分发框架，docs/adr/2026-10-24-distribution-framework.md）。
// 排版工具不直接读表（两个系统只走 HTTP、不共享存储），经分发服务的 GET /v1/accounts 读，存一份在
// 数据目录 distribution-accounts.json：分发服务连不上时接着用这份，卡片、改稿页照常显示，推送在提交前失败。
// 每个号的编号是表的 record_id，lane、改稿记录、卡片按钮、发给分发服务的 account 都用它。

const (
	externalAccountsFile         = "distribution-accounts.json"
	externalAccountsRefreshEvery = 5 * time.Minute

	// 表里「在用」的号才会被自动挑上；「测试」号只在指定时用（分发服务那边同一口径）。
	externalAccountLive    = "在用"
	externalCarrierArticle = "article"
)

type externalAccount struct {
	ID       string `json:"id"`
	Platform string `json:"platform"` // 分发服务的平台 id，选 yunsuo_platform_profiles.go 里的平台规则
	Carrier  string `json:"carrier"`  // article 文章、post 贴图
	Name     string `json:"name"`
	Status   string `json:"status"`
	// Source 这个号发哪个公众号的稿；NotifyChat 这类稿的卡片发到哪个群（贴图线接上后用）；Auto 表里「自动外发」。
	Source     string `json:"source"`
	NotifyChat string `json:"notify_chat,omitempty"`
	Auto       bool   `json:"auto,omitempty"`
	// Label 卡片、改稿页、提醒里叫它什么：平台名；同一平台不止一个号时用表里的账号名。不落盘，读出时算。
	Label string `json:"-"`
}

// label 卡片、改稿页、提醒里的叫法。
func (a externalAccount) label() string {
	return firstNonEmpty(strings.TrimSpace(a.Label), yunsuoProfileForTarget(a).Name, strings.TrimSpace(a.Name))
}

type externalAccountDirectory struct {
	mu          sync.RWMutex
	path        string
	accounts    []externalAccount
	loaded      bool
	refreshedAt time.Time
	lastErr     string
	fetch       func(context.Context) ([]externalAccount, error)
}

var externalAccounts = &externalAccountDirectory{}

type externalAccountsFileDoc struct {
	RefreshedAt string            `json:"refreshed_at"`
	Accounts    []externalAccount `json:"accounts"`
}

// startExternalAccountDirectory 先读本地那份（没有就当场向分发服务取一次），趁任务恢复、后台 worker、HTTP 都还没起来
// 交给 adopt 把旧记录改挂过来（publish_external_accounts_migration.go），之后每 5 分钟取一次。
// 候选部署暂停态也照常取：只写这份缓存，不碰任务（adopt 自己在暂停态不改）。
func startExternalAccountDirectory(path string, adopt func([]externalAccount) error) {
	d := externalAccounts
	d.mu.Lock()
	d.path = path
	d.fetch = fetchDistributionAccounts
	d.mu.Unlock()
	d.prime(adopt)
	go func() {
		for {
			if err := d.refresh(context.Background()); err != nil {
				log.Printf("publish: 向分发服务取外发账号失败，接着用上次的：%v", err)
			}
			time.Sleep(externalAccountsRefreshEvery)
		}
	}()
}

// prime 启动时：先读本地那份，没有就当场向分发服务取一次（最多 10 秒），读到了交给 adopt。
func (d *externalAccountDirectory) prime(adopt func([]externalAccount) error) {
	if err := d.loadFile(); err != nil {
		log.Printf("publish: 读不到本地的外发账号（%s）：%v", d.path, err)
	}
	if _, err := d.snapshot(); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := d.refresh(ctx); err != nil {
			log.Printf("publish: 启动时向分发服务取外发账号失败：%v", err)
		}
		cancel()
	}
	if accounts, err := d.snapshot(); err == nil && adopt != nil {
		if err := adopt(accounts); err != nil {
			log.Printf("publish: 外发旧记录改挂失败，这些稿在对应平台上先不推，下次启动再改：%v", err)
		}
	}
}

func (d *externalAccountDirectory) loadFile() error {
	d.mu.RLock()
	path := d.path
	d.mu.RUnlock()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc externalAccountsFileDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.accounts = doc.Accounts
	d.loaded = true
	d.refreshedAt, _ = time.Parse(time.RFC3339, doc.RefreshedAt)
	return nil
}

func (d *externalAccountDirectory) refresh(ctx context.Context) error {
	d.mu.RLock()
	fetch, path := d.fetch, d.path
	d.mu.RUnlock()
	if fetch == nil {
		return errors.New("没有取账号的方法")
	}
	accounts, err := fetch(ctx)
	if err == nil {
		err = validateExternalAccounts(accounts)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		d.lastErr = err.Error()
		return err
	}
	now := time.Now().UTC()
	if path != "" {
		doc, _ := json.MarshalIndent(externalAccountsFileDoc{RefreshedAt: now.Format(time.RFC3339), Accounts: accounts}, "", "  ")
		if writeErr := writeExternalAccountsFile(path, append(doc, '\n')); writeErr != nil {
			log.Printf("publish: 外发账号写不进本地（%s）：%v", path, writeErr)
		}
	}
	d.accounts, d.loaded, d.refreshedAt, d.lastErr = accounts, true, now, ""
	return nil
}

func validateExternalAccounts(accounts []externalAccount) error {
	seen := map[string]bool{}
	for i, account := range accounts {
		if !validExternalAccountID(account.ID) || strings.TrimSpace(account.Platform) == "" {
			return fmt.Errorf("分发服务返回的第 %d 个账号缺编号或平台", i+1)
		}
		if seen[account.ID] {
			return fmt.Errorf("分发服务返回了重复的账号编号 %s", account.ID)
		}
		seen[account.ID] = true
	}
	return nil
}

// validExternalAccountID 与分发服务的账号编号同一口径（表的 record_id；没配表时是平台 id）。
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

func (d *externalAccountDirectory) snapshot() ([]externalAccount, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if !d.loaded {
		if d.lastErr != "" {
			return nil, fmt.Errorf("还没读到外发账号（多维表格「平台账号」）：%s", d.lastErr)
		}
		return nil, errors.New("还没读到外发账号（多维表格「平台账号」）")
	}
	return append([]externalAccount(nil), d.accounts...), nil
}

func (d *externalAccountDirectory) status() (count int, refreshedAt time.Time, lastErr string) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.accounts), d.refreshedAt, d.lastErr
}

// externalTargets 排版工具会往哪些号发文章：表里在用、载体是文章、排版工具认得这个平台的号，按表里的顺序。
type externalTargets struct {
	Targets []externalAccount
}

func loadExternalTargets() (externalTargets, error) {
	accounts, err := externalAccounts.snapshot()
	if err != nil {
		return externalTargets{}, err
	}
	return externalTargetsFrom(accounts), nil
}

func externalTargetsFrom(accounts []externalAccount) externalTargets {
	var out externalTargets
	perPlatform := map[string]int{}
	for _, account := range accounts {
		if account.Carrier != externalCarrierArticle || account.Status != externalAccountLive {
			continue
		}
		if _, ok := yunsuoPlatformProfiles[account.Platform]; !ok {
			continue
		}
		out.Targets = append(out.Targets, account)
		perPlatform[account.Platform]++
	}
	for i := range out.Targets {
		target := &out.Targets[i]
		target.Label = yunsuoProfileForTarget(*target).Name
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

func fetchDistributionAccounts(ctx context.Context) ([]externalAccount, error) {
	cfg, err := loadDistributionRobotConfig()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return newDistributionRobotClient(cfg).Accounts(ctx)
}

func externalAccountsPath(jobsPath string) string {
	return filepath.Join(filepath.Dir(jobsPath), externalAccountsFile)
}

func writeExternalAccountsFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".distribution-accounts-*.tmp")
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
