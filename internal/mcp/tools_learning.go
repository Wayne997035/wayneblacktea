package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerLearningTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"get_due_reviews",
		mcp.WithDescription("Returns all concepts currently due for review."),
	), s.handleGetDueReviews)

	ms.AddTool(mcp.NewTool(
		"submit_review",
		mcp.WithDescription(
			"Submits a review rating for a concept and updates the next review schedule. "+
				"The current stability/difficulty/review_count are read from the stored schedule "+
				"server-side — do not pass them.",
		),
		mcp.WithString("schedule_id", mcp.Description("Review schedule UUID"), mcp.Required()),
		mcp.WithNumber("rating", mcp.Description("Rating: 1=Again, 2=Hard, 3=Good, 4=Easy"), mcp.Required()),
	), s.handleSubmitReview)

	ms.AddTool(mcp.NewTool(
		"create_concept",
		mcp.WithDescription("Creates a new concept and initialises its FSRS review schedule."),
		mcp.WithString("title", mcp.Description("Concept title"), mcp.Required()),
		mcp.WithString("content", mcp.Description("Concept explanation / body"), mcp.Required()),
		mcp.WithString("tags", mcp.Description("Comma-separated tags")),
	), s.handleCreateConcept)
}

func (s *Server) handleGetDueReviews(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	reviews, err := s.learning.DueReviews(ctx, 50)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading due reviews: %v", err)), nil
	}
	return jsonText(reviews)
}

func (s *Server) handleSubmitReview(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	scheduleID, errResult := requireUUIDArg(args, "schedule_id", "invalid schedule_id UUID")
	if errResult != nil {
		return errResult, nil
	}

	ratingVal := int(numberArg(args, "rating"))
	if ratingVal < 1 || ratingVal > 4 {
		return mcp.NewToolResultError("rating must be between 1 and 4"), nil
	}

	// Ω7 fix (mcp-surface spec, backend-security-design.md §2.1): the
	// current CardState is read from the DB, never trusted from the caller.
	// submit_review used to accept stability/difficulty/review_count as
	// LLM-supplied "current state" params; an omitted or wrong review_count
	// made NextState treat a mature, many-times-reviewed schedule as a fresh
	// card, silently resetting it to a much shorter interval. Reading state
	// server-side removes that trust boundary entirely instead of trying to
	// distinguish "omitted" from "wrong."
	state, err := s.learning.GetScheduleState(ctx, scheduleID)
	if err != nil {
		if errors.Is(err, learning.ErrNotFound) {
			return mcp.NewToolResultError("review schedule not found"), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("loading review schedule: %v", err)), nil
	}

	if err := s.learning.SubmitReview(ctx, scheduleID, state, learning.Rating(ratingVal)); err != nil {
		if errors.Is(err, learning.ErrNotFound) {
			return mcp.NewToolResultError("review schedule not found"), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("submitting review: %v", err)), nil
	}
	return mcp.NewToolResultText("review submitted"), nil
}

func (s *Server) handleCreateConcept(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	title := stringArg(args, "title")
	content := stringArg(args, "content")
	if title == "" || content == "" {
		return mcp.NewToolResultError("title and content are required"), nil
	}

	var tags []string
	if raw := stringArg(args, "tags"); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(t); trimmed != "" {
				tags = append(tags, trimmed)
			}
		}
	}
	cleanedTags, reason := sanitizeTags(tags)
	if reason != "" {
		return mcp.NewToolResultError(string(reason)), nil
	}
	tags = cleanedTags

	concept, err := s.learning.CreateConcept(ctx, title, content, tags)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating concept: %v", err)), nil
	}
	return jsonText(concept)
}
