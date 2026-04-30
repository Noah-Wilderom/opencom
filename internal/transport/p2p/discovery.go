package p2p

import (
	"context"
	"errors"
	"fmt"
	"time"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	ma "github.com/multiformats/go-multiaddr"
)

// EnableMDNS starts mDNS LAN discovery on h. Discovered peers are added
// to the host's peerstore with permanent TTL and dialed eagerly so the
// connection notifier (Host.Notifier) fires.
func EnableMDNS(h *Host, serviceName string) (stop func() error, err error) {
	if serviceName == "" {
		return nil, fmt.Errorf("mDNS service name must not be empty")
	}
	notifee := &mdnsNotifee{h: h.HostInternal()}
	svc := mdns.NewMdnsService(h.HostInternal(), serviceName, notifee)
	if err := svc.Start(); err != nil {
		return nil, fmt.Errorf("starting mDNS: %w", err)
	}
	return svc.Close, nil
}

type mdnsNotifee struct {
	h interface {
		Connect(ctx context.Context, pi peer.AddrInfo) error
		Peerstore() peerstore.Peerstore
	}
}

func (n *mdnsNotifee) HandlePeerFound(pi peer.AddrInfo) {
	n.h.Peerstore().AddAddrs(pi.ID, pi.Addrs, peerstore.PermanentAddrTTL)
	// Eager dial so callers see Connected events. Errors are non-fatal —
	// the peer might be transiently unreachable.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = n.h.Connect(ctx, pi)
	}()
}

// DHT is the narrow subset of kaddht.IpfsDHT M3 callers need.
type DHT interface {
	Bootstrap(ctx context.Context) error
	PutValue(ctx context.Context, key string, value []byte, opts ...routing.Option) error
	GetValue(ctx context.Context, key string, opts ...routing.Option) ([]byte, error)
}

// Compile-time assertion that *dht.IpfsDHT satisfies our DHT interface.
// Surfaces a clear error if M4 widens DHT in a way that drifts from the
// upstream signature.
var _ DHT = (*dht.IpfsDHT)(nil)

// publicBootstrapAddrs is the standard libp2p / IPFS bootstrap set. These
// nodes are operated by Protocol Labs and have multi-year uptime; the
// daemon falls back to them when no caller-provided bootstraps exist.
var publicBootstrapAddrs = []string{
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmbLHAnMoJPWSCR5Zhtx6BHJX9KiKNN6tpvbUcqanj75Nb",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt",
}

// EnableDHT is an M3-compatibility shim. The DHT is now constructed
// inside p2p.New (wired into libp2p via the Routing option); callers
// should prefer Host.DHT(). The returned stop function is a no-op —
// closing the host (Host.Close) tears down the DHT.
//
// Deprecated: use Host.DHT() and Host.Close() instead.
func EnableDHT(_ context.Context, h *Host) (DHT, func() error, error) {
	if h == nil || h.DHT() == nil {
		return nil, func() error { return nil }, errors.New("host has no DHT")
	}
	return h.DHT(), func() error { return nil }, nil
}

// HostAddrInfo returns h's own AddrInfo (peer ID + listen multiaddrs)
// for use as a bootstrap entry in another host.
func HostAddrInfo(h *Host) (peer.AddrInfo, error) {
	addrs := h.HostInternal().Addrs()
	out := peer.AddrInfo{ID: h.ID(), Addrs: addrs}
	if len(out.Addrs) == 0 {
		return peer.AddrInfo{}, fmt.Errorf("host has no listen addresses")
	}
	return out, nil
}

func publicBootstrapAddrInfo() ([]peer.AddrInfo, error) {
	out := make([]peer.AddrInfo, 0, len(publicBootstrapAddrs))
	for _, s := range publicBootstrapAddrs {
		m, err := ma.NewMultiaddr(s)
		if err != nil {
			return nil, fmt.Errorf("parsing bootstrap %q: %w", s, err)
		}
		info, err := peer.AddrInfoFromP2pAddr(m)
		if err != nil {
			return nil, fmt.Errorf("decoding bootstrap %q: %w", s, err)
		}
		out = append(out, *info)
	}
	return out, nil
}
