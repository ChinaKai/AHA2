package secrets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type FileStore struct {
	mu     sync.RWMutex
	path   string
	values map[string]string
}

func Open(path string) (*FileStore, error) {
	if path == "" {
		return nil, fmt.Errorf("secret store path is required")
	}
	store := &FileStore{path: path, values: map[string]string{}}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &store.values); err != nil {
			return nil, fmt.Errorf("decode secret store: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read secret store: %w", err)
	}
	return store, nil
}

func (s *FileStore) PutMany(values map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, value := range values {
		if key == "" || value == "" {
			continue
		}
		s.values[key] = value
	}
	return s.persistLocked()
}

func (s *FileStore) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[key]
	return value, ok
}

func (s *FileStore) DeleteMany(keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, ok := s.values[key]; ok {
			delete(s.values, key)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.persistLocked()
}

func (s *FileStore) Resolve(names []string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]string, len(names))
	for _, name := range names {
		if value := s.values[name]; value != "" {
			result[name] = value
		}
	}
	return result
}

func (s *FileStore) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create secret directory: %w", err)
	}
	data, err := json.MarshalIndent(s.values, "", "  ")
	if err != nil {
		return fmt.Errorf("encode secret store: %w", err)
	}
	temp := s.path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return fmt.Errorf("write secret store: %w", err)
	}
	if err := os.Chmod(temp, 0o600); err != nil {
		return fmt.Errorf("restrict secret store: %w", err)
	}
	if err := os.Rename(temp, s.path); err != nil {
		return fmt.Errorf("replace secret store: %w", err)
	}
	return os.Chmod(s.path, 0o600)
}
