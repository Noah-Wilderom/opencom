package audio_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"opencom/internal/audio"
)

// fakeSource implements audio.Source for tests: it spins up a ticker
// that delivers the supplied frames sequentially. Stop is idempotent
// and safe to call concurrently — the malgo-backed Source contract is
// the same, so tests can rely on it.
type fakeSource struct {
	frames   [][]int16
	stop     chan struct{}
	stopOnce sync.Once
}

func (f *fakeSource) Start(onFrame func(pcm []int16)) error {
	f.stop = make(chan struct{})
	f.stopOnce = sync.Once{}
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; ; i++ {
			select {
			case <-f.stop:
				return
			case <-ticker.C:
				if i >= len(f.frames) {
					return
				}
				onFrame(f.frames[i])
			}
		}
	}()
	return nil
}

func (f *fakeSource) Stop() {
	if f.stop == nil {
		return
	}
	f.stopOnce.Do(func() { close(f.stop) })
}

// Smoke test: fakeSource (test fake, no real audio) delivers frames at
// the expected rate. The malgo-backed Source/Sink need real hardware
// to test meaningfully — that's covered in the integration test (Task 16).
// For Task 6 we just verify the interfaces compile and the fake satisfies
// the interface contract.
func TestDevices_FakeSourceDeliversFramesAtRate(t *testing.T) {
	t.Parallel()
	frames := make([][]int16, 5)
	for i := range frames {
		frames[i] = makeSine(440, audio.FrameSize)
	}
	src := &fakeSource{frames: frames}

	var got int32
	err := src.Start(func(pcm []int16) {
		assert.Equal(t, audio.FrameSize, len(pcm))
		atomic.AddInt32(&got, 1)
	})
	assert.NoError(t, err)
	defer src.Stop()

	time.Sleep(150 * time.Millisecond)
	src.Stop()
	assert.GreaterOrEqual(t, atomic.LoadInt32(&got), int32(4))
}

// Compile-time interface satisfaction check.
var _ audio.Source = (*fakeSource)(nil)
