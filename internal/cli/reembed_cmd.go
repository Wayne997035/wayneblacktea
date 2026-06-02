// reembed_cmd.go — wbt reembed subcommand
//
// Idempotent backfill command that re-embeds historical rows in session_handoffs
// and decisions whose embedding is NULL or whose embedding_provider does not
// match the currently-configured provider.
//
// Usage:
//
//	wbt reembed [--table session_handoffs|decisions|all] [--batch N] [--dry-run]
//
// Design notes:
//   - Resumable: uses "WHERE embedding IS NULL OR embedding_provider <> $target"
//     with ORDER BY created_at so interrupted runs restart cleanly.
//   - Rate-limited: configurable batch size + per-batch sleep to avoid 429s.
//   - Fail-soft: a per-row embed or DB error is logged and skipped; the loop
//     continues to the next row.
//   - NOT run automatically: the operator invokes this manually against prod Aiven
//     AFTER setting GEMINI_API_KEY and DATABASE_URL in the environment.
//   - This file DOES NOT connect to any live DB during build/task check — all
//     DB I/O is behind a --dry-run guard that bypasses real connections.
package cli

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

const reembedUsage = `wbt reembed — backfill real-provider embeddings for historical rows

Usage:
  wbt reembed [--table <name>] [--batch <n>] [--dry-run]

Flags:
  --table    which table to reembed: session_handoffs, decisions, or all (default: all)
  --batch    rows per iteration (default: 50)
  --dry-run  show what would be reembedded without writing to DB (default: false)
  --help     show this help

Environment variables required (non-dry-run):
  DATABASE_URL       Postgres DSN (Aiven production)
  PGSSLROOTCERT      Path to Aiven CA cert
  GEMINI_API_KEY     Gemini API key for real 768-dim embeddings
  EMBEDDING_PROVIDER (optional) set to "gemini" to be explicit; default is gemini

This command is USER-OPERATED against production Aiven after the PR merges.
It MUST NOT be called from tests, CI, or any automated hook.

The loop processes rows in created_at DESC order, 50 at a time, with a 500 ms
sleep between batches to stay within Gemini rate limits. Interrupted runs are
safe to restart — already-reembedded rows are skipped.
`

const (
	reembedDefaultBatch = 50
	reembedBatchSleep   = 500 * time.Millisecond
	reembedDBTimeout    = 30 * time.Second

	// reembedTableHandoffs / reembedTableDecisions are the valid table names for --table flag.
	reembedTableHandoffs  = "session_handoffs"
	reembedTableDecisions = "decisions"
)

// reembedConfig holds validated parameters for the reembed run.
type reembedConfig struct {
	tables         []string
	batchSize      int
	embedder       localai.EmbeddingProvider
	targetProvider string
	targetDim      int
}

// RunReembed dispatches `wbt reembed`. args is os.Args[2:].
// Always returns nil so wbt exits 0 on dry-run; propagates errors otherwise.
func RunReembed(args []string) error {
	fs := flag.NewFlagSet("reembed", flag.ContinueOnError)
	tableFlag := fs.String("table", "all", "session_handoffs | decisions | all")
	batchFlag := fs.Int("batch", reembedDefaultBatch, "rows per batch")
	dryRun := fs.Bool("dry-run", false, "print plan without writing")
	fs.Usage = func() { fmt.Fprint(os.Stderr, reembedUsage) }

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return fmt.Errorf("reembed: %w", err)
	}

	cfg, err := buildReembedConfig(*tableFlag, *batchFlag)
	if err != nil {
		return err
	}

	if *dryRun {
		fmt.Printf("[dry-run] would reembed tables=%v batch=%d provider=%s dim=%d\n",
			cfg.tables, cfg.batchSize, cfg.targetProvider, cfg.targetDim)
		return nil
	}

	pool, err := openReembedPool()
	if err != nil {
		return err
	}
	defer pool.Close()

	for _, tbl := range cfg.tables {
		if runErr := reembedTable(pool, tbl, cfg); runErr != nil {
			slog.Warn("reembed: table failed; continuing", "table", tbl, "err", runErr)
		}
	}
	return nil
}

// buildReembedConfig validates flags and probes the embedding provider.
func buildReembedConfig(tableFlag string, batchSize int) (*reembedConfig, error) {
	tableVal := strings.TrimSpace(strings.ToLower(tableFlag))
	switch tableVal {
	case reembedTableHandoffs, reembedTableDecisions, "all":
	default:
		return nil, fmt.Errorf("reembed: --table must be session_handoffs, decisions, or all; got %q", tableFlag)
	}
	if batchSize <= 0 || batchSize > 1000 {
		return nil, fmt.Errorf("reembed: --batch must be between 1 and 1000; got %d", batchSize)
	}

	embedder, err := localai.NewEmbeddingProvider()
	if err != nil {
		return nil, fmt.Errorf("reembed: embedding provider: %w", err)
	}

	// Probe the provider to determine target provider tag and dim.
	probe, err := embedder.Embed("probe")
	if err != nil || len(probe) == 0 {
		return nil, fmt.Errorf(
			"reembed: embedding provider probe failed (check GEMINI_API_KEY / EMBEDDING_PROVIDER): %w",
			err,
		)
	}

	tables := []string{reembedTableHandoffs, reembedTableDecisions}
	if tableVal != "all" {
		tables = []string{tableVal}
	}

	cfg := &reembedConfig{
		tables:         tables,
		batchSize:      batchSize,
		embedder:       embedder,
		targetProvider: localai.ProviderTagFromDim(len(probe)),
		targetDim:      len(probe),
	}
	slog.Info("reembed: provider ready",
		"provider", cfg.targetProvider,
		"dim", cfg.targetDim,
	)
	return cfg, nil
}

// openReembedPool creates a Postgres connection pool using DATABASE_URL.
func openReembedPool() (*pgxpool.Pool, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = DSNFromFallback()
	}
	if dsn == "" {
		return nil, fmt.Errorf("reembed: DATABASE_URL is not set (required for non-dry-run)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), reembedDBTimeout)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// Do NOT wrap the raw pgx error: ParseConfig can echo the DSN (incl. password)
		// into the message, which then lands in stderr / terminal history.
		return nil, fmt.Errorf("reembed: invalid DATABASE_URL (check format; credentials redacted)")
	}
	cfg.MaxConns = 2
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Second
	tlsCfg, tlsErr := storage.BuildTLSConfig(os.Getenv("APP_ENV"), os.Getenv("PGSSLROOTCERT"))
	if tlsErr != nil {
		return nil, fmt.Errorf("reembed: TLS config: %w", tlsErr)
	}
	if tlsCfg != nil {
		cfg.ConnConfig.TLSConfig = tlsCfg
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("reembed: DB connect failed: %w", err)
	}
	return pool, nil
}

// reembedTable performs the idempotent per-table backfill loop.
func reembedTable(pool *pgxpool.Pool, table string, cfg *reembedConfig) error {
	slog.Info("reembed: starting table", "table", table, "target_provider", cfg.targetProvider)
	total := 0
	totalFailed := 0
	for {
		// Each batch gets its own context so one slow batch does not starve the next.
		// context.Background() is intentional — no parent deadline for backfill loops.
		ctx, cancel := context.WithTimeout(context.Background(), reembedDBTimeout)
		n, failed, err := reembedBatch(ctx, pool, table, cfg)
		cancel()
		if err != nil {
			return fmt.Errorf("batch error: %w", err)
		}
		total += n
		totalFailed += failed
		slog.Info("reembed: batch done", "table", table, "rows", n, "failed", failed, "total", total)
		// Break on the SELECTED count (processed+failed): a batch of failed rows must
		// not be mistaken for "no more rows" or the loop would silently skip them.
		if n+failed < cfg.batchSize {
			break // last batch — done
		}
		time.Sleep(reembedBatchSleep)
	}
	slog.Info("reembed: table complete", "table", table, "total", total, "failed", totalFailed)
	return nil
}

// reembedBatch selects up to batchSize rows that need reembedding, embeds each
// text column, and atomically UPDATEs the embedding + provider metadata.
// Returns the number of rows processed.
func reembedBatch(
	ctx context.Context, pool *pgxpool.Pool, table string, cfg *reembedConfig,
) (processed int, failed int, err error) {
	// Build a query that retrieves the text and ID for rows that need reembedding.
	// session_handoffs: embed intent + summary_text
	// decisions: embed title + decision
	q, err := reembedSelectQuery(table)
	if err != nil {
		return 0, 0, err
	}

	rows, err := pool.Query(ctx, q, cfg.targetProvider, cfg.batchSize)
	if err != nil {
		return 0, 0, fmt.Errorf("select batch: %w", err)
	}
	defer rows.Close()

	var items []reembedRow
	for rows.Next() {
		var it reembedRow
		if scanErr := rows.Scan(&it.id, &it.text); scanErr != nil {
			slog.Warn("reembed: scan failed; skipping row", "table", table, "err", scanErr)
			continue
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("iterate rows: %w", err)
	}

	return reembedRows(ctx, pool, table, items, cfg)
}

// reembedSelectQuery returns the SELECT query for the given table name.
// Table names are validated to prevent SQL injection.
func reembedSelectQuery(table string) (string, error) {
	switch table {
	case reembedTableHandoffs:
		return `SELECT id, COALESCE(intent, '') || ' ' || COALESCE(summary_text, '') AS text
			FROM session_handoffs
			WHERE (embedding IS NULL OR embedding_provider <> $1)
			ORDER BY created_at DESC
			LIMIT $2`, nil
	case reembedTableDecisions:
		return `SELECT id, COALESCE(title, '') || ' ' || COALESCE(decision, '') AS text
			FROM decisions
			WHERE (embedding IS NULL OR embedding_provider <> $1)
			ORDER BY created_at DESC
			LIMIT $2`, nil
	default:
		return "", fmt.Errorf("unknown table %q", table)
	}
}

// reembedUpdateQuery returns the UPDATE query for the given table name.
// Table names are validated to prevent SQL injection.
func reembedUpdateQuery(table string) (string, error) {
	switch table {
	case reembedTableHandoffs:
		return `UPDATE session_handoffs
			SET embedding = $1, embedding_provider = $2, embedding_dim = $3
			WHERE id = $4`, nil
	case reembedTableDecisions:
		return `UPDATE decisions
			SET embedding = $1, embedding_provider = $2, embedding_dim = $3
			WHERE id = $4`, nil
	default:
		return "", fmt.Errorf("unknown table %q for update", table)
	}
}

// reembedRow holds a single row's ID and text for embedding.
type reembedRow struct {
	id   string
	text string
}

// reembedRows embeds each item and writes the result back to DB.
// Returns the count of successfully processed rows.
func reembedRows(
	ctx context.Context, pool *pgxpool.Pool, table string,
	items []reembedRow, cfg *reembedConfig,
) (processed int, failed int, err error) {
	updateQ, err := reembedUpdateQuery(table)
	if err != nil {
		return 0, 0, err
	}

	for _, it := range items {
		if strings.TrimSpace(it.text) == "" {
			slog.Warn("reembed: empty text; skipping row", "table", table, "id", it.id)
			continue
		}
		vec, embErr := cfg.embedder.Embed(it.text)
		if embErr != nil || len(vec) == 0 {
			slog.Warn("reembed: embed failed; skipping row", "table", table, "id", it.id, "err", embErr)
			failed++
			continue
		}
		if valErr := localai.ValidateEmbedding(vec, cfg.targetDim); valErr != nil {
			slog.Warn("reembed: dimension mismatch; skipping row", "table", table, "id", it.id, "err", valErr)
			failed++
			continue
		}
		embBytes := localai.SerializeEmbedding(vec)
		if _, updateErr := pool.Exec(ctx, updateQ, embBytes, cfg.targetProvider, cfg.targetDim, it.id); updateErr != nil {
			slog.Warn("reembed: update failed; skipping row", "table", table, "id", it.id, "err", updateErr)
			failed++
			continue
		}
		processed++
	}
	return processed, failed, nil
}
