package identity

import (
	"crypto/ed25519"
	"crypto/sha512"
	"errors"
	"fmt"

	"filippo.io/edwards25519"
)

// ToX25519Public converts an ed25519 public key to the corresponding
// X25519 (Curve25519) public key via the Edwards-to-Montgomery
// birational map specified in RFC 7748 §4.1.
//
// The two key types share the same scalar field, so the same underlying
// secret can be used for both signing (ed25519) and Diffie-Hellman
// (X25519). This is the standard libsodium pattern.
//
// Safe for concurrent use.
func ToX25519Public(ed25519Pub []byte) ([32]byte, error) {
	var out [32]byte
	if len(ed25519Pub) != 32 {
		return out, fmt.Errorf("ed25519 public key must be 32 bytes, got %d", len(ed25519Pub))
	}
	pt, err := new(edwards25519.Point).SetBytes(ed25519Pub)
	if err != nil {
		return out, fmt.Errorf("decoding ed25519 point: %w", err)
	}
	mont := pt.BytesMontgomery()
	if len(mont) != 32 {
		return out, errors.New("BytesMontgomery did not return 32 bytes")
	}
	copy(out[:], mont)
	return out, nil
}

// ToX25519Private converts an ed25519 private key to the corresponding
// X25519 private key. ed25519 derives its scalar by hashing the 32-byte
// seed with SHA-512 and clamping; we reuse those same first 32 bytes
// (with the standard clamping) as the X25519 scalar.
//
// This means a single ed25519 identity key implies a stable X25519
// keypair — friends who exchange ed25519 public keys can derive a
// shared X25519 secret without any extra material.
//
// Returns an error if priv is not exactly ed25519.PrivateKeySize bytes.
// Safe for concurrent use.
func ToX25519Private(priv ed25519.PrivateKey) ([32]byte, error) {
	var out [32]byte
	if len(priv) != ed25519.PrivateKeySize {
		return out, fmt.Errorf("ed25519 private key must be %d bytes, got %d",
			ed25519.PrivateKeySize, len(priv))
	}
	seed := priv.Seed()
	h := sha512.Sum512(seed)
	copy(out[:], h[:32])
	out[0] &= 0b11111000  // clear bits 0,1,2
	out[31] &= 0b01111111 // clear bit 7
	out[31] |= 0b01000000 // set bit 6
	return out, nil
}
