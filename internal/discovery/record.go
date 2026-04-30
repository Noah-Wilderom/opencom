package discovery

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"golang.org/x/crypto/chacha20poly1305"
)

// RecordVersion is the current schema version.
const RecordVersion = 1

// Record is the discovery payload after decryption.
type Record struct {
	Version   int      `json:"version"`
	Addresses []string `json:"addresses"`
	Timestamp int64    `json:"timestamp"`
}

// Sentinel errors returned by Decode.
var (
	ErrSignatureInvalid   = errors.New("record signature invalid")
	ErrUnsupportedVersion = errors.New("record version unsupported")
	ErrStale              = errors.New("record is stale")
	ErrMalformedRecord    = errors.New("record blob malformed")
)

const (
	signatureLen = 64                          // ed25519 signature
	nonceLen     = chacha20poly1305.NonceSizeX // 24-byte XChaCha20 nonce
	minBlobLen   = signatureLen + nonceLen + chacha20poly1305.Overhead
)

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
// version + freshness.
func Decode(blob []byte, encKey [32]byte, signerPub crypto.PubKey, maxAge time.Duration) (Record, error) {
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
		// AEAD failure treated as ErrSignatureInvalid; both integrity
		// guarantees protect the blob and the user only cares the
		// record is untrustworthy.
		return rec, ErrSignatureInvalid
	}

	if err := json.Unmarshal(plaintext, &rec); err != nil {
		return rec, ErrMalformedRecord
	}
	if rec.Version != RecordVersion {
		return rec, fmt.Errorf("%w: got %d, expected %d",
			ErrUnsupportedVersion, rec.Version, RecordVersion)
	}
	age := time.Since(time.Unix(rec.Timestamp, 0))
	if age > maxAge {
		return rec, ErrStale
	}
	return rec, nil
}
