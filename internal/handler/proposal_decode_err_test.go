package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// TestPublicDecodeErr pins what publicDecodeErr lets into a 400 body [F204-04]:
// a JSON syntax or type error collapses to "<decoder prefix>: malformed JSON"
// (or the fixed fallback when there is no prefix), and every other error — the
// decoders' own validation text — passes through unchanged.
func TestPublicDecodeErr(t *testing.T) {
	var dst struct {
		Title string `json:"title"`
	}
	syntaxErr := json.Unmarshal([]byte(`{bad json`), &dst)
	typeErr := json.Unmarshal([]byte(`{"title":123}`), &dst)

	// The two inputs above must really yield the encoding/json error types the
	// cases below are named after, or a case could pass for the wrong reason.
	var syn *json.SyntaxError
	if !errors.As(syntaxErr, &syn) {
		t.Fatalf("fixture: %v is not a *json.SyntaxError", syntaxErr)
	}
	var typ *json.UnmarshalTypeError
	if !errors.As(typeErr, &typ) {
		t.Fatalf("fixture: %v is not a *json.UnmarshalTypeError", typeErr)
	}

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "wrapped SyntaxError keeps only the decoder prefix",
			err:  fmt.Errorf("decoding task payload: %w", syntaxErr),
			want: "decoding task payload: malformed JSON",
		},
		{
			name: "wrapped UnmarshalTypeError keeps only the decoder prefix",
			err:  fmt.Errorf("decoding decision payload: %w", typeErr),
			want: "decoding decision payload: malformed JSON",
		},
		{
			name: "non-JSON validation error passes through unchanged",
			err:  errors.New("task payload missing title"),
			want: "task payload missing title",
		},
		{
			name: "bare SyntaxError without a prefix gets the fixed fallback",
			err:  syntaxErr,
			want: "proposal payload is malformed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := publicDecodeErr(tc.err); got != tc.want {
				t.Errorf("publicDecodeErr(%q) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
