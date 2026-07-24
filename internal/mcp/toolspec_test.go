package mcp

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
)

// wantTitleRequired is a shared test-assertion constant (goconst): several
// toolspec_test.go/tools_gtd_*_test.go cases assert the exact seam default
// message for a required "title" field. (The analogous "invalid project_id
// UUID" case reuses the existing errMsgInvalidProjectIDUUID from server.go.)
const wantTitleRequired = "title is required"

// ---- registerToolSpec derivation tests ----

func TestRegisterToolSpec_DerivesRequiredEnumMaxLength(t *testing.T) {
	tool := mcp.NewTool(
		"spec_derive_tool",
		mcp.WithString("name", mcp.Required()),
		mcp.WithString("status", mcp.Enum("a", "b", "c")),
		mcp.WithString("title", mcp.MaxLength(10)),
		mcp.WithNumber("count"),
		mcp.WithBoolean("flag"),
	)
	ts := registerToolSpec(tool)

	if !ts.args["name"].required {
		t.Error("name should be derived as required")
	}
	if ts.args["name"].typ != "string" {
		t.Errorf("name type = %q, want string", ts.args["name"].typ)
	}
	if got := ts.args["status"].enum; len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("status enum = %v, want [a b c]", got)
	}
	if ts.args["title"].maxLength != 10 {
		t.Errorf("title maxLength = %d, want 10", ts.args["title"].maxLength)
	}
	if ts.args["count"].typ != "number" {
		t.Errorf("count type = %q, want number", ts.args["count"].typ)
	}
	if ts.args["flag"].typ != "boolean" {
		t.Errorf("flag type = %q, want boolean", ts.args["flag"].typ)
	}
	if ts.args["status"].required {
		t.Error("status should not be required")
	}
}

func TestRegisterToolSpec_RequiredOrderMatchesRegistration(t *testing.T) {
	tool := mcp.NewTool(
		"spec_order_tool",
		mcp.WithString("first", mcp.Required()),
		mcp.WithString("second", mcp.Required()),
		mcp.WithString("third", mcp.Required()),
	)
	ts := registerToolSpec(tool)
	want := []string{"first", "second", "third"}
	if len(ts.requiredOrder) != len(want) {
		t.Fatalf("requiredOrder = %v, want %v", ts.requiredOrder, want)
	}
	for i, name := range want {
		if ts.requiredOrder[i] != name {
			t.Errorf("requiredOrder[%d] = %q, want %q", i, ts.requiredOrder[i], name)
		}
	}
}

// ---- validate() tests ----

func TestValidate_RequiredMissing_DefaultMessage(t *testing.T) {
	tool := mcp.NewTool("spec_val_required", mcp.WithString("title", mcp.Required()))
	ts := registerToolSpec(tool)

	r := ts.validate(map[string]any{})
	if r == nil || !r.IsError {
		t.Fatal("missing required field must error")
	}
	if resultText(r) != wantTitleRequired {
		t.Errorf("message = %q, want %q", resultText(r), wantTitleRequired)
	}
}

func TestValidate_RequiredEmptyString_TreatedAsMissing(t *testing.T) {
	tool := mcp.NewTool("spec_val_required_empty", mcp.WithString("title", mcp.Required()))
	ts := registerToolSpec(tool)

	r := ts.validate(map[string]any{"title": ""})
	if r == nil || !r.IsError {
		t.Fatal("empty required string must error")
	}
	if resultText(r) != wantTitleRequired {
		t.Errorf("message = %q, want %q", resultText(r), wantTitleRequired)
	}
}

func TestValidate_RequiredWrongType_TypeMismatchError(t *testing.T) {
	tool := mcp.NewTool("spec_val_wrong_type", mcp.WithString("title", mcp.Required()))
	ts := registerToolSpec(tool)

	r := ts.validate(map[string]any{"title": float64(5)})
	if r == nil || !r.IsError {
		t.Fatal("wrong JSON type for required string must error")
	}
	if resultText(r) != "title must be a string" {
		t.Errorf("message = %q, want %q", resultText(r), "title must be a string")
	}
}

func TestValidate_RequiredMessageOverride(t *testing.T) {
	tool := mcp.NewTool(
		"spec_val_override",
		mcp.WithString("name", mcp.Required()),
		mcp.WithString("title", mcp.Required()),
	)
	combined := "name and title are required"
	ts := registerToolSpec(tool)
	requiredMsg("name", combined)(ts)
	requiredMsg("title", combined)(ts)

	r := ts.validate(map[string]any{"title": "x"})
	if r == nil || !r.IsError {
		t.Fatal("missing name must error")
	}
	if resultText(r) != combined {
		t.Errorf("message = %q, want %q", resultText(r), combined)
	}
}

func TestValidate_RequiredUUID_DelegatesToRequireUUIDArg(t *testing.T) {
	tool := mcp.NewTool("spec_val_req_uuid", mcp.WithString("task_id", mcp.Required()))
	ts := registerToolSpec(tool)
	uuidArgs("task_id")(ts)

	// Missing → "task_id is required" (requireUUIDArg's own default).
	r := ts.validate(map[string]any{})
	if r == nil || !r.IsError || resultText(r) != "task_id is required" {
		t.Errorf("missing uuid required = %q, want %q", resultText(r), "task_id is required")
	}

	// Malformed → default "invalid task_id UUID".
	r = ts.validate(map[string]any{"task_id": "not-a-uuid"})
	if r == nil || !r.IsError || resultText(r) != "invalid task_id UUID" {
		t.Errorf("malformed uuid required = %q, want %q", resultText(r), "invalid task_id UUID")
	}

	// Valid → nil.
	if r := ts.validate(map[string]any{"task_id": uuid.NewString()}); r != nil {
		t.Errorf("valid required uuid should pass, got: %s", resultText(r))
	}
}

func TestValidate_RequiredUUID_CustomInvalidMessage(t *testing.T) {
	tool := mcp.NewTool("spec_val_req_uuid_custom", mcp.WithString("project_id", mcp.Required()))
	ts := registerToolSpec(tool)
	uuidArgs("project_id")(ts)
	ts.args["project_id"].uuidMsg = "custom invalid project_id message"

	r := ts.validate(map[string]any{"project_id": "bad"})
	if r == nil || resultText(r) != "custom invalid project_id message" {
		t.Errorf("message = %q, want override", resultText(r))
	}
}

func TestValidate_OptionalUUID_EmptySkipsFormatCheck(t *testing.T) {
	tool := mcp.NewTool("spec_val_opt_uuid", mcp.WithString("project_id"))
	ts := registerToolSpec(tool)
	uuidArgs("project_id")(ts)

	if r := ts.validate(map[string]any{}); r != nil {
		t.Errorf("absent optional uuid should pass, got: %s", resultText(r))
	}
	if r := ts.validate(map[string]any{"project_id": ""}); r != nil {
		t.Errorf("empty optional uuid should pass, got: %s", resultText(r))
	}
}

func TestValidate_OptionalUUID_MalformedErrors(t *testing.T) {
	tool := mcp.NewTool("spec_val_opt_uuid_bad", mcp.WithString("project_id"))
	ts := registerToolSpec(tool)
	uuidArgs("project_id")(ts)

	r := ts.validate(map[string]any{"project_id": "not-a-uuid"})
	if r == nil || resultText(r) != errMsgInvalidProjectIDUUID {
		t.Errorf("message = %q, want %q", resultText(r), errMsgInvalidProjectIDUUID)
	}
}

func TestValidate_Enum_ValidAndInvalid(t *testing.T) {
	tool := mcp.NewTool("spec_val_enum", mcp.WithString("status", mcp.Enum("active", "done")))
	ts := registerToolSpec(tool)

	if r := ts.validate(map[string]any{"status": "active"}); r != nil {
		t.Errorf("valid enum value should pass, got: %s", resultText(r))
	}
	r := ts.validate(map[string]any{"status": "bogus"})
	want := "status must be one of: active, done"
	if r == nil || resultText(r) != want {
		t.Errorf("message = %q, want %q", resultText(r), want)
	}
}

func TestValidate_Enum_MessageOverride(t *testing.T) {
	tool := mcp.NewTool("spec_val_enum_override", mcp.WithString("status", mcp.Enum("active", "done")))
	ts := registerToolSpec(tool)
	ts.args["status"].enumMsg = "custom enum message"

	r := ts.validate(map[string]any{"status": "bogus"})
	if r == nil || resultText(r) != "custom enum message" {
		t.Errorf("message = %q, want override", resultText(r))
	}
}

func TestValidate_MaxLength_WithinAndExceeding(t *testing.T) {
	tool := mcp.NewTool("spec_val_maxlen", mcp.WithString("title", mcp.MaxLength(5)))
	ts := registerToolSpec(tool)

	if r := ts.validate(map[string]any{"title": "abcde"}); r != nil {
		t.Errorf("exactly-at-limit should pass, got: %s", resultText(r))
	}
	r := ts.validate(map[string]any{"title": "abcdef"})
	want := "title exceeds 5 characters"
	if r == nil || resultText(r) != want {
		t.Errorf("message = %q, want %q", resultText(r), want)
	}
}

func TestValidate_NoMaxLength_SuppressesEnforcement(t *testing.T) {
	tool := mcp.NewTool("spec_val_maxlen_suppress", mcp.WithString("assignee", mcp.MaxLength(5)))
	ts := registerToolSpec(tool)
	noMaxLength("assignee")(ts)

	if r := ts.validate(map[string]any{"assignee": "way-over-five-chars"}); r != nil {
		t.Errorf("suppressed maxLength should not fire, got: %s", resultText(r))
	}
}

func TestValidate_OptionalFieldAbsent_NoError(t *testing.T) {
	tool := mcp.NewTool(
		"spec_val_optional_absent",
		mcp.WithString("title", mcp.Required()),
		mcp.WithString("description"),
	)
	ts := registerToolSpec(tool)
	if r := ts.validate(map[string]any{"title": "x"}); r != nil {
		t.Errorf("absent optional field should not error, got: %s", resultText(r))
	}
}

// ---- decodeToolArgs() tests ----

type decodeTestArgs struct {
	ID         uuid.UUID  `mcp:"id"`
	OptID      *uuid.UUID `mcp:"opt_id"`
	Name       string     `mcp:"name"`
	OptName    *string    `mcp:"opt_name"`
	Count      int32      `mcp:"count"`
	Importance int16      `mcp:"importance"`
	Flag       bool       `mcp:"flag"`
	Untagged   string
}

func TestDecodeToolArgs_AllKinds(t *testing.T) {
	id := uuid.New()
	optID := uuid.New()
	args := map[string]any{
		"id":         id.String(),
		"opt_id":     optID.String(),
		"name":       "hello",
		"opt_name":   "world",
		"count":      float64(5),
		"importance": float64(2),
		"flag":       true,
	}
	var out decodeTestArgs
	if r := decodeToolArgs(args, &out); r != nil {
		t.Fatalf("decode should succeed, got: %s", resultText(r))
	}
	if out.ID != id {
		t.Errorf("ID = %v, want %v", out.ID, id)
	}
	if out.OptID == nil || *out.OptID != optID {
		t.Errorf("OptID = %v, want %v", out.OptID, optID)
	}
	if out.Name != "hello" {
		t.Errorf("Name = %q", out.Name)
	}
	if out.OptName == nil || *out.OptName != "world" {
		t.Errorf("OptName = %v, want world", out.OptName)
	}
	if out.Count != 5 {
		t.Errorf("Count = %d, want 5", out.Count)
	}
	if out.Importance != 2 {
		t.Errorf("Importance = %d, want 2", out.Importance)
	}
	if !out.Flag {
		t.Error("Flag should be true")
	}
}

func TestDecodeToolArgs_AbsentOptionalFieldsStayNil(t *testing.T) {
	var out decodeTestArgs
	if r := decodeToolArgs(map[string]any{}, &out); r != nil {
		t.Fatalf("decode of empty args should succeed, got: %s", resultText(r))
	}
	if out.OptID != nil {
		t.Errorf("OptID should be nil when absent, got %v", out.OptID)
	}
	if out.OptName != nil {
		t.Errorf("OptName should be nil when absent, got %v", out.OptName)
	}
	if out.Name != "" || out.Count != 0 || out.Flag {
		t.Errorf("non-pointer fields should be zero-valued when absent, got %+v", out)
	}
}

// TestDecodeToolArgs_PresentEmptyString_DistinguishedFromAbsent verifies the
// presence-based pointer semantics needed for preserve-on-omit business logic
// (handleUpdateProject): a key present with an empty-string value MUST still
// populate the pointer (to a pointed-at empty string), distinct from the key
// being entirely absent (nil pointer).
func TestDecodeToolArgs_PresentEmptyString_DistinguishedFromAbsent(t *testing.T) {
	var out decodeTestArgs
	if r := decodeToolArgs(map[string]any{"opt_name": ""}, &out); r != nil {
		t.Fatalf("decode should succeed, got: %s", resultText(r))
	}
	if out.OptName == nil {
		t.Fatal("OptName should be non-nil (present, even if empty)")
	}
	if *out.OptName != "" {
		t.Errorf("OptName = %q, want empty string", *out.OptName)
	}
}

func TestDecodeToolArgs_TypeMismatch_ReturnsClearError(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"string field gets number", map[string]any{"name": float64(1)}, "name must be a string"},
		{"number field gets string", map[string]any{"count": "five"}, "count must be a number"},
		{"bool field gets string", map[string]any{"flag": "true"}, "flag must be a boolean"},
		{"uuid field gets number", map[string]any{"id": float64(1)}, "id must be a string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out decodeTestArgs
			r := decodeToolArgs(tc.args, &out)
			if r == nil || !r.IsError {
				t.Fatalf("expected type-mismatch error, decode silently succeeded")
			}
			if resultText(r) != tc.want {
				t.Errorf("message = %q, want %q", resultText(r), tc.want)
			}
		})
	}
}

func TestDecodeToolArgs_InvalidUUIDString(t *testing.T) {
	var out decodeTestArgs
	r := decodeToolArgs(map[string]any{"id": "not-a-uuid"}, &out)
	if r == nil || resultText(r) != "invalid id UUID" {
		t.Errorf("message = %q, want %q", resultText(r), "invalid id UUID")
	}
}

func TestDecodeToolArgs_UntaggedFieldIgnored(t *testing.T) {
	var out decodeTestArgs
	if r := decodeToolArgs(map[string]any{"Untagged": "should not decode"}, &out); r != nil {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if out.Untagged != "" {
		t.Errorf("Untagged field should never be populated (no mcp tag), got %q", out.Untagged)
	}
}

// ---- seam[T] end-to-end tests ----

type seamTestArgs struct {
	TaskID uuid.UUID `mcp:"task_id"`
	Title  string    `mcp:"title"`
}

func TestSeam_ValidationFailureShortCircuitsHandler(t *testing.T) {
	tool := mcp.NewTool(
		"spec_seam_valfail",
		mcp.WithString("task_id", mcp.Required()),
		mcp.WithString("title", mcp.Required()),
	)
	ts := registerToolSpec(tool)
	uuidArgs("task_id")(ts)

	called := false
	handler := seam("spec_seam_valfail", func(_ context.Context, _ seamTestArgs) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("should not reach here"), nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"title": "x"} // missing task_id
	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected validation error result")
	}
	if called {
		t.Error("business handler must not be called when validation fails")
	}
}

func TestSeam_ValidAndDecodedArgsReachHandler(t *testing.T) {
	tool := mcp.NewTool(
		"spec_seam_ok",
		mcp.WithString("task_id", mcp.Required()),
		mcp.WithString("title", mcp.Required()),
	)
	ts := registerToolSpec(tool)
	uuidArgs("task_id")(ts)

	id := uuid.New()
	var gotArgs seamTestArgs
	handler := seam("spec_seam_ok", func(_ context.Context, args seamTestArgs) (*mcp.CallToolResult, error) {
		gotArgs = args
		return mcp.NewToolResultText("ok"), nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"task_id": id.String(), "title": "hello"}
	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got: %s", resultText(res))
	}
	if gotArgs.TaskID != id {
		t.Errorf("TaskID = %v, want %v", gotArgs.TaskID, id)
	}
	if gotArgs.Title != "hello" {
		t.Errorf("Title = %q, want hello", gotArgs.Title)
	}
}
