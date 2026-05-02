package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	ma "github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"

	"opencom/internal/audio"
	"opencom/internal/call"
	"opencom/internal/discovery"
	"opencom/internal/friends"
	"opencom/internal/invite"
	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
	"opencom/internal/notify"
	"opencom/internal/transport/p2p"
	"opencom/internal/version"
)

// parseDHTMode maps the "auto"|"server"|"client" string from
// cfg.Discovery.DHTMode to libp2p-kad-dht's mode option. Empty string
// (zero-value) maps to ModeAuto. An unknown value returns ModeAuto
// plus an error so the caller can warn.
func parseDHTMode(s string) (dht.ModeOpt, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return dht.ModeAuto, nil
	case "server":
		return dht.ModeServer, nil
	case "client":
		return dht.ModeClient, nil
	default:
		return dht.ModeAuto, fmt.Errorf("unknown dht mode %q (valid: auto, server, client)", s)
	}
}

// parseReachability maps cfg.Network.ForceReachability into a
// libp2p network.Reachability. Empty/"auto" → Unknown (no override).
func parseReachability(s string) (network.Reachability, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return network.ReachabilityUnknown, nil
	case "private":
		return network.ReachabilityPrivate, nil
	case "public":
		return network.ReachabilityPublic, nil
	default:
		return network.ReachabilityUnknown, fmt.Errorf("unknown reachability %q (valid: auto, private, public)", s)
	}
}

// parseAddrInfoList parses a YAML-configured list of /p2p/-suffixed
// multiaddrs into peer.AddrInfo, MERGING multiaddrs that share the
// same peer ID into a single AddrInfo with all of that peer's
// addresses. Unparseable entries are logged and skipped — a typo in
// one peer must not crash the daemon.
//
// Merging is essential for kad-dht / AutoRelay: passing N separate
// AddrInfo entries for the same peer means downstream code only sees
// one set of addresses (peerstore dedups by peer ID), so a host
// without IPv6 internet would fail every bootstrap attempt because
// libp2p only tried the dns6 address.
//
// Returns a non-nil empty slice when all entries fail (or input is
// empty), so callers can distinguish "user configured empty list"
// (disable feature) from "default applies" (nil sentinel).
func parseAddrInfoList(addrs []string, log *zap.Logger, field string) []peer.AddrInfo {
	byPeer := make(map[peer.ID]*peer.AddrInfo)
	order := make([]peer.ID, 0, len(addrs))
	for _, s := range addrs {
		m, err := ma.NewMultiaddr(s)
		if err != nil {
			log.Warn("skipping malformed multiaddr in config",
				zap.String("field", field), zap.String("addr", s), zap.Error(err))
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(m)
		if err != nil {
			log.Warn("skipping multiaddr with no /p2p/ peer-id suffix",
				zap.String("field", field), zap.String("addr", s), zap.Error(err))
			continue
		}
		if existing, ok := byPeer[info.ID]; ok {
			existing.Addrs = append(existing.Addrs, info.Addrs...)
		} else {
			merged := *info
			byPeer[info.ID] = &merged
			order = append(order, info.ID)
		}
	}
	out := make([]peer.AddrInfo, 0, len(byPeer))
	for _, id := range order {
		out = append(out, *byPeer[id])
	}
	return out
}

// Run starts the daemon: libp2p host, friends + presence, IPC socket,
// JSON-RPC server. Runs until ctx is canceled or the daemon.shutdown
// method is invoked. Resources are cleaned up on return. Single-instance
// is enforced by ipc.Listen failing if the IPC path is already in use —
// no separate file lock is needed.
func Run(ctx context.Context, opts Options) error {
	if dir := filepath.Dir(opts.Paths.SocketPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating socket parent dir %s: %w", dir, err)
		}
	}

	store, err := friends.Open(opts.Paths.FriendsFile)
	if err != nil {
		return fmt.Errorf("opening friends store: %w", err)
	}
	presence := friends.NewPresence(nil)

	peerCache, err := discovery.OpenCache(opts.Paths.PeerCache)
	if err != nil {
		return fmt.Errorf("opening peer cache: %w", err)
	}

	inviteStore, err := invite.OpenStore(opts.Paths.ActiveInvites)
	if err != nil {
		return fmt.Errorf("opening invite store: %w", err)
	}

	bootstraps := opts.HostBootstraps
	if bootstraps == nil {
		bootstraps = parseAddrInfoList(opts.Config.Discovery.Bootstraps, opts.Log, "discovery.bootstraps")
	}
	relays := opts.HostRelays
	if relays == nil {
		relays = parseAddrInfoList(opts.Config.Relay.Peers, opts.Log, "relay.peers")
	}

	// Stable listen port for relay/bootstrap nodes: when network.listen_port
	// is non-zero, bind explicitly on TCP+QUIC, IPv4+IPv6 instead of the
	// ephemeral default. Required for public nodes whose multiaddr is
	// referenced from other peers' config.
	var listenAddrs []ma.Multiaddr
	if port := opts.Config.Network.ListenPort; port > 0 {
		for _, s := range []string{
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port),
			fmt.Sprintf("/ip6/::/tcp/%d", port),
			fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", port),
			fmt.Sprintf("/ip6/::/udp/%d/quic-v1", port),
		} {
			m, err := ma.NewMultiaddr(s)
			if err != nil {
				return fmt.Errorf("parsing listen addr %q: %w", s, err)
			}
			listenAddrs = append(listenAddrs, m)
		}
	}

	dhtMode, err := parseDHTMode(opts.Config.Discovery.DHTMode)
	if err != nil {
		opts.Log.Warn("invalid discovery.dht_mode, using auto",
			zap.String("value", opts.Config.Discovery.DHTMode), zap.Error(err))
	}
	forceReach, err := parseReachability(opts.Config.Network.ForceReachability)
	if err != nil {
		opts.Log.Warn("invalid network.force_reachability, using auto (AutoNAT)",
			zap.String("value", opts.Config.Network.ForceReachability), zap.Error(err))
	}

	host, err := p2p.New(ctx, p2p.HostOptions{
		PrivKey:               opts.Identity.Priv,
		ListenAddrs:           listenAddrs,
		BootstrapPeers:        bootstraps,
		RelayPeers:            relays,
		DHTMode:               dhtMode,
		ForceReachability:     forceReach,
		RelayServiceUnlimited: opts.Config.Relay.Unlimited,
	})
	if err != nil {
		return fmt.Errorf("constructing libp2p host: %w", err)
	}
	// Two distinct gaps to surface separately:
	//   - empty DHT bootstraps → short-code (DHT) redemption can't
	//     traverse cross-network until opencom ships its own DHT
	//     bootstrap nodes (M8) or the user configures one.
	//   - empty relay set → AutoRelay can't reserve a circuit, so
	//     the host has no NAT-traversable address; URL invites then
	//     only work LAN-to-LAN.
	if len(bootstraps) == 0 {
		opts.Log.Info("opencom DHT discovery is disabled (no bootstraps); " +
			"short codes (OPEN-XXXX-XXXX) only redeem on the same DHT mesh — " +
			"set discovery.bootstraps in config.yaml to enable cross-network " +
			"short-code lookup. URL invites continue to work without DHT.")
	}
	if len(relays) == 0 {
		opts.Log.Warn("no relay peers configured (relay.peers); " +
			"cross-network reachability will fail unless this host has a " +
			"directly-routable public address. Set relay.peers in config.yaml " +
			"or rely on the default public libp2p relays.")
	}
	defer host.Close()

	if err := os.MkdirAll(opts.Paths.Peerstore, 0o700); err != nil {
		return fmt.Errorf("creating peerstore dir: %w", err)
	}
	if err := p2p.LoadPeerstore(host, opts.Paths.Peerstore); err != nil {
		opts.Log.Warn("loading peerstore failed", zap.Error(err))
	}
	defer func() {
		if err := p2p.SavePeerstore(host, opts.Paths.Peerstore); err != nil {
			opts.Log.Warn("saving peerstore failed", zap.Error(err))
		}
	}()

	host.Notifier(
		func(id peer.ID) bool {
			_, ok := store.GetByPeerID(id)
			return ok
		},
		func(id peer.ID) { presence.MarkOnline(id) },
		func(id peer.ID) { presence.MarkOffline(id) },
	)

	resolver, err := discovery.NewResolver(discovery.ResolverOptions{
		DHT:     host.DHT(),
		Friends: store,
		Cache:   peerCache,
		MyPriv:  opts.Identity.Priv,
		MyPub:   opts.Identity.Pub,
		Log:     opts.Log,
	})
	if err != nil {
		return fmt.Errorf("constructing resolver: %w", err)
	}

	publisher, err := discovery.NewPublisher(discovery.PublisherOptions{
		DHT:             host.DHT(),
		Friends:         store,
		Signer:          opts.Identity.Priv,
		SignerPub:       opts.Identity.Pub,
		AddressProvider: host,
		Log:             opts.Log,
	})
	if err != nil {
		return fmt.Errorf("constructing publisher: %w", err)
	}

	// No goroutine drains callMgr.Inbound(): inbound calls require explicit
	// user consent via calls.action (the user runs `opencom call accept <id>`).
	// Auto-accepting would let any peer who knows our peer ID barge in.
	callMgr := call.NewManager()
	callEngine := call.NewEngine(host, callMgr, opts.Log, nil)
	callEngine.SetResolver(resolver)
	// Hand the engine the same relay list AutoRelay uses so Place can
	// add /<relay>/p2p-circuit fallback addresses to the peerstore for
	// the dial target. Without this, libp2p only tries whatever direct
	// addresses it cached, which are typically stale or LAN-only after
	// a daemon restart, and the call times out without ever attempting
	// the relay path.
	callEngine.SetRelays(relays)
	callEngine.Start()
	defer callEngine.Stop()

	// Audio plane (M8). Skipped when DisableAudio is set (CLI/integration
	// tests that exercise call control without real audio hardware —
	// multiple parallel malgo init crashes PulseAudio's mainloop).
	var audioMgr *audio.Manager
	if !opts.DisableAudio {
		audioMgr, err = audio.NewManager(audio.ManagerOptions{
			Host:  host.HostInternal(),
			Calls: callMgr,
			Config: audio.ManagerConfig{
				InputDevice:    opts.Config.Audio.InputDevice,
				OutputDevice:   opts.Config.Audio.OutputDevice,
				Bitrate:        opts.Config.Audio.Bitrate,
				JitterTargetMs: opts.Config.Audio.JitterTargetMs,
				JitterMaxMs:    opts.Config.Audio.JitterMaxMs,
				AECEnabled:     opts.Config.Audio.AEC,
			},
			Log: opts.Log.Named("audio"),
		})
		if err != nil {
			return fmt.Errorf("constructing audio manager: %w", err)
		}
		go audioMgr.Start(ctx)
		defer audioMgr.Stop()

		// Register stream handlers for both audio protocols
		// (control + media) on this host. Inbound streams are routed
		// via audio's per-(host, proto, peer) registry to whichever
		// libp2pTransport is waiting for them.
		audio.RegisterStreamHandler(host.HostInternal())
		defer host.HostInternal().RemoveStreamHandler(audio.AudioControlProtocol)
		defer host.HostInternal().RemoveStreamHandler(audio.AudioMediaProtocol)
	}

	// Build nil-safe interface values for audio-dependent IPC handlers.
	// We deliberately avoid boxing a (*audio.Manager)(nil) into an interface,
	// which would make the interface non-nil despite holding a nil pointer.
	var audioMuter methods.AudioMuter
	var audioStatter methods.AudioStatter
	if audioMgr != nil {
		audioMuter = audioMgr
		audioStatter = audioMgr
	}

	// Desktop notifications on call state changes (incoming call,
	// outgoing call, connected, ended). Disabled when ui.notifications
	// is false — typically on relay/server profiles. Best-effort: a
	// missing display server (headless Linux without DBus) silently
	// no-ops via beeep's internal error path.
	var notifier notify.Notifier = notify.Disabled{}
	if opts.Config.UI.Notifications {
		notifier = notify.Beeep{Log: opts.Log.Named("notify")}
	}
	go notify.WatchCalls(ctx, callMgr, store, notifier)

	inviteMgr, err := invite.NewManager(invite.ManagerOptions{
		Host:        host,
		DHT:         host.DHT(),
		Friends:     store,
		Store:       inviteStore,
		Identity:    opts.Identity.Priv,
		IdentityPub: opts.Identity.Pub,
		Log:         opts.Log,
		DisplayName: opts.Config.User.Name,
	})
	if err != nil {
		return fmt.Errorf("constructing invite manager: %w", err)
	}
	inviteMgr.Start()
	defer inviteMgr.Stop()

	if !opts.DisableMDNS {
		stopMDNS, err := p2p.EnableMDNS(host, "opencom")
		if err != nil {
			opts.Log.Warn("enabling mDNS failed", zap.Error(err))
		} else {
			defer func() { _ = stopMDNS() }()
		}
	}

	listener, err := ipc.Listen(opts.Paths.SocketPath)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", opts.Paths.SocketPath, err)
	}
	defer listener.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		if err := publisher.Run(ctx); err != nil {
			opts.Log.Warn("discovery publisher exited", zap.Error(err))
		}
	}()

	// Re-publish discovery records when the host's address set changes
	// (NAT lease renewal, network switch, AutoNAT confirmation), so peers
	// can rediscover us before the 4h refresh ticker.
	addrSub, err := host.HostInternal().EventBus().Subscribe(new(event.EvtLocalAddressesUpdated))
	if err != nil {
		opts.Log.Warn("subscribing to address-change events failed", zap.Error(err))
	} else {
		go func() {
			defer addrSub.Close()
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-addrSub.Out():
					if !ok {
						return
					}
					if err := publisher.PublishOnce(ctx); err != nil {
						opts.Log.Warn("address-change publish failed", zap.Error(err))
					}
				}
			}
		}()
	}

	// Background version checker: refreshes the latest-release cache
	// on a slow cadence so version.check IPC requests are served from
	// disk without ever blocking the CLI on a network round-trip.
	versionChecker := version.New(opts.Paths.StateDir)
	go runVersionChecker(ctx, versionChecker, opts.Log.Named("version"))

	server := ipc.NewServer(opts.Log, opts.Version)
	server.Register("daemon.status",
		methods.DaemonStatus(opts.Version, opts.Identity, opts.StartedAt, host.ListenAddrs, host.Reachability, host.RelayReservations))
	server.Register("daemon.shutdown", methods.DaemonShutdown(cancel))
	server.Register("version.check", methods.VersionCheck(opts.Version, versionChecker))
	server.Register("identity.get", methods.IdentityGet(opts.Identity, opts.Config))
	server.Register("friends.add", methods.FriendsAdd(store))
	server.Register("friends.list", methods.FriendsList(store, presence))
	server.Register("friends.remove", methods.FriendsRemove(store))
	server.Register("friends.rename", methods.FriendsRename(store))
	server.Register("friends.show", methods.FriendsShow(store, presence))
	server.Register("friends.subscribe_presence", methods.FriendsSubscribePresence(presence))
	server.Register("calls.start", methods.CallsStart(callEngine, callMgr, store))
	server.Register("calls.list", methods.CallsList(callMgr, audioStatter))
	server.Register("calls.attach", methods.CallsAttach(callMgr))
	server.Register("calls.action", methods.CallsAction(callEngine, callMgr, audioMuter))
	// Reachable addrs = public + relay-circuit. Used by `opencom invite`
	// to warn the user if no cross-network address has been reserved yet.
	reachableAddrs := func() []string {
		ms := host.PublicAddrs()
		out := make([]string, 0, len(ms))
		for _, m := range ms {
			out = append(out, m.String())
		}
		return out
	}
	server.Register("invite.create", methods.InviteCreate(inviteMgr, reachableAddrs))
	server.Register("invite.list", methods.InviteList(inviteStore))
	server.Register("invite.revoke", methods.InviteRevoke(inviteMgr))
	server.Register("invite.redeem", methods.InviteRedeem(inviteMgr))
	server.Register("daemon.status_summary",
		methods.DaemonStatusSummary(opts.Version, opts.Identity, opts.StartedAt,
			host.ListenAddrs, host.Reachability, host.RelayReservations, store, presence, callMgr, inviteStore))

	opts.Log.Info("daemon listening",
		zap.String("socket", opts.Paths.SocketPath),
		zap.String("peer_id", opts.Identity.PeerID.String()),
		zap.Strings("listen_addrs", host.ListenAddrs()))

	err = server.Serve(ctx, listener)
	opts.Log.Info("daemon stopped")
	return err
}

// runVersionChecker refreshes the latest-release cache once at startup
// (so the first CLI invocation after a daemon restart gets a fresh
// answer instead of the stale pre-restart cache) and then on
// version.CheckInterval ticks. Errors are logged at debug level only
// — a transient GitHub-API hiccup is never fatal to the daemon.
func runVersionChecker(ctx context.Context, c *version.Checker, log *zap.Logger) {
	refresh := func() {
		rctx, cancel := context.WithTimeout(ctx, version.HTTPTimeout)
		defer cancel()
		if _, err := c.Refresh(rctx); err != nil {
			log.Debug("version check failed", zap.Error(err))
		}
	}
	refresh()
	tk := time.NewTicker(version.CheckInterval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			refresh()
		}
	}
}
