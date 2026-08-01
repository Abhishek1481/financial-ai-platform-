package ingestion

import (
	"context"
	"sync"
)

// MemoryDocumentRepository and MemoryJobRepository are in-process,
// non-durable implementations — lost on restart, never shared across
// replicas. See DocumentRepository's doc comment for why that's an
// accepted, temporary tradeoff rather than a gap.
type MemoryDocumentRepository struct {
	mu     sync.RWMutex
	byID   map[string]Document
	byHash map[string]string // content hash -> document ID
}

func NewMemoryDocumentRepository() *MemoryDocumentRepository {
	return &MemoryDocumentRepository{
		byID:   make(map[string]Document),
		byHash: make(map[string]string),
	}
}

func (r *MemoryDocumentRepository) Create(ctx context.Context, doc Document) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[doc.ID] = doc
	r.byHash[doc.ContentHash] = doc.ID
	return nil
}

func (r *MemoryDocumentRepository) FindByID(ctx context.Context, id string) (Document, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	doc, ok := r.byID[id]
	if !ok {
		return Document{}, ErrDocumentNotFound
	}
	return doc, nil
}

func (r *MemoryDocumentRepository) FindByContentHash(ctx context.Context, hash string) (Document, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byHash[hash]
	if !ok {
		return Document{}, ErrDocumentNotFound
	}
	return r.byID[id], nil
}

type MemoryJobRepository struct {
	mu            sync.RWMutex
	byID          map[string]Job
	latestByDocID map[string]string // document ID -> most recently created job ID
}

func NewMemoryJobRepository() *MemoryJobRepository {
	return &MemoryJobRepository{
		byID:          make(map[string]Job),
		latestByDocID: make(map[string]string),
	}
}

func (r *MemoryJobRepository) Create(ctx context.Context, job Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[job.ID] = job
	r.latestByDocID[job.DocumentID] = job.ID
	return nil
}

func (r *MemoryJobRepository) Update(ctx context.Context, job Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[job.ID]; !ok {
		return ErrJobNotFound
	}
	r.byID[job.ID] = job
	return nil
}

func (r *MemoryJobRepository) FindByID(ctx context.Context, id string) (Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	job, ok := r.byID[id]
	if !ok {
		return Job{}, ErrJobNotFound
	}
	return job, nil
}

func (r *MemoryJobRepository) FindLatestByDocumentID(ctx context.Context, documentID string) (Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	jobID, ok := r.latestByDocID[documentID]
	if !ok {
		return Job{}, ErrJobNotFound
	}
	return r.byID[jobID], nil
}
