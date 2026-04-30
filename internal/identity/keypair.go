package identity

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"opencom/internal/iox"
)

// Keypair pairs the libp2p private/public keys with the derived peer ID.
type Keypair struct {
	Priv   crypto.PrivKey
	Pub    crypto.PubKey
	PeerID peer.ID
}

// Generate creates a new Ed25519 libp2p keypair.
func Generate() (Keypair, error) {
	priv, pub, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return Keypair{}, fmt.Errorf("generating ed25519 keypair: %w", err)
	}
	id, err := peer.IDFromPublicKey(pub)
	if err != nil {
		return Keypair{}, fmt.Errorf("deriving peer id: %w", err)
	}
	return Keypair{Priv: priv, Pub: pub, PeerID: id}, nil
}

// PeerIDFromPubKey is a convenience wrapper for callers that only have the
// pubkey at hand (e.g., a friend record).
func PeerIDFromPubKey(pub crypto.PubKey) (peer.ID, error) {
	return peer.IDFromPublicKey(pub)
}

// Save writes the private key in libp2p's protobuf format atomically with
// mode 0600. Parent directory is created with mode 0700 if missing.
func Save(path string, kp Keypair) error {
	data, err := crypto.MarshalPrivateKey(kp.Priv)
	if err != nil {
		return fmt.Errorf("marshalling private key: %w", err)
	}
	return iox.AtomicWriteFile(path, data, 0o600, 0o700)
}

// Load reads the private key from path. Returns an error if the file mode
// is more permissive than 0600 (only enforced on non-Windows).
func Load(path string) (Keypair, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Keypair{}, err
	}
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			return Keypair{}, fmt.Errorf(
				"private key %s has mode %o; expected 0600 (run `chmod 600 %s`)",
				path, mode, path,
			)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Keypair{}, fmt.Errorf("reading private key: %w", err)
	}
	priv, err := crypto.UnmarshalPrivateKey(data)
	if err != nil {
		return Keypair{}, fmt.Errorf("unmarshalling private key: %w", err)
	}
	if priv.Type() != crypto.Ed25519 {
		return Keypair{}, errors.New("private key is not Ed25519")
	}
	pub := priv.GetPublic()
	id, err := peer.IDFromPublicKey(pub)
	if err != nil {
		return Keypair{}, fmt.Errorf("deriving peer id: %w", err)
	}
	return Keypair{Priv: priv, Pub: pub, PeerID: id}, nil
}
