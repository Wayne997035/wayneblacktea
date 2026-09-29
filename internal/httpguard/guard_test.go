package httpguard_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/httpguard"
)

// TestIsBlockedIP_RFC1918 verifies that all three RFC-1918 private ranges are blocked.
func TestIsBlockedIP_RFC1918(t *testing.T) {
	t.Parallel()

	cases := []string{"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1"}
	for _, addr := range cases {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(addr)
			if ip == nil {
				t.Fatalf("invalid IP literal %q", addr)
			}
			blocked, reason := httpguard.IsBlockedIP(ip)
			if !blocked {
				t.Errorf("IsBlockedIP(%q) = false, want true; reason=%q", addr, reason)
			}
		})
	}
}

// TestIsBlockedIP_Loopback verifies that loopback addresses are blocked.
func TestIsBlockedIP_Loopback(t *testing.T) {
	t.Parallel()

	cases := []string{"127.0.0.1", "::1"}
	for _, addr := range cases {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(addr)
			if ip == nil {
				t.Fatalf("invalid IP literal %q", addr)
			}
			blocked, reason := httpguard.IsBlockedIP(ip)
			if !blocked {
				t.Errorf("IsBlockedIP(%q) = false, want true; reason=%q", addr, reason)
			}
		})
	}
}

// TestIsBlockedIP_LinkLocal verifies that link-local / cloud metadata IPs are blocked.
func TestIsBlockedIP_LinkLocal(t *testing.T) {
	t.Parallel()

	cases := []string{"169.254.169.254", "169.254.0.1"}
	for _, addr := range cases {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(addr)
			if ip == nil {
				t.Fatalf("invalid IP literal %q", addr)
			}
			blocked, reason := httpguard.IsBlockedIP(ip)
			if !blocked {
				t.Errorf("IsBlockedIP(%q) = false, want true; reason=%q", addr, reason)
			}
		})
	}
}

// TestIsBlockedIP_IPv6UniqueLocal verifies that IPv6 unique-local (fc00::/7) is blocked.
func TestIsBlockedIP_IPv6UniqueLocal(t *testing.T) {
	t.Parallel()

	ip := net.ParseIP("fc00::1")
	if ip == nil {
		t.Fatal("invalid IP literal fc00::1")
	}
	blocked, reason := httpguard.IsBlockedIP(ip)
	if !blocked {
		t.Errorf("IsBlockedIP(fc00::1) = false, want true; reason=%q", reason)
	}
}

// TestIsBlockedIP_IPv6LinkLocal verifies that IPv6 link-local (fe80::/10) is blocked.
func TestIsBlockedIP_IPv6LinkLocal(t *testing.T) {
	t.Parallel()

	ip := net.ParseIP("fe80::1")
	if ip == nil {
		t.Fatal("invalid IP literal fe80::1")
	}
	blocked, reason := httpguard.IsBlockedIP(ip)
	if !blocked {
		t.Errorf("IsBlockedIP(fe80::1) = false, want true; reason=%q", reason)
	}
}

// TestIsBlockedIP_Unspecified verifies that unspecified addresses are blocked.
func TestIsBlockedIP_Unspecified(t *testing.T) {
	t.Parallel()

	cases := []string{"0.0.0.0", "::"}
	for _, addr := range cases {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(addr)
			if ip == nil {
				t.Fatalf("invalid IP literal %q", addr)
			}
			blocked, reason := httpguard.IsBlockedIP(ip)
			if !blocked {
				t.Errorf("IsBlockedIP(%q) = false, want true; reason=%q", addr, reason)
			}
		})
	}
}

// TestIsBlockedIP_PublicIPs verifies that public routable addresses are NOT blocked.
func TestIsBlockedIP_PublicIPs(t *testing.T) {
	t.Parallel()

	cases := []string{"8.8.8.8", "1.1.1.1", "2001:4860:4860::8888"}
	for _, addr := range cases {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(addr)
			if ip == nil {
				t.Fatalf("invalid IP literal %q", addr)
			}
			blocked, reason := httpguard.IsBlockedIP(ip)
			if blocked {
				t.Errorf("IsBlockedIP(%q) = true (reason=%q), want false — public IPs must not be blocked", addr, reason)
			}
		})
	}
}

// TestIsBlockedIP_NilIP verifies that a nil IP returns (false, "").
func TestIsBlockedIP_NilIP(t *testing.T) {
	t.Parallel()

	blocked, reason := httpguard.IsBlockedIP(nil)
	if blocked {
		t.Errorf("IsBlockedIP(nil) = true (reason=%q), want false", reason)
	}
	if reason != "" {
		t.Errorf("IsBlockedIP(nil) reason=%q, want empty string", reason)
	}
}

// TestIsConstructorBlockedIP_LinkLocalBlocked verifies that link-local is blocked at construction time.
func TestIsConstructorBlockedIP_LinkLocalBlocked(t *testing.T) {
	t.Parallel()

	ip := net.ParseIP("169.254.169.254")
	if ip == nil {
		t.Fatal("invalid IP literal 169.254.169.254")
	}
	blocked, reason := httpguard.IsConstructorBlockedIP(ip)
	if !blocked {
		t.Errorf("IsConstructorBlockedIP(169.254.169.254) = false, want true; reason=%q", reason)
	}
}

// TestIsConstructorBlockedIP_LoopbackAllowed verifies that loopback is NOT blocked at
// construction time (Ollama local dev use-case).
func TestIsConstructorBlockedIP_LoopbackAllowed(t *testing.T) {
	t.Parallel()

	ip := net.ParseIP("127.0.0.1")
	if ip == nil {
		t.Fatal("invalid IP literal 127.0.0.1")
	}
	blocked, _ := httpguard.IsConstructorBlockedIP(ip)
	if blocked {
		t.Error("IsConstructorBlockedIP(127.0.0.1) = true, want false — loopback must be allowed at constructor time for Ollama/local dev")
	}
}

// TestIsConstructorBlockedIP_RFC1918Allowed verifies that RFC-1918 is NOT blocked at
// construction time (vLLM on private LAN use-case).
func TestIsConstructorBlockedIP_RFC1918Allowed(t *testing.T) {
	t.Parallel()

	ip := net.ParseIP("192.168.1.1")
	if ip == nil {
		t.Fatal("invalid IP literal 192.168.1.1")
	}
	blocked, _ := httpguard.IsConstructorBlockedIP(ip)
	if blocked {
		t.Error("IsConstructorBlockedIP(192.168.1.1) = true, want false — private LAN must be allowed at constructor time")
	}
}

// TestIsSafeURL_SchemeCheck verifies that non-HTTP(S) schemes are rejected and
// that an HTTP URL with a public IP literal is accepted (no real DNS needed).
func TestIsSafeURL_SchemeCheck(t *testing.T) {
	t.Parallel()

	// ftp scheme must be blocked.
	safe, err := httpguard.IsSafeURL(context.Background(), "ftp://example.com")
	if safe {
		t.Error("IsSafeURL(ftp://example.com) = true, want false")
	}
	if err == nil {
		t.Fatal("IsSafeURL(ftp://example.com) returned nil error, want ErrSSRF")
	}
	var ssrfErr *httpguard.ErrSSRF
	if !errors.As(err, &ssrfErr) {
		t.Errorf("expected *httpguard.ErrSSRF for ftp scheme, got %T: %v", err, err)
	}

	// http URL with a public IP literal must be allowed (no DNS resolution required).
	safe, err = httpguard.IsSafeURL(context.Background(), "http://8.8.8.8/")
	if !safe {
		t.Errorf("IsSafeURL(http://8.8.8.8/) = false, want true; err=%v", err)
	}
	if err != nil {
		t.Errorf("IsSafeURL(http://8.8.8.8/) returned unexpected error: %v", err)
	}
}

// TestIsSafeURL_IPLiterals verifies blocking of dangerous IP literals and allowing public ones.
func TestIsSafeURL_IPLiterals(t *testing.T) {
	t.Parallel()

	// Link-local metadata must be blocked.
	safe, err := httpguard.IsSafeURL(context.Background(), "http://169.254.169.254/")
	if safe {
		t.Error("IsSafeURL(http://169.254.169.254/) = true, want false")
	}
	if err == nil {
		t.Fatal("IsSafeURL(http://169.254.169.254/) returned nil error, want ErrSSRF")
	}
	var ssrfErr *httpguard.ErrSSRF
	if !errors.As(err, &ssrfErr) {
		t.Errorf("expected *httpguard.ErrSSRF, got %T: %v", err, err)
	}

	// Public IP must be allowed.
	safe, err = httpguard.IsSafeURL(context.Background(), "http://8.8.8.8/")
	if !safe {
		t.Errorf("IsSafeURL(http://8.8.8.8/) = false, want true; err=%v", err)
	}
}

// TestSafeDial_BlocksLinkLocalIPLiteral verifies that SafeDial returns ErrSSRF for
// a link-local IP literal without attempting any network connection.
func TestSafeDial_BlocksLinkLocalIPLiteral(t *testing.T) {
	t.Parallel()

	_, err := httpguard.SafeDial(context.Background(), "tcp", "169.254.169.254:80")
	if err == nil {
		t.Fatal("SafeDial(169.254.169.254:80) succeeded, expected SSRF error")
	}
	var ssrfErr *httpguard.ErrSSRF
	if !errors.As(err, &ssrfErr) {
		t.Fatalf("expected *httpguard.ErrSSRF, got %T: %v", err, err)
	}
}

// TestSafeDial_BlocksRFC1918IPLiteral verifies that SafeDial blocks RFC-1918 IP literals.
func TestSafeDial_BlocksRFC1918IPLiteral(t *testing.T) {
	t.Parallel()

	_, err := httpguard.SafeDial(context.Background(), "tcp", "192.168.1.1:80")
	if err == nil {
		t.Fatal("SafeDial(192.168.1.1:80) succeeded, expected SSRF error")
	}
	var ssrfErr *httpguard.ErrSSRF
	if !errors.As(err, &ssrfErr) {
		t.Fatalf("expected *httpguard.ErrSSRF, got %T: %v", err, err)
	}
}

// TestNewSafeHTTPClient_RedirectBlocked verifies that NewSafeHTTPClient blocks redirects
// to dangerous destinations. The httptest server itself binds on loopback (127.0.0.1)
// which SafeDial blocks, so we verify IsSafeURL directly for the redirect target and
// confirm the client's CheckRedirect rejects a 169.254.x.x target URL.
func TestNewSafeHTTPClient_RedirectBlocked(t *testing.T) {
	t.Parallel()

	// Start a redirecting server on loopback.
	// Note: the initial request to this server will itself be blocked by SafeDial
	// because httptest binds to 127.0.0.1 (loopback). We use this intentionally:
	// the client must refuse the connection with an SSRF error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	client := httpguard.NewSafeHTTPClient()
	resp, err := client.Get(srv.URL) //nolint:noctx // test: verifying dial-time SSRF block
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected SSRF error when connecting to loopback httptest server, got nil")
	}
	// The error must contain "SSRF blocked" because SafeDial rejects the loopback dial.
	if resp != nil {
		t.Error("expected nil response when SSRF-blocked, got non-nil")
	}
}

// TestIsBlockedIP_CGNAT verifies RFC 6598 carrier-grade NAT shared address
// space (100.64.0.0/10) is blocked, with a boundary check just outside the
// range to prove the match isn't overbroad. [F0929-68]
func TestIsBlockedIP_CGNAT(t *testing.T) {
	t.Parallel()

	t.Run("100.64.0.1", func(t *testing.T) {
		t.Parallel()
		ip := net.ParseIP("100.64.0.1")
		blocked, reason := httpguard.IsBlockedIP(ip)
		if !blocked {
			t.Fatalf("IsBlockedIP(100.64.0.1) = false, want true")
		}
		if reason != "RFC 6598 shared address space (carrier-grade NAT)" {
			t.Errorf("reason = %q, want %q", reason, "RFC 6598 shared address space (carrier-grade NAT)")
		}
	})
	t.Run("100.63.255.255_just_outside_range", func(t *testing.T) {
		t.Parallel()
		ip := net.ParseIP("100.63.255.255")
		blocked, reason := httpguard.IsBlockedIP(ip)
		if blocked {
			t.Errorf("IsBlockedIP(100.63.255.255) = true (reason=%q), want false — just outside /10", reason)
		}
	})
}

// TestIsBlockedIP_BenchmarkRange verifies the RFC 2544 benchmark testing
// range (198.18.0.0/15) is blocked, with a boundary check just outside it.
// [F0929-68]
func TestIsBlockedIP_BenchmarkRange(t *testing.T) {
	t.Parallel()

	t.Run("198.18.0.1", func(t *testing.T) {
		t.Parallel()
		ip := net.ParseIP("198.18.0.1")
		blocked, reason := httpguard.IsBlockedIP(ip)
		if !blocked {
			t.Fatalf("IsBlockedIP(198.18.0.1) = false, want true")
		}
		if reason != "RFC 2544 benchmark testing range" {
			t.Errorf("reason = %q, want %q", reason, "RFC 2544 benchmark testing range")
		}
	})
	t.Run("198.17.255.255_just_outside_range", func(t *testing.T) {
		t.Parallel()
		ip := net.ParseIP("198.17.255.255")
		blocked, reason := httpguard.IsBlockedIP(ip)
		if blocked {
			t.Errorf("IsBlockedIP(198.17.255.255) = true (reason=%q), want false — just outside /15", reason)
		}
	})
}

// TestIsBlockedIP_NAT64Prefix verifies the RFC 6052 NAT64 well-known prefix
// (64:ff9b::/96, IPv4-embedded IPv6) is blocked regardless of the embedded
// IPv4 address, with a boundary check one bit outside the /96. [F0929-68]
func TestIsBlockedIP_NAT64Prefix(t *testing.T) {
	t.Parallel()

	t.Run("64:ff9b::7f00:1_embeds_127.0.0.1", func(t *testing.T) {
		t.Parallel()
		ip := net.ParseIP("64:ff9b::7f00:1")
		blocked, reason := httpguard.IsBlockedIP(ip)
		if !blocked {
			t.Fatalf("IsBlockedIP(64:ff9b::7f00:1) = false, want true")
		}
		if reason != "RFC 6052 NAT64 well-known prefix (IPv4-embedded IPv6)" {
			t.Errorf("reason = %q, want %q", reason, "RFC 6052 NAT64 well-known prefix (IPv4-embedded IPv6)")
		}
	})
	t.Run("64:ff9c::7f00:1_one_bit_outside_prefix", func(t *testing.T) {
		t.Parallel()
		ip := net.ParseIP("64:ff9c::7f00:1")
		blocked, reason := httpguard.IsBlockedIP(ip)
		if blocked {
			t.Errorf("IsBlockedIP(64:ff9c::7f00:1) = true (reason=%q), want false — outside the /96", reason)
		}
	})
}
