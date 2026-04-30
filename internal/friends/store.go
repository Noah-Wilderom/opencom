// Package friends implements the persistent store of trusted peer
// identities (name + peer ID + public key) used for opencom calls.
package friends

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"opencom/internal/iox"
)

// Sentinel errors for callers that need to distinguish failure modes.
// Use errors.Is to match.
var (
	// ErrFriendNotFound is returned by Get-like and Remove/Rename calls
	// when no friend with the given name exists.
	ErrFriendNotFound = errors.New("friend not found")

	// ErrFriendNameTaken is returned by Add/Rename when the target name
	// is already in use.
	ErrFriendNameTaken = errors.New("friend name already exists")

	// ErrFriendPeerIDTaken is returned by Add when a different friend
	// already records the same peer ID.
	ErrFriendPeerIDTaken = errors.New("friend peer id already exists")
)

// Friend is a single trusted peer record.
type Friend struct {
	Name           string    `json:"name"`
	PeerID         peer.ID   `json:"peer_id"`
	PublicKey      string    `json:"public_key"`
	AddedAt        time.Time `json:"added_at"`
	RendezvousHint string    `json:"rendezvous_hint,omitempty"`
}

// Store is the on-disk friends list. All mutations are atomic-write
// persisted; reads are protected by an RWMutex.
type Store struct {
	path    string
	mu      sync.RWMutex
	friends []Friend
}

// Open loads (or creates) the friends store at path. Missing file is
// initialised to an empty list.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		if err := iox.AtomicWriteFile(path, []byte("[]\n"), 0o600, 0o700); err != nil {
			return nil, fmt.Errorf("creating %s: %w", path, err)
		}
		return s, nil
	}
	if err := json.Unmarshal(data, &s.friends); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	// Normalise nil (file contained `null`) to an empty slice so List()
	// returns [] not null on the wire.
	if s.friends == nil {
		s.friends = []Friend{}
	}
	return s, nil
}

// Add inserts f. Returns ErrFriendNameTaken or ErrFriendPeerIDTaken
// (wrapped with context) on duplicate.
func (s *Store) Add(f Friend) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.friends {
		if existing.Name == f.Name {
			return fmt.Errorf("%w: name %q", ErrFriendNameTaken, f.Name)
		}
		if existing.PeerID == f.PeerID {
			return fmt.Errorf("%w: peer id %s (named %q)", ErrFriendPeerIDTaken, f.PeerID, existing.Name)
		}
	}
	s.friends = append(s.friends, f)
	return s.persistLocked()
}

// List returns a copy of the friends slice, sorted by name.
func (s *Store) List() []Friend {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Friend, len(s.friends))
	copy(out, s.friends)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get looks up a friend by display name.
func (s *Store) Get(name string) (Friend, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, f := range s.friends {
		if f.Name == name {
			return f, true
		}
	}
	return Friend{}, false
}

// GetByPeerID looks up a friend by peer ID.
func (s *Store) GetByPeerID(id peer.ID) (Friend, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, f := range s.friends {
		if f.PeerID == id {
			return f, true
		}
	}
	return Friend{}, false
}

// Remove deletes the friend with the given name. Returns ErrFriendNotFound
// (wrapped with context) if absent.
func (s *Store) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, f := range s.friends {
		if f.Name == name {
			s.friends = append(s.friends[:i], s.friends[i+1:]...)
			return s.persistLocked()
		}
	}
	return fmt.Errorf("%w: %q", ErrFriendNotFound, name)
}

// Rename changes the display name. Returns ErrFriendNotFound (wrapped) if
// oldName is absent, ErrFriendNameTaken (wrapped) if newName is already
// in use.
func (s *Store) Rename(oldName, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, f := range s.friends {
		if f.Name == oldName {
			idx = i
		}
		if f.Name == newName {
			return fmt.Errorf("%w: name %q", ErrFriendNameTaken, newName)
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: %q", ErrFriendNotFound, oldName)
	}
	s.friends[idx].Name = newName
	return s.persistLocked()
}

// PeerIDs returns the peer IDs of all currently-stored friends. Used by
// the libp2p connection notifier to filter relevant connect/disconnect
// events.
func (s *Store) PeerIDs() []peer.ID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]peer.ID, 0, len(s.friends))
	for _, f := range s.friends {
		out = append(out, f.PeerID)
	}
	return out
}

// persistLocked atomically writes s.friends to disk. Caller must hold s.mu.
func (s *Store) persistLocked() error {
	data, err := json.MarshalIndent(s.friends, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling friends: %w", err)
	}
	data = append(data, '\n')
	return iox.AtomicWriteFile(s.path, data, 0o600, 0o700)
}
