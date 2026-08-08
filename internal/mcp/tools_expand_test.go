package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// fakeSession is a minimal server.ClientSession so tests can drive the
// per-session tool filter without standing up an HTTP transport.
type fakeSession struct {
	id            string
	notifications chan mcpmsg.JSONRPCNotification
	initialized   bool
}

func newFakeSession(id string) *fakeSession {
	return &fakeSession{
		id:            id,
		notifications: make(chan mcpmsg.JSONRPCNotification, 8),
		initialized:   true,
	}
}

func (f *fakeSession) Initialize()       { f.initialized = true }
func (f *fakeSession) Initialized() bool { return f.initialized }
func (f *fakeSession) NotificationChannel() chan<- mcpmsg.JSONRPCNotification {
	return f.notifications
}
func (f *fakeSession) SessionID() string { return f.id }

// rpcListedTools issues a real tools/list over the JSON-RPC entry point and
// returns the advertised tool names.
func rpcListedTools(t *testing.T, ms *server.MCPServer, ctx context.Context) []string {
	t.Helper()
	resp := ms.HandleMessage(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal tools/list response: %v", err)
	}
	var decoded struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode tools/list response: %v", err)
	}
	if decoded.Error != nil {
		t.Fatalf("tools/list returned error: %s", decoded.Error.Message)
	}
	names := make([]string, 0, len(decoded.Result.Tools))
	for _, tool := range decoded.Result.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

// rpcCallTool issues a real tools/call and returns the JSON-RPC level error
// message (empty when the call was routed to a handler, whatever that handler
// then decided).
func rpcCallTool(t *testing.T, ms *server.MCPServer, ctx context.Context, name string, args string) string {
	t.Helper()
	if args == "" {
		args = "{}"
	}
	req := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":%q,"arguments":%s}}`,
		name, args,
	)
	resp := ms.HandleMessage(ctx, json.RawMessage(req))
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal tools/call response for %s: %v", name, err)
	}
	var decoded struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode tools/call response for %s: %v", name, err)
	}
	if decoded.Error == nil {
		return ""
	}
	return decoded.Error.Message
}

// rpcCallToolText runs a tools/call and returns the text content of a successful
// tool result. Fails the test if the call errored at either level.
func rpcCallToolText(t *testing.T, ms *server.MCPServer, ctx context.Context, name, args string) string {
	t.Helper()
	if args == "" {
		args = "{}"
	}
	req := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":%q,"arguments":%s}}`,
		name, args,
	)
	resp := ms.HandleMessage(ctx, json.RawMessage(req))
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal %s response: %v", name, err)
	}
	var decoded struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode %s response: %v", name, err)
	}
	if decoded.Error != nil {
		t.Fatalf("%s returned JSON-RPC error: %s", name, decoded.Error.Message)
	}
	if len(decoded.Result.Content) == 0 {
		t.Fatalf("%s returned no content", name)
	}
	if decoded.Result.IsError {
		t.Fatalf("%s returned tool error: %s", name, decoded.Result.Content[0].Text)
	}
	return decoded.Result.Content[0].Text
}

// TestZeroExpansionSession_ListsOnlyCore is the visible half of the feature.
func TestZeroExpansionSession_ListsOnlyCore(t *testing.T) {
	_, ms := newTestMCPServer(t)
	ctx := ms.WithContext(context.Background(), newFakeSession("sess-core"))

	got := rpcListedTools(t, ms, ctx)
	want := append([]string(nil), coreToolNames...)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tools/list on a fresh session returned\n  %v\nwant\n  %v", got, want)
	}
	t.Logf("fresh session sees %d of %d registered tools", len(got), len(ms.ListTools()))
}

// TestAllTools_CallableWhenHidden is the acceptance condition the whole design
// rests on: mcp-go applies tool filters ONLY in handleListTools, so every tool
// the filter hides is still reachable through tools/call. If mcp-go ever
// changed that, this test breaks loudly instead of the feature silently
// amputating 74 tools.
func TestAllTools_CallableWhenHidden(t *testing.T) {
	_, ms := newTestMCPServer(t)
	ctx := ms.WithContext(context.Background(), newFakeSession("sess-hidden"))

	registered := ms.ListTools()
	visible := map[string]bool{}
	for _, name := range rpcListedTools(t, ms, ctx) {
		visible[name] = true
	}

	names := make([]string, 0, len(registered))
	for name := range registered {
		names = append(names, name)
	}
	sort.Strings(names)

	hidden := 0
	for _, name := range names {
		if !visible[name] {
			hidden++
		}
		// Arguments are deliberately empty: we are asserting ROUTING, not
		// business logic. A required-argument failure comes back as a tool
		// result, not as a JSON-RPC "tool not found" error.
		if msg := rpcCallTool(t, ms, ctx, name, "{}"); strings.Contains(msg, "not found") {
			t.Errorf("tool %q was not routable while hidden from tools/list: %s", name, msg)
		}
	}
	if hidden == 0 {
		t.Fatal("no tool was hidden — the filter did not run, so this test proved nothing")
	}
	t.Logf("all %d registered tools stayed callable; %d of them were hidden from tools/list",
		len(names), hidden)
}

func TestFilterToolsForSession_FailsOpenWithoutSession(t *testing.T) {
	_, ms := newTestMCPServer(t)

	// No client session in context — the stdio/in-process case.
	got := rpcListedTools(t, ms, context.Background())
	if len(got) != len(ms.ListTools()) {
		t.Errorf("without a client session tools/list returned %d tools, want all %d (fail open)",
			len(got), len(ms.ListTools()))
	}

	// A session with an empty ID is equally unkeyable.
	ctx := ms.WithContext(context.Background(), newFakeSession(""))
	if got := rpcListedTools(t, ms, ctx); len(got) != len(ms.ListTools()) {
		t.Errorf("with an empty session ID tools/list returned %d tools, want all %d",
			len(got), len(ms.ListTools()))
	}
}

func TestExpandTools_CatalogueIsReadOnly(t *testing.T) {
	srv, ms := newTestMCPServer(t)
	sess := newFakeSession("sess-catalogue")
	ctx := ms.WithContext(context.Background(), sess)

	text := rpcCallToolText(t, ms, ctx, expandToolsName, "{}")
	var got expandCatalogueResponse
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode catalogue: %v", err)
	}
	if got.Mode != "catalogue" {
		t.Errorf("mode = %q, want catalogue", got.Mode)
	}
	if len(got.Groups) != len(toolGroups) {
		t.Errorf("catalogue listed %d groups, want %d", len(got.Groups), len(toolGroups))
	}
	total := 0
	for _, g := range got.Groups {
		if g.ToolCount != len(g.Tools) {
			t.Errorf("group %q reports %d tools but lists %d", g.Name, g.ToolCount, len(g.Tools))
		}
		total += g.ToolCount
	}
	if total != len(ms.ListTools())-1 {
		t.Errorf("catalogue covers %d tools, server registers %d besides expand_tools",
			total, len(ms.ListTools())-1)
	}
	if len(got.ExpandedGroups) != 0 {
		t.Errorf("fresh session reports expanded groups %v", got.ExpandedGroups)
	}

	// Zero state change: nothing recorded, nothing notified, list unchanged.
	if n := srv.toolExpansions().size(); n != 0 {
		t.Errorf("catalogue call recorded %d sessions, want 0", n)
	}
	if len(sess.notifications) != 0 {
		t.Errorf("catalogue call sent %d notifications, want 0", len(sess.notifications))
	}
	if got := rpcListedTools(t, ms, ctx); len(got) != len(coreToolNames) {
		t.Errorf("catalogue call changed tools/list to %d tools", len(got))
	}
}

func TestExpandTools_RevealsGroupWithSchemas(t *testing.T) {
	srv, ms := newTestMCPServer(t)
	sess := newFakeSession("sess-expand")
	ctx := ms.WithContext(context.Background(), sess)

	text := rpcCallToolText(t, ms, ctx, expandToolsName, `{"group":"procedural"}`)
	var got expandResultResponse
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode expand result: %v", err)
	}
	if got.Mode != "expanded" || got.Group != "procedural" {
		t.Errorf("mode/group = %q/%q, want expanded/procedural", got.Mode, got.Group)
	}
	wantRevealed := toolGroupIndex["procedural"]
	if len(got.RevealedTools) != len(wantRevealed) {
		t.Fatalf("revealed %d tools, want %d", len(got.RevealedTools), len(wantRevealed))
	}
	for _, tool := range got.RevealedTools {
		// The response must be self-sufficient: a client that ignores
		// notifications/tools/list_changed still gets a usable schema.
		if tool.Description == "" {
			t.Errorf("revealed tool %q carries no description", tool.Name)
		}
		if tool.InputSchema.Type == "" {
			t.Errorf("revealed tool %q carries no input schema", tool.Name)
		}
	}

	if len(sess.notifications) != 1 {
		t.Fatalf("expected exactly 1 tools/list_changed notification, got %d", len(sess.notifications))
	}
	if n := <-sess.notifications; n.Method != mcpmsg.MethodNotificationToolsListChanged {
		t.Errorf("notification method = %q, want %q", n.Method, mcpmsg.MethodNotificationToolsListChanged)
	}

	visible := rpcListedTools(t, ms, ctx)
	if len(visible) != len(coreToolNames)+len(wantRevealed) {
		t.Errorf("after expanding procedural tools/list has %d tools, want %d",
			len(visible), len(coreToolNames)+len(wantRevealed))
	}

	// Idempotent: expanding the same group again reveals nothing new.
	text = rpcCallToolText(t, ms, ctx, expandToolsName, `{"group":"procedural"}`)
	var again expandResultResponse
	if err := json.Unmarshal([]byte(text), &again); err != nil {
		t.Fatalf("decode second expand: %v", err)
	}
	if len(again.RevealedTools) != 0 {
		t.Errorf("re-expanding revealed %d tools, want 0", len(again.RevealedTools))
	}
	if n := srv.toolExpansions().size(); n != 1 {
		t.Errorf("store holds %d sessions after two calls from one session, want 1", n)
	}
}

func TestExpandTools_SessionIsolation(t *testing.T) {
	_, ms := newTestMCPServer(t)
	ctxA := ms.WithContext(context.Background(), newFakeSession("sess-a"))
	ctxB := ms.WithContext(context.Background(), newFakeSession("sess-b"))

	rpcCallToolText(t, ms, ctxA, expandToolsName, `{"group":"watchdog"}`)

	visibleA := rpcListedTools(t, ms, ctxA)
	if len(visibleA) != len(coreToolNames)+len(toolGroupIndex["watchdog"]) {
		t.Errorf("session A sees %d tools after expanding watchdog", len(visibleA))
	}
	if got := rpcListedTools(t, ms, ctxB); len(got) != len(coreToolNames) {
		t.Errorf("session B sees %d tools, want the untouched core set of %d",
			len(got), len(coreToolNames))
	}
}

func TestExpandTools_AllThenReset(t *testing.T) {
	_, ms := newTestMCPServer(t)
	sess := newFakeSession("sess-all")
	ctx := ms.WithContext(context.Background(), sess)
	registered := len(ms.ListTools())

	text := rpcCallToolText(t, ms, ctx, expandToolsName, `{"group":"all"}`)
	var all expandResultResponse
	if err := json.Unmarshal([]byte(text), &all); err != nil {
		t.Fatalf("decode all: %v", err)
	}
	if len(all.RevealedTools) != registered-len(coreToolNames) {
		t.Errorf(`group="all" revealed %d tools, want %d`,
			len(all.RevealedTools), registered-len(coreToolNames))
	}
	if got := rpcListedTools(t, ms, ctx); len(got) != registered {
		t.Errorf(`after group="all" tools/list has %d tools, want %d`, len(got), registered)
	}

	text = rpcCallToolText(t, ms, ctx, expandToolsName, `{"group":"reset"}`)
	var reset expandResultResponse
	if err := json.Unmarshal([]byte(text), &reset); err != nil {
		t.Fatalf("decode reset: %v", err)
	}
	if reset.Mode != "reset" || len(reset.ExpandedGroups) != 0 {
		t.Errorf("reset returned mode=%q expanded=%v", reset.Mode, reset.ExpandedGroups)
	}
	if got := rpcListedTools(t, ms, ctx); len(got) != len(coreToolNames) {
		t.Errorf("after reset tools/list has %d tools, want the core %d", len(got), len(coreToolNames))
	}
}

func TestExpandTools_ArgumentErrors(t *testing.T) {
	_, ms := newTestMCPServer(t)
	ctx := ms.WithContext(context.Background(), newFakeSession("sess-bad"))

	tests := []struct {
		name    string
		args    string
		wantSub string
	}{
		{"unknown group rejected by the derived enum", `{"group":"nope"}`, "must be one of"},
		{"reserved-looking but unregistered group", `{"group":"ALL"}`, "must be one of"},
		{"wrong JSON type", `{"group":123}`, "must be a string"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := fmt.Sprintf(
				`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"expand_tools","arguments":%s}}`,
				tc.args,
			)
			resp := ms.HandleMessage(ctx, json.RawMessage(req))
			raw, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(raw), tc.wantSub) {
				t.Errorf("expected %q in response, got: %s", tc.wantSub, raw)
			}
		})
	}
}

// TestExpandTools_WithoutSessionExplainsItself covers the fail-open branch on
// the WRITE path: with no session there is nothing to key state by, so the
// call must say so rather than silently pretend it stored something.
func TestExpandTools_WithoutSession(t *testing.T) {
	srv, ms := newTestMCPServer(t)
	req := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":` +
		`{"name":"expand_tools","arguments":{"group":"gtd"}}}`
	resp := ms.HandleMessage(context.Background(), json.RawMessage(req))
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), "needs a client session") {
		t.Errorf("expected the no-session explanation, got: %s", raw)
	}
	if n := srv.toolExpansions().size(); n != 0 {
		t.Errorf("a session-less expand recorded %d entries, want 0", n)
	}
}

func TestProgressiveDisclosureEnabled(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"unset stays on", "", true},
		{"1 disables", "1", false},
		{"true disables", "true", false},
		{"TRUE disables case-insensitively", "TRUE", false},
		{"on disables", "on", false},
		{"0 leaves it on", "0", true},
		{"unrecognised value leaves it on", "maybe", true},
		{"whitespace around a truthy value still disables", "  yes  ", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(disableProgressiveDisclosureEnvVar, tc.value)
			if got := progressiveDisclosureEnabled(); got != tc.want {
				t.Errorf("progressiveDisclosureEnabled() = %v, want %v (env %q)",
					got, tc.want, tc.value)
			}
		})
	}
}

func TestMCPServer_KillSwitchSkipsFilter(t *testing.T) {
	t.Setenv(disableProgressiveDisclosureEnvVar, "1")
	_, ms := newTestMCPServer(t)
	ctx := ms.WithContext(context.Background(), newFakeSession("sess-killswitch"))

	got := rpcListedTools(t, ms, ctx)
	if len(got) != len(ms.ListTools()) {
		t.Errorf("with the kill switch on tools/list returned %d tools, want all %d",
			len(got), len(ms.ListTools()))
	}
	// expand_tools stays registered so the tool inventory does not depend on
	// the environment.
	if _, ok := ms.ListTools()[expandToolsName]; !ok {
		t.Error("expand_tools must stay registered even with the kill switch on")
	}
}

func TestExpansionStore_TTLExpiry(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	st := newExpansionStore(8, time.Hour, clock)

	st.expand("s1", "gtd")
	if got := st.groupsFor("s1"); !got["gtd"] {
		t.Fatal("expected gtd expanded immediately after the write")
	}

	now = now.Add(2 * time.Hour)
	if got := st.groupsFor("s1"); len(got) != 0 {
		t.Errorf("expired entry still readable: %v", got)
	}
	if st.size() != 1 {
		t.Errorf("read path swept the map (size %d) — sweeping must stay on the write path", st.size())
	}

	// The next write sweeps.
	st.expand("s2", "vision")
	if st.size() != 1 {
		t.Errorf("after a write sweep the store holds %d entries, want 1", st.size())
	}
	if got := st.groupsFor("s2"); !got["vision"] {
		t.Error("the surviving entry lost its group")
	}
}

func TestExpansionStore_EvictsLeastRecentlyExpandedAtCap(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	st := newExpansionStore(3, 24*time.Hour, clock)

	for i := range 3 {
		st.expand(fmt.Sprintf("s%d", i), "gtd")
		now = now.Add(time.Minute)
	}
	if st.size() != 3 {
		t.Fatalf("store holds %d entries, want the cap of 3", st.size())
	}

	st.expand("s-new", "vision")
	if st.size() != 3 {
		t.Errorf("store grew past the cap to %d entries", st.size())
	}
	if got := st.groupsFor("s0"); len(got) != 0 {
		t.Errorf("oldest session s0 survived eviction with %v", got)
	}
	if got := st.groupsFor("s-new"); !got["vision"] {
		t.Error("newest session was not stored")
	}
	if got := st.groupsFor("s2"); !got["gtd"] {
		t.Error("a recently-used session was evicted instead of the oldest")
	}
}

func TestExpansionStore_ConcurrentAccessIsSafe(t *testing.T) {
	st := newExpansionStore(64, time.Hour, time.Now)
	done := make(chan struct{})
	for i := range 8 {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			id := fmt.Sprintf("sess-%d", i)
			for range 50 {
				st.expand(id, "gtd")
				_ = st.groupsFor(id)
				_ = st.size()
			}
			st.reset(id)
		}(i)
	}
	for range 8 {
		<-done
	}
	if st.size() != 0 {
		t.Errorf("all sessions reset but %d entries remain", st.size())
	}
}
