package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// resourcesListSerializedMaxBytes / promptsListSerializedMaxBytes —
// [F1003-04]. Derived the same way coreToolSerializedMaxBytes was
// (toolgroups_test.go): measured once via t.Logf (resources/list = 2808
// bytes, prompts/list = 846 bytes, `go test -run
// 'TestResourcesList_SerializedBytesWithinBudget|TestPromptsList_SerializedBytesWithinBudget'
// -v ./internal/mcp/...`), then hardcoded as ceil(measured x 1.15). Retune
// alongside a fresh measurement when a resource/prompt is added or its
// description grows — never bump silently.
const (
	resourcesListSerializedMaxBytes = 3230 // ceil(2808 * 1.15)
	promptsListSerializedMaxBytes   = 973  // ceil(846 * 1.15)
)

// TestResourcesList_SerializedBytesWithinBudget pins resources/list's wire
// payload size. Before this ticket, resources/list had no budget test at
// all — only the core tool set and mcpInstructions were pinned
// (toolgroups_test.go).
func TestResourcesList_SerializedBytesWithinBudget(t *testing.T) {
	t.Parallel()
	_, ms := newTestMCPServer(t)
	resp := ms.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`))
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal resources/list response: %v", err)
	}
	var decoded struct {
		Result struct {
			Resources []mcpmsg.Resource `json:"resources"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode resources/list response: %v\nraw: %s", err, raw)
	}
	if decoded.Error != nil {
		t.Fatalf("resources/list returned error: %s", decoded.Error.Message)
	}
	payload, err := json.Marshal(decoded.Result.Resources)
	if err != nil {
		t.Fatalf("marshal resources payload: %v", err)
	}
	got := len(payload)
	t.Logf("resources/list = %d resources, %d serialized bytes (budget %d)",
		len(decoded.Result.Resources), got, resourcesListSerializedMaxBytes)
	if got > resourcesListSerializedMaxBytes {
		t.Errorf("resources/list serializes to %d bytes, budget is %d — shrink a resource description "+
			"OR retune resourcesListSerializedMaxBytes alongside a fresh measurement — pick one, don't "+
			"silently bump the number", got, resourcesListSerializedMaxBytes)
	}
}

// TestPromptsList_SerializedBytesWithinBudget is
// TestResourcesList_SerializedBytesWithinBudget's structural twin for
// prompts/list — [F1003-04].
func TestPromptsList_SerializedBytesWithinBudget(t *testing.T) {
	t.Parallel()
	_, ms := newTestMCPServer(t)
	resp := ms.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`))
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal prompts/list response: %v", err)
	}
	var decoded struct {
		Result struct {
			Prompts []mcpmsg.Prompt `json:"prompts"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode prompts/list response: %v\nraw: %s", err, raw)
	}
	if decoded.Error != nil {
		t.Fatalf("prompts/list returned error: %s", decoded.Error.Message)
	}
	payload, err := json.Marshal(decoded.Result.Prompts)
	if err != nil {
		t.Fatalf("marshal prompts payload: %v", err)
	}
	got := len(payload)
	t.Logf("prompts/list = %d prompts, %d serialized bytes (budget %d)",
		len(decoded.Result.Prompts), got, promptsListSerializedMaxBytes)
	if got > promptsListSerializedMaxBytes {
		t.Errorf("prompts/list serializes to %d bytes, budget is %d — shrink a prompt description "+
			"OR retune promptsListSerializedMaxBytes alongside a fresh measurement — pick one, don't "+
			"silently bump the number", got, promptsListSerializedMaxBytes)
	}
}
