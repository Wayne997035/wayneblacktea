package learning

import (
	"testing"
)

func TestNextState_FirstReview(t *testing.T) {
	cases := []struct {
		name             string
		rating           Rating
		wantIntervalMin  int
		wantDifficultyOK bool
	}{
		{
			name:             "Again → short interval",
			rating:           Again,
			wantIntervalMin:  1,
			wantDifficultyOK: true,
		},
		{
			name:             "Hard → moderate stability",
			rating:           Hard,
			wantIntervalMin:  1,
			wantDifficultyOK: true,
		},
		{
			name:             "Good → standard first interval",
			rating:           Good,
			wantIntervalMin:  1,
			wantDifficultyOK: true,
		},
		{
			name:             "Easy → long first interval",
			rating:           Easy,
			wantIntervalMin:  1,
			wantDifficultyOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := CardState{ReviewCount: 0}
			stability, difficulty, interval := NextState(state, tc.rating)

			if stability <= 0 {
				t.Errorf("stability must be positive, got %f", stability)
			}
			if difficulty < 1 || difficulty > 10 {
				t.Errorf("difficulty must be in [1,10], got %f", difficulty)
			}
			if interval < tc.wantIntervalMin {
				t.Errorf("interval %d is below minimum %d", interval, tc.wantIntervalMin)
			}
			if !tc.wantDifficultyOK {
				t.Error("difficulty check failed")
			}
		})
	}
}

func TestNextState_SubsequentReviews(t *testing.T) {
	baseState := CardState{
		Stability:   3.0,
		Difficulty:  5.0,
		ReviewCount: 2,
	}

	cases := []struct {
		name            string
		rating          Rating
		wantStabilityUp bool // stability should increase on good/easy, decrease on again
	}{
		{
			name:            "Again → stability decreases",
			rating:          Again,
			wantStabilityUp: false,
		},
		{
			name:            "Hard → stability changes modestly",
			rating:          Hard,
			wantStabilityUp: true,
		},
		{
			name:            "Good → stability increases",
			rating:          Good,
			wantStabilityUp: true,
		},
		{
			name:            "Easy → stability increases more",
			rating:          Easy,
			wantStabilityUp: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stability, difficulty, interval := NextState(baseState, tc.rating)

			if stability <= 0 {
				t.Errorf("stability must be positive, got %f", stability)
			}
			if difficulty < 1 || difficulty > 10 {
				t.Errorf("difficulty must be in [1,10], got %f", difficulty)
			}
			if interval < 1 {
				t.Errorf("interval must be at least 1 day, got %d", interval)
			}
			if tc.wantStabilityUp && stability <= baseState.Stability {
				t.Errorf("expected stability to increase, but got %f (was %f)", stability, baseState.Stability)
			}
			if !tc.wantStabilityUp && stability >= baseState.Stability {
				t.Errorf("expected stability to decrease, but got %f (was %f)", stability, baseState.Stability)
			}
		})
	}
}

func TestNextState_DifficultyBounds(t *testing.T) {
	cases := []struct {
		name   string
		state  CardState
		rating Rating
	}{
		{
			name:   "very high difficulty stays at 10",
			state:  CardState{Stability: 5, Difficulty: 9.9, ReviewCount: 5},
			rating: Again,
		},
		{
			name:   "very low difficulty stays at 1",
			state:  CardState{Stability: 5, Difficulty: 1.1, ReviewCount: 5},
			rating: Easy,
		},
		{
			name:   "first review with extreme rating",
			state:  CardState{ReviewCount: 0},
			rating: Again,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, difficulty, interval := NextState(tc.state, tc.rating)
			if difficulty < 1 || difficulty > 10 {
				t.Errorf("difficulty %f out of bounds [1,10]", difficulty)
			}
			if interval < 1 {
				t.Errorf("interval %d must be at least 1", interval)
			}
		})
	}
}

func TestNextState_EasyBeatsGoodInterval(t *testing.T) {
	state := CardState{Stability: 3.0, Difficulty: 5.0, ReviewCount: 2}

	_, _, goodInterval := NextState(state, Good)
	_, _, easyInterval := NextState(state, Easy)

	if easyInterval <= goodInterval {
		t.Errorf("Easy interval (%d) should be longer than Good interval (%d)", easyInterval, goodInterval)
	}
}

func TestNextState_AgainResetsProgress(t *testing.T) {
	// After Again, interval should be very short (≤ good interval)
	state := CardState{Stability: 10.0, Difficulty: 5.0, ReviewCount: 5}

	_, _, againInterval := NextState(state, Again)
	_, _, goodInterval := NextState(state, Good)

	if againInterval >= goodInterval {
		t.Errorf("Again interval (%d) should be shorter than Good interval (%d)", againInterval, goodInterval)
	}
}
