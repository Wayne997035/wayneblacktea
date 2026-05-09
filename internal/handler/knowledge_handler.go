package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
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

const (
	knowledgeMaxTitleLen   = 200
	knowledgeMaxContentLen = 1 * 1024 * 1024 // 1 MB — matches global BodyLimit("1M") middleware; raising requires per-route override
	knowledgeMaxTagCount   = 20
	knowledgeMaxTagItemLen = 50
)

// validateAddKnowledgeRequest checks field-level invariants for AddKnowledge.
// It returns a non-empty human-readable message on the first violation, or an
// empty string when all fields are valid.
func validateAddKnowledgeRequest(req addKnowledgeRequest) string {
	validTypes := map[string]bool{"article": true, "til": true, "bookmark": true, "zettelkasten": true}
	switch {
	case req.Type == "" || req.Title == "":
		return "type and title are required"
	case !validTypes[req.Type]:
		return "type must be one of: article, til, bookmark, zettelkasten"
	case req.LearningValue != 0 && (req.LearningValue < 1 || req.LearningValue > 5):
		return "learning_value must be between 1 and 5"
	case len(req.Title) > knowledgeMaxTitleLen:
		return "title exceeds 200-character limit"
	case len(req.Content) > knowledgeMaxContentLen:
		return "content exceeds 1 MB limit"
	case req.URL != "" && !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://"):
		return "url must start with http:// or https://"
	case len(req.Tags) > knowledgeMaxTagCount:
		return "tags array exceeds 20-entry limit"
	}
	for _, tag := range req.Tags {
		if len(tag) > knowledgeMaxTagItemLen {
			return "each tag must be 50 characters or fewer"
		}
	}
	return ""
}

// AddKnowledge creates a new knowledge item.
func (h *KnowledgeHandler) AddKnowledge(c echo.Context) error {
	var req addKnowledgeRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if msg := validateAddKnowledgeRequest(req); msg != "" {
		return c.JSON(http.StatusBadRequest, errResp(msg))
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

type updateLearningValueRequest struct {
	LearningValue int `json:"learning_value"`
}

// UpdateLearningValue handles PATCH /api/knowledge/:id.
// Body: { "learning_value": N } where 1 ≤ N ≤ 5.
func (h *KnowledgeHandler) UpdateLearningValue(c echo.Context) error {
	rawID := c.Param("id")
	id, err := uuid.Parse(rawID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid knowledge id"))
	}

	var req updateLearningValueRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}

	if req.LearningValue < 1 || req.LearningValue > 5 {
		return c.JSON(http.StatusBadRequest, errResp("learning_value must be between 1 and 5"))
	}

	if err := h.store.UpdateLearningValue(c.Request().Context(), id, req.LearningValue); err != nil {
		if errors.Is(err, knowledge.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errResp("knowledge item not found"))
		}
		c.Logger().Errorf("UpdateLearningValue %s: %v", id, err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, map[string]int{"learning_value": req.LearningValue})
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
