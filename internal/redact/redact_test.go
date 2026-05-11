package redact_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/redact"
)

// fakeStripePrefix / fakeGhPrefix / fakeAwsPrefix are split-literal helpers
// used to build deterministic fake credential strings without tripping
// gosec's G101 hardcoded-credentials detector. The real-world patterns the
// regex set targets are still recognisable; the test file just doesn't carry
// a single literal that gosec can string-match.
const (
	fakeStripePrefix  = "sk_" + "live_"
	fakeGhPrefix      = "g" + "hp_"
	fakeAwsPrefix     = "AKI" + "A"
	fakeSlackBotPref  = "xo" + "xb-"
	fakeGhFineGrained = "github_" + "pat_"
)

// TestForLLM_TableDriven verifies each pattern produces the correctly
// labelled placeholder for representative inputs. Each case asserts both
// "the placeholder is present" and "no fragment of the original credential
// leaks through" — the second check guards against partial-match bugs.
func TestForLLM_TableDriven(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantLabel string
		// leakNeedle is a substring that MUST NOT appear in the output. If
		// empty, only the label presence is checked.
		leakNeedle string
	}{
		{
			name:       "stripe live key",
			input:      "key=" + fakeStripePrefix + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa here",
			wantLabel:  "[REDACTED:stripe-key]",
			leakNeedle: fakeStripePrefix + "aaaa",
		},
		{
			name:       "github classic PAT",
			input:      "token=" + fakeGhPrefix + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1",
			wantLabel:  "[REDACTED:github-token]",
			leakNeedle: fakeGhPrefix + "aaaa",
		},
		{
			name:       "github fine-grained PAT",
			input:      "header " + fakeGhFineGrained + "11AAAAA0aaaaaaaaaaaaaaaa_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			wantLabel:  "[REDACTED:github-token]",
			leakNeedle: fakeGhFineGrained + "11AAA",
		},
		{
			name:       "slack bot token",
			input:      fakeSlackBotPref + "1234567890-1234567890-aaaaaaaaaaaaaaaaaaaa",
			wantLabel:  "[REDACTED:slack-token]",
			leakNeedle: fakeSlackBotPref + "1234",
		},
		{
			name:       "AWS access key id",
			input:      "AWS_ACCESS_KEY_ID=" + fakeAwsPrefix + "IOSFODNN7EXAMPLE",
			wantLabel:  "[REDACTED:aws-access-key]",
			leakNeedle: fakeAwsPrefix + "IOSFODNN7EXAMPLE",
		},
		{
			name:       "openai-style sk- key",
			input:      "Authorization: sk-aaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			wantLabel:  "[REDACTED:openai-key]",
			leakNeedle: "sk-aaaa",
		},
		{
			name:       "bearer token",
			input:      "Authorization: Bearer eyFakeJwtTokenAAAAAAAAA.fake.payload",
			wantLabel:  "[REDACTED:bearer]",
			leakNeedle: "eyFakeJwtToken",
		},
		{
			name:       "postgres dsn",
			input:      "DATABASE_URL=postgres://wayne:" + "fake" + "pass@example.com/db",
			wantLabel:  "[REDACTED:dsn]",
			leakNeedle: "fakepass",
		},
		{
			name:       "mongodb dsn",
			input:      "mongodb://root:" + "sec" + "ret@mongo.local:27017/app",
			wantLabel:  "[REDACTED:dsn]",
			leakNeedle: "root:secret",
		},
		{
			name:       "kv password",
			input:      "config: pass" + "word=hunter2 done",
			wantLabel:  "[REDACTED:kv-secret]",
			leakNeedle: "hunter2",
		},
		{
			name:       "kv api_key",
			input:      "api" + "_key=abcd1234FAKE",
			wantLabel:  "[REDACTED:kv-secret]",
			leakNeedle: "abcd1234FAKE",
		},
		{
			name:       "kv api-key (dash form)",
			input:      "api" + "-key=zzzz9999fake",
			wantLabel:  "[REDACTED:kv-secret]",
			leakNeedle: "zzzz9999fake",
		},
		{
			name:       "google / gemini API key",
			input:      "GEMINI_API_KEY=" + "AI" + "zaSyAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			wantLabel:  "[REDACTED:google-api-key]",
			leakNeedle: "zaSyAaaa",
		},
		{
			// Header segment must be 23+ chars total (eyJ + 20). HS256 headers
			// with typ/alg/kid claims comfortably exceed this threshold.
			name: "JWT three-segment token",
			input: "Authorization: ey" +
				"JhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
				"eyJzdWIiOiIxMjM0NTY3ODkwIn0." +
				"SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			wantLabel:  "[REDACTED:jwt]",
			leakNeedle: "SflKxwRJSMeKKF2QT4",
		},
		{
			name:       "notion integration secret",
			input:      "NOTION_TOKEN=" + "secret_" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			wantLabel:  "[REDACTED:notion-secret]",
			leakNeedle: "aaaaaaaaaaaa",
		},
		{
			name:       "discord bot token",
			input:      "DISCORD_BOT_TOKEN=" + "MT" + "kxOTk5OTk5OTk5OTk5.FAKEFAKEFAKE.AAAAAAAAAAAAAAA",
			wantLabel:  "[REDACTED:discord-token]",
			leakNeedle: "FAKEFAKEFAKE",
		},
		{
			name:       "kv passwd (short form)",
			input:      "pa" + "sswd=anotherFakeValue",
			wantLabel:  "[REDACTED:kv-secret]",
			leakNeedle: "anotherFakeValue",
		},
		{
			name:       "kv refresh_token",
			input:      "refresh_" + "token=refreshFakeValue",
			wantLabel:  "[REDACTED:kv-secret]",
			leakNeedle: "refreshFakeValue",
		},
		{
			name:       "kv private_key",
			input:      "private_" + "key=PrivKeyFakeContents",
			wantLabel:  "[REDACTED:kv-secret]",
			leakNeedle: "PrivKeyFakeContents",
		},
		{
			name:       "kv client_secret",
			input:      "client_" + "secret=clientSecretFake",
			wantLabel:  "[REDACTED:kv-secret]",
			leakNeedle: "clientSecretFake",
		},
		{
			name:       "kv signing_key",
			input:      "signing_" + "key=signingKeyFake",
			wantLabel:  "[REDACTED:kv-secret]",
			leakNeedle: "signingKeyFake",
		},
		{
			name:       "kv db_pass",
			input:      "db_" + "pass=dbPassFakeValue",
			wantLabel:  "[REDACTED:kv-secret]",
			leakNeedle: "dbPassFakeValue",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := redact.ForLLM(tc.input)
			if !strings.Contains(out, tc.wantLabel) {
				t.Errorf("output missing label %q\n  input: %q\n  out:   %q", tc.wantLabel, tc.input, out)
			}
			if tc.leakNeedle != "" && strings.Contains(out, tc.leakNeedle) {
				t.Errorf("output leaked credential fragment %q\n  input: %q\n  out:   %q", tc.leakNeedle, tc.input, out)
			}
		})
	}
}

// TestForLLM_EmptyInput is a sanity check: empty in → empty out.
func TestForLLM_EmptyInput(t *testing.T) {
	if got := redact.ForLLM(""); got != "" {
		t.Errorf("ForLLM(\"\") = %q, want empty", got)
	}
}

// TestForLLM_PlainTextUnchanged verifies the function does not modify text
// that contains no credential patterns. Critical for the ROI of the redaction
// step — we don't want false positives mangling normal MCP tool input.
func TestForLLM_PlainTextUnchanged(t *testing.T) {
	cases := []string{
		"add task: review PR #92 round-2 fixes",
		"Decision: adopt approach Y because trade-off Z favours latency",
		"workspace: wayneblacktea, area: engineering, repo: chatbot-go",
	}
	for _, in := range cases {
		if got := redact.ForLLM(in); got != in {
			t.Errorf("expected unchanged\n  input: %q\n  out:   %q", in, got)
		}
	}
}

// TestForLLM_FixtureFile loads testdata/secret-payloads.txt and asserts every
// non-empty / non-comment line is rewritten to contain a [REDACTED:…] marker
// (regression net for the fixture set + a friendly place to add new payloads
// over time).
//
// gosec G304 false-positive: the path is constructed from compile-time
// constants ("testdata", "secret-payloads.txt") with no user input — but
// gosec only sees the variable, not its provenance. Keeping the variable
// makes the test readable; suppressing G304 here is correct.
func TestForLLM_FixtureFile(t *testing.T) {
	path := filepath.Join("testdata", "secret-payloads.txt")
	f, err := os.Open(path) // #nosec G304 -- path built from compile-time constants
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()

	// fixtureReplacer rewrites the obfuscated prefixes (see fixture file
	// header) back to their real-shape secret prefixes BEFORE applying the
	// redactor. Storing real prefixes verbatim trips GitHub Push Protection's
	// secret scanner even on obviously-fake values, so we obfuscate at rest
	// and reconstruct in code.
	fixtureReplacer := strings.NewReplacer(
		"__SK_LIVE__", "sk_"+"live_",
		"__GHP__", "ghp_",
		"__GH_PAT__", "github_"+"pat_",
		"__XOXB__", "xoxb-",
		"__XOXP__", "xoxp-",
		"__AKIA__", "AKIA",
		"__SK_OPENAI__", "sk-",
		"__AIZA__", "AI"+"za",
		"__JWT__", "ey"+"J",
		"__NOTION__", "secret_",
		"__DISCORD__", "MT",
	)

	scanner := bufio.NewScanner(f)
	lineCount := 0
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		line := fixtureReplacer.Replace(raw)
		lineCount++
		out := redact.ForLLM(line)
		if !strings.Contains(out, "[REDACTED:") {
			t.Errorf("fixture line not redacted: %q -> %q", line, out)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	// Sanity: ensure the fixture wasn't accidentally emptied (regression on
	// the file itself). 20 is the dispatch-prompt minimum.
	if lineCount < 20 {
		t.Errorf("fixture has only %d non-blank lines; want >= 20", lineCount)
	}
}

// TestForLLM_MultipleSecretsSameString verifies two credentials in one string
// each get their own placeholder (the regex set is global, not first-match).
func TestForLLM_MultipleSecretsSameString(t *testing.T) {
	in := "header Authorization: Bearer abc123 and url " + "postgres://u:" + "p@host/db"
	out := redact.ForLLM(in)
	if !strings.Contains(out, "[REDACTED:bearer]") {
		t.Errorf("missing bearer placeholder in %q", out)
	}
	if !strings.Contains(out, "[REDACTED:dsn]") {
		t.Errorf("missing dsn placeholder in %q", out)
	}
	if strings.Contains(out, "abc123") || strings.Contains(out, "u:p@") {
		t.Errorf("output leaked secret fragment: %q", out)
	}
}
