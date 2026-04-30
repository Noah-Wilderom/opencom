package methods_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/stretchr/testify/assert"

	"opencom/internal/config"
	"opencom/internal/identity"
	"opencom/internal/ipc/methods"
)

func TestIdentityGet_PopulatesAllFields(t *testing.T) {
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)

	cfg := config.Default()
	cfg.User.Name = "Alice"

	h := methods.IdentityGet(kp, cfg)
	out, err := h(context.Background(), nil)
	assert.NoError(t, err)

	raw, err := json.Marshal(out)
	assert.NoError(t, err)

	var got methods.IdentityGetResult
	assert.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, kp.PeerID, got.PeerID)
	assert.Equal(t, "Alice", got.DisplayName)
	assert.NotEqual(t, "", got.PublicKey)

	// Verify the encoded pubkey round-trips back to the original key.
	rawKey, err := base64.StdEncoding.DecodeString(got.PublicKey)
	assert.NoError(t, err)
	pub, err := crypto.UnmarshalPublicKey(rawKey)
	assert.NoError(t, err)
	assert.True(t, pub.Equals(kp.Pub))
}

func TestIdentityGet_EmptyDisplayNameRoundTrips(t *testing.T) {
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)

	cfg := config.Default()
	cfg.User.Name = ""

	h := methods.IdentityGet(kp, cfg)
	out, err := h(context.Background(), nil)
	assert.NoError(t, err)

	raw, err := json.Marshal(out)
	assert.NoError(t, err)

	// Field is present (no omitempty) and the value is the empty string.
	assert.Contains(t, string(raw), `"display_name":""`)

	var got methods.IdentityGetResult
	assert.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "", got.DisplayName)
}
