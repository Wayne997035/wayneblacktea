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
	if cfg.MaxConns != 8 {
		t.Errorf("MaxConns = %d, want 8 (uncapped pool exhausts Aiven; 4 starved the "+
			"6-way get_today_context fan-out)", cfg.MaxConns)
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
//
// The DSN value is deliberately NOT the default (8): an override test that
// asks for the number the code would have produced anyway proves nothing.
func TestBuildPgxPoolConfig_RespectsDSNOverride(t *testing.T) {
	dsn := "postgres://localhost:5432/db?sslmode=disable&pool_max_conns=12"
	cfg, err := buildPgxPoolConfig(dsn, "", "")
	if err != nil {
		t.Fatalf("buildPgxPoolConfig: %v", err)
	}
	if cfg.MaxConns != 12 {
		t.Errorf("MaxConns = %d, want 12 (explicit DSN override must win)", cfg.MaxConns)
	}
}

// TestBuildPgxPoolConfig_MaxConnsBracketsTodayContextFanOut pins the two
// constraints that fix the default, rather than just the number itself:
//
//   - floor: mcp.fetchTodayContext issues 6 concurrent store lookups, so a
//     pool below 6 lets one session-start request saturate it and queue every
//     other caller (PR #156 security review M-2, the regression this guards).
//   - ceiling: 10 is the hard limit for this deployment's shared connection
//     budget (two server instances during a Railway redeploy, plus the
//     MaxConns=2 hook/doctor/reembed/guard pools).
//
// Either bound being crossed is a real operational fault, so both are pinned
// here instead of only in a comment.
func TestBuildPgxPoolConfig_MaxConnsBracketsTodayContextFanOut(t *testing.T) {
	const (
		todayContextFanOut = 6
		poolCeiling        = 10
	)
	cfg, err := buildPgxPoolConfig(dsnNoPoolParams, "", "")
	if err != nil {
		t.Fatalf("buildPgxPoolConfig: %v", err)
	}
	if cfg.MaxConns < todayContextFanOut {
		t.Errorf("MaxConns = %d, must be >= %d — get_today_context fans out to %d "+
			"concurrent lookups and would saturate the pool on its own",
			cfg.MaxConns, todayContextFanOut, todayContextFanOut)
	}
	if cfg.MaxConns > poolCeiling {
		t.Errorf("MaxConns = %d, must be <= %d — the connection budget is shared with "+
			"redeploy overlap and the MaxConns=2 CLI/hook pools",
			cfg.MaxConns, poolCeiling)
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
