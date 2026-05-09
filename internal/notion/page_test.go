package notion

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// notionFakeServer captures every request the Notion client issues so a
// single test can assert "POST /pages was called once, PATCH /pages/:id was
// called once" — the critical idempotency contract.
//
// Each handler returns the JSON response body to write at HTTP 200; tests
// that need to exercise non-200 paths build a bespoke http.Handler instead
// (see TestUpsertDailyPage_NotionAPIErrorBubblesUp).
type notionFakeServer struct {
	mu             sync.Mutex
	calls          []recordedCall
	databaseQueryF func(req *http.Request) any
	createPageF    func(req *http.Request) any
	patchPageF     func(req *http.Request, pageID string) any
	patchBlocksF   func(req *http.Request, pageID string) any
	listChildrenF  func(req *http.Request, pageID string) any
	patchBlockF    func(req *http.Request, blockID string) any
}

type recordedCall struct {
	Method string
	Path   string
	Body   string
}

func (s *notionFakeServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		s.mu.Lock()
		s.calls = append(s.calls, recordedCall{Method: r.Method, Path: r.URL.Path, Body: string(body)})
		s.mu.Unlock()
		// Reset r.Body so handlers can re-read if needed (we already consumed).
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		s.dispatch(w, r)
	})
}

// dispatch routes an incoming request to the appropriate fake handler.
// Extracted from handler() so each function stays within the gocyclo limit.
func (s *notionFakeServer) dispatch(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if method == http.MethodPost && strings.HasPrefix(path, "/v1/databases/") && strings.HasSuffix(path, "/query") {
		writeJSON(w, s.databaseQueryF(r))
		return
	}
	if method == http.MethodPost && path == "/v1/pages" {
		writeJSON(w, s.createPageF(r))
		return
	}
	if method == http.MethodPatch && strings.HasPrefix(path, "/v1/pages/") {
		writeJSON(w, s.patchPageF(r, strings.TrimPrefix(path, "/v1/pages/")))
		return
	}
	if strings.HasPrefix(path, "/v1/blocks/") && strings.HasSuffix(path, "/children") {
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/blocks/"), "/children")
		if method == http.MethodPatch {
			writeJSON(w, s.patchBlocksF(r, id))
		} else {
			writeJSON(w, s.listChildrenF(r, id))
		}
		return
	}
	if method == http.MethodPatch && strings.HasPrefix(path, "/v1/blocks/") {
		writeJSON(w, s.patchBlockF(r, strings.TrimPrefix(path, "/v1/blocks/")))
		return
	}
	http.Error(w, "unhandled "+method+" "+path, http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(payload)
}

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return &Client{
		token:   "secret_test_token",
		dbID:    "db_test_123",
		baseURL: srv.URL + "/v1",
		http:    srv.Client(),
	}
}

func sampleBriefing() *DailyBriefing {
	now := time.Date(2026, 4, 28, 8, 0, 0, 0, time.UTC)
	return &DailyBriefing{
		Date: now,
		InProgressTasks: []TaskBlock{
			{ID: "t1", Title: "ship feature", Importance: 1, Context: "P0"},
		},
		RecentDecisions: []DecisionBlock{
			{ID: "d1", Title: "use option B", RepoName: "wayneblacktea", CreatedAt: now.Add(-2 * time.Hour)},
		},
		PendingProposals: []ProposalBlock{
			{ID: "p1", Type: "concept", ProposedBy: "mcp:add_knowledge", CreatedAt: now.Add(-3 * time.Hour)},
		},
		DueReviews: []ReviewBlock{
			{ConceptID: "c1", Title: "spaced repetition", DueDate: now.Add(-1 * time.Hour)},
		},
		SystemHealth: HealthBlock{
			StuckTaskCount:      1,
			StuckTaskIDs:        []string{"t-stuck"},
			WeeklyCompletedTask: 5,
			WeeklyTotalActive:   12,
		},
	}
}

func TestUpsertDailyPage_CreatesWhenMissing(t *testing.T) {
	t.Parallel()

	fake := &notionFakeServer{
		databaseQueryF: func(_ *http.Request) any {
			return map[string]any{"results": []any{}}
		},
		createPageF: func(_ *http.Request) any {
			return map[string]any{"id": "new_page_123", "url": "https://notion.so/new_page_123"}
		},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	c := newTestClient(t, srv)

	if err := c.UpsertDailyPage(context.Background(), sampleBriefing()); err != nil {
		t.Fatalf("UpsertDailyPage failed: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if got := callCount(fake.calls, http.MethodPost, "/v1/pages"); got != 1 {
		t.Errorf("expected exactly 1 POST /v1/pages, got %d (all calls: %+v)", got, fake.calls)
	}
	if got := callCount(fake.calls, http.MethodPost, "/v1/databases/db_test_123/query"); got != 1 {
		t.Errorf("expected exactly 1 POST query, got %d", got)
	}
	for _, c := range fake.calls {
		if c.Method == http.MethodPatch {
			t.Errorf("did not expect PATCH on missing-page create: %+v", c)
		}
	}
}

func TestUpsertDailyPage_PatchesWhenExisting(t *testing.T) {
	t.Parallel()

	const existingPageID = "existing_page_456"
	patchPageCalls := 0
	createPageCalls := 0
	listChildrenCalls := 0
	patchBlocksChildrenCalls := 0

	fake := &notionFakeServer{
		databaseQueryF: func(_ *http.Request) any {
			return map[string]any{
				"results": []any{
					map[string]any{"id": existingPageID},
				},
			}
		},
		createPageF: func(_ *http.Request) any {
			createPageCalls++
			return map[string]any{}
		},
		patchPageF: func(_ *http.Request, pageID string) any {
			if pageID != existingPageID {
				t.Errorf("PATCH /pages/:id called with unexpected id %q (want %q)", pageID, existingPageID)
			}
			patchPageCalls++
			return map[string]any{"id": pageID}
		},
		listChildrenF: func(_ *http.Request, _ string) any {
			listChildrenCalls++
			return map[string]any{
				"results":  []any{},
				"has_more": false,
			}
		},
		patchBlocksF: func(_ *http.Request, _ string) any {
			patchBlocksChildrenCalls++
			return map[string]any{}
		},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	c := newTestClient(t, srv)

	if err := c.UpsertDailyPage(context.Background(), sampleBriefing()); err != nil {
		t.Fatalf("UpsertDailyPage failed: %v", err)
	}

	if createPageCalls != 0 {
		t.Errorf("expected 0 POST /pages on existing page, got %d", createPageCalls)
	}
	if patchPageCalls != 1 {
		t.Errorf("expected exactly 1 PATCH /pages/:id, got %d", patchPageCalls)
	}
	if listChildrenCalls != 1 {
		t.Errorf("expected exactly 1 GET /blocks/:id/children, got %d", listChildrenCalls)
	}
	if patchBlocksChildrenCalls != 1 {
		t.Errorf("expected exactly 1 PATCH /blocks/:id/children (append), got %d", patchBlocksChildrenCalls)
	}
}

func TestUpsertDailyPage_IdempotentOnRepeatedRun(t *testing.T) {
	t.Parallel()

	const pageID = "stable_page_789"
	createCalls := 0
	patchCalls := 0
	storedExisting := false // first run creates, subsequent runs find + patch

	fake := &notionFakeServer{
		databaseQueryF: func(_ *http.Request) any {
			if !storedExisting {
				return map[string]any{"results": []any{}}
			}
			return map[string]any{
				"results": []any{map[string]any{"id": pageID}},
			}
		},
		createPageF: func(_ *http.Request) any {
			createCalls++
			storedExisting = true
			return map[string]any{"id": pageID, "url": "https://notion.so/" + pageID}
		},
		patchPageF: func(_ *http.Request, _ string) any {
			patchCalls++
			return map[string]any{}
		},
		listChildrenF: func(_ *http.Request, _ string) any {
			return map[string]any{"results": []any{}, "has_more": false}
		},
		patchBlocksF: func(_ *http.Request, _ string) any {
			return map[string]any{}
		},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	c := newTestClient(t, srv)

	for i := 0; i < 3; i++ {
		if err := c.UpsertDailyPage(context.Background(), sampleBriefing()); err != nil {
			t.Fatalf("UpsertDailyPage attempt %d failed: %v", i+1, err)
		}
	}

	if createCalls != 1 {
		t.Errorf("expected exactly 1 POST /pages over 3 runs (first creates, rest patch), got %d", createCalls)
	}
	if patchCalls != 2 {
		t.Errorf("expected exactly 2 PATCH /pages/:id (runs 2 and 3), got %d", patchCalls)
	}
}

func TestUpsertDailyPage_NotionAPIErrorBubblesUp(t *testing.T) {
	t.Parallel()

	// Use a raw handler that always returns 500 to exercise the error-bubbling path.
	// No notionFakeServer needed here because the database query itself fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"object":"error","code":"internal_server_error"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.UpsertDailyPage(context.Background(), sampleBriefing())
	if err == nil {
		t.Fatal("expected error from 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to mention status 500, got %v", err)
	}
}

func TestUpsertDailyPage_NilClientReturnsSentinel(t *testing.T) {
	t.Parallel()

	var c *Client
	err := c.UpsertDailyPage(context.Background(), sampleBriefing())
	if !errors.Is(err, ErrClientNotConfigured) {
		t.Errorf("expected ErrClientNotConfigured, got %v", err)
	}
}

func TestUpsertDailyPage_NilBriefing(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := newTestClient(t, srv)
	if err := c.UpsertDailyPage(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil briefing")
	}
}

func TestUpsertDailyPage_EmptyDatabaseID(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := newTestClient(t, srv)
	c.dbID = ""
	if err := c.UpsertDailyPage(context.Background(), sampleBriefing()); err == nil {
		t.Fatal("expected error when NOTION_DATABASE_ID is empty")
	}
}

func TestNewClient_ReturnsNilWhenSecretMissing(t *testing.T) {
	// This test does NOT run in parallel because it manipulates env vars.
	t.Setenv("NOTION_INTEGRATION_SECRET", "")
	if c := NewClient(); c != nil {
		t.Errorf("expected nil client when NOTION_INTEGRATION_SECRET is unset, got %+v", c)
	}
}

func TestNewClient_ReadsIntegrationSecret(t *testing.T) {
	t.Setenv("NOTION_INTEGRATION_SECRET", "secret_xyz")
	t.Setenv("NOTION_DATABASE_ID", "db_xyz")
	c := NewClient()
	if c == nil {
		t.Fatal("expected non-nil client when NOTION_INTEGRATION_SECRET is set")
	}
	if c.token != "secret_xyz" {
		t.Errorf("token = %q, want secret_xyz", c.token)
	}
	if c.dbID != "db_xyz" {
		t.Errorf("dbID = %q, want db_xyz", c.dbID)
	}
}

func TestDailyBriefingChildren_RespectsBlockCap(t *testing.T) {
	t.Parallel()

	tasks := make([]TaskBlock, 200)
	for i := range tasks {
		tasks[i] = TaskBlock{ID: "t", Title: "task", Importance: 3}
	}
	b := &DailyBriefing{Date: time.Now(), InProgressTasks: tasks}
	out := dailyBriefingChildren(b)
	if len(out) > dailyBriefingMaxBlocks {
		t.Errorf("dailyBriefingChildren produced %d blocks, exceeds cap of %d",
			len(out), dailyBriefingMaxBlocks)
	}
}

func TestTruncateForNotion(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", dailyBriefingRichTextLimit+50)
	got := truncateForNotion(long)
	if len(got) != dailyBriefingRichTextLimit {
		t.Errorf("len = %d, want %d", len(got), dailyBriefingRichTextLimit)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected ellipsis suffix, got %q", got[len(got)-10:])
	}

	short := "hello"
	if got := truncateForNotion(short); got != short {
		t.Errorf("short string mutated: got %q want %q", got, short)
	}
}

// callCount counts how many recorded calls match method+path exactly.
func callCount(calls []recordedCall, method, path string) int {
	n := 0
	for _, c := range calls {
		if c.Method == method && c.Path == path {
			n++
		}
	}
	return n
}

// blockType extracts the "type" field from a Notion block map.
func blockType(b map[string]any) string {
	t, _ := b["type"].(string)
	return t
}

// TestDailyBriefingChildren_CalloutIsFirstBlock asserts that the first block
// rendered by dailyBriefingChildren is always a callout block containing the
// date + count summary line. This is the N2 mobile-friendly overview callout.
func TestDailyBriefingChildren_CalloutIsFirstBlock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 10, 8, 0, 0, 0, time.UTC)
	b := &DailyBriefing{
		Date: now,
		InProgressTasks: []TaskBlock{
			{ID: "t1", Title: "alpha", Importance: 1},
			{ID: "t2", Title: "beta", Importance: 2},
		},
		DueReviews:       []ReviewBlock{{ConceptID: "c1", Title: "review A"}},
		PendingProposals: []ProposalBlock{{ID: "p1", Type: "concept"}},
	}
	out := dailyBriefingChildren(b)
	if len(out) == 0 {
		t.Fatal("expected at least one block, got none")
	}
	if got := blockType(out[0]); got != "callout" {
		t.Errorf("first block type = %q, want %q", got, "callout")
	}
	// Verify callout content contains the date and task count.
	callout, _ := out[0]["callout"].(map[string]any)
	richText, _ := callout["rich_text"].([]map[string]any)
	if len(richText) == 0 {
		t.Fatal("callout rich_text is empty")
	}
	textObj, _ := richText[0]["text"].(map[string]any)
	content, _ := textObj["content"].(string)
	if !strings.Contains(content, "2026-05-10") {
		t.Errorf("callout content %q does not contain date 2026-05-10", content)
	}
	if !strings.Contains(content, "2 tasks") {
		t.Errorf("callout content %q does not contain '2 tasks'", content)
	}
}

// TestDailyBriefingChildren_TasksUseToDoBlocks asserts that in-progress tasks
// render as to_do blocks (not bulleted_list_item), enabling mobile checkbox UX.
func TestDailyBriefingChildren_TasksUseToDoBlocks(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 10, 8, 0, 0, 0, time.UTC)
	b := &DailyBriefing{
		Date: now,
		InProgressTasks: []TaskBlock{
			{ID: "t1", Title: "first task", Importance: 1},
			{ID: "t2", Title: "second task", Importance: 3},
		},
	}
	out := dailyBriefingChildren(b)

	// Collect all to_do blocks.
	var todoCnt int
	for _, blk := range out {
		if blockType(blk) == "to_do" {
			todoCnt++
		}
	}
	if todoCnt != 2 {
		t.Errorf("expected 2 to_do blocks for 2 tasks, got %d (all types: %v)", todoCnt, blockTypes(out))
	}
}

// TestDailyBriefingChildren_DecisionWithRationaleHasToggleChildren asserts
// that a decision with a non-empty Rationale produces a toggle block whose
// children slice is non-empty (the rationale rendered as a bullet child).
func TestDailyBriefingChildren_DecisionWithRationaleHasToggleChildren(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 10, 8, 0, 0, 0, time.UTC)
	b := &DailyBriefing{
		Date: now,
		RecentDecisions: []DecisionBlock{
			{
				ID:        "dec1",
				Title:     "use toggleBlock for decisions",
				RepoName:  "wayneblacktea",
				Rationale: "better mobile UX; collapsible rationale avoids wall-of-text",
				CreatedAt: now.Add(-1 * time.Hour),
			},
		},
	}
	out := dailyBriefingChildren(b)

	var toggles []map[string]any
	for _, blk := range out {
		if blockType(blk) == "toggle" {
			toggles = append(toggles, blk)
		}
	}
	if len(toggles) != 1 {
		t.Fatalf("expected exactly 1 toggle block, got %d (all types: %v)", len(toggles), blockTypes(out))
	}

	inner, _ := toggles[0]["toggle"].(map[string]any)
	children, _ := inner["children"].([]map[string]any)
	if len(children) == 0 {
		t.Errorf("toggle children is empty; expected rationale bullet child")
	}
}

// TestDailyBriefingChildren_DecisionWithoutRationaleHasEmptyChildren asserts
// that a decision with no Rationale produces a toggle with an empty children
// slice (no child content, just the collapsible header).
func TestDailyBriefingChildren_DecisionWithoutRationaleHasEmptyChildren(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 10, 8, 0, 0, 0, time.UTC)
	b := &DailyBriefing{
		Date: now,
		RecentDecisions: []DecisionBlock{
			{ID: "dec2", Title: "no rationale decision", CreatedAt: now.Add(-30 * time.Minute)},
		},
	}
	out := dailyBriefingChildren(b)

	var toggles []map[string]any
	for _, blk := range out {
		if blockType(blk) == "toggle" {
			toggles = append(toggles, blk)
		}
	}
	if len(toggles) != 1 {
		t.Fatalf("expected exactly 1 toggle block, got %d", len(toggles))
	}

	inner, _ := toggles[0]["toggle"].(map[string]any)
	children, _ := inner["children"].([]map[string]any)
	if len(children) != 0 {
		t.Errorf("expected empty children for decision without rationale, got %d children", len(children))
	}
}

// blockTypes is a debug helper that returns all block type strings from a
// slice of blocks, for use in test failure messages.
func blockTypes(blocks []map[string]any) []string {
	types := make([]string, len(blocks))
	for i, b := range blocks {
		types[i] = blockType(b)
	}
	return types
}
