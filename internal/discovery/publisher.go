package discovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	ma "github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"

	"opencom/internal/friends"
)

// FriendStore is the narrow slice of *friends.Store the publisher needs.
// Implementations must be safe for concurrent reads from the publisher
// goroutine alongside writes from other goroutines (e.g. IPC handlers
// adding/removing friends).
type FriendStore interface {
	List() []friends.Friend
}

// AddressProvider exposes the host's current public-facing addresses.
// Typically a *p2p.Host. Implementations must be safe for concurrent
// reads.
type AddressProvider interface {
	PublicAddrs() []ma.Multiaddr
}

// PublisherOptions configures a Publisher.
type PublisherOptions struct {
	DHT             DHT
	Friends         FriendStore
	Signer          crypto.PrivKey
	SignerPub       crypto.PubKey
	AddressProvider AddressProvider
	Log             *zap.Logger

	// RefreshInterval is the cadence between publish rounds. Zero
	// means DefaultRefreshInterval (4h).
	RefreshInterval time.Duration
}

// DefaultRefreshInterval is the cadence at which records are
// re-published.
const DefaultRefreshInterval = 4 * time.Hour

// Publisher periodically publishes one encrypted DHT record per
// friend, mapping the local peer ID to its current addresses.
type Publisher struct {
	dht       DHT
	friends   FriendStore
	signer    crypto.PrivKey
	signerPub crypto.PubKey
	addrs     AddressProvider
	log       *zap.Logger
	interval  time.Duration
}

// NewPublisher constructs a Publisher. Returns an error if any required
// option is nil.
func NewPublisher(opts PublisherOptions) (*Publisher, error) {
	if opts.DHT == nil {
		return nil, errors.New("PublisherOptions.DHT is required")
	}
	if opts.Friends == nil {
		return nil, errors.New("PublisherOptions.Friends is required")
	}
	if opts.Signer == nil || opts.SignerPub == nil {
		return nil, errors.New("PublisherOptions.Signer and SignerPub are required")
	}
	if opts.AddressProvider == nil {
		return nil, errors.New("PublisherOptions.AddressProvider is required")
	}
	if opts.Log == nil {
		opts.Log = zap.NewNop()
	}
	if opts.RefreshInterval == 0 {
		opts.RefreshInterval = DefaultRefreshInterval
	}
	return &Publisher{
		dht:       opts.DHT,
		friends:   opts.Friends,
		signer:    opts.Signer,
		signerPub: opts.SignerPub,
		addrs:     opts.AddressProvider,
		log:       opts.Log,
		interval:  opts.RefreshInterval,
	}, nil
}

// Run blocks until ctx is canceled, publishing one record per friend
// every RefreshInterval. The first publish runs immediately.
//
// PublishOnce always returns nil — per-friend errors are logged at WARN
// and skipped, so the round always completes. Run therefore returns nil
// on context cancel (no propagated round-level error).
func (p *Publisher) Run(ctx context.Context) error {
	_ = p.PublishOnce(ctx)
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			_ = p.PublishOnce(ctx)
		}
	}
}

// PublishOnce performs one round of publication: snapshot friends,
// build a record from the current addresses, encrypt + sign + put per
// friend.
//
// Per-friend errors are logged at WARN and skipped — a malformed friend
// pubkey or a transient DHT failure for one friend never aborts the
// round. Always returns nil; the error type is reserved for future
// round-level failures (none in M5). Exported for tests; production
// callers should use Run.
func (p *Publisher) PublishOnce(ctx context.Context) error {
	all := p.friends.List()
	if len(all) == 0 {
		return nil
	}
	addrs := p.addrs.PublicAddrs()
	addrStrs := make([]string, 0, len(addrs))
	for _, a := range addrs {
		addrStrs = append(addrStrs, a.String())
	}
	rec := Record{
		Version:   RecordVersion,
		Addresses: addrStrs,
		Timestamp: time.Now().Unix(),
	}

	for _, f := range all {
		friendPub, err := unmarshalFriendPubKey(f.PublicKey)
		if err != nil {
			p.log.Warn("skipping friend with malformed pubkey",
				zap.String("name", f.Name),
				zap.Error(err))
			continue
		}
		secret, err := DeriveSharedSecret(p.signer, friendPub)
		if err != nil {
			p.log.Warn("deriving shared secret failed",
				zap.String("name", f.Name),
				zap.Error(err))
			continue
		}
		key, err := DeriveDHTKey(secret, p.signerPub, friendPub)
		if err != nil {
			p.log.Warn("deriving DHT key failed",
				zap.String("name", f.Name),
				zap.Error(err))
			continue
		}
		encKey := DeriveEncryptionKey(secret)
		blob, err := Encode(rec, encKey, p.signer)
		if err != nil {
			p.log.Warn("encoding record failed",
				zap.String("name", f.Name),
				zap.Error(err))
			continue
		}
		if err := p.dht.PutValue(ctx, key, blob); err != nil {
			p.log.Warn("DHT put failed",
				zap.String("name", f.Name),
				zap.Error(err))
			continue
		}
	}
	return nil
}

// unmarshalFriendPubKey decodes a libp2p public key from the format
// stored in friends.Friend.PublicKey (libp2p protobuf marshaling).
func unmarshalFriendPubKey(s string) (crypto.PubKey, error) {
	if s == "" {
		return nil, errors.New("empty public key")
	}
	pub, err := crypto.UnmarshalPublicKey([]byte(s))
	if err != nil {
		return nil, fmt.Errorf("unmarshal pubkey: %w", err)
	}
	return pub, nil
}
