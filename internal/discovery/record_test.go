package discovery_test

import (
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/stretchr/testify/assert"

	"opencom/internal/discovery"
)

func TestRecord_RoundTrip(t *testing.T) {
	t.Parallel()

	signerPriv, signerPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)

	var encKey [32]byte
	for i := range encKey {
		encKey[i] = byte(i)
	}

	rec := discovery.Record{
		Version:   discovery.RecordVersion,
		Addresses: []string{"/ip4/192.0.2.1/tcp/4001", "/ip4/192.0.2.1/udp/4001/quic-v1"},
		Timestamp: time.Now().Unix(),
	}
	blob, err := discovery.Encode(rec, encKey, signerPriv)
	assert.NoError(t, err)
	assert.NotEmpty(t, blob)

	out, err := discovery.Decode(blob, encKey, signerPub, time.Hour)
	assert.NoError(t, err)
	assert.Equal(t, rec, out)
}

func TestRecord_DecodeRejectsTamperedSignature(t *testing.T) {
	t.Parallel()

	signerPriv, signerPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	var encKey [32]byte

	rec := discovery.Record{Version: 1, Timestamp: time.Now().Unix()}
	blob, err := discovery.Encode(rec, encKey, signerPriv)
	assert.NoError(t, err)

	blob[0] ^= 0x01
	_, err = discovery.Decode(blob, encKey, signerPub, time.Hour)
	assert.ErrorIs(t, err, discovery.ErrSignatureInvalid)
}

func TestRecord_DecodeRejectsTamperedCiphertext(t *testing.T) {
	t.Parallel()

	signerPriv, signerPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	var encKey [32]byte

	rec := discovery.Record{Version: 1, Timestamp: time.Now().Unix()}
	blob, err := discovery.Encode(rec, encKey, signerPriv)
	assert.NoError(t, err)

	blob[len(blob)-1] ^= 0x01
	_, err = discovery.Decode(blob, encKey, signerPub, time.Hour)
	assert.ErrorIs(t, err, discovery.ErrSignatureInvalid)
}

func TestRecord_DecodeRejectsZeroVersion(t *testing.T) {
	t.Parallel()

	signerPriv, signerPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	var encKey [32]byte

	// Version field unset → zero value (0). Should be rejected since
	// 0 != RecordVersion.
	rec := discovery.Record{Timestamp: time.Now().Unix()}
	blob, err := discovery.Encode(rec, encKey, signerPriv)
	assert.NoError(t, err)

	_, err = discovery.Decode(blob, encKey, signerPub, time.Hour)
	assert.ErrorIs(t, err, discovery.ErrUnsupportedVersion)
}

func TestRecord_DecodeRejectsWrongEncKey(t *testing.T) {
	t.Parallel()

	signerPriv, signerPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	var encKey [32]byte
	for i := range encKey {
		encKey[i] = byte(i)
	}

	rec := discovery.Record{
		Version:   discovery.RecordVersion,
		Timestamp: time.Now().Unix(),
	}
	blob, err := discovery.Encode(rec, encKey, signerPriv)
	assert.NoError(t, err)

	// Decode with a different encryption key. Signature is valid
	// (so signerPub.Verify passes), but AEAD.Open fails. Per spec,
	// AEAD failure maps to ErrSignatureInvalid.
	var wrongKey [32]byte
	wrongKey[0] = 0xFF
	_, err = discovery.Decode(blob, wrongKey, signerPub, time.Hour)
	assert.ErrorIs(t, err, discovery.ErrSignatureInvalid)
}

func TestRecord_DecodeRejectsStale(t *testing.T) {
	t.Parallel()

	signerPriv, signerPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	var encKey [32]byte

	rec := discovery.Record{
		Version:   1,
		Timestamp: time.Now().Add(-2 * time.Hour).Unix(),
	}
	blob, err := discovery.Encode(rec, encKey, signerPriv)
	assert.NoError(t, err)

	_, err = discovery.Decode(blob, encKey, signerPub, time.Hour)
	assert.ErrorIs(t, err, discovery.ErrStale)
}

func TestRecord_DecodeRejectsBadVersion(t *testing.T) {
	t.Parallel()

	signerPriv, signerPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	var encKey [32]byte

	rec := discovery.Record{Version: 999, Timestamp: time.Now().Unix()}
	blob, err := discovery.Encode(rec, encKey, signerPriv)
	assert.NoError(t, err)

	_, err = discovery.Decode(blob, encKey, signerPub, time.Hour)
	assert.True(t, errors.Is(err, discovery.ErrUnsupportedVersion))
}

func TestRecord_DecodeRejectsMalformed(t *testing.T) {
	t.Parallel()

	_, signerPub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	var encKey [32]byte

	_, err = discovery.Decode([]byte("too short"), encKey, signerPub, time.Hour)
	assert.ErrorIs(t, err, discovery.ErrMalformedRecord)
}
