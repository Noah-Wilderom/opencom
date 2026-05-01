package audio_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/audio"
)

func TestSession_StartsAndStops(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	frames := make([][]int16, 10)
	for i := range frames {
		frames[i] = makeSine(440, audio.FrameSize)
	}
	src := &fakeSource{frames: frames}
	sink := &fakeSink{}
	tA, _ := newFakePair()

	sess, err := audio.NewSessionWithDeps(ctx, audio.SessionOptions{
		CallID:         "call-1",
		Bitrate:        48000,
		JitterTargetMs: 60,
		JitterMaxMs:    200,
		AECEnabled:     false,
		Log:            zap.NewNop(),
	}, src, sink, tA)
	assert.NoError(t, err)
	defer sess.Close()

	time.Sleep(150 * time.Millisecond)
	assert.Greater(t, sess.Stats().TxPackets, uint64(0))
}

func TestSession_MuteSendsControlMessage(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	frames := make([][]int16, 10)
	for i := range frames {
		frames[i] = makeSine(440, audio.FrameSize)
	}
	src := &fakeSource{frames: frames}
	sink := &fakeSink{}
	tA, tB := newFakePair()
	sess, err := audio.NewSessionWithDeps(ctx, audio.SessionOptions{
		CallID:         "call-1",
		Bitrate:        48000,
		JitterTargetMs: 60,
		JitterMaxMs:    200,
		AECEnabled:     false,
		Log:            zap.NewNop(),
	}, src, sink, tA)
	assert.NoError(t, err)
	defer sess.Close()

	sess.SetMuted(true)

	select {
	case msg := <-tB.Control():
		assert.Equal(t, "mute", msg.Type)
		assert.True(t, msg.Value)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no mute control message received")
	}
	assert.True(t, sess.Stats().Muted)
}
