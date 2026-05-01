package audio

import "sync"

// JitterBuffer reorders out-of-order packets and tolerates loss within
// a bounded window. It does not block; Push always returns immediately,
// and Pop returns nil when it has nothing to deliver this tick.
//
// Thread-safe: Push and Pop may run on different goroutines (typically
// the network read loop and the playback ticker).
//
// Sequence space is uint16 with wrap-around at 65535 → 0. The buffer
// keeps lower-bound aware ordering using signed int16 distance.
type JitterBuffer struct {
	targetFrames int
	maxFrames    int

	mu      sync.Mutex
	frames  map[uint16][]byte
	nextOut uint16
	hasNext bool
	primed  bool
	stats   JitterStats
}

type JitterStats struct {
	Pushed      uint64
	Popped      uint64
	DroppedLate uint64
	DroppedFull uint64
	GapsFilled  uint64
}

// NewJitterBuffer returns a buffer pre-rolled to targetMs of audio
// before delivering the first frame; drops oldest frames once buffered
// audio exceeds maxMs.
func NewJitterBuffer(targetMs, maxMs int) *JitterBuffer {
	return &JitterBuffer{
		targetFrames: targetMs / FrameMs,
		maxFrames:    maxMs / FrameMs,
		frames:       make(map[uint16][]byte),
	}
}

// seqDelta returns the signed distance b-a in modulo-65536 sequence
// space. Positive when b is "after" a; negative when "before".
func seqDelta(a, b uint16) int {
	return int(int16(b - a))
}

// Push inserts pkt at sequence seq. Returns false if the frame is
// rejected (too late, or the buffer is full).
func (jb *JitterBuffer) Push(seq uint16, pkt []byte) bool {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	jb.stats.Pushed++

	if !jb.hasNext {
		jb.nextOut = seq
		jb.hasNext = true
	} else if seqDelta(jb.nextOut, seq) < 0 {
		jb.stats.DroppedLate++
		return false
	}

	jb.frames[seq] = pkt

	// Enforce hard cap.
	for len(jb.frames) > jb.maxFrames {
		oldest := jb.nextOut
		oldestDelta := int(0x7fff)
		for s := range jb.frames {
			d := seqDelta(jb.nextOut, s)
			if d >= 0 && d < oldestDelta {
				oldest = s
				oldestDelta = d
			}
		}
		delete(jb.frames, oldest)
		jb.stats.DroppedFull++
		if oldest == jb.nextOut {
			jb.nextOut++
		}
	}
	return true
}

// Pop returns the next frame in order. nil means either the buffer is
// in pre-roll (not yet primed) or the next expected frame is missing
// (caller renders PLC).
func (jb *JitterBuffer) Pop() []byte {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	if !jb.hasNext {
		return nil
	}
	if !jb.primed {
		if len(jb.frames) < jb.targetFrames {
			return nil
		}
		jb.primed = true
	}
	pkt, ok := jb.frames[jb.nextOut]
	if ok {
		delete(jb.frames, jb.nextOut)
		jb.nextOut++
		jb.stats.Popped++
		return pkt
	}
	jb.nextOut++
	jb.stats.GapsFilled++
	return nil
}

// Stats returns a snapshot.
func (jb *JitterBuffer) Stats() JitterStats {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	return jb.stats
}
