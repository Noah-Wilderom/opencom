package friends

import (
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// PresenceEvent is emitted when a peer transitions between online/offline.
type PresenceEvent struct {
	PeerID peer.ID   `json:"peer_id"`
	Online bool      `json:"online"`
	Time   time.Time `json:"time"`
}

// Presence tracks the online/offline state of peer IDs and broadcasts
// transitions to subscribers. Safe for concurrent use.
type Presence struct {
	now func() time.Time

	mu       sync.RWMutex
	online   map[peer.ID]bool
	lastSeen map[peer.ID]time.Time

	subsMu sync.Mutex
	subs   map[int]chan PresenceEvent
	nextID int
}

// NewPresence returns a Presence whose timestamps come from now (typically
// time.Now). Tests pass a deterministic clock. Passing nil falls back to
// time.Now.
func NewPresence(now func() time.Time) *Presence {
	if now == nil {
		now = time.Now
	}
	return &Presence{
		now:      now,
		online:   make(map[peer.ID]bool),
		lastSeen: make(map[peer.ID]time.Time),
		subs:     make(map[int]chan PresenceEvent),
	}
}

// MarkOnline records that id is reachable. Fires a PresenceEvent if this
// is a transition (was offline). Updates LastSeen unconditionally.
func (p *Presence) MarkOnline(id peer.ID) {
	p.transition(id, true)
}

// MarkOffline records that id is unreachable. Fires a PresenceEvent if
// this is a transition (was online). Updates LastSeen unconditionally.
func (p *Presence) MarkOffline(id peer.ID) {
	p.transition(id, false)
}

func (p *Presence) transition(id peer.ID, online bool) {
	p.mu.Lock()
	prev, known := p.online[id]
	// MarkOffline for an unknown peer is a no-op: there is no transition
	// to record, and writing state would contradict LastSeen's docstring
	// (zero time if never observed) plus open an unbounded-write attack
	// surface for callers that loop over arbitrary peer IDs.
	if !known && !online {
		p.mu.Unlock()
		return
	}
	now := p.now()
	p.online[id] = online
	p.lastSeen[id] = now
	changed := prev != online
	p.mu.Unlock()

	if !changed {
		return
	}
	ev := PresenceEvent{PeerID: id, Online: online, Time: now}
	p.broadcast(ev)
}

// IsOnline reports the current online state of id.
func (p *Presence) IsOnline(id peer.ID) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.online[id]
}

// LastSeen returns the most recent transition time for id, or the zero
// time if id has never been observed.
func (p *Presence) LastSeen(id peer.ID) time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastSeen[id]
}

// Subscribe returns a subscription id and a buffered channel of events.
// Buffer size 16 is enough for typical bursts; if a slow consumer fills
// the buffer, additional events are dropped.
//
// The returned channel is closed when Unsubscribe is called with id;
// callers that range over it terminate cleanly. After close, receivers
// observe ok=false on the channel.
func (p *Presence) Subscribe() (int, <-chan PresenceEvent) {
	ch := make(chan PresenceEvent, 16)
	p.subsMu.Lock()
	defer p.subsMu.Unlock()
	id := p.nextID
	p.nextID++
	p.subs[id] = ch
	return id, ch
}

// Unsubscribe stops delivering events to the channel returned by id's
// Subscribe call and closes that channel. Idempotent.
func (p *Presence) Unsubscribe(id int) {
	p.subsMu.Lock()
	defer p.subsMu.Unlock()
	if ch, ok := p.subs[id]; ok {
		delete(p.subs, id)
		close(ch)
	}
}

func (p *Presence) broadcast(ev PresenceEvent) {
	p.subsMu.Lock()
	defer p.subsMu.Unlock()
	for _, ch := range p.subs {
		select {
		case ch <- ev:
		default:
			// Drop on overflow rather than block.
		}
	}
}
