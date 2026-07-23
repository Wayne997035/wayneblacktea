package cli

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

func TestDetectDoctorSignals(t *testing.T) {
	cases := []struct {
		name string
		in   DoctorSnapshot
		want []string
	}{
		{
			name: "empty snapshot — no signals",
			in:   DoctorSnapshot{},
			want: nil,
		},
		{
			name: "only stuck — single stuck signal",
			in:   DoctorSnapshot{StuckCount: 2, InProgressCount: 2},
			want: []string{"2 stuck in-progress task(s) — likely missing complete_task call"},
		},
		{
			name: "only active in_progress — single active signal",
			in:   DoctorSnapshot{StuckCount: 0, InProgressCount: 3},
			want: []string{"3 active in_progress task(s) — close with complete_task or set_session_handoff"},
		},
		{
			name: "stuck + active — both signals, stuck first",
			in:   DoctorSnapshot{StuckCount: 1, InProgressCount: 4},
			want: []string{
				"1 stuck in-progress task(s) — likely missing complete_task call",
				"3 active in_progress task(s) — close with complete_task or set_session_handoff",
			},
		},
		{
			name: "proposals threshold — 5 triggers, 4 does not",
			in:   DoctorSnapshot{PendingProposals: 5},
			want: []string{"5 pending proposals queued — triage backlog"},
		},
		{
			name: "due reviews — any positive triggers",
			in:   DoctorSnapshot{DueReviews: 2},
			want: []string{"2 concept(s) due for review today"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectDoctorSignals(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DetectDoctorSignals(%+v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestNewDoctorSnapshot_DeliveryFieldsFailSoftShape verifies the OPS-1
// contract for every fail-soft early-return path in RunDoctor (missing
// DSN, bad config, TLS error, pool connect failure — all of which construct
// the snapshot via newDoctorSnapshot and never touch a gtd.Store): goals_due
// MUST render as "[]" (never omitted, never null) and top_pending MUST
// render as JSON null (never omitted).
func TestNewDoctorSnapshot_DeliveryFieldsFailSoftShape(t *testing.T) {
	snap := newDoctorSnapshot()

	out, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	goalsRaw, ok := decoded["goals_due"]
	if !ok {
		t.Fatal(`"goals_due" key missing from JSON output — must always be present`)
	}
	if string(goalsRaw) != "[]" {
		t.Errorf(`goals_due = %s, want "[]" (empty array, not null)`, goalsRaw)
	}

	topRaw, ok := decoded["top_pending"]
	if !ok {
		t.Fatal(`"top_pending" key missing from JSON output — must always be present`)
	}
	if string(topRaw) != "null" {
		t.Errorf(`top_pending = %s, want "null"`, topRaw)
	}
}

// TestDoctorSnapshot_JSON_DeliveryFieldsPopulatedShape verifies the JSON
// shape once goals_due/top_pending are populated: exact field names
// (title/due_date/days_left for goals_due; title/due_date for top_pending)
// and that top_pending's due_date renders null when the task has no due
// date (the task itself must still be emitted — target contract "top
// pending no due still emit, due=null").
func TestDoctorSnapshot_JSON_DeliveryFieldsPopulatedShape(t *testing.T) {
	due := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	snap := DoctorSnapshot{
		GeneratedAt: time.Now().UTC(),
		GoalsDue: []gtd.DeliveryGoal{
			{Title: "ship P1", DueDate: due, DaysLeft: 2},
		},
		TopPending: &gtd.DeliveryTask{Title: "unscheduled top task", DueDate: nil},
	}

	out, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var decoded struct {
		GoalsDue []struct {
			Title    string `json:"title"`
			DueDate  string `json:"due_date"`
			DaysLeft int    `json:"days_left"`
		} `json:"goals_due"`
		TopPending struct {
			Title   string  `json:"title"`
			DueDate *string `json:"due_date"`
		} `json:"top_pending"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if len(decoded.GoalsDue) != 1 {
		t.Fatalf("expected 1 goals_due entry, got %d", len(decoded.GoalsDue))
	}
	if decoded.GoalsDue[0].Title != "ship P1" || decoded.GoalsDue[0].DaysLeft != 2 {
		t.Errorf("unexpected goals_due[0]: %+v", decoded.GoalsDue[0])
	}
	if decoded.TopPending.Title != "unscheduled top task" {
		t.Errorf("TopPending.Title = %q, want %q", decoded.TopPending.Title, "unscheduled top task")
	}
	if decoded.TopPending.DueDate != nil {
		t.Errorf("expected top_pending.due_date == null, got %q", *decoded.TopPending.DueDate)
	}
}
