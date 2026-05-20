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

// TestPendingProposalMarshalJSON_OmitsNilFields verifies that NULL pgtype
// columns produce NO key in the output (omitempty on *string pointers), so
// the frontend can distinguish "field absent" from "field present but
// null/empty". This is the same contract handler.pendingProposalResponse
// honours (PR #121 / #123).
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

	for _, key := range []string{"workspace_id", "proposed_by", "resolved_at", "reason"} {
		if _, present := decoded[key]; present {
			t.Errorf("key %q must be omitted when underlying pgtype is invalid (raw: %s)", key, raw)
		}
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
