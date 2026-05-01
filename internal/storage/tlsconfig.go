package storage

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// ErrMissingPGSSLROOTCERT is returned when APP_ENV=production and
// PGSSLROOTCERT is not set. Fail-fast at boot is safer than a silent
// unverified connection.
var ErrMissingPGSSLROOTCERT = errors.New("PGSSLROOTCERT required in production")

// BuildTLSConfig constructs a *tls.Config appropriate for the given environment.
//
//   - PGSSLROOTCERT set and file readable → custom CA pool, full verification
//   - PGSSLROOTCERT set but file unreadable → error (misconfigured deploy)
//   - PGSSLROOTCERT not set + APP_ENV=production → ErrMissingPGSSLROOTCERT
//   - PGSSLROOTCERT not set + APP_ENV != production → nil, nil (system CA pool)
func BuildTLSConfig(appEnv, pgsslrootcert string) (*tls.Config, error) {
	if pgsslrootcert == "" {
		if appEnv == "production" {
			return nil, ErrMissingPGSSLROOTCERT
		}
		// Non-production with no cert path: fall back to system CA pool.
		return nil, nil //nolint:nilnil // intentional: nil means "use system CA pool", documented in godoc
	}

	pem, err := os.ReadFile(pgsslrootcert) //nolint:gosec // path comes from PGSSLROOTCERT env, operator-controlled
	if err != nil {
		return nil, fmt.Errorf("read PGSSLROOTCERT %s: %w", pgsslrootcert, err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("PGSSLROOTCERT %s: no valid PEM certificates found", pgsslrootcert)
	}

	return &tls.Config{RootCAs: pool}, nil
}
