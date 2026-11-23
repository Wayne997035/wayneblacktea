package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	// defaultClaudeModel matches the existing internal/ai default
	// (claude-haiku-4-5) so the chain has parity with the legacy
	// classifier/reviewer/analyzer call sites.
	defaultClaudeModel = "claude-haiku-4-5"
	claudeTimeout      = 30 * time.Second

	// claudeProviderName is the stable provider label returned by Name().
	claudeProviderName = "claude"
)

// ClaudeClient is a JSONClient that wraps the Anthropic SDK's Messages API.
// We do NOT duplicate model defaults from internal/ai — feature packages
// (ActivityClassifier, ConceptReviewer) keep their own MaxTokens / model
// constants and pass them through JSONRequest.
type ClaudeClient struct {
	client *anthropic.Client
	model  string
}

// ClaudeConfig holds construction inputs read from env. Empty Model defaults
// to defaultClaudeModel.
type ClaudeConfig struct {
	APIKey string
	Model  string
}

// NewClaudeClient builds a ClaudeClient. Returns (nil, nil) when APIKey is
// empty so the caller can `if c == nil { skip }`.
func NewClaudeClient(cfg ClaudeConfig) (*ClaudeClient, error) {
	if cfg.APIKey == "" {
		return nil, nil //nolint:nilnil // documented "missing key, skip" sentinel
	}
	model := cfg.Model
	if model == "" {
		model = defaultClaudeModel
	}
	c := anthropic.NewClient(option.WithAPIKey(cfg.APIKey))
	return &ClaudeClient{client: &c, model: model}, nil
}

// NewClaudeClientWithSDK is intended for tests with a pre-configured client
// (e.g. option.WithBaseURL pointing at a httptest.Server). It bypasses the
// API-key check.
func NewClaudeClientWithSDK(client *anthropic.Client, model string) *ClaudeClient {
	if model == "" {
		model = defaultClaudeModel
	}
	return &ClaudeClient{client: client, model: model}
}

// Name implements JSONClient.
func (c *ClaudeClient) Name() string { return claudeProviderName }

// Model implements modelNamer (health.go) so the startup log and the health
// surface can report WHICH model this provider talks to, not just that a
// provider named "claude" exists. [GTD ab472814]
func (c *ClaudeClient) Model() string { return c.model }

// CompleteJSON sends a single Messages.New call and returns the first text
// block. Errors are wrapped in *Retryable so the chain falls through.
func (c *ClaudeClient) CompleteJSON(ctx context.Context, req JSONRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, claudeTimeout)
	defer cancel()

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	resp, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: int64(maxTokens),
		System: []anthropic.TextBlockParam{
			{Text: req.System},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(req.User)),
		},
	})
	if err != nil {
		// Use Anthropic SDK's own error-classification surface. Most
		// transport / 5xx errors come back as plain errors; we tag the
		// reason heuristically based on the error string and treat all of
		// them as retryable so the chain falls through.
		return "", &Retryable{Provider: c.Name(), Reason: classifyClaudeErr(err), Err: sanitiseTransportErr(err)}
	}
	if len(resp.Content) == 0 {
		return "", &Retryable{Provider: c.Name(), Reason: reasonEmptyContent, Err: errors.New("no content blocks")}
	}
	text := resp.Content[0].Text
	if text == "" {
		return "", &Retryable{Provider: c.Name(), Reason: reasonEmptyContent, Err: errors.New("empty text block")}
	}
	return text, nil
}

// classifyClaudeErr maps an Anthropic SDK error to a chain reason label.
// Uses the SDK's typed *anthropic.Error (which exposes StatusCode) so that
// the classification is not confused by port numbers in the request URL that
// the SDK includes in its Error() string.
func classifyClaudeErr(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return reasonTimeout
	}
	if errors.Is(err, context.Canceled) {
		return reasonCancelled
	}
	type timeoutErr interface{ Timeout() bool }
	var te timeoutErr
	if errors.As(err, &te) && te.Timeout() {
		return reasonTimeout
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 429:
			return reasonHTTP429
		case apiErr.StatusCode >= 500:
			return reasonHTTP5xx
		}
		return reasonProviderErr
	}
	return reasonProviderErr
}

// Compile-time assertion that ClaudeClient satisfies JSONClient.
var _ JSONClient = (*ClaudeClient)(nil)

// Compile-time assertion that ClaudeClient satisfies GenerateClient. [F1123-50]
var _ GenerateClient = (*ClaudeClient)(nil)

// claudeMaxContext is the context window Capabilities reports.
// 未驗證:保守值,僅供 5b 路由做上限比較 (unverified: conservative value, used
// only by the 5b router for an upper-bound comparison; no request is truncated).
const claudeMaxContext = 200_000

// Capabilities implements GenerateClient. [F1123-50]
//
// ImageInput is a provider capability. 送圖路徑由 slice 11 加入,在那之前
// Generate 只送文字: the 5b router MUST NOT route image-input tasks to
// Generate before slice 11.
func (c *ClaudeClient) Capabilities() Capabilities {
	return Capabilities{
		Text:        true,
		ImageInput:  true,
		ToolCalling: true,
		MaxContext:  claudeMaxContext,
	}
}

// Generate implements GenerateClient. [F1123-50]
//
// Request mapping [F1123-53]: MaxTokens <= 0 becomes 1024. Temperature > 0 is
// sent verbatim; 0 is NOT sent (SDK default), so an explicit 0 cannot be
// expressed. An empty System sends no system field.
//
// Security: same rules as client.go package doc — API key only in header (SDK),
// per-call timeout, prompt / response never logged. Tool calls are parsed, never
// executed.
func (c *ClaudeClient) Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	res := GenerateResult{Provider: c.Name(), Model: c.model}

	// [F1123-52] Validate tools before any request is sent. A bad schema is the
	// caller's fault, so it is NOT *Retryable (no provider fallthrough).
	tools, err := toClaudeTools(req.Tools)
	if err != nil {
		res.ErrorClass = reasonInvalidRequest
		return res, err
	}

	ctx, cancel := context.WithTimeout(ctx, claudeTimeout)
	defer cancel()

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024 // [F1123-53]
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: int64(maxTokens),
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(req.User)),
		},
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}
	if req.Temperature > 0 {
		params.Temperature = anthropic.Float(req.Temperature)
	}
	if len(tools) > 0 {
		params.Tools = tools
	}

	start := time.Now()
	resp, err := c.client.Messages.New(ctx, params)
	res.Latency = time.Since(start) // [F1123-51]
	if err != nil {
		res.ErrorClass = classifyClaudeErr(err)
		return res, &Retryable{Provider: c.Name(), Reason: res.ErrorClass, Err: sanitiseTransportErr(err)}
	}
	if len(resp.Content) == 0 {
		res.ErrorClass = reasonEmptyContent
		return res, &Retryable{Provider: c.Name(), Reason: reasonEmptyContent, Err: errors.New("no content blocks")}
	}

	var text strings.Builder
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			res.ToolCalls = append(res.ToolCalls, ToolCallResult{ID: block.ID, Name: block.Name, Input: block.Input})
		}
	}
	res.Text = text.String()
	res.StopReason = string(resp.StopReason)
	res.Usage = Usage{InputTokens: int(resp.Usage.InputTokens), OutputTokens: int(resp.Usage.OutputTokens)}
	return res, nil
}

// toClaudeTools converts ToolDefs to SDK params. Any invalid schema fails the
// whole call with an error naming the tool. [F1123-52]
func toClaudeTools(defs []ToolDef) ([]anthropic.ToolUnionParam, error) {
	if len(defs) == 0 {
		return nil, nil
	}
	out := make([]anthropic.ToolUnionParam, 0, len(defs))
	for _, d := range defs {
		schema, err := toClaudeSchema(d.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("llm: invalid input schema for tool %q: %w", d.Name, err)
		}
		tp := anthropic.ToolParam{Name: d.Name, InputSchema: schema}
		if d.Description != "" {
			tp.Description = anthropic.String(d.Description)
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &tp})
	}
	return out, nil
}

// toClaudeSchema maps a JSON Schema object onto the SDK's ToolInputSchemaParam.
func toClaudeSchema(raw json.RawMessage) (anthropic.ToolInputSchemaParam, error) {
	var schema anthropic.ToolInputSchemaParam
	// The zero Type marshals as an absent key; set the SDK constant explicitly
	// so every tool carries "type":"object".
	schema.Type = schema.Type.Default()
	if len(raw) == 0 {
		return schema, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return schema, errors.New("not valid JSON")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return schema, errors.New("schema must be a JSON object")
	}
	for key, val := range obj {
		switch key {
		case "type":
			if s, isStr := val.(string); !isStr || s != "object" {
				return schema, errors.New(`"type" must be "object"`)
			}
		case "properties":
			schema.Properties = val
		case "required":
			arr, isArr := val.([]any)
			if !isArr {
				return schema, errors.New(`"required" must be an array of strings`)
			}
			req := make([]string, 0, len(arr))
			for _, e := range arr {
				s, isStr := e.(string)
				if !isStr {
					return schema, errors.New(`"required" must be an array of strings`)
				}
				req = append(req, s)
			}
			schema.Required = req
		default:
			if schema.ExtraFields == nil {
				schema.ExtraFields = map[string]any{}
			}
			schema.ExtraFields[key] = val
		}
	}
	return schema, nil
}
