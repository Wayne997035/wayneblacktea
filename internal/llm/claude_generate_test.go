package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

// genCapture records every request body a mock Anthropic server receives.
type genCapture struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (g *genCapture) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.bodies)
}

func (g *genCapture) last(t *testing.T) map[string]any {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.bodies) == 0 {
		t.Fatalf("mock server received no request")
	}
	var m map[string]any
	if err := json.Unmarshal(g.bodies[len(g.bodies)-1], &m); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	return m
}

// newGenServer serves the given status and body for every request and
// captures the request bodies.
func newGenServer(t *testing.T, status int, respBody string) (*httptest.Server, *genCapture) {
	t.Helper()
	cap := &genCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		cap.mu.Lock()
		cap.bodies = append(cap.bodies, b)
		cap.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

func genResponse(content []map[string]any, stop string) string {
	b, _ := json.Marshal(map[string]any{
		"id":            "msg_test",
		"type":          "message",
		"role":          "assistant",
		"model":         "claude-haiku-4-5",
		"content":       content,
		"stop_reason":   stop,
		"stop_sequence": nil,
		"usage":         map[string]any{"input_tokens": 11, "output_tokens": 7},
	})
	return string(b)
}

// bodyArray fetches body[key] as a non-empty array or fails with a readable
// message (never indexes blindly).
func bodyArray(t *testing.T, body map[string]any, key string) []any {
	t.Helper()
	v, ok := body[key]
	if !ok {
		t.Fatalf("request body has no %s", key)
	}
	arr, ok := v.([]any)
	if !ok || len(arr) < 1 {
		t.Fatalf("request body %s is not a non-empty array: %v", key, v)
	}
	return arr
}

// [F1123-50]
func TestClaudeProvider_Capabilities_ReportsToolCalling(t *testing.T) {
	c := newTestClaudeClient("http://unused.invalid")
	caps := c.Capabilities()
	if !caps.ToolCalling {
		t.Errorf("ToolCalling = false, want true")
	}
	if !caps.Text {
		t.Errorf("Text = false, want true")
	}
	if caps.ImageInput {
		t.Errorf("ImageInput = true, want false (Generate sends text only)")
	}
	if caps.MaxContext != 200000 {
		t.Errorf("MaxContext = %d, want 200000", caps.MaxContext)
	}
	if caps.AudioInput || caps.AudioOutput || caps.Realtime {
		t.Errorf("audio/realtime must be false: %+v", caps)
	}
}

// [F1123-50]
func TestClaudeProvider_Generate_ReturnsStructuredResult(t *testing.T) {
	srv, _ := newGenServer(t, 200, genResponse([]map[string]any{
		{"type": "text", "text": "ab"},
		{"type": "text", "text": "cd"},
	}, "end_turn"))
	c := newTestClaudeClient(srv.URL)
	res, err := c.Generate(context.Background(), GenerateRequest{Task: "t", User: "hi"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Text != "abcd" {
		t.Errorf("Text = %q, want abcd", res.Text)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn", res.StopReason)
	}
}

// [F1123-51]
func TestGenerateResult_CarriesUsageProviderModelLatencyErrorClass(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv, _ := newGenServer(t, 200, genResponse([]map[string]any{{"type": "text", "text": "ok"}}, "end_turn"))
		c := newTestClaudeClient(srv.URL)
		res, err := c.Generate(context.Background(), GenerateRequest{User: "hi"})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if res.Usage.InputTokens != 11 || res.Usage.OutputTokens != 7 {
			t.Errorf("Usage = %+v, want {11 7}", res.Usage)
		}
		if res.Provider != "claude" {
			t.Errorf("Provider = %q", res.Provider)
		}
		if res.Model == "" {
			t.Errorf("Model is empty")
		}
		if res.Latency <= 0 {
			t.Errorf("Latency = %v, want > 0", res.Latency)
		}
		if res.ErrorClass != "" {
			t.Errorf("ErrorClass = %q, want empty", res.ErrorClass)
		}
	})
	t.Run("server_error_5xx", func(t *testing.T) {
		srv, _ := newGenServer(t, 500, `{"type":"error"}`)
		c := newTestClaudeClient(srv.URL)
		res, err := c.Generate(context.Background(), GenerateRequest{User: "hi"})
		var retry *Retryable
		if !errors.As(err, &retry) {
			t.Fatalf("err type = %T, want *Retryable", err)
		}
		if res.ErrorClass != reasonHTTP5xx {
			t.Errorf("ErrorClass = %q, want %q", res.ErrorClass, reasonHTTP5xx)
		}
		if res.Provider == "" || res.Model == "" {
			t.Errorf("Provider/Model not filled on failure: %+v", res)
		}
		if res.Latency <= 0 {
			t.Errorf("Latency = %v, want > 0", res.Latency)
		}
	})
}

// [F1123-51]
func TestClaudeProvider_Generate_NoContentIsEmptyContentError(t *testing.T) {
	srv, _ := newGenServer(t, 200, genResponse([]map[string]any{}, "end_turn"))
	c := newTestClaudeClient(srv.URL)
	res, err := c.Generate(context.Background(), GenerateRequest{User: "hi"})
	var retry *Retryable
	if !errors.As(err, &retry) {
		t.Fatalf("err type = %T, want *Retryable", err)
	}
	if res.ErrorClass != reasonEmptyContent {
		t.Errorf("ErrorClass = %q, want %q", res.ErrorClass, reasonEmptyContent)
	}
}

// [F1123-52]
func TestClaudeProvider_Generate_MapsToolUseBlocks(t *testing.T) {
	srv, capt := newGenServer(t, 200, genResponse([]map[string]any{
		{"type": "tool_use", "id": "toolu_1", "name": "search", "input": map[string]any{"q": "x"}},
	}, "tool_use"))
	c := newTestClaudeClient(srv.URL)
	schema := `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"],"additionalProperties":false}`
	res, err := c.Generate(context.Background(), GenerateRequest{
		User:  "hi",
		Tools: []ToolDef{{Name: "search", Description: "d", InputSchema: json.RawMessage(schema)}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// 1: request side
	tools := bodyArray(t, capt.last(t), "tools")
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != "search" {
		t.Errorf("tools[0].name = %v", tool["name"])
	}
	is, _ := tool["input_schema"].(map[string]any)
	if is == nil {
		t.Fatalf("tools[0].input_schema missing: %v", tool)
	}
	if !reflect.DeepEqual(is["properties"], map[string]any{"q": map[string]any{"type": "string"}}) {
		t.Errorf("properties = %v", is["properties"])
	}
	if !reflect.DeepEqual(is["required"], []any{"q"}) {
		t.Errorf("required = %v", is["required"])
	}
	if is["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v, want false", is["additionalProperties"])
	}

	// 2: response side
	if len(res.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(res.ToolCalls))
	}
	tc := res.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Name != "search" {
		t.Errorf("ToolCall = %+v", tc)
	}
	var in map[string]any
	if err := json.Unmarshal(tc.Input, &in); err != nil || in["q"] != "x" {
		t.Errorf("ToolCall.Input = %s (err %v)", tc.Input, err)
	}

	// 3: tool_use only is success
	if res.Text != "" {
		t.Errorf("Text = %q, want empty", res.Text)
	}
}

// [F1123-52]
func TestClaudeProvider_Generate_InvalidToolSchemaRejected(t *testing.T) {
	cases := []struct {
		name   string
		schema string
	}{
		{"not_json", `not json`},
		{"null", `null`},
		{"array", `[1]`},
		{"string", `"s"`},
		{"type_string", `{"type":"string"}`},
		{"required_not_array", `{"required":"q"}`},
		{"required_non_string_elem", `{"required":[1]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, capt := newGenServer(t, 200, genResponse([]map[string]any{{"type": "text", "text": "x"}}, "end_turn"))
			c := newTestClaudeClient(srv.URL)
			res, err := c.Generate(context.Background(), GenerateRequest{
				User:  "hi",
				Tools: []ToolDef{{Name: "badtool", InputSchema: json.RawMessage(tc.schema)}},
			})
			if err == nil {
				t.Fatalf("expected error for schema %s", tc.schema)
			}
			var retry *Retryable
			if errors.As(err, &retry) {
				t.Errorf("error must not be *Retryable: %v", err)
			}
			if res.ErrorClass != "invalid_request" {
				t.Errorf("ErrorClass = %q, want invalid_request", res.ErrorClass)
			}
			if n := capt.count(); n != 0 {
				t.Errorf("mock received %d requests, want 0", n)
			}
		})
	}
}

// [F1123-53]
func TestClaudeProvider_Generate_RequestDefaults(t *testing.T) {
	srv, capt := newGenServer(t, 200, genResponse([]map[string]any{{"type": "text", "text": "x"}}, "end_turn"))
	c := newTestClaudeClient(srv.URL)
	if _, err := c.Generate(context.Background(), GenerateRequest{User: "hi"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	body := capt.last(t)
	if body["max_tokens"] != float64(1024) {
		t.Errorf("max_tokens = %v, want 1024", body["max_tokens"])
	}
	if _, ok := body["temperature"]; ok {
		t.Errorf("temperature must not be sent when 0")
	}
	if _, ok := body["system"]; ok {
		t.Errorf("system must not be sent when empty")
	}
}

// [F1123-53]
func TestClaudeProvider_Generate_RequestMapping(t *testing.T) {
	srv, capt := newGenServer(t, 200, genResponse([]map[string]any{{"type": "text", "text": "x"}}, "end_turn"))
	c := newTestClaudeClient(srv.URL)
	_, err := c.Generate(context.Background(), GenerateRequest{
		System: "sys", User: "hi", MaxTokens: 77, Temperature: 0.5,
		Tools: []ToolDef{{Name: "e"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	body := capt.last(t)
	if body["max_tokens"] != float64(77) {
		t.Errorf("max_tokens = %v, want 77", body["max_tokens"])
	}
	if body["temperature"] != 0.5 {
		t.Errorf("temperature = %v, want 0.5", body["temperature"])
	}
	sys := bodyArray(t, body, "system")
	if len(sys) != 1 {
		t.Fatalf("system len = %d, want 1", len(sys))
	}
	if blk, _ := sys[0].(map[string]any); blk["text"] != "sys" {
		t.Errorf("system[0].text = %v", blk["text"])
	}
	tools := bodyArray(t, body, "tools")
	tool, _ := tools[0].(map[string]any)
	is, _ := tool["input_schema"].(map[string]any)
	if !reflect.DeepEqual(is, map[string]any{"type": "object"}) {
		t.Errorf("input_schema = %v, want only type=object", is)
	}
}
