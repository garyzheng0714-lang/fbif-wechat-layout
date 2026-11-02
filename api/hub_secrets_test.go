package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeHubSecrets 假中台密钥端点：answer 决定这次回什么，receipts 记回执，prefer 记每次的 Prefer 头。
type fakeHubSecrets struct {
	mu       sync.Mutex
	answer   func(http.ResponseWriter)
	asked    int
	prefer   []string
	receipts []string
}

func (f *fakeHubSecrets) server(t *testing.T, key string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Errorf("要带钥匙：%q", r.Header.Get("Authorization"))
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/config/secrets/layout":
			f.asked++
			f.prefer = append(f.prefer, r.Header.Get("Prefer"))
			f.answer(w)
		case r.Method == http.MethodPost && r.URL.Path == "/api/config/secrets/layout/receipts":
			var body struct{ Code, Version string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.receipts = append(f.receipts, body.Code+"@"+body.Version)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("不该问：%s %s", r.Method, r.URL.Path)
		}
	}))
}

func mimoAnswer(value, version string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		fmt.Fprintf(w, `{"secrets":{"xiaomi-mimo":{"value":%q,"origin":"https://api.xiaomimimo.com","version":%q}},"withheld":[]}`, value, version)
	}
}

// TestHubSecretsAdoptOnceAndReceipt 取到就用、每个版本只回执一次；换了版本就换上并再回执；每次都请中台挂着等。
func TestHubSecretsAdoptOnceAndReceipt(t *testing.T) {
	const key = "pqk_layout-test"
	fake := &fakeHubSecrets{answer: mimoAnswer("sk-one", "0123456789abcdef")}
	server := fake.server(t, key)
	defer server.Close()
	client := newHubSecretsClient(server.URL+"/api/config/bundle/layout", key)
	ask := func() hubOutcome { return client.feed.round(context.Background(), client.poll) }

	if got := ask(); got != hubAnswered {
		t.Fatalf("没挂着等的回包照样换上密钥，按出错退避再问：%v", got)
	}
	cred := client.Credential(context.Background())
	if cred == nil || cred.key != "sk-one" || cred.origin != "https://api.xiaomimimo.com" {
		t.Fatalf("取到中台发的密钥和地址：%+v", cred)
	}
	ask()
	fake.answer = mimoAnswer("sk-two", "fedcba9876543210")
	ask()
	if cred := client.Credential(context.Background()); cred == nil || cred.key != "sk-two" {
		t.Fatalf("换了版本要换上：%+v", cred)
	}
	if got := strings.Join(fake.receipts, ","); got != "xiaomi-mimo@0123456789abcdef,xiaomi-mimo@fedcba9876543210" {
		t.Fatalf("每个版本回执一次：%s", got)
	}
	if got := strings.Join(fake.prefer, ","); got != "wait=10800,wait=10800,wait=10800" {
		t.Fatalf("每次都请中台挂着等：%s", got)
	}
	if strings.Contains(fmt.Sprint(client), key) {
		t.Fatal("打印客户端不能带出钥匙")
	}
}

// TestHubSecretsRefusalAndGrace 中台拒绝或不发立刻作废；连不上沿用上一次的，过了 1 小时作废。
func TestHubSecretsRefusalAndGrace(t *testing.T) {
	const key = "pqk_layout-test"
	fake := &fakeHubSecrets{answer: mimoAnswer("sk-one", "0123456789abcdef")}
	server := fake.server(t, key)
	clock := newLayoutTestClock()
	client := newHubSecretsClient(server.URL+"/api/config/bundle/layout", key)
	client.feed.now = clock.Now
	tick := func() *mimoCredential {
		clock.Add(time.Minute)
		client.feed.round(context.Background(), client.poll)
		return client.Credential(context.Background())
	}

	if tick() == nil {
		t.Fatal("先取到")
	}
	fake.answer = func(w http.ResponseWriter) {
		fmt.Fprint(w, `{"secrets":{},"withheld":[{"code":"xiaomi-mimo","reason":"接口地址和密钥绑定的地址对不上"}]}`)
	}
	if tick() != nil {
		t.Fatal("中台不发了要立刻作废")
	}
	fake.answer = mimoAnswer("sk-one", "0123456789abcdef")
	if tick() == nil {
		t.Fatal("恢复后再取到")
	}
	fake.answer = func(w http.ResponseWriter) { w.WriteHeader(http.StatusForbidden) }
	if tick() != nil {
		t.Fatal("403 要立刻作废")
	}
	fake.answer = func(w http.ResponseWriter) {
		fmt.Fprint(w, `{"secrets":{"xiaomi-mimo":{"value":"sk-one","origin":"https://api.xiaomimimo.com/v1","version":"0123456789abcdef"}}}`)
	}
	if tick() != nil {
		t.Fatal("地址带路径不收")
	}
	fake.answer = mimoAnswer("sk-one", "0123456789abcdef")
	if tick() == nil {
		t.Fatal("恢复后再取到")
	}
	server.Close()
	for i := 0; i < 59; i++ {
		if tick() == nil {
			t.Fatalf("连不上时 1 小时内沿用（第 %d 分钟就丢了）", i+1)
		}
	}
	clock.Add(2 * time.Minute)
	if client.Credential(context.Background()) != nil {
		t.Fatal("连不上超过 1 小时要作废")
	}
}

// TestHubSecretsGraceCountsFromDisconnect 中台挂着等时：请求挂着多久密钥都算新；断开后再沿用 1 小时（约定第 6 条）。
func TestHubSecretsGraceCountsFromDisconnect(t *testing.T) {
	const key = "pqk_layout-test"
	script, server := newHubScript(t, key)
	clock := newLayoutTestClock()
	client := newHubSecretsClient(server.URL+"/api/config/bundle/layout", key)
	client.feed.now = clock.Now
	script.answers <- func(w http.ResponseWriter) {
		w.Header().Set("Preference-Applied", "wait=10800")
		w.Header().Set("ETag", `"secrets-1"`)
		mimoAnswer("sk-one", "0123456789abcdef")(w)
	}
	if got := client.feed.round(context.Background(), client.poll); got != hubWaiting {
		t.Fatalf("中台照办了等待，马上再问：%v", got)
	}
	<-script.seen
	done := make(chan hubOutcome, 1)
	go func() { done <- client.feed.round(context.Background(), client.poll) }()
	if header := <-script.seen; header.Get("If-None-Match") != `"secrets-1"` {
		t.Fatalf("挂着的请求带上一版 ETag：%q", header.Get("If-None-Match"))
	}
	clock.Add(2 * time.Hour)
	if client.Credential(context.Background()) == nil {
		t.Fatal("请求挂着，2 小时后密钥照样能用")
	}
	script.answers <- hubDrop
	if got := <-done; got != hubFailed {
		t.Fatalf("断开算没问成：%v", got)
	}
	clock.Add(59 * time.Minute)
	if client.Credential(context.Background()) == nil {
		t.Fatal("断开不到 1 小时接着用")
	}
	clock.Add(2 * time.Minute)
	if client.Credential(context.Background()) != nil {
		t.Fatal("断开超过 1 小时要作废")
	}
}

func TestHTTPSOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"https://api.xiaomimimo.com":      "https://api.xiaomimimo.com",
		"https://API.xiaomimimo.com/":     "https://api.xiaomimimo.com",
		"http://api.xiaomimimo.com":       "",
		"https://u:p@api.xiaomimimo.com":  "",
		"https://api.xiaomimimo.com/v1":   "",
		"https://api.xiaomimimo.com?x=1":  "",
		"https://api.xiaomimimo.com#frag": "",
		"api.xiaomimimo.com":              "",
	} {
		if got := httpsOrigin(raw); got != want {
			t.Errorf("httpsOrigin(%q) = %q，要 %q", raw, got, want)
		}
	}
}

// TestAICredentialPrefersHub 接了中台只用中台发的，env 里的密钥不顶上；没接中台照旧读 env。
func TestAICredentialPrefersHub(t *testing.T) {
	const key = "pqk_layout-test"
	fake := &fakeHubSecrets{answer: mimoAnswer("sk-hub", "0123456789abcdef")}
	server := fake.server(t, key)
	defer server.Close()
	reset := func() { layoutHubSecrets.once, layoutHubSecrets.client, layoutHubSecrets.err = sync.Once{}, nil, nil }
	reset()
	defer reset()

	t.Setenv("MIMO_API_KEY", "sk-env")
	t.Setenv("MIMO_BASE_URL", "https://elsewhere.example/v1")
	t.Setenv("HUB_BUNDLE_URL", "")
	if k, base, err := aiCredential(context.Background()); err != nil || k != "sk-env" || base != "https://elsewhere.example/v1" {
		t.Fatalf("没接中台读 env：%q %q %v", k, base, err)
	}
	t.Setenv("HUB_BUNDLE_URL", server.URL+"/api/config/bundle/layout")
	t.Setenv("HUB_KEY", key)
	if k, base, err := aiCredential(context.Background()); err != nil || k != "sk-hub" || base != "https://api.xiaomimimo.com/v1" {
		t.Fatalf("接了中台用中台的密钥和地址：%q %q %v", k, base, err)
	}
	reset()
	fake.answer = func(w http.ResponseWriter) { w.WriteHeader(http.StatusForbidden) }
	if _, _, err := aiCredential(context.Background()); err != errHubMimoMissing {
		t.Fatalf("中台不发时不退回 env：%v", err)
	}
	if publishEnrichMode() != "fallback" {
		t.Fatal("中台不发时 readiness 报 fallback")
	}
}
