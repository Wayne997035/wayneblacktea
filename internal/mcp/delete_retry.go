package mcp

import (
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// deadlockSQLState is PostgreSQL's deadlock_detected.
const deadlockSQLState = "40P01"

// deleteDeadlockRetries is how many times a confirmed delete is run again after PostgreSQL picked it as a
// deadlock victim; the first call is not counted, so a delete is attempted at most 1+3 times.
const deleteDeadlockRetries = 3

// deleteDeadlockBackoff is the pause before retry n: n times this.
const deleteDeadlockBackoff = 50 * time.Millisecond

// retryDeadlockVictim runs call, and runs it again while it fails with SQLSTATE 40P01, at most
// deleteDeadlockRetries more times. Any other outcome, including a nil error, is returned as it is; when the
// retries run out the last 40P01 is returned, so the caller reports it as it does today. [F203-01]
//
// It lives in the MCP tool and not in the Store on purpose. The Store probes (TestE979_*, F202) call
// Store.DeleteTask and Store.DeleteProjectExpecting directly; a retry inside the Store would absorb the
// deadlock they exist to catch, and they would stay green with the pre-lock removed.
//
// A retry is safe because spendPendingDeletion has already consumed the confirmation code, and because
// every Store call opens its own transaction and rolls it back whole when it fails, so the victim leaves no
// row changed and no tombstone behind (TestPGDeleteLocks_ResidualRingVictimRollsBackWhole [F203-02]). The
// error is matched through the %w chain: the Store wraps what the database returned with the step that failed.
func retryDeadlockVictim[T any](op string, call func() (T, error)) (T, error) {
	v, err := call()
	for attempt := 1; attempt <= deleteDeadlockRetries && isDeadlockVictim(err); attempt++ {
		slog.Warn("delete: deadlock victim, retrying", "op", op, "attempt", attempt)
		time.Sleep(time.Duration(attempt) * deleteDeadlockBackoff)
		v, err = call()
	}
	return v, err
}

// isDeadlockVictim reports whether err, or an error it wraps, is a PostgreSQL error with SQLSTATE 40P01.
func isDeadlockVictim(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == deadlockSQLState
}
