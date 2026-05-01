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

// AddressProvider exposes the host's current public-facing addresses.
// We deliberately publish only public + relay addresses (not loopback
// or RFC1918) so an invite record published from a real machine
// doesn't ask the redeemer to dial /ip4/127.0.0.1/...
type AddressProvider interface {
	PublicAddrs() []ma.Multiaddr
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
type CreateResult struct {
	Code      Code
	URL       string
	ExpiresAt time.Time
}

// Create generates a fresh invite code, publishes the encrypted record
// to the DHT under DeriveDHTKey(code), records it in the local store,
// and returns the code together with its URL form and expiry.
//
// ttl is capped at MaxTTL. The store entry's expiry matches the
// record's, so a redeem attempt against a still-cached-but-expired
// invite is rejected on either side.
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

	addrs := addrStrings(m.addrs.PublicAddrs())

	rec := Record{
		Version:     RecordVersion,
		PeerID:      m.host.ID().String(),
		PublicKey:   pubB64,
		Addresses:   addrs,
		DisplayName: m.displayName,
		ExpiresAt:   expires.Unix(),
	}

	encKey := DeriveEncryptionKey(code)
	blob, err := Encode(rec, encKey, m.priv)
	if err != nil {
		return CreateResult{}, fmt.Errorf("encoding record: %w", err)
	}
	if err := m.dht.PutValue(ctx, DeriveDHTKey(code), blob); err != nil {
		return CreateResult{}, fmt.Errorf("publishing to DHT: %w", err)
	}

	m.store.Add(Entry{
		Code:      code,
		ExpiresAt: expires,
		CreatedAt: time.Now(),
	})

	urlForm := FormatURL(URLPayload{
		PeerID:      rec.PeerID,
		Addresses:   addrs,
		DisplayName: rec.DisplayName,
		Code:        code,
	})
	return CreateResult{Code: code, URL: urlForm, ExpiresAt: expires}, nil
}

// Redeem fetches the DHT record for c, verifies its signature against
// the embedded public key (two-pass: decrypt → extract pubkey →
// re-decrypt + verify), opens a libp2p stream to the inviter, sends
// our Hello, reads the inviter's Response, and on accept adds the
// inviter to the friends store.
func (m *Manager) Redeem(ctx context.Context, c Code) (friends.Friend, error) {
	return m.redeem(ctx, c, nil)
}

// RedeemURL parses an opencom://join?... URL and delegates to Redeem
// for the cryptographic flow.
//
// The URL embeds peer ID and addresses but NOT the inviter's public
// key or the record signature, so we still fetch the DHT record to
// verify the inviter cryptographically. The URL form is therefore
// not currently a true "offline" path — see spec §3.5 for the planned
// future evolution that would embed pubkey+signature.
func (m *Manager) RedeemURL(ctx context.Context, urlStr string) (friends.Friend, error) {
	payload, err := ParseURL(urlStr)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("parsing url: %w", err)
	}
	pid, err := peer.Decode(payload.PeerID)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("decoding peer id: %w", err)
	}
	return m.redeem(ctx, payload.Code, &pid)
}

// Revoke deletes c from the local store. Returns true if an entry was
// actually removed; false if c was unknown (so callers can surface a
// "not found" error to the user). The DHT record cannot be retracted
// (Kademlia has no delete), but it will expire naturally and any
// redeem attempt will fail at the inviter-side store lookup.
func (m *Manager) Revoke(c Code) bool {
	return m.store.Remove(c)
}

// redeem is the core of Redeem and RedeemURL. If hintedPID is non-nil,
// it's used to guide the dial — but the cryptographic identity is
// always derived from the DHT record's embedded pubkey.
func (m *Manager) redeem(ctx context.Context, c Code, hintedPID *peer.ID) (friends.Friend, error) {
	encKey := DeriveEncryptionKey(c)
	blob, err := m.dht.GetValue(ctx, DeriveDHTKey(c))
	if err != nil {
		return friends.Friend{}, fmt.Errorf("fetching invite record: %w", err)
	}

	// First pass: decrypt only, so we can learn the inviter's pubkey.
	rawRec, err := DecodeUnverified(blob, encKey)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("decoding invite record: %w", err)
	}
	pubBytes, err := base64.StdEncoding.DecodeString(rawRec.PublicKey)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("decoding inviter public key: %w", err)
	}
	inviterPub, err := libp2pcrypto.UnmarshalPublicKey(pubBytes)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("parsing inviter public key: %w", err)
	}

	// Second pass: full Decode with the now-known pubkey actually
	// verifies the signature + expiry + version.
	rec, err := Decode(blob, encKey, inviterPub)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("verifying invite record: %w", err)
	}

	inviterID, err := peer.IDFromPublicKey(inviterPub)
	if err != nil {
		return friends.Friend{}, fmt.Errorf("deriving inviter peer id: %w", err)
	}
	// Defence in depth: PeerID embedded in the record must match the
	// pubkey it claims to come from.
	if rec.PeerID != inviterID.String() {
		return friends.Friend{}, errors.New("invite record peer id does not match embedded public key")
	}
	if hintedPID != nil && *hintedPID != inviterID {
		return friends.Friend{}, errors.New("invite url peer id does not match dht record")
	}

	// Inject the record's addresses into the peerstore so libp2p can
	// dial the inviter without a fresh discovery round.
	addrs := parseAddrs(rec.Addresses)
	if len(addrs) > 0 {
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
	ourPubB64 := base64.StdEncoding.EncodeToString(ourPubBytes)
	ourAddrs := addrStrings(m.addrs.PublicAddrs())

	if err := SendHello(stream, Hello{
		Type:        TypeRedeem,
		Code:        c,
		PeerID:      m.host.ID().String(),
		PublicKey:   ourPubB64,
		Addresses:   ourAddrs,
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
		PublicKey: rec.PublicKey,
		AddedAt:   time.Now().UTC(),
	}
	if err := m.friends.Add(added); err != nil {
		return friends.Friend{}, fmt.Errorf("adding friend: %w", err)
	}
	return added, nil
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
