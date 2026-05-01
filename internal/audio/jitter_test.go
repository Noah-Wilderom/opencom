package audio_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/audio"
)

func TestJitter_InOrderPushPop(t *testing.T) {
	t.Parallel()
	jb := audio.NewJitterBuffer(60, 200)
	for i := 0; i < 5; i++ {
		assert.True(t, jb.Push(uint16(i), []byte{byte(i)}))
	}
	got := []byte{}
	for i := 0; i < 5; i++ {
		f := jb.Pop()
		if f != nil {
			got = append(got, f[0])
		}
	}
	assert.Equal(t, []byte{0, 1, 2, 3, 4}, got)
}

func TestJitter_OutOfOrderReorders(t *testing.T) {
	t.Parallel()
	jb := audio.NewJitterBuffer(60, 200)
	jb.Push(0, []byte{0})
	jb.Push(2, []byte{2})
	jb.Push(1, []byte{1})
	jb.Push(3, []byte{3})
	got := []byte{}
	for i := 0; i < 4; i++ {
		f := jb.Pop()
		if f != nil {
			got = append(got, f[0])
		}
	}
	assert.Equal(t, []byte{0, 1, 2, 3}, got)
}

func TestJitter_GapReturnsNilForMissingSeq(t *testing.T) {
	t.Parallel()
	jb := audio.NewJitterBuffer(60, 200)
	jb.Push(0, []byte{0})
	jb.Push(1, []byte{1})
	jb.Push(3, []byte{3})
	got := []interface{}{}
	for i := 0; i < 4; i++ {
		f := jb.Pop()
		if f == nil {
			got = append(got, nil)
		} else {
			got = append(got, f[0])
		}
	}
	assert.Equal(t, []interface{}{byte(0), byte(1), nil, byte(3)}, got)
	assert.Equal(t, uint64(1), jb.Stats().GapsFilled)
}

func TestJitter_DropsFramesPastMax(t *testing.T) {
	t.Parallel()
	jb := audio.NewJitterBuffer(60, 200) // max = 10 frames
	for i := 0; i < 15; i++ {
		jb.Push(uint16(i), []byte{byte(i)})
	}
	assert.Greater(t, jb.Stats().DroppedFull, uint64(0))
	assert.LessOrEqual(t, jb.Stats().Pushed-jb.Stats().DroppedFull-jb.Stats().DroppedLate, uint64(10))
}

func TestJitter_DropsLateFrames(t *testing.T) {
	t.Parallel()
	jb := audio.NewJitterBuffer(60, 200)
	for i := 0; i < 5; i++ {
		jb.Push(uint16(i), []byte{byte(i)})
	}
	for i := 0; i < 3; i++ {
		jb.Pop()
	}
	assert.False(t, jb.Push(1, []byte{1}))
	assert.Equal(t, uint64(1), jb.Stats().DroppedLate)
}

func TestJitter_HandlesSeqWrap(t *testing.T) {
	t.Parallel()
	jb := audio.NewJitterBuffer(60, 200)
	jb.Push(65535, []byte{0xff})
	jb.Push(0, []byte{0x00})
	jb.Push(1, []byte{0x01})
	got := []byte{}
	for i := 0; i < 3; i++ {
		f := jb.Pop()
		if f != nil {
			got = append(got, f[0])
		}
	}
	assert.Equal(t, []byte{0xff, 0x00, 0x01}, got)
}
