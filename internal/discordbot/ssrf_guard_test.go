package discordbot

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestIsSafeURL_BlockedRanges covers SSRF block rules for IP literals, schemes,
// cloud metadata endpoints, and DNS resolution paths.
func TestIsSafeURL_BlockedRanges(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		url       string
		wantSafe  bool
		wantErrIs string // substring expected in error
	}{
		// --- Blocked schemes ---
		{
			name:      "file scheme blocked",
			url:       "file:///etc/passwd",
			wantSafe:  false,
			wantErrIs: `scheme "file" is not allowed`,
		},
		{
			name:      "gopher scheme blocked",
			url:       "gopher://127.0.0.1:70/",
			wantSafe:  false,
			wantErrIs: `scheme "gopher" is not allowed`,
		},
		{
			name:      "ftp scheme blocked",
			url:       "ftp://internal.example.com/",
			wantSafe:  false,
			wantErrIs: `scheme "ftp" is not allowed`,
		},
		{
			name:      "data scheme blocked",
			url:       "data:text/html,<h1>hi</h1>",
			wantSafe:  false,
			wantErrIs: `scheme "data" is not allowed`,
		},
		// --- Loopback ---
		{
			name:      "IPv4 loopback 127.0.0.1 blocked",
			url:       "http://127.0.0.1/",
			wantSafe:  false,
			wantErrIs: "loopback",
		},
		{
			name:      "IPv4 loopback 127.255.255.255 blocked (whole /8)",
			url:       "http://127.255.255.255/",
			wantSafe:  false,
			wantErrIs: "loopback",
		},
		{
			name:      "IPv6 loopback ::1 blocked",
			url:       "http://[::1]/",
			wantSafe:  false,
			wantErrIs: "loopback",
		},
		// --- RFC 1918 private ranges ---
		{
			name:      "10/8 private blocked",
			url:       "http://10.0.0.1/admin",
			wantSafe:  false,
			wantErrIs: "RFC 1918 private range",
		},
		{
			name:      "172.16/12 private blocked",
			url:       "http://172.31.0.1/",
			wantSafe:  false,
			wantErrIs: "RFC 1918 private range",
		},
		{
			name:      "192.168/16 private blocked",
			url:       "http://192.168.1.1/",
			wantSafe:  false,
			wantErrIs: "RFC 1918 private range",
		},
		// --- Cloud metadata ---
		{
			name:      "AWS metadata 169.254.169.254 blocked",
			url:       "http://169.254.169.254/latest/meta-data/iam/",
			wantSafe:  false,
			wantErrIs: "link-local / cloud metadata range",
		},
		{
			name:      "link-local 169.254.0.1 blocked",
			url:       "http://169.254.0.1/",
			wantSafe:  false,
			wantErrIs: "link-local / cloud metadata range",
		},
		// --- Unspecified ---
		{
			name:      "0.0.0.0 blocked",
			url:       "http://0.0.0.0/",
			wantSafe:  false,
			wantErrIs: "unspecified address",
		},
		// --- IPv6 unique-local ---
		{
			name:      "IPv6 unique-local fc00:: blocked",
			url:       "http://[fc00::1]/",
			wantSafe:  false,
			wantErrIs: "IPv6 unique-local",
		},
		{
			name:      "IPv6 link-local fe80:: blocked",
			url:       "http://[fe80::1]/",
			wantSafe:  false,
			wantErrIs: "IPv6 link-local",
		},
		// --- IPv4-mapped IPv6 ---
		// Go's net.ParseIP normalises ::ffff:10.0.0.1 to the IPv4 form 10.0.0.1
		// before CIDR matching, so it triggers the RFC 1918 rule — still blocked.
		{
			name:      "IPv4-mapped IPv6 ::ffff:10.0.0.1 blocked (RFC 1918 after normalisation)",
			url:       "http://[::ffff:10.0.0.1]/",
			wantSafe:  false,
			wantErrIs: "RFC 1918 private range",
		},
		// --- Public IPv4 must PASS (regression guard) ---
		// Without this case the over-broad ::ffff:0:0/96 CIDR would silently
		// block every public IPv4 — see the comment in ssrf_guard.go init().
		{
			name:      "public IPv4 8.8.8.8 must be allowed",
			url:       "http://8.8.8.8/",
			wantSafe:  true,
			wantErrIs: "",
		},
		{
			name:      "public IPv4 1.1.1.1 must be allowed",
			url:       "https://1.1.1.1/",
			wantSafe:  true,
			wantErrIs: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			safe, err := IsSafeURL(context.Background(), tc.url)
			if safe != tc.wantSafe {
				t.Errorf("IsSafeURL(%q) safe=%v, want %v", tc.url, safe, tc.wantSafe)
			}
			if tc.wantErrIs != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrIs)
				}
				if !strings.Contains(err.Error(), tc.wantErrIs) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrIs)
				}
				var ssrfErr *ErrSSRF
				if !errors.As(err, &ssrfErr) {
					t.Errorf("expected *ErrSSRF, got %T: %v", err, err)
				}
			}
		})
	}
}

// TestIsSafeURL_PublicDomain verifies that a well-known public hostname passes.
// Uses a local httptest to avoid real network dependency.
func TestIsSafeURL_PublicDomain(t *testing.T) {
	t.Parallel()
	// "localhost" always resolves to 127.0.0.1 — should be blocked even by name.
	_, err := IsSafeURL(context.Background(), "http://localhost/")
	if err == nil {
		t.Error("expected localhost to be blocked via DNS resolution")
	}
}

// TestIsBlockedIP_DirectChecks validates the isBlockedIP helper independently.
func TestIsBlockedIP_DirectChecks(t *testing.T) {
	t.Parallel()

	cases := []struct {
		ip      string
		blocked bool
	}{
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.0.1", true},
		{"169.254.169.254", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"0.0.0.0", true},
		{"fc00::1", true},
		{"fe80::1", true},
	}

	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("invalid IP literal: %q", tc.ip)
			}
			blocked, reason := isBlockedIP(ip)
			if blocked != tc.blocked {
				t.Errorf("isBlockedIP(%q) = %v (reason: %q), want %v", tc.ip, blocked, reason, tc.blocked)
			}
		})
	}
}

// TestSafeHTTPClient_BlocksInternalServer verifies that NewSafeHTTPClient blocks
// requests to a loopback test server even when the URL passes the IsSafeURL check
// (simulating DNS rebinding at dial time).
func TestSafeHTTPClient_BlocksInternalServer(t *testing.T) {
	t.Parallel()

	// Start a local server — listens on 127.0.0.1.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewSafeHTTPClient()
	resp, err := client.Get(srv.URL) //nolint:noctx // test: no context needed for dial-time blocking
	if err == nil {
		_ = resp.Body.Close()
		t.Error("expected dial to be blocked for loopback address, but request succeeded")
	}
	if !strings.Contains(err.Error(), "SSRF blocked") {
		t.Errorf("expected SSRF blocked error, got: %v", err)
	}
}

// TestFetchURL_BlockedSSRF verifies that FetchURL rejects SSRF URLs before dialing.
func TestFetchURL_BlockedSSRF(t *testing.T) {
	t.Parallel()

	blockedURLs := []string{
		"http://127.0.0.1/secret",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/admin",
		"file:///etc/passwd",
	}

	for _, u := range blockedURLs {
		u := u
		t.Run(u, func(t *testing.T) {
			t.Parallel()
			_, _, err := FetchURL(t.Context(), u)
			if err == nil {
				t.Errorf("FetchURL(%q) should have been blocked, but succeeded", u)
			}
			if !strings.Contains(err.Error(), "blocked") {
				t.Errorf("unexpected error (want 'blocked' in message): %v", err)
			}
		})
	}
}
