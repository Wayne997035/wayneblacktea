package llm

import (
	"context"
	"encoding/json"
	"time"
)

// [F1123-50] GenerateClient is the structured, capability-aware sibling of
// JSONClient. It runs on a path parallel to Chain: Generate calls do NOT feed
// Chain.health, so GET /api/health/llm does not see them until the 5b router
// wires them in.

// reasonInvalidRequest labels a caller-side error (e.g. an illegal tool
// schema). It is never wrapped in *Retryable: retrying another provider
// cannot fix the caller's input. [F1123-52]
const reasonInvalidRequest = "invalid_request"

// Capabilities declares what a provider can do.
type Capabilities struct {
	Text        bool
	ImageInput  bool
	AudioInput  bool
	AudioOutput bool
	ToolCalling bool
	Realtime    bool
	MaxContext  int
}

// ToolDef describes one tool the model may call. InputSchema is a JSON Schema
// object; nil or empty means "no parameters".
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ToolCallResult is one tool_use intent returned by the model. It is only
// parsed, never executed, by this package.
type ToolCallResult struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// Usage holds token counts reported by the provider.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// GenerateRequest is the provider-neutral input to Generate. Text only: the
// image-sending path is added by slice 11.
type GenerateRequest struct {
	Task        string
	System      string
	User        string
	MaxTokens   int
	Temperature float64
	Tools       []ToolDef // optional; populated only when the caller needs tool-calling
}

// GenerateResult is the normalised outcome of a Generate call. [F1123-51]
// Provider / Model / Latency are filled on success and failure alike (Latency
// is 0 when no request was sent); ErrorClass is "" on success.
type GenerateResult struct {
	Text       string
	ToolCalls  []ToolCallResult
	StopReason string
	Usage      Usage
	Provider   string
	Model      string
	Latency    time.Duration
	ErrorClass string
}

// GenerateClient is implemented by providers that support structured
// generation.
type GenerateClient interface {
	Name() string
	Capabilities() Capabilities
	Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error)
}
