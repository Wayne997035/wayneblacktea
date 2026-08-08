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
		"get_today_context",
		"log_decision",
		"complete_task",
		"set_session_handoff",
		"search_knowledge",
		"confirm_plan",
		"add_task",
		"auto-log",
	}
	for _, section := range requiredSections {
		if !strings.Contains(textContent.Text, section) {
			t.Errorf("instructions missing expected section %q", section)
		}
	}
}

// TestHandleInitialInstructions_ReturnsProtocolFull replaces the former
// TestHandleInitialInstructions_SameAsMCPInstructions, which asserted the tool
// returned mcpInstructions VERBATIM. That contract was deliberately broken:
// mcpInstructions is now the budgeted subset injected at `initialize` time,
// and initial_instructions serves mcpProtocolFull (= mcpInstructions + the
// appendix). The assertion below is stronger than the one it replaces — it
// pins the DERIVATION, so the two strings can never drift into two
// independently-edited copies of the protocol.
func TestHandleInitialInstructions_ReturnsProtocolFull(t *testing.T) {
	s := &Server{}
	result, err := s.handleInitialInstructions(context.Background(), mcpmsg.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	textContent, ok := result.Content[0].(mcpmsg.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	if textContent.Text != mcpProtocolFull {
		t.Error("initial_instructions must return mcpProtocolFull verbatim")
	}
	if !strings.HasPrefix(mcpProtocolFull, mcpInstructions) {
		t.Error("mcpProtocolFull must start with mcpInstructions — it is defined as a " +
			"concatenation precisely so a second literal copy cannot drift")
	}
	if !strings.HasSuffix(mcpProtocolFull, mcpProtocolAppendix) {
		t.Error("mcpProtocolFull must end with mcpProtocolAppendix")
	}
	if len(textContent.Text) <= len(mcpInstructions) {
		t.Error("initial_instructions returned no more than the injected protocol — " +
			"the appendix is missing")
	}
}

// TestMCPProtocolAppendix_KeepsDetailMovedOutOfDescriptions pins the content
// that was cut from the injected protocol and from the four fattest core tool
// descriptions. Trimming for byte budget is only legitimate if the detail
// survives somewhere the client can still fetch it.
func TestMCPProtocolAppendix_KeepsDetailMovedOutOfDescriptions(t *testing.T) {
	moved := []struct {
		source   string
		fragment string
	}{
		{"routing table: playbooks", "list_playbooks"},
		{"routing table: add_procedural", "add_procedural"},
		{"routing table: query_procedural", "query_procedural"},
		{"routing table: mark_procedural_used", "mark_procedural_used"},
		{"routing table: recall", "recall with query"},
		{"routing table: scope change", "log_decision + update_task + set_session_handoff"},
		{"update_task triggers section", "## update_task triggers"},
		{"confirm_plan trigger vocabulary", "\"衝\" \"開工\""},
		{"complete_task trigger vocabulary", "\"搞定\""},
		{"add_vision_item trigger vocabulary", "\"等 X 完成才能做\""},
		{"confirm_plan: work_session carve-out", "best-effort, AFTER the transaction commits"},
		{"confirm_plan: phases field shape", `{"title":"...","description":"...","priority":2}`},
		{"confirm_plan: assignee gate", "P6.8 assignee gate"},
		{"record_outcome: notes accumulation", "notes_truncated=true"},
		{"record_outcome: rule id accumulation", "related_rule_ids_truncated=true"},
		{"record_outcome: session link", "SetOutcomeLink"},
		{"add_task: priority vs importance", "DIFFERENT axis"},
		{"add_task: kind values", "general (default), fix-pr, feature, refactor, research, chore"},
		{"update_task: clearing convention", "empty string to clear the field"},
		{"complete_task: artifact detection", "40-character hex SHA"},
	}
	for _, tc := range moved {
		t.Run(tc.source, func(t *testing.T) {
			if !strings.Contains(mcpProtocolAppendix, tc.fragment) {
				t.Errorf("appendix lost %q (moved out of %s but never landed)", tc.fragment, tc.source)
			}
		})
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
