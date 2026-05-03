package guard

import (
	"context"
	"encoding/json"
	"testing"
)

// TestStore_WriteEvent_NilPool verifies fail-open: nil pool → no error, no panic.
func TestStore_WriteEvent_NilPool(t *testing.T) {
	t.Parallel()
	store := NewStore(nil)
	ev := Event{
		SessionID:  "test-session",
		ToolName:   "Bash",
		ToolInput:  json.RawMessage(`{"command":"ls"}`),
		CWD:        "/repo",
		RepoName:   "repo",
		RiskTier:   T0,
		RiskReason: "read-only",
		WouldDeny:  false,
		Matcher:    "bash",
	}
	err := store.WriteEvent(context.Background(), ev)
	if err != nil {
		t.Errorf("WriteEvent(nil pool) = %v, want nil (fail-open)", err)
	}
}

// TestStore_WriteEvent_NilStore verifies nil Store receiver is handled gracefully.
func TestStore_WriteEvent_NilStore(t *testing.T) {
	t.Parallel()
	var store *Store
	ev := Event{
		ToolName:  "Bash",
		ToolInput: json.RawMessage(`{}`),
		RiskTier:  T0,
		Matcher:   "bash",
	}
	err := store.WriteEvent(context.Background(), ev)
	if err != nil {
		t.Errorf("WriteEvent(nil store) = %v, want nil (fail-open)", err)
	}
}

// TestStore_FindBypass_NilPool verifies fail-open: nil pool returns nil, nil.
func TestStore_FindBypass_NilPool(t *testing.T) {
	t.Parallel()
	store := NewStore(nil)
	b, err := store.FindBypass(context.Background(), "repo", "myrepo", "Bash")
	if err != nil {
		t.Errorf("FindBypass(nil pool) error = %v, want nil", err)
	}
	if b != nil {
		t.Errorf("FindBypass(nil pool) = %v, want nil", b)
	}
}

// TestOpenPool_EmptyURL verifies that empty URL returns nil pool without error.
func TestOpenPool_EmptyURL(t *testing.T) {
	t.Parallel()
	pool, err := OpenPool(context.Background(), "")
	if err != nil {
		t.Errorf("OpenPool('') error = %v, want nil", err)
	}
	if pool != nil {
		t.Errorf("OpenPool('') pool = %v, want nil", pool)
	}
}

// TestOpenPool_InvalidURL verifies that an invalid DB URL returns nil pool without error.
func TestOpenPool_InvalidURL(t *testing.T) {
	t.Parallel()
	pool, err := OpenPool(context.Background(), "not-a-valid-url-$$$$")
	if err != nil {
		t.Errorf("OpenPool(invalid URL) error = %v, want nil (fail-open)", err)
	}
	if pool != nil {
		t.Errorf("OpenPool(invalid URL) pool = %v, want nil (fail-open)", pool)
	}
}

// TestOpenPool_RefusedConnection verifies that a refused connection returns nil pool without error.
// This simulates the "DB unavailable" case wbt-guard must handle fail-open.
func TestOpenPool_RefusedConnection(t *testing.T) {
	t.Parallel()
	// Port 19999 should not have a Postgres instance.
	//nolint:gosec // G101: synthetic DSN with placeholder credentials, only used to assert fail-open behaviour.
	const refusedDSN = "postgres://user:pass@localhost:19999/db?connect_timeout=1"
	pool, err := OpenPool(context.Background(), refusedDSN)
	if err != nil {
		t.Errorf("OpenPool(refused) error = %v, want nil (fail-open)", err)
	}
	if pool != nil {
		// pgxpool.NewWithConfig is lazy — it doesn't actually connect until first use,
		// so this test mainly verifies the function does not panic.
		// Close pool to avoid resource leak.
		pool.Close()
	}
}
