package atom

import (
	"errors"
	"unicode/utf8"
)

// Digest status enum — the single source of truth for the legal values of
// memory_atoms.digest_status. Both backend stores (Postgres: store.go;
// SQLite: internal/storage/sqlite/atom.go) validate every SetDigestStatus
// call against this enum before issuing the UPDATE, so nothing — including a
// prompt-injected MCP tool call carrying an arbitrary string — can write an
// unrecognised status into the column
// (adversarial input handling: treat every stored string as attacker-shaped).
//
// Values mirror the migration COMMENT on the column
// (migrations/000062_memory_atoms_promoted_status.up.sql): 'pending | done |
// failed | consolidated | promoted'. Do not re-declare these literals
// elsewhere — reference these constants instead (G6 decision 8f25c58d).
const (
	// DigestStatusPending is the initial state written by AddAtom, before
	// the LLM atomizer has processed the atom.
	DigestStatusPending = "pending"
	// DigestStatusDone marks an atom whose digestion step (atomization or
	// consolidation) finished successfully.
	DigestStatusDone = "done"
	// DigestStatusFailed marks an atom whose digestion step errored;
	// error_msg carries the failure detail.
	DigestStatusFailed = "failed"
	// DigestStatusConsolidated marks an original atom that the daily
	// atom-consolidation cron (internal/scheduler/atom_consolidation.go)
	// merged into a condensed atom.
	DigestStatusConsolidated = "consolidated"
	// DigestStatusPromoted marks a consolidated atom that was promoted to a
	// pending knowledge proposal, either manually via the
	// promote_atom_to_knowledge MCP tool or automatically by the weekly
	// atom-bridge cron (internal/scheduler/atom_bridge.go).
	DigestStatusPromoted = "promoted"
)

// ErrInvalidDigestStatus is returned by SetDigestStatus when the caller
// supplies a status outside the five-value enum above.
var ErrInvalidDigestStatus = errors.New("atom: invalid digest_status")

// IsValidDigestStatus reports whether status is one of the five legal
// digest_status enum values. Both backend stores' SetDigestStatus call this
// before sending the UPDATE so an adversarial or buggy caller cannot write
// an arbitrary string into digest_status.
func IsValidDigestStatus(status string) bool {
	switch status {
	case DigestStatusPending, DigestStatusDone, DigestStatusFailed, DigestStatusConsolidated, DigestStatusPromoted:
		return true
	default:
		return false
	}
}

// Promotion eligibility gate — shared by the manual promote_atom_to_knowledge
// MCP tool (internal/mcp/tools_atom.go) and the weekly atom-bridge cron job
// (internal/scheduler/atom_bridge.go) so the two promotion paths can never
// silently diverge on quality-gate criteria (G6 decision 8f25c58d).
const (
	// PromotionMinContentRunes is the minimum atom content length, measured
	// in Unicode runes (not bytes, so CJK/emoji content is measured
	// correctly), required for promotion to a knowledge proposal.
	PromotionMinContentRunes = 80
	// PromotionMinTags is the minimum tag count required for promotion.
	PromotionMinTags = 2
)

// PromotionEligibility reports the outcome of the promotion quality gate for
// a candidate atom's content and tags. Both sub-checks are exposed (rather
// than a single bool) so callers can surface a specific error message per
// failing condition, as promote_atom_to_knowledge does; callers that only
// need the combined verdict use Eligible().
type PromotionEligibility struct {
	ContentOK    bool
	ContentRunes int
	TagsOK       bool
	TagCount     int
}

// Eligible reports whether both the content-length and tag-count gates
// passed.
func (e PromotionEligibility) Eligible() bool {
	return e.ContentOK && e.TagsOK
}

// CheckPromotionEligibility evaluates content and tags against the shared
// promotion quality gate: content length >= PromotionMinContentRunes runes
// AND len(tags) >= PromotionMinTags.
func CheckPromotionEligibility(content string, tags []string) PromotionEligibility {
	n := utf8.RuneCountInString(content)
	return PromotionEligibility{
		ContentOK:    n >= PromotionMinContentRunes,
		ContentRunes: n,
		TagsOK:       len(tags) >= PromotionMinTags,
		TagCount:     len(tags),
	}
}
