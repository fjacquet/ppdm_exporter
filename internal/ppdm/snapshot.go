package ppdm

import (
	"sort"
	"sync"
	"time"
)

// ServerSnapshot is one PPDM server's result for a single collection cycle.
type ServerSnapshot struct {
	Server     string
	LastScrape time.Time
	OK         bool   // server reachable & authenticated
	Err        string // top-level failure (auth/unreachable); empty when OK
	Samples    []Sample
}

// Snapshot is an immutable, point-in-time view across all servers.
type Snapshot struct {
	BuiltAt time.Time
	Servers []*ServerSnapshot
}

// MetricNames returns the sorted, de-duplicated set of metric names in the snapshot.
func (s *Snapshot) MetricNames() []string {
	seen := map[string]struct{}{}
	for _, sv := range s.Servers {
		for _, sm := range sv.Samples {
			seen[sm.Name] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// SamplesByName returns every sample with the given metric name across all servers.
func (s *Snapshot) SamplesByName(name string) []Sample {
	var out []Sample
	for _, sv := range s.Servers {
		for _, sm := range sv.Samples {
			if sm.Name == name {
				out = append(out, sm)
			}
		}
	}
	return out
}

// SnapshotStore holds the latest Snapshot behind an RWMutex pointer-swap.
type SnapshotStore struct {
	mu   sync.RWMutex
	snap *Snapshot
}

// NewSnapshotStore returns a store pre-populated with an empty snapshot so readers
// never see nil before the first collection cycle.
func NewSnapshotStore() *SnapshotStore { return &SnapshotStore{snap: &Snapshot{}} }

// Store atomically swaps in a new snapshot.
func (s *SnapshotStore) Store(snap *Snapshot) { s.mu.Lock(); s.snap = snap; s.mu.Unlock() }

// Load returns the current snapshot (never nil).
func (s *SnapshotStore) Load() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}
