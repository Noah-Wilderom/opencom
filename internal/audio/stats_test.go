package audio_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/audio"
)

func TestStats_TxObservation(t *testing.T) {
	t.Parallel()
	s := audio.NewStatsCore()
	s.ObserveTx(120, makeSine(440, audio.FrameSize)) // size + pcm for level
	s.ObserveTx(100, makeSine(440, audio.FrameSize))
	snap := s.Snapshot()
	assert.Equal(t, uint64(2), snap.TxPackets)
	assert.GreaterOrEqual(t, snap.TxPeakDBFS, -10)
	assert.LessOrEqual(t, snap.TxPeakDBFS, 0)
}

func TestStats_RxObservation(t *testing.T) {
	t.Parallel()
	s := audio.NewStatsCore()
	s.ObserveRx(makeSine(440, audio.FrameSize))
	s.ObserveRxDropped()
	snap := s.Snapshot()
	assert.Equal(t, uint64(1), snap.RxPackets)
	assert.Equal(t, uint64(1), snap.RxDropped)
	assert.LessOrEqual(t, snap.RxPeakDBFS, 0)
}

func TestStats_MutedReflectsState(t *testing.T) {
	t.Parallel()
	s := audio.NewStatsCore()
	assert.False(t, s.Snapshot().Muted)
	s.SetMuted(true)
	assert.True(t, s.Snapshot().Muted)
	s.SetPeerMuted(true)
	assert.True(t, s.Snapshot().PeerMuted)
}

func TestStats_SilencePeakIsNegInf(t *testing.T) {
	t.Parallel()
	s := audio.NewStatsCore()
	s.ObserveTx(0, make([]int16, audio.FrameSize)) // all zero PCM
	snap := s.Snapshot()
	// We sentinel silence as -100 ("≤ -100 dBFS").
	assert.LessOrEqual(t, snap.TxPeakDBFS, -100)
}
