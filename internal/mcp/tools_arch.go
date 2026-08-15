package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Wayne997035/wayneblacktea/internal/arch"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	maxSlugLen    = 128
	maxSummaryLen = 8000
	maxFileMapRaw = 128 * 1024 // 128 KB

	// maxLastCommitSHAWriteBytes bounds last_commit_sha at write time
	// (validateLastCommitSHA's `len(*sha) >` check, which counts BYTES —
	// Go's len() on a string is always byte length).
	//
	// Value comes from PRODUCTION data, not from the shape of a single git
	// SHA. As of 2026-08-15 (PR #157 round 5, M-R9, security-engineer) the
	// `coverones` row's last_commit_sha stores a 14-repo `name:sha` map —
	// "multi-repo gateway:8a5ad46 user:5a501da kyc:c1b624a ... dev-stack:
	// 8e2c092" — 252 bytes, a multi-repo workspace convention, not a single
	// `git rev-parse HEAD` output. Measured with:
	//
	//   PGSSLROOTCERT=... psql "$DATABASE_URL" -At -c \
	//     "SELECT max(length(last_commit_sha)) FROM project_arch;"
	//
	// 512 gives that row ~2x headroom to grow without hitting another
	// cliff, while staying ~350x below the 180 KB unbounded case m-R7
	// closed (TestWrapUntrustedArchSnapshot_CJKWorstCaseByteBound).
	//
	// The previous value (128) was derived from the FIELD NAME, not from a
	// measurement ("a real SHA is 40 hex chars; git describe rarely exceeds
	// 50") — that reasoning never looked at what was actually stored, so it
	// missed the coverones row entirely: every upsert to it was hard-
	// rejected (never fixed by retrying — the value never got smaller) and
	// every read of it silently dropped 7 of the 15 stored `name:sha`
	// entries. If this bound needs to move again, measure production
	// first — do not re-derive it from the field name.
	//
	// Known trade-off, NOT fixed by this change: raising this cap also
	// raises the storage capacity for m-R11 (last_commit_sha has no self-
	// healing write path in mcpInstructions' forced patch-back list, so a
	// planted payload here persists indefinitely) by the same ~4x this cap
	// grew. That is a tracked, separate finding; this constant only fixes
	// the availability regression production data hit today.
	maxLastCommitSHAWriteBytes = 512

	// maxLastCommitSHAReadRunes bounds last_commit_sha at read time
	// (wrapUntrustedArchSnapshot's clipSafe call, which counts RUNES —
	// clipRunes/truncateRunes both range over `[]rune(s)`, not bytes).
	//
	// This is a SEPARATE constant from maxLastCommitSHAWriteBytes on
	// purpose and must not be merged back into one shared value (m-R10, R4
	// security review): before this split, a single `maxLastCommitSHALen`
	// was used at both call sites with a doc comment claiming a "byte cap
	// ... used at BOTH" — false for the read side, whose actual worst case
	// is up to 4x the documented byte count for astral (4-byte) runes (128
	// runes measured at 515 bytes worst case pre-fix, R4 security review
	// m-R10 / 5.5). Naming the unit in each constant is what makes that
	// class of error impossible to reintroduce silently — a reviewer
	// grepping for `maxLastCommitSHAReadRunes` in a byte-counting context
	// (or vice versa) has a mismatched name to flag, not just a mismatched
	// comment to miss.
	//
	// Set to the SAME numeric value as the write cap (512), not derived
	// independently, so that any value which cleared the write-time byte
	// check also clears the read-time rune check unclipped: byte count is
	// always >= rune count for UTF-8, so a string that fit in 512 bytes has
	// at most 512 runes, and 512 runes is therefore never a tighter bound
	// than the write cap already was. This is what keeps legitimate values
	// byte-for-byte on the way out — see
	// TestWrapUntrustedArchSnapshot_LegitSHAAndSlugByteForByte and
	// TestHandleUpsertProjectArch_ProductionCoveronesShapeRoundTrips.
	//
	// Worst-case OUTPUT size in bytes is therefore up to 512*4=2048 bytes
	// (all-astral-rune content on a pre-existing/legacy row that bypassed
	// the write gate entirely, before it existed) — still ~88x below the
	// 180 KB unbounded case m-R7 closed, so this does not reopen m-R7.
	maxLastCommitSHAReadRunes = 512
)

func (s *Server) registerArchTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"upsert_project_arch",
		mcp.WithDescription(
			"Store or refresh the architecture snapshot for a project. "+
				"Call after reading 3+ internal/ files from a project. "+
				"slug is the repo name (e.g. \"wayneblacktea\"), "+
				"summary is a one-paragraph human-readable architecture description, "+
				"file_map is a JSON object mapping file paths to their purpose, "+
				"last_commit_sha is the current HEAD SHA for staleness detection. "+
				"summary, file_map and last_commit_sha share one patch-semantics rule: "+
				"omit the field entirely to leave the stored value untouched; pass it "+
				"(even an empty value) to explicitly replace the stored value.",
		),
		mcp.WithString("slug", mcp.Description("Repository/project identifier (unique key)"), mcp.Required()),
		mcp.WithString("summary", mcp.Description(
			`Human-readable architecture description. Omit this field entirely to leave `+
				`the stored summary untouched (e.g. when this call is only refreshing `+
				`file_map or last_commit_sha) — pass "" to explicitly clear it, or any `+
				`other string to replace it.`,
		)),
		mcp.WithString("file_map", mcp.Description(
			`JSON object mapping file path to purpose. Omit this field entirely to leave `+
				`the stored file_map untouched (e.g. when you only read a couple of changed `+
				`files and don't have the full picture) — pass "" or "{}" to explicitly `+
				`clear it (both mean the same empty map), or a JSON object to replace it.`,
		)),
		mcp.WithString("last_commit_sha", mcp.Description(
			`Current git HEAD SHA (run git rev-parse HEAD), used for staleness detection. `+
				`Omit this field entirely to leave the stored last_commit_sha untouched `+
				`(e.g. when this call is only refreshing summary or file_map) — pass "" to `+
				`explicitly clear it (get_project_arch's stale field is always false — this `+
				`server does not compute staleness itself; clearing this value just removes `+
				`what a caller would otherwise compare against git rev-parse HEAD), or `+
				`any other string to replace it.`,
		)),
	), s.handleUpsertProjectArch)

	ms.AddTool(mcp.NewTool(
		"get_project_arch",
		mcp.WithDescription(
			"Retrieve the stored architecture snapshot for a project. "+
				"The returned stale field is always false — this server does not compute "+
				"it; compare the returned last_commit_sha with `git rev-parse HEAD` "+
				"yourself to determine whether the snapshot is stale. "+
				"Returns an error when no snapshot has been stored yet.",
		),
		mcp.WithString("slug", mcp.Description("Repository/project identifier"), mcp.Required()),
		mcp.WithBoolean("include_file_map", mcp.Description(
			"Include the file_map in the response (default false). file_map is the largest "+
				"field in this tool's response; call with include_file_map=true only when you "+
				"need the path→purpose index.",
		)),
	), s.handleGetProjectArch)
}

// validateArchSlug applies upsert_project_arch's slug gate: required,
// character-allow-listed, length-bounded. Split out of
// handleUpsertProjectArch to keep that function's cyclomatic complexity
// under the gocyclo threshold (build/.golangci.yml).
//
// slug is a free-text field with no enumeration surface (arch.StoreIface has
// no listing path — GetSnapshot requires an exact-match key), but it
// round-trips unmodified into the not-found error message (see
// handleGetProjectArch below) and, once stored, into every
// wrapUntrustedArchSnapshot response.
//
// statusSlugRe (tools_status.go) is reused rather than duplicated: before
// this change tools_status.go's own comment claimed "Same allow-list as
// upsert_project_arch" while upsert_project_arch had no allow-list at all —
// only a non-empty + length check (R4 dispatch round 3 finding). Reusing the
// same *regexp.Regexp makes that comment true instead of aspirational, and
// is strictly stronger than the control-char rejection this replaced:
// `^[a-zA-Z0-9_\-]+$` excludes every control character AND every other
// injection-relevant character (space, `=`, `/`, quotes, backticks) in one
// check, so a boundary marker like "=== END PROJECT ARCH ===" cannot be
// written into a NEW slug at all. maxSlugLen stays 128 here (NOT
// statusSlugMaxLen=64 — that cap is specific to generate_project_status's
// Haiku prompt budget, unrelated to this field).
func validateArchSlug(slug string) *mcp.CallToolResult {
	if slug == "" {
		return mcp.NewToolResultError("slug is required")
	}
	if !statusSlugRe.MatchString(slug) {
		return mcp.NewToolResultError("slug must match ^[a-zA-Z0-9_-]+$")
	}
	if len(slug) > maxSlugLen {
		return mcp.NewToolResultError(fmt.Sprintf("slug too long (max %d chars)", maxSlugLen))
	}
	return nil
}

// validateLastCommitSHA applies upsert_project_arch's last_commit_sha gate:
// nil (field omitted, patch-semantics preserve) is always valid; a present
// value must be control-char-free and length-bounded. Split out of
// handleUpsertProjectArch for the same gocyclo reason as validateArchSlug
// above.
//
// last_commit_sha had NO control-char or length check before this (M-R6 /
// m-R7, R4 dispatch): it feeds a byte-for-byte equality comparison against
// `git rev-parse HEAD` for staleness, is not covered by mcpInstructions'
// forced patch-back list (server.go), and round-trips into every
// wrapUntrustedArchSnapshot response — so an unvalidated value planted here
// was a standing injection vector with no self-healing write path.
func validateLastCommitSHA(sha *string) *mcp.CallToolResult {
	if sha == nil {
		return nil
	}
	if reason := checkCommandField("last_commit_sha", *sha); reason != "" {
		return mcp.NewToolResultError("invalid params: " + reason)
	}
	if len(*sha) > maxLastCommitSHAWriteBytes {
		return mcp.NewToolResultError(fmt.Sprintf("last_commit_sha too long (max %d bytes)", maxLastCommitSHAWriteBytes))
	}
	return nil
}

func (s *Server) handleUpsertProjectArch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	slug := stringArg(args, "slug")
	if errResult := validateArchSlug(slug); errResult != nil {
		return errResult, nil
	}

	// summary, file_map and last_commit_sha share one patch-semantics
	// contract (security review PR #157 round 3, unifying what M-3
	// originally fixed for file_map alone): the key must be entirely
	// ABSENT from args to mean "leave the stored value untouched".
	// optionalStringArg (and the equivalent inline check below for
	// file_map, which additionally accepts and JSON-decodes its string) do
	// NOT use stringArg, because stringArg collapses "missing" and
	// "present but not a string" into the same "" — indistinguishable from
	// "present with an explicit empty value". A present key, even "", is
	// an explicit instruction and REPLACES the stored value, including
	// clearing it. This matters because the core protocol is
	// read-then-write (get_project_arch -> read changed files ->
	// upsert_project_arch) and get_project_arch defaults to omitting
	// file_map (W2); an agent that follows the protocol literally and
	// never saw an existing value must not be able to wipe it just by not
	// mentioning it. last_commit_sha additionally feeds the staleness
	// check (mcpInstructions in server.go), so silently wiping it on every
	// upsert that omits it would make that check permanently wrong, not
	// just lose data.
	summary, errResult := optionalStringArg(args, "summary")
	if errResult != nil {
		return errResult, nil
	}
	if summary != nil && len(*summary) > maxSummaryLen {
		return mcp.NewToolResultError(fmt.Sprintf("summary too long (max %d chars)", maxSummaryLen)), nil
	}

	lastCommitSHA, errResult := optionalStringArg(args, "last_commit_sha")
	if errResult != nil {
		return errResult, nil
	}
	if errResult := validateLastCommitSHA(lastCommitSHA); errResult != nil {
		return errResult, nil
	}

	var fileMap *map[string]string
	if rawVal, present := args["file_map"]; present {
		rawFileMap, ok := rawVal.(string)
		if !ok {
			return mcp.NewToolResultError("file_map must be a JSON string"), nil
		}
		if len(rawFileMap) > maxFileMapRaw {
			return mcp.NewToolResultError(fmt.Sprintf("file_map too large (max %d bytes)", maxFileMapRaw)), nil
		}
		m := map[string]string{}
		if rawFileMap != "" {
			if err := json.Unmarshal([]byte(rawFileMap), &m); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("file_map must be a valid JSON object: %v", err)), nil
			}
		}
		fileMap = &m
	}

	snap, err := s.arch.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          slug,
		Summary:       summary,
		FileMap:       fileMap,
		LastCommitSHA: lastCommitSHA,
	})
	if err != nil {
		slog.Error("upsert_project_arch: store error", "err", err)
		return mcp.NewToolResultError("failed to store architecture snapshot"), nil
	}

	// This response goes through wrapUntrustedArchSnapshot too (R4 dispatch
	// round 1 finding, Lead self-review): before this, ONLY get_project_arch
	// was fenced/neutralised. An attacker who could reach upsert_project_arch
	// (e.g. a prompt-injected agent following the write-then-echo pattern
	// every MCP tool call implies) got their forged boundary marker reflected
	// straight back in THIS call's own response — no need to wait for a
	// subsequent get_project_arch. This applied to Summary too, which had
	// never been fenced on the upsert path even though it always was on the
	// get path.
	//
	// includeFileMap=true (not the get_project_arch default of false):
	// jsonText(snap) previously always echoed back whatever FileMap the store
	// returned, with no gating. Passing false here would silently drop
	// file_map from the upsert response — a real behaviour change with
	// unknown blast radius on any caller relying on it — so this preserves
	// that existing behaviour exactly (content neutralised, nothing hidden).
	// Flagged to Lead per dispatch round 1: this is the specific choice that
	// needed sign-off rather than being decided unilaterally.
	return jsonText(wrapUntrustedArchSnapshot(snap, true))
}

// optionalStringArg extracts args[key] using presence semantics: a nil
// *string return means key is entirely absent from args (caller wants the
// existing stored value preserved on upsert); a non-nil pointer — even to ""
// — means the key was present and should replace the stored value. The
// second return value is a ready-to-send MCP error result when the key is
// present but not a JSON string (e.g. a JSON null, a number, an array); nil
// otherwise. Local to upsert_project_arch's patch-semantics fields
// (summary, last_commit_sha) — file_map has extra JSON-parsing validation
// on top of this same presence check and is handled inline above.
func optionalStringArg(args map[string]any, key string) (*string, *mcp.CallToolResult) {
	rawVal, present := args[key]
	if !present {
		return nil, nil
	}
	strVal, ok := rawVal.(string)
	if !ok {
		return nil, mcp.NewToolResultError(fmt.Sprintf("%s must be a string", key))
	}
	return &strVal, nil
}

func (s *Server) handleGetProjectArch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	slug := stringArg(args, "slug")
	if slug == "" {
		return mcp.NewToolResultError("slug is required"), nil
	}

	snap, err := s.arch.GetSnapshot(ctx, slug)
	if err != nil {
		if errors.Is(err, arch.ErrNotFound) {
			// slug here is the caller's raw argument, not a stored value —
			// upsert_project_arch's validateArchSlug gate never runs on this
			// path, so an attacker can hand get_project_arch an arbitrarily
			// long, marker-laden slug and have it reflected back verbatim by
			// %q even though no row was ever written (R4 dispatch, ":181"
			// finding). clipSafe both neutralises any forged boundary marker
			// and bounds the length before it is quoted.
			safeSlug := clipSafe(slug, maxSlugLen)
			return mcp.NewToolResultError(fmt.Sprintf("no architecture snapshot found for %q — call upsert_project_arch first", safeSlug)), nil
		}
		slog.Error("get_project_arch: store error", "err", err)
		return mcp.NewToolResultError("failed to retrieve architecture snapshot"), nil
	}

	// stale field is set false by the store; callers should compare
	// snap.last_commit_sha with `git rev-parse HEAD` themselves.
	snap.Stale = false

	includeFileMap := boolArg(args, "include_file_map")
	return jsonText(wrapUntrustedArchSnapshot(snap, includeFileMap))
}

// wrapUntrustedArchSnapshot returns a copy of snap with its untrusted free
// text made safe to read back into an LLM context:
//
//   - Summary is neutralised against forged boundary markers AND fenced. It is
//     an assistant's prose description of a repository, so a payload planted
//     in any file that assistant read (a README, a vendored dependency, an
//     outside contributor's PR) can reach it through upsert_project_arch,
//     which stores the text with no injection filtering.
//
//   - FileMap keys and values are neutralised but NOT fenced: they are short
//     path -> purpose pairs, and fencing each of up to 128 KB worth of entries
//     would bury the map in markers. Stripping the marker text is what stops
//     one of them faking a boundary for the fenced Summary above.
//
//   - Slug and LastCommitSHA are clipSafe'd (clip to maxSlugLen /
//     maxLastCommitSHAReadRunes respectively — the latter a RUNE cap, not
//     the write side's maxLastCommitSHAWriteBytes BYTE cap; see
//     maxLastCommitSHAReadRunes' own doc comment for why those are two
//     separate constants, neutralise, clip again) but NOT fenced — same
//     unfenced-but-neutralised treatment as FileMap, justified
//     the same way: they are short values (a repo identifier, a git SHA),
//     not prose. The clip step matters even though both fields also have a
//     write-time length cap (validateArchSlug / validateLastCommitSHA in
//     handleUpsertProjectArch): that cap only binds rows written AFTER it
//     shipped, so a row written before it — including one already poisoned
//     through the same gap this closes (R4 dispatch, M-R6/m-R7) — is not
//     retroactively cleaned and must still be bounded on the way out. This
//     matters for Slug too even though maxSlugLen predates this PR: the
//     LENGTH bound always applied, but no CHARACTER allow-list did, so a
//     pre-existing row can still carry a forged boundary marker (spaces,
//     `=`, parens are all outside the new `^[a-zA-Z0-9_\-]+$` allow-list but
//     were accepted by the old length-only check) — clipSafe's neutralise
//     step is what actually protects those legacy rows, the clip step is a
//     no-op for Slug in practice.
//
//     For any legitimate value (well under either cap), the clip step never
//     triggers and the byte-for-byte content survives untouched — see
//     TestWrapUntrustedArchSnapshot_LegitSHAAndSlugByteForByte, which pins
//     that a real SHA is not silently altered before it reaches the caller,
//     who is the one actually comparing it against `git rev-parse HEAD`
//     (this server does not compute staleness itself — see get_project_arch
//     and last_commit_sha's tool descriptions above).
//
// includeFileMap gates FileMap entirely: when false (the get_project_arch
// default), the neutralisation loop is skipped and out.FileMap is left nil —
// file_map is the largest field in this response, and the caller did not ask
// for it (W2, token-diet). When true, the field is populated exactly as
// before this option existed.
//
// The core protocol asks for get_project_arch at session start, so this is on
// the automatic path — the same reason the identically-worded fence existed
// while the snapshot was embedded in get_today_context (PR #156 reviewer M1 /
// security review M-3). A nil snapshot is returned unchanged.
func wrapUntrustedArchSnapshot(snap *arch.Snapshot, includeFileMap bool) *arch.Snapshot {
	if snap == nil {
		return nil
	}
	out := *snap
	out.Slug = clipSafe(snap.Slug, maxSlugLen)
	out.Summary = fenceArchSummary(snap.Summary)
	out.LastCommitSHA = clipSafe(snap.LastCommitSHA, maxLastCommitSHAReadRunes)
	out.FileMap = nil
	if includeFileMap && len(snap.FileMap) > 0 {
		fileMap := make(map[string]string, len(snap.FileMap))
		for path, purpose := range snap.FileMap {
			fileMap[neutralizeBoundaryMarkers(path)] = neutralizeBoundaryMarkers(purpose)
		}
		out.FileMap = fileMap
	}
	return &out
}
