package sqlite

import (
	"context"
	"database/sql"
	"testing"
)

func openScanTestConn(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// [F1124-46] scanSQLiteTaskIDs must surface a rows.Scan failure instead of
// returning a partial or empty id set. `SELECT NULL` yields one row whose
// value database/sql cannot scan into a string, which is the real driver path
// for a scan error.
func TestScanSQLiteTaskIDs_ScanErrorPropagates(t *testing.T) {
	t.Run("NULL id column cannot scan into string", func(t *testing.T) {
		conn := openScanTestConn(t)
		rows, err := conn.QueryContext(context.Background(), `SELECT NULL`)
		if err != nil {
			t.Fatalf("QueryContext: %v", err)
		}
		ids, err := scanSQLiteTaskIDs(rows)
		if err == nil {
			t.Fatalf("scanSQLiteTaskIDs error = nil, want a scan error (ids = %v)", ids)
		}
		if ids != nil {
			t.Errorf("scanSQLiteTaskIDs ids = %v, want nil on error", ids)
		}
	})

	t.Run("NULL after a valid first row", func(t *testing.T) {
		conn := openScanTestConn(t)
		rows, err := conn.QueryContext(context.Background(),
			`SELECT '00000000-0000-0000-0000-000000000001' UNION ALL SELECT NULL`)
		if err != nil {
			t.Fatalf("QueryContext: %v", err)
		}
		ids, err := scanSQLiteTaskIDs(rows)
		if err == nil {
			t.Fatalf("scanSQLiteTaskIDs error = nil, want a scan error (ids = %v)", ids)
		}
		if ids != nil {
			t.Errorf("scanSQLiteTaskIDs ids = %v, want nil on error (no partial result)", ids)
		}
	})
}

// [F1124-46] A non-UUID text id is the sibling failure inside the same loop.
func TestScanSQLiteTaskIDs_InvalidUUIDRejected(t *testing.T) {
	conn := openScanTestConn(t)
	rows, err := conn.QueryContext(context.Background(), `SELECT 'not-a-uuid'`)
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	ids, err := scanSQLiteTaskIDs(rows)
	if err == nil {
		t.Fatalf("scanSQLiteTaskIDs error = nil, want a parse error (ids = %v)", ids)
	}
	if ids != nil {
		t.Errorf("scanSQLiteTaskIDs ids = %v, want nil on error", ids)
	}
}
