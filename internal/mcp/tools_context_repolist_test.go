package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// [F170-01] listActiveReposProductionBudgetBytes is the acceptance bound: 50
// stored repos, each with a 20,000-rune description, must serialise into less
// than this. It is the number that makes the row cap real — the pre-[F170-01]
// handler returned all 50 rows at their full 20,000-rune cap, roughly a
// megabyte of a caller's context window, and nothing in the code stopped it
// from being 500 rows instead.
const listActiveReposProductionBudgetBytes = 20_000

// [F170-01][F170-02] listActiveReposAdversarialBudgetBytes bounds the worst
// case the caps admit: a full default page where EVERY free-text field of
// EVERY row is saturated and known_issues carries 200x the element cap.
// Measured, not derived: the caps in tools_context.go produce 51,583 bytes
// for the fixture below (~2,580 per row x the 20-row default limit), and the
// budget is that measurement plus a little headroom. Deriving it from the
// constants would make the test tautological — it would pass no matter how
// large a cap grew.
//
// The production budget above cannot catch a missing cap: its fixture only
// saturates description, so a projection that forgot known_issues or
// current_branch would still pass it. This one fails the moment any single
// field or list is projected without a bound, which is the actual invariant
// [F170-01]/[F170-02] exist to hold.
const listActiveReposAdversarialBudgetBytes = 52_000

// stubWorkspaceStore is a workspace.StoreIface whose ActiveRepos returns a
// fixed slice (or a fixed error). Declared here rather than reusing
// u13_phase_b_b4_test.go's forgingWorkspaceStore because that file is
// //go:build !integration and this one is not.
type stubWorkspaceStore struct {
	repos []db.Repo
	err   error
}

var _ workspace.StoreIface = (*stubWorkspaceStore)(nil)

func (f *stubWorkspaceStore) ActiveRepos(context.Context) ([]db.Repo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.repos, nil
}

func (f *stubWorkspaceStore) RepoByName(context.Context, string) (*db.Repo, error) { return nil, nil }

func (f *stubWorkspaceStore) RepoByID(context.Context, uuid.UUID) (*db.Repo, error) {
	return nil, nil
}

func (f *stubWorkspaceStore) UpsertRepo(context.Context, workspace.UpsertRepoParams) (*db.Repo, error) {
	return nil, nil
}

func (f *stubWorkspaceStore) GetModelPreference(context.Context) (string, error) { return "", nil }
func (f *stubWorkspaceStore) UpsertModelPreference(context.Context, string) error {
	return nil
}

// repoListResponse mirrors list_active_repos' envelope so tests assert on the
// four paging fields by name rather than by substring.
type repoListResponse struct {
	Repos []struct {
		Name                     string   `json:"name"`
		Path                     *string  `json:"path"`
		Description              *string  `json:"description"`
		Language                 *string  `json:"language"`
		CurrentBranch            *string  `json:"current_branch"`
		KnownIssues              []string `json:"known_issues"`
		NextPlannedStep          *string  `json:"next_planned_step"`
		NameTruncated            bool     `json:"name_truncated"`
		PathTruncated            bool     `json:"path_truncated"`
		DescriptionTruncated     bool     `json:"description_truncated"`
		LanguageTruncated        bool     `json:"language_truncated"`
		CurrentBranchTruncated   bool     `json:"current_branch_truncated"`
		KnownIssuesTruncated     bool     `json:"known_issues_truncated"`
		NextPlannedStepTruncated bool     `json:"next_planned_step_truncated"`
	} `json:"repos"`
	Returned int  `json:"returned"`
	Limit    int  `json:"limit"`
	Offset   int  `json:"offset"`
	HasMore  bool `json:"has_more"`
}

// callListActiveRepos invokes the handler with the given raw arguments and
// returns the response text. Fails the test on a transport error or a tool
// error result — callers that WANT a tool error use the handler directly.
func callListActiveRepos(t *testing.T, repos []db.Repo, args map[string]any) string {
	t.Helper()
	s := &Server{workspace: &stubWorkspaceStore{repos: repos}}
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleListActiveRepos(context.Background(), req)
	if err != nil {
		t.Fatalf("handleListActiveRepos: %v", err)
	}
	if result.IsError {
		t.Fatalf("handleListActiveRepos returned a tool error: %s", resultText(result))
	}
	return resultText(result)
}

// decodeRepoList parses a list_active_repos response body.
func decodeRepoList(t *testing.T, raw string) repoListResponse {
	t.Helper()
	var got repoListResponse
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal response: %v (raw: %.400s)", err, raw)
	}
	return got
}

// repoWithDescription builds one stored repo whose description is n runes.
func repoWithDescription(name string, n int) db.Repo {
	return db.Repo{
		ID:          uuid.New(),
		Name:        name,
		Status:      "active",
		Description: pgtype.Text{String: strings.Repeat("d", n), Valid: true},
	}
}

// saturatedRepo builds one stored repo with every free-text field far past
// every cap, including known_issues' element count.
func saturatedRepo(name string, issueCount int) db.Repo {
	long := strings.Repeat("x", 20_000)
	issues := make([]string, issueCount)
	for i := range issues {
		issues[i] = long
	}
	return db.Repo{
		ID:              uuid.New(),
		Name:            name + long,
		Status:          "active",
		Path:            pgtype.Text{String: long, Valid: true},
		Description:     pgtype.Text{String: long, Valid: true},
		Language:        pgtype.Text{String: long, Valid: true},
		CurrentBranch:   pgtype.Text{String: long, Valid: true},
		NextPlannedStep: pgtype.Text{String: long, Valid: true},
		KnownIssues:     issues,
	}
}

// TestHandleListActiveRepos_RowCapBoundsPayload is [F170-01]'s acceptance
// assertion: 50 stored repos with a 20,000-rune description each must come
// back as one bounded page under listActiveReposProductionBudgetBytes, with
// has_more telling the caller the other 30 rows exist.
func TestHandleListActiveRepos_RowCapBoundsPayload(t *testing.T) {
	repos := make([]db.Repo, 50)
	for i := range repos {
		repos[i] = repoWithDescription(fmt.Sprintf("repo-%02d", i), 20_000)
	}

	raw := callListActiveRepos(t, repos, nil)
	t.Logf("list_active_repos payload for 50x20,000-rune repos: %d bytes / %d runes (budget %d bytes)",
		len(raw), utf8.RuneCountInString(raw), listActiveReposProductionBudgetBytes)

	if len(raw) >= listActiveReposProductionBudgetBytes {
		t.Errorf("payload is %d bytes, budget is %d — the row cap or a field projection is missing",
			len(raw), listActiveReposProductionBudgetBytes)
	}

	got := decodeRepoList(t, raw)
	if !got.HasMore {
		t.Errorf("has_more = false with 50 rows stored and %d returned — the caller cannot tell rows were dropped", got.Returned)
	}
	if got.Returned != listActiveReposDefaultLimit {
		t.Errorf("returned = %d, want %d (the default limit)", got.Returned, listActiveReposDefaultLimit)
	}
	if len(got.Repos) != listActiveReposDefaultLimit {
		t.Errorf("len(repos) = %d, want %d — returned disagrees with the array it counts",
			len(got.Repos), listActiveReposDefaultLimit)
	}
}

// TestHandleListActiveRepos_AdversarialBudget is the invariant half of
// [F170-01]/[F170-02]: every projected field of every row saturated at once,
// with known_issues carrying 200x the element cap. Fails if any single field
// or list reaches the wire unbounded.
func TestHandleListActiveRepos_AdversarialBudget(t *testing.T) {
	repos := make([]db.Repo, 50)
	for i := range repos {
		repos[i] = saturatedRepo(fmt.Sprintf("repo-%02d", i), 1_000)
	}

	raw := callListActiveRepos(t, repos, nil)
	t.Logf("adversarial list_active_repos payload: %d bytes / %d runes (budget %d bytes)",
		len(raw), utf8.RuneCountInString(raw), listActiveReposAdversarialBudgetBytes)

	if len(raw) >= listActiveReposAdversarialBudgetBytes {
		t.Errorf("adversarial payload is %d bytes, budget is %d — a field is projected without a cap or a "+
			"list without an element cap; find and cap it, OR retune "+
			"listActiveReposAdversarialBudgetBytes alongside a fresh measurement — pick one, don't "+
			"silently bump the number", len(raw), listActiveReposAdversarialBudgetBytes)
	}

	got := decodeRepoList(t, raw)
	if len(got.Repos) == 0 {
		t.Fatal("fixture produced an empty page")
	}
	row := got.Repos[0]

	// Every saturated field must be BOTH bounded and flagged. A bound with no
	// flag is silent data loss; a flag with no bound is a lie.
	bounded := []struct {
		field string
		got   string
		flag  bool
		cap   int
	}{
		{field: "name", got: row.Name, flag: row.NameTruncated, cap: repoListShortFieldMaxRunes},
		{field: "path", got: derefOrEmpty(row.Path), flag: row.PathTruncated, cap: repoListShortFieldMaxRunes},
		{field: "description", got: derefOrEmpty(row.Description), flag: row.DescriptionTruncated, cap: repoListFieldMaxRunes},
		{field: "language", got: derefOrEmpty(row.Language), flag: row.LanguageTruncated, cap: repoListShortFieldMaxRunes},
		{field: "current_branch", got: derefOrEmpty(row.CurrentBranch), flag: row.CurrentBranchTruncated, cap: repoListShortFieldMaxRunes},
		{field: "next_planned_step", got: derefOrEmpty(row.NextPlannedStep), flag: row.NextPlannedStepTruncated, cap: repoListFieldMaxRunes},
	}
	for _, tc := range bounded {
		if n := utf8.RuneCountInString(tc.got); n != tc.cap+1 {
			t.Errorf("%s is %d runes, want %d (cap %d + clip marker)", tc.field, n, tc.cap+1, tc.cap)
		}
		if !tc.flag {
			t.Errorf("%s was shortened but %s_truncated is false — silent truncation", tc.field, tc.field)
		}
	}

	if len(row.KnownIssues) != repoListMaxKnownIssues {
		t.Errorf("known_issues has %d entries, want %d — the element cap did not apply",
			len(row.KnownIssues), repoListMaxKnownIssues)
	}
	if !row.KnownIssuesTruncated {
		t.Error("known_issues was cut from 1,000 entries to the cap but known_issues_truncated is false")
	}
	for i, issue := range row.KnownIssues {
		if n := utf8.RuneCountInString(issue); n != repoListShortFieldMaxRunes+1 {
			t.Errorf("known_issues[%d] is %d runes, want %d", i, n, repoListShortFieldMaxRunes+1)
		}
	}
}

// derefOrEmpty unwraps a nullable JSON string field.
func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// TestHandleListActiveRepos_PagingContract pins [F170-01]'s limit/offset
// semantics, including the ones a caller reaches by accident: a zero or
// negative limit, a limit past the clamp, a negative offset, and an offset
// past the end of the list.
func TestHandleListActiveRepos_PagingContract(t *testing.T) {
	repos := make([]db.Repo, 150)
	for i := range repos {
		repos[i] = repoWithDescription(fmt.Sprintf("repo-%03d", i), 10)
	}

	tests := []struct {
		name         string
		args         map[string]any
		wantLimit    int
		wantOffset   int
		wantReturned int
		wantHasMore  bool
		wantFirst    string
	}{
		{
			name:         "no args uses the default limit",
			args:         nil,
			wantLimit:    listActiveReposDefaultLimit,
			wantOffset:   0,
			wantReturned: listActiveReposDefaultLimit,
			wantHasMore:  true,
			wantFirst:    "repo-000",
		},
		{
			name:         "explicit limit and offset",
			args:         map[string]any{"limit": float64(5), "offset": float64(10)},
			wantLimit:    5,
			wantOffset:   10,
			wantReturned: 5,
			wantHasMore:  true,
			wantFirst:    "repo-010",
		},
		{
			name:         "limit above the clamp is clamped",
			args:         map[string]any{"limit": float64(9999)},
			wantLimit:    listActiveReposMaxLimit,
			wantOffset:   0,
			wantReturned: listActiveReposMaxLimit,
			wantHasMore:  true,
			wantFirst:    "repo-000",
		},
		{
			name:         "zero limit falls back to the default",
			args:         map[string]any{"limit": float64(0)},
			wantLimit:    listActiveReposDefaultLimit,
			wantOffset:   0,
			wantReturned: listActiveReposDefaultLimit,
			wantHasMore:  true,
			wantFirst:    "repo-000",
		},
		{
			name:         "negative limit falls back to the default",
			args:         map[string]any{"limit": float64(-7)},
			wantLimit:    listActiveReposDefaultLimit,
			wantOffset:   0,
			wantReturned: listActiveReposDefaultLimit,
			wantHasMore:  true,
			wantFirst:    "repo-000",
		},
		{
			name:         "negative offset clamps to zero",
			args:         map[string]any{"offset": float64(-5)},
			wantLimit:    listActiveReposDefaultLimit,
			wantOffset:   0,
			wantReturned: listActiveReposDefaultLimit,
			wantHasMore:  true,
			wantFirst:    "repo-000",
		},
		{
			name:         "last page reports has_more false",
			args:         map[string]any{"limit": float64(50), "offset": float64(100)},
			wantLimit:    50,
			wantOffset:   100,
			wantReturned: 50,
			wantHasMore:  false,
			wantFirst:    "repo-100",
		},
		{
			name:         "offset past the end returns an empty page, not an error",
			args:         map[string]any{"offset": float64(500)},
			wantLimit:    listActiveReposDefaultLimit,
			wantOffset:   500,
			wantReturned: 0,
			wantHasMore:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeRepoList(t, callListActiveRepos(t, repos, tc.args))

			if got.Limit != tc.wantLimit {
				t.Errorf("limit = %d, want %d", got.Limit, tc.wantLimit)
			}
			if got.Offset != tc.wantOffset {
				t.Errorf("offset = %d, want %d", got.Offset, tc.wantOffset)
			}
			if got.Returned != tc.wantReturned {
				t.Errorf("returned = %d, want %d", got.Returned, tc.wantReturned)
			}
			if got.HasMore != tc.wantHasMore {
				t.Errorf("has_more = %v, want %v", got.HasMore, tc.wantHasMore)
			}
			if len(got.Repos) != tc.wantReturned {
				t.Errorf("len(repos) = %d, want %d", len(got.Repos), tc.wantReturned)
			}
			if tc.wantFirst != "" && len(got.Repos) > 0 && got.Repos[0].Name != tc.wantFirst {
				t.Errorf("repos[0].name = %q, want %q — offset applied to the wrong window",
					got.Repos[0].Name, tc.wantFirst)
			}
		})
	}
}

// TestHandleListActiveRepos_EmptyStore pins the fresh-workspace case: zero
// stored repos must serialise as an empty ARRAY, not JSON null. A nil Go
// slice marshals to null, which a caller has to special-case separately from
// [] — the same contract handleListTasks (tools_gtd.go) spells out. [F170-01]
func TestHandleListActiveRepos_EmptyStore(t *testing.T) {
	raw := callListActiveRepos(t, nil, nil)

	if !strings.Contains(raw, `"repos":[]`) {
		t.Errorf(`want "repos":[] for an empty store, got: %s`, raw)
	}
	got := decodeRepoList(t, raw)
	if got.Returned != 0 || got.HasMore {
		t.Errorf("returned = %d, has_more = %v, want 0 and false", got.Returned, got.HasMore)
	}
	if got.Limit != listActiveReposDefaultLimit {
		t.Errorf("limit = %d, want %d — the paging fields must be present even on an empty page",
			got.Limit, listActiveReposDefaultLimit)
	}
}

// TestHandleListActiveRepos_RejectsMalformedPagingArgs covers the error path
// of [F170-01]'s argument parsing: a fractional or non-numeric limit/offset is
// rejected outright rather than silently truncated toward zero (which would
// turn limit=0.5 into "use the default" with no way for the caller to know).
func TestHandleListActiveRepos_RejectsMalformedPagingArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    map[string]any
		wantMsg string
	}{
		{name: "fractional limit", args: map[string]any{"limit": 2.5}, wantMsg: "limit must be a whole number"},
		{name: "fractional offset", args: map[string]any{"offset": 0.1}, wantMsg: "offset must be a whole number"},
		{name: "string limit", args: map[string]any{"limit": "20"}, wantMsg: "limit must be a number"},
		{name: "bool offset", args: map[string]any{"offset": true}, wantMsg: "offset must be a number"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{workspace: &stubWorkspaceStore{repos: []db.Repo{repoWithDescription("r", 10)}}}
			req := mcpmsg.CallToolRequest{}
			req.Params.Arguments = tc.args

			result, err := s.handleListActiveRepos(context.Background(), req)
			if err != nil {
				t.Fatalf("handleListActiveRepos: %v", err)
			}
			if !result.IsError {
				t.Fatalf("want a tool error, got a success result: %s", resultText(result))
			}
			if got := resultText(result); got != tc.wantMsg {
				t.Errorf("error message = %q, want %q", got, tc.wantMsg)
			}
		})
	}
}

// TestHandleListActiveRepos_StoreErrorGoesThroughStoreErrorResult pins the
// error path to tool_errors.go's shared helper rather than any new
// fmt.Sprintf of the raw store error into the client response.
func TestHandleListActiveRepos_StoreErrorGoesThroughStoreErrorResult(t *testing.T) {
	const secret = "pq: password authentication failed for user \"wbt\""
	s := &Server{workspace: &stubWorkspaceStore{err: errors.New(secret)}}

	result, err := s.handleListActiveRepos(context.Background(), mcpmsg.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleListActiveRepos: %v", err)
	}
	if !result.IsError {
		t.Fatal("want a tool error when the store fails, got a success result")
	}
	if strings.Contains(resultText(result), secret) {
		t.Errorf("raw store error text reached the client: %s", resultText(result))
	}
}

// TestHandleListActiveRepos_TruncationFlags is [F170-02]'s per-field
// contract: a flag appears if and only if that field was actually shortened.
// The boundary cases matter most — a row exactly at the cap must NOT be
// flagged, or every caller learns to ignore the flag.
func TestHandleListActiveRepos_TruncationFlags(t *testing.T) {
	tests := []struct {
		name          string
		repo          db.Repo
		wantFlags     map[string]bool
		wantRawAbsent []string
	}{
		{
			name: "short row carries no truncation keys at all",
			repo: db.Repo{
				ID:              uuid.New(),
				Name:            "wayneblacktea",
				Status:          "active",
				Description:     pgtype.Text{String: "a short description", Valid: true},
				NextPlannedStep: pgtype.Text{String: "ship it", Valid: true},
				KnownIssues:     []string{"one issue"},
			},
			wantRawAbsent: []string{"_truncated"},
		},
		{
			name: "description exactly at the cap is not truncated",
			repo: db.Repo{
				ID:          uuid.New(),
				Name:        "wayneblacktea",
				Status:      "active",
				Description: pgtype.Text{String: strings.Repeat("d", repoListFieldMaxRunes), Valid: true},
			},
			wantRawAbsent: []string{"_truncated"},
		},
		{
			name: "description one rune past the cap is truncated",
			repo: db.Repo{
				ID:          uuid.New(),
				Name:        "wayneblacktea",
				Status:      "active",
				Description: pgtype.Text{String: strings.Repeat("d", repoListFieldMaxRunes+1), Valid: true},
			},
			wantFlags: map[string]bool{"description": true},
		},
		{
			name: "next_planned_step truncates independently of description",
			repo: db.Repo{
				ID:              uuid.New(),
				Name:            "wayneblacktea",
				Status:          "active",
				Description:     pgtype.Text{String: "short", Valid: true},
				NextPlannedStep: pgtype.Text{String: strings.Repeat("s", 5_000), Valid: true},
			},
			wantFlags: map[string]bool{"next_planned_step": true},
		},
		{
			name: "known_issues element count alone trips the flag",
			repo: db.Repo{
				ID:          uuid.New(),
				Name:        "wayneblacktea",
				Status:      "active",
				KnownIssues: []string{"a", "b", "c", "d", "e", "f"},
			},
			wantFlags: map[string]bool{"known_issues": true},
		},
		{
			name: "known_issues exactly at the element cap is not truncated",
			repo: db.Repo{
				ID:          uuid.New(),
				Name:        "wayneblacktea",
				Status:      "active",
				KnownIssues: []string{"a", "b", "c", "d", "e"},
			},
			wantRawAbsent: []string{"_truncated"},
		},
		{
			name: "a NULL column is never reported as truncated",
			repo: db.Repo{
				ID:     uuid.New(),
				Name:   "wayneblacktea",
				Status: "active",
			},
			wantRawAbsent: []string{"_truncated"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := callListActiveRepos(t, []db.Repo{tc.repo}, nil)
			for _, absent := range tc.wantRawAbsent {
				if strings.Contains(raw, absent) {
					t.Errorf("response carries %q for an untruncated row: %s", absent, raw)
				}
			}
			if tc.wantFlags == nil {
				return
			}

			got := decodeRepoList(t, raw)
			if len(got.Repos) != 1 {
				t.Fatalf("len(repos) = %d, want 1", len(got.Repos))
			}
			row := got.Repos[0]
			actual := map[string]bool{
				"name":              row.NameTruncated,
				"path":              row.PathTruncated,
				"description":       row.DescriptionTruncated,
				"language":          row.LanguageTruncated,
				"current_branch":    row.CurrentBranchTruncated,
				"known_issues":      row.KnownIssuesTruncated,
				"next_planned_step": row.NextPlannedStepTruncated,
			}
			for field, isSet := range actual {
				if want := tc.wantFlags[field]; isSet != want {
					t.Errorf("%s_truncated = %v, want %v (raw: %s)", field, isSet, want, raw)
				}
			}
		})
	}
}

// TestHandleListActiveRepos_NeutralisesBeforeProjecting pins the ordering
// [F170-02] depends on: the list projection runs AFTER wrapUntrustedRepo, so
// a forged boundary marker is still neutralised in a field the projection
// then shortens. Projecting first would hand the shorter cap a raw marker.
func TestHandleListActiveRepos_NeutralisesBeforeProjecting(t *testing.T) {
	marker := storedContextMarkerEnd
	repo := db.Repo{
		ID:          uuid.New(),
		Name:        "wayneblacktea",
		Status:      "active",
		Description: pgtype.Text{String: marker + strings.Repeat("d", 20_000), Valid: true},
	}

	raw := callListActiveRepos(t, []db.Repo{repo}, nil)
	if strings.Contains(raw, marker) {
		t.Errorf("forged marker survived the list projection: %.400s", raw)
	}
	if !strings.Contains(raw, boundaryMarkerPlaceholder) {
		t.Errorf("forged marker was removed without leaving the placeholder: %.400s", raw)
	}

	got := decodeRepoList(t, raw)
	if !got.Repos[0].DescriptionTruncated {
		t.Error("description_truncated = false for a 20,000-rune description")
	}
}

// TestHandleListActiveRepos_RepoListFieldMarkerTruncated is [F173-01]'s
// integration-layer probe, one case per *_truncated field. Each fixture
// carries a forged storedContextMarkerEnd (26 runes) well under that field's
// own list-view cap, so the ONLY reason the projection differs from what is
// stored is marker neutralisation — the same shape as
// TestClipRepoListField_ShortFieldWithForgedMarkerReportsTruncatedTrue below,
// but driven through handleListActiveRepos end to end instead of calling
// clipRepoListField directly, and without TestHandleListActiveRepos_
// AdversarialBudget's over-cap saturation, which trips every flag through
// length alone and never exercises the marker-only path this pins. Before
// [F173-01], toRepoListItem compared its output to wrapUntrustedRepo's
// already-neutralised copy, so this case fell through — the marker was
// already gone from the copy this comparison used, so the field's flag
// stayed false while the caller received a value that was not what raw had
// stored.
//
// Mutation check: reverting toRepoListItem's per-field comparison in
// tools_context.go from raw's stored value back to r's wrapUntrustedRepo'd
// copy makes the corresponding case here fail (all seven if the whole
// wiring is reverted, one if only that field's comparison is).
func TestHandleListActiveRepos_RepoListFieldMarkerTruncated(t *testing.T) {
	marker := storedContextMarkerEnd
	short := "x-" + marker // 28 runes, under repoListShortFieldMaxRunes (120)
	long := "y-" + marker  // 28 runes, under repoListFieldMaxRunes (500)

	tests := []struct {
		name      string
		repo      db.Repo
		wantField string
	}{
		{
			name:      "name",
			repo:      db.Repo{ID: uuid.New(), Name: short, Status: "active"},
			wantField: "name",
		},
		{
			name: "path",
			repo: db.Repo{
				ID: uuid.New(), Name: "wayneblacktea", Status: "active",
				Path: pgtype.Text{String: short, Valid: true},
			},
			wantField: "path",
		},
		{
			name: "description",
			repo: db.Repo{
				ID: uuid.New(), Name: "wayneblacktea", Status: "active",
				Description: pgtype.Text{String: long, Valid: true},
			},
			wantField: "description",
		},
		{
			name: "language",
			repo: db.Repo{
				ID: uuid.New(), Name: "wayneblacktea", Status: "active",
				Language: pgtype.Text{String: short, Valid: true},
			},
			wantField: "language",
		},
		{
			name: "current_branch",
			repo: db.Repo{
				ID: uuid.New(), Name: "wayneblacktea", Status: "active",
				CurrentBranch: pgtype.Text{String: short, Valid: true},
			},
			wantField: "current_branch",
		},
		{
			name: "known_issues",
			repo: db.Repo{
				ID: uuid.New(), Name: "wayneblacktea", Status: "active",
				KnownIssues: []string{short},
			},
			wantField: "known_issues",
		},
		{
			name: "next_planned_step",
			repo: db.Repo{
				ID: uuid.New(), Name: "wayneblacktea", Status: "active",
				NextPlannedStep: pgtype.Text{String: long, Valid: true},
			},
			wantField: "next_planned_step",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := callListActiveRepos(t, []db.Repo{tc.repo}, nil)
			got := decodeRepoList(t, raw)
			if len(got.Repos) != 1 {
				t.Fatalf("len(repos) = %d, want 1", len(got.Repos))
			}
			row := got.Repos[0]
			actual := map[string]bool{
				"name":              row.NameTruncated,
				"path":              row.PathTruncated,
				"description":       row.DescriptionTruncated,
				"language":          row.LanguageTruncated,
				"current_branch":    row.CurrentBranchTruncated,
				"known_issues":      row.KnownIssuesTruncated,
				"next_planned_step": row.NextPlannedStepTruncated,
			}
			for field, isSet := range actual {
				if want := field == tc.wantField; isSet != want {
					t.Errorf("%s_truncated = %v, want %v for a marker-only %q field (raw: %.400s)",
						field, isSet, want, tc.wantField, raw)
				}
			}
		})
	}
}

// TestClipRepoListField_OverCapReportsTruncatedTrue is [F0902-53]'s first
// named case: a field over the cap is clipped and its truncated flag is
// true. Both the pre-fix rune-count check and the post-fix output-vs-input
// check agree here — this pins the case that must never regress while the
// other two below pin the case the pre-fix check got wrong.
func TestClipRepoListField_OverCapReportsTruncatedTrue(t *testing.T) {
	s := strings.Repeat("d", repoListShortFieldMaxRunes+1)

	got, truncated := clipRepoListField(s, repoListShortFieldMaxRunes)

	if !truncated {
		t.Errorf("clipRepoListField(%d-rune string, %d): truncated = false, want true",
			utf8.RuneCountInString(s), repoListShortFieldMaxRunes)
	}
	if want := repoListShortFieldMaxRunes + 1; utf8.RuneCountInString(got) != want { // +1 for clipMarker
		t.Errorf("clipRepoListField over cap: got %d runes, want %d (cap + clip marker)",
			utf8.RuneCountInString(got), want)
	}
}

// TestClipRepoListField_ShortFieldWithForgedMarkerReportsTruncatedTrue is
// [F0902-53]'s deliberate high-report case. The field here is 33 runes,
// far under the 120-rune cap, so
// the OLD implementation — which compared utf8.RuneCountInString(s) against
// maxRunes — reported false for it. But clipSafe still rewrites the forged
// storedContextMarkerEnd substring inside it into boundaryMarkerPlaceholder
// via neutralizeBoundaryMarkers, so the value the caller receives is not
// byte-identical to what is stored either way. Under-reporting that as
// "false" told the caller it held the exact stored value when it did not;
// [F0902-53] chooses to over-report instead — a caller that sees true and
// re-fetches via sync_repo only pays one extra round trip, which is cheap
// next to a caller silently trusting a rewritten value.
//
// Mutation check: reverting clipRepoListField's condition from `out != s` to
// `utf8.RuneCountInString(s) > maxRunes` makes this test fail, because the
// 33-rune input never exceeds the 120-rune cap.
func TestClipRepoListField_ShortFieldWithForgedMarkerReportsTruncatedTrue(t *testing.T) {
	s := "branch-" + storedContextMarkerEnd // 33 runes: far under repoListShortFieldMaxRunes (120)
	if n := utf8.RuneCountInString(s); n >= repoListShortFieldMaxRunes {
		t.Fatalf("fixture is %d runes, must stay under the %d-rune cap to exercise the marker-only path",
			n, repoListShortFieldMaxRunes)
	}

	got, truncated := clipRepoListField(s, repoListShortFieldMaxRunes)

	if !truncated {
		t.Errorf("clipRepoListField(%q, %d): truncated = false, want true — the forged marker was "+
			"neutralised so the output differs from the stored value even though the input never "+
			"reached the cap (this is the deliberate high-report [F0902-53] chose over the prior "+
			"low-report)", s, repoListShortFieldMaxRunes)
	}
	if strings.Contains(got, storedContextMarkerEnd) {
		t.Errorf("forged marker survived clipRepoListField: %q", got)
	}
	if !strings.Contains(got, boundaryMarkerPlaceholder) {
		t.Errorf("marker was removed without leaving the placeholder: %q", got)
	}
}

// TestClipRepoListField_ShortFieldNoMarkerReportsTruncatedFalse pins the
// negative case both the old and new implementations agree on: a short
// field with nothing to clip and nothing to neutralise must report false.
// Without this case, a fix that flips clipRepoListField to always report
// true would still pass the two cases above.
func TestClipRepoListField_ShortFieldNoMarkerReportsTruncatedFalse(t *testing.T) {
	s := "main"

	got, truncated := clipRepoListField(s, repoListShortFieldMaxRunes)

	if truncated {
		t.Errorf("clipRepoListField(%q, %d): truncated = true, want false", s, repoListShortFieldMaxRunes)
	}
	if got != s {
		t.Errorf("clipRepoListField(%q, %d) = %q, want unchanged", s, repoListShortFieldMaxRunes, got)
	}
}
