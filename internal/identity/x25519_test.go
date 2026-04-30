package identity_test

import (
	"crypto/ed25519"
	"testing"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/curve25519"

	"opencom/internal/identity"
)

func TestToX25519_PublicConversion(t *testing.T) {
	t.Parallel()

	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)

	xpub, err := identity.ToX25519Public(pub)
	assert.NoError(t, err)
	assert.Len(t, xpub[:], 32)
	allZero := true
	for _, b := range xpub {
		if b != 0 {
			allZero = false
			break
		}
	}
	assert.False(t, allZero, "X25519 public key must not be all-zero")
}

func TestToX25519_PrivateConversion(t *testing.T) {
	t.Parallel()

	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	priv := ed25519.NewKeyFromSeed(seed)

	xpriv, err := identity.ToX25519Private(priv)
	assert.NoError(t, err)
	assert.Len(t, xpriv[:], 32)
	assert.Zero(t, xpriv[0]&0b00000111, "low 3 bits of byte 0 must be cleared")
	assert.NotZero(t, xpriv[31]&0b01000000, "bit 6 of byte 31 must be set")
	assert.Zero(t, xpriv[31]&0b10000000, "bit 7 of byte 31 must be cleared")
}

func TestToX25519_ECDHAgreement(t *testing.T) {
	t.Parallel()

	aliceSeed := make([]byte, ed25519.SeedSize)
	for i := range aliceSeed {
		aliceSeed[i] = byte(i)
	}
	bobSeed := make([]byte, ed25519.SeedSize)
	for i := range bobSeed {
		bobSeed[i] = byte(i + 100)
	}
	alicePriv := ed25519.NewKeyFromSeed(aliceSeed)
	alicePub := alicePriv.Public().(ed25519.PublicKey)
	bobPriv := ed25519.NewKeyFromSeed(bobSeed)
	bobPub := bobPriv.Public().(ed25519.PublicKey)

	aliceX, err := identity.ToX25519Public(alicePub)
	assert.NoError(t, err)
	bobX, err := identity.ToX25519Public(bobPub)
	assert.NoError(t, err)

	aliceXpriv, err := identity.ToX25519Private(alicePriv)
	assert.NoError(t, err)
	bobXpriv, err := identity.ToX25519Private(bobPriv)
	assert.NoError(t, err)

	shared1, err := curve25519.X25519(aliceXpriv[:], bobX[:])
	assert.NoError(t, err)
	shared2, err := curve25519.X25519(bobXpriv[:], aliceX[:])
	assert.NoError(t, err)

	assert.Equal(t, shared1, shared2, "ECDH shared secrets must agree")
}

func TestToX25519_PublicRejectsBadInput(t *testing.T) {
	t.Parallel()

	_, err := identity.ToX25519Public(make([]byte, 16))
	assert.Error(t, err, "must reject non-32-byte input")

	// y=2 has no corresponding x on the Edwards curve, so SetBytes rejects it.
	// (The plan suggested all-0xFF, but that input reduces mod p to a valid
	// point — y=2 is the smallest little-endian encoding that genuinely
	// exercises the curve-membership rejection path.)
	bad := make([]byte, 32)
	bad[0] = 2
	_, err = identity.ToX25519Public(bad)
	assert.Error(t, err, "must reject malformed Edwards point")
}

func TestToX25519_PrivateRejectsBadLength(t *testing.T) {
	t.Parallel()

	// Empty.
	_, err := identity.ToX25519Private(ed25519.PrivateKey{})
	assert.Error(t, err)

	// Wrong length (32 bytes — looks like the seed alone, not the
	// 64-byte private key).
	_, err = identity.ToX25519Private(make([]byte, 32))
	assert.Error(t, err)
}

func TestToX25519_LibP2PEd25519Compatible(t *testing.T) {
	t.Parallel()

	// Generate two keypairs using libp2p's helper.
	libp2pAlicePriv, libp2pAlicePub, err := libp2pcrypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	libp2pBobPriv, libp2pBobPub, err := libp2pcrypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)

	aliceRawPriv, err := libp2pAlicePriv.Raw()
	assert.NoError(t, err)
	aliceRawPub, err := libp2pAlicePub.Raw()
	assert.NoError(t, err)
	bobRawPriv, err := libp2pBobPriv.Raw()
	assert.NoError(t, err)
	bobRawPub, err := libp2pBobPub.Raw()
	assert.NoError(t, err)

	// Convert via our wrappers.
	aliceX, err := identity.ToX25519Public(aliceRawPub)
	assert.NoError(t, err)
	bobX, err := identity.ToX25519Public(bobRawPub)
	assert.NoError(t, err)
	aliceXpriv, err := identity.ToX25519Private(ed25519.PrivateKey(aliceRawPriv))
	assert.NoError(t, err)
	bobXpriv, err := identity.ToX25519Private(ed25519.PrivateKey(bobRawPriv))
	assert.NoError(t, err)

	// X25519 in both directions must agree.
	shared1, err := curve25519.X25519(aliceXpriv[:], bobX[:])
	assert.NoError(t, err)
	shared2, err := curve25519.X25519(bobXpriv[:], aliceX[:])
	assert.NoError(t, err)
	assert.Equal(t, shared1, shared2,
		"libp2p-generated ed25519 keys must produce agreeing X25519 ECDH")
}
