package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// newCappedClaudeClient builds a client through the real production
// construction path (newClaudeClientOpts), pointed at srvURL. [F1124-60]
func newCappedClaudeClient(srvURL string) *ClaudeClient {
	return newClaudeClientOpts(ClaudeConfig{APIKey: "test-key"}, "claude-haiku-4-5", option.WithBaseURL(srvURL))
}

func textResponseOfSize(n int) string {
	return genResponse([]map[string]any{{"type": "text", "text": strings.Repeat("a", n)}}, "end_turn")
}

// [F1124-60]
func TestClaudeClient_Generate_RejectsOversizedResponse(t *testing.T) {
	srv, _ := newGenServer(t, 200, textResponseOfSize(2<<20))
	c := newCappedClaudeClient(srv.URL)
	res, err := c.Generate(context.Background(), GenerateRequest{User: "hi"})
	var retry *Retryable
	if !errors.As(err, &retry) {
		t.Fatalf("err = %v (%T), want *Retryable", err, err)
	}
	if res.ErrorClass == "" || res.ErrorClass != retry.Reason {
		t.Errorf("ErrorClass = %q, Reason = %q; want equal and non-empty", res.ErrorClass, retry.Reason)
	}
}

// [F1124-60]
func TestClaudeClient_CompleteJSON_RejectsOversizedResponse(t *testing.T) {
	srv, _ := newGenServer(t, 200, textResponseOfSize(2<<20))
	c := newCappedClaudeClient(srv.URL)
	_, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t", User: "hi"})
	var retry *Retryable
	if !errors.As(err, &retry) {
		t.Fatalf("err = %v (%T), want *Retryable", err, err)
	}
}

// Positive control: a ~100 KiB body must pass, proving the cap is not too small.
// [F1124-60]
func TestClaudeClient_Generate_AcceptsResponseUnderCap(t *testing.T) {
	const size = 100 << 10
	srv, _ := newGenServer(t, 200, textResponseOfSize(size))
	c := newCappedClaudeClient(srv.URL)
	res, err := c.Generate(context.Background(), GenerateRequest{User: "hi"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(res.Text) != size {
		t.Errorf("len(Text) = %d, want %d", len(res.Text), size)
	}
}

// The public constructor must install the cap (SDK reads ANTHROPIC_BASE_URL).
// [F1124-60]
func TestNewClaudeClient_AppliesResponseCap(t *testing.T) {
	srv, _ := newGenServer(t, 200, textResponseOfSize(2<<20))
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	c, err := NewClaudeClient(ClaudeConfig{APIKey: "k"})
	if err != nil || c == nil {
		t.Fatalf("NewClaudeClient = %v, %v", c, err)
	}
	var retry *Retryable
	if _, err := c.Generate(context.Background(), GenerateRequest{User: "hi"}); !errors.As(err, &retry) {
		t.Errorf("Generate err = %v (%T), want *Retryable", err, err)
	}
	retry = nil
	if _, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t", User: "hi"}); !errors.As(err, &retry) {
		t.Errorf("CompleteJSON err = %v (%T), want *Retryable", err, err)
	}
}

// secretBaseURL returns srvURL with a proxy password embedded as userinfo.
func secretBaseURL(t *testing.T, srvURL string) string {
	t.Helper()
	u, err := url.Parse(srvURL)
	if err != nil {
		t.Fatalf("parse %q: %v", srvURL, err)
	}
	u.User = url.UserPassword("proxyuser", "s3cret")
	return u.String()
}

func assertNoLeak(t *testing.T, err error) {
	t.Helper()
	for _, bad := range []string{"SECRETBODY", "s3cret"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("error leaks %q: %v", bad, err)
		}
	}
	if !strings.Contains(err.Error(), "status=500") {
		t.Errorf("error lost status: %v", err)
	}
}

const upstreamErrBody = `{"type":"error","error":{"type":"api_error","message":"SECRETBODY"}}`

// [F1124-61]
func TestClaudeClient_Generate_ErrorOmitsBodyAndBaseURLPassword(t *testing.T) {
	srv, _ := newGenServer(t, 500, upstreamErrBody)
	c := newCappedClaudeClient(secretBaseURL(t, srv.URL))
	res, err := c.Generate(context.Background(), GenerateRequest{User: "hi"})
	var retry *Retryable
	if !errors.As(err, &retry) {
		t.Fatalf("err = %v (%T), want *Retryable", err, err)
	}
	assertNoLeak(t, err)
	if retry.Reason != reasonHTTP5xx || res.ErrorClass != reasonHTTP5xx {
		t.Errorf("Reason = %q, ErrorClass = %q, want %q (classification must use original err)", retry.Reason, res.ErrorClass, reasonHTTP5xx)
	}
}

// [F1124-61]
func TestClaudeClient_CompleteJSON_ErrorOmitsBodyAndBaseURLPassword(t *testing.T) {
	srv, _ := newGenServer(t, 500, upstreamErrBody)
	c := newCappedClaudeClient(secretBaseURL(t, srv.URL))
	_, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t", User: "hi"})
	var retry *Retryable
	if !errors.As(err, &retry) {
		t.Fatalf("err = %v (%T), want *Retryable", err, err)
	}
	assertNoLeak(t, err)
	if retry.Reason != reasonHTTP5xx {
		t.Errorf("Reason = %q, want %q", retry.Reason, reasonHTTP5xx)
	}
}

// [F1124-61]
func TestSanitiseClaudeErr_KeepsStatusAndRequestOnly(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://proxyuser:s3cret@host.invalid/v1/messages", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	api := &anthropic.Error{
		StatusCode: 429,
		RequestID:  "req_abc",
		Request:    req,
		Response:   &http.Response{StatusCode: 429},
	}
	cases := []struct {
		name     string
		in       error
		want     string // exact message; "" = only check forbidden substrings
		contains string
	}{
		{"api_error", api, "anthropic API error: status=429 request_id=req_abc", ""},
		{"wrapped_api_error", fmt.Errorf("wrap: %w", api), "anthropic API error: status=429 request_id=req_abc", ""},
		{"transport_bearer_redacted", errors.New("dial failed Authorization: Bearer sk-ant-XYZ"), "", "dial failed"},
		{"plain_error_passthrough", errors.New("connection refused"), "connection refused", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitiseClaudeErr(tc.in)
			if got == nil {
				t.Fatal("got nil error")
			}
			msg := got.Error()
			if tc.want != "" && msg != tc.want {
				t.Errorf("msg = %q, want %q", msg, tc.want)
			}
			if tc.contains != "" && !strings.Contains(msg, tc.contains) {
				t.Errorf("msg = %q, want substring %q", msg, tc.contains)
			}
			for _, bad := range []string{"s3cret", "sk-ant-XYZ", "host.invalid"} {
				if strings.Contains(msg, bad) {
					t.Errorf("msg leaks %q: %q", bad, msg)
				}
			}
		})
	}
	if sanitiseClaudeErr(nil) != nil {
		t.Errorf("nil must stay nil")
	}
}

// [F1124-62]
func TestClaudeProvider_Generate_EmptyTextWithoutToolUseIsEmptyContentError(t *testing.T) {
	cases := []struct {
		name    string
		content []map[string]any
	}{
		{"empty_text_block", []map[string]any{{"type": "text", "text": ""}}},
		{"thinking_only", []map[string]any{{"type": "thinking", "thinking": "hmm", "signature": "sig"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newGenServer(t, 200, genResponse(tc.content, "end_turn"))
			c := newTestClaudeClient(srv.URL)
			res, err := c.Generate(context.Background(), GenerateRequest{User: "hi"})
			var retry *Retryable
			if !errors.As(err, &retry) {
				t.Fatalf("err = %v (%T), want *Retryable", err, err)
			}
			if retry.Reason != reasonEmptyContent || res.ErrorClass != reasonEmptyContent {
				t.Errorf("Reason = %q, ErrorClass = %q, want %q", retry.Reason, res.ErrorClass, reasonEmptyContent)
			}
			if res.Provider != claudeProviderName || res.Model == "" || res.Latency <= 0 {
				t.Errorf("Provider/Model/Latency not filled: %+v", res)
			}
		})
	}
}

// [F1124-63] Capabilities must describe what Generate really sends: no image
// block is ever produced, so ImageInput must be false.
func TestClaudeProvider_Capabilities_ImageInputMatchesGeneratePath(t *testing.T) {
	srv, capt := newGenServer(t, 200, genResponse([]map[string]any{{"type": "text", "text": "x"}}, "end_turn"))
	c := newTestClaudeClient(srv.URL)
	if _, err := c.Generate(context.Background(), GenerateRequest{User: "hi"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	sentImage := false
	for _, m := range bodyArray(t, capt.last(t), "messages") {
		msg, _ := m.(map[string]any)
		blocks, _ := msg["content"].([]any)
		for _, b := range blocks {
			if blk, _ := b.(map[string]any); blk["type"] == "image" {
				sentImage = true
			}
		}
	}
	if got := c.Capabilities().ImageInput; got != sentImage {
		t.Errorf("Capabilities().ImageInput = %v but Generate sends image = %v", got, sentImage)
	}
	if c.Capabilities().ImageInput {
		t.Errorf("ImageInput must be false until slice 11 adds an image path")
	}
}
