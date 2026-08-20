package buildinfo

import "testing"

// resetSentinels restores Version/Commit/Date to their zero-ldflags
// defaults after a test overrides them — mirrors the t.Cleanup pattern
// internal/mcp's own buildinfo canary tests already use
// (resources_buildinfo_test.go), kept local here since this package has no
// other test yet to share it with.
func resetSentinels(t *testing.T) {
	t.Helper()
	origV, origC, origD := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = origV, origC, origD
	})
}

// TestBuildID_SentinelDefaults is U6's bad-case acceptance criterion
// (2026-08-20-mcp-surface-spec.md, U6 row): a build with no injected
// identity (the ldflags-less state this package's Commit/Date sentinels
// default to) must report the "dev" sentinel, explicitly NOT a
// pseudo-version-shaped string like "v0.0.0-unknown-none" that could pass
// for a real, if malformed, build identity.
func TestBuildID_SentinelDefaults(t *testing.T) {
	resetSentinels(t)
	Version, Commit, Date = "dev", "none", "unknown"

	got := BuildID()
	if got != "dev" {
		t.Errorf("BuildID() = %q, want sentinel %q", got, "dev")
	}
	if got == "v0.0.0-unknown-none" {
		t.Error("BuildID() returned a pseudo-version-shaped string built directly from the sentinel " +
			"strings — this must never happen, it looks like a real (if malformed) build identity")
	}
}

// TestBuildID_RealValues pins the byte-exact format against the live values
// this repo's HEAD commit produced (Lead's verification, recorded in the
// dispatch spec's assumptions table): Commit = the 40-char SHA whose first
// 12 hex chars are "5b87fcbf4b78", Date = "2026-08-16T07:43:48Z" (the
// measured Railway build-info timestamp, 20s after the commit's own author
// time — build/Dockerfile's RAILWAY_GIT_COMMIT_SHA comment block explains
// why BuildID uses build time, not commit time).
func TestBuildID_RealValues(t *testing.T) {
	resetSentinels(t)
	Version = "dev"
	Commit = "5b87fcbf4b78dd0dc290e25e3b220da110dd316f"
	Date = "2026-08-16T07:43:48Z"

	const want = "v0.0.0-20260816074348-5b87fcbf4b78"
	if got := BuildID(); got != want {
		t.Errorf("BuildID() = %q, want %q", got, want)
	}
}

// TestBuildID_MalformedDate is U6's other bad-case: Commit is a real-looking
// SHA but Date fails to parse as RFC3339 (e.g. a build pipeline that sets
// COMMIT but leaves BUILD_DATE at a non-timestamp value, or a future date
// format change nobody wired through here yet). BuildID must fall back to
// "dev" rather than embedding an unparsed or zero-value timestamp into
// something that otherwise looks like a real build identity.
func TestBuildID_MalformedDate(t *testing.T) {
	resetSentinels(t)
	Version = "dev"
	Commit = "5b87fcbf4b78dd0dc290e25e3b220da110dd316f"

	cases := []string{
		"unknown",    // the package's own Date sentinel
		"2026-08-16", // date only, no time — not RFC3339
		"not-a-date-at-all",
		"",
	}
	for _, date := range cases {
		t.Run(date, func(t *testing.T) {
			Date = date
			if got := BuildID(); got != "dev" {
				t.Errorf("BuildID() with Date=%q = %q, want sentinel %q", date, got, "dev")
			}
		})
	}
}

// TestBuildID_ShortCommitFallsBack covers a Commit value present but shorter
// than commitShaLen (12) — e.g. a hand-set short SHA — which would otherwise
// panic on the Commit[:commitShaLen] slice or silently truncate to fewer
// hex characters than the documented format promises.
func TestBuildID_ShortCommitFallsBack(t *testing.T) {
	resetSentinels(t)
	Version = "dev"
	Commit = "5b87fcb" // 7 chars, git's default abbreviated SHA length
	Date = "2026-08-16T07:43:48Z"

	if got := BuildID(); got != "dev" {
		t.Errorf("BuildID() with a 7-char Commit = %q, want sentinel %q", got, "dev")
	}
}

// TestEffectiveVersion_FallsBackWhenSentinel is U6's acceptance criterion for
// EffectiveVersion: when Version is at its sentinel (every Railway
// production deploy today — no git tag ever drove one, see README's new
// "Checking what's running" section), EffectiveVersion must return BuildID's
// output rather than the bare "dev" sentinel.
func TestEffectiveVersion_FallsBackWhenSentinel(t *testing.T) {
	resetSentinels(t)
	Version = "dev"
	Commit = "5b87fcbf4b78dd0dc290e25e3b220da110dd316f"
	Date = "2026-08-16T07:43:48Z"

	want := BuildID()
	if want == "dev" {
		t.Fatal("test setup bug: BuildID() itself returned the sentinel, this test cannot distinguish " +
			"fallback from no-fallback")
	}
	if got := EffectiveVersion(); got != want {
		t.Errorf("EffectiveVersion() = %q, want BuildID()'s output %q", got, want)
	}
}

// TestEffectiveVersion_RealTagUnchanged is EffectiveVersion's non-fallback
// half: a goreleaser tagged release sets Version to a real tag, and that
// value must pass through unchanged regardless of what Commit/Date hold —
// a real tag is never replaced by a synthetic build ID.
func TestEffectiveVersion_RealTagUnchanged(t *testing.T) {
	resetSentinels(t)
	Version = "v1.4.2"
	Commit = "5b87fcbf4b78dd0dc290e25e3b220da110dd316f"
	Date = "2026-08-16T07:43:48Z"

	if got := EffectiveVersion(); got != "v1.4.2" {
		t.Errorf("EffectiveVersion() = %q, want the real tag %q unchanged", got, "v1.4.2")
	}
}

// TestEffectiveVersion_RealTagUnchangedEvenWithSentinelCommit covers the edge
// the two tests above don't: a real Version tag but Commit/Date still at
// their sentinels (a hand-run `go build -ldflags "-X ...Version=v1.0.0"`
// with nothing else set). EffectiveVersion must still return the tag, not
// fall through to BuildID() (which would itself return "dev" here, masking
// a real version behind a less specific string).
func TestEffectiveVersion_RealTagUnchangedEvenWithSentinelCommit(t *testing.T) {
	resetSentinels(t)
	Version = "v1.0.0"
	Commit = "none"
	Date = "unknown"

	if got := EffectiveVersion(); got != "v1.0.0" {
		t.Errorf("EffectiveVersion() = %q, want the real tag %q unchanged", got, "v1.0.0")
	}
}
