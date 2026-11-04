// Package ids generates time-ordered UUIDv7 identifiers in Go, because
// PostgreSQL 16 has no uuidv7() function.
package ids

import "github.com/google/uuid"

// New returns a fresh UUIDv7. It never fails in practice; on the
// (theoretical) failure of the random source it falls back to UUIDv4.
func New() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.New()
	}
	return id
}
