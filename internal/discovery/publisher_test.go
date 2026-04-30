package discovery_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/discovery"
	"opencom/internal/friends"
)

// fakeDHT records all PutValue calls.
type fakeDHT struct {
	mu     sync.Mutex
	put    map[string][]byte
	putErr error // when non-nil, PutValue returns this instead of recording
}

func newFakeDHT() *fakeDHT { return &fakeDHT{put: make(map[string][]byte)} }
func (f *fakeDHT) PutValue(_ context.Context, key string, val []byte, _ ...routing.Option) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return f.putErr
	}
	f.put[key] = append([]byte(nil), val...)
	return nil
}
func (f *fakeDHT) GetValue(_ context.Context, key string, _ ...routing.Option) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.put[key]
	if !ok {
		return nil, assert.AnError
	}
	return v, nil
}
func (f *fakeDHT) putCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.put)
}

type fakeFriendStore struct{ items []friends.Friend }

func (f *fakeFriendStore) List() []friends.Friend { return f.items }

type fakeAddrs struct{ addrs []ma.Multiaddr }

func (f *fakeAddrs) PublicAddrs() []ma.Multiaddr { return f.addrs }

func mustMA(t *testing.T, s string) ma.Multiaddr {
	t.Helper()
	m, err := ma.NewMultiaddr(s)
	assert.NoError(t, err)
	return m
}

func TestPublisher_PublishOnce_OnePerFriend(t *testing.T) {
	t.Parallel()

	signerPriv, signerPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	_, alicePub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	_, bobPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)

	aliceID, err := peer.IDFromPublicKey(alicePub)
	assert.NoError(t, err)
	bobID, err := peer.IDFromPublicKey(bobPub)
	assert.NoError(t, err)

	alicePubBytes, err := crypto.MarshalPublicKey(alicePub)
	assert.NoError(t, err)
	bobPubBytes, err := crypto.MarshalPublicKey(bobPub)
	assert.NoError(t, err)

	store := &fakeFriendStore{items: []friends.Friend{
		{Name: "Alice", PeerID: aliceID, PublicKey: string(alicePubBytes)},
		{Name: "Bob", PeerID: bobID, PublicKey: string(bobPubBytes)},
	}}
	dht := newFakeDHT()
	addrs := &fakeAddrs{addrs: []ma.Multiaddr{mustMA(t, "/ip4/192.0.2.1/tcp/4001")}}

	p, err := discovery.NewPublisher(discovery.PublisherOptions{
		DHT:             dht,
		Friends:         store,
		Signer:          signerPriv,
		SignerPub:       signerPub,
		AddressProvider: addrs,
		Log:             zap.NewNop(),
	})
	assert.NoError(t, err)

	assert.NoError(t, p.PublishOnce(context.Background()))
	assert.Equal(t, 2, dht.putCount(), "one record per friend")
}

func TestPublisher_PublishOnce_NoFriendsNoOp(t *testing.T) {
	t.Parallel()

	signerPriv, signerPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)

	store := &fakeFriendStore{}
	dht := newFakeDHT()
	addrs := &fakeAddrs{}

	p, err := discovery.NewPublisher(discovery.PublisherOptions{
		DHT:             dht,
		Friends:         store,
		Signer:          signerPriv,
		SignerPub:       signerPub,
		AddressProvider: addrs,
		Log:             zap.NewNop(),
	})
	assert.NoError(t, err)

	assert.NoError(t, p.PublishOnce(context.Background()))
	assert.Equal(t, 0, dht.putCount())
}

func TestPublisher_Run_StopsOnContextCancel(t *testing.T) {
	t.Parallel()

	signerPriv, signerPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	_, friendPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	friendID, err := peer.IDFromPublicKey(friendPub)
	assert.NoError(t, err)
	friendPubBytes, err := crypto.MarshalPublicKey(friendPub)
	assert.NoError(t, err)

	store := &fakeFriendStore{items: []friends.Friend{
		{Name: "F", PeerID: friendID, PublicKey: string(friendPubBytes)},
	}}
	dht := newFakeDHT()
	addrs := &fakeAddrs{addrs: []ma.Multiaddr{mustMA(t, "/ip4/192.0.2.1/tcp/4001")}}

	p, err := discovery.NewPublisher(discovery.PublisherOptions{
		DHT:             dht,
		Friends:         store,
		Signer:          signerPriv,
		SignerPub:       signerPub,
		AddressProvider: addrs,
		Log:             zap.NewNop(),
		RefreshInterval: time.Hour, // long, so we only see the immediate publish
	})
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	// Wait long enough for the immediate publish to have happened.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err, "Run should return nil on context cancel")
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}

	assert.GreaterOrEqual(t, dht.putCount(), 1, "Run should have called PublishOnce immediately at startup")
}

func TestPublisher_PublishOnce_SkipsMalformedFriendPubkey(t *testing.T) {
	t.Parallel()

	signerPriv, signerPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	_, alicePub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	aliceID, err := peer.IDFromPublicKey(alicePub)
	assert.NoError(t, err)
	alicePubBytes, err := crypto.MarshalPublicKey(alicePub)
	assert.NoError(t, err)

	// One friend has a malformed pubkey; another is valid.
	store := &fakeFriendStore{items: []friends.Friend{
		{Name: "BadKey", PeerID: peer.ID("bogus"), PublicKey: "garbage"},
		{Name: "Alice", PeerID: aliceID, PublicKey: string(alicePubBytes)},
	}}
	dht := newFakeDHT()
	addrs := &fakeAddrs{addrs: []ma.Multiaddr{mustMA(t, "/ip4/192.0.2.1/tcp/4001")}}

	p, err := discovery.NewPublisher(discovery.PublisherOptions{
		DHT:             dht,
		Friends:         store,
		Signer:          signerPriv,
		SignerPub:       signerPub,
		AddressProvider: addrs,
		Log:             zap.NewNop(),
	})
	assert.NoError(t, err)

	assert.NoError(t, p.PublishOnce(context.Background()))
	// Alice's record should be published; BadKey skipped.
	assert.Equal(t, 1, dht.putCount(), "valid friend published, malformed skipped")
}

func TestPublisher_PublishOnce_LogsAndSkipsDHTPutFailure(t *testing.T) {
	t.Parallel()

	signerPriv, signerPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	_, alicePub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	aliceID, err := peer.IDFromPublicKey(alicePub)
	assert.NoError(t, err)
	alicePubBytes, err := crypto.MarshalPublicKey(alicePub)
	assert.NoError(t, err)

	store := &fakeFriendStore{items: []friends.Friend{
		{Name: "Alice", PeerID: aliceID, PublicKey: string(alicePubBytes)},
	}}
	dht := newFakeDHT()
	dht.putErr = errors.New("simulated DHT failure")
	addrs := &fakeAddrs{addrs: []ma.Multiaddr{mustMA(t, "/ip4/192.0.2.1/tcp/4001")}}

	p, err := discovery.NewPublisher(discovery.PublisherOptions{
		DHT:             dht,
		Friends:         store,
		Signer:          signerPriv,
		SignerPub:       signerPub,
		AddressProvider: addrs,
		Log:             zap.NewNop(),
	})
	assert.NoError(t, err)

	// PublishOnce returns nil even when DHT puts fail — errors are
	// logged and the round completes.
	assert.NoError(t, p.PublishOnce(context.Background()))
	assert.Equal(t, 0, dht.putCount(), "no records persisted on DHT failure")
}
