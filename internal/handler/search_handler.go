package handler

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

const (
	maxSearchQueryLen             = 500
	knowledgeSearchLimit          = 5
	decisionSearchScanLimit int32 = 50
	decisionsResultCap            = 5
	tasksResultCap                = 5
)

// SearchResult is a unified result entry across entity types.
type SearchResult struct {
	Type    string    `json:"type"`
	ID      uuid.UUID `json:"id"`
	Title   string    `json:"title"`
	Content string    `json:"content"`
	Score   *float32  `json:"score"`
}

// searchResponse is the response body for GET /api/search.
type searchResponse struct {
	Results []SearchResult `json:"results"`
	Query   string         `json:"query"`
}

// SearchHandler handles cross-entity semantic search.
type SearchHandler struct {
	knowledge searchKnowledgeStore
	decision  searchDecisionStore
	gtd       searchGTDStore
}

// NewSearchHandler creates a SearchHandler with the provided narrow-interface stores.
func NewSearchHandler(k searchKnowledgeStore, d searchDecisionStore, g searchGTDStore) *SearchHandler {
	return &SearchHandler{knowledge: k, decision: d, gtd: g}
}

// Search handles GET /api/search?q=...
// It searches knowledge items (semantic), decisions (substring), and tasks (substring),
// and returns a unified result list.
func (h *SearchHandler) Search(c echo.Context) error {
	q := strings.TrimSpace(c.QueryParam("q"))
	if q == "" {
		return c.JSON(http.StatusBadRequest, errResp("q parameter is required"))
	}
	if len(q) > maxSearchQueryLen {
		return c.JSON(http.StatusBadRequest, errResp("q parameter exceeds maximum length of 500 characters"))
	}

	ctx := c.Request().Context()
	qLower := strings.ToLower(q)

	kResults, err := h.knowledgeResults(c, q)
	if err != nil {
		return err
	}
	dResults, err := h.decisionResults(c, qLower)
	if err != nil {
		return err
	}
	tResults, err := h.taskResults(c, qLower)
	if err != nil {
		return err
	}

	_ = ctx // ctx used inside helpers via echo context
	results := make([]SearchResult, 0, len(kResults)+len(dResults)+len(tResults))
	results = append(results, kResults...)
	results = append(results, dResults...)
	results = append(results, tResults...)

	return c.JSON(http.StatusOK, searchResponse{Results: results, Query: q})
}

func (h *SearchHandler) knowledgeResults(c echo.Context, q string) ([]SearchResult, error) {
	items, err := h.knowledge.Search(c.Request().Context(), q, knowledgeSearchLimit)
	if err != nil {
		c.Logger().Errorf("SearchHandler knowledge.Search: %v", err)
		return nil, c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	out := make([]SearchResult, 0, len(items))
	for _, item := range items {
		out = append(out, SearchResult{Type: "knowledge", ID: item.ID, Title: item.Title, Content: item.Content})
	}
	return out, nil
}

func (h *SearchHandler) decisionResults(c echo.Context, qLower string) ([]SearchResult, error) {
	decisions, err := h.decision.All(c.Request().Context(), decisionSearchScanLimit)
	if err != nil {
		c.Logger().Errorf("SearchHandler decision.All: %v", err)
		return nil, c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	out := make([]SearchResult, 0, decisionsResultCap)
	for _, d := range decisions {
		if len(out) >= decisionsResultCap {
			break
		}
		if strings.Contains(strings.ToLower(d.Title), qLower) ||
			strings.Contains(strings.ToLower(d.Decision), qLower) {
			out = append(out, SearchResult{Type: "decision", ID: d.ID, Title: d.Title, Content: d.Decision})
		}
	}
	return out, nil
}

func (h *SearchHandler) taskResults(c echo.Context, qLower string) ([]SearchResult, error) {
	tasks, err := h.gtd.Tasks(c.Request().Context(), nil)
	if err != nil {
		c.Logger().Errorf("SearchHandler gtd.Tasks: %v", err)
		return nil, c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	out := make([]SearchResult, 0, tasksResultCap)
	for _, t := range tasks {
		if len(out) >= tasksResultCap {
			break
		}
		desc := ""
		if t.Description.Valid {
			desc = t.Description.String
		}
		if strings.Contains(strings.ToLower(t.Title), qLower) ||
			strings.Contains(strings.ToLower(desc), qLower) {
			out = append(out, SearchResult{Type: "task", ID: t.ID, Title: t.Title, Content: desc})
		}
	}
	return out, nil
}
