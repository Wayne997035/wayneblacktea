// Package decay implements the Ebbinghaus forgetting curve strength formula
// used by the SQLite backend (which cannot run SQL math via EXTRACT EPOCH).
//
// Postgres computes strength inside the knowledge_ranked VIEW using native SQL.
// SQLite computes strength in Go using ComputeStrength before ordering results.
package decay

import "math"

// ComputeStrength returns the Ebbinghaus-derived memory strength in [0, 1].
//
// Formula (research-validated):
//
//	strength = clamp(importance * exp(-baseLambda * (1 - importance*0.8) * ageDays) * (1 + recallCount * 0.2), 0, 1)
//
// Parameters:
//   - importance: intrinsic value of the item in [0, 1]; default 0.5.
//   - baseLambda: base decay rate; default 0.1.
//   - ageDays: days since last recall (or creation when never recalled).
//   - recallCount: number of times the item has been recalled; boosts retention.
//
// Edge cases:
//   - ageDays < 0 is clamped to 0 (e.g. clocks skew).
//   - importance = 0 → strength = 0 regardless of recall (no value, no retention).
//   - baseLambda ≤ 0 falls back to 0.1 to avoid undefined behaviour (exp(-0*age)=1 always).
func ComputeStrength(importance, baseLambda, ageDays float64, recallCount int) float64 {
	if ageDays < 0 {
		ageDays = 0
	}
	if baseLambda <= 0 {
		baseLambda = 0.1
	}
	decay := math.Exp(-baseLambda * (1.0 - importance*0.8) * ageDays)
	raw := importance * decay * (1.0 + float64(recallCount)*0.2)
	return math.Max(0, math.Min(1, raw))
}
