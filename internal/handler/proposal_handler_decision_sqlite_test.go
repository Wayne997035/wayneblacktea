package handler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// [F205-01] TypeDecision HTTP accept goes through proposal.AcceptOrchestration
// on both backends. Every case below is written ONCE against decisionBackend
// and run by a thin per-backend Test function (SQLite here, Postgres in
// proposal_handler_decision_pg_test.go), so the two backends cannot drift
// apart by test omission. Each backend drives the real stores and the real
// accept adapter — no mocks (PG = testcontainers, SQLite = real :memory: DB).

const (
	acceptBody        = `{"action":"accept"}`
	batchActionAccept = "accept"

	// wantDecisionContext / wantDecisionRationale are what
	// proposal.DecodeDecisionParams derives from decisionPayload.
	wantDecisionContext   = "auto-proposed by trigger_tool=complete_task session=s-1"
	wantDecisionRationale = "r\n\nAlternatives considered:\n- a1\n- a2"

	internalErrorBody = `{"error":"internal error"}`
	proposalPending   = string(proposal.StatusPending)
	proposalAccepted  = string(proposal.StatusAccepted)
)

// decisionAuditRow is the audit-relevant slice of a decisions row, read
// straight from the table (not from the HTTP response) for AC-8.
type decisionAuditRow struct {
	Source      string
	Confirmed   bool
	ActorIsNull bool
}

// decisionBackend is one real DB stack behind a real ProposalHandler.
type decisionBackend struct {
	name string
	// rawJSONPayload reports whether the proposals table accepts non-JSON
	// payload bytes (SQLite TEXT does; Postgres jsonb rejects them at INSERT).
	rawJSONPayload bool
	// newHandler builds the handler. wrap, when non-nil, wraps every per-accept
	// adapter — used to inject a ResolveAccepted failure after the real
	// Materialize already ran inside the open transaction.
	newHandler func(wrap func(proposal.AcceptAdapter) proposal.AcceptAdapter) *handler.ProposalHandler
	// create inserts a pending proposal and returns its id.
	create func(t *testing.T, typ proposal.Type, payload []byte) uuid.UUID
	// status returns the proposal row's current status.
	status func(t *testing.T, id uuid.UUID) string
	// decisionCount / goalCount count rows by exact title.
	decisionCount func(t *testing.T, title string) int
	goalCount     func(t *testing.T, title string) int
	// decisionAudit reads source / confirmed_by_human / actor_session_id of
	// the decisions row with title.
	decisionAudit func(t *testing.T, title string) decisionAuditRow
	// trackTitle registers cleanup of decisions/goals rows by title. A no-op
	// on SQLite (fresh :memory: DB per test); the PG container is shared.
	trackTitle func(t *testing.T, title string)
}

// uniqueTitle returns a title unique to this call and registers its cleanup.
func (be decisionBackend) uniqueTitle(t *testing.T, prefix string) string {
	t.Helper()
	title := prefix + "-" + be.name + "-" + uuid.NewString()
	be.trackTitle(t, title)
	return title
}

// titleOfBytes returns a unique title padded with ASCII to exactly n bytes.
func (be decisionBackend) titleOfBytes(t *testing.T, prefix string, n int) string {
	t.Helper()
	title := prefix + "-" + be.name + "-" + uuid.NewString()
	if len(title) > n {
		t.Fatalf("titleOfBytes: prefix title %q already longer than %d bytes", title, n)
	}
	title += strings.Repeat("p", n-len(title))
	be.trackTitle(t, title)
	return title
}

// malformedPayload is the closest thing to "malformed JSON" this backend can
// store: raw broken bytes on SQLite, valid-JSON-wrong-type on Postgres.
func (be decisionBackend) malformedPayload() []byte {
	if be.rawJSONPayload {
		return []byte(`{bad json`)
	}
	return []byte(`{"title":123}`)
}

// injectResolveFailure is the wrap func that makes ResolveAccepted fail.
func injectResolveFailure(a proposal.AcceptAdapter) proposal.AcceptAdapter {
	return failResolveAdapter{a}
}

// failResolveAdapter wraps a real proposal.AcceptAdapter and fails
// ResolveAccepted with an ErrNotFound-wrapping error AFTER the real
// Materialize ran. AcceptOrchestration must then roll the whole transaction
// back: the decision row must disappear and the proposal stay pending.
type failResolveAdapter struct {
	proposal.AcceptAdapter
}

func (failResolveAdapter) ResolveAccepted(context.Context) (*db.PendingProposal, error) {
	return nil, fmt.Errorf("injected: %w", proposal.ErrNotFound)
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return b
}

// decisionPayload is the AC-1 payload.
func decisionPayload(t *testing.T, title string) []byte {
	t.Helper()
	return mustMarshal(t, proposal.DecisionProposerPayload{
		Title:        title,
		Decision:     "d",
		Rationale:    "r",
		Alternatives: []string{"a1", "a2"},
		SessionID:    "s-1",
		TriggerTool:  "complete_task",
	})
}

func repeatAlts(n int, s string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func postConfirm(h *handler.ProposalHandler, id uuid.UUID, body string) *httptest.ResponseRecorder {
	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	return performRequest(e, http.MethodPost, "/api/proposals/"+id.String()+"/confirm", body)
}

func postConfirmBatch(t *testing.T, h *handler.ProposalHandler, action string, ids ...uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	raw := make([]string, len(ids))
	for i, id := range ids {
		raw[i] = id.String()
	}
	e := newEcho()
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch",
		string(mustMarshal(t, map[string]any{"ids": raw, "action": action})))
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm-batch: want 200 (per-row errors), got %d: %s", rec.Code, rec.Body.String())
	}
	return rec
}

type decisionConfirmResp struct {
	Proposal struct {
		Status string `json:"status"`
	} `json:"proposal"`
	Decision *db.Decision `json:"decision"`
}

// requireAcceptedDecision asserts a 200 accept response and returns its
// decision object. It checks for nil BEFORE any field read so a missing
// object fails with a named message instead of a nil-pointer panic.
func requireAcceptedDecision(t *testing.T, rec *httptest.ResponseRecorder) *db.Decision {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp decisionConfirmResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v (body: %s)", err, rec.Body.String())
	}
	if resp.Proposal.Status != proposalAccepted {
		t.Errorf("response proposal.status = %q, want %q", resp.Proposal.Status, proposalAccepted)
	}
	if resp.Decision == nil {
		t.Fatalf("response has no decision object (confirmResponse.Decision dropped): %s", rec.Body.String())
	}
	return resp.Decision
}

func requireCount(t *testing.T, what string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %d, want %d", what, got, want)
	}
}

func requireStatus(t *testing.T, be decisionBackend, id uuid.UUID, want string) {
	t.Helper()
	if got := be.status(t, id); got != want {
		t.Errorf("proposal %s status = %q, want %q", id, got, want)
	}
}

// ---------------------------------------------------------------------------
// Single route: POST /api/proposals/:id/confirm
// ---------------------------------------------------------------------------

// runDecisionMaterialises is AC-1 / AC-2: the accept writes exactly one
// decisions row, resolves the proposal, and returns the full decision object.
func runDecisionMaterialises(t *testing.T, be decisionBackend) {
	h := be.newHandler(nil)
	title := be.uniqueTitle(t, "T")
	id := be.create(t, proposal.TypeDecision, decisionPayload(t, title))

	dec := requireAcceptedDecision(t, postConfirm(h, id, acceptBody))

	if dec.Title != title {
		t.Errorf("decision.title = %q, want %q", dec.Title, title)
	}
	if dec.Source != string(decision.SourceAuto) {
		t.Errorf("decision.source = %q, want %q", dec.Source, decision.SourceAuto)
	}
	if dec.Context != wantDecisionContext {
		t.Errorf("decision.context = %q, want %q", dec.Context, wantDecisionContext)
	}
	if dec.Rationale != wantDecisionRationale {
		t.Errorf("decision.rationale = %q, want %q", dec.Rationale, wantDecisionRationale)
	}
	requireCount(t, "decisions rows", be.decisionCount(t, title), 1)
	requireStatus(t, be, id, proposalAccepted)
}

// runDecisionTagNoise is the AC-1 / AC-2 negative case: a store-level
// rejection (sanitize.ErrTagNoise) inside the transaction yields 500, no
// decisions row, and the proposal stays pending.
func runDecisionTagNoise(t *testing.T, be decisionBackend) {
	h := be.newHandler(nil)
	title := be.uniqueTitle(t, "x </decision> y")
	id := be.create(t, proposal.TypeDecision, decisionPayload(t, title))

	rec := postConfirm(h, id, acceptBody)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != internalErrorBody {
		t.Errorf("body = %s, want %s", got, internalErrorBody)
	}
	requireCount(t, "decisions rows", be.decisionCount(t, title), 0)
	requireStatus(t, be, id, proposalPending)
}

// runDecisionResolveFails is AC-4: when the resolve step fails after the
// decision insert, the insert is rolled back. The pre-seam design returned 200
// and left an orphan decisions row here.
func runDecisionResolveFails(t *testing.T, be decisionBackend) {
	h := be.newHandler(injectResolveFailure)
	title := be.uniqueTitle(t, "resolve-fails")
	id := be.create(t, proposal.TypeDecision, decisionPayload(t, title))

	rec := postConfirm(h, id, acceptBody)

	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "proposal already resolved") {
		t.Errorf("body = %s, want substring %q", rec.Body.String(), "proposal already resolved")
	}
	requireCount(t, "decisions rows after injected resolve failure", be.decisionCount(t, title), 0)
	requireStatus(t, be, id, proposalPending)
}

type rejectedPayloadCase struct {
	name    string
	title   string // row-count key; "" when the payload carries no title
	payload []byte
	wantMsg string
}

// rejectedDecisionPayloads lists the AC-5 inputs the single decoder
// (proposal.DecodeDecisionParams) must refuse with 400.
func rejectedDecisionPayloads(t *testing.T, be decisionBackend) []rejectedPayloadCase {
	t.Helper()
	pl := func(title string, mut func(*proposal.DecisionProposerPayload)) []byte {
		p := proposal.DecisionProposerPayload{Title: title}
		mut(&p)
		return mustMarshal(t, p)
	}
	noop := func(*proposal.DecisionProposerPayload) {}
	title513 := strings.Repeat("a", 513)
	titleCJK := strings.Repeat("決", 200) // 600 bytes: the cap counts bytes, not runes
	big := be.uniqueTitle(t, "decision-65537")
	rat := be.uniqueTitle(t, "rationale-65537")
	alts := be.uniqueTitle(t, "alts-51")
	alt := be.uniqueTitle(t, "alt-101")
	cases := []rejectedPayloadCase{
		{name: "title has the wrong JSON type", payload: []byte(`{"title":123}`), wantMsg: "decoding decision payload"},
		{name: "title missing", payload: []byte(`{"decision":"d"}`), wantMsg: "decision payload missing title"},
		{name: "title empty", payload: []byte(`{"title":""}`), wantMsg: "decision payload missing title"},
		{name: "title 513 bytes", title: title513, payload: pl(title513, noop), wantMsg: "decision title exceeds 512 bytes"},
		{name: "title 200 CJK chars (600 bytes)", title: titleCJK, payload: pl(titleCJK, noop), wantMsg: "decision title exceeds 512 bytes"},
		{
			name: "decision 65537 bytes", title: big, wantMsg: "decision text exceeds 64 KB",
			payload: pl(big, func(p *proposal.DecisionProposerPayload) { p.Decision = strings.Repeat("b", 65537) }),
		},
		{
			name: "rationale 65537 bytes", title: rat, wantMsg: "decision rationale exceeds 64 KB",
			payload: pl(rat, func(p *proposal.DecisionProposerPayload) { p.Rationale = strings.Repeat("c", 65537) }),
		},
		{
			name: "51 alternatives", title: alts, wantMsg: "too many alternatives (max 50)",
			payload: pl(alts, func(p *proposal.DecisionProposerPayload) { p.Alternatives = repeatAlts(51, "x") }),
		},
		{
			name: "one alternative of 101 bytes", title: alt, wantMsg: "individual alternative exceeds 100 bytes",
			payload: pl(alt, func(p *proposal.DecisionProposerPayload) { p.Alternatives = []string{strings.Repeat("d", 101)} }),
		},
	}
	if be.rawJSONPayload {
		cases = append(cases, rejectedPayloadCase{name: "malformed JSON", payload: []byte(`{bad json`), wantMsg: "decoding decision payload"})
	}
	return cases
}

// runDecisionPayloadRejected is AC-5: every over-cap or malformed payload is a
// 400 carrying the decoder's message, writes nothing, and leaves the proposal
// pending.
func runDecisionPayloadRejected(t *testing.T, be decisionBackend) {
	h := be.newHandler(nil)
	for _, tc := range rejectedDecisionPayloads(t, be) {
		t.Run(tc.name, func(t *testing.T) {
			id := be.create(t, proposal.TypeDecision, tc.payload)

			rec := postConfirm(h, id, acceptBody)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantMsg) {
				t.Errorf("body = %s, want substring %q", rec.Body.String(), tc.wantMsg)
			}
			requireCount(t, "decisions rows", be.decisionCount(t, tc.title), 0)
			requireStatus(t, be, id, proposalPending)
		})
	}
}

// runDecisionCapBoundaries is the AC-5 negative case: inputs exactly AT each
// cap still succeed, proving the 400 above is cap-driven and not blanket.
func runDecisionCapBoundaries(t *testing.T, be decisionBackend) {
	h := be.newHandler(nil)
	cases := []struct {
		name       string
		titleBytes int // 0 = ordinary unique title
		mut        func(*proposal.DecisionProposerPayload)
	}{
		{name: "title exactly 512 bytes", titleBytes: 512, mut: func(*proposal.DecisionProposerPayload) {}},
		{name: "50 alternatives of exactly 100 bytes", mut: func(p *proposal.DecisionProposerPayload) {
			p.Alternatives = repeatAlts(50, strings.Repeat("a", 100))
		}},
		{name: "decision exactly 65536 bytes", mut: func(p *proposal.DecisionProposerPayload) {
			p.Decision = strings.Repeat("b", 65536)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			title := be.uniqueTitle(t, "boundary")
			if tc.titleBytes > 0 {
				title = be.titleOfBytes(t, "boundary", tc.titleBytes)
			}
			p := proposal.DecisionProposerPayload{Title: title}
			tc.mut(&p)
			id := be.create(t, proposal.TypeDecision, mustMarshal(t, p))

			dec := requireAcceptedDecision(t, postConfirm(h, id, acceptBody))

			if dec.Title != title {
				t.Errorf("decision.title length = %d, want %d", len(dec.Title), len(title))
			}
			requireCount(t, "decisions rows", be.decisionCount(t, title), 1)
			requireStatus(t, be, id, proposalAccepted)
		})
	}
}

// runDecisionForgedSource is AC-8: unknown keys smuggled into the stored
// payload ("source", "confirmed_by_human", "actor_session_id") never reach the
// decisions row — a human accepting a proposal does not change its auto origin.
func runDecisionForgedSource(t *testing.T, be decisionBackend) {
	h := be.newHandler(nil)
	title := be.uniqueTitle(t, "forged")
	id := be.create(t, proposal.TypeDecision, mustMarshal(t, map[string]any{
		"title": title, "source": "manual", "confirmed_by_human": true, "actor_session_id": "x",
	}))

	dec := requireAcceptedDecision(t, postConfirm(h, id, acceptBody))

	if dec.Source != string(decision.SourceAuto) {
		t.Errorf("response decision.source = %q, want %q", dec.Source, decision.SourceAuto)
	}
	got := be.decisionAudit(t, title)
	if got.Source != string(decision.SourceAuto) {
		t.Errorf("stored source = %q, want %q", got.Source, decision.SourceAuto)
	}
	if got.Confirmed {
		t.Error("stored confirmed_by_human = true, want false")
	}
	if !got.ActorIsNull {
		t.Error("stored actor_session_id is set, want NULL")
	}
}

// runDecisionDoubleAccept is AC-9: the second accept of the same proposal is a
// 409 and writes nothing, while an unrelated pending proposal still accepts.
func runDecisionDoubleAccept(t *testing.T, be decisionBackend) {
	h := be.newHandler(nil)
	title := be.uniqueTitle(t, "double")
	id := be.create(t, proposal.TypeDecision, decisionPayload(t, title))
	other := be.uniqueTitle(t, "double-other")
	otherID := be.create(t, proposal.TypeDecision, decisionPayload(t, other))

	requireAcceptedDecision(t, postConfirm(h, id, acceptBody))
	rec := postConfirm(h, id, acceptBody)

	if rec.Code != http.StatusConflict {
		t.Fatalf("second accept: want 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "proposal already resolved") {
		t.Errorf("body = %s, want substring %q", rec.Body.String(), "proposal already resolved")
	}
	requireCount(t, "decisions rows after second accept", be.decisionCount(t, title), 1)

	requireAcceptedDecision(t, postConfirm(h, otherID, acceptBody))
	requireCount(t, "decisions rows of the unrelated proposal", be.decisionCount(t, other), 1)
}

// ---------------------------------------------------------------------------
// Batch route: POST /api/proposals/confirm-batch
// ---------------------------------------------------------------------------

// runBatchMaterialises is AC-6: every valid TypeDecision id in the batch is
// ok:true and writes its own decisions row.
func runBatchMaterialises(t *testing.T, be decisionBackend) {
	h := be.newHandler(nil)
	titles := []string{be.uniqueTitle(t, "batch-1"), be.uniqueTitle(t, "batch-2")}
	ids := []uuid.UUID{
		be.create(t, proposal.TypeDecision, decisionPayload(t, titles[0])),
		be.create(t, proposal.TypeDecision, decisionPayload(t, titles[1])),
	}

	rec := postConfirmBatch(t, h, batchActionAccept, ids...)

	for i, id := range ids {
		assertBatchResultOK(t, rec.Body.Bytes(), id.String())
		requireCount(t, "decisions rows for "+titles[i], be.decisionCount(t, titles[i]), 1)
		requireStatus(t, be, id, proposalAccepted)
	}
}

// runBatchMixedValidAndInvalid is the AC-6 negative case: a malformed and an
// over-cap payload are per-row errors with the decoder's message, do not block
// the valid id, and stay pending.
func runBatchMixedValidAndInvalid(t *testing.T, be decisionBackend) {
	h := be.newHandler(nil)
	validTitle := be.uniqueTitle(t, "mixed-valid")
	bigTitle := be.titleOfBytes(t, "mixed-big", 513)
	validID := be.create(t, proposal.TypeDecision, decisionPayload(t, validTitle))
	badID := be.create(t, proposal.TypeDecision, be.malformedPayload())
	bigID := be.create(t, proposal.TypeDecision, decisionPayload(t, bigTitle))

	rec := postConfirmBatch(t, h, batchActionAccept, validID, badID, bigID)

	assertBatchResultOK(t, rec.Body.Bytes(), validID.String())
	for _, tc := range []struct {
		id      uuid.UUID
		wantMsg string
	}{
		{badID, "decoding decision payload"},
		{bigID, "decision title exceeds 512 bytes"},
	} {
		entry := decodeBatchResultEntry(t, rec.Body.Bytes(), tc.id.String())
		if entry.OK {
			t.Errorf("entry %s ok = true, want false", tc.id)
		}
		if !strings.Contains(entry.Error, tc.wantMsg) {
			t.Errorf("entry %s error = %q, want substring %q", tc.id, entry.Error, tc.wantMsg)
		}
		requireStatus(t, be, tc.id, proposalPending)
	}
	requireCount(t, "decisions rows of the valid id", be.decisionCount(t, validTitle), 1)
	requireCount(t, "decisions rows of the over-cap id", be.decisionCount(t, bigTitle), 0)
}

// runBatchAlreadyAccepted is AC-7(a): re-accepting a resolved id is
// skipped:true and writes nothing; the skip is per-row, so a fresh id in the
// same request still succeeds.
func runBatchAlreadyAccepted(t *testing.T, be decisionBackend) {
	h := be.newHandler(nil)
	doneTitle := be.uniqueTitle(t, "already-done")
	freshTitle := be.uniqueTitle(t, "already-fresh")
	doneID := be.create(t, proposal.TypeDecision, decisionPayload(t, doneTitle))
	freshID := be.create(t, proposal.TypeDecision, decisionPayload(t, freshTitle))
	requireAcceptedDecision(t, postConfirm(h, doneID, acceptBody))

	rec := postConfirmBatch(t, h, batchActionAccept, doneID, freshID)

	entry := decodeBatchResultEntry(t, rec.Body.Bytes(), doneID.String())
	if entry.OK || !entry.Skipped {
		t.Errorf("already-accepted entry = %+v, want ok:false skipped:true", entry)
	}
	if entry.Error != "not found or already resolved" {
		t.Errorf("already-accepted entry error = %q, want %q", entry.Error, "not found or already resolved")
	}
	requireCount(t, "decisions rows of the already-accepted id", be.decisionCount(t, doneTitle), 1)
	assertBatchResultOK(t, rec.Body.Bytes(), freshID.String())
	requireCount(t, "decisions rows of the fresh id", be.decisionCount(t, freshTitle), 1)
}

// runBatchReject is AC-7(b): reject stays a pure status flip and never
// materialises a decision.
func runBatchReject(t *testing.T, be decisionBackend) {
	h := be.newHandler(nil)
	title := be.uniqueTitle(t, "reject")
	id := be.create(t, proposal.TypeDecision, decisionPayload(t, title))

	rec := postConfirmBatch(t, h, "reject", id)

	assertBatchResultOK(t, rec.Body.Bytes(), id.String())
	requireStatus(t, be, id, string(proposal.StatusRejected))
	requireCount(t, "decisions rows", be.decisionCount(t, title), 0)
}

// runBatchMixedWithGoal proves per-row routing under heterogeneous types: a
// TypeDecision and a TypeGoal in one batch both go through the seam and both
// materialise.
func runBatchMixedWithGoal(t *testing.T, be decisionBackend) {
	h := be.newHandler(nil)
	decTitle := be.uniqueTitle(t, "mixgoal-dec")
	goalTitle := be.uniqueTitle(t, "mixgoal-goal")
	decID := be.create(t, proposal.TypeDecision, decisionPayload(t, decTitle))
	goalID := be.create(t, proposal.TypeGoal, mustMarshal(t, map[string]any{"title": goalTitle, "area": "career"}))

	rec := postConfirmBatch(t, h, batchActionAccept, decID, goalID)

	assertBatchResultOK(t, rec.Body.Bytes(), decID.String())
	assertBatchResultOK(t, rec.Body.Bytes(), goalID.String())
	requireCount(t, "decisions rows", be.decisionCount(t, decTitle), 1)
	requireCount(t, "goals rows", be.goalCount(t, goalTitle), 1)
}

// runBatchResolveFails mirrors AC-4 on the batch route: the injected resolve
// failure maps to skipped:true, rolls the decision insert back, and leaves the
// proposal pending.
func runBatchResolveFails(t *testing.T, be decisionBackend) {
	h := be.newHandler(injectResolveFailure)
	title := be.uniqueTitle(t, "batch-resolve-fails")
	id := be.create(t, proposal.TypeDecision, decisionPayload(t, title))

	rec := postConfirmBatch(t, h, batchActionAccept, id)

	entry := decodeBatchResultEntry(t, rec.Body.Bytes(), id.String())
	if entry.OK || !entry.Skipped {
		t.Errorf("entry = %+v, want ok:false skipped:true", entry)
	}
	requireCount(t, "decisions rows after injected resolve failure", be.decisionCount(t, title), 0)
	requireStatus(t, be, id, proposalPending)
}

// ---------------------------------------------------------------------------
// SQLite backend + per-case Test functions
// ---------------------------------------------------------------------------

// newSQLiteDecisionBackend opens a fresh :memory: SQLite DB (migrations
// applied by wbtsqlite.Open) wired to the real sqlite accept adapter.
func newSQLiteDecisionBackend(t *testing.T) decisionBackend {
	t.Helper()
	ctx := context.Background()
	sdb, err := wbtsqlite.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("wbtsqlite.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := sdb.Close(); err != nil {
			t.Logf("close sqlite: %v", err)
		}
	})

	propStore := wbtsqlite.NewProposalStore(sdb)
	learningStore := wbtsqlite.NewLearningStore(sdb)
	deps := wbtsqlite.AcceptDeps{
		Proposal: propStore,
		GTD:      wbtsqlite.NewGTDStore(sdb),
		Learning: learningStore,
		Decision: wbtsqlite.NewDecisionStore(sdb),
	}
	// scanCount takes the *sql.Row (not a query string) so every SQL text stays
	// a literal at its call site — unqueryvet rejects a variable query.
	scanCount := func(t *testing.T, row *sql.Row) int {
		t.Helper()
		var n int
		if err := row.Scan(&n); err != nil {
			t.Fatalf("count query: %v", err)
		}
		return n
	}
	return decisionBackend{
		name:           "sqlite",
		rawJSONPayload: true,
		newHandler: func(wrap func(proposal.AcceptAdapter) proposal.AcceptAdapter) *handler.ProposalHandler {
			return handler.NewProposalHandler(propStore, learningStore).
				WithGoalProjectAccept(func(id uuid.UUID) proposal.AcceptAdapter {
					a := wbtsqlite.NewAcceptAdapter(id, deps)
					if wrap != nil {
						return wrap(a)
					}
					return a
				})
		},
		create: func(t *testing.T, typ proposal.Type, payload []byte) uuid.UUID {
			t.Helper()
			p, err := propStore.Create(ctx, proposal.CreateParams{Type: typ, Payload: payload})
			if err != nil {
				t.Fatalf("Create proposal: %v", err)
			}
			return p.ID
		},
		status: func(t *testing.T, id uuid.UUID) string {
			t.Helper()
			p, err := propStore.Get(ctx, id)
			if err != nil {
				t.Fatalf("Get proposal: %v", err)
			}
			return p.Status
		},
		decisionCount: func(t *testing.T, title string) int {
			t.Helper()
			return scanCount(t, sdb.QueryRowContext(ctx, "SELECT count(*) FROM decisions WHERE title = ?", title))
		},
		goalCount: func(t *testing.T, title string) int {
			t.Helper()
			return scanCount(t, sdb.QueryRowContext(ctx, "SELECT count(*) FROM goals WHERE title = ?", title))
		},
		decisionAudit: func(t *testing.T, title string) decisionAuditRow {
			t.Helper()
			var (
				row       decisionAuditRow
				confirmed int
				actorNull int
			)
			err := sdb.QueryRowContext(ctx,
				"SELECT source, confirmed_by_human, actor_session_id IS NULL FROM decisions WHERE title = ?", title).
				Scan(&row.Source, &confirmed, &actorNull)
			if err != nil {
				t.Fatalf("decision audit query: %v", err)
			}
			row.Confirmed = confirmed != 0
			row.ActorIsNull = actorNull != 0
			return row
		},
		trackTitle: func(*testing.T, string) {},
	}
}

func TestConfirmProposal_TypeDecision_SQLite_MaterialisesDecisionsRow(t *testing.T) {
	runDecisionMaterialises(t, newSQLiteDecisionBackend(t))
}

func TestConfirmProposal_TypeDecision_SQLite_TagNoise_RollsBack(t *testing.T) {
	runDecisionTagNoise(t, newSQLiteDecisionBackend(t))
}

func TestConfirmProposal_TypeDecision_SQLite_ResolveFails_RollsBackDecision(t *testing.T) {
	runDecisionResolveFails(t, newSQLiteDecisionBackend(t))
}

func TestConfirmProposal_TypeDecision_SQLite_PayloadRejected_BadRequest(t *testing.T) {
	runDecisionPayloadRejected(t, newSQLiteDecisionBackend(t))
}

func TestConfirmProposal_TypeDecision_SQLite_CapBoundaries_Accepted(t *testing.T) {
	runDecisionCapBoundaries(t, newSQLiteDecisionBackend(t))
}

func TestConfirmProposal_TypeDecision_SQLite_ForgedSourceIgnored(t *testing.T) {
	runDecisionForgedSource(t, newSQLiteDecisionBackend(t))
}

func TestConfirmProposal_TypeDecision_SQLite_DoubleAccept_Conflict(t *testing.T) {
	runDecisionDoubleAccept(t, newSQLiteDecisionBackend(t))
}

func TestConfirmBatch_TypeDecision_SQLite_MaterialisesDecisionsRows(t *testing.T) {
	runBatchMaterialises(t, newSQLiteDecisionBackend(t))
}

func TestConfirmBatch_TypeDecision_SQLite_MixedValidAndInvalid(t *testing.T) {
	runBatchMixedValidAndInvalid(t, newSQLiteDecisionBackend(t))
}

func TestConfirmBatch_TypeDecision_SQLite_AlreadyAccepted_Skipped(t *testing.T) {
	runBatchAlreadyAccepted(t, newSQLiteDecisionBackend(t))
}

func TestConfirmBatch_TypeDecision_SQLite_Reject_NoMaterialise(t *testing.T) {
	runBatchReject(t, newSQLiteDecisionBackend(t))
}

func TestConfirmBatch_TypeDecision_SQLite_MixedWithGoal(t *testing.T) {
	runBatchMixedWithGoal(t, newSQLiteDecisionBackend(t))
}

func TestConfirmBatch_TypeDecision_SQLite_ResolveFails_RollsBackDecision(t *testing.T) {
	runBatchResolveFails(t, newSQLiteDecisionBackend(t))
}
