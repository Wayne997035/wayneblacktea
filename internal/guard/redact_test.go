package guard

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRedactString covers every credential-pattern rule.  Each row asserts
// (a) the scrubbed output contains the expected REDACTED marker and
// (b) the original secret is NOT present in the scrubbed output.
func TestRedactString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		input       string
		wantSubstr  string
		secret      string
		dontContain []string
	}{
		{ //nolint:gosec // G101: synthetic credentials in test fixtures; test asserts they are redacted
			name:       "stripe live key",
			input:      "key=sk_live_AbCdEf012345_xyz",
			wantSubstr: "[REDACTED:stripe-live-key]",
			secret:     "sk_live_AbCdEf012345_xyz",
		},
		{ //nolint:gosec // G101: synthetic credentials in test fixtures; test asserts they are redacted
			name:       "stripe test key",
			input:      "auth=sk_test_AbCdEf012345_xyz",
			wantSubstr: "[REDACTED:stripe-test-key]",
			secret:     "sk_test_AbCdEf012345_xyz",
		},
		{
			name:       "github PAT",
			input:      "header=ghp_" + strings.Repeat("A", 40),
			wantSubstr: "[REDACTED:github-pat]",
			secret:     "ghp_" + strings.Repeat("A", 40),
		},
		{
			name:       "github oauth token",
			input:      "OAUTH=gho_" + strings.Repeat("Z", 38),
			wantSubstr: "[REDACTED:github-oauth]",
			secret:     "gho_" + strings.Repeat("Z", 38),
		},
		{ //nolint:gosec // G101: synthetic credentials in test fixtures; test asserts they are redacted
			name:       "slack bot token",
			input:      "send xoxb-12345-67890-abcdefg",
			wantSubstr: "[REDACTED:slack-bot-token]",
			secret:     "xoxb-12345-67890-abcdefg",
		},
		{ //nolint:gosec // G101: synthetic credentials in test fixtures; test asserts they are redacted
			name:       "slack user token",
			input:      "send xoxp-12345-67890-abcdefg",
			wantSubstr: "[REDACTED:slack-user-token]",
			secret:     "xoxp-12345-67890-abcdefg",
		},
		{ //nolint:gosec // G101: synthetic JWT in test fixture; test asserts redaction
			name:       "bearer jwt",
			input:      "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0",
			wantSubstr: "[REDACTED:jwt-bearer]",
			secret:     "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0",
		},
		{ //nolint:gosec // G101: synthetic DSN in test fixture; test asserts redaction
			name:       "postgres dsn",
			input:      "DATABASE_URL=postgres://alice:s3cret@db.example.com:5432/app",
			wantSubstr: "postgres://[REDACTED]:[REDACTED]@db.example.com:5432/app",
			secret:     "alice:s3cret",
		},
		{ //nolint:gosec // G101: synthetic DSN in test fixture; test asserts redaction
			name:       "mongodb dsn srv",
			input:      "uri=mongodb+srv://admin:topsecret@cluster.mongodb.net/db",
			wantSubstr: "mongodb://[REDACTED]:[REDACTED]@cluster.mongodb.net",
			secret:     "admin:topsecret",
		},
		{ //nolint:gosec // G101: synthetic DSN in test fixture; test asserts redaction
			name:       "mysql dsn",
			input:      "url=mysql://root:rootpw@127.0.0.1:3306/app",
			wantSubstr: "mysql://[REDACTED]:[REDACTED]@127.0.0.1:3306/app",
			secret:     "root:rootpw",
		},
		{
			name: "aws access key",
			// Built via concat so the source file does not embed a literal
			// AKIA-prefixed string that pre-commit secret scanners flag.
			// "AKIA" + 16 zeros is intentionally fake.
			input:      "AWS_ACCESS_KEY_ID=" + "AKIA" + "0000000000000000",
			wantSubstr: "[REDACTED:aws-access-key]",
			secret:     "AKIA" + "0000000000000000",
		},
		{
			name:        "generic password key=value",
			input:       "DB_PASSWORD=hunter2 PORT=8080",
			wantSubstr:  "[REDACTED]",
			secret:      "hunter2",
			dontContain: []string{"hunter2"},
		},
		{
			name:        "generic api_key key=value with hyphen",
			input:       `--api-key="my-private-key-value"`,
			wantSubstr:  "[REDACTED]",
			secret:      "my-private-key-value",
			dontContain: []string{"my-private-key-value"},
		},
		{
			name:        "generic token: with colon",
			input:       "Auth token: tok_abcdef12345",
			wantSubstr:  "[REDACTED]",
			secret:      "tok_abcdef12345",
			dontContain: []string{"tok_abcdef12345"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := redactString(tc.input)
			if !strings.Contains(got, tc.wantSubstr) {
				t.Errorf("redactString(%q) = %q, want substring %q", tc.input, got, tc.wantSubstr)
			}
			if tc.secret != "" && strings.Contains(got, tc.secret) {
				t.Errorf("redactString(%q) leaked secret %q in output: %q", tc.input, tc.secret, got)
			}
			for _, banned := range tc.dontContain {
				if strings.Contains(got, banned) {
					t.Errorf("redactString(%q) leaked %q in output: %q", tc.input, banned, got)
				}
			}
		})
	}
}

// TestRedactToolInput_PreservesJSON verifies the redacted output is still
// parseable JSON with the same shape as the input.
func TestRedactToolInput_PreservesJSON(t *testing.T) {
	t.Parallel()
	in := json.RawMessage(`{"command":"echo Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0","cwd":"/repo"}`)
	out := RedactToolInput(in)
	if len(out) == 0 {
		t.Fatal("RedactToolInput returned empty payload")
	}
	var decoded map[string]string
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("redacted output is not valid JSON: %v\noutput: %s", err, string(out))
	}
	if !strings.Contains(decoded["command"], "[REDACTED:jwt-bearer]") {
		t.Errorf("command field not redacted: %q", decoded["command"])
	}
	if decoded["cwd"] != "/repo" {
		t.Errorf("non-secret field cwd was modified: %q", decoded["cwd"])
	}
}

// TestRedactToolInput_NestedJSON verifies redaction recurses into nested
// objects and arrays — Edit / MultiEdit payloads can carry secret content
// inside .edits[].new_string.
func TestRedactToolInput_NestedJSON(t *testing.T) {
	t.Parallel()
	in := json.RawMessage(`{"edits":[{"new_string":"export DB=postgres://u:p@h/d"},{"new_string":"safe"}]}`)
	out := RedactToolInput(in)
	if !strings.Contains(string(out), "[REDACTED]") {
		t.Errorf("nested redaction missed: %s", string(out))
	}
	if strings.Contains(string(out), "u:p@h") {
		t.Errorf("nested redaction leaked secret: %s", string(out))
	}
}

// TestRedactToolInput_MalformedJSON verifies the function fails-open: even
// a non-JSON payload still has regex scrubbing applied to the raw bytes.
func TestRedactToolInput_MalformedJSON(t *testing.T) {
	t.Parallel()
	in := json.RawMessage(`not json sk_live_AbCdEf012345_xyz`)
	out := RedactToolInput(in)
	if !strings.Contains(string(out), "[REDACTED:stripe-live-key]") {
		t.Errorf("malformed-JSON fallback missed scrubbing: %s", string(out))
	}
}

// TestRedactToolInput_Empty verifies empty input returns empty input.
func TestRedactToolInput_Empty(t *testing.T) {
	t.Parallel()
	out := RedactToolInput(json.RawMessage{})
	if len(out) != 0 {
		t.Errorf("empty input should produce empty output, got %q", string(out))
	}
}
