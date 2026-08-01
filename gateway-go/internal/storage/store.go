// Package storage persists uploaded document bytes and hands back a URI
// ml-service can read them from directly (see ml-service/app/storage.py) —
// large file payloads never flow through the gRPC call to ml-service,
// only their location does.
package storage

import (
	"context"
	"io"
)

// ObjectStore is the persistence boundary internal/ingestion depends on.
// LocalObjectStore is the only implementation today, since there's no
// object storage service wired up yet (that lands with Docker Compose in
// Phase 16, MinIO in dev / S3 in the AWS deployment target) — everything
// above this interface (ingestion.Service, the HTTP handlers, the worker
// pool) is written against ObjectStore, so that swap touches only the new
// implementation, never a caller.
type ObjectStore interface {
	// Put stores the contents of r under key and returns the URI it can
	// later be read back from.
	Put(ctx context.Context, key string, r io.Reader) (uri string, err error)
}
