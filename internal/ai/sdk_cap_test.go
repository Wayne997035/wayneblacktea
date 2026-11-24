package ai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/google/uuid"
)

// sdkMessageServer serves a valid Anthropic Messages response whose single text
// block is text. The SDK reads ANTHROPIC_BASE_URL, so the public constructors
// (which only receive an API key) reach this server.
func sdkMessageServer(t *testing.T, text string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"id":            "msg_test",
		"type":          "message",
		"role":          "assistant",
		"model":         "claude-haiku-4-5",
		"content":       []map[string]any{{"type": "text", "text": text}},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage":         map[string]any{"input_tokens": 1, "output_tokens": 1},
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
}

const (
	capOverSize  = 2 << 20   // well over the 1 MiB cap
	capUnderSize = 100 << 10 // proves the cap is not too small
)

func pad(n int) string { return strings.Repeat("a", n) }

// classifyJSON is a ClassifyResult-shaped JSON whose title is padded to n bytes,
// so the body parses fine when uncapped.
func classifyJSON(n int) string {
	return `{"is_decision":true,"title":"` + pad(n) + `","rationale":"r","confidence":0.9}`
}

// [F1124-71]
func TestNewActivityClassifier_AppliesResponseCap(t *testing.T) {
	sdkMessageServer(t, classifyJSON(capUnderSize))
	if got := ai.NewActivityClassifier("k").Classify(context.Background(), "a", "b", "c"); got.Title == "" {
		t.Fatalf("positive control: Title empty for 100 KiB response, got %+v", got)
	}

	sdkMessageServer(t, classifyJSON(capOverSize))
	got := ai.NewActivityClassifier("k").Classify(context.Background(), "a", "b", "c")
	if got.Title != "" || got.IsDecision {
		t.Errorf("result = {IsDecision:%v Title len:%d}, want zero value (response capped)", got.IsDecision, len(got.Title))
	}
}

// [F1124-72]
func TestNewAtomizer_AppliesResponseCap(t *testing.T) {
	t.Setenv("CLAUDE_API_KEY", "k")
	atoms := func(n int) string {
		return `{"atoms":[{"content":"` + pad(n) + `","keywords":["k"],"tags":["t"]}],"links":[]}`
	}
	sdkMessageServer(t, atoms(capUnderSize))
	if _, err := ai.NewAtomizer().Atomize(context.Background(), "x"); err != nil {
		t.Fatalf("positive control: Atomize err = %v for 100 KiB response", err)
	}

	sdkMessageServer(t, atoms(capOverSize))
	if _, err := ai.NewAtomizer().Atomize(context.Background(), "x"); err == nil {
		t.Error("err = <nil>, want error (response over cap)")
	}
}

// [F1124-73]
func TestNewReflector_AppliesResponseCap(t *testing.T) {
	props := func(n int) string { return `[{"title":"t","content":"` + pad(n) + `"}]` }
	sdkMessageServer(t, props(capUnderSize))
	got, err := ai.NewReflector("k").Propose(context.Background(), "p")
	if err != nil || len(got) != 1 {
		t.Fatalf("positive control: Propose = %d proposals, err %v for 100 KiB response", len(got), err)
	}

	sdkMessageServer(t, props(capOverSize))
	if _, err := ai.NewReflector("k").Propose(context.Background(), "p"); err == nil {
		t.Error("err = <nil>, want error (response over cap)")
	}
}

// [F1124-74]
func TestNewConceptReviewer_AppliesResponseCap(t *testing.T) {
	id := uuid.New()
	// "pad" is an unknown field the reviewer's decoder ignores, so the body
	// parses to one result when uncapped.
	rows := func(n int) string {
		return `[{"id":"` + id.String() + `","status":"mastered","pad":"` + pad(n) + `"}]`
	}
	in := []ai.ReviewInput{{ID: id, Title: "t", Content: "c"}}
	sdkMessageServer(t, rows(capUnderSize))
	if got := ai.NewConceptReviewer("k").ReviewConcepts(context.Background(), in); len(got) != 1 {
		t.Fatalf("positive control: %d results for 100 KiB response, want 1", len(got))
	}

	sdkMessageServer(t, rows(capOverSize))
	if got := ai.NewConceptReviewer("k").ReviewConcepts(context.Background(), in); len(got) != 0 {
		t.Errorf("results = %d, want zero value (response capped)", len(got))
	}
}

// [F1124-75]
func TestNewSummarizer_AppliesResponseCap(t *testing.T) {
	sum := func(n int) string { return `{"summary":"` + pad(n) + `","decisions":[],"tasks":[]}` }
	msgs := []ai.Message{{Role: "user", Content: "hi"}}
	sdkMessageServer(t, sum(capUnderSize))
	if got := ai.New("k").Summarize(context.Background(), msgs); got.Summary == "" {
		t.Fatal("positive control: Summary empty for 100 KiB response")
	}

	sdkMessageServer(t, sum(capOverSize))
	if got := ai.New("k").Summarize(context.Background(), msgs); got.Summary != "" {
		t.Errorf("result = {Summary len:%d}, want zero value (response capped)", len(got.Summary))
	}
	// NewWithPreference builds its client through New, so it inherits the cap.
	if got := ai.NewWithPreference("k", nil).Summarize(context.Background(), msgs); got.Summary != "" {
		t.Errorf("NewWithPreference result = {Summary len:%d}, want zero value", len(got.Summary))
	}
}
