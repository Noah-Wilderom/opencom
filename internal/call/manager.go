package call

import (
	"sort"
	"sync"
)

// Manager is the registry of active Sessions.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session

	inbound chan *Session
}

// NewManager constructs an empty Manager. The Inbound channel has cap 16;
// fresh inbound sessions are dropped if no reader has caught up. The
// Manager itself never blocks on a slow inbound consumer.
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		inbound:  make(chan *Session, 16),
	}
}

// Register adds s to the registry. Idempotent if s.ID() already present.
// For Inbound sessions it is also published on the Inbound channel.
func (m *Manager) Register(s *Session) {
	m.mu.Lock()
	if _, ok := m.sessions[s.ID()]; ok {
		m.mu.Unlock()
		return
	}
	m.sessions[s.ID()] = s
	m.mu.Unlock()

	if s.Direction() == Inbound {
		select {
		case m.inbound <- s:
		default:
			// Drop on overflow; consumer will see the session via List() anyway.
		}
	}
}

// Get returns the session with the given ID and a presence flag.
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

// List returns sessions sorted by ID (deterministic for tests + UX).
func (m *Manager) List() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// Remove drops the session with the given ID from the registry. Idempotent.
func (m *Manager) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
}

// Inbound returns a channel of newly-registered Inbound sessions.
func (m *Manager) Inbound() <-chan *Session { return m.inbound }
