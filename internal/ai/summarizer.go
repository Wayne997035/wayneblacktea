package ai

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	defaultSummarizerModel = "claude-sonnet-4-6"
	maxTranscriptLen       = 64 * 1024 // 64 KB
)

// summarizerSystemPrompt instructs the model to return structured JSON.
// Line 2 of the decisions description is split to stay within the 140-char line limit.
const summarizerSystemPrompt = "You are analyzing a software development session transcript.\n" +
	"Return a JSON object with two fields:\n" +
	"1. \"summary\": 2-4 sentences describing what was accomplished this session " +
	"(decisions made, features shipped, blockers hit)\n" +
	"2. \"decisions\": array of strings, each a one-line title for an architectural/design decision " +
	"that was made implicitly (user accepted a proposal, agreed to an approach) " +
	"but NOT explicitly logged via a log_decision tool call. " +
	"Only include real decisions with clear trade-offs. Return empty array if none.\n" +
	"Respond ONLY with valid JSON, no markdown."

// Message represents a single conversation turn from the session transcript.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// SummaryResult holds the AI-generated session summary and implicit decisions.
type SummaryResult struct {
	Summary   string   `json:"summary"`
	Decisions []string `json:"decisions"`
}

// Summarizer calls the Claude API to generate session summaries.
type Summarizer struct {
	client *anthropic.Client
	model  string
}

// resolveModel reads the CLAUDE_SUMMARY_MODEL env var and falls back to the
// default model when the variable is unset or empty.
func resolveModel() string {
	if m := os.Getenv("CLAUDE_SUMMARY_MODEL"); m != "" {
		return m
	}
	return defaultSummarizerModel
}

// New creates a Summarizer with the given API key.
// The model is selected from the CLAUDE_SUMMARY_MODEL environment variable,
// defaulting to "claude-sonnet-4-6".
func New(apiKey string) *Summarizer {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return NewWithClient(&client, resolveModel())
}

// NewWithClient creates a Summarizer with a pre-configured client and explicit
// model name. Intended for testing with a mock HTTP server via option.WithBaseURL.
func NewWithClient(client *anthropic.Client, model string) *Summarizer {
	return &Summarizer{
		client: client,
		model:  model,
	}
}

// Summarize calls the configured Claude model with the transcript and returns a structured summary.
// It returns an empty SummaryResult on any error — callers should always fall back gracefully.
func (s *Summarizer) Summarize(ctx context.Context, transcript []Message) SummaryResult {
	if len(transcript) == 0 {
		return SummaryResult{}
	}

	// Build prompt text from transcript, capping at maxTranscriptLen bytes.
	promptText := buildPromptText(transcript)

	resp, err := s.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(s.model),
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: summarizerSystemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(promptText)),
		},
	})
	if err != nil {
		slog.Warn("summarizer: API call failed", "error", err)
		return SummaryResult{}
	}

	if len(resp.Content) == 0 {
		slog.Warn("summarizer: empty response from API")
		return SummaryResult{}
	}

	var result SummaryResult
	rawText := resp.Content[0].Text
	if err := json.Unmarshal([]byte(rawText), &result); err != nil {
		slog.Warn("summarizer: failed to parse API response as JSON", "error", err)
		return SummaryResult{}
	}

	return result
}

// buildPromptText concatenates transcript messages into a single string, capped at maxTranscriptLen.
func buildPromptText(transcript []Message) string {
	var total int
	var lines []byte
	for _, m := range transcript {
		line := m.Role + ": " + m.Content + "\n"
		if total+len(line) > maxTranscriptLen {
			break
		}
		lines = append(lines, line...)
		total += len(line)
	}
	return string(lines)
}
