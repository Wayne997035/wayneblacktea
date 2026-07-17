package worksession_test

import (
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
)

// TestResolveCompletedTaskIDs is a pure-function table-driven test for
// worksession.ResolveCompletedTaskIDs (wbt-2.0 P2 review F2 — deferred_task_ids
// must always win over completed_task_ids, including in the empty-completed
// fallback-to-linked-tasks case).
func TestResolveCompletedTaskIDs(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	c := uuid.New()

	cases := []struct {
		name      string
		completed []uuid.UUID
		deferred  []uuid.UUID
		linked    []uuid.UUID
		want      []uuid.UUID
	}{
		{
			name:      "completed only, no deferred",
			completed: []uuid.UUID{a, b},
			deferred:  nil,
			linked:    []uuid.UUID{a, b},
			want:      []uuid.UUID{a, b},
		},
		{
			name:      "empty completed falls back to linked, excluding deferred",
			completed: nil,
			deferred:  []uuid.UUID{b},
			linked:    []uuid.UUID{a, b},
			want:      []uuid.UUID{a},
		},
		{
			name:      "completed and deferred disjoint",
			completed: []uuid.UUID{a},
			deferred:  []uuid.UUID{b},
			linked:    []uuid.UUID{a, b},
			want:      []uuid.UUID{a},
		},
		{
			name:      "deferred wins when a task is listed in both",
			completed: []uuid.UUID{a, b},
			deferred:  []uuid.UUID{b},
			linked:    []uuid.UUID{a, b},
			want:      []uuid.UUID{a},
		},
		{
			name:      "everything deferred yields empty result via fallback",
			completed: nil,
			deferred:  []uuid.UUID{a, b},
			linked:    []uuid.UUID{a, b},
			want:      []uuid.UUID{},
		},
		{
			name:      "nothing completed, nothing linked, nothing deferred",
			completed: nil,
			deferred:  nil,
			linked:    nil,
			want:      []uuid.UUID{},
		},
		{
			name:      "preserves input order of completedTaskIDs",
			completed: []uuid.UUID{c, a, b},
			deferred:  nil,
			linked:    nil,
			want:      []uuid.UUID{c, a, b},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := worksession.ResolveCompletedTaskIDs(tc.completed, tc.deferred, tc.linked)
			if len(got) != len(tc.want) {
				t.Fatalf("length mismatch: got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("index %d: got %s, want %s", i, got[i], tc.want[i])
				}
			}
		})
	}
}
