package audio_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/audio"
)

// makeSine returns FrameSize samples of a sine wave at f Hz, amplitude 0.5.
func makeSine(f float64, frame int) []int16 {
	out := make([]int16, frame)
	for i := range out {
		t := float64(i) / float64(audio.SampleRate)
		out[i] = int16(0.5 * 32767 * math.Sin(2*math.Pi*f*t))
	}
	return out
}

// correlation returns the Pearson correlation between two equal-length
// int16 slices. 1.0 means identical shape; 0.0 means uncorrelated.
func correlation(a, b []int16) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var sumA, sumB float64
	for i := range a {
		sumA += float64(a[i])
		sumB += float64(b[i])
	}
	meanA := sumA / float64(len(a))
	meanB := sumB / float64(len(b))
	var num, denA, denB float64
	for i := range a {
		da := float64(a[i]) - meanA
		db := float64(b[i]) - meanB
		num += da * db
		denA += da * da
		denB += db * db
	}
	if denA == 0 || denB == 0 {
		return 0
	}
	return num / math.Sqrt(denA*denB)
}

func TestCodec_RoundTripPreservesShape(t *testing.T) {
	t.Parallel()
	enc, err := audio.NewOpusEncoder(48000)
	assert.NoError(t, err)
	defer enc.Close()
	dec, err := audio.NewOpusDecoder()
	assert.NoError(t, err)
	defer dec.Close()

	// Use 10 kHz: Opus AppVoIP uses SILK for voice-range signals (<=8 kHz),
	// which applies adaptive pitch prediction that reshapes the PCM waveform
	// and yields Pearson correlation of only ~0.65 even at high bitrate.
	// At 10 kHz Opus switches to its CELT (MDCT) transform codec which is
	// linear and achieves >0.999 correlation — a better test of "waveform
	// survived the round-trip".  One pre-roll frame seeds the decoder's
	// overlap-add buffer (Opus has a 26.5 ms algorithmic delay at 48 kHz).
	in := makeSine(10000, audio.FrameSize)

	// Pre-roll: encode+decode one frame so the codec is past its initial
	// latency window before the measured frame.
	preRoll, err := enc.Encode(in)
	assert.NoError(t, err)
	_, err = dec.Decode(preRoll)
	assert.NoError(t, err)

	// Measured frame.
	pkt, err := enc.Encode(in)
	assert.NoError(t, err)
	assert.Greater(t, len(pkt), 0, "encoded packet must not be empty")
	out, err := dec.Decode(pkt)
	assert.NoError(t, err)
	assert.Equal(t, audio.FrameSize, len(out))
	// Opus is lossy; correlation > 0.95 means the waveform survived.
	assert.Greater(t, correlation(in, out), 0.95,
		"decoded PCM should correlate strongly with input")
}

func TestCodec_DecodePLCOnNil(t *testing.T) {
	t.Parallel()
	dec, err := audio.NewOpusDecoder()
	assert.NoError(t, err)
	defer dec.Close()
	out, err := dec.Decode(nil) // simulate lost packet
	assert.NoError(t, err)
	assert.Equal(t, audio.FrameSize, len(out))
}

func TestCodec_EncodeRejectsWrongFrameSize(t *testing.T) {
	t.Parallel()
	enc, err := audio.NewOpusEncoder(48000)
	assert.NoError(t, err)
	defer enc.Close()
	_, err = enc.Encode(make([]int16, 480)) // 10ms instead of 20ms
	assert.Error(t, err)
}
