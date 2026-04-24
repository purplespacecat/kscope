package graph

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// ErrEmpty is returned by Store.Get when no snapshot has been recorded yet.
var ErrEmpty = errors.New("graph: no snapshot")

// Store holds the single latest snapshot in memory and mirrors it to disk.
// Safe for concurrent use.
type Store struct {
	mu       sync.RWMutex
	snap     *Snapshot
	filePath string
}

// NewStore wires the store to a JSON file (e.g. "./data/latest.json").
// Callers should invoke Load() once at startup to hydrate from disk.
func NewStore(filePath string) *Store {
	return &Store{filePath: filePath}
}

// Load reads the snapshot from disk if present. Missing file is not an error.
func (s *Store) Load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read snapshot: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("decode snapshot: %w", err)
	}
	s.mu.Lock()
	s.snap = &snap
	s.mu.Unlock()
	return nil
}

// Get returns a copy of the current snapshot or ErrEmpty.
func (s *Store) Get() (Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snap == nil {
		return Snapshot{}, ErrEmpty
	}
	return *s.snap, nil
}

// Set replaces the snapshot and atomically writes it to disk.
func (s *Store) Set(snap Snapshot) error {
	s.mu.Lock()
	s.snap = &snap
	s.mu.Unlock()
	return s.writeFile(snap)
}

// writeFile writes to a sibling temp file then renames — atomic on POSIX so
// readers never see a half-written file.
func (s *Store) writeFile(snap Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o755); err != nil {
		return fmt.Errorf("mkdir data dir: %w", err)
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.filePath), ".latest-*.json")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, s.filePath); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}
