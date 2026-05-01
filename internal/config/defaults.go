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
	InputDevice    string `yaml:"input_device"`     // "auto" or driver-specific id
	OutputDevice   string `yaml:"output_device"`    // "auto" or driver-specific id
	InputGain      int    `yaml:"input_gain"`       // 0–100 (% of max; 100 = pass-through)
	OutputGain     int    `yaml:"output_gain"`      // 0–100 (% of max; 100 = pass-through)
	Bitrate        int    `yaml:"bitrate"`          // bits per second; Opus 16k–64k (default 48000)
	JitterTargetMs int    `yaml:"jitter_target_ms"` // jitter buffer target depth (default 60)
	JitterMaxMs    int    `yaml:"jitter_max_ms"`    // jitter buffer hard cap before drops (default 200)
	AEC            bool   `yaml:"aec"`              // enable echo cancellation; M8 is pass-through, real impl in M8.5
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

	// ForceReachability bypasses AutoNAT v2's reachability detection.
	// AutoNAT v2 needs ~3 distinct peers running AutoNAT-server to
	// determine reachability with confidence; a fresh client with
	// only the project's bootstrap peer in its routing table never
	// gets that quorum, so AutoRelay never reserves a circuit.
	//
	// Values:
	//   "" / "auto"  – use AutoNAT (only safe with a populated DHT)
	//   "private"    – assume behind NAT; reserve a relay slot
	//                  immediately. Default for clients.
	//   "public"     – assume directly reachable. Used by relay
	//                  nodes that know their public IP.
	ForceReachability string `yaml:"force_reachability"`
}

// DiscoveryConfig configures peer discovery layers.
type DiscoveryConfig struct {
	MDNS bool          `yaml:"mdns"`
	DHT  bool          `yaml:"dht"`
	TTL  time.Duration `yaml:"ttl"` // DHT registration refresh

	// DHTMode controls libp2p-kad-dht mode: "auto" | "server" |
	// "client". Default "auto" picks based on AutoNAT reachability
	// (clients behind NAT default to client mode and don't respond to
	// queries; publicly-reachable nodes default to server mode and
	// participate fully).
	//
	// Bootstrap/relay nodes MUST set this to "server" — auto mode
	// won't promote a fresh node to server until AutoNAT confirms
	// reachability via other peers, which requires the routing table
	// to already be populated (chicken-and-egg).
	DHTMode string `yaml:"dht_mode"`

	// Bootstraps is the list of opencom-protocol DHT bootstrap peers
	// (multiaddrs with /p2p/<peer-id> suffix). Used to seed the
	// /opencom/kad/1.0.0 routing table.
	//
	// LAN-only deployments can leave this empty and rely on mDNS.
	// Cross-network DHT discovery (short-code redemption) requires
	// at least one reachable opencom-protocol bootstrap that is in
	// DHT server mode.
	Bootstraps []string `yaml:"bootstraps"`
}

// RelayConfig configures circuit-relay v2: AutoRelay reservations
// (so peers behind NAT can be reached via a relay) and acting as
// a relay for friends.
type RelayConfig struct {
	Enabled bool `yaml:"enabled"`

	// Peers is the list of relay-v2 nodes the daemon attempts to
	// reserve circuit-relay slots through (multiaddrs with
	// /p2p/<peer-id> suffix). At least one reachable relay is
	// required for cross-network reachability when behind NAT.
	//
	// Defaults to libp2p's public bootstrap nodes, which run
	// relay-v2 services. Self-hosted deployments should override.
	Peers []string `yaml:"peers"`
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

// opencomPublicNode is the project-operated relay + DHT bootstrap node.
// One peer ID, two transports (TCP + QUIC) over IPv4 (via dns4) and IPv6
// (via dns6) for both reachability and resilience.
//
// The same node serves two roles:
//   - relay-v2: clients behind NAT reserve a /p2p-circuit slot through
//     it so they can be dialed cross-network (Relay.Peers).
//   - DHT bootstrap: clients connect to it to seed the opencom DHT
//     routing table, enabling short-code redemption (Discovery.Bootstraps).
//
// To run your own and override these defaults, see deployments/README.md.
var opencomPublicNode = []string{
	"/dns4/opencom.noahwilderom.dev/tcp/4001/p2p/12D3KooWDBH9WwBd79Jjwk1sNenTbvteBeLF6GeDGgY6eyUFyxgJ",
	"/dns4/opencom.noahwilderom.dev/udp/4001/quic-v1/p2p/12D3KooWDBH9WwBd79Jjwk1sNenTbvteBeLF6GeDGgY6eyUFyxgJ",
	"/dns6/opencom.noahwilderom.dev/tcp/4001/p2p/12D3KooWDBH9WwBd79Jjwk1sNenTbvteBeLF6GeDGgY6eyUFyxgJ",
	"/dns6/opencom.noahwilderom.dev/udp/4001/quic-v1/p2p/12D3KooWDBH9WwBd79Jjwk1sNenTbvteBeLF6GeDGgY6eyUFyxgJ",
}

// DefaultRelayPeers returns the public opencom relay node multiaddrs.
// AutoRelay reserves a circuit-relay slot through these so clients
// behind NAT get a /p2p-circuit/... address peers can dial.
//
// Override via config.yaml's relay.peers if running your own relay.
func DefaultRelayPeers() []string {
	out := make([]string, len(opencomPublicNode))
	copy(out, opencomPublicNode)
	return out
}

// DefaultDHTBootstraps returns the public opencom DHT bootstrap node
// multiaddrs. Used to seed the /opencom/kad/1.0.0 routing table so
// short-code (DHT-based) redemption works cross-network. Same nodes
// as DefaultRelayPeers — the project's relay node also runs the DHT.
//
// Override via config.yaml's discovery.bootstraps if running your own.
func DefaultDHTBootstraps() []string {
	out := make([]string, len(opencomPublicNode))
	copy(out, opencomPublicNode)
	return out
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
			InputDevice:    "auto",
			OutputDevice:   "auto",
			InputGain:      100,
			OutputGain:     100,
			Bitrate:        48_000, // M8 default — Discord parity for voice
			JitterTargetMs: 60,
			JitterMaxMs:    200,
			AEC:            true, // M8 ships pass-through; M8.5 swaps in real AEC
		},
		Video: VideoConfig{
			Device:            "auto",
			Resolution:        "640x480",
			Framerate:         30,
			Bitrate:           500_000,
			EnableOnCallStart: true,
		},
		Network: NetworkConfig{
			ListenPort:        0,
			BitrateCap:        0,
			ForceReachability: "private",
		},
		Discovery: DiscoveryConfig{
			MDNS:       true,
			DHT:        true,
			TTL:        10 * time.Minute,
			DHTMode:    "auto",
			Bootstraps: DefaultDHTBootstraps(),
		},
		Relay: RelayConfig{
			Enabled: true,
			Peers:   DefaultRelayPeers(),
		},
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
