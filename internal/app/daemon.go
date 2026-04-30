//go:build unix

package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/peer"
	"go.uber.org/zap"

	"opencom/internal/call"
	"opencom/internal/discovery"
	"opencom/internal/friends"
	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
	"opencom/internal/transport/p2p"
)

// Run starts the daemon: PID file, libp2p host, friends + presence,
// IPC socket, JSON-RPC server. Runs until ctx is canceled or the
// daemon.shutdown method is invoked. Resources are cleaned up on return.
func Run(ctx context.Context, opts Options) error {
	if dir := filepath.Dir(opts.Paths.SocketPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating socket parent dir %s: %w", dir, err)
		}
	}

	pidPath := opts.Paths.SocketPath + ".pid"
	release, err := AcquirePIDFile(pidPath)
	if err != nil {
		return err
	}
	defer func() { _ = release() }()

	store, err := friends.Open(opts.Paths.FriendsFile)
	if err != nil {
		return fmt.Errorf("opening friends store: %w", err)
	}
	presence := friends.NewPresence(nil)

	peerCache, err := discovery.OpenCache(opts.Paths.PeerCache)
	if err != nil {
		return fmt.Errorf("opening peer cache: %w", err)
	}

	host, err := p2p.New(ctx, p2p.HostOptions{
		PrivKey:        opts.Identity.Priv,
		BootstrapPeers: opts.HostBootstraps,
	})
	if err != nil {
		return fmt.Errorf("constructing libp2p host: %w", err)
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

	if !opts.DisableMDNS {
		stopMDNS, err := p2p.EnableMDNS(host, "opencom")
		if err != nil {
			opts.Log.Warn("enabling mDNS failed", zap.Error(err))
		} else {
			defer func() { _ = stopMDNS() }()
		}
	}

	if err := os.Remove(opts.Paths.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		opts.Log.Warn("could not remove stale socket",
			zap.String("path", opts.Paths.SocketPath),
			zap.Error(err))
	}
	listener, err := net.Listen("unix", opts.Paths.SocketPath)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", opts.Paths.SocketPath, err)
	}
	defer listener.Close()
	defer func() { _ = os.Remove(opts.Paths.SocketPath) }()
	if err := os.Chmod(opts.Paths.SocketPath, 0o600); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}

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

	opts.Log.Info("daemon listening",
		zap.String("socket", opts.Paths.SocketPath),
		zap.String("peer_id", opts.Identity.PeerID.String()),
		zap.Strings("listen_addrs", host.ListenAddrs()))

	err = server.Serve(ctx, listener)
	opts.Log.Info("daemon stopped")
	return err
}
