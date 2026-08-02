package auth

import (
	"context"
	"errors"
)

var (
	ErrUserNotFound = errors.New("auth: user not found")
	ErrUserExists   = errors.New("auth: user already exists")
)

// UserRepository is the persistence boundary the auth Service depends on.
// Depending on this interface rather than a concrete store is what lets
// Service, and everything built on top of it (handlers, middleware), be
// fully unit-tested without a database — and lets the in-memory
// implementation below be swapped for a Postgres-backed one (once the
// datastore is wired up) without touching Service or any HTTP handler.
type UserRepository interface {
	Create(ctx context.Context, user User) error
	FindByEmail(ctx context.Context, email string) (User, error)
	FindByID(ctx context.Context, id string) (User, error)
	// ListAll powers the admin dashboard's user listing (Phase 15) — no
	// pagination yet, an accepted limitation at this store's in-memory,
	// dev-scale data volumes (see MemoryUserRepository's doc comment).
	ListAll(ctx context.Context) ([]User, error)
}
