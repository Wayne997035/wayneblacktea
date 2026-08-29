package mcp

import (
	"context"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Progressive tool disclosure — the runtime half. See toolgroups.go for the
// static core/group tables and why hiding a tool never makes it uncallable.

const (
	// expandToolsName is the tool's registered name. Referenced by the
	// registration, the seam key and the group-partition invariant, so it is a
	// constant rather than three string literals that could drift apart.
	expandToolsName = "expand_tools"

	// disableProgressiveDisclosureEnvVar is the operator opt-OUT switch.
	// Default is ON. Set to a truthy value ("1"/"true"/"yes"/"on") to skip
	// installing the tool filter entirely, restoring the pre-feature
	// behaviour where tools/list returns all 92 tools. `expand_tools` stays
	// registered either way so the tool inventory (and every test that
	// enumerates it) does not depend on process environment.
	//
	// Mirrors WBT_DISABLE_AUTO_DECISIONS' explicit opt-out semantics
	// (middleware_decision_proposer.go) — an unrecognised value leaves the
	// feature ON rather than silently disabling it.
	disableProgressiveDisclosureEnvVar = "WBT_DISABLE_PROGRESSIVE_DISCLOSURE"

	// maxExpansionSessions bounds the number of tracked sessions.
	//
	// This is a hard security requirement, not tuning: the streamable-HTTP
	// transport's StatelessGeneratingSessionIdManager.Validate accepts ANY
	// session ID unconditionally (mcp-go server/streamable_http.go:1495-1504),
	// so an authenticated caller can mint unlimited distinct session IDs. An
	// unbounded map here would be a memory-exhaustion vector. When the cap is
	// reached the least-recently-expanded session is evicted; the victim
	// silently falls back to the core set and can re-expand.
	maxExpansionSessions = 512

	// expansionTTL is how long a session's expansion survives without another
	// expand_tools call. Scanned on the write path only.
	expansionTTL = 24 * time.Hour
)

// expansionState is one session's disclosure state.
type expansionState struct {
	// groups holds the expanded group names, or the single reserved key
	// expandGroupAll.
	groups map[string]bool
	// lastSeen is the timestamp of the most recent expand_tools mutation for
	// this session. Reads deliberately do NOT refresh it: the read path
	// (tools/list, once per connection for most clients) must stay
	// RLock-only, and a session that never expands again has nothing worth
	// keeping past the TTL.
	lastSeen time.Time
}

// expansionStore is the process-local map of session ID -> expanded groups.
// Bounded by maxExpansionSessions and expansionTTL.
type expansionStore struct {
	mu       sync.RWMutex
	sessions map[string]*expansionState
	max      int
	ttl      time.Duration
	now      func() time.Time
}

// newExpansionStore builds a store. now is injectable so TTL/eviction can be
// tested without sleeping.
func newExpansionStore(maxSessions int, ttl time.Duration, now func() time.Time) *expansionStore {
	if now == nil {
		now = time.Now
	}
	return &expansionStore{
		sessions: make(map[string]*expansionState),
		max:      maxSessions,
		ttl:      ttl,
		now:      now,
	}
}

// groupsFor returns a copy of sessionID's expanded groups (nil when the
// session has never expanded, or its entry expired but has not been swept
// yet). Read path: RLock only, no TTL scan, no lastSeen write.
func (st *expansionStore) groupsFor(sessionID string) map[string]bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	state, ok := st.sessions[sessionID]
	if !ok {
		return nil
	}
	if st.now().Sub(state.lastSeen) > st.ttl {
		// Expired but not yet swept — treat as absent without upgrading to a
		// write lock. The next write path call removes it.
		return nil
	}
	out := make(map[string]bool, len(state.groups))
	for g := range state.groups {
		out[g] = true
	}
	return out
}

// expand records group as expanded for sessionID and returns the session's
// full group set afterwards. Write path: sweeps expired entries and enforces
// the size cap before inserting.
func (st *expansionStore) expand(sessionID, group string) map[string]bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	st.sweepLocked(now)

	state, ok := st.sessions[sessionID]
	if !ok {
		st.evictLocked()
		state = &expansionState{groups: map[string]bool{}}
		st.sessions[sessionID] = state
	}
	state.groups[group] = true
	state.lastSeen = now

	out := make(map[string]bool, len(state.groups))
	for g := range state.groups {
		out[g] = true
	}
	return out
}

// reset drops sessionID's expansion entirely. Also a write path, so it sweeps.
func (st *expansionStore) reset(sessionID string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sweepLocked(st.now())
	delete(st.sessions, sessionID)
}

// size reports the number of tracked sessions (used by tests and by the
// eviction assertions).
func (st *expansionStore) size() int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return len(st.sessions)
}

// sweepLocked removes entries older than the TTL. Caller holds st.mu.
func (st *expansionStore) sweepLocked(now time.Time) {
	for id, state := range st.sessions {
		if now.Sub(state.lastSeen) > st.ttl {
			delete(st.sessions, id)
		}
	}
}

// evictLocked makes room for one new entry by dropping the least-recently
// expanded sessions until the map is below the cap. Caller holds st.mu.
func (st *expansionStore) evictLocked() {
	for len(st.sessions) >= st.max {
		var oldestID string
		var oldestAt time.Time
		for id, state := range st.sessions {
			if oldestID == "" || state.lastSeen.Before(oldestAt) {
				oldestID, oldestAt = id, state.lastSeen
			}
		}
		if oldestID == "" {
			return
		}
		delete(st.sessions, oldestID)
	}
}

// toolExpansions returns the server's expansion store, creating the default
// one on first use. Servers built by New() and by tests that construct
// &Server{} directly both work; a test may pre-set s.expansions to inject a
// clock or a smaller cap.
func (s *Server) toolExpansions() *expansionStore {
	s.expansionsOnce.Do(func() {
		if s.expansions == nil {
			s.expansions = newExpansionStore(maxExpansionSessions, expansionTTL, time.Now)
		}
	})
	return s.expansions
}

// progressiveDisclosureEnabled reports whether the tools/list filter should be
// installed. Default ENABLED; only an explicit truthy opt-out disables it.
func progressiveDisclosureEnabled() bool {
	raw := strings.TrimSpace(os.Getenv(disableProgressiveDisclosureEnvVar))
	if raw == "" {
		return true
	}
	switch strings.ToLower(raw) {
	// [F170-14] nolint rather than a named constant: these four are one
	// truthy-env-value set that has to be read as a unit, and naming a
	// constant for exactly one of four siblings makes the accepted set harder
	// to see, not easier. The occurrences goconst is counting are not a shared
	// concept — the other two are middleware_decision_proposer.go's identical
	// env switch (a real duplicate, tracked as a follow-up to extract a shared
	// predicate rather than restructure two files in a lint-only round) and a
	// Go predeclared-identifier check in the provenance gate, which is the
	// same spelling by coincidence.
	// The goconst directive lives on the sibling switch in
	// middleware_decision_proposer.go — that is the site goconst reports once
	// the package crosses its occurrence threshold. Keeping a second directive
	// here would be flagged unused by nolintlint.
	case "1", "true", "yes", "on":
		return false
	}
	return true
}

// filterToolsForSession is the server.ToolFilterFunc installed by MCPServer().
// It trims tools/list down to the core set plus whatever groups the calling
// session has expanded.
//
// Fail open, deliberately: with no client session in context (stdio before
// session registration, in-process callers, any transport that does not attach
// one) there is nothing to key expansion state by, so the full list is
// returned — exactly the pre-feature behaviour. A filter that failed closed
// would silently amputate the catalogue for those callers.
func (s *Server) filterToolsForSession(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	sess := server.ClientSessionFromContext(ctx)
	if sess == nil || sess.SessionID() == "" {
		return tools
	}
	expanded := s.toolExpansions().groupsFor(sess.SessionID())
	if expanded[expandGroupAll] {
		return tools
	}
	visible := visibleToolSet(expanded)
	out := make([]mcp.Tool, 0, len(visible))
	for _, t := range tools {
		if visible[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// expandToolsArgs is the decoded argument struct for expand_tools.
type expandToolsArgs struct {
	Group string `mcp:"group"`
}

// expandGroupSummary is one catalogue entry.
type expandGroupSummary struct {
	Name      string   `json:"name"`
	ToolCount int      `json:"tool_count"`
	Tools     []string `json:"tools"`
}

// expandCatalogueResponse is returned when expand_tools is called with no
// group. Purely informational — it mutates nothing.
type expandCatalogueResponse struct {
	Mode           string               `json:"mode"`
	CoreTools      []string             `json:"core_tools"`
	ExpandedGroups []string             `json:"expanded_groups"`
	Groups         []expandGroupSummary `json:"groups"`
	Hint           string               `json:"hint"`
}

// expandResultResponse is returned when a group (or a reserved verb) is
// supplied. RevealedTools carries the COMPLETE mcp.Tool JSON — including input
// schemas — because whether a client re-issues tools/list after a
// notifications/tools/list_changed is client behaviour this server cannot
// guarantee. The response is self-sufficient without a re-fetch.
type expandResultResponse struct {
	Mode           string     `json:"mode"`
	Group          string     `json:"group"`
	ExpandedGroups []string   `json:"expanded_groups"`
	RevealedTools  []mcp.Tool `json:"revealed_tools"`
	Note           string     `json:"note"`
}

const expandToolsNote = "Tools hidden from tools/list remain fully callable by name at any time; " +
	"expansion only changes what the catalogue advertises."

// registerExpandTools registers the expand_tools tool. ms is captured by the
// handler closure so the handler can (a) read the live tool registry to emit
// real schemas and (b) notify this one client that its list changed.
func (s *Server) registerExpandTools(ms *server.MCPServer) {
	s.addTool(ms, mcp.NewTool(
		expandToolsName,
		mcp.WithDescription(
			"Reveals more of this server's tools in tools/list. No argument returns the group "+
				"catalogue and changes nothing. group=\"<name>\" reveals that group for THIS session "+
				"and returns the full JSON schema of every newly revealed tool. group=\"all\" reveals "+
				"everything; group=\"reset\" returns to the core set. Hidden tools are ALWAYS callable "+
				"by name — this only changes what the catalogue advertises.",
		),
		mcp.WithString("group",
			mcp.Description("Group to reveal, or \"all\" / \"reset\". Omit to list the catalogue."),
			mcp.Enum(expandToolsGroupEnum()...)),
	), seam(expandToolsName, func(ctx context.Context, args expandToolsArgs) (*mcp.CallToolResult, error) {
		return s.handleExpandTools(ctx, ms, args)
	}))
}

// handleExpandTools implements the three modes: catalogue (no group), reset,
// and reveal (a real group or "all").
func (s *Server) handleExpandTools(
	ctx context.Context, ms *server.MCPServer, args expandToolsArgs,
) (*mcp.CallToolResult, error) {
	sess := server.ClientSessionFromContext(ctx)
	sessionID := ""
	if sess != nil {
		sessionID = sess.SessionID()
	}

	group := strings.TrimSpace(args.Group)
	if group == "" {
		return jsonText(s.expandCatalogue(sessionID))
	}
	if sessionID == "" {
		return mcp.NewToolResultError(
			"expand_tools needs a client session to remember the expansion; this connection has none. " +
				"Every tool is already visible and callable on such a connection.",
		), nil
	}

	if group == expandGroupReset {
		s.toolExpansions().reset(sessionID)
		s.notifyToolListChanged(ctx, ms, group)
		return jsonText(expandResultResponse{
			Mode:           "reset",
			Group:          group,
			ExpandedGroups: []string{},
			RevealedTools:  []mcp.Tool{},
			Note:           expandToolsNote,
		})
	}

	before := visibleToolSet(s.toolExpansions().groupsFor(sessionID))
	expanded := s.toolExpansions().expand(sessionID, group)
	revealed := revealedTools(ms, before, expanded)
	s.notifyToolListChanged(ctx, ms, group)

	return jsonText(expandResultResponse{
		Mode:           "expanded",
		Group:          group,
		ExpandedGroups: sortedKeys(expanded),
		RevealedTools:  revealed,
		Note:           expandToolsNote,
	})
}

// expandCatalogue builds the read-only group directory.
func (s *Server) expandCatalogue(sessionID string) expandCatalogueResponse {
	groups := make([]expandGroupSummary, 0, len(toolGroups))
	for _, g := range toolGroups {
		groups = append(groups, expandGroupSummary{
			Name:      g.Name,
			ToolCount: len(g.Tools),
			Tools:     g.Tools,
		})
	}
	var expanded []string
	if sessionID != "" {
		expanded = sortedKeys(s.toolExpansions().groupsFor(sessionID))
	}
	if expanded == nil {
		expanded = []string{}
	}
	return expandCatalogueResponse{
		Mode:           "catalogue",
		CoreTools:      coreToolNames,
		ExpandedGroups: expanded,
		Groups:         groups,
		Hint: "Call expand_tools again with group=\"<name>\" to reveal one group, " +
			"\"all\" for every tool, or \"reset\" to return to the core set. " + expandToolsNote,
	}
}

// revealedTools returns the registered mcp.Tool values that became visible by
// going from the `before` visibility set to the `after` group set, sorted by
// name for a deterministic response.
func revealedTools(ms *server.MCPServer, before map[string]bool, after map[string]bool) []mcp.Tool {
	registered := ms.ListTools()
	names := make([]string, 0, len(registered))
	if after[expandGroupAll] {
		for name := range registered {
			if !before[name] {
				names = append(names, name)
			}
		}
	} else {
		for g := range after {
			for _, name := range toolGroupIndex[g] {
				if !before[name] {
					names = append(names, name)
				}
			}
		}
	}
	sort.Strings(names)

	out := make([]mcp.Tool, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		if st, ok := registered[name]; ok && st != nil {
			out = append(out, st.Tool)
		}
	}
	return out
}

// notifyToolListChanged tells THIS client (never all clients — another
// session's visibility did not change) that its catalogue moved. Delivery is
// best-effort: a client that never registered a session, or whose notification
// channel is full, still got the complete schemas in the tool response itself.
//
// The session ID is deliberately NOT logged: on the streamable-HTTP transport
// it is a client-held handle, and hook binaries surface slog output to the
// user's terminal.
func (s *Server) notifyToolListChanged(ctx context.Context, ms *server.MCPServer, group string) {
	if err := ms.SendNotificationToClient(ctx, mcp.MethodNotificationToolsListChanged, nil); err != nil {
		slog.Warn("expand_tools: tools/list_changed notification not delivered",
			"group", group, "error", err)
	}
}

// sortedKeys returns the map's keys in sorted order (empty slice, never nil,
// so the JSON response carries [] instead of null).
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
