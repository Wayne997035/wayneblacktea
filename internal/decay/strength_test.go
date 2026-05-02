package decay

import (
	"math"
	"testing"
)

func TestComputeStrength_MaxAtZeroAge(t *testing.T) {
	// strength(importance=1, age=0, recall=0) must equal 1.0
	got := ComputeStrength(1.0, 0.1, 0, 0)
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("got %.6f, want 1.0", got)
	}
}

func TestComputeStrength_LowImportanceLongAge(t *testing.T) {
	// strength(importance=0.1, age=100, recall=0) must be < 0.05
	got := ComputeStrength(0.1, 0.1, 100, 0)
	if got >= 0.05 {
		t.Errorf("got %.6f, want < 0.05", got)
	}
}

func TestComputeStrength_RecallBoost(t *testing.T) {
	// strength(importance=0.5, age=30, recall=10) > strength(importance=0.5, age=30, recall=0)
	withRecall := ComputeStrength(0.5, 0.1, 30, 10)
	noRecall := ComputeStrength(0.5, 0.1, 30, 0)
	if withRecall <= noRecall {
		t.Errorf("recall boost failed: with=%v, without=%v", withRecall, noRecall)
	}
}

func TestComputeStrength_ClampedToOne(t *testing.T) {
	// High recall count could theoretically push > 1; must be clamped.
	got := ComputeStrength(1.0, 0.1, 0, 100)
	if got > 1.0 {
		t.Errorf("strength > 1.0: got %v", got)
	}
}

func TestComputeStrength_ClampedToZero(t *testing.T) {
	// Zero importance means no strength.
	got := ComputeStrength(0.0, 0.1, 365, 0)
	if got != 0.0 {
		t.Errorf("zero importance should give strength=0, got %v", got)
	}
}

func TestComputeStrength_NegativeAgeClamped(t *testing.T) {
	// Negative age (clock skew) must be treated as 0, not amplify strength.
	neg := ComputeStrength(0.5, 0.1, -10, 0)
	zero := ComputeStrength(0.5, 0.1, 0, 0)
	if math.Abs(neg-zero) > 1e-9 {
		t.Errorf("negative age should behave same as age=0: neg=%v zero=%v", neg, zero)
	}
}

func TestComputeStrength_InvalidLambdaFallback(t *testing.T) {
	// baseLambda <= 0 should fall back to 0.1 (not infinite strength).
	got := ComputeStrength(0.5, 0.0, 30, 0)
	reference := ComputeStrength(0.5, 0.1, 30, 0)
	if got != reference {
		t.Errorf("zero lambda: got %v, want %v (same as lambda=0.1)", got, reference)
	}
}

func TestComputeStrength_HalfLifeApprox(t *testing.T) {
	// At importance=0.5, base_lambda=0.1, with no recall, strength should
	// decay significantly after 60 days (more than halved from day 0).
	day0 := ComputeStrength(0.5, 0.1, 0, 0)
	day60 := ComputeStrength(0.5, 0.1, 60, 0)
	if day60 >= day0/2 {
		t.Errorf("expected strength at day60 < day0/2 (decay too slow): day0=%v day60=%v", day0, day60)
	}
}
