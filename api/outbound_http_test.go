package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

type stubIPResolver map[string][]netip.Addr

func (r stubIPResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	addrs, ok := r[host]
	if !ok {
		return nil, errors.New("unexpected host: " + host)
	}
	return addrs, nil
}

type recordingDialer struct {
	called bool
}

func (d *recordingDialer) DialContext(_ context.Context, _, _ string) (net.Conn, error) {
	d.called = true
	return nil, errors.New("dial should not be reached")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestValidatePublicWebURLRejectsUnsafeTargets(t *testing.T) {
	t.Parallel()

	unsafe := []string{
		"file:///etc/passwd",
		"http://localhost/",
		"http://service.local/path",
		"http://127.0.0.1/",
		"http://0.0.0.0/",
		"http://10.0.0.1/",
		"http://100.64.0.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://172.16.0.1/",
		"http://192.168.1.1/",
		"http://198.18.0.1/",
		"http://[::1]/",
		"http://[fc00::1]/",
		"http://[fe80::1]/",
		"https://example.com:22/",
		"https://user:pass@example.com/",
	}
	for _, raw := range unsafe {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := parsePublicWebURL(raw); err == nil {
				t.Fatalf("parsePublicWebURL(%q) unexpectedly succeeded", raw)
			}
		})
	}
}

func FuzzParsePublicWebURLPolicy(f *testing.F) {
	seeds := []string{
		"https://example.com/path?q=1",
		"http://93.184.216.34/",
		"http://127.0.0.1/",
		"http://[::1]/",
		"https://user:pass@example.com/",
		"https://example.com:22/",
		"file:///etc/passwd",
		"//example.com/path",
		"http://example.com\\@127.0.0.1/",
		"https://localhost./",
		"https://example.com./",
		"https://[2001:4860:4860::8888]/",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		u, err := parsePublicWebURL(raw)
		if err != nil {
			return
		}

		if u.Scheme != "http" && u.Scheme != "https" {
			t.Fatalf("accepted scheme %q for %q", u.Scheme, raw)
		}
		if u.Hostname() == "" || u.Opaque != "" || u.User != nil {
			t.Fatalf("accepted malformed authority for %q: %#v", raw, u)
		}
		if port := u.Port(); port != "" && port != "80" && port != "443" {
			t.Fatalf("accepted port %q for %q", port, raw)
		}

		host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		if isBlockedOutboundHostname(host) {
			t.Fatalf("accepted blocked hostname %q from %q", host, raw)
		}
		if addr, parseErr := netip.ParseAddr(host); parseErr == nil && !isPublicOutboundAddr(addr) {
			t.Fatalf("accepted non-public IP %q from %q", addr, raw)
		}
		if err := publicWebURLPolicy(u); err != nil {
			t.Fatalf("accepted URL failed the same policy on recheck: %v", err)
		}
	})
}

func TestSafeDialContextRejectsDNSRebindingToPrivateAddress(t *testing.T) {
	t.Parallel()

	resolver := stubIPResolver{
		"public.example": {netip.MustParseAddr("203.0.113.20"), netip.MustParseAddr("127.0.0.1")},
	}
	dialer := &recordingDialer{}
	dial := safeDialContext(resolver, dialer)

	_, err := dial(context.Background(), "tcp", "public.example:443")
	if err == nil || !errors.Is(err, errUnsafeOutboundTarget) {
		t.Fatalf("safe dial error = %v, want errUnsafeOutboundTarget", err)
	}
	if dialer.called {
		t.Fatal("dialer was called after a private DNS answer")
	}
}

func TestOutboundRedirectPolicyRevalidatesEveryHop(t *testing.T) {
	t.Parallel()

	check := outboundRedirectPolicy(publicWebURLPolicy)
	first, _ := http.NewRequest(http.MethodGet, "https://example.com/start", nil)
	redirect, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1/admin", nil)
	if err := check(redirect, []*http.Request{first}); err == nil || !errors.Is(err, errUnsafeOutboundTarget) {
		t.Fatalf("redirect error = %v, want unsafe target", err)
	}
}

func TestOutboundRedirectPolicyCapsRedirectChain(t *testing.T) {
	t.Parallel()

	check := outboundRedirectPolicy(publicWebURLPolicy)
	redirect, _ := http.NewRequest(http.MethodGet, "https://example.com/final", nil)
	via := make([]*http.Request, maxOutboundRedirects)
	for i := range via {
		via[i], _ = http.NewRequest(http.MethodGet, "https://example.com/hop", nil)
	}
	if err := check(redirect, via); err == nil {
		t.Fatalf("redirect chain of %d hops unexpectedly succeeded", len(via))
	}
}

func TestImageProxyRedirectPolicyKeepsEveryHopOnAllowlist(t *testing.T) {
	t.Parallel()

	check := outboundRedirectPolicy(imageProxyURLPolicy)
	first, _ := http.NewRequest(http.MethodGet, "https://mmbiz.qpic.cn/a.jpg", nil)
	redirect, _ := http.NewRequest(http.MethodGet, "https://example.com/secret", nil)
	if err := check(redirect, []*http.Request{first}); err == nil {
		t.Fatal("redirect away from the image CDN allowlist unexpectedly succeeded")
	}
}

func TestReadBoundedBodyRejectsOversizedResponse(t *testing.T) {
	t.Parallel()

	_, err := readBoundedBody(io.NopCloser(strings.NewReader("12345")), 4)
	if err == nil || !errors.Is(err, errOutboundResponseTooLarge) {
		t.Fatalf("readBoundedBody error = %v, want size error", err)
	}
}

func TestHandleImageProxyRejectsForgedImageContentType(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"image/jpeg"}},
			Body:       io.NopCloser(strings.NewReader("<html>internal secret</html>")),
			Request:    req,
		}, nil
	})}
	handler := handleImageProxyWithClient(client)
	req := httptest.NewRequest(http.MethodGet, "/api/image-proxy?url="+url.QueryEscape("https://mmbiz.qpic.cn/a.jpg"), nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "internal secret") {
		t.Fatal("proxy leaked a non-image response body")
	}
}
