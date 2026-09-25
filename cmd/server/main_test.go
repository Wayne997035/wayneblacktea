package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/discordbot"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/labstack/echo/v4"
)

func TestResolveAllowedOrigins(t *testing.T) {
	// t.Setenv modifies global state; subtests must NOT call t.Parallel().
	tests := []struct {
		name       string
		origins    string
		appEnv     string
		port       string
		wantResult string
		wantErr    bool
	}{
		{
			name:    "wildcard always errors regardless of APP_ENV",
			origins: "*",
			appEnv:  "",
			port:    "8080",
			wantErr: true,
		},
		{
			name:    "wildcard errors even in production",
			origins: "*",
			appEnv:  "production",
			port:    "8080",
			wantErr: true,
		},
		{
			name:    "production with empty origins errors",
			origins: "",
			appEnv:  "production",
			port:    "8080",
			wantErr: true,
		},
		{
			name:       "local dev empty origins defaults to localhost",
			origins:    "",
			appEnv:     "",
			port:       "8080",
			wantResult: "http://localhost:8080,http://127.0.0.1:8080",
			wantErr:    false,
		},
		{
			name:       "non-production APP_ENV empty origins defaults to localhost",
			origins:    "",
			appEnv:     "development",
			port:       "3000",
			wantResult: "http://localhost:3000,http://127.0.0.1:3000",
			wantErr:    false,
		},
		{
			name:       "explicit value returned as-is in production",
			origins:    "https://app.example.com",
			appEnv:     "production",
			port:       "8080",
			wantResult: "https://app.example.com",
			wantErr:    false,
		},
		{
			name:       "explicit multi-origin value returned as-is",
			origins:    "https://app.example.com,https://staging.example.com",
			appEnv:     "",
			port:       "8080",
			wantResult: "https://app.example.com,https://staging.example.com",
			wantErr:    false,
		},
		{
			name:       "whitespace-only ALLOWED_ORIGINS treated as empty in local mode",
			origins:    "   ",
			appEnv:     "",
			port:       "9090",
			wantResult: "http://localhost:9090,http://127.0.0.1:9090",
			wantErr:    false,
		},
		{
			name:    "whitespace-only ALLOWED_ORIGINS treated as empty in production",
			origins: "   ",
			appEnv:  "production",
			port:    "8080",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ALLOWED_ORIGINS", tc.origins)
			t.Setenv("APP_ENV", tc.appEnv)

			got, err := resolveAllowedOrigins(tc.port)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantResult {
				t.Errorf("resolveAllowedOrigins(%q) = %q, want %q", tc.port, got, tc.wantResult)
			}
		})
	}
}

func TestValidateAPIKey(t *testing.T) {
	tests := []struct {
		name    string
		apiKey  string
		wantErr bool
	}{
		{name: "missing", apiKey: "", wantErr: true},
		{name: "too short", apiKey: "short", wantErr: true},
		{name: "minimum length", apiKey: "12345678901234567890123456789012", wantErr: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAPIKey(tc.apiKey)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateAPIKey() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestResolveIPExtractor_DefaultIgnoresSpoofedXFF(t *testing.T) {
	extractor, err := resolveIPExtractor("")
	if err != nil {
		t.Fatalf("resolveIPExtractor: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.10:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")

	if got := extractor(req); got != "198.51.100.10" {
		t.Fatalf("extracted IP = %q, want socket peer", got)
	}
}

func TestResolveIPExtractor_TrustedProxyCIDRUsesXFF(t *testing.T) {
	extractor, err := resolveIPExtractor("198.51.100.0/24")
	if err != nil {
		t.Fatalf("resolveIPExtractor: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.10:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.99, 198.51.100.10")

	if got := extractor(req); got != "203.0.113.99" {
		t.Fatalf("extracted IP = %q, want XFF client", got)
	}
}

func TestResolveIPExtractor_InvalidCIDR(t *testing.T) {
	if _, err := resolveIPExtractor("not-a-cidr"); err == nil {
		t.Fatal("resolveIPExtractor() err = nil, want error")
	}
}

// TestRateLimiter_FloodsOneIdentityGets429 verifies the /mcp rate limit:
// /mcp previously had no rate limit at all — every other route family in
// this file (mutationRL, activityRL,
// postToolUseRL, etc.) already had one. Exercises newMCPRateLimiter()
// directly against a minimal Echo instance (mirrors how resolveIPExtractor's
// tests above exercise their function in isolation, without wiring the full
// server) rather than the full server's DB/store dependencies, which are
// orthogonal to what this test proves.
//
// (1) flooding one identity (IP) with rapid requests eventually gets a 429.
// (2) a second, different identity is NOT throttled by the first's flood —
// echo's RateLimiterMemoryStore keys by IP by default (the same default
// every other rate limiter in this file already relies on), so isolation is
// a property of the shared store, not something newMCPRateLimiter adds.
func TestRateLimiter_FloodsOneIdentityGets429(t *testing.T) {
	e := echo.New()
	e.Any("/mcp", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	}, newMCPRateLimiter())

	request := func(remoteIP string) int {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/mcp", nil)
		req.RemoteAddr = remoteIP + ":12345"
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code
	}

	const floodIdentity = "203.0.113.5"
	got429 := false
	// mcpRateLimit is also the default Burst size (echo's
	// NewRateLimiterMemoryStore doc comment: "Burst will be set to the
	// rounded down value of the configured rate if not provided") — this
	// many rapid, no-sleep requests exhausts the token bucket within the
	// loop, no real time delay needed.
	const attempts = mcpRateLimit + 20
	for range attempts {
		if code := request(floodIdentity); code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatalf("flooding one identity with %d rapid requests never received a 429", attempts)
	}

	const otherIdentity = "203.0.113.9"
	if code := request(otherIdentity); code == http.StatusTooManyRequests {
		t.Error("a different identity was throttled by the first identity's flood — " +
			"rate limiter isolation is broken")
	}
}

// --- [F185-05] startBotAndHealth / startDiscordBotIfConfigured -----------

// fakeDiscordStarter is a discordStarter test double. Never touches a real
// Discord connection: startErr/panicMsg/block select which of Start()'s
// three failure modes (and the success mode) to exercise, matching the four
// paths the production incident history forced onto this design (dispatch
// "原本那個 bug 還能不能發生" table).
type fakeDiscordStarter struct {
	startErr error
	panicMsg string        // non-empty: Start() panics with this instead of returning
	block    bool          // true: Start() blocks forever (select{}), like an un-deadlined ReadMessage
	done     chan struct{} // closed immediately before Start() returns or panics; nil is fine if unused

	stopCalled atomic.Bool
}

func (f *fakeDiscordStarter) Start() error {
	if f.done != nil {
		defer close(f.done)
	}
	if f.block {
		select {} // never returns — mirrors discordgo's un-deadlined ReadMessage
	}
	if f.panicMsg != "" {
		panic(f.panicMsg)
	}
	return f.startErr
}

func (f *fakeDiscordStarter) Stop() {
	f.stopCalled.Store(true)
}

// waitDone blocks until fake.done closes or the timeout elapses, whichever
// comes first. This is the "共用規定" sync point from the dispatch — used for
// every test whose fake actually returns or panics, so no test asserts on
// startBotAndHealth's async Set() before it is guaranteed to have happened.
func waitDone(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fake Start() never signalled completion within 2s")
	}
}

// waitForStatus polls health until it reports want or timeout elapses.
// Only used by the two tests (1f/1g) whose fake never returns — there is no
// "done" event to synchronize on for a timeout-driven state transition, so a
// bounded poll is the only deterministic option left (this is not the
// forbidden single blind time.Sleep-then-assert: it retries against a
// deadline instead of guessing a fixed delay).
func waitForStatus(t *testing.T, health *handler.DiscordHealthHandler, want string, timeout time.Duration) handler.DiscordHealthState {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s := health.Load()
		if s.Status == want {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("health state never reached %q within %s; last=%+v", want, timeout, s)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestStartBotAndHealth_StartFailureDegrades(t *testing.T) {
	done := make(chan struct{})
	fake := &fakeDiscordStarter{startErr: errors.New("open discord session: boom"), done: done}
	health := handler.NewDiscordHealthHandler()

	stop := startBotAndHealth(fake, health, time.Minute)
	waitDone(t, done) // Start() has returned...
	// ...but health.Set() is a few more statements further down the same
	// goroutine, so waitDone alone does not guarantee it has landed yet —
	// waitForStatus closes that gap with a short bounded poll instead of
	// racing straight into health.Load().
	got := waitForStatus(t, health, handler.DiscordStatusDegraded, 2*time.Second)
	if got.Detail != "open discord session: boom" {
		t.Fatalf("detail = %q, want the wrapped error text", got.Detail)
	}
	if stop == nil {
		t.Fatal("stop = nil, want non-nil")
	}
}

func TestStartBotAndHealth_StartSuccess(t *testing.T) {
	done := make(chan struct{})
	fake := &fakeDiscordStarter{done: done}
	health := handler.NewDiscordHealthHandler()

	startBotAndHealth(fake, health, time.Minute)
	waitDone(t, done)
	got := waitForStatus(t, health, handler.DiscordStatusOK, 2*time.Second)
	if got.Detail != "" {
		t.Fatalf("detail = %q, want empty on success", got.Detail)
	}
}

func TestStartBotAndHealth_StartPanicDegrades(t *testing.T) {
	done := make(chan struct{})
	fake := &fakeDiscordStarter{panicMsg: "boom", done: done}
	health := handler.NewDiscordHealthHandler()

	startBotAndHealth(fake, health, time.Minute)
	waitDone(t, done)
	// Reaching this line at all proves the panic did not escape the
	// goroutine and kill the test binary — that is the process-survival
	// assertion. waitForStatus's bounded poll also covers the gap between
	// "Start() returned/panicked" (done) and the recover branch's own
	// writeOnce.Do(health.Set(...)) landing a few statements later.
	waitForStatus(t, health, handler.DiscordStatusDegraded, 2*time.Second)

	deadline := time.Now().Add(2 * time.Second)
	for !fake.stopCalled.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !fake.stopCalled.Load() {
		t.Fatal("bot.Stop() was never called after the panic — a live half-open session would leak")
	}
}

func TestStartBotAndHealth_NilStateDegrades(t *testing.T) {
	// [F185-05/D3] Mirrors bot.go's Start() returning ErrSessionStateUnavailable
	// when session.Open() succeeds but State.User is still nil (op 9 during
	// rate-limiting). Open() has already succeeded in this scenario, so
	// unlike the generic error path, bot.Stop() must be called.
	done := make(chan struct{})
	fake := &fakeDiscordStarter{startErr: discordbot.ErrSessionStateUnavailable, done: done}
	health := handler.NewDiscordHealthHandler()

	startBotAndHealth(fake, health, time.Minute)
	waitDone(t, done)
	got := waitForStatus(t, health, handler.DiscordStatusDegraded, 2*time.Second)
	if got.Detail != "session state unavailable" {
		t.Fatalf("detail = %q, want the sentinel error text", got.Detail)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !fake.stopCalled.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !fake.stopCalled.Load() {
		t.Fatal("bot.Stop() was never called — Open() succeeded in this path and must be cleaned up")
	}
}

func TestStartBotAndHealth_StartBlocksDoesNotBlockBoot(t *testing.T) {
	fake := &fakeDiscordStarter{block: true}
	health := handler.NewDiscordHealthHandler()

	start := time.Now()
	startBotAndHealth(fake, health, time.Minute)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("startBotAndHealth took %s to return, want <50ms — it must never wait on Start()", elapsed)
	}

	got := health.Load()
	if got.Status != handler.DiscordStatusStarting {
		t.Fatalf("status = %q, want %q immediately after a still-blocked Start()", got.Status, handler.DiscordStatusStarting)
	}
}

func TestStartBotAndHealth_StopDoesNotBlockWhenStarting(t *testing.T) {
	fake := &fakeDiscordStarter{block: true}
	health := handler.NewDiscordHealthHandler()
	stop := startBotAndHealth(fake, health, time.Minute)

	start := time.Now()
	stop()
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("stop() took %s to return while Start() was still blocked, want <50ms (D5)", elapsed)
	}
}

func TestStartBotAndHealth_StartTimeoutDegrades(t *testing.T) {
	fake := &fakeDiscordStarter{block: true}
	health := handler.NewDiscordHealthHandler()
	const tinyTimeout = 20 * time.Millisecond

	startBotAndHealth(fake, health, tinyTimeout)

	got := waitForStatus(t, health, handler.DiscordStatusDegraded, 2*time.Second)
	if got.Detail == "" {
		t.Fatal("detail = empty, want a recognizable timeout message")
	}
	t.Logf("timeout detail: %q", got.Detail)
}

func TestStartBotAndHealth_StopDoesNotBlockAfterTimeout(t *testing.T) {
	// [D5 x D6] The regression this test alone catches: if stop() ever reads
	// health's status instead of the startReturned channel, this test hangs
	// while TestStartBotAndHealth_StopDoesNotBlockWhenStarting (above) still
	// passes, because that test never lets the state leave "starting".
	fake := &fakeDiscordStarter{block: true}
	health := handler.NewDiscordHealthHandler()
	const tinyTimeout = 20 * time.Millisecond

	stop := startBotAndHealth(fake, health, tinyTimeout)
	waitForStatus(t, health, handler.DiscordStatusDegraded, 2*time.Second)

	start := time.Now()
	stop()
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("stop() took %s to return after the D6 timeout fired while Start() was still blocked, want <50ms", elapsed)
	}
}

func TestStartDiscordBotIfConfigured_NewFailureStaysFatal(t *testing.T) {
	t.Setenv("DISCORD_ENV", "")
	t.Setenv("DISCORD_BOT_TOKEN", "fake-token-value-not-a-real-secret")
	t.Setenv("DISCORD_ALLOWED_USER_IDS", "") // empty allowlist -> New()'s fail-closed guard errors, no network touched
	health := handler.NewDiscordHealthHandler()

	_, err := startDiscordBotIfConfigured("8420", "dummy-api-key-not-a-real-secret", nil, health)
	if err == nil {
		t.Fatal("err = nil, want non-nil — New()'s fail-closed misconfiguration guard must stay fatal (D1)")
	}
}

func TestStartDiscordBotIfConfigured_NoTokenUnconfigured(t *testing.T) {
	t.Setenv("DISCORD_ENV", "")
	t.Setenv("DISCORD_BOT_TOKEN", "")
	health := handler.NewDiscordHealthHandler()

	_, err := startDiscordBotIfConfigured("8420", "dummy-api-key-not-a-real-secret", nil, health)
	if err != nil {
		t.Fatalf("err = %v, want nil when no token is configured", err)
	}
	got := health.Load()
	if got.Status != handler.DiscordStatusUnconfigured {
		t.Fatalf("status = %q, want %q", got.Status, handler.DiscordStatusUnconfigured)
	}
}

// TestStartDiscordBotIfConfigured_StartFailureNoLongerFatal is acceptance
// criterion 0's positive control, in its post-change form. Before this
// task's changes, startDiscordBotIfConfigured returned a non-nil error when
// bot.Start() failed (the production incident this task fixes); after, it
// must not, regardless of why Start() eventually fails.
//
// This test does not (and must not) wait for Start() to actually finish: the
// only thing it asserts is startDiscordBotIfConfigured's own return value,
// which startBotAndHealth's async dispatch (D4) makes available immediately
// regardless of network reachability. discordbot.New() with a non-empty
// allowlist never errors on a garbage token (v0.29.0's discordgo.New()
// cannot fail — see bot.go's Start() doc / dispatch Threat Surface note), so
// this reaches the Start()-is-now-non-fatal branch without needing a real
// Discord connection to succeed or even be reachable.
func TestStartDiscordBotIfConfigured_StartFailureNoLongerFatal(t *testing.T) {
	t.Setenv("DISCORD_ENV", "")
	t.Setenv("DISCORD_BOT_TOKEN", "fake-token-value-not-a-real-secret")
	t.Setenv("DISCORD_ALLOWED_USER_IDS", "123456789012345678")
	t.Setenv("DISCORD_GUILD_ID", "")
	health := handler.NewDiscordHealthHandler()

	stop, err := startDiscordBotIfConfigured("8420", "dummy-api-key-not-a-real-secret", nil, health)
	if err != nil {
		t.Fatalf("err = %v, want nil — a bot.Start() failure must no longer be fatal (this task's entire point)", err)
	}
	if stop != nil {
		stop()
	}
}
