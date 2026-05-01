package audio_test

import (
	"testing"

	"github.com/gen2brain/malgo"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
	opus "gopkg.in/hraban/opus.v2"
)

// TestCgoDepsLink is a build-time smoke test: if any of the cgo deps
// fail to link, this file won't compile and the whole package fails.
// Touching one symbol from each ensures we don't accidentally drop a
// dep when go mod tidy runs.
//
// NOTE: github.com/livekit/echo-cancel (WebRTC AEC3 + NS + AGC) is
// listed in the M8 spec but the repository does not exist on GitHub as
// of 2026-05-01. It is excluded from this test. The user must resolve
// the correct import path before Task 2 (aec.go) can be implemented.
// See "Concerns" section of the Task 1 implementation report.
func TestCgoDepsLink(t *testing.T) {
	// malgo — cross-platform audio I/O (cgo, miniaudio)
	// malgo.Version() does not exist; use SampleSizeInBytes as a
	// build-time symbol touch instead.
	sz := malgo.SampleSizeInBytes(malgo.FormatS16)
	assert.Greater(t, sz, 0)

	// gopkg.in/hraban/opus.v2 — libopus bindings (cgo)
	enc, err := opus.NewEncoder(48000, 1, opus.AppVoIP)
	assert.NoError(t, err)
	assert.NotNil(t, enc)

	// github.com/pion/rtp — RTP packet codec (pure-Go, promoted to direct)
	pkt := &rtp.Packet{}
	assert.NotNil(t, pkt)
}
