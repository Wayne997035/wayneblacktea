package snapshot_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/google/uuid"
)

// capDecStore and capGTDStore implement only the methods Generate calls; the
// embedded nil interfaces make any other call panic loudly.
type capDecStore struct{ decision.StoreIface }

func (capDecStore) ByRepo(context.Context, string, int32, int32) ([]db.Decision, error) {
	return nil, nil
}

type capGTDStore struct{ gtd.StoreIface }

func (capGTDStore) ListActivityLogsSince(context.Context, time.Time, int32) ([]db.ActivityLog, error) {
	return nil, nil
}

func (capGTDStore) Tasks(context.Context, *uuid.UUID) ([]db.Task, error) { return nil, nil }

// statusServer serves a valid Messages response whose text is a StatusResult
// JSON with sprint_summary padded to n bytes, and points the SDK at it.
func statusServer(t *testing.T, n int) {
	t.Helper()
	text := `{"sprint_summary":"` + strings.Repeat("a", n) + `","gap_analysis":"g","sota_catchup_pct":50,"pending_summary":"p"}`
	body, err := json.Marshal(map[string]any{
		"id": "msg_test", "type": "message", "role": "assistant", "model": "claude-haiku-4-5",
		"content":     []map[string]any{{"type": "text", "text": text}},
		"stop_reason": "end_turn", "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
}

// [F1124-76]
func TestNewGenerator_AppliesResponseCap(t *testing.T) {
	ctx := context.Background()
	statusServer(t, 100<<10)
	if _, err := snapshot.NewGenerator("k").Generate(ctx, "slug", capDecStore{}, capGTDStore{}); err != nil {
		t.Fatalf("positive control: Generate err = %v for 100 KiB response", err)
	}

	statusServer(t, 2<<20)
	if _, err := snapshot.NewGenerator("k").Generate(ctx, "slug", capDecStore{}, capGTDStore{}); err == nil {
		t.Error("err = <nil>, want error (response over cap)")
	}
}
