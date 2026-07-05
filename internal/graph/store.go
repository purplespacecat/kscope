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

// ErrNoManifest is returned by Store.Manifest for unknown node IDs (or when
// the snapshot predates manifest capture).
var ErrNoManifest = errors.New("graph: no manifest for node")

// Store holds the single latest snapshot in memory and mirrors it to disk:
// the graph in latest.json, the (much larger) redacted manifests in a
// manifests.json sidecar so the graph payload stays light.
// Safe for concurrent use.
type Store struct {
	mu            sync.RWMutex
	snap          *Snapshot
	filePath      string
	manifestsPath string
}

// NewStore wires the store to a JSON file (e.g. "./data/latest.json").
// Callers should invoke Load() once at startup to hydrate from disk.
func NewStore(filePath string) *Store {
	return &Store{
		filePath:      filePath,
		manifestsPath: filepath.Join(filepath.Dir(filePath), "manifests.json"),
	}
}

// Load reads the snapshot (and manifests sidecar) from disk if present.
// Missing files are not errors — a fresh data dir is a valid state.
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

	// Manifests are optional: pre-M2 snapshots have no sidecar.
	if mdata, err := os.ReadFile(s.manifestsPath); err == nil {
		if err := json.Unmarshal(mdata, &snap.Manifests); err != nil {
			return fmt.Errorf("decode manifests: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read manifests: %w", err)
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

// Manifest returns one node's redacted YAML.
func (s *Store) Manifest(nodeID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snap == nil {
		return "", ErrEmpty
	}
	y, ok := s.snap.Manifests[nodeID]
	if !ok {
		return "", ErrNoManifest
	}
	return y, nil
}

// Set replaces the snapshot and atomically writes both files to disk.
func (s *Store) Set(snap Snapshot) error {
	s.mu.Lock()
	s.snap = &snap
	s.mu.Unlock()

	graphJSON, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	if err := atomicWrite(s.filePath, graphJSON); err != nil {
		return err
	}

	manifests := snap.Manifests
	if manifests == nil {
		manifests = map[string]string{}
	}
	manifestJSON, err := json.Marshal(manifests)
	if err != nil {
		return fmt.Errorf("encode manifests: %w", err)
	}
	return atomicWrite(s.manifestsPath, manifestJSON)
}

// atomicWrite writes to a sibling temp file then renames — atomic on POSIX so
// readers never see a half-written file.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir data dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
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
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}
