package invite_test

import (
	"encoding/base64"
	"strings"
	"testing"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"

	"opencom/internal/identity"
	"opencom/internal/invite"
)

// signedFixture builds a URL payload + ed25519 keypair, signs it, and
// returns both. Shared by all URL signature tests so they exercise the
// real Sign/Verify code paths against real libp2p keys.
func signedFixture(t *testing.T) (invite.URLPayload, libp2pcrypto.PrivKey, libp2pcrypto.PubKey) {
	t.Helper()
	kp, err := identity.Generate()
	assert.NoError(t, err)
	pubBytes, err := libp2pcrypto.MarshalPublicKey(kp.Pub)
	assert.NoError(t, err)
	pid, err := peer.IDFromPublicKey(kp.Pub)
	assert.NoError(t, err)
	p := invite.URLPayload{
		PeerID:      pid.String(),
		PublicKey:   base64.StdEncoding.EncodeToString(pubBytes),
		Addresses:   []string{"/ip4/192.0.2.1/tcp/4001", "/ip4/192.0.2.1/udp/4001/quic-v1"},
		DisplayName: "Alice Smith",
		Code:        invite.Code("A7B2X9K4"),
		ExpiresAt:   1_800_000_000,
	}
	signed, err := invite.SignURL(p, kp.Priv)
	assert.NoError(t, err)
	assert.NotEmpty(t, signed.Signature)
	return signed, kp.Priv, kp.Pub
}

func TestURL_RoundTrip(t *testing.T) {
	t.Parallel()
	signed, _, _ := signedFixture(t)
	s := invite.FormatURL(signed)
	assert.True(t, strings.HasPrefix(s, "opencom://join?"), "got %q", s)

	out, err := invite.ParseURL(s)
	assert.NoError(t, err)
	assert.Equal(t, signed, out)
}

func TestURL_VerifyAcceptsValidSignature(t *testing.T) {
	t.Parallel()
	signed, _, _ := signedFixture(t)
	pub, err := invite.VerifyURL(signed)
	assert.NoError(t, err)
	assert.NotNil(t, pub)
}

func TestURL_VerifyRejectsTamperedDisplayName(t *testing.T) {
	t.Parallel()
	signed, _, _ := signedFixture(t)
	signed.DisplayName = "Mallory"
	_, err := invite.VerifyURL(signed)
	assert.ErrorIs(t, err, invite.ErrURLSignatureInvalid)
}

func TestURL_VerifyRejectsTamperedAddresses(t *testing.T) {
	t.Parallel()
	signed, _, _ := signedFixture(t)
	signed.Addresses = []string{"/ip4/10.0.0.1/tcp/4001"}
	_, err := invite.VerifyURL(signed)
	assert.ErrorIs(t, err, invite.ErrURLSignatureInvalid)
}

func TestURL_VerifyRejectsPeerIDPubkeyMismatch(t *testing.T) {
	t.Parallel()
	// Sign one keypair's URL but swap PeerID to a different real one.
	signed, _, _ := signedFixture(t)
	other, err := identity.Generate()
	assert.NoError(t, err)
	otherID, err := peer.IDFromPublicKey(other.Pub)
	assert.NoError(t, err)
	signed.PeerID = otherID.String()
	// Re-sign so the signature is valid for the (mismatched) payload.
	signed, err = invite.SignURL(signed, other.Priv)
	assert.NoError(t, err)
	// PublicKey still belongs to the original — peer-ID/pubkey mismatch.
	_, err = invite.VerifyURL(signed)
	assert.ErrorIs(t, err, invite.ErrURLPubkeyMismatch)
}

func TestURL_VerifyRejectsMissingSignature(t *testing.T) {
	t.Parallel()
	signed, _, _ := signedFixture(t)
	signed.Signature = ""
	_, err := invite.VerifyURL(signed)
	assert.ErrorIs(t, err, invite.ErrURLSignatureInvalid)
}

func TestURL_AcceptsCaseInsensitiveScheme(t *testing.T) {
	t.Parallel()
	signed, _, _ := signedFixture(t)
	s := invite.FormatURL(signed)
	upper := "OPENCOM://JOIN?" + s[len("opencom://join?"):]
	out, err := invite.ParseURL(upper)
	assert.NoError(t, err)
	assert.Equal(t, signed, out)
}

func TestURL_RejectsWrongScheme(t *testing.T) {
	t.Parallel()
	_, err := invite.ParseURL("https://example.com/?p=x")
	assert.Error(t, err)
}

func TestURL_RejectsMissingFields(t *testing.T) {
	t.Parallel()
	_, err := invite.ParseURL("opencom://join?p=12D3&c=A7B2X9K4")
	assert.Error(t, err, "missing addresses (a=) should reject")

	_, err = invite.ParseURL("opencom://join?a=&c=A7B2X9K4")
	assert.Error(t, err, "empty peer id should reject")
}

func TestURL_RejectsBadCode(t *testing.T) {
	t.Parallel()
	_, err := invite.ParseURL("opencom://join?p=12D3&a=&n=A&c=BADCODE")
	assert.Error(t, err)
}
