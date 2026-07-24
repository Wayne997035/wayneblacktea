package handler_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/google/uuid"
)

// TestValidateBranchName_RuneLength is the round-1 follow-up for GTD
// ce3e0747: the previous len(s) > 255 check counted bytes, so a 85-character
// CJK branch name (~255 bytes, 85 runes) was rejected even though git itself
// accepts it. The fix uses len([]rune(s)) so byte-encoding no longer
// determines whether a name passes.
func TestValidateBranchName_RuneLength(t *testing.T) {
	// 85 CJK chars ("漢" 3 bytes each = 255 bytes). 85 runes ≤ 255 → accepted.
	cjk85 := strings.Repeat("漢", 85)
	// 256 ASCII chars (256 runes, 256 bytes) → rejected.
	ascii256 := strings.Repeat("a", 256)
	// 256 CJK chars (256 runes, 768 bytes) → rejected (rune count, not bytes).
	cjk256 := strings.Repeat("漢", 256)

	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty_is_valid", "", false},
		{"short_ascii", "feature/foo", false},
		{"ascii_255_boundary", strings.Repeat("a", 255), false},
		{"ascii_256_rejected", ascii256, true},
		{"cjk_85_chars_255_bytes_accepted", cjk85, false},
		{"cjk_256_chars_rejected", cjk256, true},
		{"control_char_rejected", "feature/\x01bad", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := handler.ValidateBranchNameForTest(tc.input)
			if (msg != "") != tc.wantErr {
				t.Errorf("validateBranchName(%q): err=%q, wantErr=%v",
					tc.input, msg, tc.wantErr)
			}
		})
	}
}

// TestStrictVagueness_ParseBool verifies validator.StrictModeEnabled (the
// single-source gate this package now reads instead of its own copy) accepts
// the strconv.ParseBool truthy set ("1", "t", "T", "true", "True", "TRUE")
// and rejects everything else (including the previously-accepted-only "true"
// plus "0", empty, garbage). Round-1 follow-up GTD 6cda1ce2.
func TestStrictVagueness_ParseBool(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want bool
	}{
		{"truthy_1", "1", true},
		{"truthy_lower_t", "t", true},
		{"truthy_upper_T", "T", true},
		{"truthy_true", "true", true},
		{"truthy_True", "True", true},
		{"truthy_TRUE", "TRUE", true},
		{"falsy_0", "0", false},
		{"falsy_false", "false", false},
		{"falsy_empty", "", false},
		{"invalid_yes_falsy", "yes", false},
		{"invalid_2_falsy", "2", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WBT_STRICT_VAGUENESS", tc.env)
			if got := validator.StrictModeEnabled(); got != tc.want {
				t.Errorf("StrictModeEnabled() with env=%q = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

// TestCreateTaskRequest_StrictVagueness verifies that the CreateTask handler
// reads WBT_STRICT_VAGUENESS through validator.StrictModeEnabled()
// (strconv.ParseBool) — round-2 follow-up for M-1, replacing the deleted
// strictVaguenessEnabled() == "true" reader on gtd_handler.go. Dedup follow-
// up: this package no longer owns its own copy of the reader either — see
// internal/validator/task_input.go.
//
// Drives the full CreateTask handler path with a vague description ("TBD")
// and asserts:
//   - env="true"  → 400 "vagueness check failed" (legacy literal still works)
//   - env="1"     → 400 "vagueness check failed" (ParseBool truthy values)
//   - env=""      → 201 Created with X-Vagueness-Warnings header (warn mode)
//
// The "1" case proves the unification: pre-M-1 it would have been treated as
// warn mode by the old strictVaguenessEnabled, now it correctly blocks.
func TestCreateTaskRequest_StrictVagueness(t *testing.T) {
	taskID := uuid.New()
	const vagueBody = `{"title":"Fix login","description":"TBD","kind":"general"}`

	cases := []struct {
		name            string
		env             string
		wantCode        int
		wantBodySubstr  string
		wantWarningsHdr bool
	}{
		{
			name:            "env=true → strict blocks with 400",
			env:             "true",
			wantCode:        http.StatusBadRequest,
			wantBodySubstr:  "vagueness check failed",
			wantWarningsHdr: false,
		},
		{
			name:            "env=1 → strict blocks with 400 (ParseBool truthy)",
			env:             "1",
			wantCode:        http.StatusBadRequest,
			wantBodySubstr:  "vagueness check failed",
			wantWarningsHdr: false,
		},
		{
			name:            "env=empty → warn mode 201 with header",
			env:             "",
			wantCode:        http.StatusCreated,
			wantWarningsHdr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WBT_STRICT_VAGUENESS", tc.env)
			store := &fakeGTDStore{createdTask: &db.Task{ID: taskID, Title: "Fix login"}}
			e := newEcho()
			h := handler.NewGTDHandler(store)
			e.POST("/api/tasks", h.CreateTask)
			rec := performRequest(e, http.MethodPost, "/api/tasks", vagueBody)
			if rec.Code != tc.wantCode {
				t.Fatalf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantBodySubstr != "" && !strings.Contains(rec.Body.String(), tc.wantBodySubstr) {
				t.Errorf("body %q does not contain %q", rec.Body.String(), tc.wantBodySubstr)
			}
			hdr := rec.Header().Get("X-Vagueness-Warnings")
			if tc.wantWarningsHdr && hdr == "" {
				t.Error("expected X-Vagueness-Warnings header to be set, got empty")
			}
			if !tc.wantWarningsHdr && hdr != "" && tc.wantCode == http.StatusBadRequest {
				t.Errorf("expected no X-Vagueness-Warnings header on strict 400, got: %s", hdr)
			}
		})
	}
}
