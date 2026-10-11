package gtd_test

import (
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// TestTaskSetDigest pins the properties delete_project's confirm check relies
// on: the digest names a set (order does not matter, a different set gives a
// different digest), and the empty set has a real digest, because "" is the
// "do not check" value and must never be what an empty preview produces.
// [PR #200 SE-01]
func TestTaskSetDigest(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()

	if gtd.TaskSetDigest([]uuid.UUID{a, b, c}) != gtd.TaskSetDigest([]uuid.UUID{c, a, b}) {
		t.Error("same ids in a different order must give the same digest")
	}
	if gtd.TaskSetDigest([]uuid.UUID{a, b}) == gtd.TaskSetDigest([]uuid.UUID{a, c}) {
		t.Error("different sets of the same size must give different digests")
	}
	if gtd.TaskSetDigest([]uuid.UUID{a}) == gtd.TaskSetDigest([]uuid.UUID{a, b}) {
		t.Error("a set and its superset must give different digests")
	}
	empty := gtd.TaskSetDigest(nil)
	if empty == "" {
		t.Error(`the empty set must not digest to "" (that value means "do not check")`)
	}
	if empty != gtd.TaskSetDigest([]uuid.UUID{}) {
		t.Error("nil and an empty slice are the same set and must give the same digest")
	}
	if empty == gtd.TaskSetDigest([]uuid.UUID{a}) {
		t.Error("the empty set and a one-task set must give different digests")
	}
}
