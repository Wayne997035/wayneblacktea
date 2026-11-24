//go:build !integration

package mcp

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// [F1124-14] add_vision_item always returns {"item":…,"warnings":[…]}.

const (
	visionShapeNoWarnTitle = "Migrate the notification pipeline to the new queue"
	visionShapeNoWarnWhy   = "Waiting on the upstream API contract to stabilize"
)

// visionTopLevelKeys adds a vision item and returns the raw body plus its
// sorted top-level key set.
func visionTopLevelKeys(t *testing.T, args map[string]any) (string, []string) {
	t.Helper()
	s := newTestWorkSessionServer(t)
	r := callAddVision(t, s, args)
	if r.IsError {
		t.Fatalf("add_vision_item error: %s", resultText(r))
	}
	raw := resultText(r)
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("parse: %v (%s)", err, raw)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return raw, keys
}

func TestAddVisionItem_ShapeNoWarnings(t *testing.T) {
	t.Parallel()
	raw, keys := visionTopLevelKeys(t, map[string]any{
		"title": visionShapeNoWarnTitle, "why_blocked": visionShapeNoWarnWhy,
	})
	if got := strings.Join(keys, ","); got != "item,warnings" {
		t.Errorf("top-level keys = %s, want item,warnings: %s", got, raw)
	}
	if !strings.Contains(raw, `"warnings":[]`) {
		t.Errorf(`body lacks "warnings":[]: %s`, raw)
	}
	if strings.Contains(raw, `"warnings":null`) {
		t.Errorf(`body has "warnings":null: %s`, raw)
	}
}

func TestAddVisionItem_ShapeWithWarnings(t *testing.T) {
	t.Parallel()
	raw, keys := visionTopLevelKeys(t, map[string]any{"title": "it", "why_blocked": "it"})
	if got := strings.Join(keys, ","); got != "item,warnings" {
		t.Errorf("top-level keys = %s, want item,warnings: %s", got, raw)
	}
	var body struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(body.Warnings) < 1 {
		t.Errorf("expected >=1 warning for vague input: %s", raw)
	}
}

func TestAddVisionItem_ShapeIdenticalAcrossBranches(t *testing.T) {
	t.Parallel()
	_, plain := visionTopLevelKeys(t, map[string]any{
		"title": visionShapeNoWarnTitle, "why_blocked": visionShapeNoWarnWhy,
	})
	_, warned := visionTopLevelKeys(t, map[string]any{"title": "it", "why_blocked": "it"})
	if strings.Join(plain, ",") != strings.Join(warned, ",") {
		t.Errorf("key sets differ across branches: no-warning=%v with-warning=%v", plain, warned)
	}
}
