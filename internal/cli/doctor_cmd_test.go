package cli

import (
	"reflect"
	"testing"
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
