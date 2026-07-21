package main

import (
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/evals"
)

// TestValidateFlags exercises the exhaustive client-side flag allowlist
// (backend-security-design.md §5.2): every rejection path must fail closed
// with a message naming the offending flag, and every legitimate combination
// must pass. Categories are drawn from evals.ProviderEvalCategories (the real
// registry) rather than hardcoded so this test tracks the source of truth.
func TestValidateFlags(t *testing.T) {
	// Pick a genuine category from the registry for the happy path; if the
	// registry is ever emptied this guards against a vacuous "all"-only test.
	var oneCategory string
	if len(evals.ProviderEvalCategories) > 0 {
		oneCategory = evals.ProviderEvalCategories[0]
	} else {
		oneCategory = evals.CategoryAll
	}

	tests := []struct {
		name              string
		provider          string
		model             string
		category          string
		wantErr           bool
		wantErrSubstrings []string // all must appear in the error
	}{
		{
			name:     "all valid defaults",
			provider: "claude", model: "claude-haiku-4-5", category: evals.CategoryAll,
			wantErr: false,
		},
		{
			name:     "valid specific category",
			provider: "claude", model: "claude-haiku-4-5", category: oneCategory,
			wantErr: false,
		},
		{
			name:     "unsupported provider rejected with flag name",
			provider: "openai", model: "gpt-4", category: evals.CategoryAll,
			wantErr: true, wantErrSubstrings: []string{"--provider", "openai"},
		},
		{
			name:     "empty model rejected",
			provider: "claude", model: "", category: evals.CategoryAll,
			wantErr: true, wantErrSubstrings: []string{"--model"},
		},
		{
			name:     "whitespace-only model rejected",
			provider: "claude", model: "   ", category: evals.CategoryAll,
			wantErr: true, wantErrSubstrings: []string{"--model"},
		},
		{
			name:     "unknown category rejected with flag name",
			provider: "claude", model: "claude-haiku-4-5", category: "not-a-category",
			wantErr: true, wantErrSubstrings: []string{"--category", "not-a-category"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFlags(tc.provider, tc.model, tc.category)
			if tc.wantErr && err == nil {
				t.Fatalf("validateFlags(%q,%q,%q) = nil, want error",
					tc.provider, tc.model, tc.category)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateFlags(%q,%q,%q) = %v, want nil",
					tc.provider, tc.model, tc.category, err)
			}
			if err != nil {
				for _, want := range tc.wantErrSubstrings {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q missing substring %q", err.Error(), want)
					}
				}
			}
		})
	}
}
