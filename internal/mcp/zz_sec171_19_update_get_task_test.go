//go:build !integration

package mcp

import (
	"strings"
	"testing"
)

// [F1124-15] [SEC171-19] End to end through the tool seam: update_task stores
// a printable, no-slash forged boundary marker in branch_name / pr_url (write
// validation does not reject it), and get_task must hand it back neutralised.
// wrapUntrustedTask's comment names this test as the proof.
func TestSEC171_19_UpdateTaskThenGetTaskNeutralisesBranchName(t *testing.T) {
	t.Parallel()

	t.Run("forged_marker_branch", func(t *testing.T) {
		t.Parallel()
		s := newTestWorkSessionServer(t)
		id := seedTask(t, s)
		if r := callUpdateTask(t, s, map[string]any{
			"task_id": id.String(), "branch_name": storedContextMarkerEnd,
		}); r.IsError {
			t.Fatalf("update_task must accept a printable marker string: %s", resultText(r))
		}
		got := callGetTask(t, s, map[string]any{"task_id": id.String()})
		if got.IsError {
			t.Fatalf("get_task: %s", resultText(got))
		}
		assertMarkerNeutralized(t, resultText(got), storedContextMarkerEnd)
	})

	t.Run("legit_branch_passes", func(t *testing.T) {
		t.Parallel()
		s := newTestWorkSessionServer(t)
		id := seedTask(t, s)
		if r := callUpdateTask(t, s, map[string]any{
			"task_id": id.String(), "branch_name": "feature/abc-123",
		}); r.IsError {
			t.Fatalf("update_task: %s", resultText(r))
		}
		got := resultText(callGetTask(t, s, map[string]any{"task_id": id.String()}))
		if !strings.Contains(got, `"feature/abc-123"`) {
			t.Errorf("legit branch altered or missing: %s", got)
		}
	})

	t.Run("pr_url", func(t *testing.T) {
		t.Parallel()
		s := newTestWorkSessionServer(t)
		id := seedTask(t, s)
		forged := "https://github.com/" + storedContextMarkerEnd + "/repo/pull/1"
		upd := callUpdateTask(t, s, map[string]any{"task_id": id.String(), "pr_url": forged})
		got := resultText(callGetTask(t, s, map[string]any{"task_id": id.String()}))
		if upd.IsError {
			// Rejected at write time: the marker must still never be echoed.
			t.Logf("pr_url branch: rejected at write time: %s", resultText(upd))
			if strings.Contains(got, storedContextMarkerEnd) {
				t.Errorf("rejected pr_url still echoed raw marker: %s", got)
			}
			return
		}
		t.Logf("pr_url branch: accepted at write time, checking read-side clip")
		assertMarkerNeutralized(t, got, storedContextMarkerEnd)
	})

	t.Run("control_char_rejected", func(t *testing.T) {
		t.Parallel()
		s := newTestWorkSessionServer(t)
		id := seedTask(t, s)
		if r := callUpdateTask(t, s, map[string]any{
			"task_id": id.String(), "branch_name": "bad\nbranch",
		}); !r.IsError {
			t.Fatalf("branch_name with newline must be rejected, got: %s", resultText(r))
		}
		got := resultText(callGetTask(t, s, map[string]any{"task_id": id.String()}))
		if strings.Contains(got, "bad") && strings.Contains(got, "branch") && strings.Contains(got, `"branch_name":"bad`) {
			t.Errorf("rejected branch_name was stored: %s", got)
		}
	})
}
