package mcp

import (
	"context"
	"errors"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/db"
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

// learningTextMaxRunes bounds DueReview/Concept Title/Content on read — U13
// (2026-08-20-mcp-surface-spec.md). Neither field has a write-time
// neutralisation step (create_concept below only requires non-empty), so
// this is sized like wrapUntrustedTask's gtdTitleMaxRunes/gtdBodyMaxRunes
// (tools_gtd.go) — generous enough that legitimate content never trips it.
const learningTextMaxRunes = gtdBodyMaxRunes

// wrapUntrustedDueReview returns a copy of r with Title/Content clipSafe'd
// (tools_context.go).
func wrapUntrustedDueReview(r learning.DueReview) learning.DueReview {
	r.Title = clipSafe(r.Title, learningTextMaxRunes)
	r.Content = clipSafe(r.Content, learningTextMaxRunes)
	return r
}

// wrapUntrustedDueReviews maps wrapUntrustedDueReview over a slice, always
// non-nil (preserves get_due_reviews' "[] never null" list contract).
func wrapUntrustedDueReviews(reviews []learning.DueReview) []learning.DueReview {
	out := make([]learning.DueReview, len(reviews))
	for i, r := range reviews {
		out[i] = wrapUntrustedDueReview(r)
	}
	return out
}

// conceptTagMaxRunes bounds each Tags entry on read. Sized like
// atomKeywordMaxRunes (tools_atom.go) rather than learningTextMaxRunes: a tag
// is a label, not a body, so a cap three orders of magnitude smaller still
// never touches a legitimate value while bounding what one poisoned row can
// contribute to a list_concepts response.
const conceptTagMaxRunes = 200

// wrapUntrustedConcept returns a copy of c with Title/Content clipSafe'd.
// nil in, nil out.
//
// [F170-19] Tags is clipped too. create_concept takes tags as free-text
// caller input with no write-time validation, and list_concepts renders them
// straight back into an LLM context alongside fields that ARE fenced — which
// is the whole reason boundary_markers.go neutralises the entire marker set
// on every field rather than only the pair belonging to the field being
// processed. This was one of the seven gaps [F170-11]'s walker found and
// pinned in knownUnprotectedFields; it is the same escape class as F160-10,
// just a different field.
//
// The len()>0 guard mirrors wrapUntrustedAtom (tools_atom.go): clipSafeSlice
// allocates unconditionally, so calling it on a nil slice would turn a JSON
// `null` into `[]` — a silent wire-contract change for every concept that has
// no tags.
func wrapUntrustedConcept(c *db.Concept) *db.Concept {
	if c == nil {
		return nil
	}
	out := *c
	out.Title = clipSafe(c.Title, learningTextMaxRunes)
	out.Content = clipSafe(c.Content, learningTextMaxRunes)
	if len(c.Tags) > 0 {
		out.Tags = clipSafeSlice(c.Tags, conceptTagMaxRunes)
	}
	return &out
}

func (s *Server) handleGetDueReviews(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	reviews, err := s.learning.DueReviews(ctx, 50)
	if err != nil {
		return storeErrorResult("loading due reviews", err), nil
	}
	return jsonText(wrapUntrustedDueReviews(reviews))
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
		return storeErrorResult("loading review schedule", err), nil
	}

	if err := s.learning.SubmitReview(ctx, scheduleID, state, learning.Rating(ratingVal)); err != nil {
		if errors.Is(err, learning.ErrNotFound) {
			return mcp.NewToolResultError("review schedule not found"), nil
		}
		return storeErrorResult("submitting review", err), nil
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
		return storeErrorResult("creating concept", err), nil
	}
	return jsonText(wrapUntrustedConcept(concept))
}
