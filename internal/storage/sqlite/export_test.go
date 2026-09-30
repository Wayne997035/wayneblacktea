package sqlite

import (
	"context"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// ExecUpdateTaskStatusGuardedSQLForTest runs updateTaskStatusGuardedSQL
// directly against s's underlying connection, bypassing
// UpdateTaskStatusGuarded's Go-layer assignee pre-read entirely, so a test
// can prove the SQL WHERE clause alone — not the pre-read — rejects a blank
// assignee. Returns the number of rows the UPDATE affected (0 = rejected /
// no match, 1 = written). File ends in _test.go so none of this ships in the
// production binary (same export_test.go pattern as
// internal/knowledge/export_test.go).
func ExecUpdateTaskStatusGuardedSQLForTest(
	s *GTDStore, ctx context.Context, id uuid.UUID, newStatus, expected gtd.TaskStatus,
) (int64, error) {
	now := nowRFC3339()
	res, err := s.db.conn.ExecContext(ctx, updateTaskStatusGuardedSQL,
		id.String(), string(newStatus), now, s.db.workspaceArg(), string(expected), gtd.AssigneeSpaceChars)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ExecUpdateTaskStatusSQLForTest runs updateTaskStatusSQL directly against
// s's underlying connection, bypassing UpdateTaskStatus's Go-layer assignee
// pre-read entirely, so a test can prove the [F0930-09] SQL WHERE clause
// alone — not the pre-read — rejects a blank assignee when newStatus is
// in_progress. Returns the number of rows the UPDATE affected (0 = rejected
// / no match, 1 = written). File ends in _test.go so none of this ships in
// the production binary (same export_test.go pattern as
// ExecUpdateTaskStatusGuardedSQLForTest above).
func ExecUpdateTaskStatusSQLForTest(
	s *GTDStore, ctx context.Context, id uuid.UUID, newStatus gtd.TaskStatus,
) (int64, error) {
	now := nowRFC3339()
	res, err := s.db.conn.ExecContext(ctx, updateTaskStatusSQL,
		id.String(), string(newStatus), now, s.db.workspaceArg(), gtd.AssigneeSpaceChars)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// WithUpdateTaskStatusGuardedTestHook attaches hook to ctx so a subsequent
// UpdateTaskStatusGuarded call runs it synchronously between its pre-read
// and its guarded SQL UPDATE — see updateTaskStatusGuardedHookKeyType's doc
// comment (gtd.go) for why this is the only deterministic way to exercise
// [F0930-07]'s reread-branch status-vs-assignee ordering fix.
func WithUpdateTaskStatusGuardedTestHook(ctx context.Context, hook func()) context.Context {
	return context.WithValue(ctx, updateTaskStatusGuardedHookKey, hook)
}

// ExecBeginTaskStatusSQLForTest runs beginTaskStatusSQL directly against s's
// underlying connection inside its own transaction, bypassing
// BeginTaskOrchestration's Go-layer pre-tx assignee check entirely, so a
// test can prove the [F0930-10] SQL WHERE clause alone — not the pre-tx
// check — rejects a blank assignee. The transaction is committed (rows
// affected > 0) or rolled back (0 rows) before this function returns:
// SQLite's single pooled connection (db.go's SetMaxOpenConns(1)) means a
// caller re-reading the row on the same connection while this tx is still
// open would deadlock waiting for a connection this same goroutine already
// holds — see sqliteBeginTaskAdapter.ResolveGuardBlocked's doc comment for
// the identical constraint on the production code path. Returns the number
// of rows the UPDATE affected (0 = rejected, 1 = written).
func ExecBeginTaskStatusSQLForTest(s *GTDStore, ctx context.Context, id uuid.UUID) (int64, error) {
	tx, err := s.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	now := nowRFC3339()
	res, err := tx.ExecContext(ctx, beginTaskStatusSQL, id.String(), now, s.db.workspaceArg(), gtd.AssigneeSpaceChars)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if affected == 0 {
		if rerr := tx.Rollback(); rerr != nil {
			return 0, rerr
		}
		return 0, nil
	}
	if cerr := tx.Commit(); cerr != nil {
		return 0, cerr
	}
	return affected, nil
}
