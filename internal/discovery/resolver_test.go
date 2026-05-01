package discovery_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/discovery"
	"opencom/internal/friends"
)

// resolverTestRig builds a publisher and resolver against a shared DHT
// and a single friend pair (alice publishes; bob resolves).
type resolverTestRig struct {
	alice, bob       crypto.PrivKey
	alicePub, bobPub crypto.PubKey
	aliceID, bobID   peer.ID
	dht              *fakeDHT
}

func newResolverRig(t *testing.T) resolverTestRig {
	t.Helper()
	alicePriv, alicePub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	bobPriv, bobPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	aliceID, err := peer.IDFromPublicKey(alicePub)
	assert.NoError(t, err)
	bobID, err := peer.IDFromPublicKey(bobPub)
	assert.NoError(t, err)
	return resolverTestRig{
		alice:    alicePriv,
		bob:      bobPriv,
		alicePub: alicePub,
		bobPub:   bobPub,
		aliceID:  aliceID,
		bobID:    bobID,
		dht:      newFakeDHT(),
	}
}

func TestResolver_DHTLookupHappyPath(t *testing.T) {
	t.Parallel()
	rig := newResolverRig(t)

	// Alice publishes one record (her addresses) intended for Bob.
	alicePubBytes, _ := crypto.MarshalPublicKey(rig.alicePub)
	bobPubBytes, _ := crypto.MarshalPublicKey(rig.bobPub)

	aliceFriendsForAlice := &fakeFriendStore{items: []friends.Friend{
		{Name: "Bob", PeerID: rig.bobID, PublicKey: base64.StdEncoding.EncodeToString(bobPubBytes)},
	}}
	pub, err := discovery.NewPublisher(discovery.PublisherOptions{
		DHT:             rig.dht,
		Friends:         aliceFriendsForAlice,
		Signer:          rig.alice,
		SignerPub:       rig.alicePub,
		AddressProvider: &fakeAddrs{addrs: []ma.Multiaddr{mustMA(t, "/ip4/198.51.100.7/tcp/4001")}},
		Log:             zap.NewNop(),
	})
	assert.NoError(t, err)
	assert.NoError(t, pub.PublishOnce(context.Background()))

	// Bob's resolver looks up Alice.
	bobFriends := &fakeFriendStore{items: []friends.Friend{
		{Name: "Alice", PeerID: rig.aliceID, PublicKey: base64.StdEncoding.EncodeToString(alicePubBytes)},
	}}
	cache, err := discovery.OpenCache(filepath.Join(t.TempDir(), "cache.json"))
	assert.NoError(t, err)
	res, err := discovery.NewResolver(discovery.ResolverOptions{
		DHT:     rig.dht,
		Friends: bobFriends,
		Cache:   cache,
		MyPriv:  rig.bob,
		MyPub:   rig.bobPub,
		Log:     zap.NewNop(),
	})
	assert.NoError(t, err)

	addrs, err := res.Resolve(context.Background(), rig.aliceID)
	assert.NoError(t, err)
	assert.Len(t, addrs, 1)
	assert.Equal(t, "/ip4/198.51.100.7/tcp/4001", addrs[0].String())

	// Cache should now be populated.
	entry, ok := cache.Get(rig.aliceID)
	assert.True(t, ok)
	assert.Equal(t, []string{"/ip4/198.51.100.7/tcp/4001"}, entry.Addresses)
}

func TestResolver_CacheHitSkipsDHT(t *testing.T) {
	t.Parallel()
	rig := newResolverRig(t)
	alicePubBytes, _ := crypto.MarshalPublicKey(rig.alicePub)

	bobFriends := &fakeFriendStore{items: []friends.Friend{
		{Name: "Alice", PeerID: rig.aliceID, PublicKey: base64.StdEncoding.EncodeToString(alicePubBytes)},
	}}
	cache, err := discovery.OpenCache(filepath.Join(t.TempDir(), "cache.json"))
	assert.NoError(t, err)
	cache.Set(rig.aliceID, []string{"/ip4/203.0.113.5/tcp/4001"})

	res, err := discovery.NewResolver(discovery.ResolverOptions{
		DHT:     newFakeDHT(), // empty, would fail on a DHT lookup
		Friends: bobFriends,
		Cache:   cache,
		MyPriv:  rig.bob,
		MyPub:   rig.bobPub,
		Log:     zap.NewNop(),
	})
	assert.NoError(t, err)

	addrs, err := res.Resolve(context.Background(), rig.aliceID)
	assert.NoError(t, err)
	assert.Len(t, addrs, 1)
	assert.Equal(t, "/ip4/203.0.113.5/tcp/4001", addrs[0].String())
}

func TestResolver_NotFriend(t *testing.T) {
	t.Parallel()
	rig := newResolverRig(t)

	res, err := discovery.NewResolver(discovery.ResolverOptions{
		DHT:     newFakeDHT(),
		Friends: &fakeFriendStore{},
		Cache:   nil,
		MyPriv:  rig.bob,
		MyPub:   rig.bobPub,
		Log:     zap.NewNop(),
	})
	assert.NoError(t, err)

	_, err = res.Resolve(context.Background(), rig.aliceID)
	assert.ErrorIs(t, err, discovery.ErrNotFriend)
}

func TestResolver_DHTNotFoundIsLookupFailed(t *testing.T) {
	t.Parallel()
	rig := newResolverRig(t)
	alicePubBytes, _ := crypto.MarshalPublicKey(rig.alicePub)

	bobFriends := &fakeFriendStore{items: []friends.Friend{
		{Name: "Alice", PeerID: rig.aliceID, PublicKey: base64.StdEncoding.EncodeToString(alicePubBytes)},
	}}
	cache, err := discovery.OpenCache(filepath.Join(t.TempDir(), "cache.json"))
	assert.NoError(t, err)

	res, err := discovery.NewResolver(discovery.ResolverOptions{
		DHT:     newFakeDHT(), // nothing published
		Friends: bobFriends,
		Cache:   cache,
		MyPriv:  rig.bob,
		MyPub:   rig.bobPub,
		Log:     zap.NewNop(),
	})
	assert.NoError(t, err)

	_, err = res.Resolve(context.Background(), rig.aliceID)
	assert.ErrorIs(t, err, discovery.ErrLookupFailed)
}

func TestResolver_StaleCacheFallsThroughToDHT(t *testing.T) {
	t.Parallel()
	rig := newResolverRig(t)

	alicePubBytes, _ := crypto.MarshalPublicKey(rig.alicePub)
	bobPubBytes, _ := crypto.MarshalPublicKey(rig.bobPub)
	pub, err := discovery.NewPublisher(discovery.PublisherOptions{
		DHT:             rig.dht,
		Friends:         &fakeFriendStore{items: []friends.Friend{{Name: "Bob", PeerID: rig.bobID, PublicKey: base64.StdEncoding.EncodeToString(bobPubBytes)}}},
		Signer:          rig.alice,
		SignerPub:       rig.alicePub,
		AddressProvider: &fakeAddrs{addrs: []ma.Multiaddr{mustMA(t, "/ip4/198.51.100.7/tcp/4001")}},
		Log:             zap.NewNop(),
	})
	assert.NoError(t, err)
	assert.NoError(t, pub.PublishOnce(context.Background()))

	cachePath := filepath.Join(t.TempDir(), "cache.json")
	staleEntry := map[string]any{
		"addresses":  []string{"/ip4/203.0.113.99/tcp/4001"},
		"fetched_at": time.Now().Add(-25 * time.Hour).UTC(),
	}
	stale := map[string]any{
		"version": 1,
		"entries": map[string]any{
			base64.StdEncoding.EncodeToString([]byte(rig.aliceID)): staleEntry,
		},
	}
	data, err := json.MarshalIndent(stale, "", "  ")
	assert.NoError(t, err)
	assert.NoError(t, os.WriteFile(cachePath, data, 0o600))

	cache, err := discovery.OpenCache(cachePath)
	assert.NoError(t, err)
	entry, ok := cache.Get(rig.aliceID)
	assert.True(t, ok)
	assert.Equal(t, []string{"/ip4/203.0.113.99/tcp/4001"}, entry.Addresses)

	bobFriends := &fakeFriendStore{items: []friends.Friend{
		{Name: "Alice", PeerID: rig.aliceID, PublicKey: base64.StdEncoding.EncodeToString(alicePubBytes)},
	}}
	res, err := discovery.NewResolver(discovery.ResolverOptions{
		DHT:     rig.dht,
		Friends: bobFriends,
		Cache:   cache,
		MyPriv:  rig.bob,
		MyPub:   rig.bobPub,
		Log:     zap.NewNop(),
	})
	assert.NoError(t, err)

	addrs, err := res.Resolve(context.Background(), rig.aliceID)
	assert.NoError(t, err)
	assert.Len(t, addrs, 1)
	assert.Equal(t, "/ip4/198.51.100.7/tcp/4001", addrs[0].String())

	entry2, ok := cache.Get(rig.aliceID)
	assert.True(t, ok)
	assert.Equal(t, []string{"/ip4/198.51.100.7/tcp/4001"}, entry2.Addresses)
}

func TestResolver_InvalidateCache(t *testing.T) {
	t.Parallel()
	rig := newResolverRig(t)
	alicePubBytes, _ := crypto.MarshalPublicKey(rig.alicePub)

	bobFriends := &fakeFriendStore{items: []friends.Friend{
		{Name: "Alice", PeerID: rig.aliceID, PublicKey: base64.StdEncoding.EncodeToString(alicePubBytes)},
	}}
	cache, err := discovery.OpenCache(filepath.Join(t.TempDir(), "cache.json"))
	assert.NoError(t, err)
	cache.Set(rig.aliceID, []string{"/ip4/192.0.2.1/tcp/4001"})

	res, err := discovery.NewResolver(discovery.ResolverOptions{
		DHT:     newFakeDHT(),
		Friends: bobFriends,
		Cache:   cache,
		MyPriv:  rig.bob,
		MyPub:   rig.bobPub,
		Log:     zap.NewNop(),
	})
	assert.NoError(t, err)

	res.InvalidateCache(rig.aliceID)
	_, ok := cache.Get(rig.aliceID)
	assert.False(t, ok)
}
