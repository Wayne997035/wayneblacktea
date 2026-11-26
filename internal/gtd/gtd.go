package gtd

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

// ProjectStatus represents the lifecycle of a project.
type ProjectStatus string

const (
	ProjectStatusActive    ProjectStatus = "active"
	ProjectStatusCompleted ProjectStatus = "completed"
	ProjectStatusArchived  ProjectStatus = "archived"
	ProjectStatusOnHold    ProjectStatus = "on_hold"
)

// IsValid reports whether the status is a known GoalStatus value.
func (s GoalStatus) IsValid() bool {
	switch s {
	case GoalStatusActive, GoalStatusCompleted, GoalStatusArchived:
		return true
	}
	return false
}

// IsValid reports whether the status is a known ProjectStatus value.
func (s ProjectStatus) IsValid() bool {
	switch s {
	case ProjectStatusActive, ProjectStatusCompleted, ProjectStatusArchived, ProjectStatusOnHold:
		return true
	}
	return false
}

// TaskStatus represents the lifecycle of a task.
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusCompleted  TaskStatus = "completed"
	TaskStatusCancelled  TaskStatus = "cancelled"
)

// GoalStatus represents the lifecycle of a goal.
type GoalStatus string

const (
	GoalStatusActive    GoalStatus = "active"
	GoalStatusCompleted GoalStatus = "completed"
	GoalStatusArchived  GoalStatus = "archived"
)

var (
	// ErrNotFound is returned when a requested entity does not exist.
	ErrNotFound = errors.New("gtd: not found")
	// ErrProjectNotFound is returned by UpdateTask when UpdateTaskParams.Project
	// names a project that does not exist or belongs to another workspace.
	// [F1011-11] It is built with errors.New and does NOT wrap ErrNotFound: a
	// caller that maps errors.Is(err, ErrNotFound) to "task not found" must not
	// report a missing project as a missing task. Stores MUST return this value,
	// never ErrNotFound, for the project check (see ProjectRef for the order).
	ErrProjectNotFound = errors.New("gtd: project not found")
	// ErrConflict is returned on uniqueness violations (e.g. duplicate project name).
	ErrConflict = errors.New("gtd: conflict")
	// ErrInvalidRepoName is returned when a non-empty repo_name fails
	// validator.IsValidRepoName. Store-layer backstop for CreateProject /
	// UpdateProject on both backends — MCP and HTTP callers already validate
	// before reaching the store, but this closes the gap for any other
	// caller (CLI, reconcile, future integrations) that writes projects
	// directly through the store.
	//
	// [F0925-29] It IS validator.ErrInvalidRepoName, not a second sentinel:
	// every store that validates a repo name returns that one value, so
	// errors.Is(err, gtd.ErrInvalidRepoName) and the MCP caller-facing table
	// (tool_errors.go) match a rejection from any domain.
	ErrInvalidRepoName = validator.ErrInvalidRepoName
	// ErrNotImplemented is a sentinel used by test fakes to simulate a
	// storage-layer error that is neither ErrNotFound nor ErrConflict — see
	// TestRestoreProjectTool_NotImplementedIsAnError, which uses it to prove
	// an uncategorised store error is reported as an error, never silently
	// translated into success or into the not-found response. No production
	// code path returns it. Mirrors sqlite.ErrNotImplemented's role in the
	// other backend.
	ErrNotImplemented = errors.New("gtd: not yet implemented")
	// ErrReservedAction is returned by LogActivity (both backends) when
	// action names one of reservedAuditActions (SEC-PR191-02), or contains a
	// Unicode bidi control character that could disguise one [F1124-01]. Fixed
	// string, never echoes the caller's own action value — safe to surface
	// verbatim to an MCP or HTTP caller (internal/mcp/tool_errors.go's
	// callerFacingSentinels, internal/handler/autolog_handler.go's 400
	// branch).
	ErrReservedAction = errors.New("gtd: action name is reserved for audit rows written by delete/restore")
)

// DeletionTombstoneRetention is the single source of truth for the 30-day
// soft-delete retention window (decision 17a1086b): RestoreProject's group
// lookup on both backends rejects anything older than this, the four
// snapshot methods (pgDeleteProjectAdapter/pgDeleteTaskAdapter/
// sqliteDeleteProjectAdapter/sqliteDeleteTaskAdapter) prune expired rows on
// their way in, and cmd/server/main.go's tombstone PrunerSpec uses the same
// constant for its daily sweep — one number, one place, so the lookup
// bound, the snapshot-time prune, and the scheduled prune can never drift
// out of sync with each other (SEC-PR191-01).
const DeletionTombstoneRetention = 30 * 24 * time.Hour

// reservedAuditActions is the closed set of activity_log action values that
// only the delete/restore transactions themselves may write (SEC-PR191-02):
// a row with one of these actions can be trusted to mean a real delete/restore
// happened, which breaks the moment any LogActivity caller — MCP
// log_activity, POST /api/activity, or a direct Store caller — can write an
// indistinguishable-looking row without actually deleting or restoring
// anything. The six in-tx audit writes that legitimately use these values
// (PG: store.go's pgDeleteProjectAdapter/pgDeleteTaskAdapter.
// WriteDeletionAuditLog and RestoreProject; SQLite: gtd.go's twins) call
// CreateActivityLog / INSERT INTO activity_log directly and never go
// through LogActivity, so they are unaffected by this check.
var reservedAuditActions = map[string]bool{
	"project_deleted":  true,
	"task_deleted":     true,
	"project_restored": true,
}

// canonicalAuditAction reduces action to a lowercase, whitespace/format-free
// form for IsReservedAuditAction to compare against reservedAuditActions
// (F191-14):
//  1. sanitize.Notes strips control characters and ANSI escape sequences —
//     an attacker wrapping a reserved name in ESC[31m...ESC[0m or padding it
//     with a raw C1 byte must not slip past the literal-string comparison
//     the old implementation did.
//  2. Unicode NFKD compatibility decomposition splits combining marks off
//     their base letter and folds compatibility variants (fullwidth ASCII,
//     etc.) onto their canonical form.
//  3. A whitelist keeps only runes classified Letter/Number/Punct/Symbol,
//     then additionally drops Other_Default_Ignorable_Code_Point runes and
//     U+2800 (braille blank, which renders invisible but isn't flagged
//     Cf/Mn/ODI) — this is a whitelist of visible-character categories, not
//     an enumerated denylist, because every prior version of this function
//     was bypassed by a category the denylist hadn't enumerated yet.
//  4. The result is folded to lowercase.
func canonicalAuditAction(action string) string {
	return canonFromDecomposed(norm.NFKD.String(sanitize.Notes(action)))
}

// canonFromDecomposed runs steps 3-4 on an already sanitised, NFKD string,
// so the placeholder canon is not truncated a second time by sanitize.Notes
// after NFKD has expanded it [F1125-06].
func canonFromDecomposed(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		// [F1125-03] one shared predicate, so the placeholder pass below can
		// never disagree with what this step drops.
		if droppedByCanon(r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// droppedByCanon reports whether canonicalAuditAction's step 3 discards r:
// anything outside Letter/Number/Punct/Symbol, plus
// Other_Default_Ignorable_Code_Point runes and U+2800 [F1125-03].
func droppedByCanon(r rune) bool {
	if !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsPunct(r) && !unicode.IsSymbol(r) {
		return true
	}
	return unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) || r == 0x2800
}

// spacingCompatToPlaceholder returns action after NFKD with every rune that
// canonicalAuditAction would drop, is not itself a combining mark (Mn/Me),
// and is directly followed by a run of combining marks with at least one
// visible mark in it (visibleMarkFollows) replaced by U+FFFD [F1125-01].
// Compatibility characters such as U+2017 decompose to a space
// plus a visible mark and render as ONE glyph, but canonicalAuditAction
// drops both halves and the canon ends up one rune short; sending a dropped
// character plus a visible mark directly does the same. The placeholder
// stands for the base character's cell: isHomoglyphOf lets it match one name
// character or none (a zero-width base occupies no cell). The
// not-Mn/Me condition [F1125-02] stops U+0385 (space + two marks) from
// getting a second placeholder for its first mark. A run whose marks are all
// invisible renders nothing, so it gets no placeholder either [F1125-05];
// otherwise it would make this canon one rune too long. The stored action is
// never rewritten.
func spacingCompatToPlaceholder(action string) string {
	rs := []rune(norm.NFKD.String(sanitize.Notes(action)))
	for i := 0; i+1 < len(rs); i++ {
		if droppedByCanon(rs[i]) && !unicode.In(rs[i], unicode.Mn, unicode.Me) && visibleMarkFollows(rs[i+1:]) { // [F1125-02]
			rs[i] = '\uFFFD'
		}
	}
	return string(rs)
}

// visibleMarkFollows reports whether the combining-mark run at the start of
// rs contains a mark that renders: one that is neither a Variation_Selector
// nor Other_Default_Ignorable_Code_Point (U+FE0F, U+034F, ...) [F1125-05].
func visibleMarkFollows(rs []rune) bool {
	for _, r := range rs {
		if !unicode.In(r, unicode.Mn, unicode.Me) {
			return false
		}
		if !unicode.Is(unicode.Variation_Selector, r) && !unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) {
			return true
		}
	}
	return false
}

// foldASCIIConfusable maps the common ASCII look-alikes onto the letter
// they imitate [F1124-02]: 'i', '1', '|' -> 'l' (canon is already lowercased,
// so capital I and capital L arrive as 'i' and 'l'), '0' -> 'o'. Any other
// rune is returned unchanged. Only the reserved-name comparison uses this;
// the stored action is never rewritten.
func foldASCIIConfusable(r rune) rune {
	switch r {
	case 'i', '1', '|':
		return 'l'
	case '0':
		return 'o'
	}
	return r
}

// runeLooksLike reports whether canon rune r may stand in for ASCII rune want
// [F1125-04]: equal; or, for ASCII r, equal after foldASCIIConfusable
// (i/1/| -> l, 0 -> o); or, for non-ASCII r, a Latin, Cyrillic, Greek or
// Common rune (Common covers most digit/symbol/emoji code points and U+FFFD).
// Scripts outside those four, e.g. CJK, never match.
func runeLooksLike(r, want rune) bool {
	if r == want {
		return true
	}
	if r < 0x80 {
		return foldASCIIConfusable(r) == foldASCIIConfusable(want) // [F1124-02]
	}
	return unicode.Is(unicode.Latin, r) || unicode.Is(unicode.Cyrillic, r) || unicode.Is(unicode.Greek, r) || unicode.Is(unicode.Common, r)
}

// isHomoglyphOf reports whether canon (already canonicalised) reads as name
// (an ASCII reservedAuditActions key). Each name rune is matched by one canon
// rune via runeLooksLike, except that a name "d" may also be matched by two
// canon runes that look like "c" then "l" [F1125-04], with any number of
// placeholders allowed between the "c" and the "l" [F1126-01]. A U+FFFD in
// canon (the placeholder from spacingCompatToPlaceholder) may match one name
// rune or none [F1125-07]: a zero-width base character plus a visible mark
// draws no extra cell, a wide one draws one, and the placeholder does not
// record which, so both readings are accepted and the number and position of
// skipped placeholders is unlimited. The match is a dynamic programme over two
// tables: ok[i][j] = canon[i:] reads as name[j:]; half[i][j] (only for j where
// name[j] is "d") = the "c" of that "d" has been read, and canon[i:] reads as
// the skippable placeholders, then "l", then name[j+1:]. It costs
// O(len(canon) * len(name)). A canon holding more than 2 * len(name)
// non-placeholder runes cannot match (a name rune consumes at most two of
// them, the "cl" case), so it is rejected before any table is allocated, and
// both tables are cut from one allocation [F1126-02].
func isHomoglyphOf(canon, name []rune) bool {
	n, m := len(canon), len(name)
	solid := 0
	for _, r := range canon {
		if r != '\uFFFD' {
			solid++
		}
	}
	if solid > 2*m { // [F1126-02] each name rune consumes at most two non-placeholder canon runes ("cl")
		return false
	}
	w := m + 1
	flat := make([]bool, 2*(n+1)*w) // [F1126-02] both tables in one allocation
	ok := make([][]bool, n+1)
	half := make([][]bool, n+1)
	for i := range ok {
		ok[i] = flat[2*i*w : (2*i+1)*w]
		half[i] = flat[(2*i+1)*w : (2*i+2)*w]
	}
	ok[n][m] = true
	for i := n - 1; i >= 0; i-- {
		for j := m; j >= 0; j-- {
			if j < m && name[j] == 'd' {
				// [F1126-01] the "c" of a "cl" d has been read: skip placeholders, then read the "l"
				half[i][j] = (canon[i] == '\uFFFD' && half[i+1][j]) || (runeLooksLike(canon[i], 'l') && ok[i+1][j+1])
			}
			ok[i][j] = readsFrom(canon, name, ok, half, i, j)
		}
	}
	return ok[0][0]
}

// readsFrom computes one isHomoglyphOf cell: whether canon[i:] reads as
// name[j:] given the already-computed later cells in ok and half.
func readsFrom(canon, name []rune, ok, half [][]bool, i, j int) bool {
	if canon[i] == '\uFFFD' && ok[i+1][j] { // [F1125-07] placeholder matches nothing
		return true
	}
	if j == len(name) {
		return false
	}
	if runeLooksLike(canon[i], name[j]) && ok[i+1][j+1] {
		return true
	}
	// [F1125-04] a name "d" drawn as "c" + "l"; U+FFFD may stand for either or sit between them.
	return name[j] == 'd' && runeLooksLike(canon[i], 'c') && half[i+1][j]
}

// containsBidiControl reports whether s contains any of the 12 Unicode bidi
// control code points: U+200E, U+200F, U+202A-U+202E, U+2066-U+2069, U+061C
// [F1124-01].
func containsBidiControl(s string) bool {
	for _, r := range s {
		switch {
		case r == 0x200E, r == 0x200F, r == 0x061C:
			return true
		case r >= 0x202A && r <= 0x202E:
			return true
		case r >= 0x2066 && r <= 0x2069:
			return true
		}
	}
	return false
}

// IsReservedAuditAction reports whether action, after canonicalisation
// (canonicalAuditAction), names one of reservedAuditActions exactly or is a
// visual homoglyph of one (isHomoglyphOf) — so "Project_Deleted"
// or " project_deleted " are rejected like the bare literal, and so is a
// string built out of Cyrillic/Greek/fullwidth lookalikes for
// one of the three names (F191-14, P3's "variant bypass" note). Both
// backends' LogActivity call this before writing any row.
//
// An action containing any of the 12 Unicode bidi control code points
// (U+200E/200F, U+202A-202E, U+2066-2069, U+061C) is also reported as
// reserved: those characters are dropped for the comparison but stored, so
// they could make an unreserved string render as a reserved name
// [F1124-01]. Plain RTL text without those code points is unaffected.
//
// ASCII look-alikes are folded before comparing (foldASCIIConfusable: i, 1, |
// for l; 0 for o), so "task_deIeted" is rejected [F1124-02]. Two further
// comparison forms [F1125-01] [F1125-04] [F1125-07]: a character the canon
// drops followed by combining marks of which at least one is visible (a space
// in the U+2017 / U+0385 cases, sent directly or produced by NFKD) is compared
// as a placeholder (spacingCompatToPlaceholder) that matches one name
// character or none, in any number and at any position (isHomoglyphOf), so a
// zero-width or near-zero-width base plus a visible mark cannot hide a
// reserved name; a literal U+FFFD typed by the caller is skippable the same
// way. A reserved name's "d" also matches a "c" + "l" pair of look-alikes
// with any number of placeholders between them ("task_cleleted", Cyrillic
// spellings too).
//
// Residual gaps, accepted: a character that NFKD turns into one bare space
// with no combining mark after it (U+00A0 etc., which render blank) is still
// dropped; other multi-character look-alikes ("|<" for k, "()" for o, "rn"
// for m) are not folded; homoglyphs outside Latin/Cyrillic/Greek/Common (e.g.
// some Armenian letters) are not caught; and glyphs that render blank
// without carrying Other_Default_Ignorable_Code_Point are not caught beyond
// the U+2800 exclusion in droppedByCanon; spacing combining marks (Mc) are
// dropped like other marks, so one that draws a visible glyph on its own is
// not caught unless a visible Mn/Me follows it, including the ten
// Common-script ones (U+1CE1, U+1CF7, U+1D165-U+1D166, U+1D16D-U+1D172;
// U+1D165 draws a vertical stem that reads as l). The full Unicode
// confusables table is deliberately not used.
//
// An accented spelling of a reserved name (e.g. "prôject_dèleted") is also
// rejected, even though the accent is visually distinguishable and not a
// homoglyph: NFKD decomposition splits the accent into a combining mark,
// which step 3 of canonicalAuditAction drops, so the canonical form matches
// the plain name exactly. This is treated as an acceptable false-positive
// rejection for a snake_case identifier action name, not a bypass to fix.
func IsReservedAuditAction(action string) bool {
	// [F1124-01] A bidi control character is dropped (category Cf) for the
	// comparison below but kept in the stored value, so U+202E + a reversed
	// spelling would render as a reserved name. Reject the whole action.
	if containsBidiControl(action) {
		return true
	}
	// [F1125-01] two candidate canons: the plain one, and one where each
	// dropped character + combining-mark run holding a visible mark was first
	// collapsed to a placeholder. [F1125-06] the second skips another
	// sanitize.Notes pass.
	plain := canonicalAuditAction(action)
	spacing := canonFromDecomposed(spacingCompatToPlaceholder(action))
	for _, canon := range []string{plain, spacing} {
		if reservedAuditActions[canon] {
			return true
		}
		canonRunes := []rune(canon)
		for name := range reservedAuditActions {
			if isHomoglyphOf(canonRunes, []rune(name)) {
				return true
			}
		}
	}
	return false
}

// ProjectIDCleanupExemptions is the sole, machine-checked exception list to
// "every table with a project_id column must be cleaned by delete_project"
// (F191-02). A table belongs here only when its project_id records the
// deletion mechanism's OWN history — "which project got deleted" — rather
// than "which project this row belongs to". Everything else is a real
// reference and delete_project MUST clean it; two of those (vision_items,
// procedural_memories) were missed for that reason before F191-01.
//
// This is the ONLY place this list may be edited from. Both backends'
// F191-02 tests read it — never hand-copy it into a test file — so an
// omission here fails loudly instead of silently passing a narrower check.
var ProjectIDCleanupExemptions = map[string]bool{
	// deletion_tombstones.project_id (migration 000080) records which
	// project a snapshot row belonged to at delete time. Clearing it on
	// delete_project would erase the very value restore_project's lookup
	// needs (design 4) — the opposite of what this table exists to do.
	"deletion_tombstones": true,
}

// TaskIDCleanupExemptions is the sole, machine-checked exception list to
// "every column whose name ends in task_id must be cleaned by delete_task"
// [F1124-03], keyed "table.column". A column belongs here only when it
// records the deletion mechanism's OWN history ("which task got deleted")
// rather than "which task this row belongs to". Everything else is a real
// reference and delete_task MUST clean it.
//
// To add an entry: put "table.column": true here with a comment saying why
// clearing it would erase the history the column exists to keep. Both
// backends' derived-schema tests read this map, and fail on an entry that
// no longer exists in the schema ("stale exemption"). Today it is empty:
// deletion_tombstones has entity_id/project_id but no *task_id column
// (migrations/000080_deletion_tombstones.up.sql), so an empty map is the
// correct value.
var TaskIDCleanupExemptions = map[string]bool{}

// CreateProjectParams holds parameters for creating a new project.
type CreateProjectParams struct {
	GoalID      *uuid.UUID // nil → no parent goal
	Name        string
	Title       string
	Description string // empty → NULL
	Area        string // defaults to "projects"
	Priority    int32  // defaults to 3
	RepoName    string // empty → NULL; links project to a VCS repo slug
}

// CreateGoalParams holds parameters for creating a new goal.
type CreateGoalParams struct {
	Title       string
	Description string     // empty → NULL
	Area        string     // empty → NULL
	DueDate     *time.Time // nil → NULL
}

// CreateTaskParams holds parameters for creating a new task.
type CreateTaskParams struct {
	ProjectID    *uuid.UUID // nil → no project
	Title        string
	Description  string     // empty → NULL
	Priority     int32      // defaults to 3
	Assignee     string     // empty → NULL
	DueDate      *time.Time // nil → NULL
	Importance   *int16     // nil → NULL; valid range 1..3 (1=high, 2=med, 3=low)
	Context      string     // empty → NULL; free-form discussion background
	Kind         string     // empty → defaults to "general"; one of general/fix-pr/feature/refactor/research/chore
	BranchName   *string    // nil → NULL; git branch name (migration 000047)
	PRUrl        *string    // nil → NULL; GitHub PR URL (migration 000047)
	VisionItemID *uuid.UUID // nil → NULL; vision item this task was promoted from (migration 000050)
	// Area is the single-valued classification (migration 000079). Empty
	// falls back to "unsorted" rather than erroring: HTTP callers and older
	// code paths must keep working, and a row parked in a visible 'unsorted'
	// bucket is recoverable in a way a rejected write is not. The MCP
	// add_task surface requires it at the schema instead — that is where the
	// callers who can supply it actually live.
	Area string
}

// UpdateGoalParams holds parameters for a full update of a goal.
type UpdateGoalParams struct {
	Title       string
	Description string // empty → NULL
	Area        string // empty → NULL
	Status      GoalStatus
	DueDate     *time.Time // nil → NULL
}

// UpdateProjectParams holds parameters for a full update of a project.
type UpdateProjectParams struct {
	Title       string
	Description string // empty → NULL
	Area        string // defaults to "projects" if empty
	Priority    int32  // defaults to 3 if zero
	Status      ProjectStatus
	GoalID      *uuid.UUID // nil → NULL
	RepoName    *string    // nil → preserve existing; empty string → clear to NULL
}

// ProjectRef is the tri-state target of UpdateTaskParams.Project. [F1011-11]
// A bare *uuid.UUID cannot carry it, because nil would have to mean both
// "leave project_id alone" and "clear it". The three states, and no fourth:
//
//	Project == nil                 omitted: tasks.project_id keeps its stored value
//	Project != nil, ID == nil      clear: tasks.project_id becomes NULL
//	Project != nil, ID != nil      set: tasks.project_id becomes *ID
//
// A store's UpdateTask (Postgres and SQLite alike) MUST decide in this order:
//
//  1. Pre-read the task. An unknown or other-workspace task returns ErrNotFound
//     before anything else is looked at.
//  2. When ID != nil, check the project with GetProjectByID (workspace-scoped).
//     Its ErrNotFound becomes ErrProjectNotFound, never ErrNotFound; any other
//     error is returned as it is.
//  3. Write project_id in the UPDATE with a SQL-side CASE on the tri-state, not
//     through the Go-side field merge, so an update that omits Project never
//     rewrites the column.
//
// A call rejected at step 1 or 2 writes nothing, other fields included.
type ProjectRef struct {
	ID *uuid.UUID
}

// UpdateTaskParams holds parameters for a partial update of a task.
// nil pointer = "don't change this field"; no field is cleared by being nil.
// Project clears through a typed tri-state: a non-nil ProjectRef whose ID is nil.
// Several *string fields, such as Description, Assignee, Context, BranchName and
// PRUrl, clear to NULL when they point at an empty string; the rule for each is in
// mergeTaskUpdateFields and resolveAssigneeForUpdate (gtd/store.go) and in
// mergeTaskBaseFields and mergeTaskPRFields (storage/sqlite/gtd.go).
// All-nil = invalid; callers must set at least one field.
type UpdateTaskParams struct {
	Title       *string
	Description *string
	Priority    *int32
	Importance  *int16
	Assignee    *string
	DueDate     *time.Time
	Context     *string
	Status      *string
	Kind        *string // nil → preserve existing; set to one of validator.ValidTaskKinds (GTD-c282cc04)
	BranchName  *string // nil → preserve existing; set to update (migration 000047)
	PRUrl       *string // nil → preserve existing; set to update (migration 000047)
	// Area: nil → preserve existing. Applied as COALESCE(new, area) inside the
	// UPDATE itself rather than through the Go-side merge, so omission cannot
	// wash a task back into 'unsorted' — the column is NOT NULL DEFAULT
	// 'unsorted', and any path that wrote the default instead of the stored
	// value would reclassify every task it touched without a trace.
	// Callers MUST validate against task_areas first (TaskAreaExists).
	Area *string
	// Project: nil → preserve existing; non-nil with a nil ID → clear to NULL;
	// non-nil with an ID → set. [F1011-11] The order a store checks these in
	// (task, then project, then the SQL-side write) is documented on ProjectRef.
	Project *ProjectRef
	// AppendCommitSHA: nil → no-op, commit_shas is left untouched. Non-nil →
	// the store atomically appends this single SHA to commit_shas at the SQL
	// layer (array_append in Postgres, json_insert in SQLite) — never a
	// Go-side read-modify-write, which raced under concurrent complete_task
	// calls on the same task (P7, 2026-08-20 mcp-surface-spec, GTD 25537a73
	// follow-up). There is no "replace the whole array" operation; commit_shas
	// is append-only through this API.
	AppendCommitSHA *string
}

// ChecklistItem represents a single item in a task's structured checklist.
// Stored as a JSONB array in Postgres and a JSON TEXT array in SQLite.
type ChecklistItem struct {
	ID          uuid.UUID  `json:"id"`
	Title       string     `json:"title"`
	FileRef     string     `json:"file_ref,omitempty"`
	Done        bool       `json:"done"`
	EvidenceURL string     `json:"evidence_url,omitempty"`
	Notes       string     `json:"notes,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// TaskFilter holds the parameters for the filtered task list query used by
// TasksFiltered. Status "" or "active" means pending + in_progress; "all"
// means every task status; any other exact status value is matched literally.
// Limit and Offset drive pagination; callers should pass Limit+1 to detect
// has_more without a COUNT query.
type TaskFilter struct {
	ProjectID *uuid.UUID
	Status    string
	Limit     int
	Offset    int

	// UpdatedSince, when non-nil, restricts results to rows with
	// updated_at >= *UpdatedSince, applied in the WHERE clause (both PG and
	// SQLite backends) rather than filtered client-side after the LIMIT. Nil
	// (the zero value) preserves prior behaviour for existing callers.
	//
	// wbt-2.0 review round2 F2: detectTaskNoOutcome's ORDER BY priority ASC,
	// created_at ASC + Limit 500 lets a growing total completed-task count
	// silently push recently-completed rows out of the window before the
	// 7-day cutoff filter ever runs in Go. Pushing the cutoff into the WHERE
	// clause makes the LIMIT apply to the already-filtered set, so rows
	// inside the recency window are never dropped by row count alone.
	UpdatedSince *time.Time

	// Area, when non-empty, restricts results to that single task_areas row
	// (migration 000079). Empty means "every area" and preserves the prior
	// behaviour for existing callers. Applied in the WHERE clause, not
	// filtered in Go after the LIMIT — same reasoning as UpdatedSince above:
	// post-LIMIT filtering silently drops matching rows once one area grows
	// past the page size.
	Area string

	// Q, when non-empty, restricts results to tasks whose title contains it
	// as a case-insensitive substring [F0930-20]. Empty means "no filter"
	// and preserves prior behaviour for existing callers. Both backends
	// escape Q via internal/likeescape.Escape before binding it into the
	// LIKE/ILIKE pattern, so a literal %, _ or \ in the caller's query text
	// never acts as a wildcard.
	Q string
}

// TaskIDTitle is FindTaskIDsByPrefix's row projection [F0930-17]: a
// candidate task's id + title, nothing else. Not persisted, not DB-mapped —
// exists purely so the prefix resolver (internal/mcp/task_id_resolver.go)
// and its ambiguous-match error message never need a full db.Task.
type TaskIDTitle struct {
	ID    uuid.UUID
	Title string
}

// AreaCount is one row of the area breakdown: how many tasks sit in a single
// task_areas bucket, split by the two open statuses. It exists so the
// gtd/areas resource can answer "how many are left" without any caller
// paging through task rows — a limit=60 page of list_tasks measured 21,580
// bytes, against 529 bytes for the whole breakdown.
type AreaCount struct {
	Area       string `json:"area"`
	Label      string `json:"label"`
	Pending    int    `json:"pending"`
	InProgress int    `json:"in_progress"`
	Open       int    `json:"open"`
}

// UpdateChecklistItemParams holds the optional fields for patching a checklist item.
// nil pointer = "don't change this field".
type UpdateChecklistItemParams struct {
	Done        *bool
	Title       *string
	Notes       *string
	EvidenceURL *string
}

// PullForwardCap is the maximum number of tasks PullForwardTasks returns.
// Fixed per user decision (2026-07-19): pull-forward is intentionally small so
// it surfaces important-but-not-yet-due work without drowning out today's
// actual due work in the session-start context.
const PullForwardCap = 5

// PullForwardTomorrowStart returns the absolute instant that is midnight
// "tomorrow" in Asia/Taipei relative to refDate. This is the boundary both
// PullForwardTasks backends (internal/gtd/store.go for Postgres,
// internal/storage/sqlite/gtd.go for SQLite) use to decide whether a task is
// due today (excluded — that's today's work, not pull-forward) vs due
// tomorrow or later (eligible). Shared here so the two backends cannot drift.
//
// MUST build the boundary via time.Date on refDate.In(loc), NOT
// refDate.UTC().Truncate(24*time.Hour) — Time.Truncate rounds against the
// absolute duration since the zero time (effectively a UTC-aligned
// operation), not the wall-clock day in the given Location. The old
// UTC-truncation boundary therefore landed at Taipei 08:00, not Taipei
// 00:00 — a task due later THIS Taipei calendar day (e.g. 23:00 Taipei) has
// a UTC due_date that already fell on/after that skewed boundary and was
// wrongly pulled forward as if it were tomorrow's work (production bug,
// confirmed 2026-07-20). Taiwan has no DST, so this was a plain offset bug,
// not a DST edge case. Mirrors the Asia/Taipei source used throughout
// internal/scheduler (see scheduler.go's own time.LoadLocation calls).
func PullForwardTomorrowStart(refDate time.Time) (time.Time, error) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return time.Time{}, fmt.Errorf("PullForwardTomorrowStart: loading Asia/Taipei timezone: %w", err)
	}
	inTaipei := refDate.In(loc)
	y, m, d := inTaipei.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, loc), nil
}

// ChecklistMaxTitle is the maximum length for a checklist item title in runes.
const ChecklistMaxTitle = 500

// ChecklistMaxText is the maximum length for notes/file_ref/evidence_url in runes.
const ChecklistMaxText = 2000

// SanitiseChecklistText strips null bytes and ASCII control characters (except tab)
// from a string, and trims leading/trailing whitespace.
func SanitiseChecklistText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\x00' || (r < 0x20 && r != '\t') {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
