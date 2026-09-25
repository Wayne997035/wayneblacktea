package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/Wayne997035/wayneblacktea/internal/vision"
	"github.com/google/uuid"
)

// countingVisionStore records Add calls; addErr, when set, is returned
// instead of an item.
type countingVisionStore struct {
	adds   int
	addErr error
}

func (c *countingVisionStore) Add(_ context.Context, p vision.AddVisionParams) (*vision.VisionItem, error) {
	c.adds++
	if c.addErr != nil {
		return nil, c.addErr
	}
	return &vision.VisionItem{ID: uuid.New(), Title: p.Title, RepoName: p.RepoName}, nil
}

func (c *countingVisionStore) List(context.Context, vision.ListVisionFilter) ([]vision.VisionItemSummary, error) {
	return nil, nil
}

func (c *countingVisionStore) Update(context.Context, uuid.UUID, vision.UpdateVisionParams) (*vision.VisionItem, error) {
	return nil, nil
}

const badRepoNameBody = `"repo_name":"_project/.claude"`

// TestLogDecision_RepoNameEntryCheck pins [F0925-29] on POST /api/decisions:
// a repo_name breaking the workspace repo name rule is a 400 naming the rule
// and never reaches the store; a store-side rejection is also a 400.
func TestLogDecision_RepoNameEntryCheck(t *testing.T) {
	t.Parallel()
	body := `{"title":"t","context":"c","decision":"d","rationale":"r",` + badRepoNameBody + `}`
	store := &fakeDecisionHandlerStore{}
	e := newEcho()
	e.POST("/api/decisions", handler.NewDecisionHandler(store).LogDecision)
	rec := performRequest(e, http.MethodPost, "/api/decisions", body)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "repo_name must be") {
		t.Errorf("status %d body %s: want 400 naming the rule", rec.Code, rec.Body.String())
	}
	if len(store.logged) != 0 {
		t.Errorf("store Log called %d times, want 0", len(store.logged))
	}

	rejecting := &fakeDecisionHandlerStore{logErr: fmt.Errorf("log_decision: %w", validator.ErrInvalidRepoName)}
	e2 := newEcho()
	e2.POST("/api/decisions", handler.NewDecisionHandler(rejecting).LogDecision)
	rec = performRequest(e2, http.MethodPost, "/api/decisions", `{"title":"t","context":"c","decision":"d","rationale":"r"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("store ErrInvalidRepoName: status %d, want 400", rec.Code)
	}
}

// TestAddVision_RepoNameEntryCheck pins the same contract on POST /api/vision.
func TestAddVision_RepoNameEntryCheck(t *testing.T) {
	t.Parallel()
	body := `{"title":"t","why_blocked":"w",` + badRepoNameBody + `}`
	store := &countingVisionStore{}
	e := newEcho()
	e.POST("/api/vision", handler.NewVisionHandler(store).AddVision)
	rec := performRequest(e, http.MethodPost, "/api/vision", body)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "repo_name must be") {
		t.Errorf("status %d body %s: want 400 naming the rule", rec.Code, rec.Body.String())
	}
	if store.adds != 0 {
		t.Errorf("store Add called %d times, want 0", store.adds)
	}

	rejecting := &countingVisionStore{addErr: fmt.Errorf("add_vision_item: %w", validator.ErrInvalidRepoName)}
	e2 := newEcho()
	e2.POST("/api/vision", handler.NewVisionHandler(rejecting).AddVision)
	rec = performRequest(e2, http.MethodPost, "/api/vision", `{"title":"t","why_blocked":"w"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("store ErrInvalidRepoName: status %d, want 400", rec.Code)
	}
}
