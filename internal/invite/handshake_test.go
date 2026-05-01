package invite_test

import (
	"context"
	"sync"
	"testing"
	"time"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"

	"opencom/internal/identity"
	"opencom/internal/invite"
	"opencom/internal/transport/p2p"
)

func twoHostsHandshake(t *testing.T, ctx context.Context) (a, b *p2p.Host) {
	t.Helper()
	kpA, err := identity.Generate()
	assert.NoError(t, err)
	kpB, err := identity.Generate()
	assert.NoError(t, err)
	hA, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpA.Priv})
	assert.NoError(t, err)
	t.Cleanup(func() { hA.Close() })
	hB, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpB.Priv})
	assert.NoError(t, err)
	t.Cleanup(func() { hB.Close() })
	bInfo, err := p2p.HostAddrInfo(hB)
	assert.NoError(t, err)
	assert.NoError(t, hA.Connect(ctx, bInfo))
	return hA, hB
}

func TestHandshake_HelloRoundTrip(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, b := twoHostsHandshake(t, ctx)

	hello := invite.Hello{
		Type:        invite.TypeRedeem,
		Code:        invite.Code("A7B2X9K4"),
		PeerID:      a.ID().String(),
		PublicKey:   "fake",
		Addresses:   []string{"/ip4/192.0.2.1/tcp/4001"},
		DisplayName: "Alice",
	}

	var wg sync.WaitGroup
	wg.Add(1)
	b.HostInternal().SetStreamHandler(invite.ProtocolID, func(s network.Stream) {
		defer wg.Done()
		defer s.Close()
		h, err := invite.ReadHello(s)
		assert.NoError(t, err)
		assert.Equal(t, hello, h)
		err = invite.SendResponse(s, invite.Response{
			Type:        invite.TypeAccept,
			PeerID:      b.ID().String(),
			DisplayName: "Bob",
		})
		assert.NoError(t, err)
	})

	stream, err := a.HostInternal().NewStream(ctx, b.ID(), invite.ProtocolID)
	assert.NoError(t, err)
	defer stream.Close()
	assert.NoError(t, invite.SendHello(stream, hello))

	resp, err := invite.ReadResponse(stream)
	assert.NoError(t, err)
	assert.Equal(t, invite.TypeAccept, resp.Type)
	assert.Equal(t, "Bob", resp.DisplayName)

	wg.Wait()

	_ = peer.ID("")
	_ = libp2pcrypto.PrivKey(nil)
}

func TestHandshake_ReadResponseRejectsTimeout(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a, b := twoHostsHandshake(t, ctx)

	// B sets a handler that holds the stream open without sending; A
	// should time out reading the response.
	b.HostInternal().SetStreamHandler(invite.ProtocolID, func(s network.Stream) {
		time.Sleep(15 * time.Second)
		_ = s.Close()
	})

	stream, err := a.HostInternal().NewStream(ctx, b.ID(), invite.ProtocolID)
	assert.NoError(t, err)
	defer stream.Close()

	_, err = invite.ReadResponse(stream)
	assert.Error(t, err)
}
