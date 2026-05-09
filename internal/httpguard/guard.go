// Package httpguard provides SSRF-protection helpers for outbound HTTP clients
// that accept user-configured base URLs (e.g. OpenAI-compatible providers).
//
// The package is intentionally self-contained so that any internal package can
// import it without risking a circular dependency. Do NOT import internal/discordbot
// or internal/llm from here.
package httpguard

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

// defaultResolver is used for DNS lookups so context propagation is respected.
var defaultResolver = &net.Resolver{}

// IsBlockedIP returns (true, reason) when ip falls in a blocked range.
// Nil ip returns (false, "") — safe to call with net.ParseIP output that may be nil.
func IsBlockedIP(ip net.IP) (bool, string) {
	if ip == nil {
		return false, ""
	}
	// Normalize to 16-byte form for both IPv4 and IPv6 comparisons.
	ip = ip.To16()

	for _, r := range blockedRanges {
		if r.cidr.Contains(ip) {
			return true, r.reason
		}
	}
	return false, ""
}

// IsConstructorBlockedIP returns (true, reason) only for addresses that are
// definitely unsafe as user-configured base URLs at construction time.
//
// Unlike IsBlockedIP (which is used by SafeDial and blocks all RFC-1918 ranges
// including loopback), this function is deliberately lenient about loopback and
// private LAN ranges so that legitimate Ollama / vLLM deployments on localhost
// or a private network are not rejected at startup. It ONLY blocks:
//   - 169.254.0.0/16  — link-local / cloud metadata (Railway SSRF vector)
//   - 0.0.0.0/32      — unspecified (invalid destination)
//   - ::/128           — IPv6 unspecified
//   - fe80::/10        — IPv6 link-local
func IsConstructorBlockedIP(ip net.IP) (bool, string) {
	if ip == nil {
		return false, ""
	}
	ip = ip.To16()

	for _, r := range constructorBlockedRanges {
		if r.cidr.Contains(ip) {
			return true, r.reason
		}
	}
	return false, ""
}

// IsSafeURL validates rawURL against SSRF block rules using the full
// IsBlockedIP check (all RFC-1918, link-local, loopback, unspecified).
// It performs DNS resolution and checks every returned IP.
// ctx is propagated to the DNS lookup so it can be cancelled by the caller's deadline.
func IsSafeURL(ctx context.Context, rawURL string) (bool, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false, fmt.Errorf("parse URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false, &ErrSSRF{reason: fmt.Sprintf("scheme %q is not allowed", parsed.Scheme)}
	}

	host := parsed.Hostname()
	if host == "" {
		return false, &ErrSSRF{reason: "empty hostname"}
	}

	// If the host is already an IP literal, check it directly without DNS.
	if ip := net.ParseIP(host); ip != nil {
		if blocked, reason := IsBlockedIP(ip); blocked {
			return false, &ErrSSRF{reason: reason}
		}
		return true, nil
	}

	// Resolve the hostname and check every returned address.
	addrs, err := defaultResolver.LookupHost(ctx, host)
	if err != nil {
		return false, fmt.Errorf("DNS lookup %q: %w", host, err)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			return false, &ErrSSRF{reason: fmt.Sprintf("DNS returned non-IP address %q", addr)}
		}
		if blocked, reason := IsBlockedIP(ip); blocked {
			return false, &ErrSSRF{reason: fmt.Sprintf("DNS rebinding detected — %s resolves to %s (%s)", host, addr, reason)}
		}
	}
	return true, nil
}

// SafeDial is a custom DialContext that re-validates the destination at dial
// time, defending against DNS rebinding between URL check and actual connection.
//
// net/http.Transport passes the RAW HOSTNAME to a custom DialContext, not a
// resolved IP. We handle two cases:
//  1. host is already an IP literal — check directly via IsBlockedIP and dial.
//  2. host is a hostname — resolve DNS ourselves, validate every returned
//     address with IsBlockedIP, and dial the first safe IP.
//
// Always dialing the resolved numeric IP (rather than passing the hostname
// back to the OS resolver) ensures the IP we validated is the IP we actually
// connect to — the rebind window is closed.
func SafeDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("split host/port %q: %w", addr, err)
	}

	d := &net.Dialer{Timeout: 10 * time.Second}

	// Case 1: IP literal — check directly, no DNS.
	if ip := net.ParseIP(host); ip != nil {
		if blocked, reason := IsBlockedIP(ip); blocked {
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
		if blocked, reason := IsBlockedIP(ip); blocked {
			return nil, &ErrSSRF{reason: fmt.Sprintf("DNS rebinding: %s → %s (%s)", host, a, reason)}
		}
	}
	// All resolved IPs passed IsBlockedIP; dial the first one numerically so
	// the OS resolver cannot return a different address than we validated.
	conn, err := d.DialContext(ctx, network, net.JoinHostPort(addrs[0], port))
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return conn, nil
}

// NewSafeHTTPClient returns an *http.Client whose Transport uses SafeDial,
// enforcing SSRF protection at the TCP dial layer (DNS rebinding mitigation),
// with a 10-second connection timeout.
// Caller MUST set a meaningful Timeout after calling NewSafeHTTPClient (the
// default 10 s may not suit all providers).
// Caller MUST wrap resp.Body with io.LimitReader before reading.
func NewSafeHTTPClient() *http.Client {
	transport := &http.Transport{
		DialContext:           SafeDial,
		ResponseHeaderTimeout: 5 * time.Second,
		IdleConnTimeout:       15 * time.Second,
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		// Explicit CheckRedirect re-validates the target URL at the application
		// layer so the SSRF defence is visible and regression-proof regardless
		// of future Transport changes.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			if _, err := IsSafeURL(req.Context(), req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
}

type blockedRange struct {
	cidr   *net.IPNet
	reason string
}

// blockedRanges covers all RFC-1918 private ranges, link-local (169.254/16),
// loopback (127/8, ::1), and unspecified (0.0.0.0, ::).
// Used by IsBlockedIP and SafeDial.
var blockedRanges []blockedRange

// constructorBlockedRanges covers only the most dangerous ranges for user-supplied
// IP literals at construction time. Loopback and private LAN are NOT blocked here
// so that Ollama/vLLM on localhost or a private network can be configured without
// hitting a wall on startup. The SafeDial layer still blocks those at connect time.
var constructorBlockedRanges []blockedRange

func init() {
	allCIDRs := []struct {
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
		// IPv4-mapped IPv6 addresses to their plain IPv4 form before reaching
		// IsBlockedIP, so legitimate IPv4-mapped inputs are already caught by
		// the RFC 1918 / loopback / link-local rules above. But IsBlockedIP
		// also calls ip.To16(), which produces the 16-byte ::ffff:x.x.x.x form
		// for EVERY IPv4 address — a `::ffff:0:0/96` rule would match every
		// IPv4 public address and silently DoS the entire feature.
	}
	for _, c := range allCIDRs {
		_, n, err := net.ParseCIDR(c.cidr)
		if err != nil {
			panic(fmt.Sprintf("httpguard: invalid CIDR %q: %v", c.cidr, err))
		}
		blockedRanges = append(blockedRanges, blockedRange{cidr: n, reason: c.reason})
	}

	constructorCIDRs := []struct {
		cidr   string
		reason string
	}{
		{"169.254.0.0/16", "link-local / cloud metadata range (Railway SSRF vector)"},
		{"0.0.0.0/32", "unspecified address"},
		{"::/128", "IPv6 unspecified address"},
		{"fe80::/10", "IPv6 link-local"},
	}
	for _, c := range constructorCIDRs {
		_, n, err := net.ParseCIDR(c.cidr)
		if err != nil {
			panic(fmt.Sprintf("httpguard: invalid CIDR %q: %v", c.cidr, err))
		}
		constructorBlockedRanges = append(constructorBlockedRanges, blockedRange{cidr: n, reason: c.reason})
	}
}
