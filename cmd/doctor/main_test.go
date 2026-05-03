package main

import (
	"reflect"
	"testing"
)

func TestDetectSignals(t *testing.T) {
	cases := []struct {
		name string
		in   snapshot
		want []string
	}{
		{
			name: "empty snapshot — no signals",
			in:   snapshot{},
			want: nil,
		},
		{
			name: "only stuck — single stuck signal",
			in:   snapshot{StuckCount: 2, InProgressCount: 2},
			want: []string{"2 stuck in-progress task(s) — likely missing complete_task call"},
		},
		{
			name: "only active in_progress — single active signal",
			in:   snapshot{StuckCount: 0, InProgressCount: 3},
			want: []string{"3 active in_progress task(s) — close with complete_task or set_session_handoff"},
		},
		{
			name: "stuck + active — both signals, stuck first",
			in:   snapshot{StuckCount: 1, InProgressCount: 4},
			want: []string{
				"1 stuck in-progress task(s) — likely missing complete_task call",
				"3 active in_progress task(s) — close with complete_task or set_session_handoff",
			},
		},
		{
			name: "proposals threshold — 5 triggers, 4 does not",
			in:   snapshot{PendingProposals: 5},
			want: []string{"5 pending proposals queued — triage backlog"},
		},
		{
			name: "due reviews — any positive triggers",
			in:   snapshot{DueReviews: 2},
			want: []string{"2 concept(s) due for review today"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectSignals(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("detectSignals(%+v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
