package sqlite_test

// SQLite parity tests for migration 000064 (embedding_provider, embedding_model,
// embedding_dim columns on session_handoffs, decisions, project_status_snapshots).
//
// These complement the testcontainers PG integration tests in internal/ai/.
// Per backend-security-design.md §6.5: dual-backend projects MUST have BOTH
// the SQLite integration test AND the testcontainers PG test.

import (
	"context"
	"testing"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// TestSQLiteEmbeddingProviderColumns_SessionHandoffs verifies that the SQLite
// schema (schema.sql) includes embedding_provider, embedding_model, and
// embedding_dim on session_handoffs, and that UpdateEmbeddingByID writes them.
func TestSQLiteEmbeddingProviderColumns_SessionHandoffs(t *testing.T) {
	db, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := sqlite.NewSessionStore(db)

	// Create a handoff to have a real row.
	h, err := store.SetHandoff(context.Background(), session.HandoffParams{
		Intent: "test embedding provider columns",
	})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}

	// Write embedding + provider metadata via UpdateEmbeddingByID.
	p := localai.HashedEmbeddingProvider{}
	vec, embErr := p.Embed("embedding provider sqlite parity test")
	if embErr != nil || len(vec) == 0 {
		t.Fatalf("embed: %v", embErr)
	}
	embBytes := localai.SerializeEmbedding(vec)
	providerTag := localai.ProviderTagFromDim(len(vec))

	if err := store.UpdateEmbeddingByID(context.Background(), h.ID, embBytes, providerTag, len(vec)); err != nil {
		t.Fatalf("UpdateEmbeddingByID: %v", err)
	}

	// Read back via raw SQL to confirm the columns were written.
	var gotProvider string
	var gotDim int
	row := db.QueryRowContext(context.Background(),
		`SELECT embedding_provider, embedding_dim FROM session_handoffs WHERE id = ?`, h.ID.String())
	if err := row.Scan(&gotProvider, &gotDim); err != nil {
		t.Fatalf("scan: %v (columns missing — schema.sql not updated?)", err)
	}
	if gotProvider != "hashed" {
		t.Errorf("embedding_provider = %q, want %q", gotProvider, "hashed")
	}
	if gotDim != localai.HashedEmbeddingDims {
		t.Errorf("embedding_dim = %d, want %d", gotDim, localai.HashedEmbeddingDims)
	}
}

// TestSQLiteEmbeddingProviderColumns_Decisions verifies that the decisions table
// has the 000064 columns in SQLite.
func TestSQLiteEmbeddingProviderColumns_Decisions(t *testing.T) {
	db, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Insert a row with the new columns directly to confirm they exist.
	id := uuid.New()
	const q = `INSERT INTO decisions
		(id, title, context, decision, rationale, embedding_provider, embedding_dim, created_at)
		VALUES (?, 'test', 'test', 'test', 'test', ?, ?, datetime('now'))`
	if err := db.ExecContext(context.Background(), q, id.String(), "gemini", 768); err != nil {
		t.Fatalf("insert: %v (embedding_provider or embedding_dim column missing from schema.sql?)", err)
	}

	var gotProvider string
	var gotDim int
	const sel = `SELECT embedding_provider, embedding_dim FROM decisions WHERE id = ?`
	if err := db.QueryRowContext(context.Background(), sel, id.String()).Scan(&gotProvider, &gotDim); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if gotProvider != "gemini" {
		t.Errorf("embedding_provider = %q, want %q", gotProvider, "gemini")
	}
	if gotDim != 768 {
		t.Errorf("embedding_dim = %d, want 768", gotDim)
	}
}

// TestSQLiteEmbeddingProviderColumns_ProjectStatusSnapshots verifies that
// project_status_snapshots has the 000064 columns in SQLite.
func TestSQLiteEmbeddingProviderColumns_ProjectStatusSnapshots(t *testing.T) {
	db, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	id := uuid.New().String()
	const q = `INSERT INTO project_status_snapshots
		(id, slug, embedding_provider, embedding_model, embedding_dim)
		VALUES (?, 'test-slug', ?, ?, ?)`
	if err := db.ExecContext(context.Background(), q, id, "hashed", "sha256-32", 32); err != nil {
		t.Fatalf("insert: %v (new columns missing from schema.sql?)", err)
	}

	var provider, model string
	var dim int
	const sel = `SELECT embedding_provider, embedding_model, embedding_dim
		FROM project_status_snapshots WHERE id = ?`
	if err := db.QueryRowContext(context.Background(), sel, id).Scan(&provider, &model, &dim); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if provider != "hashed" || model != "sha256-32" || dim != 32 {
		t.Errorf("got provider=%q model=%q dim=%d, want hashed/sha256-32/32", provider, model, dim)
	}
}

// TestSQLiteSearchByCosine_ProviderFilter verifies that SearchByCosine on the
// SQLite session store only returns rows matching the query's provider tag.
func TestSQLiteSearchByCosine_ProviderFilter(t *testing.T) {
	db, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := sqlite.NewSessionStore(db)

	// Insert a 'gemini' 768-dim row.
	geminiVec := make([]float32, localai.GeminiEmbeddingDims)
	for i := range geminiVec {
		geminiVec[i] = float32(i) / float32(localai.GeminiEmbeddingDims)
	}
	geminiBytes := localai.SerializeEmbedding(geminiVec)

	geminiHandoff, err := store.SetHandoff(context.Background(), session.HandoffParams{
		Intent: "gemini-provider-row",
	})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}
	err = store.UpdateEmbeddingByID(
		context.Background(), geminiHandoff.ID, geminiBytes, "gemini", localai.GeminiEmbeddingDims,
	)
	if err != nil {
		t.Fatalf("UpdateEmbeddingByID gemini: %v", err)
	}

	// Insert a 'hashed' 32-dim row.
	p := localai.HashedEmbeddingProvider{}
	hashedVec, _ := p.Embed("hashed provider row for filter test")
	hashedBytes := localai.SerializeEmbedding(hashedVec)

	hashedHandoff, err := store.SetHandoff(context.Background(), session.HandoffParams{
		Intent: "hashed-provider-row",
	})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}
	err = store.UpdateEmbeddingByID(
		context.Background(), hashedHandoff.ID, hashedBytes, "hashed", localai.HashedEmbeddingDims,
	)
	if err != nil {
		t.Fatalf("UpdateEmbeddingByID hashed: %v", err)
	}

	// Query using a hashed vector — should only see hashed rows.
	results, err := store.SearchByCosine(context.Background(), hashedVec, 10)
	if err != nil {
		t.Fatalf("SearchByCosine: %v", err)
	}
	for _, r := range results {
		if r.Intent == "gemini-provider-row" {
			t.Error("SearchByCosine with hashed query returned a 'gemini' row — provider filter not working")
		}
	}

	// CosineSimilarity of hashed vs gemini must be 0 (dim mismatch).
	crossSim := localai.CosineSimilarity(hashedVec, geminiVec)
	if crossSim != 0.0 {
		t.Errorf("cross-provider cosine similarity = %.4f, want 0.0", crossSim)
	}
}
