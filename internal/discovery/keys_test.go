package discovery_test

import (
	"strings"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/stretchr/testify/assert"

	"opencom/internal/discovery"
)

// genPair returns an ed25519 keypair via libp2p's crypto package.
func genPair(t *testing.T) (crypto.PrivKey, crypto.PubKey) {
	t.Helper()
	priv, pub, err := crypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	return priv, pub
}

func TestDeriveSharedSecret_BothSidesAgree(t *testing.T) {
	t.Parallel()

	alicePriv, alicePub := genPair(t)
	bobPriv, bobPub := genPair(t)

	s1, err := discovery.DeriveSharedSecret(alicePriv, bobPub)
	assert.NoError(t, err)
	s2, err := discovery.DeriveSharedSecret(bobPriv, alicePub)
	assert.NoError(t, err)

	assert.Equal(t, s1, s2, "shared secrets must agree")
	zero := discovery.SharedSecret{}
	assert.NotEqual(t, zero, s1)
}

func TestDeriveDHTKey_BothSidesAgree(t *testing.T) {
	t.Parallel()

	alicePriv, alicePub := genPair(t)
	bobPriv, bobPub := genPair(t)

	s1, err := discovery.DeriveSharedSecret(alicePriv, bobPub)
	assert.NoError(t, err)
	s2, err := discovery.DeriveSharedSecret(bobPriv, alicePub)
	assert.NoError(t, err)

	k1, err := discovery.DeriveDHTKey(s1, alicePub, bobPub)
	assert.NoError(t, err)
	k2, err := discovery.DeriveDHTKey(s2, bobPub, alicePub)
	assert.NoError(t, err)

	assert.Equal(t, k1, k2, "DHT keys must agree regardless of arg order")
	assert.True(t, strings.HasPrefix(k1, "/opencom-discovery/v1/"), "key must use protocol prefix")
}

func TestDeriveDHTKey_DifferentPairsDifferentKeys(t *testing.T) {
	t.Parallel()

	alicePriv, alicePub := genPair(t)
	_, bobPub := genPair(t)
	_, evePub := genPair(t)

	sBob, err := discovery.DeriveSharedSecret(alicePriv, bobPub)
	assert.NoError(t, err)
	sEve, err := discovery.DeriveSharedSecret(alicePriv, evePub)
	assert.NoError(t, err)

	kBob, err := discovery.DeriveDHTKey(sBob, alicePub, bobPub)
	assert.NoError(t, err)
	kEve, err := discovery.DeriveDHTKey(sEve, alicePub, evePub)
	assert.NoError(t, err)

	assert.NotEqual(t, kBob, kEve, "different friend pairs must produce different DHT keys")
}

func TestDeriveEncryptionKey_Deterministic(t *testing.T) {
	t.Parallel()

	alicePriv, _ := genPair(t)
	_, bobPub := genPair(t)
	s, err := discovery.DeriveSharedSecret(alicePriv, bobPub)
	assert.NoError(t, err)

	k1 := discovery.DeriveEncryptionKey(s)
	k2 := discovery.DeriveEncryptionKey(s)
	assert.Equal(t, k1, k2, "derivation must be deterministic")

	other := discovery.SharedSecret{}
	for i := range other {
		other[i] = byte(i)
	}
	k3 := discovery.DeriveEncryptionKey(other)
	assert.NotEqual(t, k1, k3)
}

func TestDeriveSharedSecret_RejectsNonEd25519(t *testing.T) {
	t.Parallel()

	rsaPriv, _, err := crypto.GenerateRSAKeyPair(2048, nil)
	assert.NoError(t, err)
	_, ed25519Pub := genPair(t)

	_, err = discovery.DeriveSharedSecret(rsaPriv, ed25519Pub)
	assert.Error(t, err, "must reject non-ed25519 keys")
}

func TestDeriveSharedSecret_RejectsSelfPair(t *testing.T) {
	t.Parallel()

	priv, pub := genPair(t)
	_, err := discovery.DeriveSharedSecret(priv, pub)
	assert.Error(t, err, "self-pair must be rejected")
}

func TestDeriveDHTKey_SecretIsPartOfDigest(t *testing.T) {
	t.Parallel()

	_, alicePub := genPair(t)
	_, bobPub := genPair(t)

	// Two arbitrary, distinct shared secrets.
	var s1, s2 discovery.SharedSecret
	for i := range s1 {
		s1[i] = byte(i)
	}
	for i := range s2 {
		s2[i] = byte(255 - i)
	}

	k1, err := discovery.DeriveDHTKey(s1, alicePub, bobPub)
	assert.NoError(t, err)
	k2, err := discovery.DeriveDHTKey(s2, alicePub, bobPub)
	assert.NoError(t, err)

	assert.NotEqual(t, k1, k2,
		"DHT key must depend on the shared secret, not just the pubkey pair")
}
