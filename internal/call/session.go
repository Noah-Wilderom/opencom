package call

import (
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// State is the call session lifecycle.
type State int

const (
	StateIdle State = iota
	StateRinging
	StateConnecting
	StateConnected
	StateEnded
)

// String returns the lowercase name used on the wire.
func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateRinging:
		return "ringing"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateEnded:
		return "ended"
	default:
		return "unknown"
	}
}

// Direction tells whether the local side initiated or received the call.
type Direction int

const (
	Outbound Direction = iota
	Inbound
)

// String returns the lowercase wire-format direction name.
func (d Direction) String() string {
	switch d {
	case Outbound:
		return "outbound"
	case Inbound:
		return "inbound"
	default:
		return "unknown"
	}
}

// StateChange is the event emitted when a Session transitions.
type StateChange struct {
	SessionID string    `json:"session_id"`
	State     string    `json:"state"`
	Direction string    `json:"direction"`
	Remote    peer.ID   `json:"remote"`
	Time      time.Time `json:"time"`
	Reason    string    `json:"reason,omitempty"`
}

// Session is one active call. Safe for concurrent use.
type Session struct {
	id        string
	remote    peer.ID
	direction Direction
	now       func() time.Time
	startedAt time.Time

	mu     sync.Mutex
	state  State
	reason string

	subsMu sync.Mutex
	subs   map[int]chan StateChange
	nextID int
}

// NewSession constructs a Session in StateIdle. If now is nil, time.Now is
// used.
func NewSession(id string, remote peer.ID, dir Direction, now func() time.Time) *Session {
	if now == nil {
		now = time.Now
	}
	return &Session{
		id:        id,
		remote:    remote,
		direction: dir,
		now:       now,
		startedAt: now(),
		subs:      make(map[int]chan StateChange),
	}
}

// ID returns the session identifier.
func (s *Session) ID() string { return s.id }

// Remote returns the peer on the other end of the call.
func (s *Session) Remote() peer.ID { return s.remote }

// Direction returns whether the session is inbound or outbound.
func (s *Session) Direction() Direction { return s.direction }

// StartedAt is the time the session was created (immutable).
func (s *Session) StartedAt() time.Time { return s.startedAt }

// State returns the current state.
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Reason returns the reason recorded by End. Empty until End is called.
func (s *Session) Reason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// ToRinging moves Idle -> Ringing.
func (s *Session) ToRinging() error { return s.transition(StateRinging, "") }

// ToConnecting moves Ringing -> Connecting.
func (s *Session) ToConnecting() error { return s.transition(StateConnecting, "") }

// ToConnected moves Connecting -> Connected.
func (s *Session) ToConnected() error { return s.transition(StateConnected, "") }

// End moves any non-Ended state to Ended. Idempotent: subsequent calls are
// no-ops and the first reason is preserved.
func (s *Session) End(reason string) error {
	s.mu.Lock()
	if s.state == StateEnded {
		s.mu.Unlock()
		return nil
	}
	s.state = StateEnded
	s.reason = reason
	now := s.now()
	s.mu.Unlock()

	s.broadcast(StateChange{
		SessionID: s.id,
		State:     StateEnded.String(),
		Direction: s.direction.String(),
		Remote:    s.remote,
		Time:      now,
		Reason:    reason,
	})
	return nil
}

func (s *Session) transition(target State, reason string) error {
	s.mu.Lock()
	if s.state == StateEnded {
		s.mu.Unlock()
		return fmt.Errorf("session %s is already ended", s.id)
	}
	if target != s.state+1 {
		current := s.state
		s.mu.Unlock()
		return fmt.Errorf("invalid transition for session %s: %s -> %s",
			s.id, current, target)
	}
	s.state = target
	now := s.now()
	s.mu.Unlock()

	s.broadcast(StateChange{
		SessionID: s.id,
		State:     target.String(),
		Direction: s.direction.String(),
		Remote:    s.remote,
		Time:      now,
		Reason:    reason,
	})
	return nil
}

// Subscribe returns a buffered channel of state changes. The buffer absorbs
// short bursts; events are dropped if the consumer is slow.
func (s *Session) Subscribe() (int, <-chan StateChange) {
	ch := make(chan StateChange, 16)
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	id := s.nextID
	s.nextID++
	s.subs[id] = ch
	return id, ch
}

// Unsubscribe closes the channel returned by Subscribe with the given id.
// Idempotent.
func (s *Session) Unsubscribe(id int) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	if ch, ok := s.subs[id]; ok {
		delete(s.subs, id)
		close(ch)
	}
}

func (s *Session) broadcast(ev StateChange) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for _, ch := range s.subs {
		select {
		case ch <- ev:
		default:
			// Drop on overflow rather than block.
		}
	}
}
