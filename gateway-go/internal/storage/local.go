package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// LocalObjectStore writes to a directory on the local filesystem and
// returns file:// URIs. It exists purely as the dev/test stand-in
// documented on ObjectStore.
type LocalObjectStore struct {
	baseDir string
}

func NewLocalObjectStore(baseDir string) (*LocalObjectStore, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("storage: create base dir %s: %w", baseDir, err)
	}
	absDir, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("storage: resolve absolute path for %s: %w", baseDir, err)
	}
	return &LocalObjectStore{baseDir: absDir}, nil
}

func (s *LocalObjectStore) Put(ctx context.Context, key string, r io.Reader) (string, error) {
	path := filepath.Join(s.baseDir, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("storage: create parent dir for %s: %w", key, err)
	}

	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("storage: create file %s: %w", key, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return "", fmt.Errorf("storage: write file %s: %w", key, err)
	}

	return pathToFileURI(path), nil
}

// pathToFileURI matches the file:// convention Python's pathlib.Path.as_uri()
// produces (and ml-service/app/storage.py's reader expects): forward
// slashes, a leading slash before a Windows drive letter, and
// percent-encoding for anything else URL-unsafe (e.g. spaces).
func pathToFileURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	return u.String()
}
