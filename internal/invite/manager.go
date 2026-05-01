package invite

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	ma "github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"

	"opencom/internal/discovery"
	"opencom/internal/friends"
	"opencom/internal/transport/p2p"
)

// MaxTTL caps the lifetime of any single invite, regardless of what the
// caller requests. Short-lived codes minimise the window during which a
// leaked invite can be redeemed.
const MaxTTL = 30 * time.Minute

// FriendStore is the narrow slice of *friends.Store the Manager needs
// for completing the friend-handshake. Defined here (not in friends/)
// so the invite package can be tested with a fake without pulling in
// the full friends API.
type FriendStore interface {
	Add(f friends.Friend) error
}

// AddressProvider exposes the host's current addresses for embedding
// in invite records and URLs.
//
// Unlike the discovery layer (which publishes to a shared DHT and
// must filter to PublicAddrs to avoid leaking LAN topology to
// arbitrary peers), invites are explicitly shared out-of-band by
// the user — so embedding RFC1918 / loopback addresses is fine and
// in fact necessary for LAN-only redemption to work without an
// internet round-trip.
type AddressProvider interface {
	InviteAddrs() []ma.Multiaddr
}

// ManagerOptions configures a Manager.
type ManagerOptions struct {
	Host        *p2p.Host
	DHT         discovery.DHT
	Friends     FriendStore
	Store       *Store
	Identity    libp2pcrypto.PrivKey
	IdentityPub libp2pcrypto.PubKey
	Log         *zap.Logger

	// DisplayName is the local user's name (cfg.User.Name). Embedded
	// in invite records and Accept responses so the redeemer's
	// friends.Store gets a real name on Add — without this, friends
	// land in the store with Name="" and `opencom call <name>`
	// can't resolve them.
	DisplayName string

	// AddressProvider, when set, replaces host.HostInternal().Addrs()
	// for record/Hello publication. Allows tests to override (or
	// inject loopback) without polluting production records.
	// Defaults to host.PublicAddrs.
	AddressProvider AddressProvider
}

// Manager wires the invite subsystem together: code generation +
// encrypted DHT publish on the inviter side, DHT fetch + libp2p
// handshake on the redeemer side, and a stream handler that completes
// the friend-handshake on the inviter side. Mirrors M4's call.Engine
// in shape: NewManager constructs, Start registers handlers, Stop
// unregisters.
type Manager struct {
	host        *p2p.Host
	dht         discovery.DHT
	friends     FriendStore
	store       *Store
	priv        libp2pcrypto.PrivKey
	pub         libp2pcrypto.PubKey
	log         *zap.Logger
	displayName string
	addrs       AddressProvider
}

// NewManager constructs a Manager. Returns an error if any required
// option is nil.
func NewManager(opts ManagerOptions) (*Manager, error) {
	if opts.Host == nil {
		return nil, errors.New("ManagerOptions.Host is required")
	}
	if opts.DHT == nil {
		return nil, errors.New("ManagerOptions.DHT is required")
	}
	if opts.Friends == nil {
		return nil, errors.New("ManagerOptions.Friends is required")
	}
	if opts.Store == nil {
		return nil, errors.New("ManagerOptions.Store is required")
	}
	if opts.Identity == nil || opts.IdentityPub == nil {
		return nil, errors.New("ManagerOptions.Identity and IdentityPub are required")
	}
	if opts.Log == nil {
		opts.Log = zap.NewNop()
	}
	addrs := opts.AddressProvider
	if addrs == nil {
		addrs = opts.Host
	}
	return &Manager{
		host:        opts.Host,
		dht:         opts.DHT,
		friends:     opts.Friends,
		store:       opts.Store,
		priv:        opts.Identity,
		pub:         opts.IdentityPub,
		log:         opts.Log,
		displayName: opts.DisplayName,
		addrs:       addrs,
	}, nil
}

// Start registers the libp2p stream handler for ProtocolID. Call once
// at daemon startup.
func (m *Manager) Start() {
	m.host.HostInternal().SetStreamHandler(ProtocolID, m.handleStream)
}

// Stop removes the stream handler. Safe to call multiple times.
func (m *Manager) Stop() {
	m.host.HostInternal().RemoveStreamHandler(ProtocolID)
}

// CreateResult is the outcome of a successful Create call.
//
// DHTPublishErr is non-nil when the encrypted DHT record could not be
// published (typically: empty routing table when no opencom bootstrap
// peers are reachable). Callers should treat this as a warning, not a
// failure: the URL form is self-contained and remains usable; only the
// short-code redemption path needs the DHT.
type CreateResult struct {
	Code          Code
	URL           string
	ExpiresAt     time.Time
	DHTPublishErr error
}

// Create generates a fresh invite code, signs a self-contained URL,
// best-effort publishes the encrypted record to the DHT under
// DeriveDHTKey(code), records the code in the local store, and returns
// the code with its URL form and expiry.
//
// ttl is capped at MaxTTL. The store entry's expiry matches the
// record's, so a redeem attempt against a still-cached-but-expired
// invite is rejected on either side.
//
// DHT publish failure is non-fatal — see CreateResult.DHTPublishErr.
// The URL is signed with the inviter's identity key, so RedeemURL can
// verify and dial without ever touching the DHT.
func (m *Manager) Create(ctx context.Context, ttl time.Duration) (CreateResult, error) {
	if ttl <= 0 || ttl > MaxTTL {
		ttl = MaxTTL
	}
	code, err := Generate()
	if err != nil {
		return CreateResult{}, fmt.Errorf("generating code: %w", err)
	}
	expires := time.Now().Add(ttl)

	pubBytes, err := libp2pcrypto.MarshalPublicKey(m.pub)
	if err != nil {
		return CreateResult{}, fmt.Errorf("marshalling public key: %w", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pubBytes)
	addrs := addrStrings(m.addrs.InviteAddrs())

	// Build + sign the self-contained URL FIRST. This must succeed: the
	// URL is the only redemption path that doesn't depend on a healthy
	// DHT, so if signing fails (extremely unlikely with a valid identity
	// key), the whole Create is a hard failure.
	urlPayload := URLPayload{
		PeerID:      m.host.ID().String(),
		PublicKey:   pubB64,
		Addresses:   addrs,
		DisplayName: m.displayName,
		Code:        code,
		ExpiresAt:   expires.Unix(),
	}
	signedURL, err := SignURL(urlPayload, m.priv)
	if err != nil {
		return CreateResult{}, fmt.Errorf("signing url: %w", err)
	}

	// Add to local store BEFORE the DHT attempt so the inviter-side
	// handshake can validate the code even if DHT publish fails.
	m.store.Add(Entry{
		Code:      code,
		ExpiresAt: expires,
		CreatedAt: time.Now(),
	})

	// Best-effort DHT publish. The encrypted record carries everything
	// the URL does (and is keyed by hash(code)), so a successful publish
	// enables short-code redemption. Failure here typically means an
	// empty kad-dht routing table — log and surface to the caller, but
	// don't fail Create.
	rec := Record{
		Version:     RecordVersion,
		PeerID:      urlPayload.PeerID,
		PublicKey:   pubB64,
		Addresses:   addrs,
		DisplayName: m.displayName,
		ExpiresAt:   expires.Unix(),
	}
	encKey := DeriveEncryptionKey(code)
	blob, encErr := Encode(rec, encKey, m.priv)
	var dhtErr error
	if encErr != nil {
		dhtErr = fmt.Errorf("encoding record: %w", encErr)
	} else if putErr := m.dht.PutValue(ctx, DeriveDHTKey(code), blob); putErr != nil {
		dhtErr = fmt.Errorf("publishing to dht: %w", putErr)
	}
	if dhtErr != nil {
		m.log.Warn("invite: dht publish failed; short code unusable until dht recovers, url form still works",
			zap.Error(dhtErr))
	}

	return CreateResult{
		Code:          code,
		URL:           FormatURL(signedURL),
		ExpiresAt:     expires,
		DHTPublishErr: dhtErr,
	}, nil
}

// Redeem looks up the DHT record for c, verifies its signature, and
// completes the friend-handshake with the inviter.
//
// The DHT path is required: only the inviter knows the encryption key
// (derived from the secret code) and only their identity key signed
// the record. Without DHT access (no opencom bootstrap peers reachable)
// short-code redemption can't work — use the URL form instead.
func (m *Manager) Redeem(ctx context.Context, c Code) (friends.Friend, error) {
	encKey := DeriveEncryptionKey(c)
	blob, err := m.dht.GetValue(ctx, DeriveDHTKey(c))
	if err != nil {
		return friends.Friend{}, fmt.Errorf("fetching invite record: %w", err)
	}

	// Two-pass decode: first decrypt to read the embedded pubkey, then
	// fully verify with that pubkey. AEAD authenticates both passes.
	rawRec, err := DecodeUnverified(blob, encKey)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("decoding invite record: %w", err)
	}
	inviterPub, err := decodePubKey(rawRec.PublicKey)
	if err != nil {
		return friends.Friend{}, err
	}
	rec, err := Decode(blob, encKey, inviterPub)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("verifying invite record: %w", err)
	}

	inviterID, err := peer.IDFromPublicKey(inviterPub)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("deriving inviter peer id: %w", err)
	}
	if rec.PeerID != inviterID.String() {
		return friends.Friend{}, errors.New("invite record peer id does not match embedded public key")
	}

	return m.dialAndHandshake(ctx, c, inviterID, rec.Addresses, rec.PublicKey)
}

// RedeemURL parses an opencom://join?... URL, verifies the embedded
// signature locally, and completes the friend-handshake with the
// inviter. Does NOT touch the DHT — the URL is fully self-contained.
//
// Use this when DHT-based discovery is unavailable (no opencom
// bootstrap peers, fresh single-machine setup, or LAN-only testing).
func (m *Manager) RedeemURL(ctx context.Context, urlStr string) (friends.Friend, error) {
	payload, err := ParseURL(urlStr)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("parsing url: %w", err)
	}
	if payload.ExpiresAt > 0 && time.Now().Unix() > payload.ExpiresAt {
		return friends.Friend{}, ErrExpired
	}
	inviterPub, err := VerifyURL(payload)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("verifying invite url: %w", err)
	}
	inviterID, err := peer.IDFromPublicKey(inviterPub)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("deriving inviter peer id: %w", err)
	}
	return m.dialAndHandshake(ctx, payload.Code, inviterID, payload.Addresses, payload.PublicKey)
}

// Revoke deletes c from the local store. Returns true if an entry was
// actually removed; false if c was unknown (so callers can surface a
// "not found" error to the user). The DHT record cannot be retracted
// (Kademlia has no delete), but it will expire naturally and any
// redeem attempt will fail at the inviter-side store lookup.
func (m *Manager) Revoke(c Code) bool {
	return m.store.Remove(c)
}

// dialAndHandshake is the post-verification half of redemption shared
// by Redeem (DHT path) and RedeemURL (URL path). By this point the
// caller has authenticated the inviter cryptographically — we only
// need to inject addresses, dial, and run the friend-handshake stream.
func (m *Manager) dialAndHandshake(
	ctx context.Context,
	c Code,
	inviterID peer.ID,
	inviterAddrs []string,
	inviterPubKeyB64 string,
) (friends.Friend, error) {
	if addrs := parseAddrs(inviterAddrs); len(addrs) > 0 {
		m.host.HostInternal().Peerstore().AddAddrs(inviterID, addrs, peerstore.TempAddrTTL)
	}

	stream, err := m.host.HostInternal().NewStream(ctx, inviterID, ProtocolID)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("opening invite stream to %s: %w", inviterID, err)
	}
	defer stream.Close()

	ourPubBytes, err := libp2pcrypto.MarshalPublicKey(m.pub)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("marshalling our public key: %w", err)
	}
	if err := SendHello(stream, Hello{
		Type:        TypeRedeem,
		Code:        c,
		PeerID:      m.host.ID().String(),
		PublicKey:   base64.StdEncoding.EncodeToString(ourPubBytes),
		Addresses:   addrStrings(m.addrs.InviteAddrs()),
		DisplayName: m.displayName,
	}); err != nil {
		_ = stream.Reset()
		return friends.Friend{}, fmt.Errorf("sending hello: %w", err)
	}

	resp, err := ReadResponse(stream)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("reading response: %w", err)
	}
	if resp.Type == TypeReject {
		return friends.Friend{}, fmt.Errorf("invite rejected: %s", resp.Reason)
	}
	if resp.Type != TypeAccept {
		return friends.Friend{}, fmt.Errorf("unexpected response type %q", resp.Type)
	}

	added := friends.Friend{
		Name:      resp.DisplayName,
		PeerID:    inviterID,
		PublicKey: inviterPubKeyB64,
		AddedAt:   time.Now().UTC(),
	}
	if err := m.friends.Add(added); err != nil {
		return friends.Friend{}, fmt.Errorf("adding friend: %w", err)
	}
	return added, nil
}

// decodePubKey decodes a base64-encoded marshaled libp2p public key.
func decodePubKey(b64 string) (libp2pcrypto.PubKey, error) {
	pubBytes, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decoding public key: %w", err)
	}
	pub, err := libp2pcrypto.UnmarshalPublicKey(pubBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}
	return pub, nil
}

// handleStream is the libp2p stream handler for inviter-side handshake
// completion. It reads a Hello, validates the embedded code against
// the local store, and either accepts (adding the invitee as a friend
// and replying with our pubkey) or rejects with a reason.
func (m *Manager) handleStream(s network.Stream) {
	defer s.Close()
	remote := s.Conn().RemotePeer()

	hello, err := ReadHello(s)
	if err != nil {
		m.log.Debug("invite: reading hello failed", zap.Error(err))
		_ = s.Reset()
		return
	}
	if hello.Type != TypeRedeem {
		m.log.Debug("invite: unexpected hello type", zap.String("type", hello.Type))
		_ = SendResponse(s, Response{Type: TypeReject, Reason: "unexpected hello type"})
		return
	}

	entry, ok := m.store.Get(hello.Code)
	if !ok {
		_ = SendResponse(s, Response{Type: TypeReject, Reason: "unknown code"})
		return
	}
	if entry.Consumed {
		_ = SendResponse(s, Response{Type: TypeReject, Reason: "code already consumed"})
		return
	}
	if time.Now().After(entry.ExpiresAt) {
		_ = SendResponse(s, Response{Type: TypeReject, Reason: "code expired"})
		return
	}

	// Parse + sanity-check the invitee's pubkey before consuming the
	// code. If pubkey is bogus we don't burn the invite.
	inviteePubBytes, err := base64.StdEncoding.DecodeString(hello.PublicKey)
	if err != nil {
		_ = SendResponse(s, Response{Type: TypeReject, Reason: "invalid public key encoding"})
		return
	}
	inviteePub, err := libp2pcrypto.UnmarshalPublicKey(inviteePubBytes)
	if err != nil {
		_ = SendResponse(s, Response{Type: TypeReject, Reason: "invalid public key"})
		return
	}
	derivedID, err := peer.IDFromPublicKey(inviteePub)
	if err != nil {
		_ = SendResponse(s, Response{Type: TypeReject, Reason: "cannot derive peer id"})
		return
	}
	// The libp2p-authenticated remote peer is the source of truth for
	// peer ID; reject if the invitee claims a different one.
	if derivedID != remote {
		_ = SendResponse(s, Response{Type: TypeReject, Reason: "peer id / public key mismatch"})
		return
	}

	// MarkConsumed first so concurrent redeems can't both pass: only
	// one CAS-style call succeeds per code. If the friends.Add below
	// then fails, we restore the entry's unconsumed state so the
	// inviter can retry without revoke/recreate.
	if !m.store.MarkConsumed(hello.Code, remote.String()) {
		_ = SendResponse(s, Response{Type: TypeReject, Reason: "code already consumed"})
		return
	}

	if err := m.friends.Add(friends.Friend{
		Name:      hello.DisplayName,
		PeerID:    remote,
		PublicKey: hello.PublicKey,
		AddedAt:   time.Now().UTC(),
	}); err != nil {
		m.log.Warn("invite: adding invitee to friends failed", zap.Error(err))
		// Roll back: this redeem failed, code should remain valid.
		m.store.UnmarkConsumed(hello.Code)
		_ = SendResponse(s, Response{Type: TypeReject, Reason: "failed to add invitee"})
		return
	}

	ourPubBytes, err := libp2pcrypto.MarshalPublicKey(m.pub)
	if err != nil {
		m.log.Warn("invite: marshalling our public key failed", zap.Error(err))
		_ = SendResponse(s, Response{Type: TypeReject, Reason: "internal error"})
		return
	}
	_ = SendResponse(s, Response{
		Type:        TypeAccept,
		PeerID:      m.host.ID().String(),
		PublicKey:   base64.StdEncoding.EncodeToString(ourPubBytes),
		DisplayName: m.displayName,
	})
}

// addrStrings serialises ms to wire-format strings. Filtering is
// deliberately absent: the loopback addresses are useful for tests and
// LAN, and the tradeoff against leaking RFC1918 space is acceptable
// given the encryption + short TTL of the record.
func addrStrings(ms []ma.Multiaddr) []string {
	out := make([]string, 0, len(ms))
	for _, a := range ms {
		out = append(out, a.String())
	}
	return out
}

// parseAddrs is the reverse of addrStrings; unparseable entries are
// silently skipped.
func parseAddrs(strs []string) []ma.Multiaddr {
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
