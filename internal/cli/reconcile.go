package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/joho/godotenv"
)

const reconcileUsage = `wbt reconcile — drain merged-PR backlog into GTD tasks

Usage:
  wbt reconcile [--since YYYY-MM-DD] [--server http://localhost:PORT]

Flags:
  --since   include PRs merged on/after this date (default: 90 days ago)
  --server  base URL of wayneblacktea-server (default: http://localhost:PORT
            from PORT env, or http://localhost:8420)
  --help    show this help

This subcommand iterates over every active repo registered via
GET /api/workspace/repos and shells out to ` + "`gh pr list --state merged`" + `
to collect the merged-PR list. The aggregated payload is then POSTed to
/api/tasks/reconcile-merged-prs which auto-closes any task whose
branch_name or pr_url matches exactly.

MVP: null-linkage backlog tasks surface as fuzzy candidates once GTD-fix
10/12 ships (Phase 2 fuzzy matcher). They are never auto-closed — the
operator must accept the candidate via the existing UI.

Authentication: uses API_KEY from .env (same as ` + "`wbt serve`" + `).
Requires the gh CLI to be installed and authenticated; if gh is missing or
unauthenticated the subcommand prints a warning and exits 0 so CI loops
remain green.
`

// reconcileHTTPTimeout caps each gh / HTTP call. Generous because gh can
// take 10+ s on slow networks and the aggregated POST may carry hundreds of
// PRs.
const reconcileHTTPTimeout = 60 * time.Second

// reconcileMaxRepos defends against accidental fan-out on a workspace with
// thousands of repos. Personal-OS scale is ~20 repos so this is comfortable.
const reconcileMaxRepos = 200

// reconcileMaxPRsPerCall mirrors the server-side cap (reconcileMaxEntries=200
// in handler/reconcile_handler.go). We chunk the POST if the union exceeds
// this; documented inline.
const reconcileMaxPRsPerCall = 200

// reconcileOptions bundles the resolved CLI flags after parsing.
type reconcileOptions struct {
	since     time.Time
	serverURL string
	apiKey    string
}

// parseReconcileArgs handles --help, flag parsing, env loading, and
// resolution of since/server defaults. Extracted from RunReconcile to keep
// the main function's cyclomatic complexity within the gocyclo budget.
func parseReconcileArgs(args []string) (reconcileOptions, bool, error) {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		_, _ = fmt.Fprint(os.Stdout, reconcileUsage)
		return reconcileOptions{}, true, nil
	}
	fs := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sinceFlag := fs.String("since", "", "include PRs merged on/after this date (YYYY-MM-DD); default 90 days ago")
	serverFlag := fs.String("server", "", "wayneblacktea-server base URL; default http://localhost:$PORT")
	if err := fs.Parse(args); err != nil {
		return reconcileOptions{}, false, fmt.Errorf("reconcile: %w", err)
	}

	_ = godotenv.Load()
	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return reconcileOptions{}, false, errors.New(
			"reconcile: API_KEY must be set (run `wbt init` or export API_KEY)")
	}
	since, err := resolveSince(*sinceFlag)
	if err != nil {
		return reconcileOptions{}, false, fmt.Errorf("reconcile: %w", err)
	}
	return reconcileOptions{
		since:     since,
		serverURL: resolveServerURL(*serverFlag),
		apiKey:    apiKey,
	}, false, nil
}

// collectMergedPRs iterates over `repos` and aggregates the gh output. Per-
// repo failures emit a warning and continue (one bad auth shouldn't kill
// the whole batch).
func collectMergedPRs(ctx context.Context, repos []string, since time.Time) []reconcilePR {
	allPRs := make([]reconcilePR, 0)
	for _, slug := range repos {
		prs, ghErr := ghListMergedPRs(ctx, slug, since)
		if ghErr != nil {
			fmt.Fprintf(os.Stderr, "wbt reconcile: warning: gh list %s: %v\n", slug, ghErr)
			continue
		}
		for i := range prs {
			prs[i].Repo = slug
		}
		allPRs = append(allPRs, prs...)
	}
	return allPRs
}

// postAllInChunks splits prs into reconcileMaxPRsPerCall chunks and aggregates
// the response counts. Returns (applied, candidateWrites, noMatch, err).
func postAllInChunks(
	ctx context.Context, serverURL, apiKey string, prs []reconcilePR,
) (applied, candidates, noMatch int, err error) {
	for chunkStart := 0; chunkStart < len(prs); chunkStart += reconcileMaxPRsPerCall {
		end := chunkStart + reconcileMaxPRsPerCall
		if end > len(prs) {
			end = len(prs)
		}
		resp, postErr := postReconcileBatch(ctx, serverURL, apiKey, prs[chunkStart:end])
		if postErr != nil {
			return 0, 0, 0, fmt.Errorf("reconcile: POST batch [%d:%d]: %w",
				chunkStart, end, postErr)
		}
		applied += resp.Applied
		candidates += resp.CandidateWrites
		noMatch += resp.NoMatch
	}
	return applied, candidates, noMatch, nil
}

// RunReconcile implements the `wbt reconcile` subcommand. args is everything
// after "wbt reconcile" on the command line. Returns nil on success or a
// fatal error; gh-missing / gh-unauth is a warning, not an error.
func RunReconcile(args []string) error {
	opts, helpShown, err := parseReconcileArgs(args)
	if err != nil || helpShown {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Soft-fail if gh is missing or unauthed — CI loops and freshly-cloned
	// dev machines shouldn't break on a missing tool.
	if msg := ensureGHReady(ctx); msg != "" {
		fmt.Fprintf(os.Stderr, "wbt reconcile: warning: %s — skipping (exit 0)\n", msg)
		return nil
	}

	repos, err := fetchActiveRepos(ctx, opts.serverURL, opts.apiKey)
	if err != nil {
		return fmt.Errorf("reconcile: fetch active repos: %w", err)
	}
	if len(repos) > reconcileMaxRepos {
		return fmt.Errorf("reconcile: too many active repos (%d > %d)",
			len(repos), reconcileMaxRepos)
	}
	if len(repos) == 0 {
		fmt.Println("wbt reconcile: no active repos registered — nothing to do.")
		return nil
	}

	allPRs := collectMergedPRs(ctx, repos, opts.since)
	if len(allPRs) == 0 {
		fmt.Printf(
			"wbt reconcile: 0 merged PRs across %d repos since %s — nothing to do.\n",
			len(repos), opts.since.Format("2006-01-02"),
		)
		return nil
	}

	applied, candidates, noMatch, err := postAllInChunks(ctx, opts.serverURL, opts.apiKey, allPRs)
	if err != nil {
		return err
	}

	fmt.Printf(
		"wbt reconcile: %d merged PRs across %d repos since %s.\n"+
			"Auto-closed %d tasks. Fuzzy candidates surfaced: %d. "+
			"(%d PRs without any task match)\n",
		len(allPRs), len(repos), opts.since.Format("2006-01-02"),
		applied, candidates, noMatch,
	)
	return nil
}

// resolveSince parses --since or defaults to 90 days ago. Date format YYYY-MM-DD.
func resolveSince(s string) (time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return time.Now().UTC().Add(-90 * 24 * time.Hour), nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("--since %q must be YYYY-MM-DD: %w", s, err)
	}
	return t, nil
}

// resolveServerURL returns the explicit --server flag, or builds the URL
// from PORT env (the same env var wbt serve sets), defaulting to :8420.
func resolveServerURL(flag string) string {
	if strings.TrimSpace(flag) != "" {
		return strings.TrimRight(strings.TrimSpace(flag), "/")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8420"
	}
	return "http://localhost:" + port
}

// ensureGHReady returns "" on success or a human-readable warning string
// describing why gh is unusable. Empty string = caller proceeds.
func ensureGHReady(ctx context.Context) string {
	if _, err := exec.LookPath("gh"); err != nil {
		return "gh CLI not installed (https://cli.github.com)"
	}
	// gh auth status exits non-zero when not authenticated. We don't care
	// about stdout/stderr content; the exit code is sufficient.
	cctx, cancel := context.WithTimeout(ctx, reconcileHTTPTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		return "gh CLI not authenticated (run `gh auth login`)"
	}
	return ""
}

// reconcilePR is the per-PR struct the CLI both decodes from gh JSON output
// and re-encodes as the wire shape the server's /api/tasks/reconcile-merged-prs
// expects.
type reconcilePR struct {
	URL      string `json:"url"`
	HeadRef  string `json:"head_ref"`
	MergedAt string `json:"merged_at"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Repo     string `json:"repo"`
}

// ghMergedPR is the JSON shape `gh pr list ... --json url,headRefName,...`
// returns. Field names are gh's camelCase, mapped into the reconcilePR
// snake_case wire shape after decode.
type ghMergedPR struct {
	URL         string `json:"url"`
	HeadRefName string `json:"headRefName"`
	MergedAt    string `json:"mergedAt"`
	Title       string `json:"title"`
	Body        string `json:"body"`
}

// ghListMergedPRs shells out to `gh -R <slug> pr list --state merged
// --search "merged:>=$SINCE" --limit 500 --json ...`. Returns parsed PRs.
// A non-zero exit code from gh is propagated as an error so the caller can
// log + skip the repo; the function itself does NOT print to stderr.
func ghListMergedPRs(ctx context.Context, slug string, since time.Time) ([]reconcilePR, error) {
	if !validator.RepoSlugRe.MatchString(slug) {
		return nil, fmt.Errorf("invalid repo slug %q (must match owner/repo pattern)", slug)
	}
	args := []string{
		"-R", slug,
		"pr", "list",
		"--state", "merged",
		"--search", "merged:>=" + since.Format("2006-01-02"),
		"--limit", "500",
		"--json", "url,headRefName,mergedAt,title,body,number,baseRefName",
	}
	cctx, cancel := context.WithTimeout(ctx, reconcileHTTPTimeout)
	defer cancel()
	// G204 rationale: the only variable argument is `slug` which we have
	// already validated against validator.RepoSlugRe (^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$);
	// every other arg is a fixed string literal. The variadic call to
	// exec.CommandContext does NOT invoke a shell — gh receives the args
	// as a strict argv. Slug content is restricted to the GitHub-permitted
	// charset, so no shell-meta / path-traversal / newline-smuggling is possible.
	//nolint:gosec // G204: gh argv is fully argv-style (no shell), slug pre-validated by RepoSlugRe
	cmd := exec.CommandContext(cctx, "gh", args...)
	cmd.Stderr = nil // discard gh's "no merged PRs" warning
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("gh exit %d: %s", ee.ExitCode(), strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh output: %w", err)
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 || string(out) == "[]" {
		return nil, nil
	}
	var raw []ghMergedPR
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("decode gh output: %w", err)
	}
	prs := make([]reconcilePR, 0, len(raw))
	for _, r := range raw {
		// gh JSON outputs mergedAt as RFC3339 (e.g. "2026-05-18T12:00:00Z"); pass
		// through unchanged — the server validates RFC3339.
		prs = append(prs, reconcilePR{
			URL:      r.URL,
			HeadRef:  r.HeadRefName,
			MergedAt: r.MergedAt,
			Title:    r.Title,
			Body:     r.Body,
		})
	}
	return prs, nil
}

// fetchActiveRepos GETs /api/workspace/repos and returns the repo slugs in
// "owner/name" form. The server's ListRepos returns []db.Repo with .Name;
// the CLI assumes .Name is the "owner/name" GitHub slug (this matches every
// existing repo registered via `wbt init` and the workspace UI).
func fetchActiveRepos(ctx context.Context, serverURL, apiKey string) ([]string, error) {
	u := serverURL + "/api/workspace/repos"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-API-Key", apiKey)

	client := &http.Client{Timeout: reconcileHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return nil, fmt.Errorf("GET %s: status %d: %s", u, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var raw []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode body: %w", err)
	}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if r.Name != "" {
			out = append(out, r.Name)
		}
	}
	return out, nil
}

// reconcileResponse mirrors the server's response shape — only the counts we
// surface to the user.
type reconcileResponse struct {
	Applied         int `json:"applied"`
	NoMatch         int `json:"no_match"`
	CandidateWrites int `json:"candidate_writes"`
}

// postReconcileBatch POSTs a chunk of PRs to /api/tasks/reconcile-merged-prs
// and returns the parsed response.
func postReconcileBatch(ctx context.Context, serverURL, apiKey string, prs []reconcilePR) (reconcileResponse, error) {
	payload := map[string]any{"merged_prs": prs}
	body, err := json.Marshal(payload)
	if err != nil {
		return reconcileResponse{}, fmt.Errorf("marshal payload: %w", err)
	}
	u := serverURL + "/api/tasks/reconcile-merged-prs"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return reconcileResponse{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	client := &http.Client{Timeout: reconcileHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return reconcileResponse{}, fmt.Errorf("POST %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return reconcileResponse{}, fmt.Errorf("POST %s: status %d: %s",
			u, resp.StatusCode, strings.TrimSpace(string(errBody)))
	}
	var out reconcileResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return reconcileResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}
