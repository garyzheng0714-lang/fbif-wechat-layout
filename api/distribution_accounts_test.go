package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setTestExternalAccounts 测试里的「平台账号」：直接放进账号目录，测试结束还原。
func setTestExternalAccounts(t *testing.T, accounts ...externalAccount) {
	t.Helper()
	d := externalAccounts
	d.mu.Lock()
	prevAccounts, prevLoaded, prevErr := d.accounts, d.loaded, d.lastErr
	d.accounts, d.loaded, d.lastErr = accounts, true, ""
	d.mu.Unlock()
	t.Cleanup(func() {
		d.mu.Lock()
		d.accounts, d.loaded, d.lastErr = prevAccounts, prevLoaded, prevErr
		d.mu.Unlock()
	})
}

// testArticleAccount 发「FBIF食品饮料创新」文章的在用号。
func testArticleAccount(id, platform string, auto bool) externalAccount {
	return externalAccount{
		ID: id, Platform: platform, Carrier: externalCarrierArticle, Status: externalAccountLive,
		Name: yunsuoPlatformProfiles[platform].Name + " · FBIF", Source: "FBIF食品饮料创新", Auto: auto,
	}
}

// TestExternalTargetsComeFromAccountTable 2026-10-24 分发框架：账号只在多维表格「平台账号」里。
// 只挑在用、载体是文章、排版工具认得这个平台的号；每篇只发「来源公众号」是它的号；同一平台不止一个号时用账号名区分。
func TestExternalTargetsComeFromAccountTable(t *testing.T) {
	trial := testArticleAccount("recTrial", "toutiao", false)
	trial.Status, trial.Name = "测试", "头条号 · 黑桃游戏"
	post := externalAccount{ID: "recDouyin", Platform: "douyin", Carrier: "post", Status: externalAccountLive, Source: "FBIF食品创新展"}
	expo := testArticleAccount("recExpo", "toutiao", false)
	expo.Source, expo.Name = "FBIF食品创新展", "头条号 · 会展"
	setTestExternalAccounts(t, testArticleAccount("recBaijia", "baijiahao", false), testArticleAccount("recToutiao", "toutiao", false), trial, post, expo)

	cfg, err := loadExternalTargets()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, target := range cfg.Targets {
		ids = append(ids, target.ID)
	}
	if strings.Join(ids, ",") != "recBaijia,recToutiao,recExpo" {
		t.Fatalf("测试号、贴图号不该被挑上，按表里的顺序：%v", ids)
	}
	jobTargets := cfg.forJob(publishJob{SourceName: "FBIF食品饮料创新"})
	if len(jobTargets) != 2 || jobTargets[0].ID != "recBaijia" || jobTargets[1].ID != "recToutiao" {
		t.Fatalf("只发来源公众号是这篇的号：%+v", jobTargets)
	}
	if got := jobTargets[0].label(); got != "百家号" {
		t.Fatalf("平台只有一个号时用平台名，实际 %q", got)
	}
	if got := jobTargets[1].label(); got != "头条号 · FBIF" {
		t.Fatalf("同一平台有两个在用号时用账号名，实际 %q", got)
	}
	if len(cfg.forJob(publishJob{})) != 0 {
		t.Fatal("不知道公众号的稿不发任何号")
	}
	if _, ok := cfg.jobTarget(publishJob{SourceName: "FBIF食品饮料创新"}, "recExpo"); ok {
		t.Fatal("别的公众号的号不能从这篇的按钮推")
	}
}

// TestExternalAccountDirectoryKeepsLastGoodCopy 分发服务连不上时接着用上次读到的那份（本地文件），从没读到过才报错。
func TestExternalAccountDirectoryKeepsLastGoodCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), externalAccountsFile)
	fetched := []externalAccount{testArticleAccount("recToutiao", "toutiao", false)}
	var fetchErr error
	d := &externalAccountDirectory{path: path, fetch: func(context.Context) ([]externalAccount, error) { return fetched, fetchErr }}
	if _, err := d.snapshot(); err == nil {
		t.Fatal("从没读到过账号时应报错")
	}
	if err := d.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	fetchErr = errors.New("连不上")
	if err := d.refresh(context.Background()); err == nil {
		t.Fatal("取不到要报出来")
	}
	if accounts, err := d.snapshot(); err != nil || len(accounts) != 1 {
		t.Fatalf("取不到时接着用上次的：%v %v", accounts, err)
	}
	if _, refreshedAt, lastErr := d.status(); refreshedAt.IsZero() || lastErr == "" {
		t.Fatalf("readiness 要看得到上次读到的时间与这次的错：%v %q", refreshedAt, lastErr)
	}
	restarted := &externalAccountDirectory{path: path}
	if err := restarted.loadFile(); err != nil {
		t.Fatal(err)
	}
	if accounts, err := restarted.snapshot(); err != nil || len(accounts) != 1 || accounts[0].ID != "recToutiao" {
		t.Fatalf("重启后先用本地那份：%v %v", accounts, err)
	}
	fetched = []externalAccount{{ID: "rec bad", Platform: "toutiao"}}
	fetchErr = nil
	if err := d.refresh(context.Background()); err == nil {
		t.Fatal("编号不合规的账号整份不收")
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), "recToutiao") {
		t.Fatalf("不收的那份不写盘：%s", raw)
	}
}

// TestExternalAccountDirectoryPrimesBeforeWorkers 启动时先拿到一份账号：有本地那份就直接用，没有就当场取一次。
func TestExternalAccountDirectoryPrimesBeforeWorkers(t *testing.T) {
	path := filepath.Join(t.TempDir(), externalAccountsFile)
	fetches := 0
	var fetchErr error
	fetch := func(context.Context) ([]externalAccount, error) {
		fetches++
		return []externalAccount{testArticleAccount("recToutiao", "toutiao", false)}, fetchErr
	}

	fetchErr = errors.New("连不上")
	d := &externalAccountDirectory{path: path, fetch: fetch}
	d.prime()
	if _, err := d.snapshot(); fetches != 1 || err == nil {
		t.Fatalf("没有本地那份就当场取一次，取不到就没有账号：%d %v", fetches, err)
	}
	fetchErr = nil
	d = &externalAccountDirectory{path: path, fetch: fetch}
	d.prime()
	if got, err := d.snapshot(); fetches != 2 || err != nil || len(got) != 1 || got[0].ID != "recToutiao" {
		t.Fatalf("当场取到就能用：%d %v %v", fetches, got, err)
	}
	d = &externalAccountDirectory{path: path, fetch: fetch}
	d.prime()
	if got, err := d.snapshot(); fetches != 2 || err != nil || len(got) != 1 {
		t.Fatalf("有本地那份就不等分发服务：%d %v %v", fetches, got, err)
	}
}
