package validator

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// productionRepoNames is every workspace repo name that exists in production
// today. A rule that rejects any of them would lock those repos out of the
// write path, so they are pinned here as accept cases.
var productionRepoNames = []string{
	"chatbot",
	"chatbot-go",
	"chat-gateway",
	"chat-web",
	"cmoney-backend",
	"demo",
	"example-app-go-pgx",
	"Flare-Go/auth",
	"food-photo-log",
	"go_web",
	"intelligence-flow",
	"kdc-p2p-report/kdc-p2p-server-status-check",
	"neomart-api",
	"skcloud-admin-portal/skcloud-advertisement-image",
	"skcloud-uid-udid-server/skcloud-uid-udid",
	"wayneblacktea",
}

// rejectedRepoPaths must fail ValidRepoPath. Each entry names the property
// that disqualifies it.
var rejectedRepoPaths = map[string]string{
	"":                 "empty",
	"../etc/passwd":    "dot-dot segment",
	"a/../b":           "dot-dot segment in the middle",
	"./a":              "dot segment",
	".x":               "segment starts with a dot",
	"_project/.claude": "second segment starts with a dot",
	"-x":               "segment starts with a dash",
	"a/-b":             "second segment starts with a dash",
	"--help":           "looks like a command-line flag",
	"a//b":             "empty segment",
	"/a":               "leading slash",
	"a/":               "trailing slash",
	"a b":              "space",
	"a;b":              "shell separator",
	"a\nb":             "newline",
	"repo\n":           "trailing newline",
	"repo\t":           "tab",
	"repo\x00":         "null byte",
	"repo$(cmd)":       "command substitution",
	"repo`cmd`":        "backticks",
	"中文":               "non-ASCII",
	"bad repo name!":   "space and bang",
}

// [F0925-29] ValidRepoPath is the single workspace repo name rule.
func TestValidRepoPath(t *testing.T) {
	t.Parallel()

	accept := append([]string{
		"_project",
		"owner/repo",
		"a.b/c_d-e",
		"a",
		"A1",
		"1234567890",
		"my_repo.name",
		"trailing.dot.",
	}, productionRepoNames...)
	for _, s := range accept {
		t.Run("accept/"+s, func(t *testing.T) {
			t.Parallel()
			if !ValidRepoPath(s) {
				t.Errorf("ValidRepoPath(%q) = false, want true", s)
			}
		})
	}

	for s, why := range rejectedRepoPaths {
		t.Run("reject/"+why, func(t *testing.T) {
			t.Parallel()
			if ValidRepoPath(s) {
				t.Errorf("ValidRepoPath(%q) = true, want false (%s)", s, why)
			}
		})
	}
}

// [F0925-29] The 100-character limit counts the whole path, slashes included.
func TestValidRepoPath_LengthBoundary(t *testing.T) {
	t.Parallel()

	if !ValidRepoPath(strings.Repeat("a", 100)) {
		t.Error("100-char name should be valid (boundary)")
	}
	if ValidRepoPath(strings.Repeat("a", 101)) {
		t.Error("101-char name should be rejected (over boundary)")
	}
	withSlash := strings.Repeat("a", 49) + "/" + strings.Repeat("b", 50)
	if !ValidRepoPath(withSlash) {
		t.Errorf("100-char path with a slash should be valid, len=%d", len(withSlash))
	}
	if ValidRepoPath(withSlash + "c") {
		t.Error("101-char path with a slash should be rejected")
	}
}

// [F0925-29] Callers with their own length budget share the segment rule.
func TestValidRepoPathMax(t *testing.T) {
	t.Parallel()

	name := strings.Repeat("a", 120)
	if !ValidRepoPathMax(name, 128) {
		t.Error("120 chars should pass a 128 limit")
	}
	if ValidRepoPathMax(name, 64) {
		t.Error("120 chars should fail a 64 limit")
	}
	if ValidRepoPathMax("-x", 128) {
		t.Error("the segment rule must still apply under a larger limit")
	}
	if ValidRepoPathMax("a", 0) {
		t.Error("a non-positive limit must reject everything")
	}
}

// TestIsValidRepoName covers optional repo_name columns: empty means "not
// set" and is accepted; any non-empty value must satisfy ValidRepoPath.
func TestIsValidRepoName(t *testing.T) {
	t.Parallel()

	accept := append([]string{"", "wayneblacktea", "owner/repo", "Flare-Go/auth"}, productionRepoNames...)
	for _, s := range accept {
		t.Run("accept/"+s, func(t *testing.T) {
			t.Parallel()
			if !IsValidRepoName(s) {
				t.Errorf("IsValidRepoName(%q) = false, want true", s)
			}
		})
	}

	i := 0
	for s, why := range rejectedRepoPaths {
		if s == "" {
			continue
		}
		i++
		t.Run(fmt.Sprintf("reject/%d/%s", i, why), func(t *testing.T) {
			t.Parallel()
			if IsValidRepoName(s) {
				t.Errorf("IsValidRepoName(%q) = true, want false (%s)", s, why)
			}
		})
	}
}

// [F0925-29] Automatic writers store an empty value instead of an invalid one.
func TestRepoNameOrEmpty(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"wayneblacktea":    "wayneblacktea",
		"Flare-Go/auth":    "Flare-Go/auth",
		"":                 "",
		".claude":          "",
		"_project/.claude": "",
		"../x":             "",
		"中文":               "",
	}
	for in, want := range cases {
		if got := RepoNameOrEmpty(in); got != want {
			t.Errorf("RepoNameOrEmpty(%q) = %q, want %q", in, got, want)
		}
	}
}

// [F0925-29] The sentinel message states the rule so callers can fix their input.
func TestErrInvalidRepoName_NamesTheRule(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("creating project: %w", ErrInvalidRepoName)
	if !errors.Is(wrapped, ErrInvalidRepoName) {
		t.Fatal("wrapped error must still match ErrInvalidRepoName")
	}
	if !strings.Contains(ErrInvalidRepoName.Error(), RepoNameRule) {
		t.Errorf("error %q should contain the rule %q", ErrInvalidRepoName.Error(), RepoNameRule)
	}
}
