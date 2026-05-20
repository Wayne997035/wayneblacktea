package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTruncateUnicodeBoundary(t *testing.T) {
	input := "我愛台灣"
	tests := []struct {
		name   string
		maxLen int
		want   string
	}{
		{name: "maxLen minus one", maxLen: 3, want: "我愛台…"},
		{name: "exact rune length", maxLen: 4, want: input},
		{name: "greater than rune length", maxLen: 5, want: input},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncate(input, tc.maxLen); got != tc.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", input, tc.maxLen, got, tc.want)
			}
		})
	}
}

func TestSortBySimDescStableForEqualScores(t *testing.T) {
	type scoredItem struct {
		name  string
		score float64
	}
	items := []scoredItem{
		{name: "first", score: 0.8},
		{name: "second", score: 0.8},
		{name: "third", score: 0.7},
	}
	SortBySimDesc(items, func(item scoredItem) float64 {
		return item.score
	})

	if items[0].name != "first" || items[1].name != "second" || items[2].name != "third" {
		t.Fatalf("sorted items = %+v, want equal-score order preserved", items)
	}
}

func TestDsnFromFallbackReadsTempEnvFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".wayneblacktea")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	envPath := filepath.Join(configDir, ".env.local")
	wantDSN := "postgres://u:" + "p" + "@host/db"
	if err := os.WriteFile(envPath, []byte("OTHER=x\nDATABASE_URL="+wantDSN+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if got := DSNFromFallback(); got != wantDSN {
		t.Fatalf("DSNFromFallback() = %q", got)
	}
}

func TestEmitContextAlwaysValidJSON(t *testing.T) {
	stdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = stdout
		_ = r.Close()
	})

	EmitContext("hello\nworld")
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	var out SessionStartOutput
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		t.Fatalf("Decode emitted JSON: %v", err)
	}
	if out.SystemMessage != "hello\nworld" {
		t.Fatalf("SystemMessage = %q", out.SystemMessage)
	}
}
