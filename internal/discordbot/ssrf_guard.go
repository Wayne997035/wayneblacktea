package discordbot

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// ErrSSRF is returned when a URL or resolved IP is blocked by the SSRF guard.
type ErrSSRF struct{ reason string }

func (e *ErrSSRF) Error() string { return "SSRF blocked: " + e.reason }

// allowedSchemes lists the only URL schemes permitted for external fetching.
var allowedSchemes = map[string]bool{"http": true, "https": true}

// defaultResolver is used for DNS lookups so context propagation is respected.
var defaultResolver = &net.Resolver{}

// IsSafeURL validates rawURL against SSRF block rules:
//   - scheme must be http or https
//   - hostname must not resolve to RFC 1918, link-local, loopback, or unspecified addresses
//
// It performs DNS resolution and checks every returned IP.
// ctx is propagated to the DNS lookup so it can be cancelled by the caller's deadline.
func IsSafeURL(ctx context.Context, rawURL string) (bool, error) {
	return isSafeURLWithResolver(ctx, rawURL, defaultResolver)
}

// isSafeURLWithResolver is the testable core — it accepts an explicit context and resolver.
func isSafeURLWithResolver(ctx context.Context, rawURL string, r *net.Resolver) (bool, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false, fmt.Errorf("parse URL: %w", err)
	}
	if !allowedSchemes[parsed.Scheme] {
		return false, &ErrSSRF{reason: fmt.Sprintf("scheme %q is not allowed", parsed.Scheme)}
	}

	host := parsed.Hostname()
	if host == "" {
		return false, &ErrSSRF{reason: "empty hostname"}
	}

	// If the host is already an IP literal, check it directly without DNS.
	if ip := net.ParseIP(host); ip != nil {
		if blocked, reason := isBlockedIP(ip); blocked {
			return false, &ErrSSRF{reason: reason}
		}
		return true, nil
	}

	// Resolve the hostname and check every returned address.
	addrs, err := r.LookupHost(ctx, host)
	if err != nil {
		return false, fmt.Errorf("DNS lookup %q: %w", host, err)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			return false, &ErrSSRF{reason: fmt.Sprintf("DNS returned non-IP address %q", addr)}
		}
		if blocked, reason := isBlockedIP(ip); blocked {
			return false, &ErrSSRF{reason: fmt.Sprintf("DNS rebinding detected — %s resolves to %s (%s)", host, addr, reason)}
		}
	}
	return true, nil
}

// isBlockedIP returns (true, reason) when ip falls in a blocked range.
func isBlockedIP(ip net.IP) (bool, string) {
	// Normalize to 16-byte form for both IPv4 and IPv6 comparisons.
	ip = ip.To16()

	for _, r := range blockedRanges {
		if r.cidr.Contains(ip) {
			return true, r.reason
		}
	}
	return false, ""
}

type blockedRange struct {
	cidr   *net.IPNet
	reason string
}

// blockedRanges is initialised once at package init.
// All RFC 1918 private ranges, link-local (169.254/16), loopback (127/8, ::1),
// and unspecified (0.0.0.0, ::) are blocked.
var blockedRanges []blockedRange

func init() {
	cidrs := []struct {
		cidr   string
		reason string
	}{
		{"10.0.0.0/8", "RFC 1918 private range"},
		{"172.16.0.0/12", "RFC 1918 private range"},
		{"192.168.0.0/16", "RFC 1918 private range"},
		{"169.254.0.0/16", "link-local / cloud metadata range"},
		{"127.0.0.0/8", "loopback"},
		{"::1/128", "IPv6 loopback"},
		{"0.0.0.0/32", "unspecified address"},
		{"::/128", "IPv6 unspecified address"},
		{"fc00::/7", "IPv6 unique-local (RFC 4193)"},
		{"fe80::/10", "IPv6 link-local"},
		// NOTE: do NOT add `::ffff:0:0/96` here. Go's net.ParseIP normalizes
		// IPv4-mapped IPv6 addresses (e.g. "::ffff:10.0.0.1") to their plain
		// IPv4 form before reaching isBlockedIP, so legitimate IPv4-mapped
		// inputs are already caught by the RFC 1918 / loopback / link-local
		// rules above. But isBlockedIP also calls ip.To16(), which produces
		// the 16-byte ::ffff:x.x.x.x form for EVERY IPv4 address — including
		// public ones like 8.8.8.8 — so a `::ffff:0:0/96` rule would match
		// every IPv4 public address and silently DoS the entire feature.
	}
	for _, c := range cidrs {
		_, net, err := net.ParseCIDR(c.cidr)
		if err != nil {
			panic(fmt.Sprintf("ssrf_guard: invalid CIDR %q: %v", c.cidr, err))
		}
		blockedRanges = append(blockedRanges, blockedRange{cidr: net, reason: c.reason})
	}
}

// safeDial is a custom DialContext that re-validates the destination at dial
// time, defending against DNS rebinding between IsSafeURL check and actual
// connection.
//
// Important: net/http.Transport passes the RAW HOSTNAME to a custom
// DialContext, not a resolved IP. We therefore handle two cases:
//  1. host is already an IP literal — check directly via isBlockedIP and dial.
//  2. host is a hostname — resolve DNS ourselves, validate every returned
//     address with isBlockedIP, and dial the first safe IP.
//
// Always dialing the resolved numeric IP (rather than passing the hostname
// back to the OS resolver) ensures the IP we validated is the IP we actually
// connect to — the rebind window is closed.
func safeDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("split host/port %q: %w", addr, err)
	}

	d := &net.Dialer{Timeout: 10 * time.Second}

	// Case 1: IP literal — check directly, no DNS.
	if ip := net.ParseIP(host); ip != nil {
		if blocked, reason := isBlockedIP(ip); blocked {
			return nil, &ErrSSRF{reason: fmt.Sprintf("dial blocked: %s (%s)", host, reason)}
		}
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(host, port))
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", addr, err)
		}
		return conn, nil
	}

	// Case 2: hostname — resolve, validate each IP, dial the first safe one.
	addrs, err := defaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS lookup %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no addresses for %q", host)
	}
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil {
			return nil, &ErrSSRF{reason: fmt.Sprintf("DNS returned non-IP address %q for %s", a, host)}
		}
		if blocked, reason := isBlockedIP(ip); blocked {
			return nil, &ErrSSRF{reason: fmt.Sprintf("DNS rebinding: %s → %s (%s)", host, a, reason)}
		}
	}
	// All resolved IPs passed isBlockedIP; dial the first one numerically so
	// the OS resolver cannot return a different address than we validated.
	conn, err := d.DialContext(ctx, network, net.JoinHostPort(addrs[0], port))
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return conn, nil
}

// NewSafeHTTPClient returns an *http.Client whose Transport uses safeDial,
// enforcing SSRF protection at the TCP dial layer (DNS rebinding mitigation),
// with a 10-second connection timeout.
// Caller MUST wrap resp.Body with io.LimitReader before reading.
// See fetcher.go:FetchURL for the maxFetchBytes pattern.
func NewSafeHTTPClient() *http.Client {
	transport := &http.Transport{
		DialContext: safeDial,
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
	}
}
