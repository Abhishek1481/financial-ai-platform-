package ingestion

import (
	"context"
	"errors"
)

var (
	ErrDocumentNotFound = errors.New("ingestion: document not found")
	ErrJobNotFound      = errors.New("ingestion: job not found")
)

// DocumentRepository and JobRepository are the persistence boundaries
// Service depends on — the same Repository-pattern seam
// internal/auth.UserRepository uses, for the same reason: there is no
// Postgres connection wired up yet (Phase 16), and every layer above these
// interfaces is written against them, not the concrete in-memory store, so
// swapping in a Postgres-backed implementation later touches only the new
// implementation.
type DocumentRepository interface {
	Create(ctx context.Context, doc Document) error
	FindByID(ctx context.Context, id string) (Document, error)
	FindByContentHash(ctx context.Context, hash string) (Document, error)
}

type JobRepository interface {
	Create(ctx context.Context, job Job) error
	Update(ctx context.Context, job Job) error
	FindByID(ctx context.Context, id string) (Job, error)
	FindLatestByDocumentID(ctx context.Context, documentID string) (Job, error)
}
