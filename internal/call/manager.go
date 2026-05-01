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

	// Manager-level state-change fanout. audio.Manager (M8) subscribes
	// here to spawn/teardown audio sessions on call transitions.
	subsMu sync.Mutex
	subs   []chan StateChange
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
// A goroutine is spawned to forward per-session StateChange events to all
// Manager-level subscribers registered via SubscribeStateChanges.
func (m *Manager) Register(s *Session) {
	m.mu.Lock()
	if _, ok := m.sessions[s.ID()]; ok {
		m.mu.Unlock()
		return
	}
	m.sessions[s.ID()] = s
	m.mu.Unlock()

	// Subscribe to the session's per-session state changes and forward to
	// Manager-level subscribers. The forwarder exits after delivering an
	// "ended" event (Session.End broadcasts it but never closes subscriber
	// channels, so we exit on the sentinel state).
	subID, ch := s.Subscribe()
	go func() {
		defer s.Unsubscribe(subID)
		for ev := range ch {
			m.fanout(ev)
			if ev.State == StateEnded.String() {
				return
			}
		}
	}()

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

// SubscribeStateChanges returns a buffered receive-only channel that emits
// StateChange events for ALL registered sessions. Currently active sessions
// are subscribed to retroactively (their future transitions will be delivered).
// Drops on full channel rather than blocking.
//
// Caller MUST call UnsubscribeStateChanges with the returned channel to avoid
// leaks.
func (m *Manager) SubscribeStateChanges() <-chan StateChange {
	ch := make(chan StateChange, 32)
	m.subsMu.Lock()
	m.subs = append(m.subs, ch)
	m.subsMu.Unlock()
	return ch
}

// UnsubscribeStateChanges removes a subscriber and closes the channel.
// Idempotent: calling with an already-removed channel is a no-op.
func (m *Manager) UnsubscribeStateChanges(ch <-chan StateChange) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for i, c := range m.subs {
		if ((<-chan StateChange)(c)) == ch {
			close(c)
			m.subs = append(m.subs[:i], m.subs[i+1:]...)
			return
		}
	}
}

// fanout sends ev to all current Manager-level subscribers. Called from the
// per-session forwarder goroutine spawned in Register. Drops on overflow.
func (m *Manager) fanout(ev StateChange) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for _, ch := range m.subs {
		select {
		case ch <- ev:
		default:
			// drop on overflow; subscriber too slow
		}
	}
}
