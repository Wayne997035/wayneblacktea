package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// proposalDriverError carries every class of internal detail a real pgx /
// modernc-sqlite failure hands back: the host and port of the database, the
// database name, and the name of the schema object that rejected the write.
// The assertions below name each fragment separately so a failure says WHICH
// kind of disclosure came back, not just "it leaked".
const proposalDriverError = `ERROR: duplicate key value violates unique constraint ` +
	`"goals_workspace_id_title_key" (SQLSTATE 23505) ` +
	`[host=db-prod.internal.example port=5432 database=wbt_prod user=wbt_app]`

// failingGoalGTDStore fails CreateGoal with proposalDriverError and nothing
// else — the materialise step of a TypeGoal proposal is the shortest real path
// from "a store returned a driver error" to "what the MCP client is shown".
type failingGoalGTDStore struct {
	noopGTDStore
}

func (failingGoalGTDStore) CreateGoal(context.Context, gtd.CreateGoalParams) (*db.Goal, error) {
	return nil, errors.New(proposalDriverError)
}

// pendingGoalProposalStore serves one pending TypeGoal proposal from Get so
// acceptProposal reaches the materialiser instead of short-circuiting on
// ErrNotFound. Everything else delegates to the package's existing no-op stub.
type pendingGoalProposalStore struct {
	*stubProposalStore
	prop *db.PendingProposal
}

func (p pendingGoalProposalStore) Get(context.Context, uuid.UUID) (*db.PendingProposal, error) {
	return p.prop, nil
}

// newPendingGoalServer wires the SQLite-shaped Server (pool nil,
// sqliteProposal nil) so acceptProposal dispatches to acceptProposalSequential
// — the path that consumes materializeFromPayloadIface's (any, string) result
// and is therefore the one carrying [F170-07]'s errMsg indirection.
func newPendingGoalServer(t *testing.T) (*Server, uuid.UUID) {
	t.Helper()
	id := uuid.New()
	return &Server{
		gtd: failingGoalGTDStore{},
		proposal: pendingGoalProposalStore{
			stubProposalStore: &stubProposalStore{},
			prop: &db.PendingProposal{
				ID:      id,
				Type:    string(proposal.TypeGoal),
				Status:  string(proposal.StatusPending),
				Payload: []byte(`{"title":"ship the thing","area":"engineering"}`),
			},
		},
	}, id
}

// assertNoDriverDetail fails the test for each fragment of proposalDriverError
// that reached the client.
func assertNoDriverDetail(t *testing.T, where, text string) {
	t.Helper()
	for _, fragment := range []string{
		"host=db-prod.internal.example",
		"port=5432",
		"database=wbt_prod",
		"user=wbt_app",
		"goals_workspace_id_title_key",
		"SQLSTATE",
	} {
		if strings.Contains(text, fragment) {
			t.Errorf("%s disclosed %q to the MCP client: %q", where, fragment, text)
		}
	}
}

// TestSEC_F170_07_AcceptProposalDoesNotLeakDBTopology is [F170-07]'s
// acceptance criterion, end to end through the real accept path.
//
// Before this fix, materializeFromPayloadIface returned
// fmt.Sprintf("creating goal: %v", err) and acceptProposalSequential handed
// that string straight to mcp.NewToolResultError — so an authenticated MCP
// caller could read the production database's host, port, name and the
// violated constraint's name out of a failed accept_proposal (CWE-209).
//
// The second half of the assertion matters as much as the first: redaction
// that also blinded the operator would trade an information leak for an
// undiagnosable incident, which is a worse bug than the one being fixed.
func TestSEC_F170_07_AcceptProposalDoesNotLeakDBTopology(t *testing.T) {
	buf := bufferLogger(t)
	s, id := newPendingGoalServer(t)

	res, err := s.acceptProposal(context.Background(), id)
	if err != nil {
		t.Fatalf("acceptProposal returned a transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("handler reported success though CreateGoal failed")
	}

	client := resultText(res)
	assertNoDriverDetail(t, "accept_proposal", client)
	if !strings.Contains(client, "creating goal") {
		t.Errorf("client lost every hint of which step failed, leaving nothing actionable: %q", client)
	}

	logged := buf.String()
	if !strings.Contains(logged, "goals_workspace_id_title_key") ||
		!strings.Contains(logged, "host=db-prod.internal.example") {
		t.Errorf("server log did NOT retain the full error — redaction must be client-facing only, "+
			"or this change costs the operator the only copy of the failure: %q", logged)
	}
	if !strings.Contains(logged, "creating goal") {
		t.Errorf("server log lost the op field, making the entry hard to attribute: %q", logged)
	}
}

// TestSEC_F170_07_BatchAcceptErrMsgDoesNotLeakDBTopology covers the OTHER
// client-visible surface of the same failure: confirm_proposals aggregates
// per-id outcomes into BatchItemResult.ErrMsg and serialises them with
// jsonText, never touching mcp.NewToolResultError.
//
// That is the surface U13 explicitly deferred to U14 ("ErrMsg carries a store
// error string, U14's jurisdiction" — u13_stored_data_inventory_test.go) while
// U14's needle could only ever match NewToolResultError(, so neither gate
// covered it and the raw driver text shipped through the batch path even for
// callers who never touched the singular one.
func TestSEC_F170_07_BatchAcceptErrMsgDoesNotLeakDBTopology(t *testing.T) {
	_ = bufferLogger(t)
	s, id := newPendingGoalServer(t)

	res, err := s.batchAccept(context.Background(), []uuid.UUID{id})
	if err != nil {
		t.Fatalf("batchAccept returned a transport error: %v", err)
	}

	body := resultText(res)
	assertNoDriverDetail(t, "confirm_proposals batch", body)
	if !strings.Contains(body, "creating goal") {
		t.Errorf("batch result lost every hint of which step failed: %q", body)
	}
	if !strings.Contains(body, `"failed":1`) {
		t.Errorf("batch result did not record the failure at all: %q", body)
	}
}

// TestSEC_F170_07_CallerFacingSentinelSurvivesRedaction is the positive
// control for the half of [F170-07] that must NOT change.
//
// storeErrorText suppresses the server's internals, not the caller's own
// mistakes: a validation sentinel that reached the handler through the store's
// error channel still has to come back, or the sweep silently converts "you
// must set an assignee" into "creating task failed" and the caller has no way
// to fix its request. This is the regression the U14 sweep actually caused
// once (TestMCPBeginTask_RequiresAssignee), which is why it has its own test
// rather than a comment.
func TestSEC_F170_07_CallerFacingSentinelSurvivesRedaction(t *testing.T) {
	_ = bufferLogger(t)

	got := storeErrorText("creating task", gtd.ErrInvalidAssignee)
	if !strings.Contains(got, gtd.ErrInvalidAssignee.Error()) {
		t.Errorf("storeErrorText = %q, want it to carry the caller-facing sentinel %q",
			got, gtd.ErrInvalidAssignee.Error())
	}
	if !strings.Contains(got, "creating task") {
		t.Errorf("storeErrorText = %q, want the operation name retained", got)
	}
}

// TestSEC_F170_07_StoreErrorTextAndResultAgree pins that the two entry points
// cannot drift: storeErrorResult is defined as storeErrorText plus the mcp
// wrapper, and a future edit that gives one a redaction rule the other lacks
// would reopen the split this dispatch closed.
func TestSEC_F170_07_StoreErrorTextAndResultAgree(t *testing.T) {
	_ = bufferLogger(t)
	err := errors.New(proposalDriverError)

	text := storeErrorText("creating goal", err)
	viaResult := resultText(storeErrorResult("creating goal", err))
	if text != viaResult {
		t.Errorf("storeErrorText = %q but storeErrorResult produced %q — the two policies have "+
			"drifted, so which one a call site uses now changes what the client sees", text, viaResult)
	}
}

// TestSEC_F170_07_InputErrorTextIsByteIdenticalToBareErrorText pins the
// promise inputErrorText makes to the call sites [F170-08] pushed onto it: it
// exists to make an intent explicit, NOT to change any message. If it ever
// starts decorating the text, every site annotated with it silently changes
// its client-visible output.
func TestSEC_F170_07_InputErrorTextIsByteIdenticalToBareErrorText(t *testing.T) {
	err := errors.New("assignee must be one of: wayne, claude-code")

	if got := inputErrorText("", err); got != err.Error() {
		t.Errorf("inputErrorText(\"\", err) = %q, want the error text unchanged (%q)", got, err.Error())
	}
	if got, want := inputErrorText("assignee", err), "assignee: "+err.Error(); got != want {
		t.Errorf("inputErrorText(\"assignee\", err) = %q, want %q", got, want)
	}
}
