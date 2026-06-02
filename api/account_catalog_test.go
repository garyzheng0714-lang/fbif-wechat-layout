package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 意图：accountCatalog 是账号元数据唯一真相，生成的 registry.js 必须与仓库里 committed
// 的 public/js/rule-packs/registry.js 一致——否则有人改了 catalog 没跑 npm run gen:registry，
// 或反过来手改了生成文件，前后端账号身份就会漂移。
func TestGeneratedRegistryMatchesCommittedFile(t *testing.T) {
	committed, err := os.ReadFile(filepath.Join("..", "public", "js", "rule-packs", "registry.js"))
	if err != nil {
		t.Fatalf("read committed registry.js: %v", err)
	}
	got := renderRegistryJS()
	if string(committed) != got {
		t.Fatalf("生成的 registry.js 与仓库文件不一致——改了 accountCatalog 后请跑 `npm run gen:registry`。\n--- want(committed) 前 400 字 ---\n%s\n--- got(generated) 前 400 字 ---\n%s",
			head(string(committed), 400), head(got, 400))
	}
}

// 意图：写一个与 catalog 一致的 registry.js → 校验通过；制造 templateId 漂移 → 校验失败（fail loud）。
func TestValidateRegistryConsistency(t *testing.T) {
	dir := t.TempDir()
	rpDir := filepath.Join(dir, "js", "rule-packs")
	if err := os.MkdirAll(rpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	regPath := filepath.Join(rpDir, "registry.js")

	// 一致：写入由 catalog 生成的内容 → 无错。
	if err := os.WriteFile(regPath, []byte(renderRegistryJS()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateRegistryConsistency(dir); err != nil {
		t.Fatalf("一致的 registry.js 不应报错，却得到：%v", err)
	}

	// 漂移：改 registry.js 任意字段（这里改 wowfood 的 templateId）→ 字节比对必须报错。
	drift := strings.Replace(renderRegistryJS(),
		"id: 'wowfood',\n    templateId: 'fbif',",
		"id: 'wowfood',\n    templateId: 'foodtalks-site',", 1)
	if drift == renderRegistryJS() {
		t.Fatal("测试自身失效：未能制造 templateId 漂移")
	}
	if err := os.WriteFile(regPath, []byte(drift), 0o644); err != nil {
		t.Fatal(err)
	}
	err := validateRegistryConsistency(dir)
	if err == nil || strings.HasPrefix(err.Error(), "warn:") {
		t.Fatalf("漂移必须 fail loud（非 warn），却得到：%v", err)
	}
	if !strings.Contains(err.Error(), "gen:registry") {
		t.Fatalf("错误信息应提示跑 gen:registry，却是：%v", err)
	}

	// 显示名漂移（accountName）也必须被捕获——字节比对覆盖全字段，不止结构字段。
	nameDrift := strings.Replace(renderRegistryJS(), "accountName: 'WowFood爆品超市'", "accountName: '改名了'", 1)
	if err := os.WriteFile(regPath, []byte(nameDrift), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateRegistryConsistency(dir); err == nil || strings.HasPrefix(err.Error(), "warn:") {
		t.Fatalf("accountName 漂移也必须 fail loud，却得到：%v", err)
	}

	// 文件缺失：只告警，不致命（避免部署路径差异 brick 生产）。
	missErr := validateRegistryConsistency(filepath.Join(dir, "nonexistent"))
	if missErr == nil || !strings.HasPrefix(missErr.Error(), "warn:") {
		t.Fatalf("registry.js 缺失应只告警，却得到：%v", missErr)
	}
}

// 意图：delete 一个账号正在用的 preset 时，账号回退到它在 catalog 里登记的 DefaultPresetID，
// 而不是无脑回退 FBIF 的 default（消灭旧的逐账号 if-chain，D5）。
func TestDeleteFallsBackToCatalogDefaultPreset(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))

	// 给 wowfood 新建并激活一个临时 preset，再删掉它。
	if _, err := store.upsert(rulePreset{ID: "wowfood-temp", Name: "临时", Config: defaultRuleConfig()}); err != nil {
		t.Fatalf("upsert temp preset: %v", err)
	}
	if _, err := store.setActiveForAccount(wowFoodRuleAccountID, "wowfood-temp"); err != nil {
		t.Fatalf("activate temp for wowfood: %v", err)
	}
	if _, err := store.delete("wowfood-temp"); err != nil {
		t.Fatalf("delete temp: %v", err)
	}

	doc, err := store.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var wow *ruleAccountBinding
	for i := range doc.Accounts {
		if doc.Accounts[i].ID == wowFoodRuleAccountID {
			wow = &doc.Accounts[i]
		}
	}
	if wow == nil {
		t.Fatalf("wowfood account missing: %#v", doc.Accounts)
	}
	if wow.ActiveID != wowFoodDefaultRulePresetID {
		t.Fatalf("删 preset 后 wowfood 应回退到 %q（catalog DefaultPresetID），却是 %q",
			wowFoodDefaultRulePresetID, wow.ActiveID)
	}
}

// 意图：账号的 catalog 默认 preset 不可删除——否则该账号回退目标会悬空（指向已删规则）。
func TestCannotDeleteCatalogDefaultPreset(t *testing.T) {
	store := newRulePresetStore(filepath.Join(t.TempDir(), "rules.json"))
	for _, m := range accountCatalog() {
		if _, err := store.delete(m.DefaultPresetID); err == nil {
			t.Fatalf("删除 catalog 默认 preset %q 应被拒绝，却成功了", m.DefaultPresetID)
		}
		// 确认账号激活规则仍指向存在的规则（没被悬空）。
		doc, err := store.list()
		if err != nil {
			t.Fatalf("list after refused delete: %v", err)
		}
		for _, a := range doc.Accounts {
			if a.ID == m.ID && a.ActiveID != m.DefaultPresetID {
				t.Fatalf("账号 %q 默认 preset 被拒删后激活规则不应改变，却变成 %q", m.ID, a.ActiveID)
			}
		}
	}
}

func head(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
