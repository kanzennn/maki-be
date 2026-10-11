// Package id creates the CUID2 strings that every table uses as its primary
// key (`<entity>_id`, stored as VARCHAR(32)). IDs are made in the
// application, never by the database, so offline clients can create them too
// without collisions.
package id

import "github.com/nrednav/cuid2"

// New returns a new 24-character CUID2, such as tz4a98xxat96iws9zmbrgj3a.
func New() string {
	return cuid2.Generate()
}

// Valid reports whether s has the shape of a CUID2 that fits an ID column:
// a lowercase letter, then lowercase letters and digits, 2 to 32 characters
// in all. Clients generate IDs too, so this checks the shape, not the length
// New happens to use.
func Valid(s string) bool {
	return cuid2.IsCuid(s)
}
