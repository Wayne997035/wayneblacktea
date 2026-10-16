package handler_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// Postgres twins of the taskBackend cases (bodies live in
// proposal_handler_task_helpers_test.go), run against the shared testcontainers
// pool (openGoalProjectTestPgPool; skipped under -short). The container is
// shared across the package, so every row a test creates is removed by title /
// id in t.Cleanup and every title is unique.

// newPGTaskBackend wires a ProposalHandler to the real PG accept adapter over
// the shared pool. Skips (via openGoalProjectTestPgPool) under -short.
func newPGTaskBackend(t *testing.T) taskBackend {
	t.Helper()
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()

	propStore := proposal.NewStore(pool, nil)
	deps := proposal.PgAcceptDeps{
		Pool:     pool,
		Proposal: propStore,
		GTD:      gtd.NewStore(pool, nil),
		Learning: learning.NewStore(pool, nil),
		Decision: decision.NewStore(pool, nil),
	}
	exec := func(t *testing.T, query string, arg any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, arg); err != nil {
			t.Logf("cleanup %q: %v", query, err)
		}
	}
	newAdapter := func(id uuid.UUID) proposal.AcceptAdapter { return proposal.NewPgAcceptAdapter(id, deps) }
	return taskBackend{
		name:       "pg",
		newAdapter: newAdapter,
		newHandler: func(wrap func(proposal.AcceptAdapter) proposal.AcceptAdapter) *handler.ProposalHandler {
			return handler.NewProposalHandler(propStore, &fakeProposalLearningStore{}).
				WithGoalProjectAccept(func(id uuid.UUID) proposal.AcceptAdapter {
					a := newAdapter(id)
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
			t.Cleanup(func() { exec(t, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })
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
		taskCount: func(t *testing.T, title string) int {
			t.Helper()
			return scanOneInt(t, pool.QueryRow(ctx, "SELECT count(*) FROM tasks WHERE title = $1", title).Scan)
		},
		taskCountByDescription: func(t *testing.T, description string) int {
			t.Helper()
			return scanOneInt(t, pool.QueryRow(ctx, "SELECT count(*) FROM tasks WHERE description = $1", description).Scan)
		},
		taskID: func(t *testing.T, title string) string {
			t.Helper()
			return scanOneString(t, pool.QueryRow(ctx, "SELECT id::text FROM tasks WHERE title = $1", title).Scan)
		},
		taskKind: func(t *testing.T, title string) string {
			t.Helper()
			return scanOneString(t, pool.QueryRow(ctx, "SELECT kind FROM tasks WHERE title = $1", title).Scan)
		},
		trackTitle: func(t *testing.T, title string) {
			t.Helper()
			t.Cleanup(func() {
				exec(t, "DELETE FROM tasks WHERE title = $1", title)
			})
		},
	}
}

// TestAcceptAdapter_TypeTask_CreatedShapeParity pins the contract the HTTP
// handler's response builder relies on: driven through proposal.AcceptOrchestration
// directly, BOTH real adapters return a *db.Task for a clean payload and a
// map[string]any with exactly the keys task and warnings for a payload that
// raises warnings in warn mode. A SQLite adapter that always returned *db.Task
// would keep every HTTP body correct (the HTTP warnings come from the pre-tx
// gate) and be caught only here.
func TestAcceptAdapter_TypeTask_CreatedShapeParity(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	backends := []struct {
		name string
		open func(*testing.T) taskBackend
	}{
		{"sqlite", newSQLiteTaskBackend},
		{"pg", newPGTaskBackend},
	}
	cases := []struct {
		name         string
		description  string
		wantWarnings []string // nil = clean payload, the adapter returns a bare *db.Task
	}{
		{"clean_returns_task", cleanKindTestDescription, nil},
		{"warn_mode_returns_task_and_warnings_map", taskWarnVague, taskVagueWarnings},
	}
	for _, b := range backends {
		for _, tc := range cases {
			t.Run(b.name+"/"+tc.name, func(t *testing.T) {
				be := b.open(t)
				title := be.uniqueTitle(t, "parity")
				id := be.create(t, proposal.TypeTask, taskPayloadJSON(t, title, tc.description, taskKindGeneral))

				_, created, err := proposal.AcceptOrchestration(context.Background(), id, be.newAdapter(id))
				if err != nil {
					t.Fatalf("AcceptOrchestration: %v", err)
				}
				requireCreatedShape(t, created, title, tc.wantWarnings)
			})
		}
	}
}

// requireCreatedShape fails unless created has the PG adapter's return shape:
// *db.Task when wantWarnings is nil, else map[string]any{task, warnings}.
func requireCreatedShape(t *testing.T, created any, title string, wantWarnings []string) {
	t.Helper()
	if wantWarnings == nil {
		task, ok := created.(*db.Task)
		if !ok || task == nil {
			t.Fatalf("created = %T, want a non-nil *db.Task", created)
		}
		if task.Title != title {
			t.Errorf("task.Title = %q, want %q", task.Title, title)
		}
		return
	}
	m, ok := created.(map[string]any)
	if !ok {
		t.Fatalf("created = %T, want map[string]any{task, warnings}", created)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"task", "warnings"}) {
		t.Fatalf("map keys = %v, want exactly [task warnings]", keys)
	}
	task, ok := m["task"].(*db.Task)
	if !ok || task == nil {
		t.Fatalf(`created["task"] = %T, want a non-nil *db.Task`, m["task"])
	}
	if task.Title != title {
		t.Errorf("task.Title = %q, want %q", task.Title, title)
	}
	if warnings, _ := m["warnings"].([]string); !reflect.DeepEqual(warnings, wantWarnings) {
		t.Errorf(`created["warnings"] = %#v, want %q`, m["warnings"], wantWarnings)
	}
}

func TestConfirmProposal_TypeTask_PG_MaterialisesTasksRow(t *testing.T) {
	runTaskMaterialises(t, newPGTaskBackend(t))
}

// TestConfirmProposal_TypeTask_PG_TypeError_BadRequest: {"title":123} is valid
// jsonb (Postgres rejects malformed JSON at INSERT, so a type error is the PG
// stand-in for "malformed") that the decoder refuses.
func TestConfirmProposal_TypeTask_PG_TypeError_BadRequest(t *testing.T) {
	runTaskBadPayload(t, newPGTaskBackend(t), func(string) []byte { return []byte(`{"title":123}`) }, taskDecodeFailedText)
}

func TestConfirmProposal_TypeTask_PG_WarnMode_StillCreates(t *testing.T) {
	runTaskWarnMode(t, newPGTaskBackend(t), taskWarnVague, taskKindGeneral, taskVagueWarnings)
}

func TestConfirmProposal_TypeTask_PG_InvalidKind_WarnMode(t *testing.T) {
	runTaskWarnMode(t, newPGTaskBackend(t), cleanKindTestDescription, taskBogusKind, taskBogusKindWarning)
}

func TestConfirmProposal_TypeTask_PG_StrictMode_BadRequest(t *testing.T) {
	runTaskStrictMode(t, newPGTaskBackend(t), taskWarnVague, taskKindGeneral, taskVagueWarnings)
}

func TestConfirmProposal_TypeTask_PG_InvalidKind_StrictMode(t *testing.T) {
	runTaskStrictMode(t, newPGTaskBackend(t), cleanKindTestDescription, taskBogusKind, taskBogusKindWarning)
}

func TestConfirmProposal_TypeTask_PG_StrictMode_CleanStillCreates(t *testing.T) {
	runTaskStrictCleanStillCreates(t, newPGTaskBackend(t))
}

func TestConfirmProposal_TypeTask_PG_ResolveFails_RollsBackTask(t *testing.T) {
	runTaskResolveFails(t, newPGTaskBackend(t))
}

func TestConfirmProposal_TypeTask_PG_DoubleAccept_Conflict(t *testing.T) {
	runTaskDoubleAccept(t, newPGTaskBackend(t))
}

func TestConfirmProposal_TypeTask_PG_PayloadRejected_BadRequest(t *testing.T) {
	runTaskPayloadRejected(t, newPGTaskBackend(t))
}

func TestConfirmProposal_TypeTask_PG_CapBoundaries_Accepted(t *testing.T) {
	runTaskCapBoundaries(t, newPGTaskBackend(t))
}

// TestConfirmProposal_TypeTask_BackendParity accepts the same clean TypeTask
// payload on each backend and compares the `task` objects the two responses
// carry: the same 21 keys and equal values for every field both dialects store
// identically. Ignored: id, title (unique per run), created_at, updated_at (the
// timestamp format differs per dialect) and commit_shas (null on SQLite, [] on
// PG — a pre-existing divergence outside this change). The PG subtest skips
// under -short, and then there is nothing to compare.
func TestConfirmProposal_TypeTask_BackendParity(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	backends := []struct {
		name string
		open func(*testing.T) taskBackend
	}{
		{"sqlite", newSQLiteTaskBackend},
		{"pg", newPGTaskBackend},
	}
	views := map[string]map[string]any{}
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			be := b.open(t)
			h := be.newHandler(nil)
			title := be.uniqueTitle(t, "parity-http")
			id := be.create(t, proposal.TypeTask, taskPayloadJSON(t, title, cleanKindTestDescription, taskKindGeneral))

			rec := postConfirm(h, id, acceptBody)

			requireTaskResponse(t, rec, taskWant{
				ID: be.taskID(t, title), Title: title, Description: cleanKindTestDescription, Kind: taskKindGeneral,
			})
			var resp struct {
				Task map[string]any `json:"task"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("response is not valid JSON: %v", err)
			}
			view := map[string]any{}
			for k, v := range resp.Task {
				switch k {
				case "id", "title", "created_at", "updated_at", "commit_shas":
				default:
					view[k] = v
				}
			}
			views[b.name] = view
		})
	}
	if len(views) < len(backends) {
		t.Skipf("only %d of %d backends ran (PG skips under -short): nothing to compare", len(views), len(backends))
	}
	if !reflect.DeepEqual(views["sqlite"], views["pg"]) {
		t.Errorf("task objects differ between backends:\nsqlite: %v\npg:     %v", views["sqlite"], views["pg"])
	}
}

func TestConfirmBatch_TypeTask_PG_MaterialisesTasks(t *testing.T) {
	runBatchTaskMaterialises(t, newPGTaskBackend(t))
}

func TestConfirmBatch_TypeTask_PG_MixedValidAndInvalid(t *testing.T) {
	runBatchTaskMixedValidAndInvalid(t, newPGTaskBackend(t))
}

func TestConfirmBatch_TypeTask_PG_BatchInvalidKind_WarnMode(t *testing.T) {
	runBatchTaskInvalidKindWarnMode(t, newPGTaskBackend(t))
}

func TestConfirmBatch_TypeTask_PG_BatchStrict_PerRow(t *testing.T) {
	runBatchTaskStrictPerRow(t, newPGTaskBackend(t))
}

func TestConfirmBatch_TypeTask_PG_BatchReject_NoGate(t *testing.T) {
	runBatchTaskRejectNoGate(t, newPGTaskBackend(t))
}

func TestConfirmBatch_TypeTask_PG_BatchAlreadyAccepted_Skipped(t *testing.T) {
	runBatchTaskAlreadyAccepted(t, newPGTaskBackend(t))
}

func TestConfirmBatch_TypeTask_PG_BatchResolveFails_RollsBackTask(t *testing.T) {
	runBatchTaskResolveFails(t, newPGTaskBackend(t))
}
