package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"opencom/internal/config"
)

func TestDefault_DiscoveryHasMDNSAndDHTOn(t *testing.T) {
	t.Parallel()

	c := config.Default()
	assert.True(t, c.Discovery.MDNS, "mDNS should be on by default")
	assert.True(t, c.Discovery.DHT, "DHT should be on by default")
	assert.Equal(t, 10*time.Minute, c.Discovery.TTL)
}

func TestDefault_RelayEnabled(t *testing.T) {
	t.Parallel()

	c := config.Default()
	assert.True(t, c.Relay.Enabled)
}

func TestDefault_AudioBitrateSensible(t *testing.T) {
	t.Parallel()

	c := config.Default()
	assert.Equal(t, 48_000, c.Audio.Bitrate, "audio default 48 kbps (M8 — Discord parity for voice)")
	assert.Equal(t, "auto", c.Audio.InputDevice)
	assert.Equal(t, "auto", c.Audio.OutputDevice)
	assert.Equal(t, 60, c.Audio.JitterTargetMs)
	assert.Equal(t, 200, c.Audio.JitterMaxMs)
	assert.True(t, c.Audio.AEC)
}

func TestDefault_VideoSensible(t *testing.T) {
	t.Parallel()

	c := config.Default()
	assert.Equal(t, 500_000, c.Video.Bitrate, "video default 500 kbps")
	assert.Equal(t, "640x480", c.Video.Resolution)
	assert.Equal(t, 30, c.Video.Framerate)
	assert.Equal(t, "auto", c.Video.Device)
	assert.True(t, c.Video.EnableOnCallStart)
}

func TestDefault_DaemonAutostartOff(t *testing.T) {
	t.Parallel()

	c := config.Default()
	assert.False(t, c.Daemon.Autostart, "autostart is opt-in")
	assert.Equal(t, "info", c.Daemon.LogLevel)
}

func TestDefault_UserNameNonEmpty(t *testing.T) {
	t.Parallel()

	c := config.Default()
	assert.NotEqual(t, "", c.User.Name)
}
