package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// stub atom.StoreIface
// ---------------------------------------------------------------------------

type stubAtomStore struct {
	countTotal    int64
	countTotalErr error
	searchResults []atom.Atom
	searchErr     error
	addedAtoms    []*atom.Atom
	addErr        error
	setStatusErr  error
}

var _ atom.StoreIface = (*stubAtomStore)(nil)

func (s *stubAtomStore) CountTotal(_ context.Context, _ *uuid.UUID) (int64, error) {
	return s.countTotal, s.countTotalErr
}

func (s *stubAtomStore) Search(_ context.Context, _ *uuid.UUID, _ string, _ int) ([]atom.Atom, error) {
	return s.searchResults, s.searchErr
}

func (s *stubAtomStore) AddAtom(_ context.Context, p atom.AddAtomParams) (*atom.Atom, error) {
	if s.addErr != nil {
		return nil, s.addErr
	}
	a := &atom.Atom{
		ID:          uuid.New(),
		ParentTable: p.ParentTable,
		Content:     p.Content,
		Keywords:    p.Keywords,
		Tags:        p.Tags,
		CreatedAt:   time.Now(),
	}
	s.addedAtoms = append(s.addedAtoms, a)
	return a, nil
}

func (s *stubAtomStore) AddLink(_ context.Context, _ atom.AddLinkParams) error { return nil }

func (s *stubAtomStore) ListByParent(_ context.Context, _ string, _ uuid.UUID) ([]atom.Atom, error) {
	return nil, nil
}

func (s *stubAtomStore) Traverse(_ context.Context, _ uuid.UUID, _ int) (*atom.TraverseResult, error) {
	return &atom.TraverseResult{}, nil
}

func (s *stubAtomStore) PruneAtoms(_ context.Context, _ time.Time) (int64, error) { return 0, nil }

func (s *stubAtomStore) SetDigestStatus(_ context.Context, _ uuid.UUID, _ string, _ string) error {
	return s.setStatusErr
}

func (s *stubAtomStore) CountByDigestStatus(_ context.Context, _ *uuid.UUID, _ string) (int64, error) {
	return 0, nil
}

func (s *stubAtomStore) ListByDigestStatus(_ context.Context, _ *uuid.UUID, _ string, _ int) ([]atom.Atom, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestBuildConsolidationPrompt_Shape verifies that buildConsolidationPrompt
// produces output containing the [BEGIN ATOMS] marker and the cluster keyword.
func TestBuildConsolidationPrompt_Shape(t *testing.T) {
	cl := atomCluster{
		keyword: "testing",
		atoms: []atom.Atom{
			{
				ID:       uuid.New(),
				Content:  "unit test best practices",
				Keywords: []string{"testing", "go"},
				Tags:     []string{"engineering"},
			},
			{
				ID:       uuid.New(),
				Content:  "integration test strategies",
				Keywords: []string{"testing", "containers"},
				Tags:     []string{"qa"},
			},
		},
	}

	prompt := buildConsolidationPrompt(cl)

	if !strings.Contains(prompt, "[BEGIN ATOMS]") {
		t.Errorf("prompt missing [BEGIN ATOMS] marker, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "testing") {
		t.Errorf("prompt missing cluster keyword 'testing', got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "unit test best practices") {
		t.Errorf("prompt missing first atom content, got:\n%s", prompt)
	}
}

// TestCollectDistinctKeywords_EmptyAtoms verifies that when the store returns
// an empty slice, collectDistinctKeywords returns an empty keywords list
// (no panic).
func TestCollectDistinctKeywords_EmptyAtoms(t *testing.T) {
	wsID := uuid.New()
	deps := atomConsolidDeps{
		store:       &stubAtomStore{searchResults: nil},
		atomizer:    nil, // not needed for this function
		workspaceID: &wsID,
	}

	keywords, err := collectDistinctKeywords(context.Background(), deps)
	if err != nil {
		t.Fatalf("collectDistinctKeywords error: %v", err)
	}
	if len(keywords) != 0 {
		t.Errorf("expected 0 keywords for empty atoms, got %d: %v", len(keywords), keywords)
	}
}

// TestCollectDistinctKeywords_DeduplicatesKeywords verifies that repeated
// keywords across atoms are de-duplicated in the output.
func TestCollectDistinctKeywords_DeduplicatesKeywords(t *testing.T) {
	wsID := uuid.New()
	kw := "golang"
	deps := atomConsolidDeps{
		store: &stubAtomStore{
			searchResults: []atom.Atom{
				{ID: uuid.New(), Content: "atom 1", Keywords: []string{kw, "testing"}},
				{ID: uuid.New(), Content: "atom 2", Keywords: []string{kw, "api"}},
			},
		},
		workspaceID: &wsID,
	}

	keywords, err := collectDistinctKeywords(context.Background(), deps)
	if err != nil {
		t.Fatalf("collectDistinctKeywords error: %v", err)
	}

	seen := make(map[string]int)
	for _, k := range keywords {
		seen[k]++
		if seen[k] > 1 {
			t.Errorf("keyword %q appears more than once in result", k)
		}
	}
}

// TestBuildAtomClusters_FiltersConsolidated verifies that atoms with
// DigestStatus='consolidated' are excluded from cluster membership.
func TestBuildAtomClusters_FiltersConsolidated(t *testing.T) {
	wsID := uuid.New()
	consolidated := "consolidated"
	activeID := uuid.New()
	consolidatedID := uuid.New()

	deps := atomConsolidDeps{
		store: &stubAtomStore{
			searchResults: []atom.Atom{
				{
					ID:           activeID,
					Content:      "active atom content",
					Keywords:     []string{"go"},
					DigestStatus: nil, // active
				},
				{
					ID:           consolidatedID,
					Content:      "already consolidated",
					Keywords:     []string{"go"},
					DigestStatus: &consolidated, // must be filtered
				},
				// add more to meet minimum cluster size (>3 = at least 4)
				{ID: uuid.New(), Content: "atom 3", Keywords: []string{"go"}},
				{ID: uuid.New(), Content: "atom 4", Keywords: []string{"go"}},
				{ID: uuid.New(), Content: "atom 5", Keywords: []string{"go"}},
			},
		},
		workspaceID: &wsID,
	}

	clusters := buildAtomClusters(context.Background(), deps, []string{"go"})
	if len(clusters) == 0 {
		t.Fatal("expected at least one cluster, got none")
	}

	// Verify consolidated atom is not in any cluster.
	for _, cl := range clusters {
		for _, a := range cl.atoms {
			if a.ID == consolidatedID {
				t.Errorf("consolidated atom %v should have been filtered from cluster", consolidatedID)
			}
		}
	}

	// Verify active atom is still included.
	found := false
	for _, cl := range clusters {
		for _, a := range cl.atoms {
			if a.ID == activeID {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("active atom %v should be included in clusters", activeID)
	}
}
