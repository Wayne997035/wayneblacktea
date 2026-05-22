package storage

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

// Regression test for the libpq-compat trap: pgxpool.ParseConfig honours the
// PGSSLROOTCERT env var and calls os.ReadFile on it unconditionally. When the
// env var holds inline PEM content (Railway/Fly cloud deploys), the parse
// step blew up with `open -----BEGIN CERTIFICATE-----…: no such file or
// directory` before BuildTLSConfig was ever reached. buildPgxPool now
// shadows the env var around ParseConfig when the value is inline PEM.
func TestBuildPgxPool_InlinePEMDoesNotTripPgxFileRead(t *testing.T) {
	pemBytes := generateTestCA(t)
	inline := string(pemBytes)

	t.Setenv("PGSSLROOTCERT", inline)

	// 127.0.0.1:1 — parse must succeed; connection failure is expected and
	// fine, what we are checking is that parse does NOT fail with a file
	// read error on the inline PEM value.
	dsn := "postgres://test:test@127.0.0.1:1/test?sslmode=require" //nolint:gosec // fake DSN for parse-only test, never connects

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := buildPgxPool(ctx, dsn, "production", inline)
	if err == nil {
		// Pool creation may succeed (pgxpool is lazy). That itself is fine —
		// it means parse worked. Bail out cleanly.
		return
	}
	msg := err.Error()
	if strings.Contains(msg, "no such file") || strings.Contains(msg, "open -----BEGIN") {
		t.Fatalf("pgx still tried to read inline PEM as file path; env-shadow failed: %v", err)
	}
	// Any other error (connect refused, timeout) means we got past parse.
}

// generateTestCA is a local copy of the helper used in tlsconfig_test.go,
// duplicated here because that file lives in package storage_test while this
// test must be in-package to reach unexported buildPgxPool.
func generateTestCA(t *testing.T) []byte {
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
