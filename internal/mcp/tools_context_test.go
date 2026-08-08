package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wayne997035/wayneblacktea/internal/arch"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// pulledForwardKey is the JSON key of the pulled_forward field, named once so
// the raw-text assertions below cannot drift apart from each other.
const pulledForwardKey = "pulled_forward"

// todayContextPayloadBudgetRunes is the hard ceiling on the get_today_context
// response, in runes. get_today_context is called once at the start of every
// session by every client, so its payload is unconditional prompt overhead:
// before this budget existed the response was ~26.4k runes, most of it the
// embedded arch snapshot plus untruncated descriptions.
const todayContextPayloadBudgetRunes = 6000

// ---------------------------------------------------------------------------
// fakes
// ---------------------------------------------------------------------------

// todayContextTestGTDStore embeds noopGTDStore (tools_contextpack_fakes_test.go)
// and overrides exactly the four lookups handleGetTodayContext performs, so a
// test can hand each of them a fixture, an error, or an artificial latency
// (the latter backs the concurrency proof).
type todayContextTestGTDStore struct {
	noopGTDStore
	goals       []db.Goal
	goalsErr    error
	projects    []db.Project
	projectsErr error
	completed   int64
	total       int64
	progressErr error
	tasks       []db.Task
	tasksErr    error
	delay       time.Duration
}

func (s todayContextTestGTDStore) ActiveGoals(context.Context) ([]db.Goal, error) {
	time.Sleep(s.delay)
	return s.goals, s.goalsErr
}

func (s todayContextTestGTDStore) ListActiveProjects(context.Context) ([]db.Project, error) {
	time.Sleep(s.delay)
	return s.projects, s.projectsErr
}

func (s todayContextTestGTDStore) WeeklyProgress(context.Context) (int64, int64, error) {
	time.Sleep(s.delay)
	return s.completed, s.total, s.progressErr
}

func (s todayContextTestGTDStore) PullForwardTasks(context.Context, time.Time) ([]db.Task, error) {
	time.Sleep(s.delay)
	return s.tasks, s.tasksErr
}

// todayContextTestSessionStore overrides LatestHandoff only; a nil handoff with
// no error reproduces the real "no pending handoff" contract (ErrNotFound),
// which handleGetTodayContext must treat as success.
type todayContextTestSessionStore struct {
	noopSessionStore
	handoff *db.SessionHandoff
	err     error
	delay   time.Duration
}

func (s todayContextTestSessionStore) LatestHandoff(context.Context) (*db.SessionHandoff, error) {
	time.Sleep(s.delay)
	if s.err != nil {
		return nil, s.err
	}
	if s.handoff == nil {
		return nil, session.ErrNotFound
	}
	return s.handoff, nil
}

// todayContextTestSnapshotStore is a snapshot.StoreIface fake for the
// best-effort latest_status_snapshot lookup.
type todayContextTestSnapshotStore struct {
	snap  *snapshot.Snapshot
	err   error
	delay time.Duration
}

var _ snapshot.StoreIface = todayContextTestSnapshotStore{}

func (s todayContextTestSnapshotStore) Write(
	context.Context, snapshot.WriteParams,
) (*snapshot.Snapshot, error) {
	return nil, nil
}

func (s todayContextTestSnapshotStore) LatestFresh(
	context.Context, string, time.Duration,
) (*snapshot.Snapshot, error) {
	time.Sleep(s.delay)
	if s.err != nil {
		return nil, s.err
	}
	if s.snap == nil {
		return nil, snapshot.ErrNotFound
	}
	return s.snap, nil
}

func (s todayContextTestSnapshotStore) LatestSlugs(context.Context) ([]string, error) {
	return nil, nil
}

func (s todayContextTestSnapshotStore) PruneOlderThan(context.Context, time.Time) (int64, error) {
	return 0, nil
}

// countingArchStore records how many times GetSnapshot is called. Asserting
// that arch_snapshot_data is absent from the JSON would still pass if the
// query were issued and its result merely dropped — the whole point of the
// change is deleting the round trip, so the call count is what gets asserted.
type countingArchStore struct {
	getCalls atomic.Int64
}

var _ arch.StoreIface = (*countingArchStore)(nil)

func (s *countingArchStore) UpsertSnapshot(context.Context, arch.UpsertParams) (*arch.Snapshot, error) {
	return nil, nil
}

func (s *countingArchStore) GetSnapshot(context.Context, string) (*arch.Snapshot, error) {
	s.getCalls.Add(1)
	return nil, arch.ErrNotFound
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// getTodayContextText calls handleGetTodayContext and returns the raw JSON
// text of a successful response, failing the test on any error or non-text
// result shape.
func getTodayContextText(t *testing.T, s *Server) string {
	t.Helper()
	result, err := s.handleGetTodayContext(context.Background(), mcpmsg.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleGetTodayContext: %v", err)
	}
	if result.IsError {
		t.Fatalf("handleGetTodayContext returned a tool error result: %+v", result.Content)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected at least one content item")
	}
	textContent, ok := result.Content[0].(mcpmsg.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	return textContent.Text
}

func pgText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

func pgTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// cjk builds a Chinese filler string of exactly n runes. Production text in
// this workspace is predominantly Chinese, so the fixtures below use it: it
// keeps the rune budget honest while making the rune/byte gap visible.
func cjk(n int) string {
	const unit = "這是一段用來模擬正式環境長度的中文敘述文字。"
	repeated := strings.Repeat(unit, n/utf8.RuneCountInString(unit)+1)
	return truncateRunes(repeated, n)
}

// mustJSON marshals v or fails the test.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return out
}

// prodShapedServer builds a Server whose stores return a fixture modelled on
// the real production dataset, measured read-only against the Aiven Postgres
// on 2026-08-08. Postgres length() counts characters, so every number below is
// a rune count:
//
//	active goals    : 2   title ~26,  description NULL (avg 0 / max 0)
//	active projects : 2   title ~15,  description avg 1095 / max 1246
//	pulled forward  : 5   (= gtd.PullForwardCap) title ~82,
//	                      description avg 1437 / max 3723
//	pending handoff : 1   intent ~199, context_summary ~2613, 5 next_actions
//	                      (title ≤88, expected ≤70, command always empty,
//	                      ref_task_id present on 2 of the 5)
//	status snapshot : 1   sprint_summary far above its cap
//
// Every field is seeded at the largest value observed for its column, so this
// is the worst case for today's dataset rather than a lucky sample. It is a
// snapshot of the data SHAPE, not of live data: if the workspace grows (a
// third active project, a sixth next action) update this fixture and re-check
// the budget — that is exactly the signal this test exists to give.
//
// Fields that are already far above their cap in production are NOT inflated
// further here: clipping makes the source length irrelevant to the payload
// size, and inflating them would make the budget look unreachable for reasons
// that never occur. oversizedTextServer covers the "much longer than the cap"
// direction explicitly.
func prodShapedServer(t *testing.T) *Server {
	t.Helper()

	goals := make([]db.Goal, 2)
	for i := range goals {
		goals[i] = db.Goal{
			ID:      uuid.New(),
			Title:   cjk(26),
			Status:  "active",
			Area:    pgText("work"),
			DueDate: pgTime(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)),
			// Measured: both active goals have a NULL/empty description.
			Description: pgtype.Text{},
		}
	}

	projects := make([]db.Project, 2)
	for i := range projects {
		projects[i] = db.Project{
			ID:          uuid.New(),
			GoalID:      pgUUID(goals[0].ID),
			Name:        "wayneblacktea",
			Title:       cjk(15),
			Status:      "active",
			Area:        "work",
			Priority:    1,
			RepoName:    pgText("wayneblacktea"),
			Description: pgText(cjk(1246)),
		}
	}

	tasks := make([]db.Task, gtd.PullForwardCap)
	for i := range tasks {
		tasks[i] = db.Task{
			ID:          uuid.New(),
			ProjectID:   pgUUID(projects[0].ID),
			Title:       cjk(82),
			Status:      "pending",
			Priority:    1,
			Importance:  pgtype.Int2{Int16: 1, Valid: true},
			DueDate:     pgTime(time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)),
			Kind:        "task",
			Description: pgText(cjk(3723)),
			// The largest column on the row, and deliberately not projected.
			Context: pgText(cjk(4000)),
		}
	}

	actions := make([]session.NextAction, 5)
	for i := range actions {
		ref := uuid.New().String()
		actions[i] = session.NextAction{
			Step:      i + 1,
			Title:     cjk(88),
			Expected:  cjk(70),
			Status:    session.NextActionPending,
			RefTaskID: &ref,
		}
	}

	handoff := &db.SessionHandoff{
		ID:             uuid.New(),
		RepoName:       pgText("wayneblacktea"),
		Intent:         cjk(199),
		ContextSummary: pgText(cjk(2613)),
		CreatedAt:      pgTime(time.Date(2026, 8, 7, 22, 0, 0, 0, time.UTC)),
		NextActions:    mustJSON(t, actions),
	}

	return &Server{
		gtd: todayContextTestGTDStore{
			goals:     goals,
			projects:  projects,
			completed: 12,
			total:     30,
			tasks:     tasks,
		},
		session: todayContextTestSessionStore{handoff: handoff},
		snapshotStore: todayContextTestSnapshotStore{snap: &snapshot.Snapshot{
			Slug:           primaryProjectSlug,
			GeneratedAt:    time.Date(2026, 8, 8, 3, 0, 0, 0, time.UTC),
			SprintSummary:  cjk(1500),
			SotaCatchupPct: 72,
		}},
	}
}

// oversizedTextServer seeds every clippable field far beyond its cap (and more
// next_actions than the cap allows), so the clipping assertions exercise the
// truncation path on every field rather than relying on production happening
// to be verbose today.
func oversizedTextServer(t *testing.T) *Server {
	t.Helper()

	// over returns text three times the given cap, so clipping always fires.
	over := func(maxRunes int) string { return cjk(maxRunes * 3) }

	goals := []db.Goal{{
		ID:          uuid.New(),
		Title:       cjk(26),
		Status:      "active",
		Area:        pgText("work"),
		DueDate:     pgTime(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)),
		Description: pgText(over(goalDescMaxRunes)),
	}}

	projects := []db.Project{{
		ID:          uuid.New(),
		GoalID:      pgUUID(goals[0].ID),
		Name:        "wayneblacktea",
		Title:       cjk(15),
		Status:      "active",
		Area:        "work",
		Priority:    1,
		RepoName:    pgText("wayneblacktea"),
		Description: pgText(over(projectDescMaxRunes)),
	}}

	tasks := []db.Task{{
		ID:          uuid.New(),
		ProjectID:   pgUUID(projects[0].ID),
		Title:       cjk(82),
		Status:      "pending",
		Priority:    1,
		Importance:  pgtype.Int2{Int16: 1, Valid: true},
		DueDate:     pgTime(time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)),
		Kind:        "task",
		Description: pgText(over(pulledForwardDescMaxRunes)),
		Context:     pgText(cjk(4000)),
	}}

	actions := make([]session.NextAction, maxHandoffNextActions+5)
	for i := range actions {
		ref := uuid.New().String()
		actions[i] = session.NextAction{
			Step:      i + 1,
			Title:     over(nextActionFieldMaxRunes),
			Command:   strings.Repeat("x", nextActionFieldMaxRunes*3),
			Expected:  over(nextActionFieldMaxRunes),
			Status:    session.NextActionPending,
			RefTaskID: &ref,
		}
	}

	handoff := &db.SessionHandoff{
		ID:             uuid.New(),
		RepoName:       pgText("wayneblacktea"),
		Intent:         cjk(handoffIntentMaxRunes - 1), // deliberately UNDER its cap
		ContextSummary: pgText(over(handoffSummaryMaxRunes)),
		CreatedAt:      pgTime(time.Date(2026, 8, 7, 22, 0, 0, 0, time.UTC)),
		NextActions:    mustJSON(t, actions),
	}

	return &Server{
		gtd: todayContextTestGTDStore{
			goals: goals, projects: projects, tasks: tasks,
			completed: 12, total: 30,
		},
		session: todayContextTestSessionStore{handoff: handoff},
		snapshotStore: todayContextTestSnapshotStore{snap: &snapshot.Snapshot{
			Slug:           primaryProjectSlug,
			GeneratedAt:    time.Date(2026, 8, 8, 3, 0, 0, 0, time.UTC),
			SprintSummary:  over(sprintSummaryMaxRunes),
			SotaCatchupPct: 72,
		}},
	}
}

// ---------------------------------------------------------------------------
// payload budget
// ---------------------------------------------------------------------------

// topLevelSizes returns the rune size of each top-level field of a
// get_today_context response, so a budget failure names the section to tighten
// instead of just a total.
func topLevelSizes(t *testing.T, raw string) map[string]int {
	t.Helper()
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, raw)
	}
	sizes := make(map[string]int, len(parsed))
	for k, v := range parsed {
		sizes[k] = utf8.RuneCountInString(string(v))
	}
	return sizes
}

// TestHandleGetTodayContext_PayloadBudget pins the session-start payload size
// against a production-shaped fixture. It always logs the measured rune and
// byte counts plus a per-section breakdown, so a failure immediately shows how
// far over budget the response is and which cap in tools_context.go to retune.
func TestHandleGetTodayContext_PayloadBudget(t *testing.T) {
	raw := getTodayContextText(t, prodShapedServer(t))

	runes := utf8.RuneCountInString(raw)
	byteLen := len(raw)
	t.Logf("get_today_context payload: %d runes / %d bytes (budget %d runes)",
		runes, byteLen, todayContextPayloadBudgetRunes)
	sizes := topLevelSizes(t, raw)
	for _, k := range []string{
		"goals", "projects", "weekly_progress", "pending_handoff",
		"latest_status_snapshot", pulledForwardKey,
	} {
		t.Logf("  %-24s %5d runes", k, sizes[k])
	}

	// This tool serialises compact on purpose (see compactJSONText). Logging
	// what the shared jsonText helper's two-space indentation would have cost
	// keeps that trade-off visible to whoever retunes the budget next, and
	// makes an accidental revert to jsonText obvious in the failure output.
	var indented bytes.Buffer
	if err := json.Indent(&indented, []byte(raw), "", "  "); err != nil {
		t.Fatalf("indent response: %v", err)
	}
	indentedRunes := utf8.RuneCountInString(indented.String())
	t.Logf("  %-24s %5d runes (jsonText's indentation would add %d)",
		"if pretty-printed", indentedRunes, indentedRunes-runes)

	if runes > todayContextPayloadBudgetRunes {
		t.Errorf("payload is %d runes, budget is %d — tighten the caps in tools_context.go",
			runes, todayContextPayloadBudgetRunes)
	}
	// A payload that collapsed to nothing would also "pass" a ceiling check.
	if runes < 500 {
		t.Errorf("payload is only %d runes — the fixture or the projection is broken", runes)
	}
}

// TestHandleGetTodayContext_NoArchRoundTrip proves the arch snapshot lookup is
// gone, not merely hidden: the spy store counts calls, and the response must
// carry no arch field at all.
func TestHandleGetTodayContext_NoArchRoundTrip(t *testing.T) {
	spy := &countingArchStore{}
	s := prodShapedServer(t)
	s.arch = spy

	raw := getTodayContextText(t, s)

	if got := spy.getCalls.Load(); got != 0 {
		t.Errorf("arch.GetSnapshot called %d times, want 0 — the round trip is still there", got)
	}
	for _, forbidden := range []string{"arch_snapshot_data", "PROJECT ARCH"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("response still contains %q", forbidden)
		}
	}
}

// ---------------------------------------------------------------------------
// field projection
// ---------------------------------------------------------------------------

// TestHandleGetTodayContext_PulledForwardFieldSet pins the exact field set of
// a pulled_forward entry. The `context` column in particular is the largest
// text column on tasks and must never be echoed here — get_task(task_id) is
// the documented way to read it.
func TestHandleGetTodayContext_PulledForwardFieldSet(t *testing.T) {
	raw := getTodayContextText(t, prodShapedServer(t))

	var parsed struct {
		PulledForward []map[string]json.RawMessage `json:"pulled_forward"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, raw)
	}
	if len(parsed.PulledForward) == 0 {
		t.Fatal("fixture produced no pulled_forward entries")
	}

	want := map[string]bool{
		"id": true, "project_id": true, "title": true, "status": true,
		"priority": true, "importance": true, "due_date": true, "kind": true,
		"description": true,
	}
	got := parsed.PulledForward[0]
	for k := range got {
		if !want[k] {
			t.Errorf("pulled_forward entry carries unexpected field %q", k)
		}
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("pulled_forward entry missing field %q", k)
		}
	}
}

// TestHandleGetTodayContext_PulledForwardAlwaysPresent verifies the
// pulled_forward field is always a JSON array (never null/omitted) in
// get_today_context, both when the store returns tasks and when it returns
// an empty/nil slice — the wire-contract guarantee callers rely on so they
// never need a presence check. The nil case asserts against the RAW TEXT,
// because a decoded []T cannot distinguish `[]` from `null`.
func TestHandleGetTodayContext_PulledForwardAlwaysPresent(t *testing.T) {
	tests := []struct {
		name  string
		tasks []db.Task
		want  int
	}{
		{
			name:  "store returns nil → pulled_forward is [] not null",
			tasks: nil,
			want:  0,
		},
		{
			name: "store returns tasks → pulled_forward carries them through",
			tasks: []db.Task{
				{ID: uuid.New(), Title: "important task", Status: "pending"},
			},
			want: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{
				gtd:     todayContextTestGTDStore{tasks: tc.tasks},
				session: todayContextTestSessionStore{},
			}

			raw := getTodayContextText(t, s)

			if strings.Contains(raw, `"`+pulledForwardKey+`":null`) {
				t.Fatalf("%s must never be null, got: %s", pulledForwardKey, raw)
			}
			if tc.want == 0 && !strings.Contains(raw, `"`+pulledForwardKey+`":[]`) {
				t.Fatalf("empty %s must serialise as [], got: %s", pulledForwardKey, raw)
			}

			var parsed map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
				t.Fatalf("unmarshal response: %v\nraw: %s", err, raw)
			}
			pfRaw, ok := parsed[pulledForwardKey]
			if !ok {
				t.Fatalf("%s key missing from response: %s", pulledForwardKey, raw)
			}
			if string(pfRaw) == nullStr {
				t.Fatalf("%s must never be null, got: %s", pulledForwardKey, pfRaw)
			}

			var pf []pulledForwardTask
			if err := json.Unmarshal(pfRaw, &pf); err != nil {
				t.Fatalf("%s is not a JSON array: %s (%v)", pulledForwardKey, pfRaw, err)
			}
			if len(pf) != tc.want {
				t.Errorf("%s length = %d, want %d", pulledForwardKey, len(pf), tc.want)
			}
		})
	}
}

// TestHandleGetTodayContext_HandoffNextActionsCapped verifies that a handoff
// with more next_actions than the cap is trimmed, that the untrimmed length is
// still reported, and that an empty list serialises as [] rather than null.
func TestHandleGetTodayContext_HandoffNextActionsCapped(t *testing.T) {
	tests := []struct {
		name      string
		count     int
		wantKept  int
		wantTotal int
	}{
		{name: "over the cap is trimmed", count: 20, wantKept: maxHandoffNextActions, wantTotal: 20},
		{
			name: "exactly at the cap is kept whole", count: maxHandoffNextActions,
			wantKept: maxHandoffNextActions, wantTotal: maxHandoffNextActions,
		},
		{name: "under the cap is kept whole", count: 2, wantKept: 2, wantTotal: 2},
		{name: "none at all still yields an array", count: 0, wantKept: 0, wantTotal: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actions := make([]session.NextAction, tc.count)
			for i := range actions {
				actions[i] = session.NextAction{
					Step:   i,
					Title:  fmt.Sprintf("step-%d", i),
					Status: session.NextActionPending,
				}
			}
			actionsJSON, err := json.Marshal(actions)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}

			s := &Server{
				gtd: todayContextTestGTDStore{},
				session: todayContextTestSessionStore{handoff: &db.SessionHandoff{
					ID:          uuid.New(),
					Intent:      "continue tomorrow",
					NextActions: actionsJSON,
				}},
			}
			raw := getTodayContextText(t, s)

			if strings.Contains(raw, `"next_actions":null`) {
				t.Errorf("next_actions must never be null, got: %s", raw)
			}

			var parsed struct {
				PendingHandoff *pendingHandoffSummary `json:"pending_handoff"`
			}
			if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
				t.Fatalf("unmarshal response: %v\nraw: %s", err, raw)
			}
			if parsed.PendingHandoff == nil {
				t.Fatal("pending_handoff missing")
			}
			if got := len(parsed.PendingHandoff.NextActions); got != tc.wantKept {
				t.Errorf("next_actions length = %d, want %d", got, tc.wantKept)
			}
			if got := parsed.PendingHandoff.NextActionsTotal; got != tc.wantTotal {
				t.Errorf("next_actions_total = %d, want %d", got, tc.wantTotal)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// truncation
// ---------------------------------------------------------------------------

func TestClipRunes(t *testing.T) {
	const max = 150

	tests := []struct {
		name         string
		in           string
		max          int
		wantRunes    int
		wantEllipsis bool
	}{
		{name: "far over the cap is clipped", in: strings.Repeat("a", 500), max: max, wantRunes: max + 1, wantEllipsis: true},
		{name: "exactly at the cap is untouched", in: strings.Repeat("a", 150), max: max, wantRunes: 150, wantEllipsis: false},
		{name: "one under the cap is untouched", in: strings.Repeat("a", 149), max: max, wantRunes: 149, wantEllipsis: false},
		{name: "empty stays empty", in: "", max: max, wantRunes: 0, wantEllipsis: false},
		{name: "one over the cap is clipped", in: strings.Repeat("a", 151), max: max, wantRunes: max + 1, wantEllipsis: true},
		{name: "zero cap clips everything", in: "abc", max: 0, wantRunes: 1, wantEllipsis: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := clipRunes(tc.in, tc.max)
			if n := utf8.RuneCountInString(got); n != tc.wantRunes {
				t.Errorf("clipRunes(%d runes, %d) = %d runes, want %d",
					utf8.RuneCountInString(tc.in), tc.max, n, tc.wantRunes)
			}
			if hasEllipsis := strings.HasSuffix(got, clipMarker); hasEllipsis != tc.wantEllipsis {
				t.Errorf("ellipsis present = %v, want %v (got %q)", hasEllipsis, tc.wantEllipsis, got)
			}
			if !tc.wantEllipsis && got != tc.in {
				t.Errorf("unclipped input must be returned verbatim: got %q, want %q", got, tc.in)
			}
		})
	}
}

// TestClipRunes_CJKNeverSplitsARune is the regression guard for byte-based
// truncation: clipping 500 Chinese characters must yield valid UTF-8 with no
// U+FFFD replacement character, because every one of them is 3 bytes wide and
// a byte-based cut would land mid-rune.
func TestClipRunes_CJKNeverSplitsARune(t *testing.T) {
	in := strings.Repeat("測", 500)
	got := clipRunes(in, pulledForwardDescMaxRunes)

	if !utf8.ValidString(got) {
		t.Fatalf("clipped CJK string is not valid UTF-8: %q", got)
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Errorf("clipped CJK string contains U+FFFD: %q", got)
	}
	if n := utf8.RuneCountInString(got); n != pulledForwardDescMaxRunes+1 {
		t.Errorf("rune count = %d, want %d", n, pulledForwardDescMaxRunes+1)
	}
	if want := strings.Repeat("測", pulledForwardDescMaxRunes) + clipMarker; got != want {
		t.Errorf("clipped value = %q, want %q", got, want)
	}
	// 150 CJK runes are 450 bytes plus the 3-byte ellipsis: the rune budget
	// and the byte size are genuinely different quantities here.
	if len(got) != pulledForwardDescMaxRunes*3+len(clipMarker) {
		t.Errorf("byte length = %d, want %d", len(got), pulledForwardDescMaxRunes*3+len(clipMarker))
	}
}

// TestHandleGetTodayContext_ClipsLongText verifies the caps are actually
// applied end to end (not just defined) for every clipped field.
func TestHandleGetTodayContext_ClipsLongText(t *testing.T) {
	raw := getTodayContextText(t, oversizedTextServer(t))

	var parsed todayContext
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, raw)
	}

	checks := []struct {
		name string
		got  string
		max  int
	}{
		{"goal description", parsed.Goals[0].Description, goalDescMaxRunes},
		{"project description", parsed.Projects[0].Description, projectDescMaxRunes},
		{"pulled_forward description", parsed.PulledForward[0].Description, pulledForwardDescMaxRunes},
		{"handoff context_summary", parsed.PendingHandoff.ContextSummary, handoffSummaryMaxRunes},
		{"next action title", parsed.PendingHandoff.NextActions[0].Title, nextActionFieldMaxRunes},
		{"next action command", parsed.PendingHandoff.NextActions[0].Command, nextActionFieldMaxRunes},
		{"next action expected", parsed.PendingHandoff.NextActions[0].Expected, nextActionFieldMaxRunes},
		{"sprint_summary", parsed.LatestStatusSnapshot.SprintSummary, sprintSummaryMaxRunes},
	}
	for _, c := range checks {
		if n := utf8.RuneCountInString(c.got); n != c.max+1 {
			t.Errorf("%s = %d runes, want %d (cap + ellipsis)", c.name, n, c.max+1)
		}
		if !strings.HasSuffix(c.got, clipMarker) {
			t.Errorf("%s was not marked as clipped: %q", c.name, c.got)
		}
	}

	// intent is under its cap in the fixture (199 < 300) and must survive intact.
	if strings.HasSuffix(parsed.PendingHandoff.Intent, clipMarker) {
		t.Errorf("intent below the cap must not be clipped: %q", parsed.PendingHandoff.Intent)
	}
}

// ---------------------------------------------------------------------------
// error handling
// ---------------------------------------------------------------------------

var errStore = errors.New("store exploded")

// callGetTodayContextErr invokes the handler and requires a tool-error result.
func callGetTodayContextErr(t *testing.T, s *Server) string {
	t.Helper()
	result, err := s.handleGetTodayContext(context.Background(), mcpmsg.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleGetTodayContext: unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected a tool error result, got success: %+v", result.Content)
	}
	return resultText(result)
}

// TestHandleGetTodayContext_ErrorPriorityIsDeterministic is the reason this
// handler uses sync.WaitGroup instead of errgroup: with every lookup failing
// at once, the reported error must always be the highest-priority one
// (goals), never whichever goroutine happened to finish first.
func TestHandleGetTodayContext_ErrorPriorityIsDeterministic(t *testing.T) {
	s := &Server{
		gtd: todayContextTestGTDStore{
			goalsErr:    errStore,
			projectsErr: errStore,
			progressErr: errStore,
			tasksErr:    errStore,
		},
		session: todayContextTestSessionStore{err: errStore},
	}

	for i := range 50 {
		if got := callGetTodayContextErr(t, s); !strings.HasPrefix(got, "loading goals:") {
			t.Fatalf("iteration %d: error = %q, want it to start with %q", i, got, "loading goals:")
		}
	}
}

// TestHandleGetTodayContext_SingleFailureMessages verifies each lookup's own
// failure surfaces with its own (unchanged) message, and that the documented
// "no pending handoff" sentinel is NOT an error.
func TestHandleGetTodayContext_SingleFailureMessages(t *testing.T) {
	tests := []struct {
		name    string
		gtd     todayContextTestGTDStore
		session todayContextTestSessionStore
		wantErr string
	}{
		{
			name:    "goals",
			gtd:     todayContextTestGTDStore{goalsErr: errStore},
			wantErr: "loading goals: store exploded",
		},
		{
			name:    "projects",
			gtd:     todayContextTestGTDStore{projectsErr: errStore},
			wantErr: "loading projects: store exploded",
		},
		{
			name:    "weekly progress",
			gtd:     todayContextTestGTDStore{progressErr: errStore},
			wantErr: "loading progress: store exploded",
		},
		{
			name:    "handoff",
			session: todayContextTestSessionStore{err: errStore},
			wantErr: "loading handoff: store exploded",
		},
		{
			name:    "pull-forward",
			gtd:     todayContextTestGTDStore{tasksErr: errStore},
			wantErr: "loading pull-forward tasks: store exploded",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{gtd: tc.gtd, session: tc.session}
			if got := callGetTodayContextErr(t, s); got != tc.wantErr {
				t.Errorf("error = %q, want %q", got, tc.wantErr)
			}
		})
	}
}

// TestHandleGetTodayContext_HandoffNotFoundIsNotAnError pins the best-effort
// semantics of the handoff lookup: session.ErrNotFound means "no pending
// handoff", which is the normal case, not a failure.
func TestHandleGetTodayContext_HandoffNotFoundIsNotAnError(t *testing.T) {
	s := &Server{
		gtd:     todayContextTestGTDStore{},
		session: todayContextTestSessionStore{err: session.ErrNotFound},
	}
	raw := getTodayContextText(t, s)
	if !strings.Contains(raw, `"pending_handoff":null`) {
		t.Errorf("expected a null pending_handoff, got: %s", raw)
	}
}

// TestHandleGetTodayContext_SnapshotFailureDegradesGracefully verifies the
// best-effort latest_status_snapshot lookup: a hard failure (not
// ErrNotFound) must omit the field, never fail the whole tool.
func TestHandleGetTodayContext_SnapshotFailureDegradesGracefully(t *testing.T) {
	tests := []struct {
		name  string
		store snapshot.StoreIface
	}{
		{name: "store not wired", store: nil},
		{name: "no fresh snapshot", store: todayContextTestSnapshotStore{}},
		{name: "store fails hard", store: todayContextTestSnapshotStore{err: errStore}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{
				gtd:           todayContextTestGTDStore{completed: 1, total: 2},
				session:       todayContextTestSessionStore{},
				snapshotStore: tc.store,
			}
			raw := getTodayContextText(t, s)
			if strings.Contains(raw, "latest_status_snapshot") {
				t.Errorf("degraded response must omit latest_status_snapshot, got: %s", raw)
			}
			if !strings.Contains(raw, `"weekly_progress"`) {
				t.Errorf("rest of the payload must still be present, got: %s", raw)
			}
		})
	}
}

// TestHandleGetTodayContext_PullForwardStoreError verifies a PullForwardTasks
// error surfaces as a tool error result (matching the sibling goals/projects/
// weekly-progress error-handling convention in handleGetTodayContext) instead
// of silently dropping the field or panicking.
func TestHandleGetTodayContext_PullForwardStoreError(t *testing.T) {
	s := &Server{
		gtd:     todayContextTestGTDStore{tasksErr: context.DeadlineExceeded},
		session: todayContextTestSessionStore{},
	}
	if got := callGetTodayContextErr(t, s); !strings.HasPrefix(got, "loading pull-forward tasks:") {
		t.Errorf("error = %q, want it to start with %q", got, "loading pull-forward tasks:")
	}
}

// ---------------------------------------------------------------------------
// concurrency
// ---------------------------------------------------------------------------

// TestHandleGetTodayContext_FetchesConcurrently proves the six store lookups
// overlap. Each of them sleeps 60 ms, so a serial implementation needs
// >= 360 ms while a parallel one needs ~60 ms; the 200 ms bound sits far from
// both, leaving room for scheduler noise without going flaky.
func TestHandleGetTodayContext_FetchesConcurrently(t *testing.T) {
	const (
		perCall  = 60 * time.Millisecond
		serial   = 6 * perCall
		maxTotal = 200 * time.Millisecond
	)

	s := &Server{
		gtd:           todayContextTestGTDStore{delay: perCall},
		session:       todayContextTestSessionStore{delay: perCall},
		snapshotStore: todayContextTestSnapshotStore{delay: perCall},
	}

	start := time.Now()
	_ = getTodayContextText(t, s)
	elapsed := time.Since(start)

	t.Logf("6 lookups × %v took %v (serial would be %v)", perCall, elapsed, serial)
	if elapsed >= maxTotal {
		t.Errorf("get_today_context took %v, want < %v — the lookups are not running concurrently",
			elapsed, maxTotal)
	}
	// Guard against the fixture silently losing its delays, which would make
	// the bound above meaningless.
	if elapsed < perCall {
		t.Errorf("took %v, less than a single lookup (%v) — the fake stores are not being called",
			elapsed, perCall)
	}
}

// ---------------------------------------------------------------------------
// regression: the session tools must NOT be clipped
// ---------------------------------------------------------------------------

// TestSessionToolsEchoBackUnclipped is the guard for the shared-type hazard:
// pendingHandoffView is used by set_session_handoff and mark_next_action_done
// (tools_session.go) to echo the caller's own text back. get_today_context's
// budget is served by the separate pendingHandoffSummary type, so clipping it
// must never leak into those two tools.
//
// Runs against a real SQLite-backed store (newTestWorkSessionServer), so it
// exercises the actual persist → read-back path rather than a fake.
func TestSessionToolsEchoBackUnclipped(t *testing.T) {
	s := newTestWorkSessionServer(t)

	// Every field is deliberately longer than the get_today_context cap that
	// applies to its counterpart.
	intent := cjk(handoffIntentMaxRunes + 100)
	summary := cjk(handoffSummaryMaxRunes + 400)
	actionTitle := cjk(nextActionFieldMaxRunes + 200)
	actionExpected := cjk(nextActionFieldMaxRunes + 150)

	actionsJSON, err := json.Marshal([]map[string]any{
		{"step": 0, "title": actionTitle, "expected": actionExpected, "status": "pending"},
		{"step": 1, "title": "second step", "status": "pending"},
	})
	if err != nil {
		t.Fatalf("marshal next_actions: %v", err)
	}

	setRes := callSetSessionHandoff(t, s, map[string]any{
		"intent":          intent,
		"context_summary": summary,
		"next_actions":    string(actionsJSON),
	})
	if setRes.IsError {
		t.Fatalf("set_session_handoff failed: %s", resultText(setRes))
	}

	assertUnclippedHandoff := func(t *testing.T, tool, raw string) {
		t.Helper()
		if strings.Contains(raw, clipMarker) {
			t.Errorf("%s response contains the clip marker %q — echo-back was truncated:\n%s",
				tool, clipMarker, raw)
		}
		var view pendingHandoffView
		if err := json.Unmarshal([]byte(raw), &view); err != nil {
			t.Fatalf("%s: unmarshal response: %v\nraw: %s", tool, err, raw)
		}
		if view.Intent != intent {
			t.Errorf("%s: intent round-tripped as %d runes, want %d",
				tool, utf8.RuneCountInString(view.Intent), utf8.RuneCountInString(intent))
		}
		if view.ContextSummary.String != summary {
			t.Errorf("%s: context_summary round-tripped as %d runes, want %d",
				tool, utf8.RuneCountInString(view.ContextSummary.String), utf8.RuneCountInString(summary))
		}
		if len(view.NextActions) != 2 {
			t.Fatalf("%s: next_actions length = %d, want 2", tool, len(view.NextActions))
		}
		if view.NextActions[0].Title != actionTitle {
			t.Errorf("%s: next_actions[0].title round-tripped as %d runes, want %d",
				tool, utf8.RuneCountInString(view.NextActions[0].Title), utf8.RuneCountInString(actionTitle))
		}
		if view.NextActions[0].Expected != actionExpected {
			t.Errorf("%s: next_actions[0].expected round-tripped as %d runes, want %d",
				tool, utf8.RuneCountInString(view.NextActions[0].Expected), utf8.RuneCountInString(actionExpected))
		}
	}

	setRaw := resultText(setRes)
	assertUnclippedHandoff(t, "set_session_handoff", setRaw)

	var setView pendingHandoffView
	if err := json.Unmarshal([]byte(setRaw), &setView); err != nil {
		t.Fatalf("unmarshal set_session_handoff response: %v", err)
	}

	doneRes := callMarkNextActionDone(t, s, map[string]any{
		"handoff_id": setView.ID.String(),
		"step":       float64(0),
	})
	if doneRes.IsError {
		t.Fatalf("mark_next_action_done failed: %s", resultText(doneRes))
	}
	assertUnclippedHandoff(t, "mark_next_action_done", resultText(doneRes))
}
