package p2p_test

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"

	"opencom/internal/identity"
	"opencom/internal/transport/p2p"
)

func TestNew_RequiresPrivKey(t *testing.T) {
	t.Parallel()

	_, err := p2p.New(context.Background(), p2p.HostOptions{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "PrivKey")
}

func TestNew_HostHasOurPeerID(t *testing.T) {
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kp.Priv})
	assert.NoError(t, err)
	defer h.Close()

	assert.Equal(t, kp.PeerID, h.ID())
}

func TestNew_HostListensOnLocalAddresses(t *testing.T) {
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kp.Priv})
	assert.NoError(t, err)
	defer h.Close()

	addrs := h.ListenAddrs()
	assert.NotEmpty(t, addrs)
}

func TestNew_TwoHostsCanConnect(t *testing.T) {
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

	// Round-trip through ListenAddrs() (which formats with /p2p/<id>) so
	// the published-string path is exercised end-to-end.
	bAddrs := b.ListenAddrs()
	assert.NotEmpty(t, bAddrs)
	bInfo := peer.AddrInfo{ID: b.ID()}
	for _, s := range bAddrs {
		m, err := ma.NewMultiaddr(s)
		assert.NoError(t, err)
		// Strip the /p2p/<id> suffix so AddrInfo carries plain transport addrs.
		transport, _ := peer.SplitAddr(m)
		if transport != nil {
			bInfo.Addrs = append(bInfo.Addrs, transport)
		}
	}
	assert.NotEmpty(t, bInfo.Addrs)
	assert.NoError(t, a.Connect(ctx, bInfo))
}

func TestNew_ReturnsDHT(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	priv, _, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	h, err := p2p.New(ctx, p2p.HostOptions{PrivKey: priv})
	assert.NoError(t, err)
	defer h.Close()

	assert.NotNil(t, h.DHT(), "DHT must be constructed")
}

func TestNew_ReachabilityIsString(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	priv, _, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	h, err := p2p.New(ctx, p2p.HostOptions{PrivKey: priv})
	assert.NoError(t, err)
	defer h.Close()

	r := h.Reachability()
	assert.Contains(t, []string{"public", "private", "unknown"}, r)
}

func TestNew_PublicAddrsFiltersLoopback(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	priv, _, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	h, err := p2p.New(ctx, p2p.HostOptions{PrivKey: priv})
	assert.NoError(t, err)
	defer h.Close()

	addrs := h.PublicAddrs()
	for _, a := range addrs {
		s := a.String()
		assert.NotContains(t, s, "127.0.0.1", "loopback IPv4 must be filtered out")
		assert.NotContains(t, s, "/ip6/::1/", "loopback IPv6 must be filtered out")
	}
}

func TestHost_NotifierFiresOnConnect(t *testing.T) {
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

	connected := make(chan peer.ID, 1)
	a.Notifier(func(_ peer.ID) bool { return true }, func(id peer.ID) {
		select {
		case connected <- id:
		default:
		}
	}, nil)

	bInfo := peer.AddrInfo{ID: b.ID(), Addrs: b.HostInternal().Addrs()}
	assert.NoError(t, a.Connect(ctx, bInfo))

	select {
	case id := <-connected:
		assert.Equal(t, b.ID(), id)
	case <-time.After(2 * time.Second):
		t.Fatal("notifier did not fire")
	}
}
