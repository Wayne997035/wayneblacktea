// Package migrationfs exposes the embedded SQL migration files so that
// golang-migrate can apply them at runtime without needing the files present
// on the filesystem. The embed is done here (inside the migrations/ directory)
// because Go's //go:embed directive cannot use ".." path components.
package migrationfs

import "embed"

// FS holds all *.sql files in the migrations/ directory.
//
//go:embed *.sql
var FS embed.FS
