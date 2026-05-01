package storage_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/storage"
)

// generateSelfSignedCA creates a minimal self-signed CA certificate and
// returns its PEM-encoded form. Used only in unit tests.
func generateSelfSignedCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// writeTempPEM writes content to a temp file and returns the path.
func writeTempPEM(t *testing.T, content []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "ca-*.pem")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	return f.Name()
}

func TestBuildTLSConfig(t *testing.T) {
	t.Parallel()

	validPEM := generateSelfSignedCA(t)
	pemPath := writeTempPEM(t, validPEM)

	cases := []struct {
		name          string
		appEnv        string
		pgsslrootcert string
		wantSentinel  error
		wantAnyErr    bool
		wantNil       bool
		checkTLSPool  bool
	}{
		{
			name:          "valid cert in production returns custom CA pool",
			appEnv:        "production",
			pgsslrootcert: pemPath,
			wantNil:       false,
			checkTLSPool:  true,
		},
		{
			name:          "valid cert in staging returns custom CA pool",
			appEnv:        "staging",
			pgsslrootcert: pemPath,
			wantNil:       false,
			checkTLSPool:  true,
		},
		{
			name:          "no cert + production → ErrMissingPGSSLROOTCERT",
			appEnv:        "production",
			pgsslrootcert: "",
			wantSentinel:  storage.ErrMissingPGSSLROOTCERT,
		},
		{
			name:          "no cert + staging → nil (use system CA)",
			appEnv:        "staging",
			pgsslrootcert: "",
			wantNil:       true,
		},
		{
			name:          "no cert + empty APP_ENV → nil (use system CA)",
			appEnv:        "",
			pgsslrootcert: "",
			wantNil:       true,
		},
		{
			name:          "cert path set but file missing → read error",
			appEnv:        "production",
			pgsslrootcert: filepath.Join(t.TempDir(), "nonexistent.pem"),
			wantAnyErr:    true,
		},
		{
			name:          "cert path set but file contains no valid PEM block",
			appEnv:        "production",
			pgsslrootcert: writeTempPEM(t, []byte("not a certificate")),
			wantAnyErr:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := storage.BuildTLSConfig(tc.appEnv, tc.pgsslrootcert)

			if tc.wantSentinel != nil {
				if !errors.Is(err, tc.wantSentinel) {
					t.Fatalf("want sentinel error %v, got %v", tc.wantSentinel, err)
				}
				return
			}

			if tc.wantAnyErr {
				if err == nil {
					t.Fatal("expected an error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil *tls.Config, got %+v", got)
				}
				return
			}

			if got == nil {
				t.Fatal("expected non-nil *tls.Config")
			}
			if tc.checkTLSPool {
				if got.RootCAs == nil {
					t.Fatal("expected RootCAs to be populated")
				}
				// InsecureSkipVerify MUST remain false.
				if got.InsecureSkipVerify {
					t.Fatal("InsecureSkipVerify must not be set on a properly constructed TLS config")
				}
			}
		})
	}
}

// TestBuildTLSConfig_InsecureSkipVerifyNeverSet verifies that no reachable
// code path in BuildTLSConfig ever sets InsecureSkipVerify=true.
func TestBuildTLSConfig_InsecureSkipVerifyNeverSet(t *testing.T) {
	t.Parallel()

	validPEM := generateSelfSignedCA(t)
	pemPath := writeTempPEM(t, validPEM)

	inputs := []struct{ appEnv, cert string }{
		{"production", pemPath},
		{"staging", pemPath},
		{"", pemPath},
		{"staging", ""},
		{"", ""},
	}

	for _, in := range inputs {
		cfg, err := storage.BuildTLSConfig(in.appEnv, in.cert)
		if err != nil {
			continue
		}
		if cfg == nil {
			continue
		}
		if cfg.InsecureSkipVerify {
			t.Errorf("appEnv=%q cert=%q: InsecureSkipVerify must never be true", in.appEnv, in.cert)
		}
	}
}

// Compile-time check: BuildTLSConfig has the expected signature.
var _ func(string, string) (*tls.Config, error) = storage.BuildTLSConfig
