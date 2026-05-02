package ai

// Package ai provides AI utilities for the wayneblacktea personal OS.
// This file defines the EmbeddingProvider interface and the HashedEmbeddingProvider
// v1 placeholder implementation.
//
// V1 PLACEHOLDER NOTE: HashedEmbeddingProvider produces a deterministic 32-dim
// float32 vector by SHA-256 hashing the normalised input text and mapping each
// byte pair to a float in [-1, 1].  This is NOT semantically meaningful —
// cosine similarity between two hashed vectors will not reflect semantic
// relatedness.  It exists solely to wire the embedding pipeline end-to-end so
// that:
//  1. session_handoffs.embedding is written (non-empty) by the Stop hook.
//  2. SearchByCosine has a callable path to exercise in tests.
//  3. The EmbeddingProvider interface is stable for the swap in 5/5 sprint.
//
// TODO(5/5): swap HashedEmbeddingProvider for a real semantic provider
// (e.g. OpenAI text-embedding-3-small or Gemini gemini-embedding-001)
// by implementing EmbeddingProvider and setting EMBEDDING_PROVIDER=openai.

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"os"
	"strings"
	"unicode"
)

const (
	// embeddingDims is the dimension of hashed embeddings.
	// 32 dims keeps storage tiny while still exercising the full pipeline.
	// TODO(5/5): real semantic providers use 768 or 1536 dims.
	embeddingDims = 32

	// maxEmbedInputBytes is the upper limit on text fed into Embed().
	// Matching the maxTranscriptLen cap from summarizer.go (64 KB).
	maxEmbedInputBytes = 64 * 1024
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
	vec := make([]float32, embeddingDims)
	for i := range embeddingDims {
		// Map each byte to [-1.0, 1.0].
		vec[i] = float32(int(hash[i])-128) / 128.0
	}
	return vec, nil
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
// EMBEDDING_PROVIDER environment variable.  Defaults to "hashed" (v1
// placeholder) when the variable is unset or unrecognised.
//
// Supported values:
//   - "hashed" (default) — deterministic SHA-256 hash, no external deps
//
// TODO(5/5): add "openai" and "gemini" cases here.
func NewEmbeddingProvider() EmbeddingProvider {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("EMBEDDING_PROVIDER"))) {
	case "hashed", "":
		return HashedEmbeddingProvider{}
	default:
		// Unknown provider → fall back to hashed so the hook never fails.
		return HashedEmbeddingProvider{}
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
