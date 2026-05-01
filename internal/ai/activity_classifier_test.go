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
