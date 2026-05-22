package storage

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrMissingPGSSLROOTCERT is returned when APP_ENV=production and
// PGSSLROOTCERT is not set. Fail-fast at boot is safer than a silent
// unverified connection.
var ErrMissingPGSSLROOTCERT = errors.New("PGSSLROOTCERT required in production")

// pemCertPrefix marks PGSSLROOTCERT values that hold inline PEM content
// rather than a file path. Cloud platforms (Railway / Fly / etc.) often have
// only env vars and no secret-file mounting, so accepting inline PEM avoids
// committing the cert or hacking the Dockerfile.
const pemCertPrefix = "-----BEGIN CERTIFICATE-----"

// BuildTLSConfig constructs a *tls.Config appropriate for the given environment.
//
//   - PGSSLROOTCERT starts with "-----BEGIN CERTIFICATE-----" → inline PEM content
//   - PGSSLROOTCERT set otherwise → file path; read file as PEM
//   - PGSSLROOTCERT file unreadable OR no valid PEM → error (misconfigured deploy)
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

	var pem []byte
	if trimmed := strings.TrimSpace(pgsslrootcert); strings.HasPrefix(trimmed, pemCertPrefix) {
		pem = []byte(trimmed)
	} else {
		var err error
		pem, err = os.ReadFile(pgsslrootcert) //nolint:gosec // path comes from PGSSLROOTCERT env, operator-controlled
		if err != nil {
			return nil, fmt.Errorf("read PGSSLROOTCERT %s: %w", pgsslrootcert, err)
		}
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("PGSSLROOTCERT: no valid PEM certificates found")
	}

	return &tls.Config{RootCAs: pool}, nil
}
