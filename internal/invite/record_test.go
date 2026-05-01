package invite_test

import (
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/stretchr/testify/assert"

	"opencom/internal/invite"
)

func newRec(t *testing.T) (invite.Code, invite.Record, [32]byte, crypto.PrivKey, crypto.PubKey) {
	t.Helper()
	c, err := invite.Generate()
	assert.NoError(t, err)
	signerPriv, signerPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	encKey := invite.DeriveEncryptionKey(c)
	rec := invite.Record{
		Version:     invite.RecordVersion,
		PeerID:      "12D3KooWAlice",
		PublicKey:   "fake-pubkey-bytes",
		Addresses:   []string{"/ip4/192.0.2.1/tcp/4001"},
		DisplayName: "Alice",
		ExpiresAt:   time.Now().Add(30 * time.Minute).Unix(),
	}
	return c, rec, encKey, signerPriv, signerPub
}

func TestRecord_RoundTrip(t *testing.T) {
	t.Parallel()
	_, rec, encKey, signerPriv, signerPub := newRec(t)

	blob, err := invite.Encode(rec, encKey, signerPriv)
	assert.NoError(t, err)
	assert.NotEmpty(t, blob)

	out, err := invite.Decode(blob, encKey, signerPub)
	assert.NoError(t, err)
	assert.Equal(t, rec, out)
}

func TestRecord_DecodeRejectsTamperedSignature(t *testing.T) {
	t.Parallel()
	_, rec, encKey, signerPriv, signerPub := newRec(t)

	blob, err := invite.Encode(rec, encKey, signerPriv)
	assert.NoError(t, err)
	blob[0] ^= 0x01
	_, err = invite.Decode(blob, encKey, signerPub)
	assert.ErrorIs(t, err, invite.ErrSignatureInvalid)
}

func TestRecord_DecodeRejectsTamperedCiphertext(t *testing.T) {
	t.Parallel()
	_, rec, encKey, signerPriv, signerPub := newRec(t)

	blob, err := invite.Encode(rec, encKey, signerPriv)
	assert.NoError(t, err)
	blob[len(blob)-1] ^= 0x01
	_, err = invite.Decode(blob, encKey, signerPub)
	assert.ErrorIs(t, err, invite.ErrSignatureInvalid)
}

func TestRecord_DecodeRejectsExpired(t *testing.T) {
	t.Parallel()
	_, rec, encKey, signerPriv, signerPub := newRec(t)

	rec.ExpiresAt = time.Now().Add(-1 * time.Minute).Unix()
	blob, err := invite.Encode(rec, encKey, signerPriv)
	assert.NoError(t, err)
	_, err = invite.Decode(blob, encKey, signerPub)
	assert.ErrorIs(t, err, invite.ErrExpired)
}

func TestRecord_DecodeRejectsBadVersion(t *testing.T) {
	t.Parallel()
	_, rec, encKey, signerPriv, signerPub := newRec(t)

	rec.Version = 999
	blob, err := invite.Encode(rec, encKey, signerPriv)
	assert.NoError(t, err)
	_, err = invite.Decode(blob, encKey, signerPub)
	assert.True(t, errors.Is(err, invite.ErrUnsupportedVersion))
}

func TestRecord_DecodeRejectsMalformed(t *testing.T) {
	t.Parallel()
	_, _, encKey, _, signerPub := newRec(t)

	_, err := invite.Decode([]byte("too short"), encKey, signerPub)
	assert.ErrorIs(t, err, invite.ErrMalformedRecord)
}

func TestRecord_DecodeRejectsWrongEncKey(t *testing.T) {
	t.Parallel()
	_, rec, encKey, signerPriv, signerPub := newRec(t)

	blob, err := invite.Encode(rec, encKey, signerPriv)
	assert.NoError(t, err)

	var wrong [32]byte
	wrong[0] = 0xFF
	_, err = invite.Decode(blob, wrong, signerPub)
	assert.ErrorIs(t, err, invite.ErrSignatureInvalid)
}

func TestDeriveDHTKey_PrefixAndStable(t *testing.T) {
	t.Parallel()
	c, err := invite.Parse("OPEN-A7B2-X9K4")
	assert.NoError(t, err)
	k1 := invite.DeriveDHTKey(c)
	k2 := invite.DeriveDHTKey(c)
	assert.Equal(t, k1, k2, "deterministic")
	assert.True(t, len(k1) > len("/opencom-invite/v1/"))
	assert.Equal(t, "/opencom-invite/v1/", k1[:len("/opencom-invite/v1/")])
}

func TestDeriveDHTKey_DifferentCodesDifferentKeys(t *testing.T) {
	t.Parallel()
	c1, err := invite.Parse("OPEN-A7B2-X9K4")
	assert.NoError(t, err)
	c2, err := invite.Parse("OPEN-K8R3-MZ2X")
	assert.NoError(t, err)
	assert.NotEqual(t, invite.DeriveDHTKey(c1), invite.DeriveDHTKey(c2))
}
