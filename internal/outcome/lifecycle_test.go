package outcome

import (
	"testing"

	"github.com/google/uuid"
)

// TestIsIdempotentReplay covers the pure comparison helper RecordExecutionResult
// uses to decide ActionReplayedIdempotent vs ActionSuperseded.
func TestIsIdempotentReplay(t *testing.T) {
	entityID := uuid.New()
	base := Outcome{
		EntityType: "task",
		EntityID:   entityID,
		Result:     "success",
		Notes:      "done on time",
		Metrics:    []byte(`{"duration_ms":100}`),
	}

	tests := []struct {
		name   string
		latest Outcome
		params CreateOutcomeParams
		want   bool
	}{
		{
			name:   "identical result, notes, and metrics",
			latest: base,
			params: CreateOutcomeParams{
				Result:  "success",
				Notes:   "done on time",
				Metrics: []byte(`{"duration_ms":100}`),
			},
			want: true,
		},
		{
			name:   "different result",
			latest: base,
			params: CreateOutcomeParams{
				Result:  "failure",
				Notes:   "done on time",
				Metrics: []byte(`{"duration_ms":100}`),
			},
			want: false,
		},
		{
			name:   "different notes",
			latest: base,
			params: CreateOutcomeParams{
				Result:  "success",
				Notes:   "done late",
				Metrics: []byte(`{"duration_ms":100}`),
			},
			want: false,
		},
		{
			name:   "different metrics bytes",
			latest: base,
			params: CreateOutcomeParams{
				Result:  "success",
				Notes:   "done on time",
				Metrics: []byte(`{"duration_ms":200}`),
			},
			want: false,
		},
		{
			name: "both empty metrics and notes match",
			latest: Outcome{
				Result: "unknown",
			},
			params: CreateOutcomeParams{
				Result: "unknown",
			},
			want: true,
		},
		{
			name: "nil metrics vs empty-but-non-nil metrics still match (bytes.Equal)",
			latest: Outcome{
				Result:  "success",
				Metrics: nil,
			},
			params: CreateOutcomeParams{
				Result:  "success",
				Metrics: []byte{},
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isIdempotentReplay(tc.latest, tc.params)
			if got != tc.want {
				t.Errorf("isIdempotentReplay() = %v, want %v", got, tc.want)
			}
		})
	}
}
