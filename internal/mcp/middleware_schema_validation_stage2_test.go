package mcp

// Stage-2 supplements for the MCP-6 schema validation middleware: two spec
// behaviours the stage-1 named tests do not reach. No t.Parallel (the stage-1
// file shares the process-global schemaValidationExempt map).

import (
	"encoding/json"
	"testing"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// TestDeriveToolSpec_RejectsUnreadable [F1127-02] [F1127-03]: a readable
// schema derives; each of the seven unreadable shapes (spec Target 3) is an
// error on its own, with every other field valid.
func TestDeriveToolSpec_RejectsUnreadable(t *testing.T) {
	mutate := func(fn func(*mcpmsg.Tool)) mcpmsg.Tool {
		tool := mcpmsg.NewTool("zz_derive_tool",
			mcpmsg.WithString("name", mcpmsg.Required()),
			mcpmsg.WithString("mode", mcpmsg.Enum("a", "b")),
		)
		fn(&tool)
		return tool
	}
	prop := func(tool *mcpmsg.Tool, name string) map[string]any {
		p, ok := tool.InputSchema.Properties[name].(map[string]any)
		if !ok {
			t.Fatalf("test setup: property %q is %T", name, tool.InputSchema.Properties[name])
		}
		return p
	}

	tests := []struct {
		name    string
		tool    mcpmsg.Tool
		wantErr bool
	}{
		{"positive control: readable schema", mcpmsg.NewTool("zz_ok_tool",
			mcpmsg.WithString("title", mcpmsg.Required())), false},
		{"input schema type empty", mutate(func(tl *mcpmsg.Tool) { tl.InputSchema.Type = "" }), true},
		{"property is not a map", mutate(func(tl *mcpmsg.Tool) { tl.InputSchema.Properties["mode"] = "oops" }), true},
		{"property has no type", mutate(func(tl *mcpmsg.Tool) { delete(prop(tl, "mode"), "type") }), true},
		{"type outside the supported set", mutate(func(tl *mcpmsg.Tool) { prop(tl, "mode")["type"] = "integer" }), true},
		{"enum of unsupported type", mutate(func(tl *mcpmsg.Tool) { prop(tl, "mode")["enum"] = []int{1} }), true},
		{"required name not in properties", mutate(func(tl *mcpmsg.Tool) {
			tl.InputSchema.Required = append(tl.InputSchema.Required, "ghost")
		}), true},
		{"raw input schema", mcpmsg.NewTool("zz_raw_tool",
			mcpmsg.WithRawInputSchema(json.RawMessage(`{"type":"object","properties":{}}`))), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := deriveToolSpec(tc.tool)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("deriveToolSpec returned nil error, want an error (spec %+v)", spec)
				}
				return
			}
			if err != nil {
				t.Fatalf("deriveToolSpec returned error %v, want nil", err)
			}
			if spec == nil || spec.args["title"] == nil || !spec.args["title"].required {
				t.Fatalf("derived spec lacks the required string arg title: %+v", spec)
			}
		})
	}
}

// TestSchemaValidation_NullRequired [F1127-04]: JSON null on a required field
// is "missing" (spec Target 5a), not a pass-through.
func TestSchemaValidation_NullRequired(t *testing.T) {
	_, ms := newTestMCPServer(t)
	n := schemaValStubs(t, ms, "add_procedural")["add_procedural"]

	isErr, text, calls := schemaValCall(t, ms, n, "add_procedural", `{"title":null,"when_to_use":"w"}`)
	if !isErr || calls != 0 {
		t.Errorf("null title: want rejected with handler not run, got isError=%v calls=%d text=%q", isErr, calls, text)
	}
	const wantPrefix = "title is required ["
	if len(text) < len(wantPrefix) || text[:len(wantPrefix)] != wantPrefix {
		t.Errorf("null title: text = %q, want prefix %q", text, wantPrefix)
	}
	// Negative: the same call with a real title reaches the handler.
	schemaValWantReaches(t, ms, n, "add_procedural", `{"title":"t","when_to_use":"w"}`)
}
