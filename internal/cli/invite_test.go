package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/cli"
	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
)

// samplePeerID parses a base58 peer-ID string (produced by
// peer.IDFromPrivateKey().String()) into a real peer.ID. Required for
// JSON-round-trippable test fixtures: peer.ID(rawString) treats the
// string as raw bytes, not as the encoded form, and the resulting
// JSON does not round-trip through peer.Decode.
func samplePeerID(t *testing.T, s string) peer.ID {
	t.Helper()
	id, err := peer.Decode(s)
	assert.NoError(t, err)
	return id
}

const samplePeerIDStr = "12D3KooWG69gneTuz2eVG5QhgPeLay7W2iciaFpJ3L7vzk5jgmTi"

// startInviteServer registers stub IPC handlers with the given map and
// listens on the daemon's expected socket. Lets each test exercise the
// CLI's flag parsing + output formatting against a known response.
func startInviteServer(t *testing.T, handlers map[string]ipc.Handler) func() {
	t.Helper()
	root := os.Getenv("XDG_RUNTIME_DIR")
	assert.NoError(t, os.MkdirAll(root, 0o700))
	sock := filepath.Join(root, "opencom.sock")

	ln, err := net.Listen("unix", sock)
	assert.NoError(t, err)
	assert.NoError(t, os.Chmod(sock, 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	s := ipc.NewServer(zap.NewNop(), "test")
	for name, h := range handlers {
		s.Register(name, h)
	}
	go func() { _ = s.Serve(ctx, ln) }()
	return func() {
		cancel()
		_ = ln.Close()
		_ = os.Remove(sock)
	}
}

func TestInviteCreate_PrintsCodeAndExpiry(t *testing.T) {
	withTempPaths(t)
	stop := startInviteServer(t, map[string]ipc.Handler{
		"invite.create": func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return methods.InviteCreateResult{
				Code:      "OPEN-K8R3-MZ2X",
				URL:       "opencom://join?code=K8R3MZ2X",
				ExpiresAt: time.Now().Add(15 * time.Minute).Unix(),
			}, nil
		},
	})
	defer stop()

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"invite"})
	assert.NoError(t, root.Execute())

	s := out.String()
	assert.Contains(t, s, "Invite code: OPEN-K8R3-MZ2X")
	assert.Contains(t, s, "opencom add OPEN-K8R3-MZ2X")
	assert.NotContains(t, s, "opencom://join", "URL should not appear by default after --offline removal")
}

func TestInviteCreate_ForwardsTTLFlag(t *testing.T) {
	withTempPaths(t)
	var seenTTL string
	stop := startInviteServer(t, map[string]ipc.Handler{
		"invite.create": func(_ context.Context, raw json.RawMessage) (interface{}, error) {
			var p methods.InviteCreateParams
			_ = json.Unmarshal(raw, &p)
			seenTTL = p.TTL
			return methods.InviteCreateResult{
				Code: "OPEN-AAAA-AAAA", ExpiresAt: time.Now().Add(10 * time.Minute).Unix(),
			}, nil
		},
	})
	defer stop()

	root := cli.NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"invite", "--ttl", "10m"})
	assert.NoError(t, root.Execute())

	assert.Equal(t, "10m0s", seenTTL)
}

func TestInviteList_RendersEntries(t *testing.T) {
	withTempPaths(t)
	stop := startInviteServer(t, map[string]ipc.Handler{
		"invite.list": func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return methods.InviteListResult{
				Invites: []methods.InviteListEntry{
					{
						Pretty:    "OPEN-K8R3-MZ2X",
						Active:    true,
						ExpiresAt: time.Now().Add(12 * time.Minute),
					},
					{
						Pretty:     "OPEN-PREV-PREV",
						Consumed:   true,
						ConsumedBy: "12D3KooWAbCdEfGhIjKlMnOpQrStUvWxYz1234567890aBcDe",
						ExpiresAt:  time.Now().Add(-1 * time.Hour),
					},
				},
			}, nil
		},
	})
	defer stop()

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"invite", "list"})
	assert.NoError(t, root.Execute())

	s := out.String()
	assert.Contains(t, s, "OPEN-K8R3-MZ2X")
	assert.Contains(t, s, "active")
	assert.Contains(t, s, "OPEN-PREV-PREV")
	assert.Contains(t, s, "consumed")
}

func TestInviteList_PrintsEmptyMessage(t *testing.T) {
	withTempPaths(t)
	stop := startInviteServer(t, map[string]ipc.Handler{
		"invite.list": func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return methods.InviteListResult{Invites: nil}, nil
		},
	})
	defer stop()

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"invite", "list"})
	assert.NoError(t, root.Execute())

	assert.Contains(t, out.String(), "no invite codes")
}

func TestInviteRevoke_PrintsConfirmation(t *testing.T) {
	withTempPaths(t)
	stop := startInviteServer(t, map[string]ipc.Handler{
		"invite.revoke": func(_ context.Context, raw json.RawMessage) (interface{}, error) {
			var p methods.InviteRevokeParams
			_ = json.Unmarshal(raw, &p)
			assert.Equal(t, "OPEN-K8R3-MZ2X", p.Code)
			return methods.InviteRevokeResult{Removed: "OPEN-K8R3-MZ2X"}, nil
		},
	})
	defer stop()

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"invite", "revoke", "OPEN-K8R3-MZ2X"})
	assert.NoError(t, root.Execute())

	assert.Contains(t, out.String(), "Revoked OPEN-K8R3-MZ2X")
}

func TestInviteRevoke_RequiresArg(t *testing.T) {
	withTempPaths(t)
	root := cli.NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"invite", "revoke"})
	err := root.Execute()
	assert.Error(t, err, "revoke without arg should fail")
}

func TestAdd_PrintsAddedConfirmation(t *testing.T) {
	withTempPaths(t)
	stop := startInviteServer(t, map[string]ipc.Handler{
		"invite.redeem": func(_ context.Context, raw json.RawMessage) (interface{}, error) {
			var p methods.InviteRedeemParams
			_ = json.Unmarshal(raw, &p)
			assert.Equal(t, "OPEN-K8R3-MZ2X", p.Code)
			return methods.InviteRedeemResult{
				Friend: methods.FriendsListEntry{
					Name:    "alice",
					PeerID:  samplePeerID(t, samplePeerIDStr),
					AddedAt: time.Now(),
				},
			}, nil
		},
	})
	defer stop()

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"add", "OPEN-K8R3-MZ2X"})
	assert.NoError(t, root.Execute())

	assert.Contains(t, out.String(), "alice")
}

func TestAdd_AcceptsURL(t *testing.T) {
	withTempPaths(t)
	var sawURL string
	stop := startInviteServer(t, map[string]ipc.Handler{
		"invite.redeem": func(_ context.Context, raw json.RawMessage) (interface{}, error) {
			var p methods.InviteRedeemParams
			_ = json.Unmarshal(raw, &p)
			sawURL = p.Code
			return methods.InviteRedeemResult{
				Friend: methods.FriendsListEntry{
					Name:    "bob",
					PeerID:  samplePeerID(t, samplePeerIDStr),
					AddedAt: time.Now(),
				},
			}, nil
		},
	})
	defer stop()

	root := cli.NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"add", "opencom://join?code=K8R3MZ2X&peer=12D3KooWG69gneTuz2eVG5QhgPeLay7W2iciaFpJ3L7vzk5jgmTi"})
	assert.NoError(t, root.Execute())

	assert.Contains(t, sawURL, "opencom://join")
}

func TestStatus_RendersConsolidatedView(t *testing.T) {
	withTempPaths(t)
	stop := startInviteServer(t, map[string]ipc.Handler{
		"daemon.status_summary": func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return methods.DaemonStatusSummaryResult{
				Identity: methods.DaemonStatusResult{
					PeerID:       samplePeerID(t, samplePeerIDStr),
					StartedAt:    time.Now().Add(-2 * time.Hour),
					Reachability: "public",
					ListenAddrs:  []string{"/ip4/192.0.2.1/tcp/4001/p2p/" + samplePeerIDStr},
				},
				Friends: []methods.FriendsListEntry{
					{Name: "alice", PeerID: samplePeerID(t, samplePeerIDStr), Online: true},
					{Name: "bob", PeerID: samplePeerID(t, samplePeerIDStr), Online: false, LastSeen: time.Now().Add(-30 * time.Minute)},
				},
				Invites: []methods.InviteListEntry{
					{Pretty: "OPEN-K8R3-MZ2X", Active: true, ExpiresAt: time.Now().Add(12 * time.Minute)},
					{Pretty: "OPEN-DEAD-DEAD", Active: false, Consumed: true},
				},
			}, nil
		},
	})
	defer stop()

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"status"})
	assert.NoError(t, root.Execute())

	s := out.String()
	assert.Contains(t, s, "12D3KooWG69gneTuz2eVG5QhgPeLay7W2iciaFpJ3L7vzk5jgmTi", "should show peer id")
	assert.Contains(t, s, "public", "should show reachability")
	assert.Contains(t, s, "alice", "should show friend names")
	assert.Contains(t, s, "online")
	assert.Contains(t, s, "bob")
	assert.Contains(t, s, "OPEN-K8R3-MZ2X", "should list active invite codes")
	assert.NotContains(t, s, "OPEN-DEAD-DEAD", "should hide non-active invites from active section")
	assert.Contains(t, s, "Active invites  : 1")
}
