package ai

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	defaultClassifierModel = "claude-haiku-4-5"
	classifierTimeout      = 10 * time.Second
	classifierMaxTokens    = 256
)

// classifierSystemPrompt instructs the model to classify development activities.
// It returns JSON indicating whether the activity implies a real architectural decision.
const classifierSystemPrompt = "You classify software development activities. " +
	"Decide if the following activity implies an architectural, design, or scope decision was made. " +
	"Only return true for real decisions with trade-offs " +
	"(e.g. 'merged PR that changes DB schema', 'deployed config change that disables feature X', " +
	"'changed deployment platform'). " +
	"NOT for routine activities (PR review comments, test runs, file edits). " +
	"Return JSON: {\"is_decision\": bool, \"title\": string, \"rationale\": string}. " +
	"Return empty title/rationale when is_decision=false."

// ClassifyResult holds the outcome of classifying a single activity.
type ClassifyResult struct {
	IsDecision bool   `json:"is_decision"`
	Title      string `json:"title"`
	Rationale  string `json:"rationale"`
}

// ActivityClassifier calls the Claude API to decide whether an activity is a decision.
type ActivityClassifier struct {
	client *anthropic.Client
	model  string
}

// NewActivityClassifier creates an ActivityClassifier with the given API key.
// It uses the claude-haiku-4-5 model for fast, low-cost classification.
func NewActivityClassifier(apiKey string) *ActivityClassifier {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return NewActivityClassifierWithClient(&client, defaultClassifierModel)
}

// NewActivityClassifierWithClient creates an ActivityClassifier with a pre-configured
// client and explicit model name. Intended for testing with a mock HTTP server.
func NewActivityClassifierWithClient(client *anthropic.Client, model string) *ActivityClassifier {
	return &ActivityClassifier{client: client, model: model}
}

// Classify sends actor+action+notes to Haiku and returns a ClassifyResult.
// On any error or parse failure it returns ClassifyResult{IsDecision: false} — never panics.
func (c *ActivityClassifier) Classify(ctx context.Context, actor, action, notes string) ClassifyResult {
	ctx, cancel := context.WithTimeout(ctx, classifierTimeout)
	defer cancel()

	prompt := "actor: " + actor + "\naction: " + action + "\nnotes: " + notes

	resp, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: classifierMaxTokens,
		System: []anthropic.TextBlockParam{
			{Text: classifierSystemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		slog.Warn("activity_classifier: API call failed", "error", err)
		return ClassifyResult{}
	}

	if len(resp.Content) == 0 {
		slog.Warn("activity_classifier: empty response from API")
		return ClassifyResult{}
	}

	var result ClassifyResult
	if err := json.Unmarshal([]byte(resp.Content[0].Text), &result); err != nil {
		slog.Warn("activity_classifier: failed to parse API response as JSON", "error", err)
		return ClassifyResult{}
	}

	return result
}
