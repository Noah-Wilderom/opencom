package audio_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/audio"
)

func TestAEC_NewSucceedsBothEnabledAndDisabled(t *testing.T) {
	t.Parallel()
	a, err := audio.NewAEC(true)
	assert.NoError(t, err)
	assert.NotNil(t, a)
	a.Close()

	b, err := audio.NewAEC(false)
	assert.NoError(t, err)
	assert.NotNil(t, b)
	b.Close()
}

func TestAEC_ProcessCaptureIsPassThrough(t *testing.T) {
	t.Parallel()
	a, err := audio.NewAEC(true)
	assert.NoError(t, err)
	defer a.Close()

	in := makeSine(440, audio.FrameSize)
	out := a.ProcessCapture(in)
	assert.Equal(t, in, out, "M8 AEC must be pass-through (real AEC ships in M8.5)")
}

func TestAEC_ProcessRenderDoesNotPanic(t *testing.T) {
	t.Parallel()
	a, err := audio.NewAEC(true)
	assert.NoError(t, err)
	defer a.Close()

	pcm := makeSine(440, audio.FrameSize)
	for i := 0; i < 50; i++ {
		a.ProcessRender(pcm)
	}
}

func TestAEC_CloseIsIdempotent(t *testing.T) {
	t.Parallel()
	a, err := audio.NewAEC(true)
	assert.NoError(t, err)
	a.Close()
	a.Close() // second close must not panic
}
