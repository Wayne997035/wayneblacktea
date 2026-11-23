package handler_test

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Wayne997035/wayneblacktea/internal/validator"
)

const (
	reconcileWorkflowPath = "../../.github/workflows/gtd-reconcile.yml"
	repoNameEnvLine       = "REPO_NAME: ${{ github.repository }}"
	scriptStartLine       = "set -euo pipefail\n"
)

// reconcileWorkflow is the slice of a GitHub Actions workflow the check reads.
type reconcileWorkflow struct {
	Jobs map[string]struct {
		Steps []struct {
			Env map[string]string `yaml:"env"`
			Run string            `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

var (
	repoBindingRe = regexp.MustCompile(`--(arg|argjson|slurpfile|rawfile)\s+repo\s+(\S+)`)
	repoFieldRe   = regexp.MustCompile(`\brepo:\s*([^,}\n]+)`)
	repoNameRe    = regexp.MustCompile(`REPO_NAME`)
)

func isIdentByte(b byte) bool {
	return b == '_' || ('0' <= b && b <= '9') || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

// checkRepoNameOnlyExpanded rejects any use of REPO_NAME in the run script
// other than an expansion ($REPO_NAME or ${REPO_NAME...}). Assignment,
// printf -v, eval, unset, read and export all name the variable without a
// leading $, so the write forms do not have to be listed one by one.
func checkRepoNameOnlyExpanded(run string) error {
	for _, loc := range repoNameRe.FindAllStringIndex(run, -1) {
		start, end := loc[0], loc[1]
		if (start > 0 && isIdentByte(run[start-1])) || (end < len(run) && isIdentByte(run[end])) {
			continue // part of a longer identifier such as MY_REPO_NAME
		}
		if before := run[:start]; strings.HasSuffix(before, "$") || strings.HasSuffix(before, "${") {
			continue
		}
		lineStart := strings.LastIndexByte(run[:start], '\n') + 1
		lineEnd := len(run)
		if i := strings.IndexByte(run[start:], '\n'); i >= 0 {
			lineEnd = start + i
		}
		return fmt.Errorf("run script uses REPO_NAME other than as an expansion: %q",
			strings.TrimSpace(run[lineStart:lineEnd]))
	}
	return nil
}

// [F0925-35] checkReconcileWorkflow returns nil when the gtd-reconcile
// workflow sends an owner/repo slug as merged_prs[].repo. The reconcile
// endpoint rejects anything else (validator.RepoSlugRe, reconcile_handler.go),
// and the value travels env REPO_NAME -> jq --arg repo -> the repo field, so
// each hop is checked. YAML is parsed, so quoting and trailing comments on
// the env line do not matter.
func checkReconcileWorkflow(raw []byte) error {
	var wf reconcileWorkflow
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		return fmt.Errorf("parse workflow: %w", err)
	}
	var values, runs []string
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			if v, ok := step.Env["REPO_NAME"]; ok {
				values = append(values, v)
				runs = append(runs, step.Run)
			}
		}
	}
	if len(values) != 1 {
		return fmt.Errorf("want exactly one step with env REPO_NAME, got %d", len(values))
	}
	if got := strings.Join(strings.Fields(values[0]), ""); got != "${{github.repository}}" {
		return fmt.Errorf("REPO_NAME = %q, want ${{ github.repository }} (owner/repo)", values[0])
	}
	run := runs[0]
	if err := checkRepoNameOnlyExpanded(run); err != nil {
		return err
	}
	if b := repoBindingRe.FindAllStringSubmatch(run, -1); len(b) != 1 || b[0][1] != "arg" || b[0][2] != `"$REPO_NAME"` {
		return errors.New(`run script must bind jq's repo exactly once, as --arg repo "$REPO_NAME"`)
	}
	if fields := repoFieldRe.FindAllStringSubmatch(run, -1); len(fields) != 1 ||
		strings.TrimSpace(fields[0][1]) != "$repo" {
		return errors.New("payload must carry exactly one repo field, set to $repo")
	}
	return nil
}

func readReconcileWorkflow(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(reconcileWorkflowPath)
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	return string(raw)
}

func TestReconcileWorkflow_SendsOwnerRepoSlug(t *testing.T) {
	t.Parallel()

	if err := checkReconcileWorkflow([]byte(readReconcileWorkflow(t))); err != nil {
		t.Fatal(err)
	}

	// Why the value matters: what each context field expands to on GitHub.
	if !validator.RepoSlugRe.MatchString("Wayne997035/wayneblacktea") {
		t.Fatal("an owner/repo slug (github.repository) must pass RepoSlugRe")
	}
	if validator.RepoSlugRe.MatchString("wayneblacktea") {
		t.Fatal("a bare repo name (github.event.repository.name) must fail RepoSlugRe")
	}
}

// TestReconcileWorkflow_CheckVariants edits the real workflow in memory: every
// edit that would send a bare repo name must be caught, and edits that keep
// the meaning must pass. Each row first requires its "from" text to exist
// exactly once, so a workflow rewrite cannot leave the table testing nothing.
func TestReconcileWorkflow_CheckVariants(t *testing.T) {
	t.Parallel()
	original := readReconcileWorkflow(t)

	cases := []struct {
		name, from, to string
		wantErr        bool
	}{
		{"env sends bare repo name", repoNameEnvLine, "REPO_NAME: ${{ github.event.repository.name }}", true},
		{"jq arg strips owner", `--arg repo "$REPO_NAME"`, `--arg repo "${REPO_NAME#*/}"`, true},
		{"script reassigns REPO_NAME", scriptStartLine, scriptStartLine + "          REPO_NAME=\"${REPO_NAME#*/}\"\n", true},
		{"printf -v rebinds REPO_NAME", scriptStartLine, scriptStartLine + "          printf -v REPO_NAME '%s' \"${REPO_NAME#*/}\"\n", true},
		{"eval rebinds REPO_NAME", scriptStartLine, scriptStartLine + "          eval 'REPO_NAME=bare'\n", true},
		{"unset REPO_NAME", scriptStartLine, scriptStartLine + "          unset REPO_NAME\n", true},
		{"argjson binds repo first", `--arg repo "$REPO_NAME"`, `--argjson repo '"bare"' --arg repo "$REPO_NAME"`, true},
		{"payload field strips owner", "repo:      $repo", `repo:      ($repo | split("/")[1])`, true},
		{"value quoted", repoNameEnvLine, `REPO_NAME: "${{ github.repository }}"`, false},
		{"no spaces in expression", repoNameEnvLine, "REPO_NAME: ${{github.repository}}", false},
		{"trailing comment", repoNameEnvLine, repoNameEnvLine + " # owner/repo", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if n := strings.Count(original, tc.from); n != 1 {
				t.Fatalf("workflow must contain %q exactly once, found %d; update this row", tc.from, n)
			}
			err := checkReconcileWorkflow([]byte(strings.Replace(original, tc.from, tc.to, 1)))
			if gotErr := err != nil; gotErr != tc.wantErr {
				t.Fatalf("checkReconcileWorkflow error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
