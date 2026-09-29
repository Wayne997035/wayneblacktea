package watchdog_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/safetext"
	"github.com/Wayne997035/wayneblacktea/internal/watchdog"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestWatchdog_RecordsSuccessfulCalls(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	for i := 0; i < 3; i++ {
		_, err := handler(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "add_task"},
		})
		if err != nil {
			t.Fatalf("handler: %v", err)
		}
	}

	recent := w.Recent(0)
	if len(recent) != 3 {
		t.Fatalf("expected 3 recorded calls, got %d", len(recent))
	}
	for _, c := range recent {
		if c.Tool != "add_task" || !c.Success {
			t.Errorf("unexpected call: %+v", c)
		}
	}
}

func TestWatchdog_RecordsErrors(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New("boom")
	})

	_, _ = handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "complete_task"},
	})

	recent := w.Recent(0)
	if len(recent) != 1 {
		t.Fatalf("expected 1 call, got %d", len(recent))
	}
	if recent[0].Success || recent[0].ErrText != "boom" {
		t.Errorf("expected error recorded, got %+v", recent[0])
	}
}

func TestWatchdog_RecordsToolResultErrors(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("bad input"), nil
	})

	_, _ = handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "log_decision"},
	})

	recent := w.Recent(0)
	if len(recent) != 1 || recent[0].Success || recent[0].ErrText != "bad input" {
		t.Errorf("expected IsError result captured, got %+v", recent[0])
	}
}

func TestWatchdog_RingBufferEvicts(t *testing.T) {
	w := watchdog.New(3)
	mw := w.Middleware()
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	for i := 0; i < 10; i++ {
		_, _ = handler(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "add_task"},
		})
	}

	if got := len(w.Recent(0)); got != 3 {
		t.Errorf("expected ring buffer capped at 3, got %d", got)
	}
}

func TestWatchdog_CountByTool(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()
	ok := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	tools := []string{"add_task", "add_task", "complete_task", "add_task"}
	for _, name := range tools {
		_, _ = ok(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name}})
	}

	counts := w.CountByTool()
	if counts["add_task"] != 3 || counts["complete_task"] != 1 {
		t.Errorf("unexpected counts %v", counts)
	}
}

// TestWatchdog_SanitizesErrText verifies record() strips control and
// invisible-formatting characters from a Go-level error before it enters
// the ring buffer.
func TestWatchdog_SanitizesErrText(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()

	// Built from runes rather than embedding literal control/invisible
	// characters in source, per dispatch: an escaped literal is easy to
	// miss on read.
	raw := "a" + "\n" + "b" +
		string(rune(0x1b)) + "[31mc" +
		string(rune(0x7f)) + "d" +
		string(rune(0x2028)) + "e" +
		string(rune(0x202e)) + "f" +
		"\t" + "g"
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New(raw)
	})

	_, _ = handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "complete_task"},
	})

	want := "a b[31mcdef" + "\t" + "g"
	recent := w.Recent(0)
	if len(recent) != 1 || recent[0].ErrText != want {
		t.Fatalf("expected sanitized ErrText %q, got %+v", want, recent)
	}
}

// TestWatchdog_SanitizesErrTextSeparators covers three sanitizeErrText
// branches TestWatchdog_SanitizesErrText does not exercise: \r collapsing to
// a space (only \n was tested there), U+2029 PARAGRAPH SEPARATOR (only
// U+2028 was tested there), and a C1 control byte (only the C0 ESC and DEL
// were tested there).
func TestWatchdog_SanitizesErrTextSeparators(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()

	raw := "x" + string(rune(0x0d)) + "y" + string(rune(0x2029)) + "z" + string(rune(0x85)) + "w"
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New(raw)
	})

	_, _ = handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "complete_task"},
	})

	want := "x yzw"
	recent := w.Recent(0)
	if len(recent) != 1 || recent[0].ErrText != want {
		t.Fatalf("expected sanitized ErrText %q, got %+v", want, recent)
	}
}

// TestWatchdog_StripsFormatAndVariationSelectors verifies sanitizeErrText
// strips Unicode format characters (category Cf) and variation selectors —
// invisible carriers used by ASCII-smuggling and variation-selector-
// smuggling techniques. Covers the Tag block, a zero-width space, BOM, and
// two variation selectors from different blocks.
func TestWatchdog_StripsFormatAndVariationSelectors(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()

	raw := "a" + string(rune(0xE0041)) + "b" + string(rune(0x200B)) + "c" + string(rune(0xFEFF)) +
		"d" + string(rune(0xFE0F)) + "e" + string(rune(0xE0100)) + "f" + string(rune(0x00AD)) + "g"
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New(raw)
	})

	_, _ = handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "complete_task"},
	})

	want := "abcdefg"
	recent := w.Recent(0)
	if len(recent) != 1 || recent[0].ErrText != want {
		t.Fatalf("expected sanitized ErrText %q, got %+v", want, recent)
	}
}

// TestWatchdog_TruncatesLongErrText verifies record() caps ErrText at
// maxErrTextRunes and marks the cut with a literal "…[truncated]" suffix,
// and that the boundary case (exactly the cap, no more) is left untouched.
// [F0929-30]
func TestWatchdog_TruncatesLongErrText(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()

	over := strings.Repeat("a", 1000)
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New(over)
	})
	_, _ = handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "complete_task"},
	})

	wantOver := strings.Repeat("a", 512) + "…[truncated]"
	recent := w.Recent(0)
	if len(recent) != 1 || recent[0].ErrText != wantOver {
		t.Fatalf("expected truncated ErrText %q, got %q", wantOver, recent[0].ErrText)
	}

	// Boundary: exactly 512 runes, no truncation marker.
	w2 := watchdog.New(10)
	mw2 := w2.Middleware()
	exact := strings.Repeat("a", 512)
	handler2 := mw2(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New(exact)
	})
	_, _ = handler2(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "complete_task"},
	})
	recent2 := w2.Recent(0)
	if len(recent2) != 1 || recent2[0].ErrText != exact {
		t.Fatalf("expected exact-boundary ErrText untouched (512 runes, no marker), got %q", recent2[0].ErrText)
	}
}

// TestWatchdog_NeutralizesBoundaryMarkers verifies sanitizeErrText replaces
// every marker in safetext.BoundaryMarkers() with BoundaryMarkerPlaceholder,
// looping over the full registry rather than a hand-picked subset.
func TestWatchdog_NeutralizesBoundaryMarkers(t *testing.T) {
	for _, marker := range safetext.BoundaryMarkers() {
		marker := marker
		t.Run(marker, func(t *testing.T) {
			w := watchdog.New(10)
			mw := w.Middleware()

			raw := "pre " + marker + " post"
			handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return nil, errors.New(raw)
			})

			_, _ = handler(context.Background(), mcp.CallToolRequest{
				Params: mcp.CallToolParams{Name: "complete_task"},
			})

			want := "pre " + safetext.BoundaryMarkerPlaceholder + " post"
			recent := w.Recent(0)
			if len(recent) != 1 || recent[0].ErrText != want {
				t.Fatalf("expected sanitized ErrText %q, got %+v", want, recent)
			}
		})
	}
}

// TestWatchdog_NeutralizesMarkerSplitByStrippedRune verifies a marker that
// arrives split by a rune sanitizeErrText strips (here, ESC) still gets
// neutralized — because the strip pass runs BEFORE neutralization, so the
// marker is reassembled into a matchable shape first.
func TestWatchdog_NeutralizesMarkerSplitByStrippedRune(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()

	marker := safetext.StoredContextMarkerEnd
	raw := marker[:3] + string(rune(0x1b)) + marker[3:]
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New(raw)
	})

	_, _ = handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "complete_task"},
	})

	want := safetext.BoundaryMarkerPlaceholder
	recent := w.Recent(0)
	if len(recent) != 1 || recent[0].ErrText != want {
		t.Fatalf("expected sanitized ErrText %q, got %+v", want, recent)
	}
}

// TestWatchdog_TruncatesAfterNeutralize verifies truncation runs AFTER
// neutralization, so the 512-rune cap bounds the post-placeholder text —
// not the pre-neutralization text, which could be shorter than its
// placeholder expansion.
func TestWatchdog_TruncatesAfterNeutralize(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()

	raw := strings.Repeat(safetext.StoredContextMarkerEnd, 40)
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New(raw)
	})

	_, _ = handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "complete_task"},
	})

	neutralizedFull := strings.Repeat(safetext.BoundaryMarkerPlaceholder, 40)
	want := string([]rune(neutralizedFull)[:512]) + "…[truncated]"
	recent := w.Recent(0)
	if len(recent) != 1 || recent[0].ErrText != want {
		t.Fatalf("expected sanitized ErrText %q, got %q", want, recent[0].ErrText)
	}
}

// TestWatchdog_SanitizesToolResultErrText verifies the errTextFromResult
// path (mcp.NewToolResultError) is sanitized identically to the Go-error
// path.
func TestWatchdog_SanitizesToolResultErrText(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()

	raw := "bad" + "\n" + "input" + string(rune(0x1b)) + "[0m"
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError(raw), nil
	})

	_, _ = handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "log_decision"},
	})

	want := "bad input[0m"
	recent := w.Recent(0)
	if len(recent) != 1 || recent[0].ErrText != want {
		t.Fatalf("expected sanitized ErrText %q, got %+v", want, recent)
	}
}

// TestWatchdog_CallerReceivesOriginalError verifies Middleware() still
// returns the unsanitized, untruncated error to the caller — only the
// watchdog's own copy is sanitized.
func TestWatchdog_CallerReceivesOriginalError(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()

	raw := "line one" + "\n" + "line two" + string(rune(0x1b)) + "[31m"
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New(raw)
	})

	_, err := handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "complete_task"},
	})
	if err == nil || err.Error() != raw {
		t.Fatalf("expected caller to receive original error %q, got %v", raw, err)
	}
}

func TestWatchdog_LastSuccessful(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()
	ok := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	if !w.LastSuccessful("add_task").IsZero() {
		t.Error("expected zero time before any call")
	}

	_, _ = ok(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "add_task"}})
	time.Sleep(2 * time.Millisecond)

	if got := w.LastSuccessful("add_task"); got.IsZero() {
		t.Error("expected non-zero time after a successful call")
	}
}
