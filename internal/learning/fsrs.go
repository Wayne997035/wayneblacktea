package learning

import "math"

// Rating represents the quality of a review response.
type Rating int

const (
	// Again means the user forgot the concept entirely.
	Again Rating = 1
	// Hard means the user recalled with significant difficulty.
	Hard Rating = 2
	// Good means the user recalled with some effort.
	Good Rating = 3
	// Easy means the user recalled effortlessly.
	Easy Rating = 4
)

// CardState holds the current FSRS parameters for a flashcard.
type CardState struct {
	Stability   float64
	Difficulty  float64
	ReviewCount int
}

// fsrsWeights are the default FSRS v4 weight parameters.
var fsrsWeights = [17]float64{
	0.4072, 1.1829, 3.1262, 15.4722, 7.2102, 0.5316, 1.0651, 0.0589,
	1.5330, 0.1544, 1.0070, 1.9395, 0.1100, 0.2900, 2.2700, 0.2500, 2.9898,
}

// NextState returns the updated stability, difficulty, and interval in days
// after performing a review with the given rating.
func NextState(s CardState, rating Rating) (stability, difficulty float64, intervalDays int) {
	w := fsrsWeights

	if s.ReviewCount == 0 {
		// Initial stability based on rating.
		switch rating {
		case Again:
			stability = w[0]
		case Hard:
			stability = w[1]
		case Good:
			stability = w[2]
		case Easy:
			stability = w[3]
		}
		difficulty = w[4] - (float64(rating)-3)*w[5]
		difficulty = math.Max(1, math.Min(10, difficulty))
		intervalDays = int(math.Round(stability))
		if intervalDays < 1 {
			intervalDays = 1
		}
		return
	}

	if s.Stability < 0.01 {
		s.Stability = 0.1
	}
	// Estimated retrievability at time of review.
	retrievability := math.Exp(math.Log(0.9) * float64(s.ReviewCount) / s.Stability)

	// Difficulty update.
	difficulty = s.Difficulty - w[6]*(float64(rating)-3)
	difficulty = math.Max(1, math.Min(10, difficulty))

	// Stability update.
	switch rating {
	case Again:
		stability = w[11] * math.Pow(difficulty, -w[12]) *
			(math.Pow(s.Stability+1, w[13]) - 1) *
			math.Exp(w[14]*(1-retrievability))
	default:
		stabilityFactor := math.Exp(w[8]) *
			(11 - difficulty) *
			math.Pow(s.Stability, -w[9]) *
			(math.Exp(w[10]*(1-retrievability)) - 1)
		switch rating {
		case Hard:
			stabilityFactor *= w[15]
		case Good:
			// factor unchanged
		case Easy:
			stabilityFactor *= w[16]
		default:
			// Again handled by outer switch; unreachable.
		}
		stability = s.Stability * (1 + stabilityFactor)
	}

	stability = math.Max(0.1, stability)
	intervalDays = int(math.Round(stability * 9)) // target ~90% retention
	if intervalDays < 1 {
		intervalDays = 1
	}
	return
}
