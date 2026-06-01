package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/joho/godotenv"
)

// postMergeLocalTimeout caps the reconcile POST so a git merge is never blocked
// for more than a couple seconds. The hook is fail-open: on timeout it logs and
// exits 0.
const postMergeLocalTimeout = 2 * time.Second

const postMergeLocalUsage = `wbt post-merge-local — auto-close the GTD task for the just-merged PR

Usage:
  wbt post-merge-local [--server http://localhost:PORT] [--dry-run]

Invoked by the git post-merge hook installed via ` + "`wbt install-git-hook`" + `.
Reads the HEAD commit to derive the merged PR (owner/repo + #N from the squash
commit subject) and POSTs it to /api/tasks/reconcile-merged-prs on the local
wayneblacktea server, which auto-closes any matching task.

Fail-open: any missing data, missing server, or network error is logged to
$TMPDIR/wbt-post-merge.log and the command exits 0 — a merge is never blocked.

Flags:
  --server   base URL of wayneblacktea-server (default: http://localhost:$PORT)
  --dry-run  print the payload that would be POSTed; make no HTTP call
  --help     show this help
`

// prNumberRe extracts the trailing "(#123)" PR marker GitHub appends to a
// squash-merge commit subject.
var prNumberRe = regexp.MustCompile(`\(#(\d+)\)`)

// postMergeInfo is the data the hook derives from the merged HEAD commit.
type postMergeInfo struct {
	URL      string
	HeadRef  string
	MergedAt string
	Title    string
	Repo     string
}

// RunPostMergeLocal implements `wbt post-merge-local`. It is designed to run
// from a git post-merge hook in an arbitrary repo and is ALWAYS fail-open: it
// returns nil on every non-flag error so the hook never aborts a merge.
func RunPostMergeLocal(args []string) error {
	if len(args) > 0 && (args[0] == helpFlag || args[0] == "-h") {
		_, _ = fmt.Fprint(os.Stdout, postMergeLocalUsage)
		return nil
	}
	fs := flag.NewFlagSet("post-merge-local", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	serverFlag := fs.String("server", "", "wayneblacktea-server base URL")
	dryRun := fs.Bool("dry-run", false, "print payload without POSTing")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("post-merge-local: %w", err)
	}

	// A post-merge hook MUST NOT print to stderr (git surfaces it mid-merge),
	// so redirect slog to a tmp file. (backend-security-design.md §5.1)
	InitHookSlog("wbt-post-merge")

	// Resolve config best-effort: global config first
	// (~/.config/wayneblacktea/config.yaml), then any .env in CWD.
	_ = LoadGlobalConfig()
	_ = godotenv.Load()

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		slog.Warn("post-merge-local: API_KEY not set; skipping reconcile")
		return nil
	}

	info, err := extractPostMergeInfo(".")
	if err != nil {
		slog.Warn("post-merge-local: cannot read git HEAD; skipping", "error", err)
		return nil
	}
	if info.URL == "" || info.Repo == "" || info.HeadRef == "" {
		slog.Info("post-merge-local: no GitHub PR detected in HEAD commit; skipping",
			"repo", info.Repo, "has_url", info.URL != "")
		return nil
	}

	pr := reconcilePR{
		URL:      info.URL,
		HeadRef:  info.HeadRef,
		MergedAt: info.MergedAt,
		Title:    info.Title,
		Repo:     info.Repo,
	}

	if *dryRun {
		b, _ := json.MarshalIndent(map[string]any{"merged_prs": []reconcilePR{pr}}, "", "  ")
		fmt.Fprintln(os.Stdout, string(b))
		return nil
	}

	if err := postMergeReconcile(resolveServerURL(*serverFlag), apiKey, pr); err != nil {
		slog.Warn("post-merge-local: reconcile POST failed (fail-open)", "error", err)
		return nil
	}
	slog.Info("post-merge-local: reconcile posted", "repo", info.Repo, "url", info.URL)
	return nil
}

// extractPostMergeInfo derives the merged-PR fields from the HEAD commit of the
// repo at repoRoot. A non-GitHub remote or a commit without a "(#N)" marker
// yields empty URL/Repo, which the caller treats as "skip".
func extractPostMergeInfo(repoRoot string) (postMergeInfo, error) {
	subject, mergedAt, err := gitHeadCommit(repoRoot)
	if err != nil {
		return postMergeInfo{}, err
	}
	repo := deriveRepoSlug(gitRemoteOrigin(repoRoot))
	prNum := extractPRNumber(subject)
	url := buildGitHubPRURL(repo, prNum)
	head := gitMergedBranch(repoRoot)
	if head == "" && prNum != "" {
		// The branch is often gone after a squash-merge; synthesize a
		// non-empty head_ref so the endpoint's validation passes. It won't
		// match any task by branch — the pr_url match carries the close.
		head = "pr-" + prNum
	}
	return postMergeInfo{
		URL:      url,
		HeadRef:  head,
		MergedAt: mergedAt,
		Title:    subject,
		Repo:     repo,
	}, nil
}

// gitHeadCommit returns the HEAD commit subject and committer date (RFC3339).
func gitHeadCommit(repoRoot string) (subject, mergedAt string, err error) {
	out, err := runGit(repoRoot, "log", "-1", "--format=%s%x00%cI")
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(strings.TrimRight(out, "\n"), "\x00", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected git log output")
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

// gitRemoteOrigin returns the origin remote URL, or "" when unset.
func gitRemoteOrigin(repoRoot string) string {
	out, err := runGit(repoRoot, "config", "--get", "remote.origin.url")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// gitMergedBranch best-effort resolves the merged branch name from the ref
// decorations on HEAD. Returns "" when only the default branch is present
// (typical after a squash-merge that deleted the source branch).
func gitMergedBranch(repoRoot string) string {
	out, err := runGit(repoRoot, "log", "-1", "--format=%D")
	if err != nil {
		return ""
	}
	for ref := range strings.SplitSeq(out, ",") {
		ref = strings.TrimSpace(ref)
		b := strings.TrimPrefix(ref, "origin/")
		if b == ref {
			continue // not an origin/* ref
		}
		if b != "HEAD" && b != "main" && b != "master" {
			return b
		}
	}
	return ""
}

// runGit runs `git -C repoRoot <args...>` with a short timeout and returns
// stdout. stderr is discarded.
func runGit(repoRoot string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	full := append([]string{"-C", repoRoot}, args...)
	// G204 rationale: every arg is a fixed string literal except repoRoot (the
	// repo path, not attacker input); exec.CommandContext uses strict argv with
	// no shell, so no meta-character injection is possible.
	//nolint:gosec // G204: fixed git argv, no shell
	cmd := exec.CommandContext(ctx, "git", full...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return buf.String(), nil
}

// deriveRepoSlug normalizes a GitHub remote URL to "owner/repo". Non-github.com
// remotes (GitLab, Bitbucket, etc.) return "" — out of scope for PR reconcile.
func deriveRepoSlug(originURL string) string {
	u := strings.TrimSpace(originURL)
	if u == "" {
		return ""
	}
	var path string
	switch {
	case strings.HasPrefix(u, "https://github.com/"):
		path = strings.TrimPrefix(u, "https://github.com/")
	case strings.HasPrefix(u, "git@github.com:"):
		path = strings.TrimPrefix(u, "git@github.com:")
	case strings.HasPrefix(u, "ssh://git@github.com/"):
		path = strings.TrimPrefix(u, "ssh://git@github.com/")
	default:
		return ""
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if validator.RepoSlugRe.MatchString(path) {
		return path
	}
	return ""
}

// extractPRNumber returns the digits of the trailing "(#N)" marker, or "".
func extractPRNumber(subject string) string {
	m := prNumberRe.FindStringSubmatch(subject)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// buildGitHubPRURL constructs the canonical PR URL, or "" when inputs are empty.
func buildGitHubPRURL(slug, prNumber string) string {
	if slug == "" || prNumber == "" {
		return ""
	}
	return "https://github.com/" + slug + "/pull/" + prNumber
}

// postMergeReconcile POSTs a single merged PR to the reconcile endpoint with a
// short timeout. Returns an error the caller logs and swallows (fail-open).
func postMergeReconcile(serverURL, apiKey string, pr reconcilePR) error {
	payload, err := json.Marshal(map[string]any{"merged_prs": []reconcilePR{pr}})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), postMergeLocalTimeout)
	defer cancel()
	u := serverURL + "/api/tasks/reconcile-merged-prs"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	client := &http.Client{Timeout: postMergeLocalTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4*1024))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
