package workspace

import "errors"

// DefaultModelPreference is the model used when a workspace has no stored
// preference. Mirrors the workspace_preferences.model_preference DB column
// DEFAULT (migrations/000013_workspace_model_preference.up.sql).
const DefaultModelPreference = "claude-sonnet-4-6"

// ErrInvalidModel is returned by UpsertModelPreference when the requested model
// is not in AllowedModels.
var ErrInvalidModel = errors.New("workspace: model not in allowed list")

// AllowedModels is the single source of truth for valid AI model identifiers a
// workspace may select. Add new Claude model versions here as they ship; the
// frontend selector mirrors this list (web/src/types/api.ts ALLOWED_MODELS).
var AllowedModels = map[string]struct{}{
	"claude-haiku-4-5":  {},
	"claude-sonnet-4-6": {},
	"claude-opus-4-8":   {},
}

// IsAllowedModel reports whether m is a selectable model identifier.
func IsAllowedModel(m string) bool {
	_, ok := AllowedModels[m]
	return ok
}
