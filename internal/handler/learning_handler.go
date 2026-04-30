package handler

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// LearningHandler handles all Learning-domain endpoints.
type LearningHandler struct {
	store     learningStore
	knowledge knowledgeStore
	decisions suggestionDecisionStore
}

// NewLearningHandler creates a LearningHandler with optional knowledge and
// decision stores for the suggestions and from-knowledge endpoints.
// knowledge and decisions may be nil; those endpoints will return 501 when absent.
func NewLearningHandler(s learningStore, opts ...learningHandlerOption) *LearningHandler {
	h := &LearningHandler{store: s}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// learningHandlerOption is a functional option for LearningHandler.
type learningHandlerOption func(*LearningHandler)

// WithKnowledgeStore sets the knowledge store on the handler.
func WithKnowledgeStore(k knowledgeStore) learningHandlerOption {
	return func(h *LearningHandler) { h.knowledge = k }
}

// WithDecisionStore sets the decision store on the handler.
func WithDecisionStore(d suggestionDecisionStore) learningHandlerOption {
	return func(h *LearningHandler) { h.decisions = d }
}

// suggestionsResponse is the response body for GET /api/learning/suggestions.
type suggestionsResponse struct {
	KnowledgeItems []knowledgeSuggestion `json:"knowledge_items"`
	Decisions      []decisionSuggestion  `json:"decisions"`
}

type knowledgeSuggestion struct {
	ID            uuid.UUID `json:"id"`
	Title         string    `json:"title"`
	Content       string    `json:"content"`
	Tags          []string  `json:"tags"`
	LearningValue int32     `json:"learning_value"`
}

type decisionSuggestion struct {
	ID      uuid.UUID `json:"id"`
	Title   string    `json:"title"`
	Context string    `json:"context"`
}

// GetSuggestions returns AI-curated concept suggestions from the knowledge base
// and recent decisions.
func (h *LearningHandler) GetSuggestions(c echo.Context) error {
	ctx := c.Request().Context()

	var knowledgeSuggestions []knowledgeSuggestion
	if h.knowledge != nil {
		items, err := h.knowledge.List(ctx, 50, 0)
		if err != nil {
			c.Logger().Errorf("GetSuggestions: list knowledge: %v", err)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		var filtered []db.KnowledgeItem
		for _, item := range items {
			if item.LearningValue.Valid && item.LearningValue.Int32 >= 2 {
				filtered = append(filtered, item)
			}
		}
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].LearningValue.Int32 > filtered[j].LearningValue.Int32
		})
		if len(filtered) > 10 {
			filtered = filtered[:10]
		}
		knowledgeSuggestions = make([]knowledgeSuggestion, 0, len(filtered))
		for _, item := range filtered {
			knowledgeSuggestions = append(knowledgeSuggestions, knowledgeSuggestion{
				ID:            item.ID,
				Title:         item.Title,
				Content:       item.Content,
				Tags:          item.Tags,
				LearningValue: item.LearningValue.Int32,
			})
		}
	}
	if knowledgeSuggestions == nil {
		knowledgeSuggestions = []knowledgeSuggestion{}
	}

	var decisionSuggestions []decisionSuggestion
	if h.decisions != nil {
		recent, err := h.decisions.All(ctx, 5)
		if err != nil {
			c.Logger().Errorf("GetSuggestions: list decisions: %v", err)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		decisionSuggestions = make([]decisionSuggestion, 0, len(recent))
		for _, d := range recent {
			decisionSuggestions = append(decisionSuggestions, decisionSuggestion{
				ID:      d.ID,
				Title:   d.Title,
				Context: d.Context,
			})
		}
	}
	if decisionSuggestions == nil {
		decisionSuggestions = []decisionSuggestion{}
	}

	return c.JSON(http.StatusOK, suggestionsResponse{
		KnowledgeItems: knowledgeSuggestions,
		Decisions:      decisionSuggestions,
	})
}

type createFromKnowledgeRequest struct {
	KnowledgeID string `json:"knowledge_id"`
}

// CreateConceptFromKnowledge creates a learning concept from an existing
// knowledge item in one click.
func (h *LearningHandler) CreateConceptFromKnowledge(c echo.Context) error {
	var req createFromKnowledgeRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	id, err := uuid.Parse(req.KnowledgeID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid knowledge_id: must be a valid UUID"))
	}

	if h.knowledge == nil {
		return c.JSON(http.StatusInternalServerError, errResp("knowledge store not configured"))
	}

	item, err := h.knowledge.GetByID(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, knowledge.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errResp("knowledge item not found"))
		}
		c.Logger().Errorf("CreateConceptFromKnowledge: get knowledge: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	concept, err := h.store.CreateConcept(c.Request().Context(), item.Title, item.Content, item.Tags)
	if err != nil {
		c.Logger().Errorf("CreateConceptFromKnowledge: create concept: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusCreated, concept)
}

// GetDueReviews returns concepts currently due for review.
func (h *LearningHandler) GetDueReviews(c echo.Context) error {
	limit, err := strconv.Atoi(c.QueryParam("limit"))
	if err != nil || limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	reviews, err := h.store.DueReviews(c.Request().Context(), limit)
	if err != nil {
		c.Logger().Errorf("GetDueReviews: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, reviews)
}

type submitReviewRequest struct {
	Rating      int     `json:"rating"`
	Stability   float64 `json:"stability"`
	Difficulty  float64 `json:"difficulty"`
	ReviewCount int     `json:"review_count"`
}

// SubmitReview applies an FSRS rating for a scheduled review.
func (h *LearningHandler) SubmitReview(c echo.Context) error {
	scheduleID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid schedule id"))
	}

	var req submitReviewRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.Rating < 1 || req.Rating > 4 {
		return c.JSON(http.StatusBadRequest, errResp("rating must be between 1 and 4"))
	}

	stability := req.Stability
	if stability == 0 {
		stability = 1.0
	}

	state := learning.CardState{
		Stability:   stability,
		Difficulty:  req.Difficulty,
		ReviewCount: req.ReviewCount,
	}

	if err := h.store.SubmitReview(c.Request().Context(), scheduleID, state, learning.Rating(req.Rating)); err != nil {
		if errors.Is(err, learning.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errResp("review schedule not found"))
		}
		c.Logger().Errorf("SubmitReview: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

type createConceptRequest struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

// CreateConcept inserts a new concept with an initial review schedule.
func (h *LearningHandler) CreateConcept(c echo.Context) error {
	var req createConceptRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Content = strings.TrimSpace(req.Content)
	if req.Title == "" || req.Content == "" {
		return c.JSON(http.StatusBadRequest, errResp("title and content are required"))
	}

	concept, err := h.store.CreateConcept(c.Request().Context(), req.Title, req.Content, req.Tags)
	if err != nil {
		c.Logger().Errorf("CreateConcept: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusCreated, concept)
}
