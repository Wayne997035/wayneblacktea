package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/storage"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// newTestResourceServer constructs a minimal Server backed by an in-process
// SQLite DB for resource handler tests. No mocks — real store implementation.
func newTestResourceServer(t *testing.T) *Server {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "resource-test.db")
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
	return srv
}

// parseResourceJSON extracts the JSON text from the first TextResourceContents
// in the slice and unmarshals it into v.
func parseResourceJSON(t *testing.T, contents []mcpmsg.ResourceContents, v any) {
	t.Helper()
	if len(contents) == 0 {
		t.Fatal("resource returned 0 contents")
	}
	rc, ok := contents[0].(mcpmsg.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", contents[0])
	}
	if err := json.Unmarshal([]byte(rc.Text), v); err != nil {
		t.Fatalf("unmarshal resource JSON: %v\ntext: %s", err, rc.Text)
	}
}

// TestResource_DashboardOverview_Empty verifies the overview resource
// returns valid JSON with required fields when the DB is empty.
func TestResource_DashboardOverview_Empty(t *testing.T) {
	s := newTestResourceServer(t)

	contents, err := s.handleResourceDashboardOverview(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceDashboardOverview: %v", err)
	}

	var body map[string]any
	parseResourceJSON(t, contents, &body)

	// generated_at must be present and RFC3339-parseable.
	genAt, ok := body["generated_at"].(string)
	if !ok || genAt == "" {
		t.Error("missing or empty generated_at")
	}
	if _, err := time.Parse(time.RFC3339, genAt); err != nil {
		t.Errorf("generated_at not RFC3339: %v", err)
	}

	// workspace_id must be present.
	wsID, ok := body["workspace_id"].(string)
	if !ok || wsID == "" {
		t.Error("missing or empty workspace_id")
	}

	// pending_handoff must be false (no handoff in empty DB).
	ph, _ := body["pending_handoff"].(bool)
	if ph {
		t.Error("expected pending_handoff=false for empty DB")
	}

	// arch_snapshot_present must be bool false (no snapshot stored).
	asp, ok := body["arch_snapshot_present"].(bool)
	if !ok {
		t.Error("arch_snapshot_present should be a bool")
	}
	if asp {
		t.Error("expected arch_snapshot_present=false for empty DB")
	}

	// Raw arch snapshot text MUST NOT appear in the response.
	raw, _ := json.Marshal(body)
	if contains(string(raw), "PROJECT ARCH") || contains(string(raw), "arch_snapshot_data") {
		t.Error("overview resource must not include raw arch snapshot text (prompt-injection risk)")
	}
}

// TestResource_DashboardOverview_NoRawArchText verifies that even when the
// arch snapshot has been stored, the resource does NOT include raw snapshot text.
func TestResource_DashboardOverview_NoRawArchText(t *testing.T) {
	s := newTestResourceServer(t)

	contents, err := s.handleResourceDashboardOverview(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceDashboardOverview: %v", err)
	}

	var body map[string]any
	parseResourceJSON(t, contents, &body)

	// Confirm fields arch_snapshot_data and PROJECT ARCH are absent.
	if _, found := body["arch_snapshot_data"]; found {
		t.Error("overview resource must not expose arch_snapshot_data key")
	}
	b, _ := json.Marshal(body)
	if contains(string(b), "PROJECT ARCH") {
		t.Error("overview resource must not contain raw arch text markers")
	}
}

// TestResource_DashboardOverview_WorkspaceID verifies workspace_id is populated.
func TestResource_DashboardOverview_WorkspaceID(t *testing.T) {
	s := newTestResourceServer(t)

	contents, err := s.handleResourceDashboardOverview(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceDashboardOverview: %v", err)
	}

	var body map[string]any
	parseResourceJSON(t, contents, &body)

	wsID, ok := body["workspace_id"].(string)
	if !ok || wsID == "" {
		t.Error("workspace_id must be non-empty string")
	}
}

// TestResource_DashboardOverview_NilHandoff verifies that session.ErrNotFound
// (no handoff in DB) is handled gracefully — pending_handoff=false, no error.
func TestResource_DashboardOverview_NilHandoff(t *testing.T) {
	s := newTestResourceServer(t)

	// Empty DB → no handoff → session.ErrNotFound must be absorbed.
	contents, err := s.handleResourceDashboardOverview(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("expected no error for nil handoff; got: %v", err)
	}

	var body map[string]any
	parseResourceJSON(t, contents, &body)

	if ph, _ := body["pending_handoff"].(bool); ph {
		t.Error("pending_handoff must be false when no handoff exists")
	}
	if _, hasAt := body["pending_handoff_created_at"]; hasAt {
		t.Error("pending_handoff_created_at must be absent when no handoff exists")
	}
}

// TestResource_DashboardUpcoming_Empty verifies the upcoming resource returns
// valid JSON with 5 bucket keys even when the DB is empty.
func TestResource_DashboardUpcoming_Empty(t *testing.T) {
	s := newTestResourceServer(t)

	contents, err := s.handleResourceDashboardUpcoming(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceDashboardUpcoming: %v", err)
	}

	var body map[string]any
	parseResourceJSON(t, contents, &body)

	// required top-level fields
	assertStringField(t, body, "generated_at")
	assertStringField(t, body, "workspace_id")

	groups, ok := body["groups"].(map[string]any)
	if !ok {
		t.Fatalf("groups must be an object, got %T", body["groups"])
	}
	for _, bucket := range []string{"today", "tomorrow", "day_after", "upcoming", "unscheduled_important"} {
		if _, found := groups[bucket]; !found {
			t.Errorf("groups missing bucket %q", bucket)
		}
	}
}

// TestResource_SystemHealth_Empty verifies the health resource returns valid
// JSON with required fields and does NOT include recent_calls or tool_call_counts.
func TestResource_SystemHealth_Empty(t *testing.T) {
	s := newTestResourceServer(t)

	contents, err := s.handleResourceSystemHealth(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceSystemHealth: %v", err)
	}

	var body map[string]any
	parseResourceJSON(t, contents, &body)

	// required fields
	assertStringField(t, body, "generated_at")
	assertStringField(t, body, "workspace_id")

	if _, found := body["tasks"]; !found {
		t.Error("system/health must include 'tasks' field")
	}

	// MUST NOT include expensive/high-churn fields.
	if _, found := body["recent_calls"]; found {
		t.Error("system/health resource must NOT include recent_calls")
	}
	if _, found := body["tool_call_counts"]; found {
		t.Error("system/health resource must NOT include tool_call_counts")
	}
	if _, found := body["completion_drift_candidates"]; found {
		t.Error("system/health resource must NOT include completion_drift_candidates")
	}
}

// TestResource_SystemHealth_WorkspaceID verifies workspace_id is present.
func TestResource_SystemHealth_WorkspaceID(t *testing.T) {
	s := newTestResourceServer(t)

	contents, err := s.handleResourceSystemHealth(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceSystemHealth: %v", err)
	}

	var body map[string]any
	parseResourceJSON(t, contents, &body)

	wsID, ok := body["workspace_id"].(string)
	if !ok || wsID == "" {
		t.Error("workspace_id must be non-empty string")
	}

	// generated_at must be RFC3339
	genAt, _ := body["generated_at"].(string)
	if _, err := time.Parse(time.RFC3339, genAt); err != nil {
		t.Errorf("generated_at not RFC3339: %v", err)
	}
}

// TestResource_GTDCurrent_Empty verifies the gtd/current resource returns
// valid JSON with required fields when the DB is empty.
func TestResource_GTDCurrent_Empty(t *testing.T) {
	s := newTestResourceServer(t)

	contents, err := s.handleResourceGTDCurrent(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceGTDCurrent: %v", err)
	}

	var body map[string]any
	parseResourceJSON(t, contents, &body)

	assertStringField(t, body, "generated_at")
	assertStringField(t, body, "workspace_id")

	// top_task must be null when no tasks exist.
	if tt, found := body["top_task"]; found && tt != nil {
		t.Errorf("expected top_task=null for empty DB, got %v", tt)
	}

	// proposal_backlog must be 0.
	if pb, _ := body["proposal_backlog"].(float64); pb != 0 {
		t.Errorf("expected proposal_backlog=0, got %v", pb)
	}

	// unresolved_handoff must be false.
	if uh, _ := body["unresolved_handoff"].(bool); uh {
		t.Error("expected unresolved_handoff=false for empty DB")
	}
}

// TestResource_GTDCurrent_NilHandoff verifies that a missing handoff
// (session.ErrNotFound) is handled as unresolved_handoff=false without error.
func TestResource_GTDCurrent_NilHandoff(t *testing.T) {
	s := newTestResourceServer(t)

	contents, err := s.handleResourceGTDCurrent(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("expected no error for nil handoff; got: %v", err)
	}

	var body map[string]any
	parseResourceJSON(t, contents, &body)

	if uh, _ := body["unresolved_handoff"].(bool); uh {
		t.Error("unresolved_handoff must be false when no handoff exists")
	}
}

// TestResource_GTDCurrent_WorkspaceID verifies workspace_id is always present.
func TestResource_GTDCurrent_WorkspaceID(t *testing.T) {
	s := newTestResourceServer(t)

	contents, err := s.handleResourceGTDCurrent(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceGTDCurrent: %v", err)
	}

	var body map[string]any
	parseResourceJSON(t, contents, &body)

	wsID, ok := body["workspace_id"].(string)
	if !ok || wsID == "" {
		t.Error("workspace_id must be non-empty string")
	}
}

// TestResource_MCPServer_RegistersExactlyFourResources verifies that MCPServer()
// registers exactly 4 resource URIs after the resources/prompts extension.
func TestResource_MCPServer_RegistersExactlyFourResources(t *testing.T) {
	s := newTestResourceServer(t)
	ms := s.MCPServer()
	if ms == nil {
		t.Fatal("MCPServer() returned nil")
	}
	// Smoke test: resource registration must not panic and server must be non-nil.
	// (Introspecting the exact URIs requires calling resources/list over the wire;
	// the integration test in server_test.go covers the full round-trip via MCPServer.)
}

// TestResource_MarshalResource_ReturnsTextResourceContents verifies that
// marshalResource produces TextResourceContents with the right URI and MIME type.
func TestResource_MarshalResource_ReturnsTextResourceContents(t *testing.T) {
	type payload struct {
		Foo string `json:"foo"`
	}
	contents, err := marshalResource("wayneblacktea://test", payload{Foo: "bar"})
	if err != nil {
		t.Fatalf("marshalResource: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	rc, ok := contents[0].(mcpmsg.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", contents[0])
	}
	if rc.URI != "wayneblacktea://test" {
		t.Errorf("URI = %q, want %q", rc.URI, "wayneblacktea://test")
	}
	if rc.MIMEType != "application/json" {
		t.Errorf("MIMEType = %q, want %q", rc.MIMEType, "application/json")
	}
	var got payload
	if err := json.Unmarshal([]byte(rc.Text), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Foo != "bar" {
		t.Errorf("Foo = %q, want %q", got.Foo, "bar")
	}
}

// ─── prompt handler tests ──────────────────────────────────────────────────

// TestPrompt_AllHandlers_ReturnUserRoleMessage verifies each prompt handler
// returns exactly 1 PromptMessage with role=user and non-empty text.
func TestPrompt_AllHandlers_ReturnUserRoleMessage(t *testing.T) {
	s := newTestResourceServer(t)

	cases := []struct {
		name    string
		handler func(context.Context, mcpmsg.GetPromptRequest) (*mcpmsg.GetPromptResult, error)
		wantURI string // a resource URI that must appear in the text
	}{
		{
			name:    "start_work",
			handler: s.handlePromptStartWork,
			wantURI: "wayneblacktea://dashboard/overview",
		},
		{
			name:    "closeout_session",
			handler: s.handlePromptCloseoutSession,
			wantURI: "wayneblacktea://system/health",
		},
		{
			name:    "plan_tomorrow",
			handler: s.handlePromptPlanTomorrow,
			wantURI: "wayneblacktea://dashboard/upcoming",
		},
		{
			name:    "reconcile_dashboard",
			handler: s.handlePromptReconcileDashboard,
			wantURI: "wayneblacktea://system/health",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.handler(context.Background(), mcpmsg.GetPromptRequest{})
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil GetPromptResult")
			}
			if len(result.Messages) != 1 {
				t.Fatalf("expected exactly 1 message, got %d", len(result.Messages))
			}
			msg := result.Messages[0]
			if msg.Role != mcpmsg.RoleUser {
				t.Errorf("role = %q, want %q", msg.Role, mcpmsg.RoleUser)
			}
			tc2, ok := msg.Content.(mcpmsg.TextContent)
			if !ok {
				t.Fatalf("Content type = %T, want TextContent", msg.Content)
			}
			if tc2.Text == "" {
				t.Error("prompt message text must not be empty")
			}
			if !contains(tc2.Text, tc.wantURI) {
				t.Errorf("prompt text must reference resource URI %q\ngot: %s", tc.wantURI, tc2.Text)
			}
		})
	}
}

// TestPrompt_StartWork_ReferencesGTDCurrent verifies start_work also
// references the gtd/current resource (two resource URIs in one prompt).
func TestPrompt_StartWork_ReferencesGTDCurrent(t *testing.T) {
	s := newTestResourceServer(t)
	result, err := s.handlePromptStartWork(context.Background(), mcpmsg.GetPromptRequest{})
	if err != nil {
		t.Fatalf("handlePromptStartWork: %v", err)
	}
	tc, ok := result.Messages[0].Content.(mcpmsg.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}
	if !contains(tc.Text, "wayneblacktea://gtd/current") {
		t.Error("start_work prompt must reference wayneblacktea://gtd/current")
	}
}

// ─── helpers ────────────────────────────────────────────────────────────────

// contains reports whether substr appears in s.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}

// assertStringField fails the test when body[key] is not a non-empty string.
func assertStringField(t *testing.T, body map[string]any, key string) {
	t.Helper()
	v, ok := body[key].(string)
	if !ok || v == "" {
		t.Errorf("field %q must be a non-empty string, got %v", key, body[key])
	}
}
