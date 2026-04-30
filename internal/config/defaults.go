package config

import (
	"os"
	"time"
)

// UserConfig holds user-facing identity that isn't cryptographic.
type UserConfig struct {
	Name string `yaml:"name"`
}

// AudioConfig configures audio capture, encoding, and playback.
type AudioConfig struct {
	InputDevice  string `yaml:"input_device"`  // "auto" or driver-specific id
	OutputDevice string `yaml:"output_device"` // "auto" or driver-specific id
	InputGain    int    `yaml:"input_gain"`    // 0–100 (% of max; 100 = pass-through)
	OutputGain   int    `yaml:"output_gain"`   // 0–100 (% of max; 100 = pass-through)
	Bitrate      int    `yaml:"bitrate"`       // bits per second; Opus 16k–64k
}

// VideoConfig configures video capture and encoding.
type VideoConfig struct {
	Device            string `yaml:"device"`     // "auto" or driver-specific id
	Resolution        string `yaml:"resolution"` // e.g. "640x480"
	Framerate         int    `yaml:"framerate"`
	Bitrate           int    `yaml:"bitrate"` // bits per second
	EnableOnCallStart bool   `yaml:"enable_on_call_start"`
}

// NetworkConfig configures the libp2p host's listen ports and bandwidth caps.
type NetworkConfig struct {
	ListenPort int `yaml:"listen_port"` // 0 = ephemeral
	BitrateCap int `yaml:"bitrate_cap"` // bits per second; 0 = no cap
}

// DiscoveryConfig configures peer discovery layers.
type DiscoveryConfig struct {
	MDNS bool          `yaml:"mdns"`
	DHT  bool          `yaml:"dht"`
	TTL  time.Duration `yaml:"ttl"` // DHT registration refresh
}

// RelayConfig configures circuit-relay v2 fallback.
type RelayConfig struct {
	Enabled bool `yaml:"enabled"`
}

// UIConfig configures the TUI and notifications.
type UIConfig struct {
	Theme             string `yaml:"theme"` // "dark" | "light" | "auto"
	NotificationSound bool   `yaml:"notification_sound"`
	Ringtone          string `yaml:"ringtone"` // path; "" = use embedded default
}

// DaemonConfig configures the daemon process itself.
type DaemonConfig struct {
	Autostart bool   `yaml:"autostart"`
	LogLevel  string `yaml:"log_level"` // "debug" | "info" | "warn" | "error"
}

// Config is the on-disk configuration loaded from config.yaml.
type Config struct {
	User      UserConfig      `yaml:"user"`
	Audio     AudioConfig     `yaml:"audio"`
	Video     VideoConfig     `yaml:"video"`
	Network   NetworkConfig   `yaml:"network"`
	Discovery DiscoveryConfig `yaml:"discovery"`
	Relay     RelayConfig     `yaml:"relay"`
	UI        UIConfig        `yaml:"ui"`
	Daemon    DaemonConfig    `yaml:"daemon"`
}

// Default returns a Config populated with safe defaults. Used as the seed
// when writing the initial config.yaml and as the fallback for missing keys
// when loading.
//
// Note: User.Name is sourced from $USER and falls back to "user" — the
// returned value depends on process environment, not just the call site.
func Default() Config {
	name := os.Getenv("USER")
	if name == "" {
		name = "user"
	}
	return Config{
		User: UserConfig{Name: name},
		Audio: AudioConfig{
			InputDevice:  "auto",
			OutputDevice: "auto",
			InputGain:    100,
			OutputGain:   100,
			Bitrate:      32_000,
		},
		Video: VideoConfig{
			Device:            "auto",
			Resolution:        "640x480",
			Framerate:         30,
			Bitrate:           500_000,
			EnableOnCallStart: true,
		},
		Network: NetworkConfig{
			ListenPort: 0,
			BitrateCap: 0,
		},
		Discovery: DiscoveryConfig{
			MDNS: true,
			DHT:  true,
			TTL:  10 * time.Minute,
		},
		Relay: RelayConfig{Enabled: true},
		UI: UIConfig{
			Theme:             "auto",
			NotificationSound: true,
			Ringtone:          "",
		},
		Daemon: DaemonConfig{
			Autostart: false,
			LogLevel:  "info",
		},
	}
}
