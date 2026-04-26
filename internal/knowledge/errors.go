package knowledge

import (
	"errors"
	"fmt"
)

// ErrNotFound is returned when a requested knowledge item does not exist.
var ErrNotFound = errors.New("knowledge: not found")

// ErrDuplicate is returned when AddItem detects content that is already saved
// (by URL exact match or by vector cosine similarity >= dedup threshold).
type ErrDuplicate struct {
	ExistingTitle string
	Similarity    float64
}

func (e ErrDuplicate) Error() string {
	return fmt.Sprintf("similar content already saved: %q (%.0f%% match)", e.ExistingTitle, e.Similarity*100)
}
