package audio

import (
	"math"
	"sync"
	"sync/atomic"
)

// Stats is a snapshot of audio counters at a moment in time, intended
// for emission over IPC.
type Stats struct {
	TxPackets      uint64
	TxDropped      uint64
	TxPeakDBFS     int
	RxPackets      uint64
	RxDropped      uint64
	RxPeakDBFS     int
	JitterMsEWMA   float64
	EncoderBitrate int
	Muted          bool
	PeerMuted      bool
}

// statsCore is the live counter set updated from pipeline goroutines.
// Hot-path methods (ObserveTx, ObserveRx*) avoid locks via atomics
// where they can; peak computation runs under the mutex.
type statsCore struct {
	txPackets atomic.Uint64
	txDropped atomic.Uint64
	rxPackets atomic.Uint64
	rxDropped atomic.Uint64

	mu             sync.Mutex
	txPeak, rxPeak int     // dBFS, -100 = silence sentinel
	jitterEWMA     float64 // ms
	bitrate        int
	muted          bool
	peerMuted      bool
}

// NewStatsCore returns a fresh statsCore. One per Session.
func NewStatsCore() *statsCore {
	return &statsCore{txPeak: -100, rxPeak: -100}
}

// ObserveTx records an outbound RTP packet. encodedSize is informational;
// pcm is the PCM frame BEFORE encoding, used to compute peak level.
func (s *statsCore) ObserveTx(encodedSize int, pcm []int16) {
	s.txPackets.Add(1)
	peak := peakDBFS(pcm)
	s.mu.Lock()
	if peak > s.txPeak {
		s.txPeak = peak
	}
	s.mu.Unlock()
}

func (s *statsCore) ObserveTxDropped() {
	s.txDropped.Add(1)
}

// ObserveRx records an inbound decoded PCM frame.
func (s *statsCore) ObserveRx(pcm []int16) {
	s.rxPackets.Add(1)
	peak := peakDBFS(pcm)
	s.mu.Lock()
	if peak > s.rxPeak {
		s.rxPeak = peak
	}
	s.mu.Unlock()
}

func (s *statsCore) ObserveRxDropped() {
	s.rxDropped.Add(1)
}

// ObserveJitter feeds an EWMA filter with alpha=0.2.
func (s *statsCore) ObserveJitter(ms float64) {
	s.mu.Lock()
	s.jitterEWMA = 0.8*s.jitterEWMA + 0.2*ms
	s.mu.Unlock()
}

func (s *statsCore) SetEncoderBitrate(bitrate int) {
	s.mu.Lock()
	s.bitrate = bitrate
	s.mu.Unlock()
}

func (s *statsCore) SetMuted(b bool) {
	s.mu.Lock()
	s.muted = b
	s.mu.Unlock()
}

func (s *statsCore) SetPeerMuted(b bool) {
	s.mu.Lock()
	s.peerMuted = b
	s.mu.Unlock()
}

// Snapshot returns a stable Stats copy. Resets the rolling peaks so
// the next interval's peaks reflect that interval, not lifetime maxes.
func (s *statsCore) Snapshot() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Stats{
		TxPackets:      s.txPackets.Load(),
		TxDropped:      s.txDropped.Load(),
		TxPeakDBFS:     s.txPeak,
		RxPackets:      s.rxPackets.Load(),
		RxDropped:      s.rxDropped.Load(),
		RxPeakDBFS:     s.rxPeak,
		JitterMsEWMA:   s.jitterEWMA,
		EncoderBitrate: s.bitrate,
		Muted:          s.muted,
		PeerMuted:      s.peerMuted,
	}
	// Roll peaks so subsequent snapshots reflect new audio, not history.
	s.txPeak = -100
	s.rxPeak = -100
	return out
}

// peakDBFS returns the peak amplitude of pcm in dBFS, capped at -100
// for total silence. Full-scale (32767) maps to 0 dBFS.
func peakDBFS(pcm []int16) int {
	var peak int32
	for _, s := range pcm {
		v := int32(s)
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	if peak == 0 {
		return -100
	}
	db := 20 * math.Log10(float64(peak)/32767.0)
	if db < -100 {
		db = -100
	}
	return int(math.Round(db))
}
