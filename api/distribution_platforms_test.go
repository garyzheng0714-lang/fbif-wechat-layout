package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// 测试用的平台限制是分发服务 GET /v1/platforms 的一份拷贝（testdata/distribution-platforms.json）；
// 和分发服务的平台文件对不对得上，DISTRIBUTION_REPO=<分发工具仓库> 时由 TestDistributionFixturesMatchService 核对。
func init() {
	items, err := readTestDistributionPlatforms()
	if err != nil {
		panic(err)
	}
	distributionPlatforms.items, distributionPlatforms.loaded = items, true
}

func readTestDistributionPlatforms() ([]distributionPlatformInfo, error) {
	raw, err := os.ReadFile(filepath.Join("testdata", platformsFile))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Platforms []distributionPlatformInfo `json:"platforms"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc.Platforms, validateDistributionPlatforms(doc.Platforms)
}

// testPlatform 测试里按平台编号取限制，没有就是 fixture 缺了这个平台。
func testPlatform(t testing.TB, key string) distributionPlatform {
	t.Helper()
	p := platformByKey(key)
	if !p.Known {
		t.Fatalf("testdata/%s 里没有平台 %s", platformsFile, key)
	}
	return p
}

// setTestDistributionPlatforms 测试里换一份平台限制，测试结束还原。
func setTestDistributionPlatforms(t *testing.T, items ...distributionPlatformInfo) {
	t.Helper()
	d := distributionPlatforms
	d.mu.Lock()
	prevItems, prevLoaded, prevErr := d.items, d.loaded, d.lastErr
	d.items, d.loaded, d.lastErr = items, true, ""
	d.mu.Unlock()
	t.Cleanup(func() {
		d.mu.Lock()
		d.items, d.loaded, d.lastErr = prevItems, prevLoaded, prevErr
		d.mu.Unlock()
	})
}

// distributionOutline 外发块读起来的样子，一块一行；加粗写成 **字**。测试按它断言，各平台写成什么标签归分发服务测。
func distributionOutline(blocks []Block) string {
	runs := func(rs []Run) string {
		var b strings.Builder
		for _, r := range rs {
			if r.Bold {
				b.WriteString("**" + r.Text + "**")
			} else {
				b.WriteString(r.Text)
			}
		}
		return b.String()
	}
	var lines []string
	for _, block := range blocks {
		switch block.K {
		case "h":
			lines = append(lines, fmt.Sprintf("h%d %s", block.Level, block.Text))
		case "img":
			lines = append(lines, "img "+block.Src)
		case "cap":
			lines = append(lines, "cap "+block.Text)
		case "video":
			lines = append(lines, "video "+block.Src+" "+block.Poster)
		case "table":
			var rows []string
			for _, row := range block.Rows {
				var cells []string
				for _, cell := range row {
					text := runs(cell.Runs)
					if cell.Header {
						text = "[" + text + "]"
					}
					cells = append(cells, text)
				}
				rows = append(rows, strings.Join(cells, " | "))
			}
			lines = append(lines, "table "+strings.Join(rows, " / "))
		default:
			lines = append(lines, block.K+" "+runs(block.Runs))
		}
	}
	return strings.Join(lines, "\n")
}

// outgoingOutline 各平台一样的规则跑一遍，返回外发块的样子。
func outgoingOutline(t *testing.T, blocks, imageMap string) string {
	t.Helper()
	content, err := buildDistributionContent(publishJob{BlocksJSON: blocks, ImageMap: imageMap}, testPlatform(t, "toutiao"), publishExternalDraft{})
	if err != nil {
		t.Fatal(err)
	}
	return distributionOutline(content.Blocks)
}

// TestDistributionPlatformsCacheKeepsLastGoodCopy 平台限制和账号一样：网络上取到的、本地读出的都先校验，
// 不对就整份不要，接着用上一份；不对的那份不写盘。
func TestDistributionPlatformsCacheKeepsLastGoodCopy(t *testing.T) {
	good, err := readTestDistributionPlatforms()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), platformsFile)
	fetched, fetchErr := good, error(nil)
	d := &distributionCache[distributionPlatformInfo]{what: "平台限制", key: "platforms", validate: validateDistributionPlatforms, path: path,
		fetch: func(context.Context) ([]distributionPlatformInfo, error) { return fetched, fetchErr }}
	if err := d.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	bad := append([]distributionPlatformInfo(nil), good...)
	bad[0].Title.Count = "words"
	for name, c := range map[string]struct {
		items []distributionPlatformInfo
		err   error
	}{
		"连不上":          {nil, errors.New("连不上")},
		"缺这一项或给了 null": {nil, nil},
		"标题数法不认得":      {bad, nil},
		"重复平台":         {append(append([]distributionPlatformInfo(nil), good...), good[0]), nil},
	} {
		fetched, fetchErr = c.items, c.err
		if err := d.refresh(context.Background()); err == nil {
			t.Fatalf("%s：应报错", name)
		}
		if items, err := d.snapshot(); err != nil || len(items) != len(good) || items[0].Title.Count != good[0].Title.Count {
			t.Fatalf("%s：接着用上一份：%v %v", name, items, err)
		}
	}
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), "words") {
		t.Fatalf("不收的那份不写盘：%s", raw)
	}

	// 本地文件被改坏：重启时不认，当场向分发服务取。
	raw, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(raw), `"ascii_half"`, `"words"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	fetched, fetchErr = good, nil
	restarted := &distributionCache[distributionPlatformInfo]{what: "平台限制", key: "platforms", validate: validateDistributionPlatforms, path: path,
		fetch: func(context.Context) ([]distributionPlatformInfo, error) { return fetched, fetchErr }}
	if err := restarted.loadFile(); err == nil {
		t.Fatal("改坏的本地文件应不认")
	}
	restarted.prime()
	if items, err := restarted.snapshot(); err != nil || len(items) != len(good) {
		t.Fatalf("本地那份不对就当场取：%v %v", items, err)
	}

	// 本地文件里这一项是 null 或没有：也不认，不当「一个平台都没有」。
	for _, doc := range []string{`{"platforms":null}`, `{}`} {
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := restarted.loadFile(); err == nil {
			t.Fatalf("%s 应不认", doc)
		}
		if items, err := restarted.snapshot(); err != nil || len(items) != len(good) {
			t.Fatalf("%s：接着用上一份：%v %v", doc, items, err)
		}
	}
}

// TestUnknownPlatformShowsButNeverPushes 从没拿到某个平台的限制时：号照样列出、卡片照常显示（名字用账号名），
// 推送在提交前失败；标题不算超（没检查过的不算通过，也不算不通过）。
func TestUnknownPlatformShowsButNeverPushes(t *testing.T) {
	setTestDistributionPlatforms(t)
	setTestExternalAccounts(t, externalAccount{ID: "recDayu", Platform: "dayu", Carrier: externalCarrierArticle, Status: externalAccountLive, Name: "大鱼号 · FBIF", Source: "FBIF食品饮料创新"})
	cfg, err := loadExternalTargets()
	if err != nil {
		t.Fatal(err)
	}
	targets := cfg.forJob(publishJob{SourceName: "FBIF食品饮料创新"})
	if len(targets) != 1 || targets[0].label() != "大鱼号 · FBIF" {
		t.Fatalf("没拿到平台限制的号照样列出，名字用账号名：%+v", targets)
	}
	profile := platformForAccount(targets[0])
	if profile.Known {
		t.Fatal("没拿到的平台不能算 Known")
	}
	if _, _, over := profile.titleOver(strings.Repeat("长", 200)); over {
		t.Fatal("没拿到上限时不判超")
	}
	_, err = buildRobotDraftPayload(manualLayoutTestJob(), profile, publishExternalDraft{})
	if err == nil || !strings.Contains(err.Error(), "还没从分发服务拿到大鱼号 · FBIF的平台限制") {
		t.Fatalf("推送前就失败并说清楚：%v", err)
	}
}

// TestDistributionFixturesMatchService 两边各存的一份要对得上：测试用的平台限制、考卷前一半写出的正文块。
// 改了分发服务的平台文件或通用规则，两边都要重新生成（见 testdata/distribution-golden/README.md）。
func TestDistributionFixturesMatchService(t *testing.T) {
	repo := os.Getenv("DISTRIBUTION_REPO")
	if repo == "" {
		t.Skip("DISTRIBUTION_REPO=<分发工具仓库> 时才核对")
	}
	ours, err := os.ReadFile(filepath.Join("testdata", platformsFile))
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := os.ReadFile(filepath.Join(repo, "test", "fixtures", "platforms.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(ours) != string(theirs) {
		t.Errorf("testdata/%s 和分发服务的 test/fixtures/platforms.json 不一样：在分发工具跑 npm run gen:docs 再拷过来", platformsFile)
	}
	paths, _ := filepath.Glob(filepath.Join(distributionGoldenDir, "*.blocks.json"))
	if len(paths) == 0 {
		t.Fatal("没有考卷前一半的正文块")
	}
	for _, path := range paths {
		ours, _ := os.ReadFile(path)
		theirs, err := os.ReadFile(filepath.Join(repo, "test", "golden", filepath.Base(path)))
		if err != nil || string(ours) != string(theirs) {
			t.Errorf("%s 和分发服务 test/golden 里的不一样，拷过去：%v", path, err)
		}
	}
}

// TestDistributionRobotClientReadsPlatforms 平台限制按分发服务 GET /v1/platforms 的原样解出来。
func TestDistributionRobotClientReadsPlatforms(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", platformsFile))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/platforms" || r.Header.Get("Authorization") != "Bearer "+testRobotToken {
			http.Error(w, `{"error":{"code":"not_found","message":"没有"}}`, http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	got, err := newDistributionRobotClient(distributionRobotConfig{BaseURL: server.URL, Token: testRobotToken}).Platforms(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := readTestDistributionPlatforms()
	if !reflect.DeepEqual(got, want) || validateDistributionPlatforms(got) != nil {
		t.Fatalf("解出来的平台限制不对：%+v", got)
	}
	toutiao := testPlatform(t, "toutiao")
	if toutiao.Name != "头条号" || toutiao.TitleMax != 30 || !toutiao.TitleHalfWidthASCII || toutiao.CoverMinWidth != 452 || toutiao.CoverMinHeight != 352 || toutiao.DailyLimit != 5 {
		t.Fatalf("头条号的限制没照分发服务给的：%+v", toutiao)
	}
}

// TestExternalArticleURLAcceptsPlatformSubdomains 文章、草稿链接只认分发服务给的域名，子域名也算
// （头条号草稿在 mp.toutiao.com，以前只认整串相同，草稿链接被丢了）；长得像的别家域名不算。
func TestExternalArticleURLAcceptsPlatformSubdomains(t *testing.T) {
	toutiao := testPlatform(t, "toutiao")
	for _, ok := range []string{"https://www.toutiao.com/article/7696704683460641306/", "https://mp.toutiao.com/profile_v4/graphic/publish?pgc_id=1"} {
		if _, err := validateExternalArticleURL(ok, toutiao); err != nil {
			t.Errorf("%s 应认：%v", ok, err)
		}
	}
	for _, bad := range []string{"https://eviltoutiao.com/a", "https://toutiao.com.evil.cn/a", "http://www.toutiao.com/a", "https://user@www.toutiao.com/a"} {
		if _, err := validateExternalArticleURL(bad, toutiao); err == nil {
			t.Errorf("%s 不该认", bad)
		}
	}
}
