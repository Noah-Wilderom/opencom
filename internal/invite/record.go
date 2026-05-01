package invite

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// RecordVersion is the current schema version.
const RecordVersion = 1

// Record is the invite payload (decrypted).
type Record struct {
	Version     int      `json:"version"`
	PeerID      string   `json:"peer_id"`
	PublicKey   string   `json:"public_key"`
	Addresses   []string `json:"addresses"`
	DisplayName string   `json:"display_name"`
	ExpiresAt   int64    `json:"expires_at"`
}

// Sentinel errors returned by Decode.
var (
	ErrSignatureInvalid   = errors.New("invite record signature invalid")
	ErrUnsupportedVersion = errors.New("invite record version unsupported")
	ErrExpired            = errors.New("invite record expired")
	ErrMalformedRecord    = errors.New("invite record blob malformed")
)

const (
	dhtKeyPrefix  = "/opencom-invite/v1/"
	dhtKeyDomain  = "opencom-invite-v1"
	recordKeyInfo = "opencom-invite-record-v1"

	signatureLen = 64                          // ed25519 signature
	nonceLen     = chacha20poly1305.NonceSizeX // 24-byte XChaCha20 nonce
	minBlobLen   = signatureLen + nonceLen + chacha20poly1305.Overhead
)

// DeriveDHTKey returns the libp2p DHT key for an invite identified by code.
// Hashing the code (rather than embedding it) keeps the code itself off the
// wire while still producing a deterministic, lookup-able key.
func DeriveDHTKey(c Code) string {
	h := sha256.New()
	h.Write([]byte(dhtKeyDomain))
	h.Write([]byte(c))
	return dhtKeyPrefix + hex.EncodeToString(h.Sum(nil))
}

// DeriveEncryptionKey returns the 32-byte AEAD key derived from code via HKDF.
// Domain-separated from the DHT key so observing one cannot infer the other.
func DeriveEncryptionKey(c Code) [32]byte {
	r := hkdf.New(sha256.New, []byte(c), nil, []byte(recordKeyInfo))
	var out [32]byte
	if _, err := io.ReadFull(r, out[:]); err != nil {
		panic(fmt.Sprintf("hkdf failed: %v", err))
	}
	return out
}

// Encode serializes r, encrypts under encKey with XChaCha20-Poly1305,
// and signs the (nonce || ciphertext) tuple with signer's ed25519 key.
// Returns blob = signature(64) || nonce(24) || ciphertext.
//
// Version is inside the encrypted plaintext, so the AEAD tag
// authenticates it; no separate signature binding for Version is
// needed.
func Encode(r Record, encKey [32]byte, signer crypto.PrivKey) ([]byte, error) {
	plaintext, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("marshal record: %w", err)
	}

	aead, err := chacha20poly1305.NewX(encKey[:])
	if err != nil {
		return nil, fmt.Errorf("constructing AEAD: %w", err)
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}
	ct := aead.Seal(nil, nonce, plaintext, nil)

	signed := append(append([]byte{}, nonce...), ct...)
	sig, err := signer.Sign(signed)
	if err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}
	if len(sig) != signatureLen {
		return nil, fmt.Errorf("ed25519 signature has unexpected length %d", len(sig))
	}

	out := make([]byte, 0, signatureLen+len(signed))
	out = append(out, sig...)
	out = append(out, signed...)
	return out, nil
}

// Decode verifies blob's signature under signerPub, decrypts the
// ciphertext under encKey, parses the JSON Record, and validates
// version + expiry.
//
// AEAD failures and signature failures both map to ErrSignatureInvalid:
// callers don't branch on the underlying reason — both indicate the
// record is untrustworthy.
func Decode(blob []byte, encKey [32]byte, signerPub crypto.PubKey) (Record, error) {
	var rec Record
	if len(blob) < minBlobLen {
		return rec, ErrMalformedRecord
	}
	sig := blob[:signatureLen]
	signed := blob[signatureLen:]
	nonce := signed[:nonceLen]
	ct := signed[nonceLen:]

	ok, err := signerPub.Verify(signed, sig)
	if err != nil || !ok {
		return rec, ErrSignatureInvalid
	}

	aead, err := chacha20poly1305.NewX(encKey[:])
	if err != nil {
		return rec, fmt.Errorf("constructing AEAD: %w", err)
	}
	plaintext, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return rec, ErrSignatureInvalid
	}

	if err := json.Unmarshal(plaintext, &rec); err != nil {
		return rec, ErrMalformedRecord
	}
	if rec.Version != RecordVersion {
		return rec, fmt.Errorf("%w: got %d, expected %d",
			ErrUnsupportedVersion, rec.Version, RecordVersion)
	}
	if time.Now().Unix() > rec.ExpiresAt {
		return rec, ErrExpired
	}
	return rec, nil
}

// DecodeUnverified decrypts blob under encKey and parses the JSON Record
// without verifying the signature. It is intended for the two-pass
// invite-redeem flow where the verifying public key lives inside the
// encrypted record itself: callers MUST extract the embedded pubkey,
// then re-run full Decode to verify the signature before trusting any
// data.
//
// AEAD authentication still runs (so the ciphertext can't be tampered
// with), but the outer ed25519 signature is skipped. Version + expiry
// are NOT checked — callers must handle them after re-verification.
//
// Returns ErrMalformedRecord on truncated blobs or unparseable JSON,
// ErrSignatureInvalid on AEAD failure (preserving the original Decode's
// error mapping).
func DecodeUnverified(blob []byte, encKey [32]byte) (Record, error) {
	var rec Record
	if len(blob) < minBlobLen {
		return rec, ErrMalformedRecord
	}
	signed := blob[signatureLen:]
	nonce := signed[:nonceLen]
	ct := signed[nonceLen:]

	aead, err := chacha20poly1305.NewX(encKey[:])
	if err != nil {
		return rec, fmt.Errorf("constructing AEAD: %w", err)
	}
	plaintext, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return rec, ErrSignatureInvalid
	}
	if err := json.Unmarshal(plaintext, &rec); err != nil {
		return rec, ErrMalformedRecord
	}
	return rec, nil
}
