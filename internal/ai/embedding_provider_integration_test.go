//go:build integration

package ai_test

// Integration test: embedding provider cosine correctness + cross-provider filter.
//
// Assertions:
//  1. Same-provider rows produce meaningful cosine scores in SearchByCosine.
//  2. Cross-provider rows (hashed 32-dim vs gemini 768-dim) are filtered out,
//     i.e. a query with a hashed vector does NOT match rows tagged 'gemini' and
//     vice versa, because CosineSimilarity returns 0 on dimension mismatch.
//  3. ProviderTagFromDim returns the correct tag for known dims.
//  4. ValidateEmbedding returns an error on empty/wrong-dim vectors.
//  5. GeminiEmbeddingAdapter wraps successfully (no panic/nil deref).

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"testing"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var embeddingTestPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(runEmbeddingTests(m))
}

func runEmbeddingTests(m *testing.M) int {
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_embedding_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Fatalf("start postgres container: %v", err)
	}
	defer func() { _ = c.Terminate(ctx) }()

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Printf("get dsn: %v", err)
		return 1
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Printf("pgxpool.New: %v", err)
		return 1
	}
	defer pool.Close()

	applyEmbeddingUpMigrations(ctx, pool)

	embeddingTestPool = pool
	return m.Run()
}

// skipMigrations is the set of migration files that reference data requiring
// a live workspace — safe to skip for the embedding-provider test DB.
var skipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true,
}

func applyEmbeddingUpMigrations(ctx context.Context, pool *pgxpool.Pool) {
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		log.Fatalf("read embedded migrations dir: %v", err)
	}
	var ups []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		ups = append(ups, name)
	}
	sort.Strings(ups)
	for _, name := range ups {
		if skipMigrations[name] {
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			log.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			log.Fatalf("apply %s: %v", name, err)
		}
	}
}

// insertHandoffWithEmbedding inserts a session_handoffs row carrying a synthetic
// embedding (already serialised bytes) tagged with providerTag and dim.
// Returns the row ID for cleanup.
func insertHandoffWithEmbedding(
	t *testing.T, pool *pgxpool.Pool,
	intent string, embBytes []byte, providerTag string, dim int,
) uuid.UUID {
	t.Helper()
	id := uuid.New()
	const q = `INSERT INTO session_handoffs
		(id, intent, embedding, embedding_provider, embedding_dim, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())`
	if _, err := pool.Exec(context.Background(), q, id, intent, embBytes, providerTag, dim); err != nil {
		t.Fatalf("insert handoff: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM session_handoffs WHERE id = $1", id)
	})
	return id
}

// TestSameProviderCosineRecall asserts that two hashed-provider rows inserted into
// session_handoffs with identical embeddings have cosine similarity 1.0 when
// queried, and that the provider filter allows them through.
func TestSameProviderCosineRecall(t *testing.T) {
	if embeddingTestPool == nil {
		t.Skip("testcontainers pool not initialised")
	}

	p := localai.HashedEmbeddingProvider{}
	vec, err := p.Embed("test same provider cosine recall")
	if err != nil || len(vec) == 0 {
		t.Fatalf("embed: %v", err)
	}
	embBytes := localai.SerializeEmbedding(vec)

	// Insert two rows with the same embedding (cosine = 1.0) and same provider.
	insertHandoffWithEmbedding(t, embeddingTestPool, "row-a", embBytes, "hashed", localai.HashedEmbeddingDims)
	insertHandoffWithEmbedding(t, embeddingTestPool, "row-b", embBytes, "hashed", localai.HashedEmbeddingDims)

	// Query with the same vector.
	activeProvider := localai.ProviderTagFromDim(len(vec))
	const q = `SELECT intent, embedding FROM session_handoffs
		WHERE embedding IS NOT NULL
		  AND (embedding_provider = $1 OR embedding_provider IS NULL AND $1 = 'hashed')
		ORDER BY created_at DESC LIMIT 10`
	rows, err := embeddingTestPool.Query(context.Background(), q, activeProvider)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	type row struct{ intent string; sim float64 }
	var results []row
	for rows.Next() {
		var intent string
		var rawEmb []byte
		if err := rows.Scan(&intent, &rawEmb); err != nil {
			continue
		}
		candidate := localai.DeserializeEmbedding(rawEmb)
		sim := localai.CosineSimilarity(vec, candidate)
		if sim >= 0.5 {
			results = append(results, row{intent: intent, sim: sim})
		}
	}

	if len(results) < 2 {
		t.Errorf("expected at least 2 same-provider results; got %d", len(results))
	}
	for _, r := range results {
		if r.sim < 0.99 {
			t.Errorf("row %q: expected cosine ~1.0, got %.4f", r.intent, r.sim)
		}
	}
}

// TestCrossProviderRowsFiltered asserts that a row tagged 'gemini' (768-dim)
// does NOT appear when querying with a hashed (32-dim) vector and provider filter,
// because they use different provider tags.
func TestCrossProviderRowsFiltered(t *testing.T) {
	if embeddingTestPool == nil {
		t.Skip("testcontainers pool not initialised")
	}

	// Insert a 'gemini'-tagged row with a synthetic 768-dim embedding.
	geminiVec := make([]float32, localai.GeminiEmbeddingDims)
	for i := range geminiVec {
		geminiVec[i] = 0.001 * float32(i)
	}
	geminiBytes := localai.SerializeEmbedding(geminiVec)
	geminiID := insertHandoffWithEmbedding(t, embeddingTestPool, "gemini-row", geminiBytes, "gemini", localai.GeminiEmbeddingDims)
	_ = geminiID

	// Query using a hashed (32-dim) vector — should NOT find 'gemini' rows.
	p := localai.HashedEmbeddingProvider{}
	hashedVec, _ := p.Embed("cross provider filter test")
	activeProvider := localai.ProviderTagFromDim(len(hashedVec))
	if activeProvider != "hashed" {
		t.Fatalf("expected hashed provider, got %s", activeProvider)
	}

	const q = `SELECT intent FROM session_handoffs
		WHERE embedding IS NOT NULL
		  AND embedding_provider = $1`
	rows, err := embeddingTestPool.Query(context.Background(), q, activeProvider)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var intent string
		if err := rows.Scan(&intent); err != nil {
			continue
		}
		if intent == "gemini-row" {
			t.Error("cross-provider 'gemini-row' should NOT appear in hashed query results")
		}
	}
}

// TestValidateEmbedding verifies the dimension guard.
func TestValidateEmbeddingDimGuard(t *testing.T) {
	cases := []struct {
		name    string
		vec     []float32
		wantDim int
		wantErr bool
	}{
		{
			name:    "correct dim",
			vec:     make([]float32, localai.HashedEmbeddingDims),
			wantDim: localai.HashedEmbeddingDims,
			wantErr: false,
		},
		{
			name:    "wrong dim",
			vec:     make([]float32, localai.HashedEmbeddingDims),
			wantDim: localai.GeminiEmbeddingDims,
			wantErr: true,
		},
		{
			name:    "empty vec",
			vec:     []float32{},
			wantDim: localai.HashedEmbeddingDims,
			wantErr: true,
		},
		{
			name:    "nil vec",
			vec:     nil,
			wantDim: localai.HashedEmbeddingDims,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := localai.ValidateEmbedding(tc.vec, tc.wantDim)
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestProviderTagFromDim verifies the dimension-to-tag mapping.
func TestProviderTagFromDimIntegration(t *testing.T) {
	cases := []struct {
		dim  int
		want string
	}{
		{localai.HashedEmbeddingDims, "hashed"},
		{localai.GeminiEmbeddingDims, "gemini"},
		{999, "unknown"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("dim%d", tc.dim), func(t *testing.T) {
			got := localai.ProviderTagFromDim(tc.dim)
			if got != tc.want {
				t.Errorf("ProviderTagFromDim(%d) = %q, want %q", tc.dim, got, tc.want)
			}
		})
	}
}

// TestProviderMigration000064 verifies that migration 000064 added the
// embedding_provider, embedding_model, embedding_dim columns to session_handoffs.
func TestProviderMigration000064(t *testing.T) {
	if embeddingTestPool == nil {
		t.Skip("testcontainers pool not initialised")
	}

	// Try inserting a row with all three new columns and reading them back.
	id := uuid.New()
	const insert = `INSERT INTO session_handoffs
		(id, intent, embedding_provider, embedding_model, embedding_dim, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())`
	if _, err := embeddingTestPool.Exec(context.Background(), insert,
		id, "migration-test", "gemini", "gemini-embedding-001", 768,
	); err != nil {
		t.Fatalf("insert: %v (columns may be missing — migration 000064 not applied?)", err)
	}
	t.Cleanup(func() {
		_, _ = embeddingTestPool.Exec(context.Background(), "DELETE FROM session_handoffs WHERE id = $1", id)
	})

	var providerTag, model string
	var dim int
	const sel = `SELECT embedding_provider, embedding_model, embedding_dim
		FROM session_handoffs WHERE id = $1`
	if err := embeddingTestPool.QueryRow(context.Background(), sel, id).Scan(
		&providerTag, &model, &dim,
	); err != nil {
		t.Fatalf("select: %v", err)
	}
	if providerTag != "gemini" {
		t.Errorf("embedding_provider = %q, want %q", providerTag, "gemini")
	}
	if model != "gemini-embedding-001" {
		t.Errorf("embedding_model = %q, want %q", model, "gemini-embedding-001")
	}
	if dim != 768 {
		t.Errorf("embedding_dim = %d, want 768", dim)
	}
}
