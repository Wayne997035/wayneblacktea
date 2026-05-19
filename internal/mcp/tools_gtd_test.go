package mcp

import "testing"

// TestStrictVagueness_ParseBool verifies that (*Server).strictVagueness reads
// WBT_STRICT_VAGUENESS through strconv.ParseBool, accepting the canonical
// truthy/falsy set ("1", "t", "true", "True", "TRUE", "T" → true; "0",
// "false", "f", empty, anything not in the truthy set → false).
//
// Round-1 follow-up GTD 6cda1ce2: tightens the previous exact-"true" match
// so operators don't get caught out by `WBT_STRICT_VAGUENESS=1` silently
// running in warn mode.
func TestStrictVagueness_ParseBool(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want bool
	}{
		{"truthy_1", "1", true},
		{"truthy_lower_t", "t", true},
		{"truthy_upper_T", "T", true},
		{"truthy_true", "true", true},
		{"truthy_True", "True", true},
		{"truthy_TRUE", "TRUE", true},
		{"falsy_0", "0", false},
		{"falsy_false", "false", false},
		{"falsy_empty", "", false},
		{"invalid_garbage_falsy", "yes", false},
		{"invalid_two_falsy", "2", false},
	}
	s := &Server{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WBT_STRICT_VAGUENESS", tc.env)
			if got := s.strictVagueness(); got != tc.want {
				t.Errorf("strictVagueness() with env=%q = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}
