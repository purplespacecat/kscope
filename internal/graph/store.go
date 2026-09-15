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
	mu sync.RWMutex
	// manifestsLoaded records whether snap.Manifests reflects the sidecar.
	// It is false only after LoadGraph, which skips the sidecar; Manifest
	// then reads it on demand.
	manifestsLoaded bool
	snap            *Snapshot
	filePath        string
	manifestsPath   string
}

// NewStore wires the store to a JSON file (e.g. "./data/latest.json").
// Callers should invoke Load() once at startup to hydrate from disk.
func NewStore(filePath string) *Store {
	return &Store{
		filePath:      filePath,
		manifestsPath: filepath.Join(filepath.Dir(filePath), "manifests.json"),
	}
}

// Load reads the snapshot and the manifests sidecar from disk if present.
// Missing files are not errors — a fresh data dir is a valid state.
func (s *Store) Load() error {
	snap, err := s.readGraph()
	if err != nil || snap == nil {
		return err
	}

	// Manifests are optional: pre-M2 snapshots have no sidecar.
	m, err := s.readManifests()
	if err != nil {
		return err
	}
	snap.Manifests = m

	s.mu.Lock()
	s.snap = snap
	s.manifestsLoaded = true
	s.mu.Unlock()
	return nil
}

// LoadGraph reads only latest.json. The manifests sidecar holds every
// captured object's YAML and is much the larger of the two files, so a caller
// that only reads the graph — map, find and info all do — should not pay to
// read, decode and retain it. Manifest picks it up on demand if it is needed
// after all.
func (s *Store) LoadGraph() error {
	snap, err := s.readGraph()
	if err != nil || snap == nil {
		return err
	}
	s.mu.Lock()
	s.snap = snap
	s.manifestsLoaded = false
	s.mu.Unlock()
	return nil
}

// readGraph returns the decoded latest.json, or (nil, nil) when there is no
// file — a fresh data dir is a valid state, not an error.
func (s *Store) readGraph() (*Snapshot, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	return &snap, nil
}

// readManifests returns the sidecar, or nil when there is none.
func (s *Store) readManifests() (map[string]string, error) {
	data, err := os.ReadFile(s.manifestsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read manifests: %w", err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode manifests: %w", err)
	}
	return m, nil
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

// Manifest returns one node's redacted YAML, reading the sidecar first if the
// snapshot was hydrated by LoadGraph.
func (s *Store) Manifest(nodeID string) (string, error) {
	s.mu.RLock()
	empty, loaded := s.snap == nil, s.manifestsLoaded
	s.mu.RUnlock()
	if empty {
		return "", ErrEmpty
	}
	if !loaded {
		if err := s.hydrateManifests(); err != nil {
			return "", err
		}
	}

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

// hydrateManifests reads the sidecar into the held snapshot. The file read
// happens under the write lock: it is the simple choice, the lock is held for
// one read of one file, and it means two concurrent first calls to Manifest
// cannot both decode it. The flag is re-checked inside because the other one
// may have done exactly that while this call waited.
func (s *Store) hydrateManifests() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snap == nil || s.manifestsLoaded {
		return nil
	}
	m, err := s.readManifests()
	if err != nil {
		return err
	}
	s.snap.Manifests = m
	s.manifestsLoaded = true
	return nil
}

// Set replaces the snapshot and atomically writes both files to disk.
func (s *Store) Set(snap Snapshot) error {
	s.mu.Lock()
	s.snap = &snap
	s.manifestsLoaded = true
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
