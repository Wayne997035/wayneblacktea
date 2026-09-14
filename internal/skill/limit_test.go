package skill

import "testing"

// [GTD b0c90957] ClampListLimit is the single source of truth four call sites
// now share — both MCP handlers and both backends' Search/ListRelevant. Before
// it, each of the four floored limit at 10 and none capped it, so a caller's
// `limit: 1000000` reached SQL as the LIMIT verbatim.
//
// The ceiling cases are the ones that matter: the floor was already correct in
// all four copies, which is exactly why nobody noticed the ceiling was missing.
func TestClampListLimit(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in   int
		want int
	}{
		"unset resolves to the default, not to no cap": {0, DefaultListLimit},
		"negative resolves to the default":             {-1, DefaultListLimit},
		"large negative resolves to the default":       {-1 << 30, DefaultListLimit},
		"one passes through":                           {1, 1},
		"default passes through":                       {DefaultListLimit, DefaultListLimit},
		"one below the cap passes through":             {MaxListLimit - 1, MaxListLimit - 1},
		"exactly the cap passes through":               {MaxListLimit, MaxListLimit},
		"one over the cap is capped":                   {MaxListLimit + 1, MaxListLimit},
		"the reported attack value is capped":          {1000000, MaxListLimit},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := ClampListLimit(tc.in); got != tc.want {
				t.Errorf("ClampListLimit(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestClampListLimit_NeverReturnsOutOfRange is the property the table above
// only samples. A future edit that reorders the two branches — floor after
// ceiling — still passes every case above, and this one catches it.
func TestClampListLimit_NeverReturnsOutOfRange(t *testing.T) {
	t.Parallel()

	for in := -10; in <= MaxListLimit+10; in++ {
		got := ClampListLimit(in)
		if got < 1 || got > MaxListLimit {
			t.Fatalf("ClampListLimit(%d) = %d, outside [1, %d]", in, got, MaxListLimit)
		}
	}
}
