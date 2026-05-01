package invite_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/friends"
	"opencom/internal/identity"
	"opencom/internal/invite"
	"opencom/internal/transport/p2p"
)

// allAddrsProvider exposes the host's full address set for tests.
// Production manager uses host.InviteAddrs() which is also unfiltered;
// this struct exists so tests can choose the address-source explicitly.
type allAddrsProvider struct{ h *p2p.Host }

func (a allAddrsProvider) InviteAddrs() []ma.Multiaddr { return a.h.HostInternal().Addrs() }

type fakeDHTMgr struct {
	mu        sync.Mutex
	put       map[string][]byte
	failPut   bool // when true, PutValue returns ErrPutFailed
	failGet   bool // when true, GetValue returns ErrGetFailed
}

var (
	errFakePutFailed = assert.AnError
	errFakeGetFailed = assert.AnError
)

func newFakeDHTMgr() *fakeDHTMgr { return &fakeDHTMgr{put: make(map[string][]byte)} }
func (f *fakeDHTMgr) PutValue(_ context.Context, key string, val []byte, _ ...routing.Option) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPut {
		return errFakePutFailed
	}
	f.put[key] = append([]byte(nil), val...)
	return nil
}
func (f *fakeDHTMgr) GetValue(_ context.Context, key string, _ ...routing.Option) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failGet {
		return nil, errFakeGetFailed
	}
	v, ok := f.put[key]
	if !ok {
		return nil, assert.AnError
	}
	return v, nil
}

type fakeFriendStoreMgr struct {
	mu    sync.Mutex
	added []friends.Friend
}

func (f *fakeFriendStoreMgr) Add(fr friends.Friend) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, fr)
	return nil
}

type managerRig struct {
	alice, bob         *invite.Manager
	hA, hB             *p2p.Host
	storeA, storeB     *invite.Store
	friendsA, friendsB *fakeFriendStoreMgr
	dht                *fakeDHTMgr
}

func newManagerRig(t *testing.T, ctx context.Context) *managerRig {
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

	storeA, err := invite.OpenStore(filepath.Join(t.TempDir(), "a.json"))
	assert.NoError(t, err)
	storeB, err := invite.OpenStore(filepath.Join(t.TempDir(), "b.json"))
	assert.NoError(t, err)
	friendsA := &fakeFriendStoreMgr{}
	friendsB := &fakeFriendStoreMgr{}
	dht := newFakeDHTMgr()

	mA, err := invite.NewManager(invite.ManagerOptions{
		Host: hA, DHT: dht, Friends: friendsA, Store: storeA,
		Identity: kpA.Priv, IdentityPub: kpA.Pub, Log: zap.NewNop(),
		DisplayName:     "alice",
		AddressProvider: allAddrsProvider{hA},
	})
	assert.NoError(t, err)
	mB, err := invite.NewManager(invite.ManagerOptions{
		Host: hB, DHT: dht, Friends: friendsB, Store: storeB,
		Identity: kpB.Priv, IdentityPub: kpB.Pub, Log: zap.NewNop(),
		DisplayName:     "bob",
		AddressProvider: allAddrsProvider{hB},
	})
	assert.NoError(t, err)
	mA.Start()
	mB.Start()
	t.Cleanup(func() { mA.Stop(); mB.Stop() })

	return &managerRig{alice: mA, bob: mB, hA: hA, hB: hB,
		storeA: storeA, storeB: storeB, friendsA: friendsA, friendsB: friendsB, dht: dht}
}

func TestManager_CreateAndRedeem_HappyPath(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rig := newManagerRig(t, ctx)

	res, err := rig.alice.Create(ctx, 30*time.Minute)
	assert.NoError(t, err)
	assert.NotEmpty(t, res.URL)
	assert.False(t, res.ExpiresAt.IsZero())

	added, err := rig.bob.Redeem(ctx, res.Code)
	assert.NoError(t, err)
	assert.Equal(t, rig.hA.ID(), added.PeerID)

	e, ok := rig.storeA.Get(res.Code)
	assert.True(t, ok)
	assert.True(t, e.Consumed)
	assert.Equal(t, rig.hB.ID().String(), e.ConsumedBy)

	rig.friendsA.mu.Lock()
	defer rig.friendsA.mu.Unlock()
	rig.friendsB.mu.Lock()
	defer rig.friendsB.mu.Unlock()
	assert.Len(t, rig.friendsA.added, 1, "Alice should have added Bob")
	assert.Len(t, rig.friendsB.added, 1, "Bob should have added Alice")
	assert.Equal(t, rig.hB.ID(), rig.friendsA.added[0].PeerID)
	assert.Equal(t, rig.hA.ID(), rig.friendsB.added[0].PeerID)
	assert.Equal(t, "bob", rig.friendsA.added[0].Name, "Alice should know Bob's display name")
	assert.Equal(t, "alice", rig.friendsB.added[0].Name, "Bob should know Alice's display name")

	_ = peer.ID("")
	_ = libp2pcrypto.PrivKey(nil)
}

func TestManager_Redeem_RejectsConsumedTwice(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rig := newManagerRig(t, ctx)

	res, err := rig.alice.Create(ctx, 30*time.Minute)
	assert.NoError(t, err)

	_, err = rig.bob.Redeem(ctx, res.Code)
	assert.NoError(t, err)

	_, err = rig.bob.Redeem(ctx, res.Code)
	assert.Error(t, err)
}

func TestManager_Redeem_RejectsUnknownCode(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rig := newManagerRig(t, ctx)

	bogus := invite.Code("AAAAAAA0")
	_, err := rig.bob.Redeem(ctx, bogus)
	assert.Error(t, err)
}

func TestManager_Revoke(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rig := newManagerRig(t, ctx)

	res, err := rig.alice.Create(ctx, 30*time.Minute)
	assert.NoError(t, err)

	assert.True(t, rig.alice.Revoke(res.Code), "revoking known code should return true")

	_, err = rig.bob.Redeem(ctx, res.Code)
	assert.Error(t, err)

	// Re-revoke should report unknown.
	assert.False(t, rig.alice.Revoke(res.Code), "revoking unknown code should return false")
}

// TestManager_RedeemURL_WorksWithoutDHT proves the URL form is fully
// self-contained: even with the DHT completely broken (Get/Put both
// fail), URL-based redemption succeeds because pubkey+signature are
// embedded in the URL itself.
func TestManager_RedeemURL_WorksWithoutDHT(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rig := newManagerRig(t, ctx)

	// First create the invite while DHT is healthy so the local store
	// records the code; the URL form is what we'll later redeem.
	res, err := rig.alice.Create(ctx, 30*time.Minute)
	assert.NoError(t, err)
	assert.NotEmpty(t, res.URL)

	// Now break the DHT entirely. Bob redeems via URL — must succeed.
	rig.dht.mu.Lock()
	rig.dht.failGet = true
	rig.dht.failPut = true
	rig.dht.mu.Unlock()

	added, err := rig.bob.RedeemURL(ctx, res.URL)
	assert.NoError(t, err, "RedeemURL must work without DHT")
	assert.Equal(t, rig.hA.ID(), added.PeerID)
	assert.Equal(t, "alice", added.Name)
}

// TestManager_Create_NonFatalDHTFailure confirms Create still returns
// a usable invite (with DHTPublishErr set) when DHT publish fails.
func TestManager_Create_NonFatalDHTFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rig := newManagerRig(t, ctx)
	rig.dht.mu.Lock()
	rig.dht.failPut = true
	rig.dht.mu.Unlock()

	res, err := rig.alice.Create(ctx, 30*time.Minute)
	assert.NoError(t, err, "Create must NOT fail when DHT publish fails")
	assert.NotEmpty(t, res.URL, "URL must still be returned")
	assert.NotEmpty(t, res.Code, "code must still be returned")
	assert.Error(t, res.DHTPublishErr, "DHTPublishErr must surface the failure")

	// And Bob can still redeem via URL.
	added, err := rig.bob.RedeemURL(ctx, res.URL)
	assert.NoError(t, err)
	assert.Equal(t, rig.hA.ID(), added.PeerID)
}
