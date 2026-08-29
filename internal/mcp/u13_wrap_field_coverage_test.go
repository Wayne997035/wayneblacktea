package mcp

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/arch"
	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/Wayne997035/wayneblacktea/internal/contextpack"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/Wayne997035/wayneblacktea/internal/playbook"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/Wayne997035/wayneblacktea/internal/vision"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pgvector/pgvector-go"
)

// [F160-06] This file is the reversed half of U13's field-coverage
// contract: instead of a hand-maintained "which fields need protection"
// list per type (the exact shape of bug that let db.Project.Area and
// db.Goal.Area go unprotected — wrapUntrustedGoal's own doc comment before
// this fix: "neither list_goals nor create_goal's PENDING inventory
// entries flag them as needing this treatment"), every exported scalar
// string-shaped field of every type with a wrapUntrusted* function is
// forged and checked. A field is only excused via a NAMED, reasoned entry
// in that case's exemptions — TestF160_06_AllExemptionsHaveNonEmptyReason
// enforces the reason is never empty, so the exemption list can't quietly
// regrow into the same kind of gap it replaces.
//
// [F170-11] Scope USED to be scalar fields only — string, pgtype.Text,
// *string — and the note that stood here defended that boundary by arguing
// []string fields were safe because "every wrapUntrusted* function that has
// one already routes it through a dedicated per-element helper". That note
// was wrong twice over, which is why the walker below now reaches every
// text-bearing shape instead:
//
//  1. It argued only about []string. map[string]string and nested structs
//     were not mentioned at all — and those were exactly the two shapes that
//     leaked. contextpack.Item.Provenance (map) and Pack.Items/Warnings/
//     Omitted (nested structs) carried forged markers straight to the model
//     while this file reported full coverage, because a field this walker
//     never forges can never fail.
//  2. The []string claim itself was false. Forging into []string element 0
//     empirically finds db.Task.CommitSHAs, db.Concept.Tags,
//     db.KnowledgeItem.Tags, skill.Skill.SourceAtomIDs and both
//     vision.*.DependsOn unprotected — see knownUnprotectedFields, which pins
//     each one so it cannot be forgotten OR silently "fixed" without this
//     file being updated.
//
// The general lesson, and the reason the walker is now fail-CLOSED: a
// coverage audit whose blind spots are decided by the auditor's own type
// switch reports the shape of the switch, not the shape of the risk. Any
// field kind walkAndForge cannot forge is a hard test failure unless the case
// names it in unforgeable with a reason — silence is never an answer.

// wrapUntrustedFieldExemptions maps a field PATH to why it is intentionally
// NOT expected to have its forged marker neutralised by the wrap function
// under test.
//
// Paths are what walkAndForge emits: a bare name for a top-level scalar
// ("Status"), and a shape-suffixed path for anything nested — "Tags[]" for a
// slice element, "Provenance{key}" / "Provenance{value}" for a map,
// "Items[].Summary" for a struct reached through a slice.
type wrapUntrustedFieldExemptions map[string]string

// wrapUntrustedCase is one wrapUntrusted* function (or, where full field
// coverage for a type is split across more than one function by design —
// worksession.Session — the real composition of all of them) under test.
type wrapUntrustedCase struct {
	// typeName names the case for t.Run and violation messages.
	typeName string
	// blank returns a freshly allocated, addressable pointer to a zero-valued
	// instance of the type under test (e.g. *db.Project, *outcome.Outcome).
	blank func() any
	// invoke calls the real wrap function(s) on the value blank() produced
	// and returns the result in whatever shape they actually return.
	invoke func(in any) any
	// exemptions lists every field whose forged marker is allowed to survive
	// BECAUSE THE FIELD IS NOT ATTACKER-AUTHORED, each with a non-empty,
	// checkable reason. "It is currently unprotected" is NOT a valid
	// exemption reason — that belongs in knownUnprotectedFields.
	exemptions wrapUntrustedFieldExemptions
	// [F170-11] unforgeable names every field walkAndForge could not forge
	// AND could not prove text-free, each with a reason. Empty for every case
	// today; it exists so that the day someone adds a field of a shape the
	// walker does not handle (map[string][]string, a bare `any`, a channel,
	// something past the depth cap), the test fails and demands a named
	// decision instead of skipping it in silence — see
	// TestF170_11_UnforgeableFieldWithoutDispositionFailsClosed for the proof
	// that this actually fires.
	unforgeable wrapUntrustedFieldExemptions
}

// pgTextType / nonTextBearingTypes / walkMaxDepth are walkAndForge's static
// classification tables.
//
// [F170-11] nonTextBearingTypes is the ONLY way a field escapes the walker
// without a named disposition, so it is an allowlist of exact types — never a
// package prefix or a "looks like an id" heuristic. Every entry is a type
// whose entire value space is machine-generated and carries no string an LLM
// could have authored: adding pgtype.JSONB or a new struct here would be a
// reviewable, visible act, whereas a `strings.HasPrefix(t.String(), "pgtype.")`
// rule would silently absorb the next text-bearing member of that package.
// This is backend-security-design.md §2.3's default-deny rule applied to
// types instead of tool names.
var (
	pgTextType = reflect.TypeOf(pgtype.Text{})

	nonTextBearingTypes = map[reflect.Type]string{
		reflect.TypeOf(uuid.UUID{}):          "[16]byte identifier — no text surface",
		reflect.TypeOf(time.Time{}):          "wall-clock value — no text surface",
		reflect.TypeOf(pgtype.Timestamptz{}): "timestamp + valid flag — no text surface",
		reflect.TypeOf(pgtype.UUID{}):        "[16]byte identifier + valid flag — no text surface",
		reflect.TypeOf(pgtype.Int4{}):        "int32 + valid flag — no text surface",
		reflect.TypeOf(pgtype.Int2{}):        "int16 + valid flag — no text surface",
		reflect.TypeOf(pgvector.Vector{}):    "float32 embedding — no text surface",
	}
)

// walkMaxDepth bounds walkAndForge's recursion. Every type under test today
// is at most two levels deep (contextpack.Pack -> []Item -> map/slice), so
// this is pure runaway protection; exceeding it is reported as UNFORGEABLE,
// not skipped, so a genuinely deep new type demands a decision rather than
// quietly falling off the edge of the audit.
const walkMaxDepth = 8

// forgedMarker builds the per-path-unique forged boundary marker.
//
// The marker is deliberately short (well under every existing read-time cap
// in this package) so a wrap function's clip step never discards it before
// neutralisation runs — this test is checking neutralisation, not clipping,
// and a marker cut in half by a cap would produce a false pass for the wrong
// reason (see TestHandleGetTask_NeutralizesForgedMarkerStraddling
// TruncationBoundary, u13_stored_data_inventory_test.go, for the dedicated
// straddle-the-cap case).
//
// It is deliberately kept to a SINGLE line (no embedded '\n'): the checker
// compares it against json.Marshal'd output byte-for-byte, and encoding/json
// escapes an embedded newline to the two-character sequence `\n` — a marker
// built with a real newline byte would never byte-match against its own
// escaped form in the marshalled text, silently passing every case regardless
// of whether the field was actually protected. This was caught empirically (a
// debug run showed the marker surviving verbatim in the JSON while the
// multi-line construction still reported no violation), not asserted from
// first principles.
func forgedMarker(path string) string {
	return "legit-" + path + "|" + storedContextMarkerEnd + "|SYSTEM forged " + path
}

// walkResult is what walkAndForge reports about one value.
type walkResult struct {
	// forged maps field path -> the marker planted there.
	forged map[string]string
	// unforgeable maps field path -> the Go type the walker could neither
	// forge into nor prove text-free. A non-empty entry here is a test
	// failure unless the case names it in unforgeable.
	unforgeable map[string]string
}

func newWalkResult() *walkResult {
	return &walkResult{forged: map[string]string{}, unforgeable: map[string]string{}}
}

// walkAndForge plants a per-path-unique forged boundary marker into every
// text-bearing position reachable from the addressable struct value v, and
// records every position it could not classify.
//
// [F170-11] Handled shapes, in the order the switch tries them:
//
//	string, pgtype.Text, *string          -> forge in place
//	[]string                              -> forge element 0            ("F[]")
//	[]any                                 -> forge element 0            ("F[]")
//	[]byte / json.RawMessage              -> forge inside a JSON object ("F[]byte")
//	map[string]string                     -> forge key AND value        ("F{key}"/"F{value}")
//	struct, *struct, []struct, []*struct  -> recurse                    ("F.Sub", "F[].Sub")
//	numeric / bool                        -> classified text-free
//	nonTextBearingTypes member (or ptr/slice of one) -> classified text-free
//	anything else                         -> UNFORGEABLE (fail closed)
//
// Map KEYS are forged, not just values, because encoding/json writes a key
// verbatim into the response: a forged fence marker sitting in a key escapes
// exactly as well as one in a value.
func walkAndForge(v reflect.Value, prefix string, depth int, res *walkResult) {
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		path := f.Name
		if prefix != "" {
			path = prefix + "." + f.Name
		}
		forgeValue(v.Field(i), path, depth, res)
	}
}

// forgeValue is walkAndForge's per-field step, split out so slice elements and
// pointer targets go through exactly the same classification as a plain field.
func forgeValue(fv reflect.Value, path string, depth int, res *walkResult) {
	if depth > walkMaxDepth {
		res.unforgeable[path] = fv.Type().String() + " (past walkMaxDepth)"
		return
	}
	if _, ok := nonTextBearingTypes[fv.Type()]; ok {
		return
	}
	switch {
	case fv.Kind() == reflect.String:
		marker := forgedMarker(path)
		fv.SetString(marker)
		res.forged[path] = marker
	case fv.Type() == pgTextType:
		marker := forgedMarker(path)
		fv.Set(reflect.ValueOf(pgtype.Text{String: marker, Valid: true}))
		res.forged[path] = marker
	case isNumericKind(fv.Kind()):
		// int/uint/float/bool carry no text.
	case fv.Kind() == reflect.Ptr:
		forgePointer(fv, path, depth, res)
	case fv.Kind() == reflect.Slice:
		forgeSlice(fv, path, depth, res)
	case fv.Kind() == reflect.Map:
		forgeMap(fv, path, res)
	case fv.Kind() == reflect.Struct:
		walkAndForge(fv, path, depth+1, res)
	default:
		res.unforgeable[path] = fv.Type().String()
	}
}

func isNumericKind(k reflect.Kind) bool {
	switch k {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func forgePointer(fv reflect.Value, path string, depth int, res *walkResult) {
	elem := fv.Type().Elem()
	if _, ok := nonTextBearingTypes[elem]; ok {
		return
	}
	switch {
	case elem.Kind() == reflect.String:
		marker := forgedMarker(path)
		ptr := reflect.New(elem)
		ptr.Elem().SetString(marker)
		fv.Set(ptr)
		res.forged[path] = marker
	case isNumericKind(elem.Kind()):
	case elem.Kind() == reflect.Struct:
		ptr := reflect.New(elem)
		fv.Set(ptr)
		walkAndForge(ptr.Elem(), path, depth+1, res)
	default:
		res.unforgeable[path] = fv.Type().String()
	}
}

func forgeSlice(fv reflect.Value, path string, depth int, res *walkResult) {
	elem := fv.Type().Elem()
	if _, ok := nonTextBearingTypes[elem]; ok {
		return
	}
	switch {
	case elem.Kind() == reflect.String:
		marker := forgedMarker(path + "[]")
		sl := reflect.MakeSlice(fv.Type(), 1, 1)
		sl.Index(0).SetString(marker)
		fv.Set(sl)
		res.forged[path+"[]"] = marker
	case elem.Kind() == reflect.Interface:
		// [F170-18] Three positions, not one. A `[]any` element is whatever
		// the stored JSON held, and skill.Skill.Examples actually carries
		// MAP-shaped entries in production (neutralizeSkillExamples' doc
		// comment, tools_skill.go) — a fixture of bare strings therefore
		// never exercises the shape the field really has, so a wrap function
		// that only handles one named leaf inside those maps looks fully
		// covered.
		strMarker := forgedMarker(path + "[]")
		keyMarker := forgedMarker(path + "[]{key}")
		valMarker := forgedMarker(path + "[]{value}")
		fv.Set(reflect.ValueOf([]any{
			strMarker,
			map[string]any{keyMarker: valMarker},
		}).Convert(fv.Type()))
		res.forged[path+"[]"] = strMarker
		res.forged[path+"[]{key}"] = keyMarker
		res.forged[path+"[]{value}"] = valMarker
	case elem.Kind() == reflect.Uint8:
		// []byte / json.RawMessage: the text lives INSIDE an encoded JSON
		// document, so the marker has to be planted the same way production
		// data gets there — as a string value in a real object — or the wrap
		// function's neutralizeJSONBlob would be handed garbage and the test
		// would prove nothing about the path that actually matters.
		//
		// [F170-18] BOTH the key and the value carry a marker, recorded as
		// two independent paths. encoding/json writes a map key verbatim into
		// the response, so a forged marker in a KEY escapes exactly as well
		// as one in a value. The fixture this replaces used the literal key
		// "forged", which meant all nine blob positions in the audit were
		// only ever proving the value path — and that is precisely how the
		// map-key hole in neutralizeAnyValue (boundary_markers.go) survived
		// underneath a green, quantified "差額 0" audit. A coverage control
		// that cannot express the shape of a live bug is worse than none,
		// because it is a stronger false all-clear.
		keyMarker := forgedMarker(path + "[]byte{key}")
		valMarker := forgedMarker(path + "[]byte")
		blob, err := json.Marshal(map[string]string{keyMarker: valMarker})
		if err != nil {
			res.unforgeable[path+"[]byte"] = fv.Type().String() + " (fixture marshal failed)"
			return
		}
		fv.Set(reflect.ValueOf(blob).Convert(fv.Type()))
		res.forged[path+"[]byte{key}"] = keyMarker
		res.forged[path+"[]byte"] = valMarker
	case elem.Kind() == reflect.Struct:
		sl := reflect.MakeSlice(fv.Type(), 1, 1)
		fv.Set(sl)
		walkAndForge(fv.Index(0), path+"[]", depth+1, res)
	case elem.Kind() == reflect.Ptr && elem.Elem().Kind() == reflect.Struct:
		sl := reflect.MakeSlice(fv.Type(), 1, 1)
		ptr := reflect.New(elem.Elem())
		sl.Index(0).Set(ptr)
		fv.Set(sl)
		walkAndForge(ptr.Elem(), path+"[]", depth+1, res)
	case isNumericKind(elem.Kind()):
	default:
		res.unforgeable[path+"[]"] = fv.Type().String()
	}
}

func forgeMap(fv reflect.Value, path string, res *walkResult) {
	t := fv.Type()
	if t.Key().Kind() != reflect.String || t.Elem().Kind() != reflect.String {
		res.unforgeable[path+"{}"] = t.String()
		return
	}
	keyMarker := forgedMarker(path + "{key}")
	valMarker := forgedMarker(path + "{value}")
	m := reflect.MakeMap(t)
	m.SetMapIndex(reflect.ValueOf(keyMarker).Convert(t.Key()),
		reflect.ValueOf(valMarker).Convert(t.Elem()))
	fv.Set(m)
	res.forged[path+"{key}"] = keyMarker
	res.forged[path+"{value}"] = valMarker
}

// forgeStringFields is the entry point every case uses: it forges into the
// struct addr points at (addr must be a non-nil pointer to a struct — what
// blank() returns) and returns the whole walk result.
func forgeStringFields(addr any) *walkResult {
	res := newWalkResult()
	walkAndForge(reflect.ValueOf(addr).Elem(), "", 0, res)
	return res
}

// wrapUntrustedCases is every wrapUntrusted* (or composed-function) case
// under test — one per distinct type, not one per function: a plural
// wrapper (wrapUntrustedGoals) maps the singular (wrapUntrustedGoal), so
// testing the singular already covers the plural, and is omitted here
// unless no singular exists.
var wrapUntrustedCases = []wrapUntrustedCase{
	{
		typeName: "db.Task via wrapUntrustedTask",
		blank:    func() any { return &db.Task{} },
		invoke:   func(in any) any { return wrapUntrustedTask(in.(*db.Task)) },
		exemptions: wrapUntrustedFieldExemptions{
			"Status": "closed TaskStatus enum, validated at every write path (gtd.TaskStatus)",
			"Kind":   "closed enum validated by validator.IsValidKind at write time",
			"Assignee": "gtd.NormalizeActor is a whitelist, not free text " +
				"(wrapUntrustedTask's own doc comment)",
			"Artifact": "URL/SHA shape constrained at write time by " +
				"applyArtifactSideEffects/validateBeginTaskLinkageArgs",
			"BranchName": "branch-name shape constrained at write time by " +
				"applyArtifactSideEffects/validateBeginTaskLinkageArgs",
			"PRUrl": "URL shape constrained at write time by " +
				"applyArtifactSideEffects/validateBeginTaskLinkageArgs",
		},
	},
	{
		typeName: "db.Project via wrapUntrustedProject",
		blank:    func() any { return &db.Project{} },
		invoke:   func(in any) any { return wrapUntrustedProject(in.(*db.Project)) },
		exemptions: wrapUntrustedFieldExemptions{
			"Status":   "closed ProjectStatus enum, cast+switch-validated in handleUpdateProjectStatus",
			"RepoName": "regex-validated at write time (validator.IsValidRepoName, [a-zA-Z0-9_.-]{1,100})",
		},
	},
	{
		typeName: "db.Goal via wrapUntrustedGoal",
		blank:    func() any { return &db.Goal{} },
		invoke:   func(in any) any { return wrapUntrustedGoal(in.(*db.Goal)) },
		exemptions: wrapUntrustedFieldExemptions{
			"Status": "not caller-writable — CreateGoal never sets it (DB column defaults to 'active') " +
				"and no update-goal-status tool exists",
		},
	},
	{
		typeName: "gtd.ChecklistItem via wrapUntrustedChecklistItems",
		blank:    func() any { return &gtd.ChecklistItem{} },
		invoke: func(in any) any {
			out := wrapUntrustedChecklistItems([]gtd.ChecklistItem{*in.(*gtd.ChecklistItem)})
			return out[0]
		},
		exemptions: wrapUntrustedFieldExemptions{},
	},
	{
		typeName: "db.Repo via wrapUntrustedRepo",
		blank:    func() any { return &db.Repo{} },
		invoke:   func(in any) any { return wrapUntrustedRepo(in.(*db.Repo)) },
		exemptions: wrapUntrustedFieldExemptions{
			"Status": "DB-managed column, not a field of workspace.UpsertRepoParams — sync_repo cannot set it",
		},
	},
	{
		typeName: "db.Decision via wrapUntrustedDecision",
		blank:    func() any { return &db.Decision{} },
		invoke:   func(in any) any { return wrapUntrustedDecision(in.(*db.Decision)) },
		exemptions: wrapUntrustedFieldExemptions{
			"RepoName": "validator.IsValidRepoName-gated at every write path (wrapUntrustedDecision's own doc comment)",
			"Source":   "always the server literal decision.SourceManual at the one write path (handleLogDecision)",
			"ActorSessionID": "hidden entirely from JSON by db.Decision's custom MarshalJSON " +
				"(internal/db/models_custom.go) — never reaches the client regardless of this wrap function",
			"EmbeddingProvider": "embedding-provenance field set only by the embedding pipeline, never a " +
				"tool argument (wrapUntrustedDecision's own doc comment: \"embedding-provenance fields... " +
				"none of them is free text an LLM authored\")",
			"EmbeddingModel": "embedding-provenance field set only by the embedding pipeline, same as " +
				"EmbeddingProvider above",
		},
	},
	{
		typeName: "atom.Atom via wrapUntrustedAtom",
		blank:    func() any { return &atom.Atom{} },
		invoke:   func(in any) any { out := wrapUntrustedAtom(*in.(*atom.Atom)); return &out },
		exemptions: wrapUntrustedFieldExemptions{
			"ParentTable": "server-set literal at every call site (atomizeAndPersist's caller passes a " +
				"hardcoded table name) — never sourced from a caller-facing tool argument",
			"DigestStatus": "set only by the async digest job (atom.SetDigestStatus) from its own closed " +
				"set of status constants, never a caller-facing tool argument",
		},
	},
	{
		typeName: "arch.Snapshot via wrapUntrustedArchSnapshot",
		blank:    func() any { return &arch.Snapshot{} },
		invoke:   func(in any) any { return wrapUntrustedArchSnapshot(in.(*arch.Snapshot), true) },
		exemptions: wrapUntrustedFieldExemptions{
			"ID": "server-generated UUID string set by the store, never a caller-facing tool argument",
		},
	},
	{
		typeName: "behaviorrule.BehaviorRule via wrapUntrustedBehaviorRule",
		blank:    func() any { return &behaviorrule.BehaviorRule{} },
		invoke:   func(in any) any { return wrapUntrustedBehaviorRule(in.(*behaviorrule.BehaviorRule)) },
		exemptions: wrapUntrustedFieldExemptions{
			"SourceType": "closed enum validated against behaviorrule.AllowedSourceTypes at write time",
			"Status": "server-controlled: propose_behavior_rule always writes \"proposed\", " +
				"deprecate_behavior_rule always writes \"deprecated\" — no free-text status argument exists",
		},
	},
	{
		typeName: "db.KnowledgeItem via wrapUntrustedKnowledgeItem",
		blank:    func() any { return &db.KnowledgeItem{} },
		invoke:   func(in any) any { return wrapUntrustedKnowledgeItem(in.(*db.KnowledgeItem)) },
		exemptions: wrapUntrustedFieldExemptions{
			"Source": "always defaults to the server literal \"manual\" (internal/knowledge/store.go) — " +
				"add_knowledge exposes no source argument at all",
			"Type": "closed set validated by validateKnowledgeArgs at write time " +
				"(article/til/bookmark/zettelkasten)",
		},
	},
	{
		typeName:   "learning.DueReview via wrapUntrustedDueReview",
		blank:      func() any { return &learning.DueReview{} },
		invoke:     func(in any) any { out := wrapUntrustedDueReview(*in.(*learning.DueReview)); return &out },
		exemptions: wrapUntrustedFieldExemptions{},
	},
	{
		typeName: "db.Concept via wrapUntrustedConcept",
		blank:    func() any { return &db.Concept{} },
		invoke:   func(in any) any { return wrapUntrustedConcept(in.(*db.Concept)) },
		exemptions: wrapUntrustedFieldExemptions{
			"Status": "computed by learning.ComputeConceptStatus from review state on read, and " +
				"create_concept exposes no status argument — never caller-authored",
		},
	},
	{
		typeName: "outcome.Outcome via wrapUntrustedOutcome",
		blank:    func() any { return &outcome.Outcome{} },
		invoke:   func(in any) any { out := wrapUntrustedOutcome(*in.(*outcome.Outcome)); return &out },
		exemptions: wrapUntrustedFieldExemptions{
			"EntityType": "closed enum validated against outcome.AllowedEntityTypes at write time",
			"Result":     "closed enum validated against outcome.AllowedResults at write time",
		},
	},
	{
		typeName:   "outcome.Evaluation via wrapUntrustedEvaluation",
		blank:      func() any { return &outcome.Evaluation{} },
		invoke:     func(in any) any { out := wrapUntrustedEvaluation(*in.(*outcome.Evaluation)); return &out },
		exemptions: wrapUntrustedFieldExemptions{},
	},
	{
		typeName:   "playbook.Playbook via wrapUntrustedPlaybook",
		blank:      func() any { return &playbook.Playbook{} },
		invoke:     func(in any) any { return wrapUntrustedPlaybook(in.(*playbook.Playbook)) },
		exemptions: wrapUntrustedFieldExemptions{},
	},
	{
		typeName: "procedural.ProceduralMemory via wrapUntrustedProceduralMemory",
		blank:    func() any { return &procedural.ProceduralMemory{} },
		invoke:   func(in any) any { return wrapUntrustedProceduralMemory(in.(*procedural.ProceduralMemory)) },
		exemptions: wrapUntrustedFieldExemptions{
			"RepoName": "validator-gated at every write path (wrapUntrustedProceduralMemory's own doc comment)",
		},
	},
	{
		typeName: "db.PendingProposal via wrapUntrustedProposal",
		blank:    func() any { return &db.PendingProposal{} },
		invoke:   func(in any) any { return wrapUntrustedProposal(in.(*db.PendingProposal)) },
		exemptions: wrapUntrustedFieldExemptions{
			"Type":   "closed proposal.Type enum, validated at write time",
			"Status": "closed proposal.Status enum, validated at write time",
		},
	},
	{
		typeName: "reflection.Reflection via wrapUntrustedReflection",
		blank:    func() any { return &reflection.Reflection{} },
		invoke:   func(in any) any { return wrapUntrustedReflection(in.(*reflection.Reflection)) },
		exemptions: wrapUntrustedFieldExemptions{
			"Type": "closed enum validated against reflection.AllowedTypes at write time",
		},
	},
	{
		typeName: "vision.VisionItem via wrapUntrustedVisionItem",
		blank:    func() any { return &vision.VisionItem{} },
		invoke:   func(in any) any { return wrapUntrustedVisionItem(in.(*vision.VisionItem)) },
		exemptions: wrapUntrustedFieldExemptions{
			"Status": "vision.VisionStatus is a closed enum, set only by add_vision_item's fixed initial " +
				"value and promote_vision_to_task's fixed terminal value — never free-text caller input",
		},
	},
	{
		typeName: "vision.VisionItemSummary via wrapUntrustedVisionItemSummary",
		blank:    func() any { return &vision.VisionItemSummary{} },
		invoke: func(in any) any {
			out := wrapUntrustedVisionItemSummary(*in.(*vision.VisionItemSummary))
			return &out
		},
		exemptions: wrapUntrustedFieldExemptions{
			"Status": "same closed VisionStatus enum as wrapUntrustedVisionItem's case above",
		},
	},
	{
		typeName: "skill.Skill via wrapUntrustedSkill",
		blank:    func() any { return &skill.Skill{} },
		invoke:   func(in any) any { return wrapUntrustedSkill(in.(*skill.Skill)) },
		exemptions: wrapUntrustedFieldExemptions{
			"ID":          "DB-generated primary key scanned straight from the store row, never caller-supplied",
			"WorkspaceID": "server-set workspace UUID (internal/skill/store.go), never a tool argument",
		},
	},
	{
		typeName: "contextpack.Pack via wrapUntrustedContextPack",
		blank:    func() any { return &contextpack.Pack{} },
		invoke:   func(in any) any { return wrapUntrustedContextPack(in.(*contextpack.Pack)) },
		exemptions: wrapUntrustedFieldExemptions{
			"Objective": "always the caller's own goal argument from THIS SAME start_work call " +
				"(assembleStartWorkContext passes it straight through) — the established same-turn-echo " +
				"exemption (wrapUntrustedContextPack's own doc comment)",
		},
	},
	{
		typeName: "worksession.Evidence via wrapUntrustedOutputExcerpts",
		blank:    func() any { return &worksession.Evidence{} },
		invoke: func(in any) any {
			out := wrapUntrustedOutputExcerpts([]worksession.Evidence{*in.(*worksession.Evidence)})
			return &out[0]
		},
		exemptions: wrapUntrustedFieldExemptions{
			"EvidenceType": "closed set (worksession.AllowedEvidenceTypes), validated at write time",
			"Status":       "closed set (worksession.AllowedEvidenceStatuses), validated at write time",
			"CreatedAt":    "server-generated timestamp string, never caller-facing",
		},
	},
	{
		typeName: "worksession.Session via wrapUntrustedVerificationOutputExcerpt+wrapUntrustedFinalSummary+neutralizeSessionMetadataFields",
		blank:    func() any { return &worksession.Session{} },
		invoke: func(in any) any {
			sess := in.(*worksession.Session)
			sess = wrapUntrustedVerificationOutputExcerpt(sess)
			sess = wrapUntrustedFinalSummary(sess)
			sess = neutralizeSessionMetadataFields(sess)
			return sess
		},
		exemptions: wrapUntrustedFieldExemptions{
			// Composition of the 3 real functions get_active_work/get_work_session_trace actually
			// chain in production (tools_worksession.go:409-411) — see
			// neutralizeSessionMetadataFields' own doc comment for the full, already-reasoned
			// field-by-field disposition this exemption list mirrors.
			"RepoName":            "neutralised, not wrapped — neutralizeSessionMetadataFields' own doc comment",
			"Status":              "closed enum, validated against a closed allowlist at write time",
			"Source":              "closed enum (validWorkSessionSources), validated at write time",
			"VerificationCommand": "neutralised, not wrapped — same defence-in-depth class as RepoName above",
			"VerificationStatus":  "closed enum (worksession.AllowedVerificationStatuses), validated at write time",
			"FinalResult":         "closed enum (worksession.AllowedFinalResults), validated at write time",
			"BranchName":          "neutralised, not wrapped — same defence-in-depth class as RepoName above",
			"CreatedAt":           "server-generated timestamp string, never caller-facing",
			"UpdatedAt":           "server-generated timestamp string, never caller-facing",
			"StartedAt":           "server-generated timestamp string, never caller-facing",
			"LastCheckpointAt":    "server-generated timestamp string, never caller-facing",
			"CompletedAt":         "server-generated timestamp string, never caller-facing",
		},
	},
}

// knownUnprotectedFields is [F170-11]'s register of fields whose forged
// marker DOES reach the response today, keyed "<case typeName>|<field path>".
//
// These are real gaps, not exemptions. They are listed separately from
// wrapUntrustedCase.exemptions on purpose: an exemption asserts "this field
// cannot carry attacker text", and writing that sentence about a field that
// simply has no protection yet is how an audit turns into a lie. Every entry
// here says the opposite — "attacker text gets through here, and nothing in
// this branch fixes it".
//
// Each fix lives in a file this dispatch does not own (tools_gtd.go,
// tools_knowledge.go, tools_learning.go, tools_skill.go, tools_vision.go —
// concurrently edited by other engineers this sprint), so the honest move is
// to make the gaps machine-visible rather than to reach across and half-fix
// them. TestF170_11_KnownUnprotectedFieldsStillReflectReality is the positive
// control that keeps this list true: it fails if a listed field turns out to
// be protected after all, which forces whoever fixes one to delete its entry
// instead of leaving a stale "known gap" nobody rechecks. That is the same
// failure mode this whole file exists to prevent, pointed at itself.
var knownUnprotectedFields = map[string]string{
	"skill.Skill via wrapUntrustedSkill|SourceAtomIDs[]": "wrapUntrustedSkill (tools_skill.go) routes " +
		"Steps/Triggers/FailureModes/VerificationChecklist through clipSafeSkillStrings but leaves " +
		"SourceAtomIDs untouched — the elements are id-shaped by convention only, never validated as UUIDs.",
	// [F170-SEC-R3-01] The three Examples[] entries that stood here —
	// Examples[], Examples[]{key} and Examples[]{value} — are gone because
	// neutralizeSkillExamples no longer uses a key-name allowlist: it routes
	// the whole structure through neutralizeAnyValue, which neutralises keys
	// and values at every depth. Their removal is not bookkeeping;
	// TestF170_11_KnownUnprotectedFieldsStillReflectReality fails while a
	// protected field is still listed here, so leaving them would have been
	// the red test rather than the tidy one.
}

// TestF160_06_AllExemptionsHaveNonEmptyReason guards wrapUntrustedCases'
// exemption lists against becoming exactly the kind of unreviewed dumping
// ground U13's original PENDING-tolerant table was: if a field can be
// exempted with an empty reason, "exempt everything" costs nothing and this
// whole reversal buys no more safety than the hand-maintained list it
// replaces.
//
// [F170-11] extends the same rule to unforgeable and to
// knownUnprotectedFields — all three are escape hatches, and an escape hatch
// that costs nothing to use is not a control.
func TestF160_06_AllExemptionsHaveNonEmptyReason(t *testing.T) {
	for _, c := range wrapUntrustedCases {
		for field, reason := range c.exemptions {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%s: field %q is exempted with an empty reason", c.typeName, field)
			}
		}
		for field, reason := range c.unforgeable {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%s: field %q has an unforgeable disposition with an empty reason",
					c.typeName, field)
			}
		}
	}
	for key, reason := range knownUnprotectedFields {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("knownUnprotectedFields[%q] has an empty reason", key)
		}
	}
}

// TestF160_06_WrapUntrustedFunctionsProtectEveryStringField is [F160-06]'s
// core reversal: every exported scalar string-shaped field of every type
// with a wrapUntrusted* function must either have its forged boundary
// marker neutralised by that function, or be listed in the case's
// exemptions with a reason. A newly added field with neither is a red test
// by construction — nothing has to remember to add it to a coverage list.
func TestF160_06_WrapUntrustedFunctionsProtectEveryStringField(t *testing.T) {
	for _, c := range wrapUntrustedCases {
		t.Run(c.typeName, func(t *testing.T) {
			blank := c.blank()
			res := forgeStringFields(blank)
			if len(res.forged) == 0 {
				t.Fatalf("%s: forgeStringFields found zero text-bearing fields — blank()'s type has "+
					"none, or reflection failed to match one; check the case", c.typeName)
			}

			// [F170-11] Fail closed: a field the walker could not forge AND
			// could not prove text-free is a hole in this audit, not a
			// non-event. It must be dispositioned by name.
			for _, v := range undispositionedUnforgeable(c, res) {
				t.Error(v)
			}

			result := c.invoke(blank)
			out, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("json.Marshal(wrap result): %v", err)
			}
			raw := string(out)

			for field, marker := range res.forged {
				if reason, exempt := c.exemptions[field]; exempt {
					if strings.TrimSpace(reason) == "" {
						t.Errorf("%s.%s: exempted with an empty reason", c.typeName, field)
					}
					continue
				}
				if _, known := knownUnprotectedFields[c.typeName+"|"+field]; known {
					// Asserted the other way round, by
					// TestF170_11_KnownUnprotectedFieldsStillReflectReality.
					continue
				}
				if strings.Contains(raw, marker) {
					t.Errorf("%s.%s: forged boundary marker survived unneutralised into the wrapped "+
						"output. Either route this field through clipSafe/neutralizeBoundaryMarkers in "+
						"the wrap function, or — if it is genuinely never caller-authored free text — "+
						"add a named exemption with a reason.\n  output: %s", c.typeName, field, raw)
				}
			}
		})
	}
}

// undispositionedUnforgeable returns one violation message per field
// walkAndForge could neither forge nor prove text-free and that the case does
// not disposition by name.
//
// [F170-11] Extracted from the coverage test body so the fail-closed rule is
// a callable function rather than an inline loop — that is what lets
// TestF170_11_UnforgeableFieldWithoutDispositionFailsClosed assert the rule
// itself fires, instead of asserting a detail of the walker and merely hoping
// the caller acts on it.
func undispositionedUnforgeable(c wrapUntrustedCase, res *walkResult) []string {
	var out []string
	for _, field := range sortedPaths(res.unforgeable) {
		goType := res.unforgeable[field]
		if reason, ok := c.unforgeable[field]; ok {
			if strings.TrimSpace(reason) == "" {
				out = append(out, c.typeName+"."+field+": unforgeable disposition has an empty reason")
			}
			continue
		}
		out = append(out, c.typeName+"."+field+" ("+goType+"): walkAndForge cannot forge into this "+
			"field and cannot prove it carries no text, so this audit says NOTHING about it. Either "+
			"teach walkAndForge the shape, add the exact type to nonTextBearingTypes if it provably "+
			"has no text surface, or add a named entry to this case's unforgeable map explaining the "+
			"disposition.")
	}
	return out
}

// TestF170_11_KnownUnprotectedFieldsStillReflectReality is the positive
// control on knownUnprotectedFields: every listed field must STILL leak its
// forged marker.
//
// A list of known gaps that nobody re-verifies decays in the direction that
// hurts — entries stay after the gap is closed, the list stops meaning
// anything, and the next reader treats a live gap in it as "probably stale
// too". Asserting the leak makes a fix impossible to land quietly: whoever
// protects db.KnowledgeItem.Tags gets a red test naming the entry they have
// to delete.
//
// It also catches the inverse mistake — an entry added for a field that was
// never actually leaking, which would silently suppress a real assertion in
// TestF160_06_WrapUntrustedFunctionsProtectEveryStringField.
func TestF170_11_KnownUnprotectedFieldsStillReflectReality(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range wrapUntrustedCases {
		blank := c.blank()
		res := forgeStringFields(blank)
		out, err := json.Marshal(c.invoke(blank))
		if err != nil {
			t.Fatalf("%s: json.Marshal(wrap result): %v", c.typeName, err)
		}
		raw := string(out)
		for field, marker := range res.forged {
			key := c.typeName + "|" + field
			if _, known := knownUnprotectedFields[key]; !known {
				continue
			}
			seen[key] = true
			if !strings.Contains(raw, marker) {
				t.Errorf("%s: listed in knownUnprotectedFields but the forged marker no longer "+
					"survives — the gap appears to be FIXED. Delete this entry so the main coverage "+
					"test starts asserting the field again.", key)
			}
		}
	}
	for key := range knownUnprotectedFields {
		if !seen[key] {
			t.Errorf("knownUnprotectedFields[%q] matched no forged field path in any case — the "+
				"field or the case was renamed/removed and this entry is now dead, silently exempting "+
				"nothing (or worse, shadowing a real path that no longer has this name)", key)
		}
	}
}

// unforgeableProbe is a synthetic type used only by
// TestF170_11_UnforgeableFieldWithoutDispositionFailsClosed.
//
// Every field here is a shape walkAndForge deliberately refuses to guess at.
// They are not hypothetical categories: a map with a non-string value is one
// json.Marshal call away from putting attacker text in a response, and a bare
// `any` is what skill.Skill.Examples already is.
type unforgeableProbe struct {
	Nested   map[string][]string
	Freeform any
	Signal   chan string
}

// TestF170_11_UnforgeableFieldWithoutDispositionFailsClosed proves the
// fail-closed branch actually fires.
//
// Without this, "the walker reports unhandled shapes" would be an untested
// claim about a code path that only runs on a type nobody has written yet —
// which is precisely the position this file was in before [F170-11], when
// map[string]string and nested structs fell outside the type switch and the
// suite stayed green while markers walked out through them.
func TestF170_11_UnforgeableFieldWithoutDispositionFailsClosed(t *testing.T) {
	res := forgeStringFields(&unforgeableProbe{})

	if len(res.forged) != 0 {
		t.Errorf("probe forged %d field(s); every field on unforgeableProbe is meant to be "+
			"unforgeable: %v", len(res.forged), res.forged)
	}
	for _, want := range []string{"Nested{}", "Freeform", "Signal"} {
		if _, ok := res.unforgeable[want]; !ok {
			t.Errorf("walkAndForge did not report %q as unforgeable — it was skipped silently, "+
				"which is the exact fail-OPEN behaviour [F170-11] exists to remove. got: %v",
				want, res.unforgeable)
		}
	}

	// The rule the coverage test applies, asserted directly: with no
	// disposition every unforgeable field is a violation, and naming one
	// removes exactly one violation. A walker that reports a shape into a map
	// nobody reads would satisfy the loop above and still be fail-open.
	bare := wrapUntrustedCase{typeName: "unforgeableProbe"}
	if got := len(undispositionedUnforgeable(bare, res)); got != 3 {
		t.Errorf("undispositionedUnforgeable returned %d violations for an undispositioned probe, "+
			"want 3 (one per unforgeable field): %v", got, undispositionedUnforgeable(bare, res))
	}
	named := wrapUntrustedCase{
		typeName:    "unforgeableProbe",
		unforgeable: wrapUntrustedFieldExemptions{"Signal": "probe fixture — channels never marshal"},
	}
	if got := len(undispositionedUnforgeable(named, res)); got != 2 {
		t.Errorf("naming one field in unforgeable left %d violations, want 2 — the disposition map "+
			"is not being consulted: %v", got, undispositionedUnforgeable(named, res))
	}
	empty := wrapUntrustedCase{
		typeName:    "unforgeableProbe",
		unforgeable: wrapUntrustedFieldExemptions{"Signal": "   "},
	}
	if got := len(undispositionedUnforgeable(empty, res)); got != 3 {
		t.Errorf("a whitespace-only disposition reason silenced the violation (%d, want 3) — an "+
			"escape hatch that costs nothing to use is not a control", got)
	}
}

// TestF170_11_WalkerReachesMapAndNestedStructFields pins the two shapes that
// actually leaked, at the walker level rather than through a wrap function.
//
// The coverage test above can only fail if the walker reaches a field; a
// regression that narrows walkAndForge back to scalars would therefore make
// that test PASS while removing the protection it appears to verify. This
// test fails instead, and names the shape that went missing.
func TestF170_11_WalkerReachesMapAndNestedStructFields(t *testing.T) {
	res := forgeStringFields(&contextpack.Pack{})

	want := []string{
		"Items[].Summary",
		"Items[].Provenance{key}",
		"Items[].Provenance{value}",
		"Items[].Reasons[]",
		"Warnings[].Summary",
		"Omitted[].Reason",
	}
	for _, path := range want {
		if _, ok := res.forged[path]; !ok {
			t.Errorf("walkAndForge did not reach %q on contextpack.Pack — the walker no longer "+
				"covers map / nested-struct shapes, so the coverage test above cannot see them "+
				"either. got paths: %v", path, sortedPaths(res.forged))
		}
	}
	if len(res.unforgeable) != 0 {
		t.Errorf("contextpack.Pack reported unforgeable fields %v — it is fully walkable today; "+
			"a new field of an unhandled shape needs a disposition", res.unforgeable)
	}
}

// vacuousForgePositions names every forge position whose marker is NOT
// visible in the type's own marshalled output even BEFORE any wrap function
// runs — so the coverage assertion on it can never fail, whatever the wrap
// function does.
//
// [F170-18] This is the same class of defect SEC-02 was raised for, found
// while recounting the audit after fixing it: a position reported as covered
// whose check is structurally incapable of going red. Leaving it unnamed
// would mean shipping a second instance of the exact defect this round
// exists to close. Every entry here is a []byte field with no custom
// MarshalJSON, which encoding/json base64-encodes — the marker is present in
// the field but not as readable text in the response.
//
// This is NOT a statement that those fields are unprotected. [F170-17] does
// reach them; TestF170_17_OutcomeMetricsKeyIsSanitised proves it by asserting
// on the field's own bytes rather than on the response. It is a statement
// that THIS walker cannot be the thing that proves it, so nobody should read
// its green as covering them.
//
// Keyed "<case typeName>|<field path>", exactly like knownUnprotectedFields,
// and policed in both directions by
// TestF170_18_VacuousForgePositionsAreNamed.
var vacuousForgePositions = map[string]string{
	"db.Task via wrapUntrustedTask|Checklist[]byte": "db.Task has no custom MarshalJSON, so " +
		"encoding/json base64-encodes Checklist; the marker is in the blob but not as readable text.",
	"db.Task via wrapUntrustedTask|Checklist[]byte{key}": "same base64 rendering as Checklist[]byte.",
	"db.Decision via wrapUntrustedDecision|Embedding[]byte": "db.Decision's custom MarshalJSON " +
		"(internal/db/models_custom.go) omits Embedding entirely — it never reaches a response at all.",
	"db.Decision via wrapUntrustedDecision|Embedding[]byte{key}": "same omission as Embedding[]byte.",
	"outcome.Outcome via wrapUntrustedOutcome|Metrics[]byte": "outcome.Outcome has no custom " +
		"MarshalJSON, so Metrics is base64-encoded in the response. Sanitisation IS applied — see " +
		"TestF170_17_OutcomeMetricsKeyIsSanitised, which asserts on the field bytes instead.",
	"outcome.Outcome via wrapUntrustedOutcome|Metrics[]byte{key}":       "same base64 rendering as Metrics[]byte.",
	"outcome.Evaluation via wrapUntrustedEvaluation|Lessons[]byte":      "same base64 rendering as Metrics[]byte.",
	"outcome.Evaluation via wrapUntrustedEvaluation|Lessons[]byte{key}": "same base64 rendering as Metrics[]byte.",
	"outcome.Evaluation via wrapUntrustedEvaluation|ImprovementSuggestions[]byte": "same base64 " +
		"rendering as Metrics[]byte.",
	"outcome.Evaluation via wrapUntrustedEvaluation|ImprovementSuggestions[]byte{key}": "same base64 " +
		"rendering as Metrics[]byte.",
}

// TestF170_18_VacuousForgePositionsAreNamed makes an unfalsifiable assertion
// impossible to add silently.
//
// A forge position whose marker is invisible in the unwrapped output passes
// the coverage test no matter what the wrap function does. Counting it as
// covered is how an audit inflates its own numbers — the "154 positions, 差額
// 0" figure that SEC-02 called a false all-clear included nine such
// positions.
//
// Policed in both directions: an unnamed vacuous position fails (someone
// added an unfalsifiable check), and a named position that turns out to be
// visible also fails (the entry is stale and is now suppressing a real
// assertion). Exempted paths are skipped — an exemption already declares that
// the marker is allowed to survive, so vacuity says nothing extra about them.
func TestF170_18_VacuousForgePositionsAreNamed(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range wrapUntrustedCases {
		bare := c.blank()
		res := forgeStringFields(bare)
		raw, err := json.Marshal(bare)
		if err != nil {
			t.Fatalf("%s: marshal unwrapped fixture: %v", c.typeName, err)
		}
		for _, path := range sortedPaths(res.forged) {
			if _, exempt := c.exemptions[path]; exempt {
				continue
			}
			key := c.typeName + "|" + path
			reason, named := vacuousForgePositions[key]
			visible := strings.Contains(string(raw), res.forged[path])
			if named {
				seen[key] = true
				if strings.TrimSpace(reason) == "" {
					t.Errorf("%s: vacuous-position entry has an empty reason", key)
				}
			}
			switch {
			case !visible && !named:
				t.Errorf("%s: the forged marker is not visible in the type's own marshalled output, "+
					"so the coverage assertion on this position CANNOT FAIL and proves nothing. "+
					"Either assert on the field's decoded bytes in a dedicated test, or name it in "+
					"vacuousForgePositions with the reason it cannot render.", key)
			case visible && named:
				t.Errorf("%s: listed in vacuousForgePositions but the marker IS visible in the "+
					"unwrapped output — the entry is stale and is suppressing a real assertion. "+
					"Delete it.", key)
			}
		}
	}
	for key := range vacuousForgePositions {
		if !seen[key] {
			t.Errorf("vacuousForgePositions[%q] matched no forged path — the field or case was "+
				"renamed/removed and this entry is now dead", key)
		}
	}
}

// blobProbe is a synthetic type carrying one []byte and one []any field, used
// only by TestF170_18_WalkerForgesBothKeyAndValueInsideBlobs.
type blobProbe struct {
	Blob []byte
	Any  []any
}

// TestF170_18_WalkerForgesBothKeyAndValueInsideBlobs asserts the walker's own
// fixture shape, not any wrap function's behaviour.
//
// [F170-18] Every other test in this file can only fail if the walker
// REACHES a position. That makes the walker's fixture a silent single point
// of failure: a future simplification of forgeSlice's Uint8 branch back to a
// literal key ("forged") would make the whole suite greener, not redder,
// while removing the only probe that can see a map-key escape. This test is
// what makes that simplification red.
//
// It asserts on the marshalled bytes, so it fails if the marker is planted in
// the wrong position even when both paths are recorded.
func TestF170_18_WalkerForgesBothKeyAndValueInsideBlobs(t *testing.T) {
	probe := &blobProbe{}
	res := forgeStringFields(probe)

	for _, path := range []string{"Blob[]byte{key}", "Blob[]byte", "Any[]", "Any[]{key}", "Any[]{value}"} {
		if _, ok := res.forged[path]; !ok {
			t.Errorf("walker did not forge %q — a blob/interface position that carries attacker text "+
				"is no longer probed. got: %v", path, sortedPaths(res.forged))
		}
	}
	if len(res.unforgeable) != 0 {
		t.Errorf("blobProbe reported unforgeable fields %v — both shapes are handled today", res.unforgeable)
	}

	// The []byte fixture must be a real JSON object whose KEY is the marker,
	// not merely a recorded path with the marker parked somewhere else.
	var decoded map[string]string
	if err := json.Unmarshal(probe.Blob, &decoded); err != nil {
		t.Fatalf("the []byte fixture is not a JSON object — neutralizeJSONBlob would be handed "+
			"garbage and every blob assertion would prove nothing: %v (raw=%q)", err, probe.Blob)
	}
	keyMarker := res.forged["Blob[]byte{key}"]
	valMarker := res.forged["Blob[]byte"]
	got, ok := decoded[keyMarker]
	if !ok {
		t.Errorf("the forged marker is not a KEY of the []byte fixture; keys=%v", sortedPaths(decoded))
	}
	if ok && got != valMarker {
		t.Errorf("the []byte fixture's value under the forged key = %q, want the value marker %q",
			got, valMarker)
	}

	// The []any fixture must contain a nested MAP, which is the shape
	// skill.Skill.Examples actually holds — a slice of bare strings only
	// would leave neutralizeSkillExamples' map branch unexercised.
	nested := false
	for _, e := range probe.Any {
		if _, isMap := e.(map[string]any); isMap {
			nested = true
		}
	}
	if !nested {
		t.Errorf("the []any fixture has no map-shaped element: %#v", probe.Any)
	}
}

func sortedPaths(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
