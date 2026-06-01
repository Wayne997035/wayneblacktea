package reflection_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	"github.com/google/uuid"
)

// TestReflection_JSONTags asserts that marshaled Reflection uses snake_case
// field names (not Go-default capitalized names). This verifies finding 1 of
// the Memory Phase 2 checkpoint review.
func TestReflection_JSONTags(t *testing.T) {
	wsID := uuid.New()
	relType := "task"
	relID := uuid.New()

	r := reflection.Reflection{
		ID:                uuid.New(),
		WorkspaceID:       &wsID,
		Type:              "daily",
		RelatedEntityType: &relType,
		RelatedEntityID:   &relID,
		Summary:           "test summary",
		Insights:          json.RawMessage(`["insight"]`),
		PatternsDetected:  json.RawMessage(`{"key":"val"}`),
		SuggestedActions:  json.RawMessage(`["act"]`),
		Confidence:        0.9,
		CreatedAt:         time.Now(),
	}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	got := string(b)

	// Must contain snake_case keys, not Go-default capitalized names.
	wantKeys := []string{
		`"id"`,
		`"workspace_id"`,
		`"type"`,
		`"related_entity_type"`,
		`"related_entity_id"`,
		`"summary"`,
		`"insights"`,
		`"patterns_detected"`,
		`"suggested_actions"`,
		`"confidence"`,
		`"created_at"`,
	}
	for _, key := range wantKeys {
		if !strings.Contains(got, key) {
			t.Errorf("marshaled JSON missing key %s; got: %s", key, got)
		}
	}

	// Must NOT contain any Go-default capitalized field names.
	forbiddenKeys := []string{
		`"ID"`,
		`"WorkspaceID"`,
		`"Type"`,
		`"RelatedEntityType"`,
		`"RelatedEntityID"`,
		`"Summary"`,
		`"Insights"`,
		`"PatternsDetected"`,
		`"SuggestedActions"`,
		`"Confidence"`,
		`"CreatedAt"`,
	}
	for _, key := range forbiddenKeys {
		if strings.Contains(got, key) {
			t.Errorf("marshaled JSON contains Go-default capitalized key %s; got: %s", key, got)
		}
	}
}

// TestReflection_JSONTags_OmitEmpty asserts that nil/zero optional fields are
// omitted from the marshaled output.
func TestReflection_JSONTags_OmitEmpty(t *testing.T) {
	r := reflection.Reflection{
		ID:        uuid.New(),
		Type:      "system",
		Summary:   "minimal",
		CreatedAt: time.Now(),
	}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	got := string(b)

	// Optional fields with nil/zero value must be absent.
	absentKeys := []string{
		`"workspace_id"`,
		`"related_entity_type"`,
		`"related_entity_id"`,
		`"insights"`,
		`"patterns_detected"`,
		`"suggested_actions"`,
	}
	for _, key := range absentKeys {
		if strings.Contains(got, key) {
			t.Errorf("marshaled JSON should omit nil field %s; got: %s", key, got)
		}
	}

	// Required fields must always appear.
	requiredKeys := []string{`"id"`, `"type"`, `"summary"`, `"confidence"`, `"created_at"`}
	for _, key := range requiredKeys {
		if !strings.Contains(got, key) {
			t.Errorf("marshaled JSON missing required key %s; got: %s", key, got)
		}
	}
}
