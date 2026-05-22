package storage

import (
	"crypto/x509"
	"os"
	"strings"
	"testing"
)

// TestMaterializePGSSLROOTCERT_InlinePEM verifies the inline-PEM → temp-file
// round-trip that protects RunMigrations (and any other pgconn-backed code) from
// the "file name too long" error that occurs when PGSSLROOTCERT holds PEM content
// instead of a file path.
//
// This test was added AFTER the bug was discovered in production: RunMigrations
// was failing because golang-migrate's pgconn unconditionally calls
// os.ReadFile(PGSSLROOTCERT), and the existing unit tests only covered
// BuildTLSConfig and buildPgxPool in isolation — neither exercised RunMigrations
// with an inline-PEM env var.
func TestMaterializePGSSLROOTCERT_InlinePEM(t *testing.T) {
	// Use the real ca.pem from the repo so this test exercises the exact bytes
	// that Railway stores in PGSSLROOTCERT.
	pemBytes, err := os.ReadFile("../../ca.pem")
	if err != nil {
		t.Skip("ca.pem not found — skipping inline-PEM materialize test")
	}
	inlinePEM := strings.TrimSpace(string(pemBytes))

	// Pre-condition: PGSSLROOTCERT is inline PEM, not a file path.
	if !strings.HasPrefix(inlinePEM, pemCertPrefix) {
		t.Fatalf("ca.pem does not start with %q; test setup is wrong", pemCertPrefix)
	}

	t.Setenv("PGSSLROOTCERT", inlinePEM)

	cleanup, err := materializePGSSLROOTCERT()
	if err != nil {
		t.Fatalf("materializePGSSLROOTCERT() returned error: %v", err)
	}

	// After materialization PGSSLROOTCERT must be a real file path (not PEM).
	materialized := os.Getenv("PGSSLROOTCERT")
	if strings.HasPrefix(materialized, pemCertPrefix) {
		t.Error("PGSSLROOTCERT still looks like inline PEM after materialize — env shadow did not apply")
	}

	// The temp file must exist and contain valid x509 PEM.
	if _, statErr := os.Stat(materialized); statErr != nil {
		t.Errorf("temp file %q does not exist: %v", materialized, statErr)
	}
	content, readErr := os.ReadFile(materialized) //nolint:gosec // temp file path written by materializePGSSLROOTCERT
	if readErr != nil {
		t.Fatalf("cannot read temp file %q: %v", materialized, readErr)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(content) {
		t.Error("temp file does not contain a valid PEM certificate")
	}

	// After cleanup the env var must be restored and the temp file deleted.
	cleanup()

	if got := os.Getenv("PGSSLROOTCERT"); got != inlinePEM {
		t.Errorf("after cleanup PGSSLROOTCERT = %q, want original inline PEM", got[:min(len(got), 40)])
	}
	if _, statErr := os.Stat(materialized); !os.IsNotExist(statErr) {
		t.Errorf("temp file %q still exists after cleanup", materialized)
	}
}

// TestMaterializePGSSLROOTCERT_FilePath verifies the function is a no-op when
// PGSSLROOTCERT already holds a file path.
func TestMaterializePGSSLROOTCERT_FilePath(t *testing.T) {
	const fakePath = "/fake/path/ca.pem"
	t.Setenv("PGSSLROOTCERT", fakePath)

	cleanup, err := materializePGSSLROOTCERT()
	if err != nil {
		t.Fatalf("unexpected error for file-path value: %v", err)
	}
	defer cleanup()

	if got := os.Getenv("PGSSLROOTCERT"); got != fakePath {
		t.Errorf("PGSSLROOTCERT changed to %q, want %q (no-op for file paths)", got, fakePath)
	}
}

// TestMaterializePGSSLROOTCERT_Unset verifies the function is a no-op when
// PGSSLROOTCERT is not set at all.
func TestMaterializePGSSLROOTCERT_Unset(t *testing.T) {
	t.Setenv("PGSSLROOTCERT", "")
	_ = os.Unsetenv("PGSSLROOTCERT")

	cleanup, err := materializePGSSLROOTCERT()
	if err != nil {
		t.Fatalf("unexpected error for unset PGSSLROOTCERT: %v", err)
	}
	defer cleanup()

	if got := os.Getenv("PGSSLROOTCERT"); got != "" {
		t.Errorf("PGSSLROOTCERT set to %q, want empty (no-op when unset)", got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
