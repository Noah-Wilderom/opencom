package discovery

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/libp2p/go-libp2p/core/crypto"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"

	"opencom/internal/identity"
)

// SharedSecret is the 32-byte X25519 ECDH output for a friend pair.
type SharedSecret [32]byte

// dhtKeyPrefix is the protocol namespace for opencom discovery records
// under the libp2p DHT. Bumping the version (v1 → v2) lets future schema
// changes co-exist on the same DHT.
const dhtKeyPrefix = "/opencom-discovery/v1/"

// dhtKeyDomain is the domain-separation tag prefixed to the SHA-256
// preimage when deriving DHT lookup keys.
const dhtKeyDomain = "opencom-discovery-v1"

// recordKeyInfo is the HKDF info string for record-encryption key
// derivation.
const recordKeyInfo = "opencom-record-v1"

// DeriveSharedSecret performs X25519(my_priv, friend_pub) using the
// X25519 keys derived from each party's libp2p ed25519 identity.
//
// Returns an error if either key is not ed25519.
func DeriveSharedSecret(myPriv crypto.PrivKey, theirPub crypto.PubKey) (SharedSecret, error) {
	var out SharedSecret

	myEdPriv, err := edPrivFromLibp2p(myPriv)
	if err != nil {
		return out, fmt.Errorf("local key: %w", err)
	}
	theirEdPub, err := edPubFromLibp2p(theirPub)
	if err != nil {
		return out, fmt.Errorf("remote key: %w", err)
	}

	// Reject self-pair — the only callers should be deriving keys with
	// distinct friends, and self-DH is operationally meaningless here.
	myEdPub, err := myPriv.GetPublic().Raw()
	if err == nil && bytes.Equal(myEdPub, theirEdPub) {
		return out, errors.New("cannot derive shared secret with self")
	}

	myXPriv, err := identity.ToX25519Private(myEdPriv)
	if err != nil {
		return out, fmt.Errorf("converting local privkey: %w", err)
	}
	theirXPub, err := identity.ToX25519Public(theirEdPub)
	if err != nil {
		return out, fmt.Errorf("converting remote pubkey: %w", err)
	}

	shared, err := curve25519.X25519(myXPriv[:], theirXPub[:])
	if err != nil {
		return out, fmt.Errorf("X25519: %w", err)
	}
	copy(out[:], shared)
	return out, nil
}

// DeriveDHTKey returns the DHT lookup key for the friend pair. The key
// is symmetric: both sides derive the same value when given each
// other's pubkeys.
func DeriveDHTKey(s SharedSecret, myPub, theirPub crypto.PubKey) (string, error) {
	myRaw, err := myPub.Raw()
	if err != nil {
		return "", fmt.Errorf("raw local pubkey: %w", err)
	}
	theirRaw, err := theirPub.Raw()
	if err != nil {
		return "", fmt.Errorf("raw remote pubkey: %w", err)
	}
	a, b := canonicalOrder(myRaw, theirRaw)

	h := sha256.New()
	h.Write([]byte(dhtKeyDomain))
	h.Write(s[:])
	h.Write(a)
	h.Write(b)
	digest := h.Sum(nil)

	return dhtKeyPrefix + hex.EncodeToString(digest), nil
}

// DeriveEncryptionKey returns the 32-byte AEAD key for record content.
//
// HKDF salt is nil — the IKM is already a high-entropy ECDH output, so
// per RFC 5869 §3.1, omitting the salt produces a sound PRK. If future
// versions need to bind extra non-secret context (epoch, app version),
// extend recordKeyInfo rather than introducing a salt.
func DeriveEncryptionKey(s SharedSecret) [32]byte {
	r := hkdf.New(sha256.New, s[:], nil, []byte(recordKeyInfo))
	var out [32]byte
	if _, err := io.ReadFull(r, out[:]); err != nil {
		// HKDF over a 32-byte key reading 32 bytes cannot fail.
		panic(fmt.Sprintf("hkdf failed: %v", err))
	}
	return out
}

// canonicalOrder returns the two byte slices in lexicographic order so
// the caller-side argument order of (my, their) doesn't matter.
func canonicalOrder(a, b []byte) ([]byte, []byte) {
	if bytes.Compare(a, b) < 0 {
		return a, b
	}
	return b, a
}

// edPrivFromLibp2p extracts the underlying ed25519.PrivateKey (64 bytes)
// from a libp2p crypto.PrivKey. Returns an error if not ed25519.
func edPrivFromLibp2p(k crypto.PrivKey) (ed25519.PrivateKey, error) {
	if k.Type() != crypto.Ed25519 {
		return nil, errors.New("private key is not ed25519")
	}
	raw, err := k.Raw()
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("ed25519 raw private key must be %d bytes, got %d",
			ed25519.PrivateKeySize, len(raw))
	}
	return ed25519.PrivateKey(raw), nil
}

// edPubFromLibp2p extracts the 32-byte ed25519 public key from a libp2p
// crypto.PubKey. Returns an error if not ed25519.
func edPubFromLibp2p(k crypto.PubKey) ([]byte, error) {
	if k.Type() != crypto.Ed25519 {
		return nil, errors.New("public key is not ed25519")
	}
	raw, err := k.Raw()
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("ed25519 raw public key must be %d bytes, got %d",
			ed25519.PublicKeySize, len(raw))
	}
	return raw, nil
}
