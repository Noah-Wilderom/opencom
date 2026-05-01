package audio

// AEC is the echo cancellation / noise suppression / AGC stage of
// the audio pipeline.
//
// M8 ships a pass-through implementation. The original M8 design
// called for a wrapper around livekit/echo-cancel (WebRTC AEC3) but
// no such Go binding exists in the ecosystem as of 2026. See spec
// §2.3 — M8.5 will swap a real implementation (cgo binding to
// webrtc-audio-processing or SpeexDSP) into this same type without
// changing any caller.
//
// Until then, ProcessCapture returns its input unchanged and
// ProcessRender is a no-op. Users on speakers (no headphones) will
// hear feedback; the CLI surfaces this in `opencom call` startup
// messages (see Task 14).
type AEC struct {
	enabled bool
}

// NewAEC returns a pass-through AEC. The enabled flag is recorded
// but has no effect in M8; callers should still pass true so M8.5
// can ship a real implementation without callers changing.
func NewAEC(enabled bool) (*AEC, error) {
	return &AEC{enabled: enabled}, nil
}

// ProcessCapture is the echo-cancellation step on mic-side PCM. In
// M8 this is the identity function.
func (a *AEC) ProcessCapture(pcm []int16) []int16 {
	return pcm
}

// ProcessRender feeds the speaker-side PCM as a reference signal so
// the AEC can subtract its energy from subsequent ProcessCapture
// calls. In M8 this is a no-op.
func (a *AEC) ProcessRender(pcm []int16) {
	_ = pcm
}

// Close releases native resources. No-op in M8.
func (a *AEC) Close() {}
