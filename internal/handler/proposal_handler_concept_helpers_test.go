package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// TypeConcept HTTP accept goes through proposal.AcceptOrchestration on both
// backends. Like the TypeDecision suite (proposal_handler_decision_sqlite_test.go)
// every case is written ONCE against conceptBackend and run by a thin
// per-backend Test function (SQLite in proposal_handler_concept_sqlite_test.go,
// Postgres in proposal_handler_concept_pg_test.go). Each backend drives the real
// stores and the real accept adapter — no mocks (PG = testcontainers, SQLite =
// real :memory: DB).

const (
	conceptContent       = "Memory decays without review."
	conceptTagLearning   = "learning"
	conceptStatusActive  = "active"
	errBoomText          = "boom"
	conceptOversizeTitle = "concept title exceeds 512"
	conceptTooManyTags   = "too many tags (max 50)"
	conceptAlreadyDone   = "proposal already resolved"
)

// tripwireConceptStore is the handler's first-class learning dependency. With
// the accept adapter wired it must NEVER run: any call fails the test. That is
// what separates "went through the seam" from "the legacy path happened to
// produce the same rows".
type tripwireConceptStore struct{ t *testing.T }

func (s tripwireConceptStore) CreateConcept(context.Context, string, string, []string) (*db.Concept, error) {
	s.t.Errorf("legacy h.learning.CreateConcept ran: TypeConcept accept did not go through the accept seam")
	return nil, errors.New("tripwire: legacy concept path used")
}

// failResolveErrAdapter wraps a real proposal.AcceptAdapter and fails
// ResolveAccepted with err AFTER the real Materialize already inserted the
// concept (and its review_schedule row) inside the open tx. Unlike
// failResolveAdapter it lets a test pick the error, so one case can inject
// proposal.ErrNotFound (a concurrent resolver won) and another an arbitrary
// store error whose text must not reach the client.
type failResolveErrAdapter struct {
	proposal.AcceptAdapter
	err error
}

func (f failResolveErrAdapter) ResolveAccepted(context.Context) (*db.PendingProposal, error) {
	return nil, f.err
}

func injectResolveErr(err error) func(proposal.AcceptAdapter) proposal.AcceptAdapter {
	return func(a proposal.AcceptAdapter) proposal.AcceptAdapter {
		return failResolveErrAdapter{AcceptAdapter: a, err: err}
	}
}

// conceptBackend is one real DB stack behind a real ProposalHandler.
type conceptBackend struct {
	name string
	// newHandler builds the handler over the real stores with the tripwire as
	// its learning dependency. wrap, when non-nil, wraps every per-accept adapter.
	newHandler func(t *testing.T, wrap func(proposal.AcceptAdapter) proposal.AcceptAdapter) *handler.ProposalHandler
	// create inserts a pending proposal and returns its id.
	create func(t *testing.T, typ proposal.Type, payload []byte) uuid.UUID
	// status returns the proposal row's current status.
	status func(t *testing.T, id uuid.UUID) string
	// conceptCount counts concepts rows by exact title; decisionCount the same for decisions.
	conceptCount  func(t *testing.T, title string) int
	decisionCount func(t *testing.T, title string) int
	// conceptID returns the id of the single concepts row with that title.
	conceptID func(t *testing.T, title string) string
	// scheduleCount counts review_schedule rows of one concept id.
	scheduleCount func(t *testing.T, conceptID string) int
	// trackTitle registers cleanup of concepts/review_schedule/decisions rows
	// by title. A no-op on SQLite (fresh :memory: DB per test); the PG container is shared.
	trackTitle func(t *testing.T, title string)
}

func (be conceptBackend) uniqueTitle(t *testing.T, prefix string) string {
	t.Helper()
	title := prefix + "-" + be.name + "-" + uuid.NewString()
	be.trackTitle(t, title)
	return title
}

// titleOfBytes returns a unique title padded with ASCII to exactly n bytes.
func (be conceptBackend) titleOfBytes(t *testing.T, prefix string, n int) string {
	t.Helper()
	title := prefix + "-" + be.name + "-" + uuid.NewString()
	if len(title) > n {
		t.Fatalf("titleOfBytes: prefix title %q already longer than %d bytes", title, n)
	}
	title += strings.Repeat("p", n-len(title))
	be.trackTitle(t, title)
	return title
}

func conceptPayloadJSON(t *testing.T, title string) []byte {
	t.Helper()
	return mustMarshal(t, map[string]any{"title": title, "content": conceptContent, "tags": []string{conceptTagLearning}})
}

// scanOneInt / scanOneString take a row's Scan method value rather than a
// *sql.Row so this package never imports database/sql (importing it makes noctx
// start analysing the whole package's *sql.DB calls — see the SQLite decision
// suite's scanCount for the history).
func scanOneInt(t *testing.T, scan func(...any) error) int {
	t.Helper()
	var n int
	if err := scan(&n); err != nil {
		t.Fatalf("count query: %v", err)
	}
	return n
}

func scanOneString(t *testing.T, scan func(...any) error) string {
	t.Helper()
	var s string
	if err := scan(&s); err != nil {
		t.Fatalf("id query: %v", err)
	}
	return s
}

// conceptWant is what the singular accept response must carry for a concept.
type conceptWant struct {
	ID      string
	Title   string
	Content string
	Tags    []string
}

// checkConceptResponse is the ONE helper that asserts the singular accept
// response on BOTH backends (the cbbbf686 parity comparison): `concept` is
// present and complete (id equals the concepts row id, title/content/tags equal
// the payload, status active, timestamps present) and the proposal is accepted.
// It returns the first problem instead of failing the test so a self-test can
// prove it rejects the pre-fix SQLite shape (only id and title).
func checkConceptResponse(body []byte, want conceptWant) error {
	var resp struct {
		Proposal struct {
			Status string `json:"status"`
		} `json:"proposal"`
		Concept *struct {
			ID        string   `json:"id"`
			Title     string   `json:"title"`
			Content   string   `json:"content"`
			Tags      []string `json:"tags"`
			Status    string   `json:"status"`
			CreatedAt *string  `json:"created_at"`
		} `json:"concept"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("response is not valid JSON: %w (body: %s)", err, body)
	}
	if resp.Proposal.Status != string(proposal.StatusAccepted) {
		return fmt.Errorf("proposal.status = %q, want %q", resp.Proposal.Status, proposal.StatusAccepted)
	}
	c := resp.Concept
	if c == nil {
		return fmt.Errorf("response has no non-null concept object: %s", body)
	}
	if _, err := uuid.Parse(c.ID); err != nil {
		return fmt.Errorf("concept.id %q is not a UUID: %w", c.ID, err)
	}
	if c.ID != want.ID {
		return fmt.Errorf("concept.id = %q, want the concepts row id %q", c.ID, want.ID)
	}
	if c.Title != want.Title {
		return fmt.Errorf("concept.title = %q, want %q", c.Title, want.Title)
	}
	if c.Content != want.Content {
		return fmt.Errorf("concept.content = %q, want %q (an id/title-only concept object?)", c.Content, want.Content)
	}
	if !reflect.DeepEqual(c.Tags, want.Tags) {
		return fmt.Errorf("concept.tags = %v, want %v", c.Tags, want.Tags)
	}
	if c.Status != conceptStatusActive {
		return fmt.Errorf("concept.status = %q, want %q", c.Status, conceptStatusActive)
	}
	if c.CreatedAt == nil {
		return errors.New("concept.created_at is missing or null")
	}
	return nil
}

func requireConceptResponse(t *testing.T, rec *httptest.ResponseRecorder, want conceptWant) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := checkConceptResponse(rec.Body.Bytes(), want); err != nil {
		t.Fatal(err)
	}
}

// TestConfirmProposal_TypeConcept_ResponseHelper_RejectsIDTitleOnly proves the
// shared helper is not vacuous: the pre-fix SQLite response carried only
// {"id","title"} for the concept, and the helper MUST refuse it (and a response
// with no concept at all), while still accepting a complete one.
func TestConfirmProposal_TypeConcept_ResponseHelper_RejectsIDTitleOnly(t *testing.T) {
	id := uuid.NewString()
	want := conceptWant{ID: id, Title: "t", Content: conceptContent, Tags: []string{conceptTagLearning}}
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name: "complete concept passes (positive control)",
			body: `{"proposal":{"status":"accepted"},"concept":{"id":"` + id + `","title":"t","content":"` + conceptContent +
				`","tags":["learning"],"status":"active","created_at":"2026-10-14T00:00:00Z"}}`,
		},
		{
			name:    "id/title-only concept is rejected: the pre-fix SQLite shape",
			body:    `{"proposal":{"status":"accepted"},"concept":{"id":"` + id + `","title":"t"}}`,
			wantErr: true,
		},
		{
			name:    "missing concept is rejected",
			body:    `{"proposal":{"status":"accepted"}}`,
			wantErr: true,
		},
		{
			name:    "null concept is rejected",
			body:    `{"proposal":{"status":"accepted"},"concept":null}`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkConceptResponse([]byte(tc.body), want)
			if tc.wantErr && err == nil {
				t.Error("helper accepted a response it must reject")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("helper rejected a complete response: %v", err)
			}
		})
	}
}

func requireConceptProposalStatus(t *testing.T, be conceptBackend, id uuid.UUID, want string) {
	t.Helper()
	if got := be.status(t, id); got != want {
		t.Errorf("proposal %s status = %q, want %q", id, got, want)
	}
}

// ---------------------------------------------------------------------------
// Single route: POST /api/proposals/:id/confirm
// ---------------------------------------------------------------------------

// runConceptMaterialises is AC1 / AC2: the accept writes exactly one concepts
// row and its review_schedule row, resolves the proposal, and returns the full
// concept — identically on both backends.
func runConceptMaterialises(t *testing.T, be conceptBackend) {
	h := be.newHandler(t, nil)
	title := be.uniqueTitle(t, "concept")
	id := be.create(t, proposal.TypeConcept, conceptPayloadJSON(t, title))

	rec := postConfirm(h, id, acceptBody)

	requireCount(t, "concepts rows", be.conceptCount(t, title), 1)
	conceptID := be.conceptID(t, title)
	requireCount(t, "review_schedule rows of the concept", be.scheduleCount(t, conceptID), 1)
	requireConceptResponse(t, rec, conceptWant{ID: conceptID, Title: title, Content: conceptContent, Tags: []string{conceptTagLearning}})
	requireConceptProposalStatus(t, be, id, proposalAccepted)
}

// runConceptResolveFails is AC4: when the resolve step fails after the concept
// insert, the whole transaction rolls back. The pre-seam design left an orphan
// concepts row here.
func runConceptResolveFails(t *testing.T, be conceptBackend) {
	t.Run("a concurrent resolver (ErrNotFound) gives 409 and no orphan concept", func(t *testing.T) {
		h := be.newHandler(t, injectResolveErr(fmt.Errorf("injected: %w", proposal.ErrNotFound)))
		title := be.uniqueTitle(t, "resolve-race")
		id := be.create(t, proposal.TypeConcept, conceptPayloadJSON(t, title))

		rec := postConfirm(h, id, acceptBody)

		if rec.Code != http.StatusConflict {
			t.Fatalf("want 409, got %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), conceptAlreadyDone) {
			t.Errorf("body = %s, want substring %q", rec.Body.String(), conceptAlreadyDone)
		}
		requireCount(t, "concepts rows after injected resolve failure", be.conceptCount(t, title), 0)
		requireConceptProposalStatus(t, be, id, proposalPending)
	})
	t.Run("an unexpected store error gives 500 without leaking its text", func(t *testing.T) {
		h := be.newHandler(t, injectResolveErr(errors.New(errBoomText)))
		title := be.uniqueTitle(t, "resolve-boom")
		id := be.create(t, proposal.TypeConcept, conceptPayloadJSON(t, title))

		rec := postConfirm(h, id, acceptBody)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := strings.TrimSpace(rec.Body.String()); got != internalErrorBody {
			t.Errorf("body = %s, want exactly %s (no raw error text)", got, internalErrorBody)
		}
		requireCount(t, "concepts rows after injected resolve failure", be.conceptCount(t, title), 0)
		requireConceptProposalStatus(t, be, id, proposalPending)
	})
}

// runConceptOversize is AC5: over-cap payloads are a 400 with the gate's text,
// never open the transaction, and leave the proposal pending. (jsonb accepts a
// 513-byte string, so these fixtures are valid JSON that violates a cap.)
func runConceptOversize(t *testing.T, be conceptBackend) {
	h := be.newHandler(t, nil)
	title513 := strings.Repeat("a", 513)
	tagsTitle := be.uniqueTitle(t, "tags-51")
	cases := []struct {
		name    string
		title   string
		payload []byte
		wantMsg string
	}{
		{"title 513 bytes", title513, mustMarshal(t, map[string]any{"title": title513, "content": "c"}), conceptOversizeTitle},
		{
			"51 tags", tagsTitle,
			mustMarshal(t, map[string]any{"title": tagsTitle, "content": "c", "tags": make([]string, 51)}), conceptTooManyTags,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := be.create(t, proposal.TypeConcept, tc.payload)

			rec := postConfirm(h, id, acceptBody)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantMsg) {
				t.Errorf("body = %s, want substring %q", rec.Body.String(), tc.wantMsg)
			}
			requireCount(t, "concepts rows", be.conceptCount(t, tc.title), 0)
			requireConceptProposalStatus(t, be, id, proposalPending)
		})
	}
}

// runConceptAlreadyResolved is AC6: the second accept of the same proposal is a
// 409 and writes nothing, while an unrelated pending proposal still accepts.
func runConceptAlreadyResolved(t *testing.T, be conceptBackend) {
	h := be.newHandler(t, nil)
	title := be.uniqueTitle(t, "double")
	id := be.create(t, proposal.TypeConcept, conceptPayloadJSON(t, title))
	other := be.uniqueTitle(t, "double-other")
	otherID := be.create(t, proposal.TypeConcept, conceptPayloadJSON(t, other))

	first := postConfirm(h, id, acceptBody)
	if first.Code != http.StatusOK {
		t.Fatalf("first accept: want 200, got %d: %s", first.Code, first.Body.String())
	}
	rec := postConfirm(h, id, acceptBody)

	if rec.Code != http.StatusConflict {
		t.Fatalf("second accept: want 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), conceptAlreadyDone) {
		t.Errorf("body = %s, want substring %q", rec.Body.String(), conceptAlreadyDone)
	}
	requireCount(t, "concepts rows after second accept", be.conceptCount(t, title), 1)

	if again := postConfirm(h, otherID, acceptBody); again.Code != http.StatusOK {
		t.Fatalf("unrelated proposal: want 200, got %d: %s", again.Code, again.Body.String())
	}
	requireCount(t, "concepts rows of the unrelated proposal", be.conceptCount(t, other), 1)
}

// ---------------------------------------------------------------------------
// Batch route: POST /api/proposals/confirm-batch
// ---------------------------------------------------------------------------

// runBatchConceptMaterialises is AC7: every valid TypeConcept id in the batch
// is ok:true and writes its own concepts row and review_schedule row.
func runBatchConceptMaterialises(t *testing.T, be conceptBackend) {
	h := be.newHandler(t, nil)
	titles := []string{be.uniqueTitle(t, "batch-1"), be.uniqueTitle(t, "batch-2")}
	ids := []uuid.UUID{
		be.create(t, proposal.TypeConcept, conceptPayloadJSON(t, titles[0])),
		be.create(t, proposal.TypeConcept, conceptPayloadJSON(t, titles[1])),
	}

	rec := postConfirmBatch(t, h, batchActionAccept, ids...)

	for i, id := range ids {
		assertBatchResultOK(t, rec.Body.Bytes(), id.String())
		requireCount(t, "concepts rows for "+titles[i], be.conceptCount(t, titles[i]), 1)
		requireCount(t, "review_schedule rows for "+titles[i], be.scheduleCount(t, be.conceptID(t, titles[i])), 1)
		requireConceptProposalStatus(t, be, id, proposalAccepted)
	}
}

// runBatchConceptMixedValidAndOversize is AC8: an over-cap payload is a per-row
// error with the gate's text, does not block the valid id, and stays pending.
func runBatchConceptMixedValidAndOversize(t *testing.T, be conceptBackend) {
	h := be.newHandler(t, nil)
	validTitle := be.uniqueTitle(t, "mixed-valid")
	bigTitle := be.titleOfBytes(t, "mixed-big", 513)
	validID := be.create(t, proposal.TypeConcept, conceptPayloadJSON(t, validTitle))
	bigID := be.create(t, proposal.TypeConcept, conceptPayloadJSON(t, bigTitle))

	rec := postConfirmBatch(t, h, batchActionAccept, validID, bigID)

	assertBatchResultOK(t, rec.Body.Bytes(), validID.String())
	entry := decodeBatchResultEntry(t, rec.Body.Bytes(), bigID.String())
	if entry.OK {
		t.Errorf("over-cap entry ok = true, want false")
	}
	if !strings.Contains(entry.Error, conceptOversizeTitle) {
		t.Errorf("over-cap entry error = %q, want substring %q", entry.Error, conceptOversizeTitle)
	}
	requireConceptProposalStatus(t, be, bigID, proposalPending)
	requireCount(t, "concepts rows of the valid id", be.conceptCount(t, validTitle), 1)
	requireCount(t, "concepts rows of the over-cap id", be.conceptCount(t, bigTitle), 0)
}

// runBatchConceptResolveFails is AC9: the injected resolve failure maps to
// skipped:true, rolls the concept insert back, and leaves the proposal pending.
func runBatchConceptResolveFails(t *testing.T, be conceptBackend) {
	h := be.newHandler(t, injectResolveErr(fmt.Errorf("injected: %w", proposal.ErrNotFound)))
	title := be.uniqueTitle(t, "batch-resolve-fails")
	id := be.create(t, proposal.TypeConcept, conceptPayloadJSON(t, title))

	rec := postConfirmBatch(t, h, batchActionAccept, id)

	entry := decodeBatchResultEntry(t, rec.Body.Bytes(), id.String())
	if entry.OK || !entry.Skipped {
		t.Errorf("entry = %+v, want ok:false skipped:true", entry)
	}
	if entry.Error != "not found or already resolved" {
		t.Errorf("entry error = %q, want %q", entry.Error, "not found or already resolved")
	}
	requireCount(t, "concepts rows after injected resolve failure", be.conceptCount(t, title), 0)
	requireConceptProposalStatus(t, be, id, proposalPending)
}

// runBatchConceptMixedWithDecision takes over the "Decision + Concept in one
// batch, both materialise" protection that the deleted fake-store batch test
// carried: a valid TypeDecision and a valid TypeConcept in one request both go
// through the seam and both write their rows.
func runBatchConceptMixedWithDecision(t *testing.T, be conceptBackend) {
	h := be.newHandler(t, nil)
	decTitle := be.uniqueTitle(t, "mixdec-dec")
	conTitle := be.uniqueTitle(t, "mixdec-con")
	decID := be.create(t, proposal.TypeDecision, decisionPayload(t, decTitle))
	conID := be.create(t, proposal.TypeConcept, conceptPayloadJSON(t, conTitle))

	rec := postConfirmBatch(t, h, batchActionAccept, decID, conID)

	assertBatchResultOK(t, rec.Body.Bytes(), decID.String())
	assertBatchResultOK(t, rec.Body.Bytes(), conID.String())
	requireCount(t, "decisions rows", be.decisionCount(t, decTitle), 1)
	requireCount(t, "concepts rows", be.conceptCount(t, conTitle), 1)
	requireCount(t, "review_schedule rows", be.scheduleCount(t, be.conceptID(t, conTitle)), 1)
}
