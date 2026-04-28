package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// LearningHandler handles all Learning-domain endpoints.
type LearningHandler struct {
	store learningStore
}

// NewLearningHandler creates a LearningHandler.
func NewLearningHandler(s learningStore) *LearningHandler {
	return &LearningHandler{store: s}
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
