package handler

import (
	"net/http"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/timeline"
	"github.com/labstack/echo/v4"
)

// maxTimelineRangeDays is the maximum allowed date range for a single request.
const maxTimelineRangeDays = 366

// TimelineHandler handles the aggregated timeline endpoint.
type TimelineHandler struct {
	agg timelineAggregator
}

// NewTimelineHandler creates a TimelineHandler backed by the given aggregator.
func NewTimelineHandler(agg timelineAggregator) *TimelineHandler {
	return &TimelineHandler{agg: agg}
}

// timelineResponse is the JSON envelope returned by GET /api/timeline.
type timelineResponse struct {
	Events []timeline.Event `json:"events"`
	From   string           `json:"from"`
	To     string           `json:"to"`
}

// GetTimeline handles GET /api/timeline?from=<RFC3339>&to=<RFC3339>.
//
// Both params are optional:
//   - Absent → default: to=now, from=now-30days.
//   - Range > 366 days → 400.
//   - Unparseable date → 400.
func (h *TimelineHandler) GetTimeline(c echo.Context) error {
	now := time.Now().UTC()

	fromStr := c.QueryParam("from")
	toStr := c.QueryParam("to")

	var from, to time.Time

	if toStr == "" {
		to = now
	} else {
		var err error
		to, err = time.Parse(time.RFC3339, toStr)
		if err != nil {
			return c.JSON(http.StatusBadRequest, errResp("invalid 'to' param: must be RFC3339"))
		}
		to = to.UTC()
	}

	if fromStr == "" {
		from = to.Add(-30 * 24 * time.Hour)
	} else {
		var err error
		from, err = time.Parse(time.RFC3339, fromStr)
		if err != nil {
			return c.JSON(http.StatusBadRequest, errResp("invalid 'from' param: must be RFC3339"))
		}
		from = from.UTC()
	}

	if from.After(to) {
		return c.JSON(http.StatusBadRequest, errResp("'from' must not be after 'to'"))
	}
	if to.Sub(from) > maxTimelineRangeDays*24*time.Hour {
		return c.JSON(http.StatusBadRequest, errResp("date range must not exceed 366 days"))
	}

	events, err := h.agg.Aggregate(c.Request().Context(), from, to)
	if err != nil {
		c.Logger().Errorf("GetTimeline: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	if events == nil {
		events = []timeline.Event{}
	}

	return c.JSON(http.StatusOK, timelineResponse{
		Events: events,
		From:   from.Format(time.RFC3339),
		To:     to.Format(time.RFC3339),
	})
}
