package ai

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/uuid"
)

const (
	defaultReviewerModel    = "claude-haiku-4-5"
	reviewerMaxConceptBatch = 50

	reviewerSystemPrompt = "You are evaluating spaced-repetition flashcards. " +
		"For each concept, decide if the learner has mastered it (status: mastered) " +
		"or if it is not helpful/too vague (status: not_helpful). " +
		"Leave as active if unclear. " +
		`Respond with ONLY a JSON array: [{"id":"...","status":"mastered|not_helpful|active"}]`
)

// validConceptStatuses is the allowlist for status values returned by Claude.
// Any value not in this set is rejected before being written to the DB.
var validConceptStatuses = map[string]struct{}{
	"active":      {},
	"mastered":    {},
	"not_helpful": {},
}

// ReviewInput is one concept sent to Claude for evaluation.
type ReviewInput struct {
	ID          uuid.UUID `json:"id"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	ReviewCount int       `json:"review_count"`
	Stability   float64   `json:"stability"`
}

// ReviewResult is one concept's AI-generated status recommendation.
type ReviewResult struct {
	ID        uuid.UUID
	NewStatus string
}

// ConceptReviewerIface is the contract for AI-driven concept status evaluation.
type ConceptReviewerIface interface {
	ReviewConcepts(ctx context.Context, concepts []ReviewInput) []ReviewResult
}

// ConceptReviewer calls the Claude API to evaluate spaced-repetition flashcards.
type ConceptReviewer struct {
	client *anthropic.Client
	model  string
}

// NewConceptReviewer creates a ConceptReviewer with the given API key.
// The model defaults to claude-haiku-4-5 for cost efficiency.
func NewConceptReviewer(apiKey string) *ConceptReviewer {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return newConceptReviewerWithClient(&client, defaultReviewerModel)
}

// newConceptReviewerWithClient creates a ConceptReviewer with a pre-configured
// client and explicit model name. Intended for testing via option.WithBaseURL.
func newConceptReviewerWithClient(client *anthropic.Client, model string) *ConceptReviewer {
	return &ConceptReviewer{client: client, model: model}
}

// ReviewConcepts sends a batch of concepts to Claude and returns status
// recommendations. Batches are capped at reviewerMaxConceptBatch (50) to stay
// within token limits. On any error the function logs a warn and returns an
// empty slice — callers must not rely on a non-nil return for partial results.
func (r *ConceptReviewer) ReviewConcepts(ctx context.Context, concepts []ReviewInput) []ReviewResult {
	if len(concepts) == 0 {
		return nil
	}

	// Cap batch at 50 to avoid token overflow.
	if len(concepts) > reviewerMaxConceptBatch {
		concepts = concepts[:reviewerMaxConceptBatch]
	}

	payload, err := json.Marshal(concepts)
	if err != nil {
		slog.Warn("concept reviewer: failed to marshal concepts", "error", err)
		return nil
	}

	resp, err := r.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(r.model),
		MaxTokens: 2048,
		System: []anthropic.TextBlockParam{
			{Text: reviewerSystemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(string(payload))),
		},
	})
	if err != nil {
		slog.Warn("concept reviewer: API call failed", "error", err)
		return nil
	}

	if len(resp.Content) == 0 {
		slog.Warn("concept reviewer: empty response from API")
		return nil
	}

	type rawResult struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}

	var raw []rawResult
	if err := json.Unmarshal([]byte(resp.Content[0].Text), &raw); err != nil {
		slog.Warn("concept reviewer: failed to parse API response as JSON", "error", err, "response", resp.Content[0].Text)
		return nil
	}

	results := make([]ReviewResult, 0, len(raw))
	for _, item := range raw {
		// Validate status is in the allowlist before accepting.
		if _, ok := validConceptStatuses[item.Status]; !ok {
			slog.Warn("concept reviewer: ignoring unknown status from API",
				"id", item.ID,
				"status", item.Status,
			)
			continue
		}

		id, err := uuid.Parse(item.ID)
		if err != nil {
			slog.Warn("concept reviewer: ignoring invalid UUID from API", "id", item.ID, "error", err)
			continue
		}

		results = append(results, ReviewResult{ID: id, NewStatus: item.Status})
	}

	return results
}
