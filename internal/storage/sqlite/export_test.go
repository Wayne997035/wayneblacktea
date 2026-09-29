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
