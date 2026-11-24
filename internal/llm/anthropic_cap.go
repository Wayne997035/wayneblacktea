package llm

import "github.com/anthropics/anthropic-sdk-go/option"

// AnthropicResponseCap returns the option that bounds every Anthropic SDK
// response body to 1 MiB (claudeMaxBody), the same cap ClaudeClient uses.
//
// Any code OUTSIDE internal/llm that builds an Anthropic SDK client directly
// (anthropic.NewClient) MUST pass this option, otherwise an oversized upstream
// response (or a hostile ANTHROPIC_BASE_URL proxy) is read fully into memory.
// [F1124-70]
func AnthropicResponseCap() option.RequestOption {
	return option.WithMiddleware(capResponseBody)
}
