package ai_test

import (
	"strings"
	"testing"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
)

func TestHashedEmbedding_NilAndEmpty(t *testing.T) {
	p := localai.HashedEmbeddingProvider{}
	for _, tc := range []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"whitespace only", "   \t\n  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vec, err := p.Embed(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if vec != nil {
				t.Errorf("expected nil, got len=%d", len(vec))
			}
		})
	}
}

func TestHashedEmbedding_Returns32Dims(t *testing.T) {
	p := localai.HashedEmbeddingProvider{}
	vec, err := p.Embed("hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 32 {
		t.Errorf("expected 32 dims, got %d", len(vec))
	}
}

func TestHashedEmbedding_Deterministic(t *testing.T) {
	p := localai.HashedEmbeddingProvider{}
	v1, _ := p.Embed("deterministic test input")
	v2, _ := p.Embed("deterministic test input")
	if len(v1) != len(v2) {
		t.Fatalf("length mismatch: %d vs %d", len(v1), len(v2))
	}
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Errorf("dim %d: got %f vs %f", i, v1[i], v2[i])
		}
	}
}

func TestHashedEmbedding_Normalisation(t *testing.T) {
	p := localai.HashedEmbeddingProvider{}
	v1, _ := p.Embed("Hello   World")
	v2, _ := p.Embed("hello world")
	if len(v1) != len(v2) {
		t.Fatalf("length mismatch: %d vs %d", len(v1), len(v2))
	}
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Errorf("dim %d: got %f vs %f — case/space normalisation failed", i, v1[i], v2[i])
		}
	}
}

func TestHashedEmbedding_DifferentInputsDifferentVectors(t *testing.T) {
	p := localai.HashedEmbeddingProvider{}
	v1, _ := p.Embed("alpha beta gamma")
	v2, _ := p.Embed("zeta eta theta")
	if len(v1) == 0 || len(v2) == 0 {
		t.Fatal("got nil vector")
	}
	allSame := true
	for i := range v1 {
		if v1[i] != v2[i] {
			allSame = false
			break
		}
	}
	if allSame {
		t.Error("different inputs should produce different vectors")
	}
}

func TestHashedEmbedding_LargeInputCapped(t *testing.T) {
	p := localai.HashedEmbeddingProvider{}
	huge := strings.Repeat("x", 128*1024)
	vec, err := p.Embed(huge)
	if err != nil {
		t.Fatalf("unexpected error on large input: %v", err)
	}
	if len(vec) != 32 {
		t.Errorf("expected 32 dims, got %d", len(vec))
	}
}

func TestHashedEmbedding_AllDimsInRange(t *testing.T) {
	p := localai.HashedEmbeddingProvider{}
	vec, _ := p.Embed("boundary check")
	for i, v := range vec {
		if v < -1.0 || v > 1.0 {
			t.Errorf("dim %d value %f out of [-1,1]", i, v)
		}
	}
}

func TestNewEmbeddingProvider_Defaults(t *testing.T) {
	t.Setenv("EMBEDDING_PROVIDER", "")
	p := localai.NewEmbeddingProvider()
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	vec, err := p.Embed("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 32 {
		t.Errorf("expected 32-dim vector, got %d", len(vec))
	}
}

func TestNewEmbeddingProvider_Hashed(t *testing.T) {
	t.Setenv("EMBEDDING_PROVIDER", "hashed")
	p := localai.NewEmbeddingProvider()
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	vec, err := p.Embed("hashed provider test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 32 {
		t.Errorf("expected 32-dim vector, got %d", len(vec))
	}
}

func TestNewEmbeddingProvider_UnknownFallsBackToHashed(t *testing.T) {
	t.Setenv("EMBEDDING_PROVIDER", "unknown-provider")
	p := localai.NewEmbeddingProvider()
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	vec, err := p.Embed("fallback test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 32 {
		t.Errorf("expected 32-dim vector from fallback, got %d", len(vec))
	}
}

func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		name    string
		a, b    []float32
		wantMin float64
		wantMax float64
	}{
		{
			name:    "identical vectors → similarity 1.0",
			a:       []float32{1, 0, 0},
			b:       []float32{1, 0, 0},
			wantMin: 0.9999,
			wantMax: 1.0001,
		},
		{
			name:    "orthogonal vectors → similarity 0.0",
			a:       []float32{1, 0, 0},
			b:       []float32{0, 1, 0},
			wantMin: -0.0001,
			wantMax: 0.0001,
		},
		{
			name:    "opposite vectors → similarity -1.0",
			a:       []float32{1, 0, 0},
			b:       []float32{-1, 0, 0},
			wantMin: -1.0001,
			wantMax: -0.9999,
		},
		{
			name:    "empty slice a → 0",
			a:       []float32{},
			b:       []float32{1, 0},
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "nil a → 0",
			a:       nil,
			b:       []float32{1, 0},
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "dimension mismatch → 0",
			a:       []float32{1, 2, 3},
			b:       []float32{1, 2},
			wantMin: 0,
			wantMax: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := localai.CosineSimilarity(tc.a, tc.b)
			if got < tc.wantMin || got > tc.wantMax {
				t.Errorf("CosineSimilarity = %f, want [%f, %f]", got, tc.wantMin, tc.wantMax)
			}
		})
	}
}

func TestCosineSimilarity_SameTextSameHash(t *testing.T) {
	p := localai.HashedEmbeddingProvider{}
	v1, _ := p.Embed("hello world cosine")
	v2, _ := p.Embed("hello world cosine")
	sim := localai.CosineSimilarity(v1, v2)
	if sim < 0.9999 {
		t.Errorf("same text should have cosine ~1.0, got %f", sim)
	}
}

func TestSerializeDeserializeEmbedding(t *testing.T) {
	t.Run("round-trip preserves values", func(t *testing.T) {
		input := []float32{0.1, -0.5, 0.9, 0.0, -1.0, 1.0}
		b := localai.SerializeEmbedding(input)
		got := localai.DeserializeEmbedding(b)
		if len(got) != len(input) {
			t.Fatalf("length mismatch: got %d, want %d", len(got), len(input))
		}
		for i := range input {
			if got[i] != input[i] {
				t.Errorf("dim %d: got %f, want %f", i, got[i], input[i])
			}
		}
	})

	t.Run("nil input serializes to nil", func(t *testing.T) {
		b := localai.SerializeEmbedding(nil)
		if b != nil {
			t.Errorf("expected nil, got %v", b)
		}
	})

	t.Run("empty slice serializes to nil", func(t *testing.T) {
		b := localai.SerializeEmbedding([]float32{})
		if b != nil {
			t.Errorf("expected nil for empty slice, got len=%d", len(b))
		}
	})

	t.Run("nil bytes deserializes to nil", func(t *testing.T) {
		vec := localai.DeserializeEmbedding(nil)
		if vec != nil {
			t.Errorf("expected nil, got %v", vec)
		}
	})

	t.Run("truncated bytes (not multiple of 4) returns nil", func(t *testing.T) {
		vec := localai.DeserializeEmbedding([]byte{0x01, 0x02, 0x03}) // 3 bytes
		if vec != nil {
			t.Errorf("expected nil for corrupt bytes, got %v", vec)
		}
	})

	t.Run("hashed embedding round-trip preserves cosine identity", func(t *testing.T) {
		p := localai.HashedEmbeddingProvider{}
		original, _ := p.Embed("round-trip cosine test")
		b := localai.SerializeEmbedding(original)
		restored := localai.DeserializeEmbedding(b)
		sim := localai.CosineSimilarity(original, restored)
		if sim < 0.9999 {
			t.Errorf("round-trip cosine similarity %f < 0.9999", sim)
		}
	})
}
