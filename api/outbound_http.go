package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var (
	errUnsafeOutboundTarget     = errors.New("unsafe outbound target")
	errOutboundResponseTooLarge = errors.New("outbound response too large")
)

type outboundURLPolicyFunc func(*url.URL) error

type outboundIPResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type outboundDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

const maxOutboundRedirects = 3

var blockedOutboundPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func parsePublicWebURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid URL", errUnsafeOutboundTarget)
	}
	if err := publicWebURLPolicy(u); err != nil {
		return nil, err
	}
	return u, nil
}

func publicWebURLPolicy(u *url.URL) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" {
		return fmt.Errorf("%w: only absolute HTTP(S) URLs are allowed", errUnsafeOutboundTarget)
	}
	if u.User != nil {
		return fmt.Errorf("%w: URL credentials are not allowed", errUnsafeOutboundTarget)
	}
	if port := u.Port(); port != "" && port != "80" && port != "443" {
		return fmt.Errorf("%w: port is not allowed", errUnsafeOutboundTarget)
	}

	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if isBlockedOutboundHostname(host) {
		return fmt.Errorf("%w: hostname is not public", errUnsafeOutboundTarget)
	}
	if addr, err := netip.ParseAddr(host); err == nil && !isPublicOutboundAddr(addr) {
		return fmt.Errorf("%w: IP address is not public", errUnsafeOutboundTarget)
	}
	return nil
}

func isBlockedOutboundHostname(host string) bool {
	if host == "localhost" || host == "instance-data" || host == "metadata.google.internal" {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".home.arpa"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func isPublicOutboundAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsUnspecified() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() {
		return false
	}
	for _, prefix := range blockedOutboundPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func imageProxyURLPolicy(u *url.URL) error {
	if err := publicWebURLPolicy(u); err != nil {
		return err
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	for _, allowed := range hostAllowlistCDN {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return nil
		}
	}
	return fmt.Errorf("%w: image host is not allowlisted", errUnsafeOutboundTarget)
}

func safeDialContext(resolver outboundIPResolver, dialer outboundDialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("split outbound address: %w", err)
		}

		var addrs []netip.Addr
		if literal, parseErr := netip.ParseAddr(strings.Trim(host, "[]")); parseErr == nil {
			addrs = []netip.Addr{literal}
		} else {
			addrs, err = resolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("resolve outbound host: %w", err)
			}
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("resolve outbound host: no addresses")
		}
		for _, addr := range addrs {
			if !isPublicOutboundAddr(addr) {
				return nil, fmt.Errorf("%w: DNS returned a non-public address", errUnsafeOutboundTarget)
			}
		}

		var lastErr error
		for _, addr := range addrs {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, fmt.Errorf("dial outbound host: %w", lastErr)
	}
}

func outboundRedirectPolicy(policy outboundURLPolicyFunc) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxOutboundRedirects {
			return errors.New("too many redirects")
		}
		if err := policy(req.URL); err != nil {
			return fmt.Errorf("redirect rejected: %w", err)
		}
		return nil
	}
}

func newOutboundHTTPClient(timeout time.Duration, policy outboundURLPolicyFunc) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A process-wide HTTP proxy would resolve the attacker-controlled target
	// outside this transport and bypass the connect-time IP check.
	transport.Proxy = nil
	transport.DialContext = safeDialContext(net.DefaultResolver, &net.Dialer{
		Timeout:   8 * time.Second,
		KeepAlive: 30 * time.Second,
	})
	transport.ResponseHeaderTimeout = 10 * time.Second
	transport.TLSHandshakeTimeout = 8 * time.Second

	return &http.Client{
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: outboundRedirectPolicy(policy),
	}
}

func doOutboundRequest(client *http.Client, req *http.Request, policy outboundURLPolicyFunc) (*http.Response, error) {
	if err := policy(req.URL); err != nil {
		return nil, err
	}
	return client.Do(req)
}

func readBoundedBody(r io.Reader, maxBytes int64) ([]byte, error) {
	limited := &io.LimitedReader{R: r, N: maxBytes + 1}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, errOutboundResponseTooLarge
	}
	return body, nil
}
