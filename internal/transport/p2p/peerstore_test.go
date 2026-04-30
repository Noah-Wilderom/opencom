package p2p_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/identity"
	"opencom/internal/transport/p2p"
)

func TestSavePeerstore_WritesKnownAddrs(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
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

	bInfo, err := p2p.HostAddrInfo(b)
	assert.NoError(t, err)
	assert.NoError(t, a.Connect(ctx, bInfo))

	dir := t.TempDir()
	assert.NoError(t, p2p.SavePeerstore(a, dir))

	info, err := os.Stat(filepath.Join(dir, "peerstore.json"))
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestLoadPeerstore_RestoresAddrs(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()

	// Phase 1: a connects to b, saves peerstore.
	kp1, err := identity.Generate()
	assert.NoError(t, err)
	kp2, err := identity.Generate()
	assert.NoError(t, err)

	a1, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kp1.Priv})
	assert.NoError(t, err)
	b, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kp2.Priv})
	assert.NoError(t, err)
	defer b.Close()
	bInfo, err := p2p.HostAddrInfo(b)
	assert.NoError(t, err)
	assert.NoError(t, a1.Connect(ctx, bInfo))
	assert.NoError(t, p2p.SavePeerstore(a1, dir))
	a1.Close()

	// Phase 2: a2 (fresh process simulation) loads the peerstore and
	// can see b's address without ever calling Connect.
	a2, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kp1.Priv})
	assert.NoError(t, err)
	defer a2.Close()
	assert.NoError(t, p2p.LoadPeerstore(a2, dir))

	addrs := a2.HostInternal().Peerstore().Addrs(b.ID())
	assert.NotEmpty(t, addrs, "b's addresses should be restored from peerstore.json")
}

func TestLoadPeerstore_MissingFileIsNoOp(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	kp, err := identity.Generate()
	assert.NoError(t, err)
	h, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kp.Priv})
	assert.NoError(t, err)
	defer h.Close()

	assert.NoError(t, p2p.LoadPeerstore(h, t.TempDir()))
}
