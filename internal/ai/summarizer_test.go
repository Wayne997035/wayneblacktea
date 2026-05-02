package ai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
)

// newMockServer creates a test HTTP server that returns the given JSON body and status.
func newMockServer(t *testing.T, statusCode int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(body))
	}))
}

// newSummarizerWithBase creates a Summarizer pointed at a test base URL.
func newSummarizerWithBase(baseURL string) *localai.Summarizer {
	client := anthropic.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(baseURL),
	)
	return localai.NewWithClient(&client, "claude-haiku-4-5")
}

func makeAPIResponse(summaryText string, decisions []string) string {
	return makeAPIResponseWithTasks(summaryText, decisions, nil)
}

func makeAPIResponseWithTasks(summaryText string, decisions, tasks []string) string {
	if decisions == nil {
		decisions = []string{}
	}
	if tasks == nil {
		tasks = []string{}
	}
	payload := map[string]any{
		"summary":   summaryText,
		"decisions": decisions,
		"tasks":     tasks,
	}
	b, _ := json.Marshal(payload)
	// Wrap in Anthropic Messages API response envelope.
	resp := map[string]any{
		"id":    "msg_test",
		"type":  "message",
		"role":  "assistant",
		"model": "claude-haiku-4-5",
		"content": []map[string]any{
			{"type": "text", "text": string(b)},
		},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  100,
			"output_tokens": 50,
		},
	}
	out, _ := json.Marshal(resp)
	return string(out)
}

func TestSummarizer_EmptyTranscript(t *testing.T) {
	// No HTTP call should be made — test server panics if hit.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("unexpected HTTP call for empty transcript")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newSummarizerWithBase(srv.URL)
	result := s.Summarize(context.Background(), []localai.Message{})
	if result.Summary != "" {
		t.Errorf("expected empty summary, got %q", result.Summary)
	}
	if len(result.Decisions) != 0 {
		t.Errorf("expected 0 decisions, got %d", len(result.Decisions))
	}
}

func TestSummarizer_NilTranscript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("unexpected HTTP call for nil transcript")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newSummarizerWithBase(srv.URL)
	result := s.Summarize(context.Background(), nil)
	if result.Summary != "" {
		t.Errorf("expected empty summary, got %q", result.Summary)
	}
}

func TestSummarizer_APIError(t *testing.T) {
	srv := newMockServer(t, http.StatusInternalServerError, `{"type":"error","error":{"type":"api_error","message":"server error"}}`)
	defer srv.Close()

	s := newSummarizerWithBase(srv.URL)
	transcript := []localai.Message{
		{Role: "user", Content: "Can we implement OAuth?"},
		{Role: "assistant", Content: "Sure, I'll use PKCE flow."},
	}
	result := s.Summarize(context.Background(), transcript)
	// Must return empty result, not panic.
	if result.Summary != "" {
		t.Errorf("expected empty summary on API error, got %q", result.Summary)
	}
	if len(result.Decisions) != 0 {
		t.Errorf("expected 0 decisions on API error, got %d", len(result.Decisions))
	}
}

func TestSummarizer_InvalidJSONResponse(t *testing.T) {
	// API returns 200 but content is not valid JSON.
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

	s := newSummarizerWithBase(srv.URL)
	transcript := []localai.Message{
		{Role: "user", Content: "Let's ship this."},
	}
	result := s.Summarize(context.Background(), transcript)
	// Must not panic, must return empty.
	if result.Summary != "" {
		t.Errorf("expected empty summary on bad JSON, got %q", result.Summary)
	}
}

func TestSummarizer_Success(t *testing.T) {
	wantSummary := "Implemented OAuth login with PKCE flow. Shipped to production."
	wantDecisions := []string{"Use PKCE over implicit flow", "Store tokens in httpOnly cookie"}

	srv := newMockServer(t, http.StatusOK, makeAPIResponse(wantSummary, wantDecisions))
	defer srv.Close()

	s := newSummarizerWithBase(srv.URL)
	transcript := []localai.Message{
		{Role: "user", Content: "Let's implement OAuth."},
		{Role: "assistant", Content: "I'll use PKCE flow for security."},
	}

	result := s.Summarize(context.Background(), transcript)
	if result.Summary != wantSummary {
		t.Errorf("got summary %q, want %q", result.Summary, wantSummary)
	}
	if len(result.Decisions) != len(wantDecisions) {
		t.Errorf("got %d decisions, want %d", len(result.Decisions), len(wantDecisions))
	}
	for i, d := range wantDecisions {
		if result.Decisions[i] != d {
			t.Errorf("decision[%d]: got %q, want %q", i, result.Decisions[i], d)
		}
	}
}

func TestSummarizer_TranscriptCapAt64KB(t *testing.T) {
	// Build a transcript that exceeds 64KB.
	bigContent := make([]byte, 1024)
	for i := range bigContent {
		bigContent[i] = 'x'
	}
	var msgs []localai.Message
	for range 100 {
		msgs = append(msgs, localai.Message{Role: "user", Content: string(bigContent)})
	}

	var receivedLen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if messages, ok := body["messages"].([]any); ok && len(messages) > 0 {
				if m0, ok := messages[0].(map[string]any); ok {
					if content, ok := m0["content"].([]any); ok && len(content) > 0 {
						if c0, ok := content[0].(map[string]any); ok {
							if text, ok := c0["text"].(string); ok {
								receivedLen = len(text)
							}
						}
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(makeAPIResponse("short summary", nil)))
	}))
	defer srv.Close()

	s := newSummarizerWithBase(srv.URL)
	_ = s.Summarize(context.Background(), msgs)

	const maxBytes = 64 * 1024
	if receivedLen > maxBytes {
		t.Errorf("prompt sent to API was %d bytes, exceeds 64KB cap", receivedLen)
	}
}

func TestSummarizer_Tasks_Success(t *testing.T) {
	wantSummary := "Discussed implementation plan."
	wantTasks := []string{"Write integration tests", "Update API docs"}

	srv := newMockServer(t, http.StatusOK, makeAPIResponseWithTasks(wantSummary, nil, wantTasks))
	defer srv.Close()

	s := newSummarizerWithBase(srv.URL)
	transcript := []localai.Message{
		{Role: "user", Content: "We need integration tests and updated API docs."},
		{Role: "assistant", Content: "I'll add both."},
	}

	result := s.Summarize(context.Background(), transcript)
	if result.Summary != wantSummary {
		t.Errorf("got summary %q, want %q", result.Summary, wantSummary)
	}
	if len(result.Tasks) != len(wantTasks) {
		t.Fatalf("got %d tasks, want %d", len(result.Tasks), len(wantTasks))
	}
	for i, task := range wantTasks {
		if result.Tasks[i] != task {
			t.Errorf("task[%d]: got %q, want %q", i, result.Tasks[i], task)
		}
	}
}

func TestSummarizer_Tasks_EmptyWhenNotInTranscript(t *testing.T) {
	// API returns empty tasks array → Tasks field should be empty.
	srv := newMockServer(t, http.StatusOK, makeAPIResponseWithTasks("Done.", nil, []string{}))
	defer srv.Close()

	s := newSummarizerWithBase(srv.URL)
	transcript := []localai.Message{
		{Role: "user", Content: "Reviewed the PR."},
	}

	result := s.Summarize(context.Background(), transcript)
	if len(result.Tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(result.Tasks))
	}
}

func TestSummarizer_Tasks_NilOnAPIError(t *testing.T) {
	// API error → Tasks must be nil/empty, not panic.
	srv := newMockServer(t, http.StatusInternalServerError, `{"type":"error","error":{"type":"api_error","message":"server error"}}`)
	defer srv.Close()

	s := newSummarizerWithBase(srv.URL)
	transcript := []localai.Message{
		{Role: "user", Content: "Let's fix the bug."},
	}

	result := s.Summarize(context.Background(), transcript)
	if len(result.Tasks) != 0 {
		t.Errorf("expected 0 tasks on API error, got %d", len(result.Tasks))
	}
}

// makePlainTextAPIResponse wraps a plain-text string in the Anthropic response envelope.
func makePlainTextAPIResponse(text string) string {
	resp := map[string]any{
		"id":    "msg_test",
		"type":  "message",
		"role":  "assistant",
		"model": "claude-haiku-4-5",
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  50,
			"output_tokens": 25,
		},
	}
	out, _ := json.Marshal(resp)
	return string(out)
}

func TestSummarizeSession_EmptyTranscript(t *testing.T) {
	// No HTTP call should be made for an empty transcript.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("unexpected HTTP call for empty transcript")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newSummarizerWithBase(srv.URL)
	text, err := s.SummarizeSession(context.Background(), []localai.Message{})
	if err != nil {
		t.Errorf("expected nil error for empty transcript, got %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text for empty transcript, got %q", text)
	}
}

func TestSummarizeSession_NilTranscript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("unexpected HTTP call for nil transcript")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newSummarizerWithBase(srv.URL)
	text, err := s.SummarizeSession(context.Background(), nil)
	if err != nil {
		t.Errorf("expected nil error for nil transcript, got %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
}

func TestSummarizeSession_Success(t *testing.T) {
	want := "Implemented OAuth login. Shipped feature/auth branch."
	srv := newMockServer(t, http.StatusOK, makePlainTextAPIResponse(want))
	defer srv.Close()

	s := newSummarizerWithBase(srv.URL)
	transcript := []localai.Message{
		{Role: "user", Content: "Implement OAuth."},
		{Role: "assistant", Content: "Done with PKCE."},
	}

	text, err := s.SummarizeSession(context.Background(), transcript)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if text != want {
		t.Errorf("got %q, want %q", text, want)
	}
}

func TestSummarizeSession_APIError(t *testing.T) {
	srv := newMockServer(t, http.StatusInternalServerError, `{"type":"error","error":{"type":"api_error","message":"server error"}}`)
	defer srv.Close()

	s := newSummarizerWithBase(srv.URL)
	transcript := []localai.Message{
		{Role: "user", Content: "Fix the test."},
	}

	text, err := s.SummarizeSession(context.Background(), transcript)
	if err == nil {
		t.Error("expected error on API failure, got nil")
	}
	if text != "" {
		t.Errorf("expected empty text on API error, got %q", text)
	}
}

func TestSummarizeSession_TruncatesAt500Chars(t *testing.T) {
	// Build a 600-character response.
	longText := strings.Repeat("a", 600)

	srv := newMockServer(t, http.StatusOK, makePlainTextAPIResponse(longText))
	defer srv.Close()

	s := newSummarizerWithBase(srv.URL)
	transcript := []localai.Message{
		{Role: "user", Content: "Do stuff."},
	}

	text, err := s.SummarizeSession(context.Background(), transcript)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len([]rune(text)) > 500 {
		t.Errorf("text length %d exceeds 500-char cap", len([]rune(text)))
	}
}

func TestSummarizeSession_EmptyAPIResponse(t *testing.T) {
	// API returns 200 but content array is empty.
	emptyContentResp, _ := json.Marshal(map[string]any{
		"id":            "msg_test",
		"type":          "message",
		"role":          "assistant",
		"model":         "claude-haiku-4-5",
		"content":       []map[string]any{},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage":         map[string]any{"input_tokens": 10, "output_tokens": 0},
	})

	srv := newMockServer(t, http.StatusOK, string(emptyContentResp))
	defer srv.Close()

	s := newSummarizerWithBase(srv.URL)
	transcript := []localai.Message{
		{Role: "user", Content: "Session work."},
	}

	text, err := s.SummarizeSession(context.Background(), transcript)
	if err == nil {
		t.Error("expected error for empty content array, got nil")
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
}
