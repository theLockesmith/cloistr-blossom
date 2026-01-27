package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LocalStorage implements StorageBackend using the local filesystem.
type LocalStorage struct {
	basePath string
}

// NewLocalStorage creates a new LocalStorage instance.
// The basePath directory will be created if it doesn't exist.
func NewLocalStorage(basePath string) (*LocalStorage, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("create storage directory: %w", err)
	}
	return &LocalStorage{basePath: basePath}, nil
}

// blobPath returns the filesystem path for a given hash.
// Uses a two-level directory structure to avoid too many files in one directory.
// e.g., hash "abcdef..." -> basePath/ab/cd/abcdef...
func (s *LocalStorage) blobPath(hash string) string {
	if len(hash) < 4 {
		return filepath.Join(s.basePath, hash)
	}
	return filepath.Join(s.basePath, hash[:2], hash[2:4], hash)
}

func (s *LocalStorage) Put(ctx context.Context, hash string, data io.Reader, size int64) error {
	path := s.blobPath(hash)

	// Create parent directories
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create blob directory: %w", err)
	}

	// Write to temporary file first, then rename for atomicity
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	_, err = io.Copy(f, data)
	if err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write blob data: %w", err)
	}

	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}

func (s *LocalStorage) Get(ctx context.Context, hash string) (io.ReadCloser, error) {
	path := s.blobPath(hash)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrBlobNotFound
		}
		return nil, fmt.Errorf("open blob file: %w", err)
	}
	return f, nil
}

func (s *LocalStorage) Delete(ctx context.Context, hash string) error {
	path := s.blobPath(hash)
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete blob file: %w", err)
	}
	return nil
}

func (s *LocalStorage) Exists(ctx context.Context, hash string) (bool, error) {
	path := s.blobPath(hash)
	_, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat blob file: %w", err)
	}
	return true, nil
}

func (s *LocalStorage) Size(ctx context.Context, hash string) (int64, error) {
	path := s.blobPath(hash)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, ErrBlobNotFound
		}
		return 0, fmt.Errorf("stat blob file: %w", err)
	}
	return info.Size(), nil
}
