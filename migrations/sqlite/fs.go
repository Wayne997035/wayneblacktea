// Package sqlitemigrations exposes the embedded SQLite migration files so
// that golang-migrate can apply them at runtime without needing the files
// present on the filesystem. The embed is done here (inside the
// migrations/sqlite/ directory) because Go's //go:embed directive cannot use
// ".." path components.
package sqlitemigrations

import "embed"

// FS holds all *.sql files in the migrations/sqlite/ directory.
//
//go:embed *.sql
var FS embed.FS
