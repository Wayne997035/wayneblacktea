package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/waynechen/wayneblacktea/internal/knowledge"
)

// KnowledgeHandler handles all Knowledge-domain endpoints.
type KnowledgeHandler struct {
	store    knowledgeStore
	proposal proposalStore // optional; nil disables auto-propose-concept
}

// NewKnowledgeHandler creates a KnowledgeHandler. proposal may be nil to opt
// out of the auto-propose-concept-card behaviour (mainly for tests).
func NewKnowledgeHandler(s knowledgeStore, p proposalStore) *KnowledgeHandler {
	return &KnowledgeHandler{store: s, proposal: p}
}

// ListKnowledge returns knowledge items with optional pagination.
func (h *KnowledgeHandler) ListKnowledge(c echo.Context) error {
	limit, err := strconv.Atoi(c.QueryParam("limit"))
	if err != nil || limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	offset, err := strconv.Atoi(c.QueryParam("offset"))
	if err != nil || offset < 0 {
		offset = 0
	}

	items, err := h.store.List(c.Request().Context(), limit, offset)
	if err != nil {
		c.Logger().Errorf("ListKnowledge: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, items)
}

type addKnowledgeRequest struct {
	Type          string   `json:"type"`
	Title         string   `json:"title"`
	Content       string   `json:"content"`
	URL           string   `json:"url"`
	Tags          []string `json:"tags"`
	Source        string   `json:"source"`
	LearningValue int      `json:"learning_value"`
}

// AddKnowledge creates a new knowledge item.
func (h *KnowledgeHandler) AddKnowledge(c echo.Context) error {
	var req addKnowledgeRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.Type == "" || req.Title == "" {
		return c.JSON(http.StatusBadRequest, errResp("type and title are required"))
	}

	validTypes := map[string]bool{"article": true, "til": true, "bookmark": true, "zettelkasten": true}
	if !validTypes[req.Type] {
		return c.JSON(http.StatusBadRequest, errResp("type must be one of: article, til, bookmark, zettelkasten"))
	}

	item, err := h.store.AddItem(c.Request().Context(), knowledge.AddItemParams{
		Type:          req.Type,
		Title:         req.Title,
		Content:       req.Content,
		URL:           req.URL,
		Tags:          req.Tags,
		Source:        req.Source,
		LearningValue: req.LearningValue,
	})
	if err != nil {
		var dupErr knowledge.ErrDuplicate
		if errors.As(err, &dupErr) {
			return c.JSON(http.StatusConflict, errResp(dupErr.Error()))
		}
		c.Logger().Errorf("AddKnowledge: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	// Best-effort auto-propose: failure here must not roll back the freshly
	// created knowledge item. The proposal can be retried by the agent.
	if h.proposal != nil {
		if _, err := h.proposal.AutoProposeConceptFromKnowledge(
			c.Request().Context(), item, "http:add_knowledge",
		); err != nil {
			c.Logger().Errorf("auto-propose concept (knowledge_id=%s): %v", item.ID, err)
		}
	}
	return c.JSON(http.StatusCreated, item)
}

// SearchKnowledge searches knowledge items by full-text query.
func (h *KnowledgeHandler) SearchKnowledge(c echo.Context) error {
	query := strings.TrimSpace(c.QueryParam("q"))
	if query == "" {
		return c.JSON(http.StatusBadRequest, errResp("q parameter is required"))
	}

	limit, err := strconv.Atoi(c.QueryParam("limit"))
	if err != nil || limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	items, err := h.store.Search(c.Request().Context(), query, limit)
	if err != nil {
		c.Logger().Errorf("SearchKnowledge: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, items)
}
