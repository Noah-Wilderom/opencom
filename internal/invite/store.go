package invite

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"opencom/internal/iox"
)

// Entry is one row in the active-invites store.
type Entry struct {
	Code       Code      `json:"code"`
	ExpiresAt  time.Time `json:"expires_at"`
	Consumed   bool      `json:"consumed"`
	ConsumedBy string    `json:"consumed_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Store is a disk-backed map of active invite codes.
//
// Reads come from an in-memory mirror; writes flush atomically to disk.
// On corruption (bad JSON), Open returns an empty store rather than
// failing — losing the cache is preferable to losing the daemon.
type Store struct {
	path    string
	mu      sync.RWMutex
	entries map[Code]Entry

	flushMu sync.Mutex // serializes flush operations to preserve write ordering on disk
}

// storeFile is the on-disk shape. Entries are serialized as a slice
// rather than a map keyed by Code: Code is already ASCII so a map would
// work, but a slice keeps the format symmetric with future stores and
// avoids encoding ambiguity.
type storeFile struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

const storeVersion = 1

// OpenStore loads the store at path, creating an empty store if it
// doesn't exist or is corrupted.
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, entries: make(map[Code]Entry)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading store: %w", err)
	}
	var f storeFile
	if err := json.Unmarshal(data, &f); err != nil || f.Version != storeVersion {
		// Corrupted or unknown version → drop and start fresh.
		return s, nil
	}
	for _, e := range f.Entries {
		s.entries[e.Code] = e
	}
	return s, nil
}

// Add or replace an entry, then flushes.
func (s *Store) Add(e Entry) {
	s.mu.Lock()
	s.entries[e.Code] = e
	s.mu.Unlock()
	s.flush()
}

// Get returns the entry for c, if any.
func (s *Store) Get(c Code) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[c]
	return e, ok
}

// MarkConsumed sets Consumed=true and records the redeemer's peer ID.
// Returns false if the code is unknown, expired, or already consumed.
func (s *Store) MarkConsumed(c Code, byPeerID string) bool {
	s.mu.Lock()
	e, ok := s.entries[c]
	if !ok || e.Consumed || time.Now().After(e.ExpiresAt) {
		s.mu.Unlock()
		return false
	}
	e.Consumed = true
	e.ConsumedBy = byPeerID
	s.entries[c] = e
	s.mu.Unlock()
	s.flush()
	return true
}

// UnmarkConsumed reverts a prior MarkConsumed for c. Used to roll back
// when the post-MarkConsumed step (e.g. friends.Add) fails so the
// inviter can retry without re-issuing the code. No-op if c is unknown
// or wasn't consumed.
func (s *Store) UnmarkConsumed(c Code) {
	s.mu.Lock()
	e, ok := s.entries[c]
	if !ok || !e.Consumed {
		s.mu.Unlock()
		return
	}
	e.Consumed = false
	e.ConsumedBy = ""
	s.entries[c] = e
	s.mu.Unlock()
	s.flush()
}

// Remove drops the entry for c, then flushes. Returns true if an
// entry was actually removed; false if c was unknown.
func (s *Store) Remove(c Code) bool {
	s.mu.Lock()
	_, existed := s.entries[c]
	if existed {
		delete(s.entries, c)
	}
	s.mu.Unlock()
	if existed {
		s.flush()
	}
	return existed
}

// ActiveList returns non-consumed, non-expired entries.
func (s *Store) ActiveList() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		if !e.Consumed && now.Before(e.ExpiresAt) {
			out = append(out, e)
		}
	}
	return out
}

// AllList returns every entry (including consumed and expired).
// Used by `opencom invite list` UX so users see "this code was used by Bob."
func (s *Store) AllList() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	return out
}

func (s *Store) flush() {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	s.mu.RLock()
	entries := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		entries = append(entries, e)
	}
	s.mu.RUnlock()

	f := storeFile{Version: storeVersion, Entries: entries}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')
	_ = iox.AtomicWriteFile(s.path, data, 0o600, 0o700)
}
