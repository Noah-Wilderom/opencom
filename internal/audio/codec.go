package audio

import (
	"fmt"

	opus "gopkg.in/hraban/opus.v2"
)

const (
	SampleRate = 48000
	Channels   = 1
	FrameMs    = 20
	FrameSize  = SampleRate * FrameMs / 1000 // 960 samples per 20ms frame
)

// OpusEncoder wraps libopus configured for opencom's voice profile:
// 48 kbps default (overridable per call), inband FEC, DTX, voice-tuned
// signal type, complexity 10 (highest quality, still real-time on
// modern CPUs).
type OpusEncoder struct {
	enc *opus.Encoder
}

// NewOpusEncoder constructs an encoder. bitrate is in bits/sec; 48000
// is the project default. The encoder is stateful — one per direction.
func NewOpusEncoder(bitrate int) (*OpusEncoder, error) {
	enc, err := opus.NewEncoder(SampleRate, Channels, opus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("opus.NewEncoder: %w", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		return nil, fmt.Errorf("SetBitrate(%d): %w", bitrate, err)
	}
	if err := enc.SetInBandFEC(true); err != nil {
		return nil, fmt.Errorf("SetInBandFEC: %w", err)
	}
	if err := enc.SetDTX(true); err != nil {
		return nil, fmt.Errorf("SetDTX: %w", err)
	}
	// hraban/opus exposes complexity via SetComplexity(0..10). If the
	// method doesn't exist on this version of the library, drop this
	// block — defaults are reasonable.
	if err := enc.SetComplexity(10); err != nil {
		return nil, fmt.Errorf("SetComplexity: %w", err)
	}
	return &OpusEncoder{enc: enc}, nil
}

// Encode encodes one 20ms frame of PCM. pcm must be exactly FrameSize
// samples; anything else returns an error.
func (e *OpusEncoder) Encode(pcm []int16) ([]byte, error) {
	if len(pcm) != FrameSize {
		return nil, fmt.Errorf("opus encode: pcm has %d samples, expected %d",
			len(pcm), FrameSize)
	}
	// Opus output is bounded; 1500 bytes is well above any 20ms frame.
	out := make([]byte, 1500)
	n, err := e.enc.Encode(pcm, out)
	if err != nil {
		return nil, fmt.Errorf("opus encode: %w", err)
	}
	return out[:n], nil
}

// Close releases the encoder. hraban/opus does not require explicit
// close; this is a no-op kept for API symmetry with the decoder.
func (e *OpusEncoder) Close() {}

// OpusDecoder wraps libopus's decoder. Stateful — one per direction.
// Passing nil to Decode invokes packet loss concealment.
type OpusDecoder struct {
	dec *opus.Decoder
}

func NewOpusDecoder() (*OpusDecoder, error) {
	dec, err := opus.NewDecoder(SampleRate, Channels)
	if err != nil {
		return nil, fmt.Errorf("opus.NewDecoder: %w", err)
	}
	return &OpusDecoder{dec: dec}, nil
}

// Decode returns FrameSize samples of PCM. If pkt is nil, the decoder
// produces a PLC frame (packet loss concealment) by extrapolating
// from prior state.
func (d *OpusDecoder) Decode(pkt []byte) ([]int16, error) {
	out := make([]int16, FrameSize)
	if pkt == nil {
		err := d.dec.DecodePLC(out)
		if err != nil {
			return nil, fmt.Errorf("opus DecodePLC: %w", err)
		}
		return out, nil
	}
	n, err := d.dec.Decode(pkt, out)
	if err != nil {
		return nil, fmt.Errorf("opus Decode: %w", err)
	}
	if n != FrameSize {
		return nil, fmt.Errorf("opus Decode: got %d samples, expected %d",
			n, FrameSize)
	}
	return out, nil
}

func (d *OpusDecoder) Close() {}
