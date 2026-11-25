package gtd

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// scanErrRows is a pgx.Rows whose first row is present but whose Scan fails.
// The embedded nil pgx.Rows is never touched: scanPGTaskIDs only calls
// Next, Scan, Err and Close.
type scanErrRows struct {
	pgx.Rows
	scanErr error
	err     error
	nexts   int
	closed  bool
}

func (r *scanErrRows) Next() bool {
	r.nexts++
	return r.nexts == 1
}

func (r *scanErrRows) Scan(_ ...any) error { return r.scanErr }
func (r *scanErrRows) Err() error          { return r.err }
func (r *scanErrRows) Close()              { r.closed = true }

// [F1124-46] scanPGTaskIDs must surface a rows.Scan failure instead of
// returning a partial or empty id set: a swallowed error would hand the
// delete_project preview/lock an empty task set.
func TestScanPGTaskIDs_ScanErrorPropagates(t *testing.T) {
	boom := errors.New("scan boom")
	iterErr := errors.New("iteration boom")
	tests := []struct {
		name    string
		rows    *scanErrRows
		wantErr error
	}{
		{name: "scan fails on the first row", rows: &scanErrRows{scanErr: boom}, wantErr: boom},
		{name: "scan error wins over a later iteration error", rows: &scanErrRows{scanErr: boom, err: iterErr}, wantErr: boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids, err := scanPGTaskIDs(tt.rows)
			if err == nil {
				t.Fatalf("scanPGTaskIDs error = nil, want it to wrap %v", tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("scanPGTaskIDs error = %v, want errors.Is %v", err, tt.wantErr)
			}
			if ids != nil {
				t.Errorf("scanPGTaskIDs ids = %v, want nil on error", ids)
			}
			if !tt.rows.closed {
				t.Errorf("rows were not closed on the error path")
			}
		})
	}
}

// [F1124-46] The rows.Err() branch (iteration finished with an error) is the
// other way the id set can be silently truncated.
func TestScanPGTaskIDs_IterationErrorPropagates(t *testing.T) {
	iterErr := errors.New("iteration boom")
	rows := &scanErrRows{err: iterErr, nexts: 1} // Next returns false at once; Scan never runs
	ids, err := scanPGTaskIDs(rows)
	if !errors.Is(err, iterErr) {
		t.Fatalf("scanPGTaskIDs error = %v, want errors.Is %v", err, iterErr)
	}
	if ids != nil {
		t.Errorf("scanPGTaskIDs ids = %v, want nil on error", ids)
	}
}
