package discordbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AnalysisResult is what the GROQ model returns.
type AnalysisResult struct {
	Summary       string   `json:"summary"`
	KeyConcepts   []string `json:"key_concepts"`
	LearningValue int      `json:"learning_value"` // 1-5
	WorthSaving   bool     `json:"worth_saving"`
	SuggestedType string   `json:"suggested_type"` // article|til|zettelkasten|bookmark
	Tags          []string `json:"tags"`
	SkipReason    string   `json:"skip_reason,omitempty"`
}

// Analyzer calls GROQ to evaluate content for learning value.
type Analyzer struct {
	apiKey string
	model  string
	client *http.Client
}

// NewAnalyzer creates an Analyzer backed by GROQ.
func NewAnalyzer(groqAPIKey string) *Analyzer {
	return &Analyzer{
		apiKey: groqAPIKey,
		model:  "llama-3.3-70b-versatile",
		client: &http.Client{Timeout: 45 * time.Second},
	}
}

const analyzePrompt = `You are a technical knowledge curator. ` +
	`Analyze the following content and decide if it is worth saving as a learning note.

Return ONLY a JSON object with this schema (no markdown, no explanation):
{
  "summary": "2-3 sentence summary",
  "key_concepts": ["concept1", "concept2"],
  "learning_value": 4,
  "worth_saving": true,
  "suggested_type": "article",
  "tags": ["tag1", "tag2"],
  "skip_reason": ""
}

Rules:
- learning_value 1-5 (1=noise/marketing, 3=useful, 5=must-save deep insight)
- worth_saving = true if learning_value >= 3
- suggested_type: "article" for long-form, "til" for short facts, ` +
	`"zettelkasten" for ideas/concepts, "bookmark" for tools/refs
- tags: 2-5 lowercase keywords
- skip_reason: brief reason only when worth_saving=false, otherwise ""
- summary must be in the same language as the content

Content:
`

// Analyze sends content to GROQ and returns a structured learning assessment.
func (a *Analyzer) Analyze(ctx context.Context, content string) (*AnalysisResult, error) {
	body, err := json.Marshal(map[string]any{
		"model": a.model,
		"messages": []map[string]string{
			{"role": "user", "content": analyzePrompt + content},
		},
		"temperature": 0.2,
		"max_tokens":  512,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.groq.com/openai/v1/chat/completions",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("groq request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("groq status %d: %s", resp.StatusCode, raw)
	}

	var groqResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &groqResp); err != nil || len(groqResp.Choices) == 0 {
		return nil, fmt.Errorf("parse groq response: %w", err)
	}

	var result AnalysisResult
	if err := json.Unmarshal([]byte(groqResp.Choices[0].Message.Content), &result); err != nil {
		return nil, fmt.Errorf("parse analysis json: %w", err)
	}
	return &result, nil
}
