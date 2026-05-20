package ai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
)

// newClassifierWithBase creates an ActivityClassifier pointed at a test base URL.
func newClassifierWithBase(baseURL string) *localai.ActivityClassifier {
	client := anthropic.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(baseURL),
	)
	return localai.NewActivityClassifierWithClient(&client, "claude-haiku-4-5")
}

// makeClassifyAPIResponse wraps a ClassifyResult JSON payload in the Anthropic Messages API envelope.
func makeClassifyAPIResponse(result localai.ClassifyResult) string {
	payload, _ := json.Marshal(result)
	resp := map[string]any{
		"id":    "msg_test",
		"type":  "message",
		"role":  "assistant",
		"model": "claude-haiku-4-5",
		"content": []map[string]any{
			{"type": "text", "text": string(payload)},
		},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  50,
			"output_tokens": 30,
		},
	}
	out, _ := json.Marshal(resp)
	return string(out)
}

func TestActivityClassifier_Classify_Decision(t *testing.T) {
	want := localai.ClassifyResult{
		IsDecision: true,
		Title:      "Switched deployment platform to Railway",
		Rationale:  "Changing deployment platform is an architectural decision with trade-offs",
	}

	srv := newMockServer(t, http.StatusOK, makeClassifyAPIResponse(want))
	defer srv.Close()

	c := newClassifierWithBase(srv.URL)
	result := c.Classify(context.Background(), "bash-hook", "deploy:railway", "migrated from Render to Railway")

	if !result.IsDecision {
		t.Errorf("expected IsDecision=true, got false")
	}
	if result.Title != want.Title {
		t.Errorf("Title = %q, want %q", result.Title, want.Title)
	}
	if result.Rationale != want.Rationale {
		t.Errorf("Rationale = %q, want %q", result.Rationale, want.Rationale)
	}
}

func TestActivityClassifier_Classify_Routine(t *testing.T) {
	// API returns is_decision=false → result must reflect that
	routine := localai.ClassifyResult{IsDecision: false, Title: "", Rationale: ""}

	srv := newMockServer(t, http.StatusOK, makeClassifyAPIResponse(routine))
	defer srv.Close()

	c := newClassifierWithBase(srv.URL)
	result := c.Classify(context.Background(), "bash-hook", "pr:review-comment", "left a comment on PR #42")

	if result.IsDecision {
		t.Errorf("expected IsDecision=false for routine activity, got true")
	}
	if result.Title != "" {
		t.Errorf("expected empty title for routine activity, got %q", result.Title)
	}
}

func TestActivityClassifier_Classify_APIError(t *testing.T) {
	// API returns 500 → must return ClassifyResult{} with no panic
	srv := newMockServer(t, http.StatusInternalServerError, `{"type":"error","error":{"type":"api_error","message":"server error"}}`)
	defer srv.Close()

	c := newClassifierWithBase(srv.URL)
	result := c.Classify(context.Background(), "bash-hook", "deploy:bash", "something happened")

	if result.IsDecision {
		t.Errorf("expected IsDecision=false on API error, got true")
	}
	if result.Title != "" {
		t.Errorf("expected empty title on API error, got %q", result.Title)
	}
}

func TestActivityClassifier_Classify_InvalidJSON(t *testing.T) {
	// API returns 200 but content is not valid JSON
	rawContent, _ := json.Marshal(map[string]any{
		"id":    "msg_test",
		"type":  "message",
		"role":  "assistant",
		"model": "claude-haiku-4-5",
		"content": []map[string]any{
			{"type": "text", "text": "not json at all"},
		},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 10, "output_tokens": 5},
	})

	srv := newMockServer(t, http.StatusOK, string(rawContent))
	defer srv.Close()

	c := newClassifierWithBase(srv.URL)
	result := c.Classify(context.Background(), "bash-hook", "pr:merge", "merged PR #99")

	// Must not panic and must return safe zero value
	if result.IsDecision {
		t.Errorf("expected IsDecision=false on bad JSON response, got true")
	}
}

func TestActivityClassifier_Classify_EmptyAPIResponse(t *testing.T) {
	// API returns 200 with an empty content array
	rawContent, _ := json.Marshal(map[string]any{
		"id":          "msg_test",
		"type":        "message",
		"role":        "assistant",
		"model":       "claude-haiku-4-5",
		"content":     []map[string]any{},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 10, "output_tokens": 0},
	})

	srv := newMockServer(t, http.StatusOK, string(rawContent))
	defer srv.Close()

	c := newClassifierWithBase(srv.URL)
	result := c.Classify(context.Background(), "bash-hook", "pr:merge", "merged PR #100")

	if result.IsDecision {
		t.Errorf("expected IsDecision=false on empty API content, got true")
	}
}

func TestActivityClassifier_Classify_IsTask(t *testing.T) {
	// API returns is_task=true with a task title.
	want := localai.ClassifyResult{
		IsDecision: false,
		Title:      "",
		Rationale:  "",
		IsTask:     true,
		TaskTitle:  "Implement OAuth login with PKCE",
	}

	srv := newMockServer(t, http.StatusOK, makeClassifyAPIResponse(want))
	defer srv.Close()

	c := newClassifierWithBase(srv.URL)
	result := c.Classify(context.Background(), "bash-hook", "pr:open", "opened PR for OAuth login with PKCE")

	if !result.IsTask {
		t.Errorf("expected IsTask=true, got false")
	}
	if result.TaskTitle != want.TaskTitle {
		t.Errorf("TaskTitle = %q, want %q", result.TaskTitle, want.TaskTitle)
	}
	if result.IsDecision {
		t.Errorf("expected IsDecision=false, got true")
	}
}

func TestActivityClassifier_Classify_IsTaskAndDecision(t *testing.T) {
	// API returns both is_decision=true and is_task=true.
	want := localai.ClassifyResult{
		IsDecision: true,
		Title:      "Switched deployment platform to Railway",
		Rationale:  "Platform change with trade-offs",
		IsTask:     true,
		TaskTitle:  "Update CI/CD config for Railway",
	}

	srv := newMockServer(t, http.StatusOK, makeClassifyAPIResponse(want))
	defer srv.Close()

	c := newClassifierWithBase(srv.URL)
	result := c.Classify(context.Background(), "bash-hook", "deploy:railway", "migrated from Render to Railway")

	if !result.IsDecision {
		t.Errorf("expected IsDecision=true, got false")
	}
	if !result.IsTask {
		t.Errorf("expected IsTask=true, got false")
	}
	if result.Title != want.Title {
		t.Errorf("Title = %q, want %q", result.Title, want.Title)
	}
	if result.TaskTitle != want.TaskTitle {
		t.Errorf("TaskTitle = %q, want %q", result.TaskTitle, want.TaskTitle)
	}
}

func TestActivityClassifier_Classify_IsTask_FalseOnAPIError(t *testing.T) {
	// API returns 500 → IsTask must be false (safe zero value).
	srv := newMockServer(t, http.StatusInternalServerError, `{"type":"error","error":{"type":"api_error","message":"server error"}}`)
	defer srv.Close()

	c := newClassifierWithBase(srv.URL)
	result := c.Classify(context.Background(), "bash-hook", "pr:open", "opened PR for feature Y")

	if result.IsTask {
		t.Errorf("expected IsTask=false on API error, got true")
	}
	if result.TaskTitle != "" {
		t.Errorf("expected empty TaskTitle on API error, got %q", result.TaskTitle)
	}
}

// TestActivityClassifier_Classify_ConfidenceParsing verifies the LLM-returned
// confidence value is parsed AND clamped into [0, 1]. Anything outside that
// range — negative, > 1, missing field — normalises to 0 so downstream
// auto-accept logic (gated on confidence >= ClassifierAutoAcceptThreshold)
// defaults to the conservative proposal-queue path.
func TestActivityClassifier_Classify_ConfidenceParsing(t *testing.T) {
	cases := []struct {
		name        string
		raw         string // raw JSON the model "returns" inside content[0].text
		wantConf    float64
		wantIsTask  bool
		wantTask    string
		description string
	}{
		{
			name:        "high confidence — preserved as-is",
			raw:         `{"is_decision":false,"title":"","rationale":"","is_task":true,"task_title":"Add log rotation","confidence":0.95}`,
			wantConf:    0.95,
			wantIsTask:  true,
			wantTask:    "Add log rotation",
			description: "0.95 is in-range and copied through unchanged",
		},
		{
			name:        "boundary 0 — preserved",
			raw:         `{"is_decision":false,"title":"","rationale":"","is_task":false,"task_title":"","confidence":0}`,
			wantConf:    0,
			wantIsTask:  false,
			wantTask:    "",
			description: "lower boundary preserved",
		},
		{
			name:        "boundary 1 — preserved",
			raw:         `{"is_decision":false,"title":"","rationale":"","is_task":true,"task_title":"X","confidence":1}`,
			wantConf:    1,
			wantIsTask:  true,
			wantTask:    "X",
			description: "upper boundary preserved",
		},
		{
			name:        "negative — clamped to 0",
			raw:         `{"is_decision":false,"title":"","rationale":"","is_task":true,"task_title":"X","confidence":-0.5}`,
			wantConf:    0,
			wantIsTask:  true,
			wantTask:    "X",
			description: "negative values are normalised to 0 (conservative default disables auto-accept)",
		},
		{
			name:        "above 1 — clamped to 0",
			raw:         `{"is_decision":false,"title":"","rationale":"","is_task":true,"task_title":"X","confidence":1.5}`,
			wantConf:    0,
			wantIsTask:  true,
			wantTask:    "X",
			description: "out-of-range high values default to 0",
		},
		{
			name:        "missing field — defaults to 0",
			raw:         `{"is_decision":false,"title":"","rationale":"","is_task":true,"task_title":"X"}`,
			wantConf:    0,
			wantIsTask:  true,
			wantTask:    "X",
			description: "json.Unmarshal leaves the field at zero; clamp keeps it at 0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := map[string]any{
				"id":    "msg_test",
				"type":  "message",
				"role":  "assistant",
				"model": "claude-haiku-4-5",
				"content": []map[string]any{
					{"type": "text", "text": tc.raw},
				},
				"stop_reason":   "end_turn",
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  10,
					"output_tokens": 10,
				},
			}
			out, _ := json.Marshal(resp)
			srv := newMockServer(t, http.StatusOK, string(out))
			defer srv.Close()

			c := newClassifierWithBase(srv.URL)
			result := c.Classify(context.Background(), "bash-hook", "x", "y")

			if result.Confidence != tc.wantConf {
				t.Errorf("[%s] Confidence = %v, want %v", tc.description, result.Confidence, tc.wantConf)
			}
			if result.IsTask != tc.wantIsTask {
				t.Errorf("[%s] IsTask = %v, want %v", tc.description, result.IsTask, tc.wantIsTask)
			}
			if result.TaskTitle != tc.wantTask {
				t.Errorf("[%s] TaskTitle = %q, want %q", tc.description, result.TaskTitle, tc.wantTask)
			}
		})
	}
}

// TestActivityClassifier_Classify_ConfidenceAutoAcceptThresholdConst is a
// guard that the published const ClassifierAutoAcceptThreshold is 0.85 — the
// value the HTTP twin (internal/handler/autolog_handler.go) and the MCP path
// (internal/mcp/middleware_classify.go) BOTH reference. Failing here is a
// signal to also re-check the downstream tests, not just to bump this number.
func TestActivityClassifier_Classify_ConfidenceAutoAcceptThresholdConst(t *testing.T) {
	if localai.ClassifierAutoAcceptThreshold != 0.85 {
		t.Errorf("ClassifierAutoAcceptThreshold = %v, want 0.85 (changing this affects auto-accept callers)",
			localai.ClassifierAutoAcceptThreshold)
	}
}
