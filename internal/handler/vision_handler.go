package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/Wayne997035/wayneblacktea/internal/vision"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// VisionHandler handles all vision-domain endpoints.
type VisionHandler struct {
	store visionStore
}

// visionStore is the narrow store interface VisionHandler depends on.
// Defined in interfaces.go to keep the pattern consistent.

// NewVisionHandler creates a VisionHandler.
func NewVisionHandler(s visionStore) *VisionHandler {
	return &VisionHandler{store: s}
}

type addVisionRequest struct {
	Title            string   `json:"title"`
	WhyBlocked       string   `json:"why_blocked"`
	DependsOn        []string `json:"depends_on"`
	ParentInitiative string   `json:"parent_initiative"`
	ContextMD        string   `json:"context_md"`
	RepoName         string   `json:"repo_name"`
}

// AddVision creates a new vision item.
func (h *VisionHandler) AddVision(c echo.Context) error {
	body := io.LimitReader(c.Request().Body, 1<<20) // 1 MB
	var req addVisionRequest
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.Title == "" {
		return c.JSON(http.StatusBadRequest, errResp("title is required"))
	}
	if len([]rune(req.Title)) > 255 {
		return c.JSON(http.StatusBadRequest, errResp("title exceeds 255 characters"))
	}
	if req.WhyBlocked == "" {
		return c.JSON(http.StatusBadRequest, errResp("why_blocked is required"))
	}
	if len([]rune(req.WhyBlocked)) > 2000 {
		return c.JSON(http.StatusBadRequest, errResp("why_blocked exceeds 2000 characters"))
	}

	// Vagueness check on title and why_blocked (warn-only; vision items are
	// not subject to strict mode — they are intentionally forward-looking).
	var allWarnings []string
	allWarnings = append(allWarnings, validator.CheckVagueness("title", req.Title, "general")...)
	allWarnings = append(allWarnings, validator.CheckVagueness("why_blocked", req.WhyBlocked, "general")...)
	if len(allWarnings) > 0 {
		warningsJSON, _ := json.Marshal(allWarnings)
		c.Response().Header().Set("X-Vagueness-Warnings", string(warningsJSON))
	}

	item, err := h.store.Add(c.Request().Context(), vision.AddVisionParams{
		Title:            req.Title,
		WhyBlocked:       req.WhyBlocked,
		DependsOn:        req.DependsOn,
		ParentInitiative: req.ParentInitiative,
		ContextMD:        req.ContextMD,
		RepoName:         req.RepoName,
	})
	if err != nil {
		c.Logger().Errorf("AddVision: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusCreated, item)
}

// ListVision returns vision items optionally filtered by status and/or initiative.
func (h *VisionHandler) ListVision(c echo.Context) error {
	filter := vision.ListVisionFilter{
		ParentInitiative: c.QueryParam("initiative"),
	}
	if rawStatus := c.QueryParam("status"); rawStatus != "" {
		st := vision.VisionStatus(rawStatus)
		switch st {
		case vision.VisionStatusOpen, vision.VisionStatusDiscussing,
			vision.VisionStatusMaturing, vision.VisionStatusPromoted,
			vision.VisionStatusDismissed:
		default:
			return c.JSON(http.StatusBadRequest, errResp("invalid status value"))
		}
		filter.Status = st
	}

	items, err := h.store.List(c.Request().Context(), filter)
	if err != nil {
		c.Logger().Errorf("ListVision: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	if items == nil {
		items = []vision.VisionItemSummary{}
	}
	return c.JSON(http.StatusOK, items)
}

type updateVisionRequest struct {
	Status          *vision.VisionStatus `json:"status"`
	ContextMD       *string              `json:"context_md"`
	LastDiscussedAt *time.Time           `json:"last_discussed_at"`
}

// UpdateVision patches a vision item's status or context.
func (h *VisionHandler) UpdateVision(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid vision item id"))
	}

	body := io.LimitReader(c.Request().Body, 1<<20)
	var req updateVisionRequest
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}

	if req.Status != nil {
		switch *req.Status {
		case vision.VisionStatusOpen, vision.VisionStatusDiscussing,
			vision.VisionStatusMaturing, vision.VisionStatusPromoted,
			vision.VisionStatusDismissed:
		default:
			return c.JSON(http.StatusBadRequest, errResp("invalid status value"))
		}
	}

	item, err := h.store.Update(c.Request().Context(), id, vision.UpdateVisionParams{
		Status:          req.Status,
		ContextMD:       req.ContextMD,
		LastDiscussedAt: req.LastDiscussedAt,
	})
	if errors.Is(err, vision.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errResp("vision item not found"))
	}
	if err != nil {
		c.Logger().Errorf("UpdateVision: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, item)
}
