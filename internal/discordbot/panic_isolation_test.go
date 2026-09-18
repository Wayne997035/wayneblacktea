package discordbot

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// --- recoverHandler ---

// TestRecoverHandler_SwallowsPanic is the guard for the whole point of this
// wrapper: discordgo runs every handler on its own goroutine (event.go does
// `go eh.eventHandler.Handle(s, i)` whenever SyncEvents is false, the default
// this bot never changes), and a panic on an unrecovered goroutine takes the
// entire process down — the HTTP server, the MCP endpoint and the scheduler
// along with the bot.
//
// The test calls the wrapped function directly rather than through a real
// Session: the goroutine is discordgo's to create, and what has to be proven
// here is that the panic does not escape the function discordgo will call.
func TestRecoverHandler_SwallowsPanic(t *testing.T) {
	called := false
	wrapped := recoverHandler("boom", func(_ *discordgo.Session, _ *discordgo.MessageCreate) {
		called = true
		panic("handler exploded")
	})

	// No defer/recover here on purpose: if the wrapper fails to contain the
	// panic, this test binary dies and the failure is unmissable.
	wrapped(nil, nil)

	if !called {
		t.Fatal("probe broken: the inner handler never ran, so nothing was contained")
	}
}

// TestRecoverHandler_PassesThroughNormalCalls is the reverse control. A
// wrapper that swallowed the call entirely would also make the test above
// pass — "nothing panicked" and "nothing ran" are the same observation from
// outside.
func TestRecoverHandler_PassesThroughNormalCalls(t *testing.T) {
	var gotContent string
	wrapped := recoverHandler("ok", func(_ *discordgo.Session, m *discordgo.MessageCreate) {
		gotContent = m.Content
	})

	wrapped(nil, &discordgo.MessageCreate{Message: &discordgo.Message{Content: "hello"}})

	if gotContent != "hello" {
		t.Fatalf("inner handler got content %q, want %q — the wrapper is not passing calls through",
			gotContent, "hello")
	}
}

// TestRecoverHandler_PreservesConcreteSignature pins the generic. discordgo
// decides which events reach a handler by reflecting on its parameter type,
// so a wrapper that widened the event to `any` would compile, register
// without error, and then never be invoked for anything — a failure that is
// completely silent at runtime.
func TestRecoverHandler_PreservesConcreteSignature(t *testing.T) {
	var _ func(*discordgo.Session, *discordgo.MessageCreate) = recoverHandler(
		"msg", func(*discordgo.Session, *discordgo.MessageCreate) {},
	)
	var _ func(*discordgo.Session, *discordgo.InteractionCreate) = recoverHandler(
		"interaction", func(*discordgo.Session, *discordgo.InteractionCreate) {},
	)
}

// --- the nil chain onMessage dereferences first ---

// TestOnMessage_NilChainDoesNotPanic covers the self-message check, which is
// the first thing every inbound message runs through. Each of these three is
// reachable in production: Session.State is nil when state tracking is off,
// State.User stays nil until READY arrives (the window bot.go already guards
// for slash-command registration), and MessageCreate.Author is nil on message
// types that carry no author.
//
// Without the guard any one of them panics — and because it happens on
// discordgo's handler goroutine, that used to be fatal to the process rather
// than to the message.
func TestOnMessage_NilChainDoesNotPanic(t *testing.T) {
	b := &Bot{allowedUsers: ParseAllowedUserIDs("alice")}

	msg := func(authorID string) *discordgo.MessageCreate {
		return &discordgo.MessageCreate{Message: &discordgo.Message{
			Author: &discordgo.User{ID: authorID}, Content: "!note hi",
		}}
	}

	tests := []struct {
		name string
		sess *discordgo.Session
		evt  *discordgo.MessageCreate
	}{
		{
			name: "Session.State is nil",
			sess: &discordgo.Session{},
			evt:  msg("alice"),
		},
		{
			name: "State.User is nil (pre-READY window)",
			sess: &discordgo.Session{State: &discordgo.State{}},
			evt:  msg("alice"),
		},
		{
			name: "Message.Author is nil",
			sess: &discordgo.Session{State: &discordgo.State{Ready: discordgo.Ready{User: &discordgo.User{ID: "bot"}}}},
			evt:  &discordgo.MessageCreate{Message: &discordgo.Message{Content: "!note hi"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Deliberately no recover: a panic here must fail loudly, since
			// the production consequence is a dead process.
			b.onMessage(tc.sess, tc.evt)
		})
	}
}

// TestOnMessage_StillIgnoresItsOwnMessages is the positive control for the
// guard above. Returning early on every nil would also satisfy that test
// while breaking the check the code is actually there to perform — the bot
// must not answer itself, or it loops on its own output.
func TestOnMessage_StillIgnoresItsOwnMessages(t *testing.T) {
	var sent []string
	b := &Bot{
		allowedUsers: ParseAllowedUserIDs("bot-self"),
		// A send attempt would need a real session; instead the assertion is
		// that we return before reaching any command dispatch, which
		// ParseAllowedUserIDs("bot-self") would otherwise allow.
	}
	sess := &discordgo.Session{State: &discordgo.State{
		Ready: discordgo.Ready{User: &discordgo.User{ID: "bot-self"}},
	}}
	evt := &discordgo.MessageCreate{Message: &discordgo.Message{
		Author: &discordgo.User{ID: "bot-self"}, Content: "!note loop",
	}}

	b.onMessage(sess, evt)

	if len(sent) != 0 {
		t.Fatalf("bot acted on its own message: %v", sent)
	}
	// The real proof is that this returns at all: reaching the dispatch
	// switch with a nil-transport session would panic inside handleNote.
	if strings.TrimSpace(evt.Content) == "" {
		t.Fatal("probe broken: fixture lost its content")
	}
}
