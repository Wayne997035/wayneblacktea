package mcp

import (
	"context"
	"strings"
	"testing"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

func TestHandleInitialInstructions_ReturnsProtocol(t *testing.T) {
	s := &Server{}
	result, err := s.handleInitialInstructions(context.Background(), mcpmsg.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Fatal("expected success result, got error")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected at least one content item")
	}

	textContent, ok := result.Content[0].(mcpmsg.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	if textContent.Text == "" {
		t.Fatal("expected non-empty instructions text")
	}

	// Verify key sections are present.
	requiredSections := []string{
		"MANDATORY",
		"get_today_context",
		"log_decision",
		"complete_task",
		"set_session_handoff",
		"search_knowledge",
		"When user signals task completion",
		"Session-end auto-handoff",
	}
	for _, section := range requiredSections {
		if !strings.Contains(textContent.Text, section) {
			t.Errorf("instructions missing expected section %q", section)
		}
	}
}

func TestHandleInitialInstructions_SameAsMCPInstructions(t *testing.T) {
	s := &Server{}
	result, err := s.handleInitialInstructions(context.Background(), mcpmsg.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	textContent, ok := result.Content[0].(mcpmsg.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	// The tool must return exactly the same string as mcpInstructions.
	if textContent.Text != mcpInstructions {
		t.Errorf("returned text does not match mcpInstructions constant")
	}
}

func TestMCPInstructions_NotEmpty(t *testing.T) {
	if mcpInstructions == "" {
		t.Fatal("mcpInstructions constant must not be empty")
	}
	if len(mcpInstructions) < 200 {
		t.Errorf("mcpInstructions seems too short (%d chars), expected at least 200", len(mcpInstructions))
	}
}
