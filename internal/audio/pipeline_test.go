package audio_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"opencom/internal/audio"
)

// fakeTransport routes datagrams in-process between two pipelines.
type fakeTransport struct {
	out    chan []byte
	in     chan []byte
	ctlOut chan audio.ControlMessage // SendControl writes here (peer's inbox)
	ctlIn  chan audio.ControlMessage // Control() reads from here (our inbox)
}

func newFakePair() (*fakeTransport, *fakeTransport) {
	a2b := make(chan []byte, 64)
	b2a := make(chan []byte, 64)
	ctlA2B := make(chan audio.ControlMessage, 8)
	ctlB2A := make(chan audio.ControlMessage, 8)
	return &fakeTransport{out: a2b, in: b2a, ctlOut: ctlA2B, ctlIn: ctlB2A},
		&fakeTransport{out: b2a, in: a2b, ctlOut: ctlB2A, ctlIn: ctlA2B}
}

func (f *fakeTransport) SendDatagram(b []byte) error {
	cp := append([]byte(nil), b...)
	select {
	case f.out <- cp:
	default:
	}
	return nil
}

func (f *fakeTransport) RecvDatagram(ctx context.Context) ([]byte, error) {
	select {
	case b := <-f.in:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeTransport) SendControl(m audio.ControlMessage) error {
	f.ctlOut <- m
	return nil
}
func (f *fakeTransport) Control() <-chan audio.ControlMessage { return f.ctlIn }
func (f *fakeTransport) Close() error                        { return nil }

// fakeSink collects PCM frames pulled by the pipeline. Stop is
// idempotent and safe to call concurrently — Pipeline.Start defers
// Sink.Stop and Session.Close also calls Sink.Stop, so the contract
// must tolerate both paths firing.
type fakeSink struct {
	mu       sync.Mutex
	frames   [][]int16
	pull     func() []int16
	stop     chan struct{}
	stopOnce sync.Once
}

func (s *fakeSink) Start(pull func() []int16) error {
	s.pull = pull
	s.stop = make(chan struct{})
	s.stopOnce = sync.Once{}
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				pcm := pull()
				s.mu.Lock()
				s.frames = append(s.frames, pcm)
				s.mu.Unlock()
			}
		}
	}()
	return nil
}

func (s *fakeSink) Stop() {
	if s.stop == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stop) })
}

func TestPipeline_EndToEndDeliversAudio(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	frames := make([][]int16, 50)
	for i := range frames {
		frames[i] = makeSine(440, audio.FrameSize)
	}
	srcA := &fakeSource{frames: frames}
	srcB := &fakeSource{frames: nil}
	sinkA := &fakeSink{}
	sinkB := &fakeSink{}
	tA, tB := newFakePair()

	aecA, _ := audio.NewAEC(false)
	aecB, _ := audio.NewAEC(false)

	pipeA, err := audio.NewPipeline(audio.PipelineConfig{
		Source: srcA, Sink: sinkA, Transport: tA, AEC: aecA,
		Stats: audio.NewStatsCore(), Bitrate: 48000,
		JitterTargetMs: 60, JitterMaxMs: 200,
	})
	assert.NoError(t, err)
	pipeB, err := audio.NewPipeline(audio.PipelineConfig{
		Source: srcB, Sink: sinkB, Transport: tB, AEC: aecB,
		Stats: audio.NewStatsCore(), Bitrate: 48000,
		JitterTargetMs: 60, JitterMaxMs: 200,
	})
	assert.NoError(t, err)

	go pipeA.Start(ctx)
	go pipeB.Start(ctx)
	time.Sleep(1 * time.Second)
	cancel()
	time.Sleep(50 * time.Millisecond)

	sinkB.mu.Lock()
	defer sinkB.mu.Unlock()
	assert.GreaterOrEqual(t, len(sinkB.frames), 30)
	gotEnergy := false
	for _, f := range sinkB.frames {
		for _, s := range f {
			if s != 0 {
				gotEnergy = true
				break
			}
		}
		if gotEnergy {
			break
		}
	}
	assert.True(t, gotEnergy, "B should have received decoded audio energy")
}

func TestPipeline_MutedSuppressesTransmission(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	frames := make([][]int16, 20)
	for i := range frames {
		frames[i] = makeSine(440, audio.FrameSize)
	}
	srcA := &fakeSource{frames: frames}
	sinkA := &fakeSink{}
	tA, tB := newFakePair()
	statsA := audio.NewStatsCore()
	aecA, _ := audio.NewAEC(false)

	pipeA, err := audio.NewPipeline(audio.PipelineConfig{
		Source: srcA, Sink: sinkA, Transport: tA, AEC: aecA,
		Stats: statsA, Bitrate: 48000, JitterTargetMs: 60, JitterMaxMs: 200,
	})
	assert.NoError(t, err)
	pipeA.SetMuted(true)

	go pipeA.Start(ctx)
	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case <-tB.in:
		t.Fatal("muted pipeline transmitted a datagram")
	default:
	}
	assert.Equal(t, uint64(0), statsA.Snapshot().TxPackets)
}
