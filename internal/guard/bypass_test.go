package guard

import (
	"context"
	"testing"
)

// TestResolveBypass_NilStore verifies fail-open: nil store returns nil bypass.
func TestResolveBypass_NilStore(t *testing.T) {
	t.Parallel()
	b := ResolveBypass(context.Background(), nil, "/repo", "", "Bash")
	if b != nil {
		t.Errorf("ResolveBypass(nil store) = %v, want nil", b)
	}
}

// TestResolveBypass_NilPool verifies fail-open: store with nil pool returns nil bypass.
func TestResolveBypass_NilPool(t *testing.T) {
	t.Parallel()
	store := NewStore(nil)
	b := ResolveBypass(context.Background(), store, "/repo", "", "Bash")
	if b != nil {
		t.Errorf("ResolveBypass(nil pool store) = %v, want nil", b)
	}
}

// TestIsWhitespacesOnly verifies the helper function.
func TestIsWhitespacesOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  bool
	}{
		{"", true},
		{"   ", true},
		{"\t\n", true},
		{"hello", false},
		{"  hello  ", false},
		{"a", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := IsWhitespacesOnly(tc.input)
			if got != tc.want {
				t.Errorf("IsWhitespacesOnly(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
