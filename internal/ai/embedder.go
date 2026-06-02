package ai

// Package ai provides AI utilities for the wayneblacktea personal OS.
// This file defines the EmbeddingProvider interface, the HashedEmbeddingProvider
// deterministic placeholder, and the GeminiEmbeddingProvider real provider.
//
// Provider selection via EMBEDDING_PROVIDER env var:
//   - "hashed"              — deterministic SHA-256 hash, 32-dim, no external deps
//   - ""  / "gemini"        — Gemini gemini-embedding-001, 768-dim, requires GEMINI_API_KEY
//   - "openai-compatible"   — OpenAI-compatible endpoint (requires EMBEDDING_BASE_URL etc.)
//
// Fail-soft: if "gemini" is selected but GEMINI_API_KEY is absent the provider
// silently falls back to HashedEmbeddingProvider and logs a slog.Warn. This keeps
// the Stop hook operational on machines without a key while preventing silent
// dimension mismatches in production (which would cause CosineSimilarity to return 0).
//
// Dimension guard: ValidateEmbedding must be called before SerializeEmbedding on
// write paths. Rows written by the hashed fallback have embedding_provider='hashed'
// and are excluded from real-space cosine recall.

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/Wayne997035/wayneblacktea/internal/search"
)

const (
	// HashedEmbeddingDims is the dimension of hashed embeddings.
	// 32 dims keeps storage tiny while still exercising the full pipeline.
	// Exported so callers can tag embedding_provider='hashed' rows correctly.
	HashedEmbeddingDims = 32

	// GeminiEmbeddingDims is the expected dimension returned by Gemini gemini-embedding-001
	// with OutputDimensionality=768 (the default used by search.EmbeddingClient).
	// Exported so callers can tag embedding_provider='gemini' rows correctly.
	GeminiEmbeddingDims = 768

	// maxEmbedInputBytes is the upper limit on text fed into Embed().
	// Matching the maxTranscriptLen cap from summarizer.go (64 KB).
	maxEmbedInputBytes = 64 * 1024

	// geminiAdaptorTimeout is the context deadline injected by GeminiEmbeddingAdapter.Embed
	// so the no-context EmbeddingProvider interface honours the ≤5 s cap
	// documented in the interface contract.
	geminiAdaptorTimeout = 5 * time.Second
)

// EmbeddingProvider is the stable interface for generating float32 embedding
// vectors from text.  Callers treat the dimensionality as opaque — the
// concrete provider determines the length.
//
// Embed MUST be:
//   - Deterministic for the same input (required for reproducible tests)
//   - Thread-safe
//   - Fast enough to call in the Stop hook (≤50 ms for hashed; ≤5 s for real APIs)
//
// Callers MUST check len(vec)==0 before storing.
type EmbeddingProvider interface {
	// Embed returns a float32 vector for text, or nil on error.
	// Empty input returns (nil, nil) — callers treat nil as "skip write".
	Embed(text string) ([]float32, error)
}

// HashedEmbeddingProvider implements EmbeddingProvider using SHA-256.
// It is the v1 placeholder (see package doc above).
//
//nolint:recvcheck // value receiver intentional: no mutable state
type HashedEmbeddingProvider struct{}

// Embed produces a deterministic 32-dim hashed vector.
// Empty or whitespace-only text returns (nil, nil).
//
//nolint:recvcheck // value receiver intentional: no mutable state
func (HashedEmbeddingProvider) Embed(text string) ([]float32, error) {
	text = normalise(text)
	if text == "" {
		return nil, nil
	}
	if len(text) > maxEmbedInputBytes {
		text = text[:maxEmbedInputBytes]
	}

	hash := sha256.Sum256([]byte(text)) // always 32 bytes
	vec := make([]float32, HashedEmbeddingDims)
	for i := range HashedEmbeddingDims {
		// Map each byte to [-1.0, 1.0].
		vec[i] = float32(int(hash[i])-128) / 128.0
	}
	return vec, nil
}

// GeminiEmbeddingAdapter wraps a search.ContextEmbedder behind the no-context
// EmbeddingProvider interface by injecting a short timeout on each call.
// This allows the Stop-hook path (which uses EmbeddingProvider) to benefit from
// the real Gemini 768-dim semantic embeddings without changing the interface.
type GeminiEmbeddingAdapter struct {
	inner search.ContextEmbedder
}

// Embed calls the underlying ContextEmbedder with a 5 s timeout context,
// as required by the EmbeddingProvider interface contract.
//
//nolint:recvcheck // value receiver intentional: no mutable state
func (a GeminiEmbeddingAdapter) Embed(text string) ([]float32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), geminiAdaptorTimeout)
	defer cancel()
	vec, err := a.inner.Embed(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("gemini embedding: %w", err)
	}
	return vec, nil
}

// ProviderTagFromDim returns the provider string tag for a given embedding dimension.
// Used to build same-provider WHERE clauses in SearchByCosine queries.
func ProviderTagFromDim(dim int) string {
	switch dim {
	case HashedEmbeddingDims:
		return "hashed"
	case GeminiEmbeddingDims:
		return "gemini"
	default:
		return "unknown"
	}
}

// ValidateEmbedding returns an error if vec does not match wantDim.
// Call before SerializeEmbedding to guard against dimension mismatches caused
// by mixed providers (e.g. hashed 32-dim vs Gemini 768-dim) writing to the
// same BYTEA column.
func ValidateEmbedding(vec []float32, wantDim int) error {
	if len(vec) == 0 {
		return fmt.Errorf("embedding vector is empty")
	}
	if len(vec) != wantDim {
		return fmt.Errorf("embedding dimension mismatch: got %d, want %d", len(vec), wantDim)
	}
	return nil
}

// normalise lower-cases and collapses all runs of unicode whitespace to a
// single ASCII space.
func normalise(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// Replace each whitespace rune with a space, then collapse repeated spaces.
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
	// Collapse consecutive spaces produced by the Map pass above.
	var prev rune
	return strings.Map(func(r rune) rune {
		if r == ' ' && prev == ' ' {
			return -1 // drop
		}
		prev = r
		return r
	}, s)
}

// NewEmbeddingProvider returns the EmbeddingProvider selected by the
// EMBEDDING_PROVIDER environment variable.
//
// Supported values:
//   - ""  / "gemini"        — Gemini gemini-embedding-001, 768-dim (default).
//     Requires GEMINI_API_KEY. If the key is absent, falls back to HashedEmbeddingProvider
//     and logs a slog.Warn — rows written via fallback carry embedding_provider='hashed'.
//   - "hashed"              — deterministic SHA-256 hash, 32-dim; deterministic tests / CI.
//   - "openai-compatible"   — delegates to search.NewEmbeddingClientFromEnv() for
//     the openai-compatible branch; same key-absent fail-soft as gemini.
//
// Never returns an error for key-absent cases — fail-soft to hashed instead.
// Returns an error only for completely unknown provider names.
func NewEmbeddingProvider() (EmbeddingProvider, error) {
	providerEnv := strings.ToLower(strings.TrimSpace(os.Getenv("EMBEDDING_PROVIDER")))
	switch providerEnv {
	case "hashed":
		return HashedEmbeddingProvider{}, nil

	case "", "gemini", "openai-compatible":
		// Use the ctx-aware search client and wrap it behind the no-ctx interface.
		client := search.NewEmbeddingClientFromEnv()
		if client == nil {
			// Key absent or misconfigured — fall back to hashed with a warn.
			slog.Warn("ai: real embedding provider not configured; falling back to hashed 32-dim",
				"hint", "set GEMINI_API_KEY or EMBEDDING_API_KEY for semantic cosine recall")
			return HashedEmbeddingProvider{}, nil
		}
		return GeminiEmbeddingAdapter{inner: client}, nil

	default:
		return nil, fmt.Errorf(
			"unknown embedding provider %q; supported: \"\", \"gemini\", \"hashed\", \"openai-compatible\"",
			os.Getenv("EMBEDDING_PROVIDER"),
		)
	}
}

// CosineSimilarity computes the cosine similarity between two float32 vectors.
// Returns 0 when either vector is zero-length or dimensions differ.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// SerializeEmbedding encodes a float32 vector to little-endian bytes for
// storage in Postgres BYTEA / SQLite BLOB columns.
// Returns nil when vec is empty.
func SerializeEmbedding(vec []float32) []byte {
	if len(vec) == 0 {
		return nil
	}
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// DeserializeEmbedding decodes bytes previously written by SerializeEmbedding
// into a float32 slice.  Returns nil when b is empty or has a non-multiple-of-4
// length (defensive: treats corrupt bytes as no embedding rather than panicking).
func DeserializeEmbedding(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	dims := len(b) / 4
	vec := make([]float32, dims)
	for i := range dims {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return vec
}
