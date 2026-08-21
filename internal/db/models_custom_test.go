package db_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestPendingProposalMarshalJSON_HidesPgxWrappers is the core defense-in-depth
// guarantee: serialising a raw db.PendingProposal MUST NOT leak any
// pgtype.{Text,UUID,Timestamptz} internal wrapper keys ("String", "Bytes",
// "Valid", "Time", "InfinityModifier") into the wire output. The presence of
// these keys would indicate that a future caller bypassed handler.toResponse()
// and emitted the raw model — which we want to make safe at the type layer.
func TestPendingProposalMarshalJSON_HidesPgxWrappers(t *testing.T) {
	id := uuid.New()
	wsID := uuid.New()
	created := time.Date(2026, 5, 19, 12, 30, 0, 0, time.UTC)
	resolved := time.Date(2026, 5, 19, 14, 0, 0, 0, time.UTC)

	row := db.PendingProposal{
		ID:          id,
		WorkspaceID: pgtype.UUID{Bytes: [16]byte(wsID), Valid: true},
		Type:        "concept",
		Payload:     []byte(`{"title":"x"}`),
		Status:      "rejected",
		ProposedBy:  pgtype.Text{String: "auto-mcp", Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: created, Valid: true},
		ResolvedAt:  pgtype.Timestamptz{Time: resolved, Valid: true},
		Reason:      pgtype.Text{String: "ttl-expired-30d", Valid: true},
	}

	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("MarshalJSON returned error: %v", err)
	}

	// Spot-check that no pgx wrapper key leaked into the wire JSON.
	// Use substring check because the JSON keys would be capitalised and the
	// raw field would be visible.
	for _, leak := range []string{`"String"`, `"Bytes"`, `"Valid"`, `"InfinityModifier"`, `"Time":"`} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("output leaks pgx wrapper key %q: %s", leak, raw)
		}
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("output not valid JSON: %v (raw: %s)", err, raw)
	}

	// Verify each field came through with the right plain-Go shape.
	if got := decoded["id"]; got != id.String() {
		t.Errorf("id = %v, want %q", got, id.String())
	}
	if got := decoded["workspace_id"]; got != wsID.String() {
		t.Errorf("workspace_id = %v, want %q", got, wsID.String())
	}
	if got := decoded["type"]; got != "concept" {
		t.Errorf("type = %v, want concept", got)
	}
	if got := decoded["status"]; got != "rejected" {
		t.Errorf("status = %v, want rejected", got)
	}
	if got := decoded["proposed_by"]; got != "auto-mcp" {
		t.Errorf("proposed_by = %v, want auto-mcp", got)
	}
	if got := decoded["reason"]; got != "ttl-expired-30d" {
		t.Errorf("reason = %v, want ttl-expired-30d", got)
	}
	// Time format mirrors handler.toResponse() — RFC3339 with second precision.
	if got := decoded["created_at"]; got != "2026-05-19T12:30:00Z" {
		t.Errorf("created_at = %v, want 2026-05-19T12:30:00Z", got)
	}
	if got := decoded["resolved_at"]; got != "2026-05-19T14:00:00Z" {
		t.Errorf("resolved_at = %v, want 2026-05-19T14:00:00Z", got)
	}
}

// TestPendingProposalMarshalJSON_OmitsNilFields verifies how NULL pgtype
// columns are emitted: workspace_id / resolved_at / reason are omitted (the
// frontend distinguishes "field absent" from "field present but null"),
// while proposed_by is ALWAYS present (emitted as JSON null when invalid) to
// match handler.pendingProposalResponse — see proposal_handler.go:138 where
// the field carries no omitempty tag. Keeping the two writers congruent so
// any direct c.JSON(prop) produces the same shape as the handler path
// (PR #123 / round-2 reviewer 🟡 consistency).
func TestPendingProposalMarshalJSON_OmitsNilFields(t *testing.T) {
	row := db.PendingProposal{
		ID:          uuid.New(),
		WorkspaceID: pgtype.UUID{Valid: false},
		Type:        "concept",
		Payload:     []byte(`{}`),
		Status:      "rejected",
		ProposedBy:  pgtype.Text{Valid: false},
		CreatedAt:   pgtype.Timestamptz{Valid: false},
		ResolvedAt:  pgtype.Timestamptz{Valid: false},
		Reason:      pgtype.Text{Valid: false},
	}

	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("MarshalJSON returned error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("output not valid JSON: %v (raw: %s)", err, raw)
	}

	// workspace_id / resolved_at / reason remain omitempty.
	for _, key := range []string{"workspace_id", "resolved_at", "reason"} {
		if _, present := decoded[key]; present {
			t.Errorf("key %q must be omitted when underlying pgtype is invalid (raw: %s)", key, raw)
		}
	}

	// proposed_by is ALWAYS present — null when invalid, string when valid.
	// Aligns with handler.pendingProposalResponse JSON tag (no omitempty).
	pb, present := decoded["proposed_by"]
	if !present {
		t.Errorf("proposed_by must be present (as null) when invalid, got omitted (raw: %s)", raw)
	}
	if pb != nil {
		t.Errorf("proposed_by = %v, want JSON null when pgtype invalid (raw: %s)", pb, raw)
	}

	// CreatedAt has no omitempty (matches handler.pendingProposalResponse),
	// so an invalid timestamp emits an empty string. That's existing
	// contract — assert on it so any future refactor changing the shape
	// surfaces here.
	if got, ok := decoded["created_at"].(string); !ok || got != "" {
		t.Errorf("created_at = %v (ok=%v), want \"\"", got, ok)
	}
}

// TestPendingProposalMarshalJSON_RawMessagePayload guarantees the payload
// bytes are emitted as nested JSON (not base64-encoded as Go's default for
// []byte). This matches the handler-layer pendingProposalResponse contract,
// where Payload is json.RawMessage and clients parse it as a JSON object.
func TestPendingProposalMarshalJSON_RawMessagePayload(t *testing.T) {
	cases := []struct {
		name        string
		payload     []byte
		wantPayload string // exact JSON value inside the output's "payload" key
	}{
		{
			name:        "object payload inlined",
			payload:     []byte(`{"title":"hello","tags":["a","b"]}`),
			wantPayload: `{"title":"hello","tags":["a","b"]}`,
		},
		{
			name:        "array payload inlined",
			payload:     []byte(`[1,2,3]`),
			wantPayload: `[1,2,3]`,
		},
		{
			name:        "empty payload coerced to null",
			payload:     nil,
			wantPayload: `null`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := db.PendingProposal{
				ID:      uuid.New(),
				Type:    "knowledge",
				Status:  "pending",
				Payload: tc.payload,
			}
			raw, err := json.Marshal(row)
			if err != nil {
				t.Fatalf("MarshalJSON error: %v", err)
			}

			// Ensure payload is NOT base64-encoded (Go's default for []byte).
			// A base64 leak would look like "payload":"eyJ0aXRsZSI6ImhlbGxvIn0=".
			if strings.Contains(string(raw), `"payload":"`) && !strings.Contains(string(raw), `"payload":""`) {
				t.Errorf("payload is base64-encoded string instead of raw JSON: %s", raw)
			}

			// Find the payload field by re-marshalling decoded map; use
			// json.RawMessage to confirm shape.
			type peek struct {
				Payload json.RawMessage `json:"payload"`
			}
			var p peek
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatalf("decode peek: %v (raw: %s)", err, raw)
			}
			if string(p.Payload) != tc.wantPayload {
				t.Errorf("payload bytes = %s, want %s (full output: %s)", p.Payload, tc.wantPayload, raw)
			}
		})
	}
}

// TestPendingProposalMarshalJSON_RoundTripStability verifies the output is
// valid JSON that can be decoded back into a map with the expected key set,
// and that re-marshalling the map produces byte-stable output. This guards
// against silent shape drift (extra keys, missing keys, type changes).
func TestPendingProposalMarshalJSON_RoundTripStability(t *testing.T) {
	row := db.PendingProposal{
		ID:          uuid.New(),
		WorkspaceID: pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true},
		Type:        "decision",
		Payload:     []byte(`{"k":1}`),
		Status:      "pending",
		ProposedBy:  pgtype.Text{String: "claude", Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		Reason:      pgtype.Text{Valid: false},
	}

	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	wantKeys := map[string]bool{
		"id":           true,
		"workspace_id": true,
		"type":         true,
		"payload":      true,
		"status":       true,
		"proposed_by":  true,
		"created_at":   true,
	}
	for k := range m {
		if !wantKeys[k] {
			t.Errorf("unexpected key in output: %q (full: %s)", k, raw)
		}
		delete(wantKeys, k)
	}
	for k := range wantKeys {
		t.Errorf("missing expected key %q in output: %s", k, raw)
	}
}

// --- PR160 M-3/M-2: Decision.MarshalJSON must never emit actor_session_id
// or confirmed_by_human, regardless of how the value reaches json.Marshal. ---

// TestDecisionMarshalJSON_HidesActorSessionIDAndConfirmedByHuman is the core
// bad-case check: a row carrying a real, distinguishable session ID must not
// leak that value — in any form, including as a JSON key — into the wire
// output. confirmed_by_human is dropped for the same reason PR160 M-2
// flagged: the field always writes false today (no confirmation gate
// exists yet), so surfacing it reads as a false "not human-approved" signal.
func TestDecisionMarshalJSON_HidesActorSessionIDAndConfirmedByHuman(t *testing.T) {
	const leakedSessionID = "mcp-session-1111-should-never-leak"

	row := db.Decision{
		ID:               uuid.New(),
		Title:            "some decision",
		Context:          "ctx",
		Decision:         "dec",
		Rationale:        "rat",
		Source:           "manual",
		ActorSessionID:   pgtype.Text{String: leakedSessionID, Valid: true},
		ConfirmedByHuman: true,
	}

	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("MarshalJSON returned error: %v", err)
	}

	if strings.Contains(string(raw), leakedSessionID) {
		t.Errorf("output leaks the raw actor_session_id value %q: %s", leakedSessionID, raw)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("output not valid JSON: %v (raw: %s)", err, raw)
	}
	if _, present := decoded["actor_session_id"]; present {
		t.Errorf("output contains the actor_session_id key (raw: %s)", raw)
	}
	if _, present := decoded["confirmed_by_human"]; present {
		t.Errorf("output contains the confirmed_by_human key (raw: %s)", raw)
	}
}

// TestDecisionMarshalJSON_PreservesOtherFields verifies every field OTHER
// than the two dropped audit fields still comes through with the exact
// same wire representation pgtype's own MarshalJSON methods already
// produced — proving decisionJSON did not accidentally reshape a kept
// field's format (e.g. UUID form, RFC3339Nano timestamp precision) while
// removing the two audit fields.
func TestDecisionMarshalJSON_PreservesOtherFields(t *testing.T) {
	id := uuid.New()
	projectID := uuid.New()
	wsID := uuid.New()
	taskID := uuid.New()
	created := time.Date(2026, 8, 20, 9, 15, 30, 123000000, time.UTC)

	row := db.Decision{
		ID:                id,
		ProjectID:         pgtype.UUID{Bytes: [16]byte(projectID), Valid: true},
		RepoName:          pgtype.Text{String: "wayneblacktea", Valid: true},
		Title:             "use Echo",
		Context:           "need HTTP framework",
		Decision:          "use echo/v4",
		Rationale:         "minimal, fast",
		Alternatives:      pgtype.Text{String: "gin, chi", Valid: true},
		CreatedAt:         pgtype.Timestamptz{Time: created, Valid: true},
		WorkspaceID:       pgtype.UUID{Bytes: [16]byte(wsID), Valid: true},
		Embedding:         []byte{1, 2, 3, 4},
		TaskID:            pgtype.UUID{Bytes: [16]byte(taskID), Valid: true},
		EmbeddingProvider: pgtype.Text{String: "anthropic", Valid: true},
		EmbeddingModel:    pgtype.Text{String: "voyage-3", Valid: true},
		EmbeddingDim:      pgtype.Int4{Int32: 1024, Valid: true},
		Source:            "manual",
		ActorSessionID:    pgtype.Text{String: "some-session", Valid: true},
		ConfirmedByHuman:  false,
	}

	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("MarshalJSON returned error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("output not valid JSON: %v (raw: %s)", err, raw)
	}

	checks := map[string]any{
		"id":                 id.String(),
		"project_id":         projectID.String(),
		"repo_name":          "wayneblacktea",
		"title":              "use Echo",
		"context":            "need HTTP framework",
		"decision":           "use echo/v4",
		"rationale":          "minimal, fast",
		"alternatives":       "gin, chi",
		"created_at":         "2026-08-20T09:15:30.123Z",
		"workspace_id":       wsID.String(),
		"task_id":            taskID.String(),
		"embedding_provider": "anthropic",
		"embedding_model":    "voyage-3",
		"embedding_dim":      float64(1024),
		"source":             "manual",
	}
	for key, want := range checks {
		if got := decoded[key]; got != want {
			t.Errorf("%s = %v, want %v (full: %s)", key, got, want, raw)
		}
	}
	// Embedding round-trips as base64 (Go's default []byte JSON encoding —
	// decisionJSON declares it as plain []byte, same as Decision itself).
	if _, present := decoded["embedding"]; !present {
		t.Errorf("embedding key missing from output (raw: %s)", raw)
	}
}

// TestDecisionMarshalJSON_HoldsRegardlessOfEmbeddingShape is the structural
// guarantee this method exists for: it must hold no matter HOW a future
// handler or MCP tool embeds a db.Decision when it calls json.Marshal —
// value, pointer, inside a slice, inside a map[string]any, inside an `any`
// slice, or as a named field of another response struct. This is what makes
// the fix a type-level defense rather than a per-call-site wrapper: a NEW
// handler that does `c.JSON(status, someDecision)` tomorrow inherits this
// guarantee automatically, without needing to know M-3/M-2 ever existed.
func TestDecisionMarshalJSON_HoldsRegardlessOfEmbeddingShape(t *testing.T) {
	const leakedSessionID = "mcp-session-shape-probe"
	d := db.Decision{
		ID:             uuid.New(),
		Title:          "shape probe",
		ActorSessionID: pgtype.Text{String: leakedSessionID, Valid: true},
	}

	type wrapperResponse struct {
		Decision *db.Decision `json:"decision,omitempty"`
	}

	cases := map[string]any{
		"bare value":                d,
		"pointer":                   &d,
		"slice of values":           []db.Decision{d},
		"slice of pointers":         []*db.Decision{&d},
		"map[string]any value":      map[string]any{"decision": d},
		"any-slice element":         []any{d},
		"named struct field (ptr)":  wrapperResponse{Decision: &d},
		"nested in another wrapper": map[string]any{"outer": map[string]any{"decision": &d}},
	}

	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("json.Marshal(%s) returned error: %v", name, err)
			}
			if strings.Contains(string(raw), leakedSessionID) {
				t.Errorf("%s: output leaks actor_session_id value: %s", name, raw)
			}
			if strings.Contains(string(raw), "actor_session_id") {
				t.Errorf("%s: output contains the actor_session_id key: %s", name, raw)
			}
		})
	}
}
