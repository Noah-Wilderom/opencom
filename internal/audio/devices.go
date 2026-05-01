package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/gen2brain/malgo"
)

// Source delivers PCM frames to a consumer. The callback runs on the
// audio thread; do not block.
type Source interface {
	Start(onFrame func(pcm []int16)) error
	Stop()
}

// Sink consumes PCM frames. pull is invoked by the audio thread when
// it needs the next frame; return FrameSize samples (zeros for silence).
type Sink interface {
	Start(pull func() []int16) error
	Stop()
}

// malgoCtx is the shared malgo context. malgo requires one shared
// context per process; reusing it avoids reinitialising the audio
// subsystem on every device open.
var (
	malgoCtxOnce sync.Once
	malgoCtx     *malgo.AllocatedContext
	malgoCtxErr  error
)

func sharedMalgoCtx() (*malgo.AllocatedContext, error) {
	malgoCtxOnce.Do(func() {
		malgoCtx, malgoCtxErr = malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	})
	return malgoCtx, malgoCtxErr
}

// bytesToInt16LE converts a little-endian byte slice into int16 samples.
// Assumes len(b) is even.
func bytesToInt16LE(b []byte) []int16 {
	n := len(b) / 2
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(binary.LittleEndian.Uint16(b[i*2:]))
	}
	return out
}

// int16ToBytesLE converts int16 samples to a little-endian byte slice.
func int16ToBytesLE(samples []int16) []byte {
	out := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(s))
	}
	return out
}

type malgoCapturer struct {
	device *malgo.Device
}

// NewMalgoCapturer returns a Source backed by the default capture device.
// Pass "" or "auto" to use the system default microphone.
func NewMalgoCapturer(deviceName string) (Source, error) {
	if deviceName != "" && deviceName != "auto" {
		return nil, fmt.Errorf("named audio devices not supported in M8 (requested %q)", deviceName)
	}
	if _, err := sharedMalgoCtx(); err != nil {
		return nil, fmt.Errorf("malgo init: %w", err)
	}
	return &malgoCapturer{}, nil
}

func (c *malgoCapturer) Start(onFrame func(pcm []int16)) error {
	if c.device != nil {
		return errors.New("capturer already started")
	}
	ctx, err := sharedMalgoCtx()
	if err != nil {
		return err
	}
	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = Channels
	cfg.SampleRate = SampleRate
	cfg.PeriodSizeInFrames = FrameSize

	cb := malgo.DeviceCallbacks{
		Data: func(pOutputSample, pInputSamples []byte, framecount uint32) {
			// Capture: pInputSamples contains the mic data.
			if int(framecount) != FrameSize {
				// Drop short/long frames in M8; an accumulator can be
				// added later if telemetry shows we need it.
				return
			}
			onFrame(bytesToInt16LE(pInputSamples))
		},
	}
	dev, err := malgo.InitDevice(ctx.Context, cfg, cb)
	if err != nil {
		return fmt.Errorf("malgo InitDevice (capture): %w", err)
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		return fmt.Errorf("malgo Start (capture): %w", err)
	}
	c.device = dev
	return nil
}

func (c *malgoCapturer) Stop() {
	if c.device == nil {
		return
	}
	_ = c.device.Stop()
	c.device.Uninit()
	c.device = nil
}

type malgoPlayer struct {
	device *malgo.Device
}

// NewMalgoPlayer returns a Sink backed by the default playback device.
// Pass "" or "auto" to use the system default speaker/headphones.
func NewMalgoPlayer(deviceName string) (Sink, error) {
	if deviceName != "" && deviceName != "auto" {
		return nil, fmt.Errorf("named audio devices not supported in M8 (requested %q)", deviceName)
	}
	if _, err := sharedMalgoCtx(); err != nil {
		return nil, fmt.Errorf("malgo init: %w", err)
	}
	return &malgoPlayer{}, nil
}

func (p *malgoPlayer) Start(pull func() []int16) error {
	if p.device != nil {
		return errors.New("player already started")
	}
	ctx, err := sharedMalgoCtx()
	if err != nil {
		return err
	}
	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = Channels
	cfg.SampleRate = SampleRate
	cfg.PeriodSizeInFrames = FrameSize

	cb := malgo.DeviceCallbacks{
		Data: func(pOutputSample, pInputSamples []byte, framecount uint32) {
			pcm := pull()
			if pcm == nil || len(pcm) != int(framecount)*Channels {
				// Underrun or wrong size: fill with silence.
				for i := range pOutputSample {
					pOutputSample[i] = 0
				}
				return
			}
			data := int16ToBytesLE(pcm)
			copy(pOutputSample, data)
		},
	}
	dev, err := malgo.InitDevice(ctx.Context, cfg, cb)
	if err != nil {
		return fmt.Errorf("malgo InitDevice (playback): %w", err)
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		return fmt.Errorf("malgo Start (playback): %w", err)
	}
	p.device = dev
	return nil
}

func (p *malgoPlayer) Stop() {
	if p.device == nil {
		return
	}
	_ = p.device.Stop()
	p.device.Uninit()
	p.device = nil
}
