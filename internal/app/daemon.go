package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"

	"opencom/internal/call"
	"opencom/internal/discovery"
	"opencom/internal/friends"
	"opencom/internal/invite"
	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
	"opencom/internal/transport/p2p"
)

// parseAddrInfoList parses a YAML-configured list of /p2p/-suffixed
// multiaddrs into peer.AddrInfo. Unparseable entries are logged and
// skipped — a typo in one peer must not crash the daemon.
//
// Returns a non-nil empty slice when all entries fail (or input is
// empty), so callers can distinguish "user configured empty list"
// (disable feature) from "default applies" (nil sentinel).
func parseAddrInfoList(addrs []string, log *zap.Logger, field string) []peer.AddrInfo {
	out := make([]peer.AddrInfo, 0, len(addrs))
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
		out = append(out, *info)
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

	host, err := p2p.New(ctx, p2p.HostOptions{
		PrivKey:        opts.Identity.Priv,
		BootstrapPeers: bootstraps,
		RelayPeers:     relays,
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
	callEngine.Start()
	defer callEngine.Stop()

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

	server := ipc.NewServer(opts.Log, opts.Version)
	server.Register("daemon.status",
		methods.DaemonStatus(opts.Version, opts.Identity, opts.StartedAt, host.ListenAddrs, host.Reachability))
	server.Register("daemon.shutdown", methods.DaemonShutdown(cancel))
	server.Register("identity.get", methods.IdentityGet(opts.Identity, opts.Config))
	server.Register("friends.add", methods.FriendsAdd(store))
	server.Register("friends.list", methods.FriendsList(store, presence))
	server.Register("friends.remove", methods.FriendsRemove(store))
	server.Register("friends.rename", methods.FriendsRename(store))
	server.Register("friends.show", methods.FriendsShow(store, presence))
	server.Register("friends.subscribe_presence", methods.FriendsSubscribePresence(presence))
	server.Register("calls.start", methods.CallsStart(callEngine, callMgr, store))
	server.Register("calls.list", methods.CallsList(callMgr))
	server.Register("calls.attach", methods.CallsAttach(callMgr))
	server.Register("calls.action", methods.CallsAction(callEngine, callMgr))
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
			host.ListenAddrs, host.Reachability, store, presence, callMgr, inviteStore))

	opts.Log.Info("daemon listening",
		zap.String("socket", opts.Paths.SocketPath),
		zap.String("peer_id", opts.Identity.PeerID.String()),
		zap.Strings("listen_addrs", host.ListenAddrs()))

	err = server.Serve(ctx, listener)
	opts.Log.Info("daemon stopped")
	return err
}
