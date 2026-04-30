package identity

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"go.yaml.in/yaml/v2"

	"opencom/internal/iox"
)

// PublicIdentity is the YAML blob a user shares with a friend.
type PublicIdentity struct {
	Version        int    `yaml:"version"`
	Name           string `yaml:"name"`
	PeerID         string `yaml:"peer_id"`
	PublicKey      string `yaml:"public_key"`
	RendezvousHint string `yaml:"rendezvous_hint,omitempty"`
}

// Export builds a PublicIdentity from a Keypair and display name.
func Export(kp Keypair, name string) (PublicIdentity, error) {
	pubBytes, err := crypto.MarshalPublicKey(kp.Pub)
	if err != nil {
		return PublicIdentity{}, fmt.Errorf("marshalling public key: %w", err)
	}
	return PublicIdentity{
		Version:   1,
		Name:      name,
		PeerID:    kp.PeerID.String(),
		PublicKey: base64.StdEncoding.EncodeToString(pubBytes),
	}, nil
}

// WriteExport writes ident to path atomically as YAML with mode 0644.
// Parent directories are created with mode 0755 if missing.
func WriteExport(path string, ident PublicIdentity) error {
	data, err := yaml.Marshal(ident)
	if err != nil {
		return fmt.Errorf("marshalling public identity: %w", err)
	}
	return iox.AtomicWriteFile(path, data, 0o644, 0o755)
}

// ReadExport reads and validates a PublicIdentity from path.
func ReadExport(path string) (PublicIdentity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PublicIdentity{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var ident PublicIdentity
	if err := yaml.UnmarshalStrict(data, &ident); err != nil {
		return PublicIdentity{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if ident.Version != 1 {
		return PublicIdentity{}, fmt.Errorf("unsupported public identity version %d", ident.Version)
	}
	pub, err := ident.PubKey()
	if err != nil {
		return PublicIdentity{}, err
	}
	derivedID, err := peer.IDFromPublicKey(pub)
	if err != nil {
		return PublicIdentity{}, fmt.Errorf("deriving peer id: %w", err)
	}
	if derivedID.String() != ident.PeerID {
		return PublicIdentity{}, fmt.Errorf(
			"peer id %s does not match the embedded public key (derived %s)",
			ident.PeerID, derivedID,
		)
	}
	return ident, nil
}

// PubKey reconstructs the libp2p crypto.PubKey from the YAML-encoded form.
func (p PublicIdentity) PubKey() (crypto.PubKey, error) {
	raw, err := base64.StdEncoding.DecodeString(p.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("decoding public key base64: %w", err)
	}
	pub, err := crypto.UnmarshalPublicKey(raw)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling public key: %w", err)
	}
	return pub, nil
}
