// Package migrations embeds the goose SQL migrations in this directory, so
// the api binary can apply them (`api migrate`) without the source tree and
// tests can build a schema from exactly the files a deploy runs.
package migrations

import "embed"

// FS holds every migration file. Add migrations with `make migration`; a
// migration that has run anywhere shared is never edited.
//
//go:embed *.sql
var FS embed.FS
