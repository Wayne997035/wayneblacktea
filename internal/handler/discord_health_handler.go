package handler

import (
	"net/http"
	"sync/atomic"

	"github.com/labstack/echo/v4"
)

// The four values DiscordHealthState.Status can take. [F185-06]
//
// All four map to HTTP 200 — deliberately, unlike llmHealthResponse's status
// code carrying the verdict. This endpoint must never become a second thing
// that can fail Railway's deploy healthcheck: /health (main.go:198-200) is
// the only route Railway's healthcheckPath watches, and it never reads
// Discord state. Callers of this endpoint read the status field, not the
// HTTP code.
const (
	// DiscordStatusStarting is the state before the one-shot startup
	// attempt has produced an outcome — the default until Set() is called,
	// and also the state for as long as the background start goroutine has
	// neither returned nor been timed out. It can be a terminal state: if
	// bot.Start() never returns (see [F185-05]'s startBotAndHealth), the
	// state only leaves "starting" via the timeout watcher.
	DiscordStatusStarting = "starting"
	// DiscordStatusOK means the bot's Start() call returned nil.
	DiscordStatusOK = "ok"
	// DiscordStatusDegraded means Start() returned an error, panicked, or
	// timed out before returning. The bot is not running; the server is.
	DiscordStatusDegraded = "degraded"
	// DiscordStatusUnconfigured means no bot was ever attempted — either no
	// DISCORD_BOT_TOKEN is set, or DISCORD_ENV=local. Set explicitly by the
	// two no-bot branches of startDiscordBotIfConfigured; never the zero
	// value (that's DiscordStatusStarting).
	DiscordStatusUnconfigured = "unconfigured"
)

// DiscordHealthState is the JSON shape for GET /api/health/discord.
type DiscordHealthState struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// DiscordHealthHandler serves the Discord bot startup health surface.
//
// state is an atomic.Pointer, not a bare field, because [F185-05]'s
// startBotAndHealth runs bot.Start() in a background goroutine and writes
// the outcome asynchronously — possibly concurrently with a timeout watcher
// goroutine racing to write its own "degraded" outcome, and with in-flight
// GetDiscordHealth reads. Unlike LLMHealthHandler (which holds a *llm.Chain
// that already synchronizes its own reads internally), this handler owns the
// mutable state directly, so it needs its own concurrency-safe storage.
type DiscordHealthHandler struct {
	state atomic.Pointer[DiscordHealthState]
}

// NewDiscordHealthHandler builds the handler pre-populated with the
// "starting" state. Before Set() is ever called, an in-flight bot connection
// attempt must read as "starting", not as an error and not as
// "unconfigured" — "unconfigured" is reserved for the two no-bot-configured
// branches of startDiscordBotIfConfigured, which call Set() themselves.
func NewDiscordHealthHandler() *DiscordHealthHandler {
	h := &DiscordHealthHandler{}
	h.state.Store(&DiscordHealthState{Status: DiscordStatusStarting})
	return h
}

// Set records the current Discord bot health outcome. Safe to call
// concurrently with GetDiscordHealth and with itself: the start goroutine and
// the timeout watcher goroutine ([F185-05]) can both attempt to call Set()
// for the same startup attempt, guarded upstream by a sync.Once so only one
// write actually lands, but Set() itself does not assume single-writer.
func (h *DiscordHealthHandler) Set(s DiscordHealthState) { h.state.Store(&s) }

// Load returns the current health state. Exposed mainly for tests in other
// packages (cmd/server) that need to synchronize on an async Set() without
// going through the HTTP handler and a fake echo.Context.
func (h *DiscordHealthHandler) Load() DiscordHealthState {
	if s := h.state.Load(); s != nil {
		return *s
	}
	return DiscordHealthState{Status: DiscordStatusStarting}
}

// GetDiscordHealth handles GET /api/health/discord. Always returns 200 — see
// the Status const doc comment for why.
func (h *DiscordHealthHandler) GetDiscordHealth(c echo.Context) error {
	s := h.state.Load()
	if s == nil {
		// Defensive: only reachable if a *DiscordHealthHandler is
		// constructed as a zero value rather than via
		// NewDiscordHealthHandler (the sole production construction site).
		// Treat as "starting", never as an error.
		s = &DiscordHealthState{Status: DiscordStatusStarting}
	}
	return c.JSON(http.StatusOK, s)
}
