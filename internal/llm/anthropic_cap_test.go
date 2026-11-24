package llm

import (
	"context"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

func callCappedSDK(t *testing.T, size int) error {
	t.Helper()
	srv, _ := newGenServer(t, 200, textResponseOfSize(size))
	c := anthropic.NewClient(option.WithAPIKey("k"), option.WithBaseURL(srv.URL), AnthropicResponseCap())
	_, err := c.Messages.New(context.Background(), anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5,
		MaxTokens: 16,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("hi"))},
	})
	return err
}

// [F1124-70]
func TestAnthropicResponseCap_RejectsOversizedResponse(t *testing.T) {
	if err := callCappedSDK(t, 2<<20); err == nil {
		t.Fatal("err = <nil>, want error for 2 MiB response")
	}
}

// Positive control: ~100 KiB must pass, proving the cap is not too small. [F1124-70]
func TestAnthropicResponseCap_AcceptsResponseUnderCap(t *testing.T) {
	if err := callCappedSDK(t, 100<<10); err != nil {
		t.Fatalf("err = %v, want nil for 100 KiB response", err)
	}
}
