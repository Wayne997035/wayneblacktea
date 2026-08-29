package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// rowCapSeedCount is the fixture size for the [F170-04]/[F170-05]/[F170-06]
// row-cap tests: an order of magnitude above the default page so a handler
// that forgot its cap produces an obviously different answer, not a marginal
// one.
const rowCapSeedCount = 500

// listPage is the common response shape the three paged list tools return.
// Deliberately decoded through one struct: the acceptance criterion is that
// all three carry limit/offset/returned/has_more, and a per-tool struct would
// let one of them quietly drop a field.
type listPage struct {
	Returned int  `json:"returned"`
	Limit    int  `json:"limit"`
	Offset   int  `json:"offset"`
	HasMore  bool `json:"has_more"`
}

// decodeListPage decodes the envelope and fails loudly on a missing field.
// json.Unmarshal is happy to leave a field at its zero value, so absence is
// checked against the raw text as well — "limit":0 and no "limit" key at all
// decode identically otherwise.
func decodeListPage(t *testing.T, r *mcpmsg.CallToolResult) listPage {
	t.Helper()
	if r.IsError {
		t.Fatalf("list tool returned an error result: %s", resultText(r))
	}
	body := resultText(r)
	for _, field := range []string{`"limit"`, `"offset"`, `"returned"`, `"has_more"`} {
		if !strings.Contains(body, field) {
			t.Errorf("response is missing %s — a truncated page the caller cannot detect is "+
				"worse than an uncapped one: %s", field, body)
		}
	}
	var page listPage
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("unmarshal list page: %v (body: %s)", err, body)
	}
	return page
}

func seedProjectsForPaging(t *testing.T, s *Server, n int) {
	t.Helper()
	ctx := context.Background()
	for i := range n {
		if _, err := s.gtd.CreateProject(ctx, gtd.CreateProjectParams{
			Name:  fmt.Sprintf("rowcap-proj-%04d", i),
			Title: fmt.Sprintf("Row cap project %04d", i),
			Area:  "engineering",
		}); err != nil {
			t.Fatalf("CreateProject %d: %v", i, err)
		}
	}
}

func seedGoalsForPaging(t *testing.T, s *Server, n int) {
	t.Helper()
	ctx := context.Background()
	for i := range n {
		if _, err := s.gtd.CreateGoal(ctx, gtd.CreateGoalParams{
			Title: fmt.Sprintf("Row cap goal %04d", i),
			Area:  "career",
		}); err != nil {
			t.Fatalf("CreateGoal %d: %v", i, err)
		}
	}
}

func seedProposalsForPaging(t *testing.T, s *Server, n int) {
	t.Helper()
	ctx := context.Background()
	for i := range n {
		payload := fmt.Appendf(nil, `{"title":"Row cap goal %04d","area":"career"}`, i)
		if _, err := s.proposal.Create(ctx, proposal.CreateParams{
			Type:       proposal.TypeGoal,
			Payload:    payload,
			ProposedBy: "rowcap-test",
		}); err != nil {
			t.Fatalf("proposal.Create %d: %v", i, err)
		}
	}
}

// assertPayloadCeiling is the "回應 chars 有天花板" half of the acceptance
// criterion, expressed against the thing the tool USED to return rather than
// against a magic number: the capped response must be a fraction of the
// unpaged serialisation, which is what an uncapped list tool put into the
// caller's context window.
//
// A quarter, not a tenth (50/500), because the page carries envelope fields
// the bare array did not — the point is the ceiling exists and scales with the
// page, not the exact ratio.
func assertPayloadCeiling(t *testing.T, tool string, pagedBody string, uncapped any) {
	t.Helper()
	raw, err := json.Marshal(uncapped)
	if err != nil {
		t.Fatalf("marshal uncapped %s rows: %v", tool, err)
	}
	if len(pagedBody) >= len(raw) {
		t.Errorf("%s: paged response is %d chars, the unpaged one is %d — no cap took effect",
			tool, len(pagedBody), len(raw))
	}
	if ceiling := len(raw) / 4; len(pagedBody) > ceiling {
		t.Errorf("%s: paged response is %d chars, above the %d-char ceiling implied by a "+
			"%d-row default page over %d rows", tool, len(pagedBody), ceiling,
			listPageDefaultLimit, rowCapSeedCount)
	}
}

// TestF170_04_ListProjectsRowCap pins list_projects' row cap over a real
// SQLite store holding 500 active projects.
//
// Before this, both the handler and ListActiveProjects' SQL were uncapped, so
// this same call returned all 500 rows — one tool call spending as much of the
// caller's context window as the projects table happened to be worth.
func TestF170_04_ListProjectsRowCap(t *testing.T) {
	s := newTestWorkSessionServer(t)
	seedProjectsForPaging(t, s, rowCapSeedCount)

	r := callListProjects(t, s, map[string]any{})
	page := decodeListPage(t, r)
	if page.Limit != listPageDefaultLimit {
		t.Errorf("limit = %d, want the default %d", page.Limit, listPageDefaultLimit)
	}
	if page.Returned != listPageDefaultLimit {
		t.Errorf("returned = %d, want %d (a full first page of %d rows)",
			page.Returned, listPageDefaultLimit, rowCapSeedCount)
	}
	if !page.HasMore {
		t.Errorf("has_more = false with %d rows behind a %d-row page — the caller is told it has "+
			"seen everything", rowCapSeedCount, listPageDefaultLimit)
	}
	if page.Offset != 0 {
		t.Errorf("offset = %d, want 0 by default", page.Offset)
	}

	uncapped, err := s.gtd.ListActiveProjects(context.Background())
	if err != nil {
		t.Fatalf("ListActiveProjects: %v", err)
	}
	if len(uncapped) != rowCapSeedCount {
		t.Fatalf("fixture seeded %d projects but the store returns %d", rowCapSeedCount, len(uncapped))
	}
	assertPayloadCeiling(t, "list_projects", resultText(r), uncapped)
}

// TestF170_04_ListProjectsClampsAndPages covers the two edges the default-page
// test cannot: a caller asking for more than the maximum, and the last page.
func TestF170_04_ListProjectsClampsAndPages(t *testing.T) {
	s := newTestWorkSessionServer(t)
	seedProjectsForPaging(t, s, rowCapSeedCount)

	// float64, not int: these arguments arrive as decoded JSON, and the seam's
	// own type check rejects a Go int with "limit must be a number".
	clamped := decodeListPage(t, callListProjects(t, s, map[string]any{"limit": float64(10000)}))
	if clamped.Limit != listPageMaxLimit {
		t.Errorf("limit = %d for a requested 10000, want the clamp %d", clamped.Limit, listPageMaxLimit)
	}
	if clamped.Returned != listPageMaxLimit {
		t.Errorf("returned = %d, want the clamped %d", clamped.Returned, listPageMaxLimit)
	}

	last := decodeListPage(t, callListProjects(t, s, map[string]any{"limit": float64(50), "offset": float64(480)}))
	if last.Returned != rowCapSeedCount-480 {
		t.Errorf("returned = %d at offset 480 of %d rows, want %d",
			last.Returned, rowCapSeedCount, rowCapSeedCount-480)
	}
	if last.HasMore {
		t.Error("has_more = true on the final page — the caller will page forever")
	}

	// A negative offset is floored rather than reaching the driver, where
	// Postgres errors and SQLite silently treats it as 0.
	negative := decodeListPage(t, callListProjects(t, s, map[string]any{"offset": float64(-5)}))
	if negative.Offset != 0 {
		t.Errorf("offset = %d for a requested -5, want 0", negative.Offset)
	}
}

// TestF170_04_ListProjectsPagesDoNotOverlap pins the reason the id tiebreaker
// was added to the query: OFFSET paging over a non-total order silently
// repeats and drops rows. Every seeded project here shares one priority and is
// created in the same instant-ish window, which is exactly the tie the old
// ORDER BY could not break.
func TestF170_04_ListProjectsPagesDoNotOverlap(t *testing.T) {
	s := newTestWorkSessionServer(t)
	seedProjectsForPaging(t, s, 120)

	seen := map[string]bool{}
	for offset := 0; offset < 120; offset += 50 {
		r := callListProjects(t, s, map[string]any{"limit": float64(50), "offset": float64(offset)})
		var body struct {
			Projects []struct {
				ID string `json:"id"`
			} `json:"projects"`
		}
		if err := json.Unmarshal([]byte(resultText(r)), &body); err != nil {
			t.Fatalf("unmarshal page at offset %d: %v", offset, err)
		}
		for _, p := range body.Projects {
			if seen[p.ID] {
				t.Errorf("project %s appeared on two pages — the ORDER BY is not a total order, "+
					"so OFFSET paging is unstable", p.ID)
			}
			seen[p.ID] = true
		}
	}
	if len(seen) != 120 {
		t.Errorf("paging over 120 rows yielded %d distinct projects — rows were dropped between "+
			"pages", len(seen))
	}
}

// TestF170_05_ListGoalsRowCap is list_goals' half of the same criterion. Goals
// are the worst case for stable paging: none of these fixtures has a due_date,
// so every row ties under `ORDER BY due_date ASC NULLS LAST`.
func TestF170_05_ListGoalsRowCap(t *testing.T) {
	s := newTestWorkSessionServer(t)
	seedGoalsForPaging(t, s, rowCapSeedCount)

	r := callListGoals(t, s, map[string]any{})
	page := decodeListPage(t, r)
	if page.Limit != listPageDefaultLimit {
		t.Errorf("limit = %d, want the default %d", page.Limit, listPageDefaultLimit)
	}
	if page.Returned != listPageDefaultLimit {
		t.Errorf("returned = %d, want %d", page.Returned, listPageDefaultLimit)
	}
	if !page.HasMore {
		t.Errorf("has_more = false with %d goals behind a %d-row page", rowCapSeedCount, listPageDefaultLimit)
	}

	uncapped, err := s.gtd.ActiveGoals(context.Background())
	if err != nil {
		t.Fatalf("ActiveGoals: %v", err)
	}
	if len(uncapped) != rowCapSeedCount {
		t.Fatalf("fixture seeded %d goals but the store returns %d", rowCapSeedCount, len(uncapped))
	}
	assertPayloadCeiling(t, "list_goals", resultText(r), uncapped)
}

// TestF170_05_ListGoalsClampsAndPages mirrors the projects edge cases.
func TestF170_05_ListGoalsClampsAndPages(t *testing.T) {
	s := newTestWorkSessionServer(t)
	seedGoalsForPaging(t, s, rowCapSeedCount)

	clamped := decodeListPage(t, callListGoals(t, s, map[string]any{"limit": float64(10000)}))
	if clamped.Returned != listPageMaxLimit {
		t.Errorf("returned = %d for a requested 10000, want the clamp %d", clamped.Returned, listPageMaxLimit)
	}

	last := decodeListPage(t, callListGoals(t, s, map[string]any{"limit": float64(50), "offset": float64(480)}))
	if last.Returned != rowCapSeedCount-480 {
		t.Errorf("returned = %d at offset 480, want %d", last.Returned, rowCapSeedCount-480)
	}
	if last.HasMore {
		t.Error("has_more = true on the final page")
	}
}

// TestF170_06_ListPendingProposalsRowCap is the third tool's half. Pending
// proposals carry a whole JSON payload each, so this was the largest per-row
// uncapped list on the surface.
func TestF170_06_ListPendingProposalsRowCap(t *testing.T) {
	s := newTestWorkSessionServer(t)
	seedProposalsForPaging(t, s, rowCapSeedCount)

	r := callListPendingProposalsContract(t, s)
	page := decodeListPage(t, r)
	if page.Limit != listPageDefaultLimit {
		t.Errorf("limit = %d, want the default %d", page.Limit, listPageDefaultLimit)
	}
	if page.Returned != listPageDefaultLimit {
		t.Errorf("returned = %d, want %d", page.Returned, listPageDefaultLimit)
	}
	if !page.HasMore {
		t.Errorf("has_more = false with %d pending proposals behind a %d-row page",
			rowCapSeedCount, listPageDefaultLimit)
	}

	uncapped, err := s.proposal.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(uncapped) != rowCapSeedCount {
		t.Fatalf("fixture seeded %d proposals but the store returns %d", rowCapSeedCount, len(uncapped))
	}
	assertPayloadCeiling(t, "list_pending_proposals", resultText(r), uncapped)
}

// TestF170_06_ListPendingProposalsClampsAndPages mirrors the other two tools'
// edge cases, invoked through the raw-request handler (list_pending_proposals
// is not seam-routed, so its limit/offset arrive as float64 in the arguments
// map — a different decode path from the two above, and one that would fail
// independently).
func TestF170_06_ListPendingProposalsClampsAndPages(t *testing.T) {
	s := newTestWorkSessionServer(t)
	seedProposalsForPaging(t, s, rowCapSeedCount)

	call := func(args map[string]any) *mcpmsg.CallToolResult {
		t.Helper()
		req := mcpmsg.CallToolRequest{}
		req.Params.Arguments = args
		res, err := s.handleListPendingProposals(context.Background(), req)
		if err != nil {
			t.Fatalf("handleListPendingProposals: %v", err)
		}
		return res
	}

	clamped := decodeListPage(t, call(map[string]any{"limit": float64(10000)}))
	if clamped.Returned != listPageMaxLimit {
		t.Errorf("returned = %d for a requested 10000, want the clamp %d", clamped.Returned, listPageMaxLimit)
	}

	last := decodeListPage(t, call(map[string]any{"limit": float64(50), "offset": float64(480)}))
	if last.Returned != rowCapSeedCount-480 {
		t.Errorf("returned = %d at offset 480, want %d", last.Returned, rowCapSeedCount-480)
	}
	if last.HasMore {
		t.Error("has_more = true on the final page")
	}
}

// pagingSpyGTDStore records the limit/offset each paged call receives, and
// flags any call to the UNBOUNDED variants.
type pagingSpyGTDStore struct {
	noopGTDStore
	projectLimit, projectOffset int32
	goalLimit, goalOffset       int32
	calledUnbounded             []string
}

func (s *pagingSpyGTDStore) ActiveProjectsPage(_ context.Context, limit, offset int32) ([]db.Project, error) {
	s.projectLimit, s.projectOffset = limit, offset
	return nil, nil
}

func (s *pagingSpyGTDStore) ActiveGoalsPage(_ context.Context, limit, offset int32) ([]db.Goal, error) {
	s.goalLimit, s.goalOffset = limit, offset
	return nil, nil
}

func (s *pagingSpyGTDStore) ListActiveProjects(context.Context) ([]db.Project, error) {
	s.calledUnbounded = append(s.calledUnbounded, "ListActiveProjects")
	return nil, nil
}

func (s *pagingSpyGTDStore) ActiveGoals(context.Context) ([]db.Goal, error) {
	s.calledUnbounded = append(s.calledUnbounded, "ActiveGoals")
	return nil, nil
}

type pagingSpyProposalStore struct {
	*stubProposalStore
	limit, offset   int32
	calledUnbounded bool
}

func (s *pagingSpyProposalStore) ListPendingPage(_ context.Context, limit, offset int32) ([]db.PendingProposal, error) {
	s.limit, s.offset = limit, offset
	return nil, nil
}

func (s *pagingSpyProposalStore) ListPending(context.Context) ([]db.PendingProposal, error) {
	s.calledUnbounded = true
	return nil, nil
}

// TestF170_04_ListHandlersAskTheStoreForOneMorePage pins the WIRING, which no
// other test in this file can see.
//
// Found by mutation: swapping handleListProjects' ActiveProjectsPage call for
// the unbounded ListActiveProjects leaves every other assertion here green,
// because the handler slices to `limit` in Go afterwards — the client sees the
// same 50 rows and the same has_more either way. What changes is that the
// database ships the whole table into process memory, which is half of what
// [F170-04] is for and was, until this test, entirely unprobed.
//
// limit+1 rather than limit is the has_more probe: asking for exactly `limit`
// makes a full page indistinguishable from the last page.
func TestF170_04_ListHandlersAskTheStoreForOneMorePage(t *testing.T) {
	gtdSpy := &pagingSpyGTDStore{}
	propSpy := &pagingSpyProposalStore{stubProposalStore: &stubProposalStore{}}
	s := &Server{gtd: gtdSpy, proposal: propSpy}
	s.MCPServer() // register tool specs so seam() can decode args

	if r := callListProjects(t, s, map[string]any{}); r.IsError {
		t.Fatalf("list_projects failed: %s", resultText(r))
	}
	if gtdSpy.projectLimit != listPageDefaultLimit+1 {
		t.Errorf("list_projects asked the store for limit=%d, want %d (default page + 1 to detect "+
			"has_more)", gtdSpy.projectLimit, listPageDefaultLimit+1)
	}

	if r := callListGoals(t, s, map[string]any{"limit": float64(30), "offset": float64(7)}); r.IsError {
		t.Fatalf("list_goals failed: %s", resultText(r))
	}
	if gtdSpy.goalLimit != 31 || gtdSpy.goalOffset != 7 {
		t.Errorf("list_goals asked the store for limit=%d offset=%d, want 31/7",
			gtdSpy.goalLimit, gtdSpy.goalOffset)
	}

	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = map[string]any{"limit": float64(200), "offset": float64(400)}
	if _, err := s.handleListPendingProposals(context.Background(), req); err != nil {
		t.Fatalf("handleListPendingProposals: %v", err)
	}
	if propSpy.limit != listPageMaxLimit+1 || propSpy.offset != 400 {
		t.Errorf("list_pending_proposals asked the store for limit=%d offset=%d, want %d/400",
			propSpy.limit, propSpy.offset, listPageMaxLimit+1)
	}

	if len(gtdSpy.calledUnbounded) > 0 {
		t.Errorf("a paged list handler called the UNBOUNDED store method(s) %v — the row cap then "+
			"exists only in Go, and the query still ships the whole table", gtdSpy.calledUnbounded)
	}
	if propSpy.calledUnbounded {
		t.Error("list_pending_proposals called the unbounded ListPending — same defect as above")
	}
}

// TestF170_04_ListToolDescriptionsDoNotPromiseEverything is the
// Excessive-Agency / description-drift check the dispatch calls for: the three
// tools used to say "all active projects" / "all active goals" / "all
// proposals", which after paging would tell a caller it had seen the whole set
// when it had seen fifty rows. It also pins that adding limit/offset did not
// turn a read-only tool's description into something that invites writes.
// TestF170_13_ListPageBoundsClampsWithinInt32 pins the clamp at the function
// that now carries it in its TYPE.
//
// [F170-13] listPageBounds returns int32 so the three call sites can pass the
// result straight to the store with no narrowing conversion — gosec G115 was
// right to flag those conversions, because nothing in the old `int` signature
// said the value was bounded. The bound is only a type guarantee while this
// clamp holds, so it gets a direct test rather than relying on the handler
// tests to notice: they seed 500 rows and would still look correct if the
// clamp let a larger page through, as long as the fixture ran out first.
//
// Revert either clamp branch and this goes red immediately.
func TestF170_13_ListPageBoundsClampsWithinInt32(t *testing.T) {
	cases := []struct {
		name                 string
		rawLimit, rawOffset  int32
		wantLimit, wantOffet int32
	}{
		{"absent means default", 0, 0, listPageDefaultLimit, 0},
		{"negative limit means default", -1, 0, listPageDefaultLimit, 0},
		{"in range is kept", 30, 7, 30, 7},
		{"at the cap is kept", listPageMaxLimit, 0, listPageMaxLimit, 0},
		{"over the cap is clamped", listPageMaxLimit + 1, 0, listPageMaxLimit, 0},
		{"far over the cap is clamped", 10000, 0, listPageMaxLimit, 0},
		{"MaxInt32 is clamped", math.MaxInt32, 0, listPageMaxLimit, 0},
		{"negative offset floors at zero", 50, -5, 50, 0},
		{"MinInt32 offset floors at zero", 50, math.MinInt32, 50, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limit, offset := listPageBounds(tc.rawLimit, tc.rawOffset)
			if limit != tc.wantLimit || offset != tc.wantOffet {
				t.Errorf("listPageBounds(%d, %d) = (%d, %d), want (%d, %d)",
					tc.rawLimit, tc.rawOffset, limit, offset, tc.wantLimit, tc.wantOffet)
			}
			// The whole point of the int32 return: callers pass limit+1 with no
			// conversion, so that expression must not be able to wrap.
			if limit+1 <= 0 {
				t.Errorf("limit+1 = %d wrapped or went non-positive — the store would be asked "+
					"for a nonsense page size", limit+1)
			}
			if limit > listPageMaxLimit {
				t.Errorf("limit %d exceeds the cap %d", limit, listPageMaxLimit)
			}
		})
	}
}

func TestF170_04_ListToolDescriptionsDoNotPromiseEverything(t *testing.T) {
	s := newTestWorkSessionServer(t)
	registered := s.MCPServer().ListTools()
	for _, name := range []string{"list_projects", "list_goals", "list_pending_proposals"} {
		tool, ok := registered[name]
		if !ok {
			t.Fatalf("%s is not registered — the registration moved and this test is now "+
				"checking nothing", name)
		}
		desc := tool.Tool.Description
		lower := strings.ToLower(desc)
		for _, arg := range []string{"limit", "offset"} {
			if _, ok := tool.Tool.InputSchema.Properties[arg]; !ok {
				t.Errorf("%s does not advertise a %q argument, so a caller cannot page at all",
					name, arg)
			}
		}
		for _, banned := range []string{"returns all", "lists all"} {
			if strings.Contains(lower, banned) {
				t.Errorf("%s description still promises everything (%q): %q", name, banned, desc)
			}
		}
		if !strings.Contains(lower, "has_more") {
			t.Errorf("%s description does not tell the caller how to detect a further page: %q",
				name, desc)
		}
		for _, write := range []string{"delete", "modif", "overwrit"} {
			if strings.Contains(lower, write) {
				t.Errorf("%s is read-only but its description uses write language (%q): %q",
					name, write, desc)
			}
		}
	}
}
