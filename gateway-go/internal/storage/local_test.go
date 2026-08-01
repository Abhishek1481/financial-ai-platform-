package storage

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestLocalObjectStore_PutWritesFileAndReturnsFileURI(t *testing.T) {
	store, err := NewLocalObjectStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalObjectStore() error: %v", err)
	}

	uri, err := store.Put(context.Background(), "doc-1/report.txt", strings.NewReader("hello from go"))
	if err != nil {
		t.Fatalf("Put() error: %v", err)
	}

	if !strings.HasPrefix(uri, "file:///") {
		t.Errorf("uri = %q, want file:/// prefix", uri)
	}

	// The URI is what ml-service's app/storage.py reads back from — this
	// only round-trips the write side (the read side is Python's), but
	// confirms the bytes landed at a real, findable path.
	path := strings.TrimPrefix(uri, "file://")
	// On Windows this leaves a leading slash before the drive letter
	// (/C:/...); strip it the same way url2pathname does on the Python
	// side so the test reads the same path Put() actually wrote.
	if len(path) > 2 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back file at %s: %v", path, err)
	}
	if string(content) != "hello from go" {
		t.Errorf("content = %q, want %q", content, "hello from go")
	}
}

func TestLocalObjectStore_CreatesBaseDirIfMissing(t *testing.T) {
	base := t.TempDir() + "/nested/does-not-exist-yet"

	if _, err := NewLocalObjectStore(base); err != nil {
		t.Fatalf("NewLocalObjectStore() error: %v", err)
	}
	if info, err := os.Stat(base); err != nil || !info.IsDir() {
		t.Errorf("base dir %s was not created", base)
	}
}

func TestLocalObjectStore_DistinctKeysDoNotCollide(t *testing.T) {
	store, err := NewLocalObjectStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalObjectStore() error: %v", err)
	}

	uriA, err := store.Put(context.Background(), "doc-a/file.txt", strings.NewReader("A"))
	if err != nil {
		t.Fatalf("Put(a) error: %v", err)
	}
	uriB, err := store.Put(context.Background(), "doc-b/file.txt", strings.NewReader("B"))
	if err != nil {
		t.Fatalf("Put(b) error: %v", err)
	}

	if uriA == uriB {
		t.Errorf("distinct keys produced the same URI: %s", uriA)
	}
}
