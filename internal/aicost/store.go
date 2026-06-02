package aicost

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CostRow is one model's aggregated cost over a time period.
// Mirrors handler.AICostRow; handler imports this package so AICostRow lives
// here to avoid the cycle aicost→handler→aicost.
type CostRow struct {
	Model        string  `json:"model"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
}

// PgStore queries the ai_cost_ledger table.
type PgStore struct {
	pool        *pgxpool.Pool
	workspaceID *uuid.UUID
}

// NewPgStore creates a PgStore. pool must not be nil.
func NewPgStore(pool *pgxpool.Pool, workspaceID *uuid.UUID) *PgStore {
	return &PgStore{pool: pool, workspaceID: workspaceID}
}

// AICostLast30d returns per-model aggregated cost_usd_micro totals and grand
// total for the last 30 days in the configured workspace.
// The returned []CostRow is ordered by total_cost_usd_micro DESC.
func (s *PgStore) AICostLast30d(ctx context.Context) ([]CostRow, int64, error) {
	cutoff := time.Now().AddDate(0, 0, -30)

	const q = `
SELECT model,
       SUM(cost_usd_micro)  AS total_cost_usd_micro,
       SUM(input_tokens)    AS input_tokens,
       SUM(output_tokens)   AS output_tokens
  FROM ai_cost_ledger
 WHERE created_at >= $1
   AND ($2::uuid IS NULL OR workspace_id = $2)
 GROUP BY model
 ORDER BY total_cost_usd_micro DESC`

	rows, err := s.pool.Query(ctx, q, cutoff, s.workspaceID)
	if err != nil {
		return nil, 0, fmt.Errorf("aicost.PgStore.AICostLast30d query: %w", err)
	}
	defer rows.Close()

	var result []CostRow
	var grandTotal int64
	for rows.Next() {
		var (
			model     string
			costMicro int64
			inTokens  int64
			outTokens int64
		)
		if err := rows.Scan(&model, &costMicro, &inTokens, &outTokens); err != nil {
			return nil, 0, fmt.Errorf("aicost.PgStore.AICostLast30d scan: %w", err)
		}
		result = append(result, CostRow{
			Model:        model,
			TotalCostUSD: float64(costMicro) / 1_000_000,
			InputTokens:  inTokens,
			OutputTokens: outTokens,
		})
		grandTotal += costMicro
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("aicost.PgStore.AICostLast30d rows: %w", err)
	}

	return result, grandTotal, nil
}
