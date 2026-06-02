package storage

import (
	"testing"
	"time"
)

// dsnNoPoolParams is a syntactically valid pgx DSN that carries no pool_*
// tuning params, so buildPgxPoolConfig must apply our personal-OS caps.
const dsnNoPoolParams = "postgres://localhost:5432/db?sslmode=disable"

// TestBuildPgxPoolConfig_AppliesCaps is the regression guard for PR #147: it
// fails if any of the Aiven connection-exhaustion caps are reverted.
func TestBuildPgxPoolConfig_AppliesCaps(t *testing.T) {
	cfg, err := buildPgxPoolConfig(dsnNoPoolParams, "", "")
	if err != nil {
		t.Fatalf("buildPgxPoolConfig: %v", err)
	}
	if cfg.MaxConns != 4 {
		t.Errorf("MaxConns = %d, want 4 (uncapped pool exhausts Aiven)", cfg.MaxConns)
	}
	if cfg.MinConns != 0 {
		t.Errorf("MinConns = %d, want 0 (no standing connections)", cfg.MinConns)
	}
	if cfg.MaxConnIdleTime != 5*time.Minute {
		t.Errorf("MaxConnIdleTime = %v, want 5m", cfg.MaxConnIdleTime)
	}
	if cfg.MaxConnLifetime != 30*time.Minute {
		t.Errorf("MaxConnLifetime = %v, want 30m", cfg.MaxConnLifetime)
	}
}

// TestBuildPgxPoolConfig_RespectsDSNOverride proves the caps defer to an
// explicit pool_max_conns in the DSN — an operator can still tune up.
func TestBuildPgxPoolConfig_RespectsDSNOverride(t *testing.T) {
	dsn := "postgres://localhost:5432/db?sslmode=disable&pool_max_conns=8"
	cfg, err := buildPgxPoolConfig(dsn, "", "")
	if err != nil {
		t.Fatalf("buildPgxPoolConfig: %v", err)
	}
	if cfg.MaxConns != 8 {
		t.Errorf("MaxConns = %d, want 8 (explicit DSN override must win)", cfg.MaxConns)
	}
}

// TestBuildPgxPoolConfig_JitterParamDoesNotSuppressCap guards the reviewer's
// substring finding: pool_max_conn_lifetime_jitter shares a prefix with
// pool_max_conn_lifetime but must NOT suppress the 30-minute lifetime cap.
func TestBuildPgxPoolConfig_JitterParamDoesNotSuppressCap(t *testing.T) {
	dsn := "postgres://localhost:5432/db?sslmode=disable&pool_max_conn_lifetime_jitter=1m"
	cfg, err := buildPgxPoolConfig(dsn, "", "")
	if err != nil {
		t.Fatalf("buildPgxPoolConfig: %v", err)
	}
	if cfg.MaxConnLifetime != 30*time.Minute {
		t.Errorf("MaxConnLifetime = %v, want 30m (jitter param must not skip cap)", cfg.MaxConnLifetime)
	}
}
