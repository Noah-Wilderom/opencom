package methods

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"opencom/internal/config"
	"opencom/internal/identity"
	"opencom/internal/ipc"
)

// IdentityGetResult is the JSON shape of the identity.get response.
type IdentityGetResult struct {
	PeerID      peer.ID `json:"peer_id"`
	PublicKey   string  `json:"public_key"`
	DisplayName string  `json:"display_name"`
}

// IdentityGet returns an ipc.Handler that reports the daemon's identity:
// peer ID, base64-encoded libp2p public key, and configured display name.
func IdentityGet(kp identity.Keypair, cfg config.Config) ipc.Handler {
	return func(_ context.Context, _ json.RawMessage) (interface{}, error) {
		// MarshalPublicKey can fail for unsupported key types; defensive for
		// future non-Ed25519 paths.
		pubBytes, err := crypto.MarshalPublicKey(kp.Pub)
		if err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInternalError, fmt.Sprintf("marshalling pubkey: %v", err))
		}
		return IdentityGetResult{
			PeerID:      kp.PeerID,
			PublicKey:   base64.StdEncoding.EncodeToString(pubBytes),
			DisplayName: cfg.User.Name,
		}, nil
	}
}
