package discordbot

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestBot constructs a *Bot wired to the given apiURL without opening a
// Discord WebSocket (session and analyzer are intentionally nil — only the
// HTTP-based run* methods are under test here).
func newTestBot(apiURL string) *Bot {
	return &Bot{
		apiURL:     strings.TrimRight(apiURL, "/"),
		apiKey:     "test-api-key",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// ---------------------------------------------------------------------------
// truncate
// ---------------------------------------------------------------------------

func TestTruncate_ShortString(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate(%q, 10) = %q, want %q", "hello", got, "hello")
	}
}

func TestTruncate_ExactLength(t *testing.T) {
	s := "1234567890"
	if got := truncate(s, 10); got != s {
		t.Errorf("truncate exact-length: got %q, want %q", got, s)
	}
}

func TestTruncate_LongerString(t *testing.T) {
	s := "abcdefghij"
	got := truncate(s, 5)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncate should append ellipsis, got %q", got)
	}
	// The prefix before "…" must be the first 5 chars.
	if !strings.HasPrefix(got, "abcde") {
		t.Errorf("truncate prefix wrong, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// runNote
// ---------------------------------------------------------------------------

// TestRunNote_success verifies that runNote POSTs to /api/knowledge and returns
// a confirmation message on HTTP 201.
func TestRunNote_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/knowledge" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	b := newTestBot(srv.URL)
	msg := b.runNote("interesting Go tip about channels")
	if !strings.Contains(msg, "knowledge base") {
		t.Errorf("runNote success should mention 'knowledge base', got: %q", msg)
	}
}

// TestRunNote_apiError verifies that runNote returns an error message when the
// API responds with 500.
func TestRunNote_apiError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := newTestBot(srv.URL)
	msg := b.runNote("note that will fail")
	if !strings.Contains(strings.ToLower(msg), "fail") && !strings.Contains(strings.ToLower(msg), "error") {
		t.Errorf("runNote API error should indicate failure, got: %q", msg)
	}
}

// TestRunNote_duplicate verifies that runNote surfaces a user-friendly message
// on 409 Conflict (already saved).
func TestRunNote_duplicate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprint(w, `{"error":"already saved similar content"}`)
	}))
	defer srv.Close()

	b := newTestBot(srv.URL)
	msg := b.runNote("duplicate note")
	if !strings.Contains(strings.ToLower(msg), "fail") && !strings.Contains(strings.ToLower(msg), "already") {
		t.Errorf("runNote duplicate should mention already saved, got: %q", msg)
	}
}

// ---------------------------------------------------------------------------
// runSearch
// ---------------------------------------------------------------------------

// TestRunSearch_success verifies that runSearch returns formatted results when
// the API returns matching knowledge items.
func TestRunSearch_success(t *testing.T) {
	items := []map[string]any{
		{"title": "Go concurrency patterns", "content": "goroutines and channels"},
		{"title": "Redis caching", "content": "cache-aside pattern"},
	}
	body, _ := json.Marshal(items)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/knowledge/search") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	b := newTestBot(srv.URL)
	msg := b.runSearch("Go concurrency")
	if !strings.Contains(msg, "Go concurrency patterns") {
		t.Errorf("runSearch should include result title, got: %q", msg)
	}
}

// TestRunSearch_noResults verifies that runSearch returns a "no results" message
// when the API returns an empty array.
func TestRunSearch_noResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()

	b := newTestBot(srv.URL)
	msg := b.runSearch("nonexistent query")
	if !strings.Contains(strings.ToLower(msg), "no result") {
		t.Errorf("runSearch empty response should say no results, got: %q", msg)
	}
}

// TestRunSearch_invalidJSON verifies that runSearch returns a "no results"
// message when the API returns malformed JSON.
func TestRunSearch_invalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `not-json`)
	}))
	defer srv.Close()

	b := newTestBot(srv.URL)
	msg := b.runSearch("any query")
	if !strings.Contains(strings.ToLower(msg), "no result") {
		t.Errorf("runSearch malformed JSON should return no results, got: %q", msg)
	}
}

// ---------------------------------------------------------------------------
// runRecent
// ---------------------------------------------------------------------------

// TestRunRecent_success verifies that runRecent returns a formatted list when
// the API returns knowledge items.
func TestRunRecent_success(t *testing.T) {
	items := []map[string]any{
		{"title": "Redis patterns", "type": "article", "created_at": "2025-01-15T10:00:00Z"},
		{"title": "Go channels", "type": "til", "created_at": "2025-01-14T08:00:00Z"},
	}
	body, _ := json.Marshal(items)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/knowledge") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	b := newTestBot(srv.URL)
	msg := b.runRecent()
	if !strings.Contains(msg, "Redis patterns") {
		t.Errorf("runRecent should include item title, got: %q", msg)
	}
	if !strings.Contains(msg, "Go channels") {
		t.Errorf("runRecent should include second item, got: %q", msg)
	}
}

// TestRunRecent_empty verifies that runRecent returns a "no items" message
// when the API returns an empty array.
func TestRunRecent_empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()

	b := newTestBot(srv.URL)
	msg := b.runRecent()
	if !strings.Contains(strings.ToLower(msg), "no item") {
		t.Errorf("runRecent empty response should say no items, got: %q", msg)
	}
}

// TestRunRecent_requestError verifies that runRecent surfaces an error message
// when the HTTP request fails (server closed before request).
func TestRunRecent_requestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// Close the server before the request so the connection is refused.
	srv.Close()

	b := newTestBot(srv.URL)
	msg := b.runRecent()
	if !strings.Contains(strings.ToLower(msg), "fail") && !strings.Contains(strings.ToLower(msg), "error") &&
		!strings.Contains(strings.ToLower(msg), "refused") && !strings.Contains(strings.ToLower(msg), "no item") {
		t.Errorf("runRecent with closed server should surface error, got: %q", msg)
	}
}

// ---------------------------------------------------------------------------
// runReview
// ---------------------------------------------------------------------------

// TestRunReview_success verifies that runReview returns a confirmation message
// when the API responds with 201 Created.
func TestRunReview_success(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/learning/concepts" {
			http.NotFound(w, r)
			return
		}
		var err error
		receivedBody, err = io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	b := newTestBot(srv.URL)
	msg := b.runReview("Goroutines", "concurrent execution units in Go", "go,concurrency")
	if !strings.Contains(strings.ToLower(msg), "concept saved") && !strings.Contains(strings.ToLower(msg), "saved") {
		t.Errorf("runReview success should confirm save, got: %q", msg)
	}

	// Verify the title is in the sent payload.
	var payload map[string]any
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("could not parse sent payload: %v", err)
	}
	if payload["title"] != "Goroutines" {
		t.Errorf("payload title = %v, want Goroutines", payload["title"])
	}
}

// TestRunReview_missingTitle verifies that runReview returns a usage hint when
// title is empty.
func TestRunReview_missingTitle(t *testing.T) {
	b := newTestBot("http://localhost:9999") // server never reached
	msg := b.runReview("", "content here", "")
	if !strings.Contains(strings.ToLower(msg), "required") && !strings.Contains(strings.ToLower(msg), "usage") {
		t.Errorf("runReview empty title should ask for required fields, got: %q", msg)
	}
}

// TestRunReview_missingContent verifies that runReview returns a usage hint
// when content is empty.
func TestRunReview_missingContent(t *testing.T) {
	b := newTestBot("http://localhost:9999")
	msg := b.runReview("Some Title", "", "")
	if !strings.Contains(strings.ToLower(msg), "required") && !strings.Contains(strings.ToLower(msg), "usage") {
		t.Errorf("runReview empty content should ask for required fields, got: %q", msg)
	}
}

// TestRunReview_apiError verifies that runReview surfaces an error on 500.
func TestRunReview_apiError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, "internal error")
	}))
	defer srv.Close()

	b := newTestBot(srv.URL)
	msg := b.runReview("Title", "Content", "")
	if !strings.Contains(strings.ToLower(msg), "api returned") && !strings.Contains(strings.ToLower(msg), "error") {
		t.Errorf("runReview API error should report failure, got: %q", msg)
	}
}

// TestRunReview_tagsOptional verifies that tags field is omitted from the
// payload when no tags are supplied.
func TestRunReview_tagsOptional(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		receivedBody, err = io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	b := newTestBot(srv.URL)
	b.runReview("Title", "Content", "")

	var payload map[string]any
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("could not parse sent payload: %v", err)
	}
	if _, ok := payload["tags"]; ok {
		t.Error("tags key should be absent from payload when no tags provided")
	}
}

// ---------------------------------------------------------------------------
// runSaveAnyway
// ---------------------------------------------------------------------------

// TestRunSaveAnyway_success verifies that runSaveAnyway returns a confirmation
// message on 201.
func TestRunSaveAnyway_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	b := newTestBot(srv.URL)
	msg := b.runSaveAnyway("https://example.com/interesting-page")
	if !strings.Contains(strings.ToLower(msg), "saved") {
		t.Errorf("runSaveAnyway success should confirm saved, got: %q", msg)
	}
}

// TestRunSaveAnyway_emptyInput verifies that runSaveAnyway rejects empty input.
func TestRunSaveAnyway_emptyInput(t *testing.T) {
	b := newTestBot("http://localhost:9999")
	msg := b.runSaveAnyway("")
	if !strings.Contains(strings.ToLower(msg), "provide") && !strings.Contains(strings.ToLower(msg), "url") {
		t.Errorf("runSaveAnyway empty input should ask for input, got: %q", msg)
	}
}

// TestRunSaveAnyway_duplicate verifies that runSaveAnyway surfaces a friendly
// message on 409 Conflict.
func TestRunSaveAnyway_duplicate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprint(w, `{"error":"already saved similar content"}`)
	}))
	defer srv.Close()

	b := newTestBot(srv.URL)
	msg := b.runSaveAnyway("https://example.com/duplicate")
	if !strings.Contains(strings.ToLower(msg), "already") && !strings.Contains(strings.ToLower(msg), "saved") {
		t.Errorf("runSaveAnyway duplicate should mention already saved, got: %q", msg)
	}
}

// ---------------------------------------------------------------------------
// handleReview parsing
// ---------------------------------------------------------------------------

// TestHandleReview_ParsesFieldsCorrectly verifies that the "!review title :: content :: tags"
// format is parsed and the correct title/content/tags are extracted.
func TestHandleReview_ParsesFieldsCorrectly(t *testing.T) {
	type testCase struct {
		name    string
		raw     string
		wantMsg string // substring expected in reply
	}
	cases := []testCase{
		{
			name:    "valid full review with tags",
			raw:     "Goroutines :: concurrent execution :: go,concurrency",
			wantMsg: "Goroutines",
		},
		{
			name:    "missing content returns usage hint",
			raw:     "Title only",
			wantMsg: "required",
		},
		{
			name:    "empty input returns usage hint",
			raw:     "",
			wantMsg: "required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var receivedMsg string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
			}))
			defer srv.Close()

			b := newTestBot(srv.URL)

			// Replicate the handleReview parsing logic and call runReview
			// (handleReview itself calls reply() via discordgo.Session which
			// requires a real WebSocket — we test the logic layer directly).
			parts := splitReviewRaw(tc.raw)
			receivedMsg = b.runReview(parts[0], parts[1], parts[2])

			if !strings.Contains(strings.ToLower(receivedMsg), strings.ToLower(tc.wantMsg)) {
				t.Errorf("case %q: got %q, want substring %q", tc.name, receivedMsg, tc.wantMsg)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ParseAllowedUserIDs + isAuthorised + New fail-closed
// ---------------------------------------------------------------------------

func TestParseAllowedUserIDs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", nil},
		{"single", "123", []string{"123"}},
		{"comma list", "111,222,333", []string{"111", "222", "333"}},
		{"trims whitespace", " 111 , 222 , 333 ", []string{"111", "222", "333"}},
		{"drops empty entries", "111,,222,  ,333", []string{"111", "222", "333"}},
		{"only commas", ",,,,", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseAllowedUserIDs(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (got=%v)", len(got), len(tc.want), got)
			}
			for _, want := range tc.want {
				if _, ok := got[want]; !ok {
					t.Errorf("missing %q in %v", want, got)
				}
			}
		})
	}
}

func TestIsAuthorised_failClosedOnEmptyAllowlist(t *testing.T) {
	b := &Bot{allowedUsers: ParseAllowedUserIDs("")}
	if b.isAuthorised("anything") {
		t.Error("empty allowlist must reject everyone")
	}
	if b.isAuthorised("") {
		t.Error("empty allowlist must reject empty user")
	}
}

func TestIsAuthorised_acceptsListedDeniesOthers(t *testing.T) {
	b := &Bot{allowedUsers: ParseAllowedUserIDs("alice,bob")}
	if !b.isAuthorised("alice") {
		t.Error("alice should be allowed")
	}
	if !b.isAuthorised("bob") {
		t.Error("bob should be allowed")
	}
	if b.isAuthorised("eve") {
		t.Error("eve should be rejected")
	}
	if b.isAuthorised("") {
		t.Error("empty user id should be rejected")
	}
}

func TestNew_failsWhenTokenSetButAllowlistEmpty(t *testing.T) {
	// Use a syntactically valid (but invalid for auth) bot token format so
	// discordgo.New itself doesn't reject it before our allowlist check runs.
	_, err := New("Mzk1.NotARealToken.AAAAAAAAAA", "http://example.test", "key", "", "", nil)
	if err == nil {
		t.Fatal("New must reject empty allowlist when token is set")
	}
	if !strings.Contains(err.Error(), "DISCORD_ALLOWED_USER_IDS") {
		t.Errorf("error should mention allowlist env var, got: %v", err)
	}
}

func TestNew_emptyTokenAndEmptyAllowlistIsAllowed(t *testing.T) {
	// Bot can be constructed with both empty — useful for tests / memory-only
	// scenarios where the caller will never call Start.
	b, err := New("", "http://example.test", "key", "", "", nil)
	if err != nil {
		t.Fatalf("New(empty token, empty allowlist) should not error: %v", err)
	}
	if b == nil {
		t.Fatal("bot is nil")
	}
	// Even so, isAuthorised must remain fail-closed.
	if b.isAuthorised("anyone") {
		t.Error("bot constructed with empty allowlist must still reject all users")
	}
}

// splitReviewRaw replicates the parsing logic from Bot.handleReview so tests
// can exercise runReview directly without a discordgo.Session.
func splitReviewRaw(raw string) [3]string {
	parts := strings.SplitN(raw, "::", 3)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	var out [3]string
	if len(parts) > 0 {
		out[0] = parts[0]
	}
	if len(parts) > 1 {
		out[1] = parts[1]
	}
	if len(parts) > 2 {
		out[2] = parts[2]
	}
	return out
}
