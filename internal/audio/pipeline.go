package audio

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"

	"github.com/pion/rtp"
)

// rtpOpusPayloadType matches WebRTC's Opus PT (111) so any RTP-aware
// debugging tool recognises the stream.
const rtpOpusPayloadType = 111

// PipelineConfig groups the dependencies a Pipeline needs.
type PipelineConfig struct {
	Source         Source
	Sink           Sink
	Transport      Transport
	AEC            *AEC
	Stats          *statsCore
	Bitrate        int
	JitterTargetMs int
	JitterMaxMs    int
}

// Pipeline ties Source → AEC → Encoder → RTP → Transport on the
// outbound side, and Transport → RTP → JitterBuffer → Decoder → AEC.render
// → Sink on the inbound side. One Pipeline per call session.
type Pipeline struct {
	cfg   PipelineConfig
	enc   *OpusEncoder
	dec   *OpusDecoder
	jb    *JitterBuffer
	muted atomic.Bool
	txSeq atomic.Uint32
	txTs  atomic.Uint32
	ssrc  uint32

	pullMu sync.Mutex
	pullQ  [][]int16
}

func NewPipeline(cfg PipelineConfig) (*Pipeline, error) {
	if cfg.Source == nil || cfg.Sink == nil || cfg.Transport == nil ||
		cfg.AEC == nil || cfg.Stats == nil {
		return nil, errors.New("pipeline: missing required dependency")
	}
	if cfg.Bitrate == 0 {
		cfg.Bitrate = 48000
	}
	if cfg.JitterTargetMs == 0 {
		cfg.JitterTargetMs = 60
	}
	if cfg.JitterMaxMs == 0 {
		cfg.JitterMaxMs = 200
	}
	enc, err := NewOpusEncoder(cfg.Bitrate)
	if err != nil {
		return nil, fmt.Errorf("encoder: %w", err)
	}
	dec, err := NewOpusDecoder()
	if err != nil {
		enc.Close()
		return nil, fmt.Errorf("decoder: %w", err)
	}
	jb := NewJitterBuffer(cfg.JitterTargetMs, cfg.JitterMaxMs)
	cfg.Stats.SetEncoderBitrate(cfg.Bitrate)
	return &Pipeline{
		cfg:  cfg,
		enc:  enc,
		dec:  dec,
		jb:   jb,
		ssrc: rand.Uint32(),
	}, nil
}

func (p *Pipeline) SetMuted(muted bool) {
	p.muted.Store(muted)
	p.cfg.Stats.SetMuted(muted)
}

// Start runs all goroutines. Returns when ctx is cancelled.
func (p *Pipeline) Start(ctx context.Context) error {
	defer p.enc.Close()
	defer p.dec.Close()

	if err := p.cfg.Source.Start(p.onCapture); err != nil {
		return fmt.Errorf("source start: %w", err)
	}
	defer p.cfg.Source.Stop()

	go p.recvLoop(ctx)

	if err := p.cfg.Sink.Start(p.onPull); err != nil {
		return fmt.Errorf("sink start: %w", err)
	}
	defer p.cfg.Sink.Stop()

	<-ctx.Done()
	return nil
}

// onCapture is the audio-thread callback. Must not block.
func (p *Pipeline) onCapture(pcm []int16) {
	cleaned := p.cfg.AEC.ProcessCapture(pcm)
	if p.muted.Load() {
		return
	}
	pkt, err := p.enc.Encode(cleaned)
	if err != nil {
		return
	}
	rtpPkt := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    rtpOpusPayloadType,
			SequenceNumber: uint16(p.txSeq.Add(1)),
			Timestamp:      p.txTs.Add(FrameSize),
			SSRC:           p.ssrc,
		},
		Payload: pkt,
	}
	bytes, err := rtpPkt.Marshal()
	if err != nil {
		p.cfg.Stats.ObserveTxDropped()
		return
	}
	if err := p.cfg.Transport.SendMedia(bytes); err != nil {
		p.cfg.Stats.ObserveTxDropped()
		return
	}
	p.cfg.Stats.ObserveTx(len(bytes), cleaned)
}

func (p *Pipeline) recvLoop(ctx context.Context) {
	for {
		dg, err := p.cfg.Transport.RecvMedia(ctx)
		if err != nil {
			return
		}
		var pkt rtp.Packet
		if err := pkt.Unmarshal(dg); err != nil {
			p.cfg.Stats.ObserveRxDropped()
			continue
		}
		p.jb.Push(pkt.SequenceNumber, pkt.Payload)
		opusFrame := p.jb.Pop()
		decoded, err := p.dec.Decode(opusFrame)
		if err != nil {
			p.cfg.Stats.ObserveRxDropped()
			continue
		}
		p.cfg.AEC.ProcessRender(decoded)
		p.pullMu.Lock()
		if len(p.pullQ) >= 4 {
			p.pullQ = p.pullQ[1:]
		}
		p.pullQ = append(p.pullQ, decoded)
		p.pullMu.Unlock()
		p.cfg.Stats.ObserveRx(decoded)

		// Drain peer mute/stats messages opportunistically.
		select {
		case msg := <-p.cfg.Transport.Control():
			if msg.Type == "mute" {
				p.cfg.Stats.SetPeerMuted(msg.Value)
			}
		default:
		}
	}
}

func (p *Pipeline) onPull() []int16 {
	p.pullMu.Lock()
	defer p.pullMu.Unlock()
	if len(p.pullQ) == 0 {
		return make([]int16, FrameSize)
	}
	f := p.pullQ[0]
	p.pullQ = p.pullQ[1:]
	return f
}
