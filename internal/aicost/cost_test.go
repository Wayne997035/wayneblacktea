package aicost

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestCostMicroUSD verifies the cost computation helper.
func TestCostMicroUSD(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		model        string
		inputTokens  int64
		outputTokens int64
		wantCost     int64
		wantZero     bool // unknown model → 0
	}{
		{
			name:         "haiku 1M in + 1M out",
			model:        "claude-haiku-4-5",
			inputTokens:  1_000_000,
			outputTokens: 1_000_000,
			// in: 0.25 * 1 = 0.25 USD, out: 1.25 * 1 = 1.25 USD → total 1.5 USD = 1_500_000 micro
			wantCost: 1_500_000,
		},
		{
			name:         "sonnet 100k in + 10k out",
			model:        "claude-sonnet-4-6",
			inputTokens:  100_000,
			outputTokens: 10_000,
			// in: 3 * 0.1 = 0.3 USD = 300_000 micro; out: 15 * 0.01 = 0.15 = 150_000 micro → 450_000
			wantCost: 450_000,
		},
		{
			name:         "opus zero tokens",
			model:        "claude-opus-4-8",
			inputTokens:  0,
			outputTokens: 0,
			wantCost:     0,
		},
		{
			name:         "unknown model returns 0",
			model:        "claude-unknown-99",
			inputTokens:  1_000_000,
			outputTokens: 1_000_000,
			wantZero:     true,
			wantCost:     0,
		},
		{
			name:         "opus small tokens",
			model:        "claude-opus-4-8",
			inputTokens:  1000,
			outputTokens: 500,
			// in: 15 * 0.001 = 0.015 = 15_000 micro; out: 75 * 0.0005 = 0.0375 = 37_500 micro → 52_500
			wantCost: 52_500,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := costMicroUSD(tc.model, tc.inputTokens, tc.outputTokens)
			if got != tc.wantCost {
				t.Errorf("costMicroUSD(%q, %d, %d) = %d; want %d",
					tc.model, tc.inputTokens, tc.outputTokens, got, tc.wantCost)
			}
		})
	}
}

// TestNopRecorder verifies that the NopRecorder does not panic and is a valid
// Recorder (compile-time interface check below).
func TestNopRecorder(t *testing.T) {
	t.Parallel()
	var r Recorder = NopRecorder{}
	wsID := uuid.New()

	// Should not panic under any combination of inputs.
	r.Record(context.Background(), nil, RecordParams{})
	r.Record(context.Background(), &wsID, RecordParams{
		Caller:           "test.caller",
		Model:            "claude-haiku-4-5",
		InputTokens:      1000,
		OutputTokens:     200,
		CacheReadTokens:  50,
		CacheWriteTokens: 10,
	})
}

// TestRateTableContainsRequiredModels verifies the three models required by the
// spec are present in the rate table.
func TestRateTableContainsRequiredModels(t *testing.T) {
	t.Parallel()
	required := []string{"claude-haiku-4-5", "claude-sonnet-4-6", "claude-opus-4-8"}
	for _, m := range required {
		if _, ok := RateUSDPerMillion[m]; !ok {
			t.Errorf("RateUSDPerMillion: missing required model %q", m)
		}
	}
}
