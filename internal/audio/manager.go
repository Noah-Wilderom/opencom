package audio

import (
	"context"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"go.uber.org/zap"

	"opencom/internal/call"
)

// CallStateSource is what Manager subscribes to. *call.Manager is the
// production implementation; tests inject a fake.
type CallStateSource interface {
	SubscribeStateChanges() <-chan call.StateChange
	UnsubscribeStateChanges(ch <-chan call.StateChange)
}

type ManagerOptions struct {
	Host   host.Host
	Calls  CallStateSource
	Config ManagerConfig
	Log    *zap.Logger
}

type ManagerConfig struct {
	InputDevice    string
	OutputDevice   string
	Bitrate        int
	JitterTargetMs int
	JitterMaxMs    int
	AECEnabled     bool
}

// Manager owns the callID → *Session map. One per daemon.
type Manager struct {
	opts ManagerOptions

	mu       sync.Mutex
	sessions map[string]*Session
	cancel   context.CancelFunc
}

func NewManager(opts ManagerOptions) (*Manager, error) {
	if opts.Log == nil {
		opts.Log = zap.NewNop()
	}
	return &Manager{
		opts:     opts,
		sessions: make(map[string]*Session),
	}, nil
}

// Start blocks until ctx is cancelled, processing call-state events.
// Spawn this in a goroutine.
func (m *Manager) Start(ctx context.Context) {
	innerCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()
	events := m.opts.Calls.SubscribeStateChanges()
	defer m.opts.Calls.UnsubscribeStateChanges(events)
	for {
		select {
		case <-innerCtx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			switch ev.State {
			case "connected":
				m.startSession(innerCtx, ev)
			case "ended":
				m.stopSession(ev.SessionID)
			}
		}
	}
}

// Stop cancels the Start loop and closes all sessions.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	sessions := m.sessions
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()
	for _, s := range sessions {
		s.Close()
	}
}

func (m *Manager) SetMuted(callID string, muted bool) bool {
	m.mu.Lock()
	s, ok := m.sessions[callID]
	m.mu.Unlock()
	if !ok {
		return false
	}
	s.SetMuted(muted)
	return true
}

func (m *Manager) Stats(callID string) (Stats, bool) {
	m.mu.Lock()
	s, ok := m.sessions[callID]
	m.mu.Unlock()
	if !ok {
		return Stats{}, false
	}
	return s.Stats(), true
}

func (m *Manager) startSession(ctx context.Context, ev call.StateChange) {
	m.mu.Lock()
	if _, exists := m.sessions[ev.SessionID]; exists {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	// Production path requires Host + parsed Peer ID. Task 12 wires in
	// the libp2p host; for now if Host is nil (tests) we register a
	// bare placeholder Session that satisfies Stats()/SetMuted lookup
	// without spinning up real audio.
	if m.opts.Host == nil {
		m.mu.Lock()
		m.sessions[ev.SessionID] = &Session{stats: NewStatsCore()}
		m.mu.Unlock()
		return
	}

	// Production path.
	sess, err := NewSession(ctx, SessionOptions{
		CallID:         ev.SessionID,
		Host:           m.opts.Host,
		Peer:           ev.Remote,
		InputDevice:    m.opts.Config.InputDevice,
		OutputDevice:   m.opts.Config.OutputDevice,
		Bitrate:        m.opts.Config.Bitrate,
		JitterTargetMs: m.opts.Config.JitterTargetMs,
		JitterMaxMs:    m.opts.Config.JitterMaxMs,
		AECEnabled:     m.opts.Config.AECEnabled,
		Log:            m.opts.Log.With(zap.String("call_id", ev.SessionID)),
	})
	if err != nil {
		m.opts.Log.Warn("audio: failed to start session",
			zap.String("call_id", ev.SessionID),
			zap.String("peer", ev.Remote.String()),
			zap.Error(err))
		return
	}
	m.mu.Lock()
	m.sessions[ev.SessionID] = sess
	m.mu.Unlock()
}

// HandleControlStream is the libp2p stream handler for the audio control
// protocol. The daemon registers this via SetStreamHandler so inbound
// /opencom/audio-control/1.0.0 streams from peers are routed to the correct
// Transport's readControlLoop via the per-(host,peer) inbound channel.
//
// When Host is nil (test path) the stream is closed immediately — no real
// transport is running and RegisterStreamHandler was never called.
func (m *Manager) HandleControlStream(stream network.Stream) {
	if m.opts.Host == nil {
		_ = stream.Close()
		return
	}
	remote := stream.Conn().RemotePeer()
	ch := inboundChan(m.opts.Host, remote)
	ch <- stream // buffered (cap 8); practically instant
}

func (m *Manager) stopSession(callID string) {
	m.mu.Lock()
	s, ok := m.sessions[callID]
	if ok {
		delete(m.sessions, callID)
	}
	m.mu.Unlock()
	if ok {
		s.Close()
	}
}
