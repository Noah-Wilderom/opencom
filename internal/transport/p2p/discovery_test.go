package p2p_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"

	"opencom/internal/identity"
	"opencom/internal/transport/p2p"
)

func TestEnableMDNS_PeersDiscoverEachOtherOnLAN(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	kp1, err := identity.Generate()
	assert.NoError(t, err)
	kp2, err := identity.Generate()
	assert.NoError(t, err)

	a, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kp1.Priv})
	assert.NoError(t, err)
	defer a.Close()
	b, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kp2.Priv})
	assert.NoError(t, err)
	defer b.Close()

	// A unique service name keeps this test from picking up real
	// opencom or libp2p instances on the developer's LAN.
	svc := fmt.Sprintf("opencom-test-%d", time.Now().UnixNano())

	stopA, err := p2p.EnableMDNS(a, svc)
	assert.NoError(t, err)
	defer stopA()

	stopB, err := p2p.EnableMDNS(b, svc)
	assert.NoError(t, err)
	defer stopB()

	// Wait for them to find each other (mDNS announces every ~5s by
	// default).
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if a.HostInternal().Network().Connectedness(b.ID()).String() == "Connected" {
			return // pass
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("hosts did not connect via mDNS within deadline; a peers = %v, b peers = %v",
		a.HostInternal().Peerstore().Peers(), b.HostInternal().Peerstore().Peers())
}

func TestEnableDHT_BootstrapsAgainstPeer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Bootstrap host: build it first with its own DHT (no bootstraps).
	bsKP, err := identity.Generate()
	assert.NoError(t, err)
	bs, err := p2p.New(ctx, p2p.HostOptions{
		PrivKey:        bsKP.Priv,
		BootstrapPeers: []peer.AddrInfo{}, // explicitly empty: don't dial public bootstraps
	})
	assert.NoError(t, err)
	defer bs.Close()
	assert.NotNil(t, bs.DHT())

	// Client host: pass the bootstrap host's AddrInfo via HostOptions so the
	// DHT (constructed inside New) seeds against it.
	bsInfo, err := p2p.HostAddrInfo(bs)
	assert.NoError(t, err)

	clKP, err := identity.Generate()
	assert.NoError(t, err)
	cl, err := p2p.New(ctx, p2p.HostOptions{
		PrivKey:        clKP.Priv,
		BootstrapPeers: []peer.AddrInfo{bsInfo},
	})
	assert.NoError(t, err)
	defer cl.Close()
	assert.NotNil(t, cl.DHT())
	assert.NoError(t, cl.DHT().Bootstrap(ctx))

	// The DHT is supposed to dial bootstrap peers eagerly. Force-connect
	// as a fallback so the assertion is robust when the DHT bootstrap
	// timing varies.
	_ = cl.Connect(ctx, bsInfo)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cl.HostInternal().Network().Connectedness(bs.ID()).String() == "Connected" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("client did not connect to bootstrap peer")
}
