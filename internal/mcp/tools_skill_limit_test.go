//go:build !integration

// Tagged to match the files owning the helpers used below — stubSkillStore,
// newSkillServer and callSearchSkills in tools_skill_test.go, and
// callListRelevantSkills in u13_phase_b_b3_test.go — which all carry
// !integration. Without the tag this file compiles under `-tags integration`
// while those helpers do not exist, and `go test` with no tags stays green the
// whole time.

package mcp

import (
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/mark3labs/mcp-go/server"
)

// [GTD b0c90957] These assert on what reaches the STORE, not on what comes
// back. mcp.Max on the schema is a hint the client may ignore, and a test that
// only counted returned rows would pass against a stub no matter what limit it
// was handed — the value that becomes the SQL LIMIT is the thing under test.

func TestB0C90957_SearchSkillsCapsTheLimitReachingSQL(t *testing.T) {
	store := &stubSkillStore{}
	s := newSkillServer(store)

	res := callSearchSkills(t, s, map[string]any{
		"query": "anything",
		"limit": float64(1000000),
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	if got := store.lastSearchFilter.Limit; got != skill.MaxListLimit {
		t.Errorf("store received Limit = %d, want it capped at %d", got, skill.MaxListLimit)
	}
}

func TestB0C90957_ListRelevantSkillsCapsTheLimitReachingSQL(t *testing.T) {
	store := &stubSkillStore{}
	s := newSkillServer(store)

	res := callListRelevantSkills(t, s, map[string]any{"limit": float64(1000000)})
	if res.IsError {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	if got := store.lastListRelevantLimit; got != skill.MaxListLimit {
		t.Errorf("store received limit = %d, want it capped at %d", got, skill.MaxListLimit)
	}
}

// The floor has to keep working. Capping is a one-line change away from also
// resolving "unset" to the cap, which would quietly turn every default call
// into a 100-row response.
func TestB0C90957_UnsetLimitStillMeansTheDefault(t *testing.T) {
	store := &stubSkillStore{}
	s := newSkillServer(store)

	if res := callSearchSkills(t, s, map[string]any{"query": "anything"}); res.IsError {
		t.Fatalf("search_skills: %s", resultText(res))
	}
	if got := store.lastSearchFilter.Limit; got != skill.DefaultListLimit {
		t.Errorf("search_skills with no limit sent %d, want the default %d", got, skill.DefaultListLimit)
	}

	if res := callListRelevantSkills(t, s, nil); res.IsError {
		t.Fatalf("list_relevant_skills: %s", resultText(res))
	}
	if got := store.lastListRelevantLimit; got != skill.DefaultListLimit {
		t.Errorf("list_relevant_skills with no limit sent %d, want the default %d",
			got, skill.DefaultListLimit)
	}
}

// TestB0C90957_LimitSchemaMatchesTheConstants pins the half that is prose plus
// the half a client actually validates against. The description is what an
// agent reads before choosing a limit; if it keeps promising "default 10" after
// the constants move, the tool lies to every caller and nothing else notices.
func TestB0C90957_LimitSchemaMatchesTheConstants(t *testing.T) {
	ms := server.NewMCPServer("test", "0.0.0")
	(&Server{}).registerSkillTools(ms)
	tools := ms.ListTools()

	for _, name := range []string{"search_skills", "list_relevant_skills"} {
		entry, ok := tools[name]
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		prop, ok := entry.Tool.InputSchema.Properties["limit"].(map[string]any)
		if !ok {
			t.Fatalf("%s: limit property missing or not a schema object: %v",
				name, entry.Tool.InputSchema.Properties["limit"])
		}

		desc, _ := prop["description"].(string)
		for _, want := range []string{
			"default " + itoa(skill.DefaultListLimit),
			"max " + itoa(skill.MaxListLimit),
		} {
			if !strings.Contains(desc, want) {
				t.Errorf("%s limit description %q does not say %q", name, desc, want)
			}
		}

		max, ok := prop["maximum"].(float64)
		if !ok || int(max) != skill.MaxListLimit {
			t.Errorf("%s limit schema maximum = %v, want %d",
				name, prop["maximum"], skill.MaxListLimit)
		}
	}
}
