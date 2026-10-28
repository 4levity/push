// Package store keeps the last status per client name, in memory.
package store

import (
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// Status is one recorded status report.
type Status struct {
	Name     string          `json:"name"`
	Time     time.Time       `json:"time"`
	SourceIP string          `json:"source_ip"`
	Status   json.RawMessage `json:"status"`
}

// Info is the per-client summary used by the listing endpoint.
type Info struct {
	Name     string     `json:"name"`
	LastTime *time.Time `json:"last_status_time"`
}

// Store is a concurrency-safe last-status-wins map.
type Store struct {
	mu sync.RWMutex
	m  map[string]*Status
}

// New returns an empty Store.
func New() *Store { return &Store{m: make(map[string]*Status)} }

// Set records st, replacing any previous status for the same client name.
func (s *Store) Set(st Status) {
	st.Time = st.Time.UTC()
	cp := st
	s.mu.Lock()
	s.m[st.Name] = &cp
	s.mu.Unlock()
}

// Get returns the last recorded status for name.
func (s *Store) Get(name string) (Status, bool) {
	s.mu.RLock()
	st, ok := s.m[name]
	s.mu.RUnlock()
	if !ok {
		return Status{}, false
	}
	cp := *st
	return cp, true
}

// List returns one Info per given client name, sorted by name.
// Clients without a recorded status have a nil LastTime.
func (s *Store) List(names []string) []Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Info, 0, len(names))
	for _, n := range names {
		info := Info{Name: n}
		if st, ok := s.m[n]; ok {
			t := st.Time
			info.LastTime = &t
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
