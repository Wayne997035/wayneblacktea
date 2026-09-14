package validator

import (
	"strings"
	"testing"
)

// allKindLabels is every label kindRequiredSections checks, flattened. Derived
// from the map rather than retyped: a new kind added there without a test here
// would otherwise be silently uncovered.
func allKindLabels() []string {
	seen := map[string]bool{}
	var out []string
	for _, labels := range kindRequiredSections {
		for _, l := range labels {
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	return out
}

// TestHasKindSection_AcceptedForms covers [GTD 7fc84288]'s widening for every
// label, not just the "acceptance" one the ticket was filed about — the same
// judgement key gates all seven, so a fix that only handled one would leave six
// warnings still firing on every correctly-written description.
func TestHasKindSection_AcceptedForms(t *testing.T) {
	t.Parallel()

	for _, label := range allKindLabels() {
		for name, body := range map[string]string{
			"inline with colon":     "some prose\n" + label + ": the value\n",
			"h1 heading":            "# " + label + "\nthe value\n",
			"h2 heading":            "## " + label + "\nthe value\n",
			"h3 heading with colon": "### " + label + ":\nthe value\n",
			"heading with trailing words": "## " + label + " criteria\n" +
				"- one\n- two\n",
			"heading with no space after hashes": "##" + label + "\nthe value\n",
			"heading indented":                   "  ## " + label + "\nthe value\n",
		} {
			t.Run(label+"/"+name, func(t *testing.T) {
				t.Parallel()
				if !hasKindSection(strings.ToLower(body), label) {
					t.Errorf("hasKindSection(%q, %q) = false, want true", body, label)
				}
			})
		}
	}
}

// TestHasKindSection_RejectedForms is the half that has to keep failing. A
// widened matcher earns its keep only if it still says no to a description that
// merely mentions the word — otherwise the warning has been killed in the
// opposite direction and, again, nothing reports it.
func TestHasKindSection_RejectedForms(t *testing.T) {
	t.Parallel()

	for _, label := range allKindLabels() {
		for name, body := range map[string]string{
			"empty":           "",
			"unrelated prose": "just some notes about the work\n",
			"bare mention mid-sentence": "we will write the " + label +
				" later, once the shape settles\n",
			"mention at line start, no colon and no hashes": label + " comes later\n",
			"longer word that merely starts with the label": "## " + label + "ness\nthe value\n",
			"label inside a word":                           "## pre" + label + "\nthe value\n",
		} {
			t.Run(label+"/"+name, func(t *testing.T) {
				t.Parallel()
				if hasKindSection(strings.ToLower(body), label) {
					t.Errorf("hasKindSection(%q, %q) = true, want false", body, label)
				}
			})
		}
	}
}

// TestCheckKindFields_MarkdownHeadingsSatisfyEveryKind is the end-to-end shape
// of the reported failure: a ticket written the way this repo writes tickets
// used to come back with a full set of warnings.
func TestCheckKindFields_MarkdownHeadingsSatisfyEveryKind(t *testing.T) {
	t.Parallel()

	for kind, labels := range kindRequiredSections {
		desc := "# " + kind + " work\n\n"
		for _, l := range labels {
			desc += "## " + l + "\n- something concrete\n\n"
		}
		if kind == "fix-pr" {
			desc += "root cause at internal/validator/task_kind.go:109\n"
		}

		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			if got := CheckKindFields(kind, desc); len(got) != 0 {
				t.Errorf("CheckKindFields(%q, <headings-only description>) = %v, want no warnings",
					kind, got)
			}
		})
	}
}

// TestCheckKindFields_MentionWithoutSectionStillWarns is the same end-to-end
// path for the negative case, per kind.
func TestCheckKindFields_MentionWithoutSectionStillWarns(t *testing.T) {
	t.Parallel()

	for kind, labels := range kindRequiredSections {
		desc := "we should think about "
		for _, l := range labels {
			desc += l + " and "
		}
		desc += "then start\n"

		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			got := CheckKindFields(kind, desc)
			if len(got) < len(labels) {
				t.Errorf("CheckKindFields(%q, <mentions only>) = %v, want a warning for each of %v",
					kind, got, labels)
			}
		})
	}
}

// TestCheckKindFields_WarningNamesBothAcceptedForms keeps the message useful:
// a caller told only that "acceptance:" is missing, while the checker now also
// accepts a heading, would go add the one form the ticket convention does not
// use.
func TestCheckKindFields_WarningNamesBothAcceptedForms(t *testing.T) {
	t.Parallel()

	got := CheckKindFields("feature", "nothing useful here")
	if len(got) == 0 {
		t.Fatal("CheckKindFields(feature, <empty description>) returned no warnings")
	}
	for _, w := range got {
		if !strings.Contains(w, `:"`) || !strings.Contains(w, "## ") {
			t.Errorf("warning %q does not name both accepted forms", w)
		}
	}
}
