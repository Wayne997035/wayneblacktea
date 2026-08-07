package mcp

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// argSpec holds derived validation metadata for one MCP tool argument. The
// required/type/enum/maxLength fields are derived from the tool's existing
// mcp.NewTool() registration (see registerToolSpec) — never hand-duplicated.
// isUUID is a separate, lightweight annotation layered on top (see
// uuidArgs): mcp-go's Pattern() option would express this in the advertised
// JSON schema, but changing the schema is out of scope for this seam (ADR
// 0001 non-goal), so UUID-ness is tracked out-of-band instead.
type argSpec struct {
	name      string
	required  bool
	typ       string // "string" | "number" | "boolean" (from schema "type")
	enum      []string
	maxLength int
	isUUID    bool

	// Optional per-arg message overrides, applied verbatim in place of the
	// seam's default-formatted message. Empty means "use the default".
	requiredMsg string
	uuidMsg     string
	enumMsg     string
	maxLenMsg   string
}

// toolSpec is the derived+cached validation metadata for one registered MCP
// tool, built once at registration time from its mcp.NewTool() call.
type toolSpec struct {
	args map[string]*argSpec
	// requiredOrder preserves mcp.NewTool()'s registration call order (from
	// tool.InputSchema.Required), so required-field checks fail in the same
	// order the original handlers checked them field-by-field.
	requiredOrder []string
	// orderedNames is a deterministic (alpha-sorted) iteration order for the
	// enum/maxLength/uuid constraint pass, independent of Go map iteration.
	orderedNames []string
}

var (
	toolSpecMu       sync.RWMutex
	toolSpecRegistry = map[string]*toolSpec{}
)

// registerToolSpec derives a toolSpec from tool's own InputSchema (already
// populated by whatever mcp.WithString/mcp.Required/mcp.Enum/mcp.MaxLength
// options the caller passed to mcp.NewTool) and caches it under tool.Name.
// This is the single point where validation metadata is read out of the
// registration — callers never hand-write a parallel declaration.
func registerToolSpec(tool mcp.Tool) *toolSpec {
	ts := &toolSpec{args: map[string]*argSpec{}}

	for name, raw := range tool.InputSchema.Properties {
		propMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		a := &argSpec{name: name}
		if t, ok := propMap["type"].(string); ok {
			a.typ = t
		}
		if enumRaw, ok := propMap["enum"].([]string); ok {
			a.enum = enumRaw
		}
		if ml, ok := propMap["maxLength"].(int); ok {
			a.maxLength = ml
		}
		ts.args[name] = a
		ts.orderedNames = append(ts.orderedNames, name)
	}
	sort.Strings(ts.orderedNames)

	ts.requiredOrder = append([]string(nil), tool.InputSchema.Required...)
	for _, name := range ts.requiredOrder {
		if a, ok := ts.args[name]; ok {
			a.required = true
		}
	}

	toolSpecMu.Lock()
	toolSpecRegistry[tool.Name] = ts
	toolSpecMu.Unlock()
	return ts
}

// specFor returns the cached toolSpec for toolName. Panics if the tool was
// never registered via registerToolSpec — a programming error (a seam-wrapped
// handler with no matching addTool call), not a runtime/user condition.
func specFor(toolName string) *toolSpec {
	toolSpecMu.RLock()
	defer toolSpecMu.RUnlock()
	ts, ok := toolSpecRegistry[toolName]
	if !ok {
		panic("mcp: no toolSpec registered for " + toolName + " (missing addTool call?)")
	}
	return ts
}

// specOption customises a toolSpec immediately after registerToolSpec derives
// it from the tool's registration. Applied by addTool.
type specOption func(*toolSpec)

// uuidArgs marks the named arguments as UUID-formatted. Independent of the
// mcp.NewTool() registration (see argSpec doc) — only affects the seam's own
// validate() pass, never the advertised JSON schema.
func uuidArgs(names ...string) specOption {
	return func(ts *toolSpec) {
		for _, n := range names {
			if a, ok := ts.args[n]; ok {
				a.isUUID = true
			}
		}
	}
}

// requiredMsg overrides the default "<name> is required" message for a
// specific argument. Used where several arguments share one legacy combined
// message (e.g. create_project's "name, title and area are required").
func requiredMsg(name, msg string) specOption {
	return func(ts *toolSpec) {
		if a, ok := ts.args[name]; ok {
			a.requiredMsg = msg
		}
	}
}

// noMaxLength suppresses automatic maxLength enforcement for an argument that
// declares mcp.MaxLength() purely as a client-side schema hint, not a
// server-enforced constraint (e.g. assignee — see the doc comment on its
// registration in tools_gtd.go: NormalizeActor's allowlist is the real
// validation authority regardless of length).
func noMaxLength(names ...string) specOption {
	return func(ts *toolSpec) {
		for _, n := range names {
			if a, ok := ts.args[n]; ok {
				a.maxLength = 0
			}
		}
	}
}

// addTool derives+caches tool's toolSpec (applying opts), then registers it
// on ms exactly as ms.AddTool would. The mcp.NewTool() call itself (tool) is
// never altered by this wrapper — only the internal validation cache and the
// registration call site change shape.
func (s *Server) addTool(ms *server.MCPServer, tool mcp.Tool, handler server.ToolHandlerFunc, opts ...specOption) {
	ts := registerToolSpec(tool)
	for _, opt := range opts {
		opt(ts)
	}
	ms.AddTool(tool, handler)
}

// seam adapts a typed handler function into a server.ToolHandlerFunc: it
// validates the raw MCP arguments against toolName's cached toolSpec, decodes
// them into a T, and only then calls fn. Validation/decode failures never
// reach fn — it always receives a fully-populated, already-valid T.
func seam[T any](toolName string, fn func(ctx context.Context, args T) (*mcp.CallToolResult, error)) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		ts := specFor(toolName)

		if errResult := ts.validate(args); errResult != nil {
			return errResult, nil
		}
		var parsed T
		if errResult := decodeToolArgs(args, &parsed); errResult != nil {
			return errResult, nil
		}
		return fn(ctx, parsed)
	}
}

// validate runs the seam's two-pass boilerplate validation over raw MCP args:
// pass A checks required-field presence (in registration order, delegating
// UUID-typed required fields to requireUUIDArg so its exact "X is required" /
// invalidMsg behaviour is reused rather than reimplemented — see ADR 0001);
// pass B checks enum/maxLength/UUID-format constraints on every present,
// non-empty string argument (alpha order, deterministic). Returns nil when
// every constraint passes.
func (ts *toolSpec) validate(args map[string]any) *mcp.CallToolResult {
	if errResult := ts.validateRequired(args); errResult != nil {
		return errResult
	}
	return ts.validateConstraints(args)
}

// validateRequired is validate()'s pass A: required-field presence, in
// registration order. UUID-typed required fields delegate to requireUUIDArg
// (ADR 0001 decision 3); everything else goes through checkRequiredPresent.
func (ts *toolSpec) validateRequired(args map[string]any) *mcp.CallToolResult {
	for _, name := range ts.requiredOrder {
		a := ts.args[name]
		if a.isUUID {
			invalidMsg := a.uuidMsg
			if invalidMsg == "" {
				invalidMsg = "invalid " + name + " UUID"
			}
			if _, errResult := requireUUIDArg(args, name, invalidMsg); errResult != nil {
				return errResult
			}
			continue
		}
		if errResult := checkRequiredPresent(args, a); errResult != nil {
			return errResult
		}
	}
	return nil
}

// validateConstraints is validate()'s pass B: enum/maxLength/UUID-format
// checks on every present, non-empty string argument (alpha order).
func (ts *toolSpec) validateConstraints(args map[string]any) *mcp.CallToolResult {
	for _, name := range ts.orderedNames {
		a := ts.args[name]
		raw, present := args[name]
		if !present {
			continue
		}
		sv, isStr := raw.(string)
		if !isStr || sv == "" {
			continue
		}
		if errResult := checkArgConstraints(name, sv, a); errResult != nil {
			return errResult
		}
	}
	return nil
}

// checkArgConstraints applies a's uuid/enum/maxLength constraints (in that
// order) to a single present, non-empty string value sv.
func checkArgConstraints(name, sv string, a *argSpec) *mcp.CallToolResult {
	if a.isUUID {
		if _, err := uuid.Parse(sv); err != nil {
			msg := a.uuidMsg
			if msg == "" {
				msg = "invalid " + name + " UUID"
			}
			return mcp.NewToolResultError(msg)
		}
	}
	if len(a.enum) > 0 && !containsString(a.enum, sv) {
		msg := a.enumMsg
		if msg == "" {
			msg = name + " must be one of: " + strings.Join(a.enum, ", ")
		}
		return mcp.NewToolResultError(msg)
	}
	if a.maxLength > 0 && len([]rune(sv)) > a.maxLength {
		msg := a.maxLenMsg
		if msg == "" {
			msg = fmt.Sprintf("%s exceeds %d characters", name, a.maxLength)
		}
		return mcp.NewToolResultError(msg)
	}
	return nil
}

// checkRequiredPresent validates presence (and, for strings, non-emptiness —
// matching every legacy `if x == "" { return "x is required" }` check this
// seam replaces) plus basic JSON-type correctness for a single required,
// non-UUID argument.
//
// Non-emptiness is checked against the trimmed value (strings.TrimSpace), not
// the raw value: a whitespace-only string like "   " is not "provided" in any
// meaningful sense and previously slipped past this seam's `sv == ""` check
// while the HTTP layer's equivalent handlers (CreateGoal, UpdateGoal,
// UpdateProject, …) already trimmed first — sprint 8-7 gap F. The stored
// value itself is untouched (decodeToolArgs still decodes the raw string);
// only the required/empty decision uses the trimmed form.
func checkRequiredPresent(args map[string]any, a *argSpec) *mcp.CallToolResult {
	raw, present := args[a.name]
	empty := !present
	if present {
		switch a.typ {
		case "string":
			sv, ok := raw.(string)
			if !ok {
				return mcp.NewToolResultError(a.name + " must be a string")
			}
			empty = strings.TrimSpace(sv) == ""
		case "boolean":
			if _, ok := raw.(bool); !ok {
				return mcp.NewToolResultError(a.name + " must be a boolean")
			}
		case "number":
			if _, ok := raw.(float64); !ok {
				return mcp.NewToolResultError(a.name + " must be a number")
			}
		}
	}
	if empty {
		msg := a.requiredMsg
		if msg == "" {
			msg = a.name + " is required"
		}
		return mcp.NewToolResultError(msg)
	}
	return nil
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

var uuidType = reflect.TypeOf(uuid.UUID{})

// decodeToolArgs populates out (a pointer to a struct) from args by walking
// its fields via reflection. Each field tagged `mcp:"<arg name>"` is decoded
// according to its Go type:
//
//   - string / bool / int32 / int16: zero value when args[key] is absent
//     (matching the legacy stringArg/numberArg/boolArg getters this seam
//     replaces); the raw arg value otherwise.
//   - *string / *uuid.UUID: nil when args[key] is absent from the map at
//     all (presence-based — needed for preserve-on-omit business logic like
//     handleUpdateProject); populated when the key is present, even with a
//     zero value.
//   - uuid.UUID: the seam only ever decodes this for fields the caller has
//     already validated as required+UUID via validate(), so args[key] is
//     guaranteed present and well-formed; decode still checks defensively
//     rather than trusting that invariant silently.
//
// Any JSON-type mismatch (e.g. a number arg holding a string) returns a
// clear tool error instead of silently decoding a zero value — the decode
// step is the last line of defence against a malformed/adversarial payload
// reaching handler business logic with a silently-wrong field.
func decodeToolArgs[T any](args map[string]any, out *T) *mcp.CallToolResult {
	v := reflect.ValueOf(out).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		key := field.Tag.Get("mcp")
		if key == "" {
			continue
		}
		if errResult := decodeField(args, key, v.Field(i)); errResult != nil {
			return errResult
		}
	}
	return nil
}

// decodeField decodes a single struct field from args[key] per fv's Go type.
func decodeField(args map[string]any, key string, fv reflect.Value) *mcp.CallToolResult {
	raw, present := args[key]
	if !present {
		return nil
	}

	switch {
	case fv.Type() == uuidType:
		return decodeUUIDField(key, raw, fv, false)
	case fv.Kind() == reflect.Ptr && fv.Type().Elem() == uuidType:
		return decodeUUIDField(key, raw, fv, true)
	case fv.Kind() == reflect.String:
		return decodeStringField(key, raw, fv, false)
	case fv.Kind() == reflect.Ptr && fv.Type().Elem().Kind() == reflect.String:
		return decodeStringField(key, raw, fv, true)
	case fv.Kind() == reflect.Bool:
		return decodeBoolField(key, raw, fv, false)
	case fv.Kind() == reflect.Ptr && fv.Type().Elem().Kind() == reflect.Bool:
		return decodeBoolField(key, raw, fv, true)
	case fv.Kind() == reflect.Int32, fv.Kind() == reflect.Int16, fv.Kind() == reflect.Int:
		return decodeIntField(key, raw, fv)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("internal: unsupported arg field type for %s", key))
	}
}

// decodeUUIDField decodes a uuid.UUID (ptr=false) or *uuid.UUID (ptr=true)
// field. For ptr fields, an empty string is a no-op (leaves the pointer nil)
// rather than a parse error — matching every optional-UUID call site's
// existing `if raw != "" { ... }` guard.
func decodeUUIDField(key string, raw any, fv reflect.Value, ptr bool) *mcp.CallToolResult {
	sv, ok := raw.(string)
	if !ok {
		return mcp.NewToolResultError(key + " must be a string")
	}
	if ptr && sv == "" {
		return nil
	}
	id, err := uuid.Parse(sv)
	if err != nil {
		return mcp.NewToolResultError("invalid " + key + " UUID")
	}
	if ptr {
		fv.Set(reflect.ValueOf(&id))
	} else {
		fv.Set(reflect.ValueOf(id))
	}
	return nil
}

// decodeStringField decodes a string (ptr=false) or *string (ptr=true)
// field. The ptr form always sets the pointer when the key is present, even
// to an empty string — presence-based semantics for preserve-on-omit fields.
func decodeStringField(key string, raw any, fv reflect.Value, ptr bool) *mcp.CallToolResult {
	sv, ok := raw.(string)
	if !ok {
		return mcp.NewToolResultError(key + " must be a string")
	}
	if ptr {
		fv.Set(reflect.ValueOf(&sv))
	} else {
		fv.SetString(sv)
	}
	return nil
}

// decodeBoolField decodes a bool (ptr=false) or *bool (ptr=true) field.
func decodeBoolField(key string, raw any, fv reflect.Value, ptr bool) *mcp.CallToolResult {
	bv, ok := raw.(bool)
	if !ok {
		return mcp.NewToolResultError(key + " must be a boolean")
	}
	if ptr {
		fv.Set(reflect.ValueOf(&bv))
	} else {
		fv.SetBool(bv)
	}
	return nil
}

// decodeIntField decodes a JSON number into an int/int16/int32 field.
func decodeIntField(key string, raw any, fv reflect.Value) *mcp.CallToolResult {
	nv, ok := raw.(float64)
	if !ok {
		return mcp.NewToolResultError(key + " must be a number")
	}
	fv.SetInt(int64(nv))
	return nil
}
