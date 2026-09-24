package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// callProposeGoal drives the real handler, not storeErrorText directly: the
// question this file answers is what an MCP client is shown, and the redaction
// seam sits between the two.
func callProposeGoal(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleProposeGoal(context.Background(), req)
	if err != nil {
		t.Fatalf("handleProposeGoal returned a transport error: %v", err)
	}
	return r
}

// oversizedPayloadError is shaped like the chain internal/proposal.Store.Create
// actually returns (store.go wraps ErrPayloadTooLarge with the two byte counts)
// with real driver noise spliced in, because a production failure arrives
// wrapped by whatever else was on the stack. Both halves are load-bearing: the
// sentinel's own reviewed text has to survive, and everything around it must
// not.
func oversizedPayloadError() error {
	return fmt.Errorf("creating proposal: payload of %d bytes exceeds %d [%s]: %w",
		proposal.MaxPayloadBytes+1, proposal.MaxPayloadBytes, proposalDriverError,
		proposal.ErrPayloadTooLarge)
}

// TestSEC_PR1_05_ProposeGoalTellsTheCallerThePayloadWasTooLarge is [GTD
// 21aa901d]'s acceptance criterion.
//
// propose_goal answers a store failure with storeErrorResult, which redacts
// anything not in callerFacingSentinels down to "creating proposal failed".
// ErrPayloadTooLarge was not on that list, so an agent that sent an oversized
// payload got a message indistinguishable from a transient outage — and the
// correct response to an outage is to retry the same request. It re-sent the
// identical payload, burned rate limit, and the operator saw rate-limit noise
// rather than a size error. The new failure mode was undiagnosable from the
// only end that could fix it.
//
// Mutation: delete proposal.ErrPayloadTooLarge from callerFacingSentinels and
// this test goes red on the first assertion — the client text falls back to
// "creating proposal failed".
func TestSEC_PR1_05_ProposeGoalTellsTheCallerThePayloadWasTooLarge(t *testing.T) {
	// Not parallel: swaps slog.Default() for a capture logger; parallel tests would write into the captured buffer.
	_ = bufferLogger(t)

	s := &Server{proposal: &stubProposalStore{createEr: oversizedPayloadError()}}
	res := callProposeGoal(t, s, map[string]any{
		"title": "ship the thing",
		"area":  "engineering",
	})

	if !res.IsError {
		t.Fatal("handler reported success though Create failed")
	}
	client := resultText(res)

	if !strings.Contains(client, proposal.ErrPayloadTooLarge.Error()) {
		t.Errorf("client was told %q, which does not name the size problem — an agent reading this "+
			"cannot tell it apart from a transient failure and will re-send the same payload", client)
	}
	if !strings.Contains(client, "creating proposal") {
		t.Errorf("client lost the operation name, leaving nothing to attribute the failure to: %q", client)
	}
}

// TestSEC_PR1_05_PayloadTooLargeCarriesNothingElse is the other half: adding a
// sentinel to the allowlist widens what storeErrorText is allowed to emit, so
// the widening has to be shown to be exactly one fixed string. storeErrorText
// returns the SENTINEL's text rather than the wrapped chain's, and this pins
// that — a future edit switching it to err.Error() would ship the byte counts
// and the driver topology spliced in above.
func TestSEC_PR1_05_PayloadTooLargeCarriesNothingElse(t *testing.T) {
	// Not parallel: swaps slog.Default() for a capture logger; parallel tests would write into the captured buffer.
	_ = bufferLogger(t)

	s := &Server{proposal: &stubProposalStore{createEr: oversizedPayloadError()}}
	client := resultText(callProposeGoal(t, s, map[string]any{
		"title": "ship the thing",
		"area":  "engineering",
	}))

	assertNoDriverDetail(t, "propose_goal", client)

	// The byte counts are the caller's own input size and the server's
	// configured cap. Neither is a secret, but neither is in the reviewed
	// sentinel string either, and "the allowlist emits the sentinel verbatim"
	// is only true if nothing else rides along.
	for _, unwanted := range []string{
		fmt.Sprintf("%d", proposal.MaxPayloadBytes+1),
		"exceeds",
	} {
		if strings.Contains(client, unwanted) {
			t.Errorf("client text carried %q from the wrapped chain: %q — storeErrorText must "+
				"return the sentinel's own message, not err's", unwanted, client)
		}
	}
}

// TestSEC_PR1_05_OperatorKeepsTheFullChain guards the same trade the [F170-07]
// tests guard: client-facing redaction must not also blind the operator, or an
// information leak has been traded for an undiagnosable incident.
func TestSEC_PR1_05_OperatorKeepsTheFullChain(t *testing.T) {
	// Not parallel: swaps slog.Default() for a capture logger; parallel tests would write into the captured buffer.
	buf := bufferLogger(t)

	s := &Server{proposal: &stubProposalStore{createEr: oversizedPayloadError()}}
	_ = callProposeGoal(t, s, map[string]any{
		"title": "ship the thing",
		"area":  "engineering",
	})

	logged := buf.String()
	for _, want := range []string{
		"creating proposal",
		"host=db-prod.internal.example",
		fmt.Sprintf("%d", proposal.MaxPayloadBytes+1),
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("server log lost %q — redaction must be client-facing only: %q", want, logged)
		}
	}
}
