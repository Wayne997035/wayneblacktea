package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/waynechen/wayneblacktea/internal/learning"
)

func (s *Server) registerLearningTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("get_due_reviews",
		mcp.WithDescription("Returns all concepts currently due for review."),
	), s.handleGetDueReviews)

	ms.AddTool(mcp.NewTool("submit_review",
		mcp.WithDescription("Submits a review rating for a concept and updates the next review schedule."),
		mcp.WithString("schedule_id", mcp.Description("Review schedule UUID"), mcp.Required()),
		mcp.WithNumber("rating", mcp.Description("Rating: 1=Again, 2=Hard, 3=Good, 4=Easy"), mcp.Required()),
		mcp.WithNumber("stability", mcp.Description("Current stability value from get_due_reviews")),
		mcp.WithNumber("difficulty", mcp.Description("Current difficulty value from get_due_reviews")),
		mcp.WithNumber("review_count", mcp.Description("Current review count from get_due_reviews")),
	), s.handleSubmitReview)

	ms.AddTool(mcp.NewTool("create_concept",
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

	rawID := stringArg(args, "schedule_id")
	if rawID == "" {
		return mcp.NewToolResultError("schedule_id is required"), nil
	}
	scheduleID, err := uuid.Parse(rawID)
	if err != nil {
		return mcp.NewToolResultError("invalid schedule_id UUID"), nil
	}

	ratingVal := int(numberArg(args, "rating"))
	if ratingVal < 1 || ratingVal > 4 {
		return mcp.NewToolResultError("rating must be between 1 and 4"), nil
	}

	state := learning.CardState{
		Stability:   floatArg(args, "stability"),
		Difficulty:  floatArg(args, "difficulty"),
		ReviewCount: int(numberArg(args, "review_count")),
	}
	// Default stability if not provided.
	if state.Stability == 0 {
		state.Stability = 1.0
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

	concept, err := s.learning.CreateConcept(ctx, title, content, tags)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating concept: %v", err)), nil
	}
	return jsonText(concept)
}
