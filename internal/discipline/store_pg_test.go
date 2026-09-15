package discipline

import (
	"math"
	"testing"
)

// TestClampToInt32 covers clampToInt32's boundary/overflow branches, which
// TestPgStore_InsertAndRecentMutating (store_test.go, integration-tagged)
// does not exercise — that test only passes small in-range values through
// Insert. [F184-05] response_bytes/duration_ms are narrowed from Go int to
// PG's 4-byte INTEGER; this guards against a silent overflow wraparound
// (gosec G115) rather than trusting the narrowing blindly.
func TestClampToInt32(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int32
	}{
		{"zero passes through", 0, 0},
		{"typical small value passes through", 512, 512},
		{"exactly math.MaxInt32 passes through", math.MaxInt32, math.MaxInt32},
		{"one above math.MaxInt32 clamps down", math.MaxInt32 + 1, math.MaxInt32},
		{"far above math.MaxInt32 clamps down", math.MaxInt32 * 10, math.MaxInt32},
		{"negative value clamps to zero", -1, 0},
		{"large negative value clamps to zero", -1000000, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := clampToInt32(tc.in)
			if got != tc.want {
				t.Errorf("clampToInt32(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
