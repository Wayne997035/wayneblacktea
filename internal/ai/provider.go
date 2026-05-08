package ai

import (
	"context"

	"github.com/Wayne997035/wayneblacktea/internal/search"
)

// compile-time assertion: *search.EmbeddingClient must satisfy ContextEmbeddingProvider.
var _ ContextEmbeddingProvider = (*search.EmbeddingClient)(nil)

// ContextEmbeddingProvider computes dense vector embeddings for a text string.
// Unlike EmbeddingProvider (the no-context variant used by the Stop hook /
// SQLite path), this interface accepts a context so the caller can propagate
// deadlines and cancellations — required for outbound HTTP calls such as the
// Gemini embedding API.
//
// Embed MUST be thread-safe.
// Returning (nil, nil) means "no embedding available" — callers treat nil as
// "skip write".
type ContextEmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// AnalyzerProvider runs structured analysis (sentiment, topic, learning value)
// on a text string and returns a structured AnalysisResult.
//
// Analyze MUST be thread-safe.
// Implementations return (nil, ErrAnalyzerDisabled) when no LLM provider is
// configured, allowing callers to surface a "memory-only mode" message rather
// than treating a nil result as a hard error.
type AnalyzerProvider interface {
	Analyze(ctx context.Context, content string) (*AnalysisResult, error)
}

// AnalysisResult is the structured assessment returned by AnalyzerProvider
// implementations. The shape is part of the public Discord bot contract —
// field changes are user-visible and MUST be coordinated with the downstream
// knowledge-save handler in internal/discordbot.
type AnalysisResult struct {
	Summary       string   `json:"summary"`
	KeyConcepts   []string `json:"key_concepts"`
	LearningValue int      `json:"learning_value"` // 1-5
	WorthSaving   bool     `json:"worth_saving"`
	SuggestedType string   `json:"suggested_type"` // article|til|zettelkasten|bookmark
	Tags          []string `json:"tags"`
	SkipReason    string   `json:"skip_reason,omitempty"`
}
