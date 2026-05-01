package audio

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"go.uber.org/zap"
)

// SessionOptions configures one audio session.
type SessionOptions struct {
	CallID         string
	Host           host.Host
	Peer           peer.ID
	InputDevice    string
	OutputDevice   string
	Bitrate        int
	JitterTargetMs int
	JitterMaxMs    int
	AECEnabled     bool
	Log            *zap.Logger
}

// Session orchestrates one call's audio: devices, transport, pipeline,
// stats, mute state.
type Session struct {
	opts      SessionOptions
	pipeline  *Pipeline
	transport Transport
	source    Source
	sink      Sink
	aec       *AEC
	stats     *statsCore
	cancel    context.CancelFunc
	// pipelineDone is closed by the pipeline.Start goroutine when it
	// returns. Close waits on this so Source/Sink aren't stopped
	// concurrently with their Start (race) or after the pipeline's own
	// deferred cleanup (double-stop panic).
	pipelineDone chan struct{}
}

// NewSession opens system audio devices, opens the libp2p transport,
// and starts the pipeline. Returns ErrDatagramsUnavailable if the
// connection to opts.Peer cannot carry datagrams.
func NewSession(ctx context.Context, opts SessionOptions) (*Session, error) {
	src, err := NewMalgoCapturer(opts.InputDevice)
	if err != nil {
		return nil, fmt.Errorf("capturer: %w", err)
	}
	sink, err := NewMalgoPlayer(opts.OutputDevice)
	if err != nil {
		src.Stop()
		return nil, fmt.Errorf("player: %w", err)
	}
	transport, err := NewLibp2pTransport(ctx, opts.Host, opts.Peer)
	if err != nil {
		src.Stop()
		sink.Stop()
		return nil, err
	}
	return NewSessionWithDeps(ctx, opts, src, sink, transport)
}

// NewSessionWithDeps is the dependency-injected constructor used by
// tests. Production code should call NewSession.
func NewSessionWithDeps(ctx context.Context, opts SessionOptions,
	src Source, sink Sink, transport Transport) (*Session, error) {
	if opts.Log == nil {
		opts.Log = zap.NewNop()
	}
	aec, err := NewAEC(opts.AECEnabled)
	if err != nil {
		opts.Log.Warn("audio: AEC init failed; proceeding in bypass mode", zap.Error(err))
		aec, _ = NewAEC(false)
	}
	stats := NewStatsCore()
	pipeline, err := NewPipeline(PipelineConfig{
		Source: src, Sink: sink, Transport: transport, AEC: aec,
		Stats: stats, Bitrate: opts.Bitrate,
		JitterTargetMs: opts.JitterTargetMs, JitterMaxMs: opts.JitterMaxMs,
	})
	if err != nil {
		src.Stop()
		sink.Stop()
		transport.Close()
		aec.Close()
		return nil, fmt.Errorf("pipeline: %w", err)
	}
	pipeCtx, cancel := context.WithCancel(ctx)
	s := &Session{
		opts: opts, pipeline: pipeline, transport: transport,
		source: src, sink: sink, aec: aec, stats: stats, cancel: cancel,
		pipelineDone: make(chan struct{}),
	}
	go func() {
		defer close(s.pipelineDone)
		_ = pipeline.Start(pipeCtx)
	}()
	go s.statsTicker(pipeCtx)
	return s, nil
}

// SetMuted toggles outbound mute and notifies the peer.
func (s *Session) SetMuted(b bool) {
	s.pipeline.SetMuted(b)
	_ = s.transport.SendControl(ControlMessage{Type: "mute", Value: b})
}

// Stats returns the latest snapshot.
func (s *Session) Stats() Stats {
	return s.stats.Snapshot()
}

// Close shuts down the session. All fields may be nil (bare placeholder
// sessions inserted by Manager tests), so every access is nil-guarded.
//
// Order matters: cancel the pipeline context first, then wait for the
// pipeline goroutine to exit (which runs Source.Stop and Sink.Stop in
// its defers), then close the transport and AEC. We deliberately do
// NOT call Source.Stop / Sink.Stop here when a pipeline is running —
// double-stopping would race with the pipeline's setup or cleanup.
// For bare placeholder sessions (no pipeline goroutine), we still
// guard so Close stays safe.
func (s *Session) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.pipelineDone != nil {
		<-s.pipelineDone
	} else {
		// Bare placeholder: no pipeline ran, so its defers won't stop
		// the devices. Stop them ourselves.
		if s.source != nil {
			s.source.Stop()
		}
		if s.sink != nil {
			s.sink.Stop()
		}
	}
	if s.transport != nil {
		_ = s.transport.Close()
	}
	if s.aec != nil {
		s.aec.Close()
	}
}

// statsTicker emits per-second stats over the control stream so the
// peer's CallsListEntry reflects what we're observing locally.
func (s *Session) statsTicker(ctx context.Context) {
	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			snap := s.stats.Snapshot()
			_ = s.transport.SendControl(ControlMessage{Type: "stats", Stats: &snap})
		}
	}
}
