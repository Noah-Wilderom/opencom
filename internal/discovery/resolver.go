package discovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"

	"opencom/internal/friends"
)

// Sentinel errors.
var (
	ErrNotFriend    = errors.New("peer is not a friend")
	ErrLookupFailed = errors.New("DHT lookup failed")
)

// DefaultLookupTTL is the maximum age of a cached entry that the
// resolver will accept without re-querying the DHT.
const DefaultLookupTTL = 24 * time.Hour

// DefaultLookupTimeout is the per-DHT-lookup deadline.
const DefaultLookupTimeout = 10 * time.Second

// ResolverOptions configures a Resolver.
type ResolverOptions struct {
	DHT       DHT
	Friends   FriendStore
	Cache     *Cache
	MyPriv    crypto.PrivKey
	MyPub     crypto.PubKey
	Log       *zap.Logger
	LookupTTL time.Duration
	Timeout   time.Duration
}

// Resolver returns the current addresses for a friend by peer ID,
// trying its disk cache first and falling back to a DHT lookup.
type Resolver struct {
	dht     DHT
	friends FriendStore
	cache   *Cache
	myPriv  crypto.PrivKey
	myPub   crypto.PubKey
	log     *zap.Logger
	ttl     time.Duration
	timeout time.Duration
}

// NewResolver constructs a Resolver. Returns an error if any required
// option is missing.
func NewResolver(opts ResolverOptions) (*Resolver, error) {
	if opts.DHT == nil {
		return nil, errors.New("ResolverOptions.DHT is required")
	}
	if opts.Friends == nil {
		return nil, errors.New("ResolverOptions.Friends is required")
	}
	if opts.MyPriv == nil || opts.MyPub == nil {
		return nil, errors.New("ResolverOptions.MyPriv and MyPub are required")
	}
	if opts.Log == nil {
		opts.Log = zap.NewNop()
	}
	if opts.LookupTTL == 0 {
		opts.LookupTTL = DefaultLookupTTL
	}
	if opts.Timeout == 0 {
		opts.Timeout = DefaultLookupTimeout
	}
	return &Resolver{
		dht:     opts.DHT,
		friends: opts.Friends,
		cache:   opts.Cache,
		myPriv:  opts.MyPriv,
		myPub:   opts.MyPub,
		log:     opts.Log,
		ttl:     opts.LookupTTL,
		timeout: opts.Timeout,
	}, nil
}

// Resolve returns current addresses for target. Tries cache → DHT.
func (r *Resolver) Resolve(ctx context.Context, target peer.ID) ([]ma.Multiaddr, error) {
	if r.cache != nil {
		if entry, ok := r.cache.Get(target); ok && time.Since(entry.FetchedAt) < r.ttl {
			return parseMultiaddrs(entry.Addresses), nil
		}
	}

	friend, ok := findFriend(r.friends, target)
	if !ok {
		return nil, fmt.Errorf("peer %s: %w", target, ErrNotFriend)
	}
	friendPub, err := unmarshalFriendPubKey(friend.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("friend pubkey: %w", err)
	}

	secret, err := DeriveSharedSecret(r.myPriv, friendPub)
	if err != nil {
		return nil, fmt.Errorf("deriving shared secret: %w", err)
	}
	key, err := DeriveDHTKey(secret, r.myPub, friendPub)
	if err != nil {
		return nil, fmt.Errorf("deriving DHT key: %w", err)
	}

	lctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	blob, err := r.dht.GetValue(lctx, key)
	if err != nil {
		r.log.Warn("DHT lookup failed",
			zap.String("peer", target.String()),
			zap.Error(err))
		// Inner err is wrapped with %v (not %w) so callers cannot branch on
		// the underlying transport error. Resolver's contract is "looked up
		// or didn't" — callers (the Engine in Task 10) only need to know the
		// lookup failed and they can retry / surface a generic error.
		return nil, fmt.Errorf("%w: %v", ErrLookupFailed, err)
	}

	encKey := DeriveEncryptionKey(secret)
	rec, err := Decode(blob, encKey, friendPub, r.ttl)
	if err != nil {
		r.log.Warn("decoding discovery record failed",
			zap.String("peer", target.String()),
			zap.Error(err))
		// As above: hide Decode's specific reasons (signature, AEAD, version,
		// stale) behind ErrLookupFailed. Callers don't branch on these.
		return nil, fmt.Errorf("%w: %v", ErrLookupFailed, err)
	}

	if r.cache != nil {
		r.cache.Set(target, rec.Addresses)
	}
	return parseMultiaddrs(rec.Addresses), nil
}

// InvalidateCache drops the cached entry for target.
func (r *Resolver) InvalidateCache(target peer.ID) {
	if r.cache != nil {
		r.cache.Invalidate(target)
	}
}

// findFriend looks up a friend by peer.ID in the FriendStore.
func findFriend(s FriendStore, target peer.ID) (friends.Friend, bool) {
	for _, f := range s.List() {
		if f.PeerID == target {
			return f, true
		}
	}
	return friends.Friend{}, false
}

func parseMultiaddrs(strs []string) []ma.Multiaddr {
	out := make([]ma.Multiaddr, 0, len(strs))
	for _, s := range strs {
		m, err := ma.NewMultiaddr(s)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}
