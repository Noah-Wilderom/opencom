package p2p

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p-record"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	libp2ptls "github.com/libp2p/go-libp2p/p2p/security/tls"

	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// Reachability values returned by Host.Reachability. These are the
// exact strings consumers (the IPC daemon.status method) should match
// against.
const (
	ReachabilityPublic  = "public"
	ReachabilityPrivate = "private"
	ReachabilityUnknown = "unknown"
)

// HostOptions configures a libp2p host construction.
//
// BootstrapPeers and RelayPeers are deliberately separate concerns:
//   - BootstrapPeers: peers that speak the /opencom/kad/1.0.0 DHT
//     protocol; used to seed the DHT routing table.
//   - RelayPeers: peers that run libp2p relay-v2; used by AutoRelay
//     to obtain circuit-relay reservations so the local host is
//     reachable from outside its NAT.
//
// Production hosts populate both. Tests typically populate neither
// (empty, non-nil slices) to stay off the public network.
type HostOptions struct {
	PrivKey     crypto.PrivKey
	ListenAddrs []ma.Multiaddr

	// BootstrapPeers seeds the opencom DHT routing table. Default
	// (nil) is empty — opencom does not yet ship public DHT bootstrap
	// nodes, so cross-network DHT discovery requires the user to
	// configure their own. LAN-only deployments use mDNS instead.
	BootstrapPeers []peer.AddrInfo

	// RelayPeers is the list of libp2p relay-v2 nodes used by
	// AutoRelay. At least one reachable relay is required for
	// cross-network reachability when behind NAT. Default (nil) uses
	// the public libp2p bootstrap nodes (see publicBootstrapAddrs)
	// which run relay-v2 services.
	//
	// Pass an empty (non-nil) slice to disable AutoRelay entirely
	// (tests).
	RelayPeers []peer.AddrInfo

	// DHTMode overrides the kad-dht mode. Zero value is dht.ModeAuto,
	// which is correct for production. Tests on tiny loopback networks
	// set this to dht.ModeServer so the routing table populates without
	// waiting for AutoNAT to confirm public reachability (which never
	// happens on a 3-node localhost test).
	DHTMode dht.ModeOpt

	// ForceReachability, when non-zero, bypasses AutoNAT v2 and
	// forces libp2p to treat the host as having the given
	// reachability. Required for AutoRelay to reserve a circuit
	// without a quorum of AutoNAT servers — AutoNAT v2 needs
	// multiple peers to determine reachability with confidence,
	// which a fresh client with only one bootstrap peer doesn't
	// have.
	//
	// Pass network.ReachabilityPrivate for clients that should
	// always reserve a relay slot (typical home/mobile users).
	// Pass network.ReachabilityPublic for the relay node itself.
	// Zero value (network.ReachabilityUnknown) leaves AutoNAT in
	// charge.
	ForceReachability network.Reachability
}

// Host wraps the libp2p host with opencom-specific helpers, including
// the DHT, reachability tracking, and address publishing.
type Host struct {
	h   host.Host
	ddh *dht.IpfsDHT

	reachMu      sync.RWMutex
	reachability network.Reachability

	stopReachability func()
}

// New constructs a libp2p host with TCP+QUIC transports, Noise/TLS,
// the DHT wired into routing, AutoNAT, DCUtR, Circuit Relay v2, and
// NAT-PMP/UPnP. Returns a *Host that exposes both the libp2p host and
// the DHT.
func New(ctx context.Context, opts HostOptions) (*Host, error) {
	if opts.PrivKey == nil {
		return nil, errors.New("HostOptions.PrivKey is required")
	}
	listen := opts.ListenAddrs
	if len(listen) == 0 {
		// Ephemeral on TCP and QUIC v1, IPv4 + IPv6.
		for _, s := range []string{
			"/ip4/0.0.0.0/tcp/0",
			"/ip6/::/tcp/0",
			"/ip4/0.0.0.0/udp/0/quic-v1",
			"/ip6/::/udp/0/quic-v1",
		} {
			m, err := ma.NewMultiaddr(s)
			if err != nil {
				return nil, fmt.Errorf("parsing default listen %q: %w", s, err)
			}
			listen = append(listen, m)
		}
	}
	// BootstrapPeers default to empty: opencom does not yet ship its
	// own DHT bootstrap nodes, and the public IPFS bootstraps speak
	// /ipfs/kad/1.0.0, not /opencom/kad/1.0.0 — they cannot seed our
	// routing table. Cross-network DHT discovery requires the user to
	// configure HostOptions.BootstrapPeers (or cfg.Discovery.Bootstraps)
	// pointing at opencom-protocol DHT nodes.
	bootstraps := opts.BootstrapPeers
	if bootstraps == nil {
		bootstraps = nil
	}

	// RelayPeers default to the public libp2p bootstraps, which run
	// relay-v2. Empty (non-nil) slice disables AutoRelay (tests).
	relays := opts.RelayPeers
	if relays == nil {
		rs, err := publicBootstrapAddrInfo()
		if err != nil {
			return nil, err
		}
		relays = rs
	}

	var ddht *dht.IpfsDHT
	libp2pOpts := []libp2p.Option{
		libp2p.Identity(opts.PrivKey),
		libp2p.ListenAddrs(listen...),
		libp2p.Security(noise.ID, noise.New),
		libp2p.Security(libp2ptls.ID, libp2ptls.New),
		libp2p.DefaultTransports,
		libp2p.NATPortMap(),
		libp2p.EnableHolePunching(),
		libp2p.EnableAutoNATv2(),
		libp2p.EnableRelayService(),
		// libp2p invokes this Routing callback synchronously during New(),
		// before the host is returned, so the local ddht assignment is
		// race-free relative to subsequent ddht usage below.
		libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			var rerr error
			ddht, rerr = dht.New(ctx, h,
				dht.Mode(opts.DHTMode),
				dht.BootstrapPeers(bootstraps...),
				// Use our own protocol prefix so we run a separate DHT
				// mesh from the public IPFS DHT. The default /ipfs prefix
				// enforces validators = exactly {pk, ipns}, which blocks
				// us from registering opencom-discovery / opencom-invite
				// validators. Our records have no business living on the
				// public IPFS DHT anyway.
				dht.ProtocolPrefix("/opencom"),
				// Custom record validators for opencom-managed namespaces.
				// We do AEAD + ed25519 signature verification ourselves at
				// the discovery/invite layer; the DHT's per-namespace
				// validator just needs to accept records under our prefixes.
				// "pk" is preserved (libp2p uses it internally for
				// peer-id-to-pubkey lookups).
				dht.Validator(record.NamespacedValidator{
					"pk":                record.PublicKeyValidator{},
					"opencom-discovery": opencomValidator{},
					"opencom-invite":    opencomValidator{},
				}),
			)
			return ddht, rerr
		}),
	}
	if len(relays) > 0 {
		// AutoRelay reserves circuit-relay slots through these peers,
		// so we get a /p2p-circuit/<relay>/p2p/<our-id> address that
		// peers behind any NAT can dial.
		libp2pOpts = append(libp2pOpts, libp2p.EnableAutoRelayWithStaticRelays(relays))
	}
	// ForceReachability bypasses AutoNAT (which needs multiple peers
	// to converge) and tells AutoRelay to reserve immediately. See
	// HostOptions.ForceReachability docstring.
	switch opts.ForceReachability {
	case network.ReachabilityPrivate:
		libp2pOpts = append(libp2pOpts, libp2p.ForceReachabilityPrivate())
	case network.ReachabilityPublic:
		libp2pOpts = append(libp2pOpts, libp2p.ForceReachabilityPublic())
	}
	libp2pHost, err := libp2p.New(libp2pOpts...)
	if err != nil {
		return nil, fmt.Errorf("constructing libp2p host: %w", err)
	}
	if ddht == nil {
		_ = libp2pHost.Close()
		return nil, errors.New("DHT was not constructed via Routing option")
	}
	// Bootstrap continues asynchronously; failure here is non-fatal.
	_ = ddht.Bootstrap(ctx)

	wrapped := &Host{
		h:            libp2pHost,
		ddh:          ddht,
		reachability: network.ReachabilityUnknown,
	}
	wrapped.stopReachability = wrapped.startReachabilityWatcher()
	return wrapped, nil
}

// startReachabilityWatcher subscribes to local-reachability events and
// updates h.reachability. Returns a function that closes the
// subscription.
func (h *Host) startReachabilityWatcher() func() {
	sub, err := h.h.EventBus().Subscribe(new(event.EvtLocalReachabilityChanged))
	if err != nil {
		return func() {}
	}
	go func() {
		for ev := range sub.Out() {
			e := ev.(event.EvtLocalReachabilityChanged)
			h.reachMu.Lock()
			h.reachability = e.Reachability
			h.reachMu.Unlock()
		}
	}()
	return func() { _ = sub.Close() }
}

// ID returns the host's peer ID.
func (h *Host) ID() peer.ID { return h.h.ID() }

// ListenAddrs returns the host's bound multiaddrs as strings (peer-ID
// suffix included for shareability).
func (h *Host) ListenAddrs() []string {
	addrs := h.h.Addrs()
	out := make([]string, 0, len(addrs))
	id := h.h.ID()
	for _, a := range addrs {
		out = append(out, fmt.Sprintf("%s/p2p/%s", a, id))
	}
	return out
}

// InviteAddrs returns ALL of the host's bound multiaddrs (loopback,
// LAN, public, relay) for embedding in invite records and URLs.
//
// Invites are shared out-of-band by the user; embedding LAN
// addresses is required for the recipient to dial the inviter when
// they're on the same network without an internet round-trip.
// PublicAddrs is for DHT discovery records (which are broadcast to
// untrusted peers); InviteAddrs is for explicitly-shared invite
// payloads.
func (h *Host) InviteAddrs() []ma.Multiaddr { return h.h.Addrs() }

// PublicAddrs returns the host's multiaddrs that are usable from
// the public internet: addresses on routable IP space (per
// manet.IsPublicAddr) plus relay-circuit addresses (/p2p-circuit/...)
// that AutoRelay has reserved on our behalf.
//
// Loopback, link-local, and RFC1918 addresses are filtered out so that
// the discovery publisher (internal/discovery) doesn't broadcast our
// local network topology to friends or the DHT.
func (h *Host) PublicAddrs() []ma.Multiaddr {
	all := h.h.Addrs()
	out := make([]ma.Multiaddr, 0, len(all))
	for _, a := range all {
		if manet.IsPublicAddr(a) || isRelayCircuitAddr(a) {
			out = append(out, a)
		}
	}
	return out
}

// isRelayCircuitAddr reports whether a multiaddr contains a
// /p2p-circuit component (i.e., is a circuit-relay v2 reservation).
// These are reachable from any peer that can dial the relay.
func isRelayCircuitAddr(a ma.Multiaddr) bool {
	for _, p := range a.Protocols() {
		if p.Name == "p2p-circuit" {
			return true
		}
	}
	return false
}

// RelayReservations returns the unique peer IDs of relays that have
// granted us a circuit-relay-v2 reservation. Derived from the host's
// own listen addresses: any address of the form
// /...<relay-multiaddr>/p2p/<RELAY-ID>/p2p-circuit/... reflects an
// active reservation on that relay (libp2p removes these addrs as soon
// as the reservation is dropped).
//
// Used by `daemon status` to surface AutoRelay state explicitly so
// users don't have to grep the listen-addr block for /p2p-circuit/.
func (h *Host) RelayReservations() []peer.ID {
	seen := map[peer.ID]struct{}{}
	out := []peer.ID{}
	for _, a := range h.h.Addrs() {
		id, ok := relayIDFromCircuitAddr(a)
		if !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// relayIDFromCircuitAddr extracts the relay's peer ID from a
// /p2p-circuit multiaddr. The /p2p/<id> component immediately preceding
// /p2p-circuit identifies the relay (the trailing /p2p/<target> after
// /p2p-circuit identifies the destination, which is us).
func relayIDFromCircuitAddr(a ma.Multiaddr) (peer.ID, bool) {
	var (
		lastPeer string
		found    bool
	)
	ma.ForEach(a, func(c ma.Component) bool {
		switch c.Protocol().Name {
		case "p2p":
			lastPeer = c.Value()
		case "p2p-circuit":
			found = lastPeer != ""
			return false // stop iteration
		}
		return true
	})
	if !found {
		return "", false
	}
	id, err := peer.Decode(lastPeer)
	if err != nil {
		return "", false
	}
	return id, true
}

// Connect dials info and adds its addresses to the peerstore.
func (h *Host) Connect(ctx context.Context, info peer.AddrInfo) error {
	return h.h.Connect(ctx, info)
}

// Notifier registers a libp2p network notifiee that calls online when a
// new connection to a peer matching filter is established, and offline
// when the last connection is torn down. Either callback may be nil.
func (h *Host) Notifier(filter func(peer.ID) bool, online, offline func(peer.ID)) {
	if filter == nil {
		filter = func(peer.ID) bool { return true }
	}
	h.h.Network().Notify(&filteredNotifiee{
		filter:  filter,
		online:  online,
		offline: offline,
	})
}

// DHT returns the libp2p Kademlia DHT instance built during New.
func (h *Host) DHT() *dht.IpfsDHT { return h.ddh }

// Reachability returns "public", "private", or "unknown" based on the
// most recent AutoNAT determination.
func (h *Host) Reachability() string {
	h.reachMu.RLock()
	defer h.reachMu.RUnlock()
	switch h.reachability {
	case network.ReachabilityPublic:
		return ReachabilityPublic
	case network.ReachabilityPrivate:
		return ReachabilityPrivate
	default:
		return ReachabilityUnknown
	}
}

// Close shuts down the host and DHT and releases all resources.
func (h *Host) Close() error {
	if h.stopReachability != nil {
		h.stopReachability()
	}
	if h.ddh != nil {
		_ = h.ddh.Close()
	}
	return h.h.Close()
}

// HostInternal is a (deliberately verbose) accessor for the underlying
// libp2p host, used by other package-internal collaborators (Tasks 5–7).
func (h *Host) HostInternal() host.Host { return h.h }

type filteredNotifiee struct {
	filter  func(peer.ID) bool
	online  func(peer.ID)
	offline func(peer.ID)
}

func (n *filteredNotifiee) Listen(_ network.Network, _ ma.Multiaddr)      {}
func (n *filteredNotifiee) ListenClose(_ network.Network, _ ma.Multiaddr) {}

func (n *filteredNotifiee) Connected(_ network.Network, c network.Conn) {
	id := c.RemotePeer()
	if !n.filter(id) {
		return
	}
	if n.online != nil {
		n.online(id)
	}
}

func (n *filteredNotifiee) Disconnected(net network.Network, c network.Conn) {
	id := c.RemotePeer()
	if !n.filter(id) {
		return
	}
	// Only fire offline when the last connection is gone.
	if len(net.ConnsToPeer(id)) > 0 {
		return
	}
	if n.offline != nil {
		n.offline(id)
	}
}

// opencomValidator is the libp2p record validator for the
// /opencom-discovery/v1/... and /opencom-invite/v1/... DHT namespaces.
//
// We accept any record under these prefixes — the discovery and invite
// layers do their own AEAD + ed25519 signature verification on top, so
// the DHT validator's job is only to opt these namespaces in to the
// PutValue/GetValue path. (Without this, libp2p-kad-dht's default
// NamespacedValidator rejects records under unknown namespaces with
// "invalid record keytype".)
type opencomValidator struct{}

func (opencomValidator) Validate(_ string, _ []byte) error                 { return nil }
func (opencomValidator) Select(_ string, _ [][]byte) (int, error)          { return 0, nil }
