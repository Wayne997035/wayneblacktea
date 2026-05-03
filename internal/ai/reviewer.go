package ai

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/uuid"

	"github.com/Wayne997035/wayneblacktea/internal/llm"
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

// ConceptReviewer calls an LLM provider to evaluate spaced-repetition flashcards.
//
// Two construction paths are supported (mirroring ActivityClassifier):
//   - NewConceptReviewer(apiKey)           — backward-compat, single Claude provider
//   - NewConceptReviewerFromLLM(jsonClient) — provider-neutral via internal/llm
type ConceptReviewer struct {
	// Exactly one of {client, llm} is set; constructors enforce this.
	client *anthropic.Client
	model  string

	llm llm.JSONClient
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

// NewConceptReviewerFromLLM wraps any JSONClient (single provider OR a
// llm.Chain). When client is nil ReviewConcepts returns nil — matching the
// existing graceful-degrade contract.
func NewConceptReviewerFromLLM(client llm.JSONClient) *ConceptReviewer {
	return &ConceptReviewer{llm: client}
}

// ReviewConcepts sends a batch of concepts to the configured provider and
// returns status recommendations. Batches are capped at reviewerMaxConceptBatch
// (50) to stay within token limits. On any error the function logs a warn and
// returns an empty slice — callers must not rely on a non-nil return for
// partial results.
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
	if r.llm != nil {
		return r.reviewViaLLM(ctx, payload)
	}
	return r.reviewViaSDK(ctx, payload)
}

// reviewViaLLM uses the provider-neutral abstraction.
func (r *ConceptReviewer) reviewViaLLM(ctx context.Context, payload []byte) []ReviewResult {
	out, err := r.llm.CompleteJSON(ctx, llm.JSONRequest{
		Task:        "review",
		System:      reviewerSystemPrompt,
		User:        string(payload),
		MaxTokens:   2048,
		Temperature: 0.2,
		// Reviewer expects a JSON array, not an object — JSONMode requests
		// json_object on providers that support it, which most accept as
		// "valid JSON" including arrays. Leave true for OpenAI-style
		// providers; downstream parser tolerates both.
		JSONMode: true,
	})
	if err != nil {
		if errors.Is(err, llm.ErrNoProviders) {
			return nil
		}
		slog.Warn("concept reviewer: provider chain failed", "error", err)
		return nil
	}
	return parseReviewerOutput(out)
}

// reviewViaSDK preserves the legacy direct-Claude path.
func (r *ConceptReviewer) reviewViaSDK(ctx context.Context, payload []byte) []ReviewResult {
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
	return parseReviewerOutput(resp.Content[0].Text)
}

// parseReviewerOutput decodes the LLM JSON-array response and validates each
// row. Unknown statuses and invalid UUIDs are dropped (logged at WARN). This
// is the data-hygiene gate that prevents bogus model output from reaching
// the DB.
func parseReviewerOutput(raw string) []ReviewResult {
	type rawResult struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	var rows []rawResult
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		slog.Warn("concept reviewer: failed to parse API response as JSON", "error", err)
		return nil
	}
	results := make([]ReviewResult, 0, len(rows))
	for _, item := range rows {
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
