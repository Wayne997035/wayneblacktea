package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// Sprint 8-7 MCP vs HTTP validation-parity matrix.
//
// A gap audit found seven concepts
// (A-G) where MCP tools and HTTP handlers enforced different strength
// guarantees for the same domain concept — downstream code cannot tell which
// transport a row came through, so the two paths disagreeing means the
// weaker path's guarantee doesn't actually hold.
//
// Learned the hard way in PR #149 — three review rounds, each one only
// patching the previously-found hole because 10 hardcoded if-tests covered
// "known bugs" instead of the input space. So this file is ONE
// table-driven matrix, not seven independent hardcoded tests. Each row is a
// "concept x validation dimension" pair that drives BOTH the MCP tool and
// the HTTP handler against the SAME kind of input and asserts they agree
// with each other and with the row's declared expectation.
//
// Gap G (SQLite CreateProjectTx vs Postgres Store.CreateProject repo_name
// validation) is NOT an MCP-vs-HTTP concept — SA's own audit table marks
// both its MCP and HTTP columns "—" and the dispatch's G ruling frames it as
// a backend (SQLite vs Postgres) asymmetry in the confirm_proposal
// materialisation path. It is covered separately by
// internal/storage/sqlite/gtd_createprojecttx_repo_name_test.go (SQLite,
// fast) and internal/gtd/store_postgres_createprojecttx_repo_name_test.go
// (Postgres, testcontainers) — forcing it into this table's MCP/HTTP shape
// would misrepresent what's actually being compared.
// ---------------------------------------------------------------------------

// parityOutcome is what one side (MCP or HTTP) of a validation-parity case
// produced: whether the write was rejected, and — only when accepted — a
// value read back from the REAL store (never a value the sanitizer itself
// computed) for content-parity assertions. This is the assertion-source
// discipline PR #149 forced: asserting "sanitize(x) == sanitize(x)" is
// tautological and can't catch a broken sanitizer; asserting "what actually
// landed in the DB == this literal I computed by hand" can.
type parityOutcome struct {
	rejected bool
	stored   string
}

// parityCase is one row of the A-F table. gap/name identify the row for
// -run filtering and for the mutation-testing evidence in the sprint report
// (e.g. `-run TestValidationParity_MCPvsHTTP/A_`).
type parityCase struct {
	gap  string
	name string
	mcp  func(t *testing.T, env *parityEnv) parityOutcome
	http func(t *testing.T, env *parityEnv) parityOutcome

	wantRejected bool
	// checkStored gates the wantStored comparison. It is a SEPARATE bool
	// (not "wantStored != \"\"") on purpose: an empty string is a legitimate
	// expected value (gap B's "all-disallowed-chars tag gets dropped
	// entirely" case expects the stored CSV to be ""), and conflating
	// "empty expected value" with "don't check" silently skips exactly the
	// case most likely to regress.
	checkStored bool
	wantStored  string
}

// parityEnv bundles a fresh SQLite-backed *Server (MCP) and the equivalent
// HTTP handlers, wired to the SAME storage.ServerStores instance so that a
// "read back" after either path reads a genuinely-persisted row, and so a
// project/task/goal created via one path is visible to the other (used by
// gap D, which updates a project created via direct store fixture setup).
type parityEnv struct {
	ctx    context.Context
	stores storage.ServerStores
	srv    *Server
	e      *echo.Echo
}

func newParityEnv(t *testing.T) *parityEnv {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "validation-parity.db")
	stores, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
		Backend:    storage.BackendSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("NewServerStores: %v", err)
	}
	t.Cleanup(func() { _ = stores.Close() })

	srv, err := New(stores)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	// Registers every tool (including deriving+caching each toolSpec via
	// addTool/registerToolSpec — toolspec.go) exactly as production init
	// (cmd/mcp/main.go) does. Required before seam-wrapped tools
	// (update_project, create_goal, add_task) can be invoked in this test.
	srv.MCPServer()

	e := echo.New()
	kh := handler.NewKnowledgeHandler(stores.Knowledge(), nil)
	lh := handler.NewLearningHandler(stores.Learning())
	gh := handler.NewGTDHandler(stores.GTD())
	e.POST("/api/knowledge", kh.AddKnowledge)
	e.POST("/api/learning/concepts", lh.CreateConcept)
	e.POST("/api/goals", gh.CreateGoal)
	e.PATCH("/api/projects/:id", gh.UpdateProject)
	e.POST("/api/tasks", gh.CreateTask)

	return &parityEnv{ctx: context.Background(), stores: stores, srv: srv, e: e}
}

// doJSON performs an HTTP request against env's router with a JSON-marshaled
// body and returns the recorder.
func (env *parityEnv) doJSON(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal request body: %v", err)
	}
	req := httptest.NewRequestWithContext(env.ctx, method, path, strings.NewReader(string(raw)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	return rec
}

// mcpReq builds a mcp.CallToolRequest from a raw arguments map.
func mcpReq(args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

// idOnly extracts just the "id" field from a JSON payload — safe against
// pgtype.Text/pgtype.Timestamptz fields elsewhere in the struct that would
// otherwise need bespoke JSON-unmarshal handling.
func idOnly(t *testing.T, raw string) uuid.UUID {
	t.Helper()
	var v struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("unmarshal id from %q: %v", raw, err)
	}
	return v.ID
}

func TestValidationParity_MCPvsHTTP(t *testing.T) {
	for _, tc := range validationParityCases() {
		t.Run(tc.gap+"_"+tc.name, func(t *testing.T) {
			env := newParityEnv(t)

			mcpOut := tc.mcp(t, env)
			httpOut := tc.http(t, env)

			if mcpOut.rejected != tc.wantRejected {
				t.Errorf("MCP rejected=%v, want %v", mcpOut.rejected, tc.wantRejected)
			}
			if httpOut.rejected != tc.wantRejected {
				t.Errorf("HTTP rejected=%v, want %v", httpOut.rejected, tc.wantRejected)
			}
			if mcpOut.rejected != httpOut.rejected {
				t.Fatalf("MCP/HTTP DISAGREE on rejection (mcp=%v http=%v) — exactly the gap this matrix exists to catch",
					mcpOut.rejected, httpOut.rejected)
			}
			if !tc.wantRejected && tc.checkStored {
				if mcpOut.stored != tc.wantStored {
					t.Errorf("MCP stored = %q, want %q", mcpOut.stored, tc.wantStored)
				}
				if httpOut.stored != tc.wantStored {
					t.Errorf("HTTP stored = %q, want %q", httpOut.stored, tc.wantStored)
				}
			}
		})
	}
}

// validationParityCases assembles the full A-F table. Adding a new concept
// means adding a row here — the loop in TestValidationParity_MCPvsHTTP never
// changes.
func validationParityCases() []parityCase {
	var cases []parityCase
	cases = append(cases, knowledgeControlCharCases()...)
	cases = append(cases, knowledgeTagCases()...)
	cases = append(cases, learningConceptTagCases()...)
	cases = append(cases, updateProjectLengthCases()...)
	cases = append(cases, branchNameRuneCases()...)
	cases = append(cases, goalAreaTrimCases()...)
	return cases
}

// ---------------------------------------------------------------------------
// Gap A — knowledge title/content control characters.
// ---------------------------------------------------------------------------

func knowledgeControlCharCases() []parityCase {
	return []parityCase{
		knowledgeControlCharCase("title_bell_char_rejected", "bad\x07title", "clean content", true),
		knowledgeControlCharCase("title_embedded_newline_rejected", "bad\ntitle", "clean content", true),
		knowledgeControlCharCase("content_nul_byte_rejected", "clean title", "bad\x00content", true),
		knowledgeControlCharCase("clean_input_accepted_title_roundtrips", "a perfectly clean title", "clean content", false),
	}
}

func knowledgeControlCharCase(name, title, content string, wantRejected bool) parityCase {
	return parityCase{
		gap:  "A",
		name: name,
		mcp: func(t *testing.T, env *parityEnv) parityOutcome {
			t.Helper()
			args := map[string]any{"type": "til", "title": title, "content": content}
			r, err := env.srv.handleAddKnowledge(env.ctx, mcpReq(args))
			if err != nil {
				t.Fatalf("handleAddKnowledge Go error: %v", err)
			}
			if r.IsError {
				return parityOutcome{rejected: true}
			}
			id := idOnly(t, extractField(t, resultText(r), "item"))
			got, gErr := env.stores.Knowledge().GetByID(env.ctx, id)
			if gErr != nil {
				t.Fatalf("GetByID after MCP add: %v", gErr)
			}
			return parityOutcome{stored: got.Title}
		},
		http: func(t *testing.T, env *parityEnv) parityOutcome {
			t.Helper()
			rec := env.doJSON(t, http.MethodPost, "/api/knowledge", map[string]any{
				"type": "til", "title": title, "content": content,
			})
			if rec.Code != http.StatusCreated {
				return parityOutcome{rejected: true}
			}
			id := idOnly(t, rec.Body.String())
			got, gErr := env.stores.Knowledge().GetByID(env.ctx, id)
			if gErr != nil {
				t.Fatalf("GetByID after HTTP add: %v", gErr)
			}
			return parityOutcome{stored: got.Title}
		},
		wantRejected: wantRejected,
		checkStored:  true,
		wantStored:   title,
	}
}

// extractField pulls a named raw-JSON sub-object out of an MCP tool result's
// text body — used for add_knowledge's {"item": {...}, ...} wrapper shape.
func extractField(t *testing.T, raw, field string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal MCP result envelope from %q: %v", raw, err)
	}
	sub, ok := m[field]
	if !ok {
		t.Fatalf("MCP result envelope missing field %q: %s", field, raw)
	}
	return string(sub)
}

// ---------------------------------------------------------------------------
// Gap B — knowledge tags character whitelist.
// ---------------------------------------------------------------------------

func knowledgeTagCases() []parityCase {
	return []parityCase{
		knowledgeTagCase("script_tag_sanitized_to_alnum", []string{"<script>alert(1)</script>"}, "scriptalert1/script"),
		knowledgeTagCase("all_disallowed_chars_tag_dropped", []string{"!!!"}, ""),
		knowledgeTagCase("clean_tag_passes_through_unchanged", []string{"go-lang_2026"}, "go-lang_2026"),
	}
}

func knowledgeTagCase(name string, rawTags []string, wantTagsCSV string) parityCase {
	return parityCase{
		gap:  "B",
		name: name,
		mcp: func(t *testing.T, env *parityEnv) parityOutcome {
			t.Helper()
			args := map[string]any{
				"type": "til", "title": "b-mcp-" + name, "tags": strings.Join(rawTags, ","),
			}
			r, err := env.srv.handleAddKnowledge(env.ctx, mcpReq(args))
			if err != nil {
				t.Fatalf("handleAddKnowledge Go error: %v", err)
			}
			if r.IsError {
				return parityOutcome{rejected: true}
			}
			id := idOnly(t, extractField(t, resultText(r), "item"))
			got, gErr := env.stores.Knowledge().GetByID(env.ctx, id)
			if gErr != nil {
				t.Fatalf("GetByID after MCP add: %v", gErr)
			}
			return parityOutcome{stored: strings.Join(got.Tags, ",")}
		},
		http: func(t *testing.T, env *parityEnv) parityOutcome {
			t.Helper()
			rec := env.doJSON(t, http.MethodPost, "/api/knowledge", map[string]any{
				"type": "til", "title": "b-http-" + name, "tags": rawTags,
			})
			if rec.Code != http.StatusCreated {
				return parityOutcome{rejected: true}
			}
			id := idOnly(t, rec.Body.String())
			got, gErr := env.stores.Knowledge().GetByID(env.ctx, id)
			if gErr != nil {
				t.Fatalf("GetByID after HTTP add: %v", gErr)
			}
			return parityOutcome{stored: strings.Join(got.Tags, ",")}
		},
		wantRejected: false,
		checkStored:  true,
		wantStored:   wantTagsCSV,
	}
}

// ---------------------------------------------------------------------------
// Gap C — learning concept tags character whitelist.
// ---------------------------------------------------------------------------

func learningConceptTagCases() []parityCase {
	return []parityCase{
		learningConceptTagCase("script_tag_sanitized_to_alnum", []string{"<script>x</script>"}, "scriptx/script"),
		learningConceptTagCase("clean_tag_passes_through_unchanged", []string{"fsrs-algo"}, "fsrs-algo"),
	}
}

func learningConceptTagCase(name string, rawTags []string, wantTagsCSV string) parityCase {
	return parityCase{
		gap:  "C",
		name: name,
		mcp: func(t *testing.T, env *parityEnv) parityOutcome {
			t.Helper()
			args := map[string]any{
				"title": "c-mcp-" + name, "content": "concept body", "tags": strings.Join(rawTags, ","),
			}
			r, err := env.srv.handleCreateConcept(env.ctx, mcpReq(args))
			if err != nil {
				t.Fatalf("handleCreateConcept Go error: %v", err)
			}
			if r.IsError {
				return parityOutcome{rejected: true}
			}
			var concept db.Concept
			if uErr := json.Unmarshal([]byte(resultText(r)), &concept); uErr != nil {
				t.Fatalf("unmarshal concept: %v", uErr)
			}
			return parityOutcome{stored: strings.Join(concept.Tags, ",")}
		},
		http: func(t *testing.T, env *parityEnv) parityOutcome {
			t.Helper()
			rec := env.doJSON(t, http.MethodPost, "/api/learning/concepts", map[string]any{
				"title": "c-http-" + name, "content": "concept body", "tags": rawTags,
			})
			if rec.Code != http.StatusCreated {
				return parityOutcome{rejected: true}
			}
			var concept db.Concept
			if uErr := json.Unmarshal(rec.Body.Bytes(), &concept); uErr != nil {
				t.Fatalf("unmarshal concept: %v", uErr)
			}
			return parityOutcome{stored: strings.Join(concept.Tags, ",")}
		},
		wantRejected: false,
		checkStored:  true,
		wantStored:   wantTagsCSV,
	}
}

// ---------------------------------------------------------------------------
// Gap D — update_project title/description rune-length caps.
// ---------------------------------------------------------------------------

func updateProjectLengthCases() []parityCase {
	title500 := strings.Repeat("a", 500)
	return []parityCase{
		{
			gap: "D", name: "title_501_runes_rejected",
			mcp:          updateProjectCall(strings.Repeat("a", 501), ""),
			http:         updateProjectHTTPCall(strings.Repeat("a", 501), ""),
			wantRejected: true,
		},
		{
			gap: "D", name: "title_exactly_500_runes_accepted",
			mcp:          updateProjectCall(title500, ""),
			http:         updateProjectHTTPCall(title500, ""),
			wantRejected: false,
			checkStored:  true,
			wantStored:   title500,
		},
		{
			gap: "D", name: "description_5001_runes_rejected",
			mcp:          updateProjectCall("ok title", strings.Repeat("b", 5001)),
			http:         updateProjectHTTPCall("ok title", strings.Repeat("b", 5001)),
			wantRejected: true,
		},
	}
}

// fixtureProject creates a project directly via the store (bypassing both
// MCP and HTTP — pure test fixture setup, not part of what's under test) and
// returns its ID.
func fixtureProject(t *testing.T, env *parityEnv, namePrefix string) uuid.UUID {
	t.Helper()
	p, err := env.stores.GTD().CreateProject(env.ctx, gtd.CreateProjectParams{
		Name: namePrefix + "-" + uuid.NewString()[:8], Title: "fixture", Area: "engineering",
	})
	if err != nil {
		t.Fatalf("fixture CreateProject: %v", err)
	}
	return p.ID
}

func updateProjectCall(title, description string) func(t *testing.T, env *parityEnv) parityOutcome {
	return func(t *testing.T, env *parityEnv) parityOutcome {
		t.Helper()
		id := fixtureProject(t, env, "d-mcp")
		args := map[string]any{"project_id": id.String(), "title": title}
		if description != "" {
			args["description"] = description
		}
		r := callTool(t, "update_project", args, env.srv.handleUpdateProject)
		if r.IsError {
			return parityOutcome{rejected: true}
		}
		var proj db.Project
		if err := json.Unmarshal([]byte(resultText(r)), &proj); err != nil {
			t.Fatalf("unmarshal project: %v", err)
		}
		return parityOutcome{stored: proj.Title}
	}
}

func updateProjectHTTPCall(title, description string) func(t *testing.T, env *parityEnv) parityOutcome {
	return func(t *testing.T, env *parityEnv) parityOutcome {
		t.Helper()
		id := fixtureProject(t, env, "d-http")
		body := map[string]any{"title": title}
		if description != "" {
			body["description"] = description
		}
		rec := env.doJSON(t, http.MethodPatch, "/api/projects/"+id.String(), body)
		if rec.Code != http.StatusOK {
			return parityOutcome{rejected: true}
		}
		var proj db.Project
		if err := json.Unmarshal(rec.Body.Bytes(), &proj); err != nil {
			t.Fatalf("unmarshal project: %v", err)
		}
		return parityOutcome{stored: proj.Title}
	}
}

// ---------------------------------------------------------------------------
// Gap E — branch_name rune-vs-byte length cap unification.
// ---------------------------------------------------------------------------

func branchNameRuneCases() []parityCase {
	cjk255 := strings.Repeat("漢", 255) // 255 runes, 765 bytes: within the 255-RUNE cap.
	ascii256 := strings.Repeat("a", 256)
	return []parityCase{
		{
			gap: "E", name: "255_cjk_runes_765_bytes_accepted",
			mcp:          branchNameMCPCall(cjk255),
			http:         branchNameHTTPCall(cjk255),
			wantRejected: false,
			checkStored:  true,
			wantStored:   cjk255,
		},
		{
			gap: "E", name: "256_ascii_runes_rejected",
			mcp:          branchNameMCPCall(ascii256),
			http:         branchNameHTTPCall(ascii256),
			wantRejected: true,
		},
	}
}

func branchNameMCPCall(branchName string) func(t *testing.T, env *parityEnv) parityOutcome {
	return func(t *testing.T, env *parityEnv) parityOutcome {
		t.Helper()
		args := map[string]any{
			"title": "e-mcp-branch-name", "branch_name": branchName,
			"due_date": "2026-12-31T00:00:00Z", // add_task requires due_date (OVERRIDE 1) — see dispatch §4.
			// kind=chore skips CheckVagueness (short-description warning),
			// which would otherwise wrap the result as {"task":...,
			// "warnings":...} instead of the bare task JSON idOnly expects.
			"kind": "chore",
		}
		r := callTool(t, "add_task", args, env.srv.handleAddTask)
		if r.IsError {
			return parityOutcome{rejected: true}
		}
		id := idOnly(t, resultText(r))
		got, err := env.stores.GTD().GetTaskByID(env.ctx, id)
		if err != nil {
			t.Fatalf("GetTaskByID after MCP add_task: %v", err)
		}
		if !got.BranchName.Valid {
			t.Fatalf("branch_name not persisted")
		}
		return parityOutcome{stored: got.BranchName.String}
	}
}

func branchNameHTTPCall(branchName string) func(t *testing.T, env *parityEnv) parityOutcome {
	return func(t *testing.T, env *parityEnv) parityOutcome {
		t.Helper()
		rec := env.doJSON(t, http.MethodPost, "/api/tasks", map[string]any{
			"title": "e-http-branch-name", "branch_name": branchName,
		})
		if rec.Code != http.StatusCreated {
			return parityOutcome{rejected: true}
		}
		id := idOnly(t, rec.Body.String())
		got, err := env.stores.GTD().GetTaskByID(env.ctx, id)
		if err != nil {
			t.Fatalf("GetTaskByID after HTTP add: %v", err)
		}
		if !got.BranchName.Valid {
			t.Fatalf("branch_name not persisted")
		}
		return parityOutcome{stored: got.BranchName.String}
	}
}

// ---------------------------------------------------------------------------
// Gap F — goal area whitespace-only value treated as empty (trim-before-check).
// ---------------------------------------------------------------------------

func goalAreaTrimCases() []parityCase {
	return []parityCase{
		{
			gap: "F", name: "whitespace_only_area_rejected",
			mcp:          goalAreaMCPCall("   "),
			http:         goalAreaHTTPCall("   "),
			wantRejected: true,
		},
		{
			gap: "F", name: "clean_area_accepted_and_roundtrips",
			mcp:          goalAreaMCPCall("career"),
			http:         goalAreaHTTPCall("career"),
			wantRejected: false,
			checkStored:  true,
			wantStored:   "career",
		},
	}
}

func goalAreaMCPCall(area string) func(t *testing.T, env *parityEnv) parityOutcome {
	return func(t *testing.T, env *parityEnv) parityOutcome {
		t.Helper()
		args := map[string]any{"title": "f-mcp-goal-" + uuid.NewString()[:8], "area": area}
		r := callTool(t, "create_goal", args, env.srv.handleCreateGoal)
		if r.IsError {
			return parityOutcome{rejected: true}
		}
		var goal db.Goal
		if err := json.Unmarshal([]byte(resultText(r)), &goal); err != nil {
			t.Fatalf("unmarshal goal: %v", err)
		}
		return parityOutcome{stored: goal.Area.String}
	}
}

func goalAreaHTTPCall(area string) func(t *testing.T, env *parityEnv) parityOutcome {
	return func(t *testing.T, env *parityEnv) parityOutcome {
		t.Helper()
		rec := env.doJSON(t, http.MethodPost, "/api/goals", map[string]any{
			"title": "f-http-goal-" + uuid.NewString()[:8], "area": area,
		})
		if rec.Code != http.StatusCreated {
			return parityOutcome{rejected: true}
		}
		var goal db.Goal
		if err := json.Unmarshal(rec.Body.Bytes(), &goal); err != nil {
			t.Fatalf("unmarshal goal: %v", err)
		}
		return parityOutcome{stored: goal.Area.String}
	}
}
