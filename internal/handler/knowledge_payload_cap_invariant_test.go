package handler

import (
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
)

// [F984-01] knowledgeMaxContentLen is the largest content this package accepts,
// and every accepted item is handed to AutoProposeConceptFromKnowledge, which
// marshals it into a pending_proposals payload guarded by
// proposal.MaxPayloadBytes. Size the guard below this limit and items in the
// gap are accepted here (HTTP 201) but their concept proposals are rejected --
// the silent regression that 1f07d74 fixed by widening the cap from 128 KB.
//
// This test compares the two constants on purpose instead of expressing a size
// relative to proposal.MaxPayloadBytes. The payload-cap tests in
// internal/proposal and internal/storage/sqlite are deliberately relative so
// that retuning the cap does not break them -- which is also why none of them
// pin its value: mutating the constant back to 128 KB leaves every one of them
// green. This is the one place that fails when the guard is shrunk back below
// the largest legitimate producer.
//
// Strictly greater, not equal: the stored payload is a JSON envelope around the
// content, so it is always somewhat larger than the raw bytes counted by
// knowledgeMaxContentLen. The exact envelope overhead is not pinned here; the
// current 2 MiB / 1 MiB ratio leaves ample room for it.
func TestProposalPayloadCapCoversKnowledgeContentLimit(t *testing.T) {
	if proposal.MaxPayloadBytes <= knowledgeMaxContentLen {
		t.Fatalf(
			"proposal.MaxPayloadBytes (%d) does not exceed knowledgeMaxContentLen (%d): "+
				"knowledge items between the two sizes are accepted by this handler but "+
				"their concept proposals are rejected by the payload guard, and the "+
				"caller is told neither",
			proposal.MaxPayloadBytes, knowledgeMaxContentLen,
		)
	}
}
