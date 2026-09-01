package gtd

import (
	"errors"
	"fmt"
	"strings"
)

// actorRegistry is the single source of truth for recognized GTD task
// assignee identities. CanonicalActors and actorAliases are both derived
// from this slice at init time, so onboarding a new AI/human executor is a
// one-entry addition here — never a switch-case duplicated across callers.
//
// Ambiguous multi-actor spellings (e.g. "Codex/Claude", "claude/codex") and
// role labels that aren't actors (e.g. "backend") are deliberately absent
// from every entry's aliases — they fall through NormalizeActor's error path
// instead of being silently guessed.
var actorRegistry = []struct {
	canonical string
	aliases   []string // lowercased, trimmed spellings; canonical itself always matches too
}{
	{canonical: "claude", aliases: []string{"claude-code", "claude (engineer dispatch)"}},
	{canonical: "codex", aliases: []string{"codex lead"}},
	{canonical: "human", aliases: nil},
}

// CanonicalActors is the allowlist of normalized assignee values, derived
// from actorRegistry. Used to render the "must be one of" error message.
var CanonicalActors = buildCanonicalActors()

// actorAliases is a flattened lowercase-alias -> canonical lookup, built once
// from actorRegistry.
var actorAliases = buildActorAliases()

func buildCanonicalActors() []string {
	out := make([]string, len(actorRegistry))
	for i, e := range actorRegistry {
		out[i] = e.canonical
	}
	return out
}

func buildActorAliases() map[string]string {
	m := make(map[string]string, len(actorRegistry)*2)
	for _, e := range actorRegistry {
		m[e.canonical] = e.canonical
		for _, a := range e.aliases {
			m[strings.ToLower(strings.TrimSpace(a))] = e.canonical
		}
	}
	return m
}

// ErrInvalidAssignee is the sentinel wrapped by NormalizeActor's returned
// error so callers that need to distinguish "bad input" (400) from an
// unexpected backend failure (500) — e.g. the HTTP handler in
// internal/handler/gtd_handler.go — can test with errors.Is instead of
// matching on the error string.
var ErrInvalidAssignee = errors.New("assignee is not a recognized actor")

// NormalizeActor case/whitespace-normalizes raw and resolves it to a
// canonical actor value from actorRegistry. Callers MUST reject the write on
// error rather than store the raw value — LLM tool input AND HTTP request
// bodies are both adversarial (backend-security-design.md §2.1), and an
// unvalidated assignee corrupts the "who is working on what" audit trail
// multi-AI collaboration depends on. Whitelist match, not blacklist: anything
// not explicitly registered is refused.
//
// Originally enforced only at the MCP tool layer (add_task / update_task,
// P6.6). P6.7 sinks the same call into CreateTask/UpdateTask on both backend
// Store implementations (internal/gtd/store.go, internal/storage/sqlite/gtd.go)
// so begin_task, set_task_status, and the HTTP handler — none of which had
// their own MCP-layer guard — are covered too, along with any caller that
// bypasses the StoreIface entirely (e.g. WithTx transactional callers).
//
// The error message intentionally never echoes raw back to the caller
// (backend-security-design.md §5.4 reason-sanitisation principle applied
// here too: untrusted input must not round-trip into a string that may be
// logged or displayed) — it only lists the allowlist.
func NormalizeActor(raw string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(raw))
	if canonical, ok := actorAliases[key]; ok {
		return canonical, nil
	}
	return "", fmt.Errorf("%w; must be one of: %s", ErrInvalidAssignee, strings.Join(CanonicalActors, ", "))
}

// ErrAssigneeRequiredForInProgress is returned by RequireAssigneeForInProgress
// (and thus by CreateTask/BeginTask/UpdateTaskStatus/UpdateTask on both
// backends) when a task write would leave the row in_progress with no
// resolved assignee.
var ErrAssigneeRequiredForInProgress = errors.New(
	"assignee is required when moving a task to in_progress; set assignee on this call or beforehand",
)

// RequireAssigneeForInProgress enforces that a task cannot be in in_progress
// status without a non-empty assignee — the domain-layer sink (P6.7) of the
// guarantee P6.6 first enforced only inside the update_task MCP tool
// (requireAssigneeForInProgress in internal/mcp/tools_gtd.go). Called from
// BOTH backends' leaf write methods (BeginTask / UpdateTaskStatus /
// UpdateTask), so the guarantee holds for: MCP begin_task / set_task_status /
// update_task, the HTTP handler (routes through the same Store methods), and
// any WithTx transactional caller that bypasses StoreIface.
//
// P6.8 closed a sixth transition path this function itself cannot reach:
// internal/worksession.Store.batchMarkTasksInProgress (and its SQLite twin)
// is a raw batch UPDATE against the tasks table used by start_work and
// confirm_plan — it never calls into gtd.Store, so this function's call
// sites never execute for it. That path now applies the equivalent gate
// directly in SQL (COALESCE-stamp the session's NormalizeActor'd assignee
// onto a task with none, exclude from the flip entirely if neither exists)
// rather than by calling this function — see worksession.CreateParams.Assignee
// and Store.batchMarkTasksInProgress for the parallel implementation. As of
// P6.8 all known in_progress-transition entry points are covered; if a future
// caller adds a seventh, it must be listed here honestly rather than assumed
// covered by "regardless of entry point" phrasing that predates it.
//
// assignee is the value THIS WRITE will actually persist — already merged
// with the existing row when the write doesn't touch assignee (BeginTask and
// UpdateTaskStatus take no assignee argument, so their callers pass the
// existing row's value straight through). This function only checks
// presence, not canonical form: a brand-new value being set THIS call must
// separately pass NormalizeActor before being merged in, but an untouched
// legacy value already sitting in the row is not re-validated here —
// re-validating it would retroactively block tasks whose assignee predates
// the P6.6 allowlist from ever reaching in_progress again.
func RequireAssigneeForInProgress(assignee string, status TaskStatus) error {
	if status != TaskStatusInProgress {
		return nil
	}
	if strings.TrimSpace(assignee) == "" {
		return ErrAssigneeRequiredForInProgress
	}
	return nil
}

// ActorBackfillCandidate is one row under consideration for assignee
// backfill: an opaque identifier (e.g. a task UUID string, for the caller's
// own bookkeeping) plus the existing raw tasks.assignee text.
type ActorBackfillCandidate struct {
	ID  string
	Raw string
}

// ActorBackfillResult classifies one ActorBackfillCandidate: either a safe
// suggested normalization, or a flag that a human must decide manually.
type ActorBackfillResult struct {
	ID                  string
	Raw                 string
	SuggestedActor      string // canonical value; empty when NeedsManualDecision
	NeedsManualDecision bool
	Reason              string // why manual decision is needed; empty when SuggestedActor is set
}

// [F981-02] The pure batch-classifier function that used ActorBackfillCandidate
// / ActorBackfillResult above was removed here: it had zero production callers
// (only its own test called it, and no cmd/ entrypoint ever wired it up) —
// waste-audit finding 08dbd246. The two candidate/result types above are kept
// in place even though they lose their only caller — out of scope for this
// ticket per its non-goals (not touching other exported symbols); a future
// waste-audit ticket may revisit them.
