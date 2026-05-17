package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/labstack/echo/v4"
)

// postToolUseBodyLimit caps the request body from wbt-hook at 4 KB.
// The payload is tiny (actor + tool_name + sha256 hash), so 4 KB is generous.
const postToolUseBodyLimit = 4 * 1024

// postToolUseChannelSize is the buffered-channel capacity for enqueued events.
// When full, new events are dropped and a warning is logged (DoS protection).
const postToolUseChannelSize = 1000

// postToolUseRequest is the JSON body posted by cmd/wbt-hook.
type postToolUseRequest struct {
	Actor  string `json:"actor"`
	Action string `json:"tool_name"`
	Notes  string `json:"notes"`
}

// postToolUseEvent is an internal message queued in the channel.
type postToolUseEvent struct {
	Actor  string
	Action string
	Notes  string
}

// PostToolUseHandler handles POST /api/activity/posttooluse.
// It enqueues events into a buffered channel (fire-and-forget from the HTTP
// handler's perspective) and a background worker drains them to activity_log.
type PostToolUseHandler struct {
	gtd    autologGTDStore
	queue  chan postToolUseEvent
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewPostToolUseHandler creates a PostToolUseHandler and starts the background
// drain worker.  Call Stop() to shut it down gracefully.
func NewPostToolUseHandler(g autologGTDStore) *PostToolUseHandler {
	h := &PostToolUseHandler{
		gtd:    g,
		queue:  make(chan postToolUseEvent, postToolUseChannelSize),
		stopCh: make(chan struct{}),
	}
	h.wg.Add(1)
	go h.drainWorker()
	return h
}

// PostToolUse handles POST /api/activity/posttooluse.
// It reads the request, validates minimal fields, enqueues the event, and
// returns 202 immediately.  The actual DB write happens in the drain worker.
func (h *PostToolUseHandler) PostToolUse(c echo.Context) error {
	body := io.LimitReader(c.Request().Body, postToolUseBodyLimit)

	var req postToolUseRequest
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.Actor == "" {
		req.Actor = "claude-code"
	}
	req.Actor = sanitize.Notes(req.Actor)
	req.Action = sanitize.Notes(req.Action)
	req.Notes = sanitize.Notes(req.Notes)
	if req.Action == "" {
		return c.JSON(http.StatusBadRequest, errResp("tool_name is required"))
	}

	evt := postToolUseEvent(req)

	select {
	case h.queue <- evt:
		// enqueued successfully
	default:
		slog.Warn("posttooluse: channel full, dropping event",
			"actor", req.Actor,
			"action", req.Action,
		)
	}

	return c.JSON(http.StatusAccepted, map[string]string{"status": "queued"})
}

// Stop signals the drain worker to exit and waits for the flush to complete.
// It blocks until drainWorker has finished persisting all queued events so
// that in-flight events are never lost on server shutdown.
func (h *PostToolUseHandler) Stop() {
	close(h.stopCh)
	h.wg.Wait()
}

// drainWorker runs in a background goroutine and writes batched events to the
// activity_log table via the GTD store.  It uses context.Background() with an
// independent timeout so DB writes are never cancelled by a request context.
// It signals h.wg.Done() after flush() completes so Stop() can safely wait.
func (h *PostToolUseHandler) drainWorker() {
	defer h.wg.Done()
	for {
		select {
		case <-h.stopCh:
			// Drain remaining events before exit.
			h.flush()
			return
		case evt := <-h.queue:
			h.writeOne(evt)
		}
	}
}

// writeOne persists a single event to activity_log.
func (h *PostToolUseHandler) writeOne(evt postToolUseEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.gtd.LogActivity(ctx, evt.Actor, evt.Action, nil, evt.Notes); err != nil {
		slog.Warn("posttooluse: failed to log activity",
			"actor", evt.Actor,
			"action", evt.Action,
			"err", err,
		)
	}
}

// flush drains whatever is left in the queue (called on Stop).
func (h *PostToolUseHandler) flush() {
	for {
		select {
		case evt := <-h.queue:
			h.writeOne(evt)
		default:
			return
		}
	}
}
