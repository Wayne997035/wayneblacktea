// Package runtime exposes process-wide configuration read at startup time:
// most importantly the optional workspace context that scopes every domain
// Store. WORKSPACE_ID env unset → legacy mode (no filter, NULL inserts);
// set → all reads filtered by that UUID and all writes populate it.
package runtime

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
)

// ErrInvalidWorkspaceID is returned by WorkspaceIDFromEnv when the env var is
// set but cannot be parsed as a UUID. Callers typically log.Fatal on this.
var ErrInvalidWorkspaceID = errors.New("WORKSPACE_ID env is not a valid UUID")

// WorkspaceIDFromEnv returns the optional workspace ID configured via the
// WORKSPACE_ID environment variable.
//
// Returns (nil, nil) when the env is unset → all stores operate in legacy
// mode: no WHERE filter on workspace_id, INSERT with NULL workspace_id.
//
// Returns (id, nil) when set and well-formed → stores filter and populate.
//
// Returns (nil, err wrapping ErrInvalidWorkspaceID) when set but malformed.
func WorkspaceIDFromEnv() (*uuid.UUID, error) {
	raw := strings.TrimSpace(os.Getenv("WORKSPACE_ID"))
	if raw == "" {
		return nil, nil //nolint:nilnil // sentinel: no workspace = legacy mode
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidWorkspaceID, err)
	}
	return &id, nil
}

// UserIDFromEnv returns the optional user identity configured via USER_ID.
// Returns ("", nil) when unset. The schema does not yet have a user_id column
// (deferred to Phase C/D); this helper exists so the MCP/HTTP surface can
// already start reading the value (e.g. for proposed_by attribution).
func UserIDFromEnv() string {
	return strings.TrimSpace(os.Getenv("USER_ID"))
}
