package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/arch"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// recordingArchStore captures the arch.UpsertParams passed to
// UpsertSnapshot so handler-level tests can assert on the args-map ->
// UpsertParams translation (presence -> *string / *map[string]string)
// without a real store. fakeArchStore (boundary_markers_test.go) can't do
// this: it's a value type that discards its UpsertSnapshot argument and
// just echoes back a fixed snapshot/error, which is enough for
// get_project_arch tests but not for asserting what upsert_project_arch
// actually built from a given args map. GetSnapshot is unused by these
// tests and always returns arch.ErrNotFound.
type recordingArchStore struct {
	upserted *arch.UpsertParams
	snap     *arch.Snapshot
	err      error
}

var _ arch.StoreIface = (*recordingArchStore)(nil)

func (s *recordingArchStore) UpsertSnapshot(_ context.Context, p arch.UpsertParams) (*arch.Snapshot, error) {
	s.upserted = &p
	if s.err != nil {
		return nil, s.err
	}
	if s.snap != nil {
		return s.snap, nil
	}
	return &arch.Snapshot{ID: "1", Slug: p.Slug}, nil
}

func (s *recordingArchStore) GetSnapshot(context.Context, string) (*arch.Snapshot, error) {
	return nil, arch.ErrNotFound
}

// callUpsertProjectArch invokes handleUpsertProjectArch directly with the
// given MCP args map. This bypasses wire JSON, but req.GetArguments() just
// type-asserts Params.Arguments to map[string]any (mcp-go@v0.49.0
// tools.go:69), so setting it directly is equivalent — including for a
// JSON `null` value, which decodes to a present map key with a nil
// interface{} value, exactly what map[string]any{"file_map": nil} is.
func callUpsertProjectArch(t *testing.T, srv *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := srv.handleUpsertProjectArch(context.Background(), req)
	if err != nil {
		t.Fatalf("handleUpsertProjectArch: %v", err)
	}
	return result
}

// --- one-軍: handler-layer file_map four-state coverage ------------------
//
// internal/arch/store_postgres_test.go and internal/storage/sqlite/arch_test.go
// already pin the *store* layer's file_map patch semantics (three-state
// matrix, security review PR #157 M-3). None of that exercised the
// *handler*: every existing test built arch.UpsertParams directly, skipping
// the args["file_map"] presence extraction in tools_arch.go entirely (PR
// #157 round 2 reviewer finding). These four tests feed
// handleUpsertProjectArch the raw MCP args map instead, covering the fourth
// state (JSON-null, which the store-layer type can't even express) too.

func TestHandleUpsertProjectArch_FileMap_Absent(t *testing.T) {
	store := &recordingArchStore{}
	srv := &Server{arch: store}

	result := callUpsertProjectArch(t, srv, map[string]any{"slug": "repo", "summary": "s"})
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result.Content)
	}
	if store.upserted == nil {
		t.Fatal("UpsertSnapshot was not called")
	}
	if store.upserted.FileMap != nil {
		t.Errorf("file_map absent from args must translate to nil FileMap (preserve stored value), got %v", *store.upserted.FileMap)
	}
}

func TestHandleUpsertProjectArch_FileMap_JSONNull(t *testing.T) {
	store := &recordingArchStore{}
	srv := &Server{arch: store}

	result := callUpsertProjectArch(t, srv, map[string]any{"slug": "repo", "summary": "s", "file_map": nil})
	if !result.IsError {
		t.Fatalf("expected an error result for JSON-null file_map, got success: %+v", result.Content)
	}
	if store.upserted != nil {
		t.Errorf("store must not be called when file_map fails type validation, got %+v", store.upserted)
	}
}

func TestHandleUpsertProjectArch_FileMap_EmptyString(t *testing.T) {
	store := &recordingArchStore{}
	srv := &Server{arch: store}

	result := callUpsertProjectArch(t, srv, map[string]any{"slug": "repo", "summary": "s", "file_map": ""})
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result.Content)
	}
	if store.upserted == nil || store.upserted.FileMap == nil {
		t.Fatal(`file_map="" must translate to a non-nil, explicit-clear FileMap pointer`)
	}
	if len(*store.upserted.FileMap) != 0 {
		t.Errorf(`file_map="" must translate to an empty map, got %v`, *store.upserted.FileMap)
	}
}

func TestHandleUpsertProjectArch_FileMap_Content(t *testing.T) {
	store := &recordingArchStore{}
	srv := &Server{arch: store}

	result := callUpsertProjectArch(t, srv, map[string]any{
		"slug": "repo", "summary": "s",
		"file_map": `{"main.go":"entrypoint"}`,
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result.Content)
	}
	if store.upserted == nil || store.upserted.FileMap == nil {
		t.Fatal("file_map with content must translate to a non-nil FileMap pointer")
	}
	if got := (*store.upserted.FileMap)["main.go"]; got != "entrypoint" {
		t.Errorf(`file_map["main.go"] = %q, want %q`, got, "entrypoint")
	}
}

// --- Lead 裁決: summary / last_commit_sha presence translation -----------
//
// Store-layer semantics (preserve/clear/replace) are pinned in
// internal/arch/store_postgres_test.go and internal/storage/sqlite/arch_test.go;
// these prove the *handler* builds the right *string for the store to act
// on, mirroring the file_map coverage above.

func TestHandleUpsertProjectArch_Summary_AbsentTranslatesToNil(t *testing.T) {
	store := &recordingArchStore{}
	srv := &Server{arch: store}

	result := callUpsertProjectArch(t, srv, map[string]any{"slug": "repo", "last_commit_sha": "abc"})
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result.Content)
	}
	if store.upserted.Summary != nil {
		t.Errorf("summary absent from args must translate to nil (preserve), got %q", *store.upserted.Summary)
	}
}

func TestHandleUpsertProjectArch_Summary_EmptyStringIsExplicit(t *testing.T) {
	store := &recordingArchStore{}
	srv := &Server{arch: store}

	result := callUpsertProjectArch(t, srv, map[string]any{"slug": "repo", "summary": ""})
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result.Content)
	}
	if store.upserted.Summary == nil || *store.upserted.Summary != "" {
		t.Errorf(`summary="" must translate to a non-nil pointer to "" (explicit clear), got %v`, store.upserted.Summary)
	}
}

func TestHandleUpsertProjectArch_Summary_TooLongStillRejected(t *testing.T) {
	// Regression guard: moving summary from a plain-string required check to
	// pointer-based presence extraction must not accidentally drop the
	// maxSummaryLen bound.
	store := &recordingArchStore{}
	srv := &Server{arch: store}

	result := callUpsertProjectArch(t, srv, map[string]any{
		"slug":    "repo",
		"summary": strings.Repeat("a", maxSummaryLen+1),
	})
	if !result.IsError {
		t.Fatalf("expected an error result for over-length summary, got success: %+v", result.Content)
	}
	if store.upserted != nil {
		t.Errorf("store must not be called when summary fails length validation, got %+v", store.upserted)
	}
}

func TestHandleUpsertProjectArch_LastCommitSHA_AbsentTranslatesToNil(t *testing.T) {
	store := &recordingArchStore{}
	srv := &Server{arch: store}

	result := callUpsertProjectArch(t, srv, map[string]any{"slug": "repo", "summary": "s"})
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result.Content)
	}
	if store.upserted.LastCommitSHA != nil {
		t.Errorf("last_commit_sha absent from args must translate to nil (preserve), got %q", *store.upserted.LastCommitSHA)
	}
}

func TestHandleUpsertProjectArch_LastCommitSHA_EmptyStringIsExplicit(t *testing.T) {
	store := &recordingArchStore{}
	srv := &Server{arch: store}

	result := callUpsertProjectArch(t, srv, map[string]any{"slug": "repo", "summary": "s", "last_commit_sha": ""})
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result.Content)
	}
	if store.upserted.LastCommitSHA == nil || *store.upserted.LastCommitSHA != "" {
		t.Errorf(`last_commit_sha="" must translate to a non-nil pointer to "" (explicit clear), got %v`, store.upserted.LastCommitSHA)
	}
}

func TestHandleUpsertProjectArch_LastCommitSHA_JSONNullRejected(t *testing.T) {
	store := &recordingArchStore{}
	srv := &Server{arch: store}

	result := callUpsertProjectArch(t, srv, map[string]any{"slug": "repo", "summary": "s", "last_commit_sha": nil})
	if !result.IsError {
		t.Fatalf("expected an error result for JSON-null last_commit_sha, got success: %+v", result.Content)
	}
	if store.upserted != nil {
		t.Errorf("store must not be called when last_commit_sha fails type validation, got %+v", store.upserted)
	}
}

// --- optionalStringArg direct unit coverage -------------------------------

func TestOptionalStringArg(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		wantNil   bool
		wantVal   string
		wantError bool
	}{
		{name: "absent", args: map[string]any{}, wantNil: true},
		{name: "json_null", args: map[string]any{"summary": nil}, wantError: true},
		{name: "empty_string", args: map[string]any{"summary": ""}, wantVal: ""},
		{name: "content", args: map[string]any{"summary": "hello"}, wantVal: "hello"},
		{name: "wrong_type_number", args: map[string]any{"summary": 42.0}, wantError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, errResult := optionalStringArg(tc.args, "summary")
			if tc.wantError {
				if errResult == nil {
					t.Fatalf("expected error result, got nil (value=%v)", got)
				}
				return
			}
			if errResult != nil {
				t.Fatalf("unexpected error result: %+v", errResult.Content)
			}
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil (absent), got %q", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected non-nil pointer to %q, got nil", tc.wantVal)
			}
			if *got != tc.wantVal {
				t.Errorf("got %q, want %q", *got, tc.wantVal)
			}
		})
	}
}

// --- m-R3: tool description documents all three states -------------------
//
// Round 2 reviewer finding: the file_map description covered "omit" and
// "{}" but never mentioned "" as an equally valid explicit-clear value.
// Round 3 unifies patch semantics across all three fields, so all three
// descriptions must now spell out all three states.
func TestUpsertProjectArchToolDescriptions_ThreeStates(t *testing.T) {
	ms := server.NewMCPServer("test", "0.0.0")
	srv := &Server{}
	srv.registerArchTools(ms)

	tools := ms.ListTools()
	tool, ok := tools["upsert_project_arch"]
	if !ok {
		t.Fatal("upsert_project_arch not registered")
	}

	for _, field := range []string{"summary", "file_map", "last_commit_sha"} {
		prop, ok := tool.Tool.InputSchema.Properties[field].(map[string]any)
		if !ok {
			t.Fatalf("property %q missing from InputSchema or not a schema object: %v", field, tool.Tool.InputSchema.Properties[field])
		}
		desc, _ := prop["description"].(string)
		lower := strings.ToLower(desc)
		if !strings.Contains(lower, "omit") {
			t.Errorf("%s description missing the omit/absent-state wording: %q", field, desc)
		}
		if !strings.Contains(desc, `""`) {
			t.Errorf(`%s description missing the empty-string ("") state: %q`, field, desc)
		}
		if !strings.Contains(lower, "replace") {
			t.Errorf("%s description missing the replace/value-state wording: %q", field, desc)
		}
	}
}
