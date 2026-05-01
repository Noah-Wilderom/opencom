//go:build unix

package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/app"
	"opencom/internal/config"
	"opencom/internal/identity"
	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
	"opencom/internal/transport/p2p"
)

// startRealDaemon launches app.Run in a goroutine. Returns the socket path
// and a cleanup function.
func startRealDaemon(t *testing.T) (sock string, peerID string, cleanup func()) {
	t.Helper()
	root := t.TempDir()
	paths := config.Paths{
		ConfigDir:   root,
		StateDir:    filepath.Join(root, "state"),
		ConfigFile:  filepath.Join(root, "config.yaml"),
		PrivateKey:  filepath.Join(root, "priv.key"),
		FriendsFile: filepath.Join(root, "friends.json"),
		SocketPath:  filepath.Join(root, "opencom.sock"),
		Peerstore:   filepath.Join(root, "state", "peerstore"),
		LogFile:     filepath.Join(root, "state", "daemon.log"),
	}
	assert.NoError(t, os.MkdirAll(paths.StateDir, 0o700))

	kp, err := identity.Generate()
	assert.NoError(t, err)

	cfg := config.Default()
	cfg.User.Name = "E2E"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = app.Run(ctx, app.Options{
			Paths:        paths,
			Config:       cfg,
			Identity:     kp,
			Log:          zap.NewNop(),
			Version:      "e2e",
			StartedAt:    time.Now().UTC(),
			DisableAudio: true, // tests don't open real audio devices
		})
		close(done)
	}()

	// Wait for the socket to appear.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(paths.SocketPath); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	return paths.SocketPath, kp.PeerID.String(), func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("daemon did not exit")
		}
	}
}

func TestE2E_DaemonStatusFlow(t *testing.T) {
	sock, peerID, cleanup := startRealDaemon(t)
	defer cleanup()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	c, err := ipc.Dial(dialCtx, sock)
	assert.NoError(t, err)
	defer c.Close()

	var status methods.DaemonStatusResult
	assert.NoError(t, c.Call(context.Background(), "daemon.status", nil, &status))
	assert.Equal(t, "e2e", status.Version)
	assert.Equal(t, peerID, status.PeerID.String())
	assert.NotEmpty(t, status.ListenAddrs, "daemon should expose libp2p listen addrs in M3")
}

func TestE2E_IdentityGetFlow(t *testing.T) {
	sock, peerID, cleanup := startRealDaemon(t)
	defer cleanup()

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()

	var id methods.IdentityGetResult
	assert.NoError(t, c.Call(context.Background(), "identity.get", nil, &id))
	assert.Equal(t, peerID, id.PeerID.String())
	assert.Equal(t, "E2E", id.DisplayName)
	assert.NotEqual(t, "", id.PublicKey)
}

func TestE2E_DaemonShutdownFlow(t *testing.T) {
	sock, _, cleanup := startRealDaemon(t)
	defer cleanup()

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()

	var resp map[string]string
	assert.NoError(t, c.Call(context.Background(), "daemon.shutdown", nil, &resp))
	assert.Equal(t, "shutting down", resp["status"])

	// Daemon should exit shortly. Probe by re-dialing and expecting failure.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		dialCtx, dialCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		c2, err := ipc.Dial(dialCtx, sock)
		dialCancel()
		if err != nil {
			return // success — daemon stopped
		}
		c2.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("daemon did not shut down within 2s")
}

func TestE2E_UnknownMethodReturnsError(t *testing.T) {
	sock, _, cleanup := startRealDaemon(t)
	defer cleanup()

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()

	err = c.Call(context.Background(), "calls.bogus", nil, nil)
	assert.Error(t, err)
	var rpcErr *ipc.Error
	assert.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, ipc.ErrCodeMethodNotFound, rpcErr.Code)
}

func TestE2E_CallFriendFlow(t *testing.T) {
	sockA, peerA, cleanupA := startRealDaemon(t)
	defer cleanupA()
	sockB, peerB, cleanupB := startRealDaemon(t)
	defer cleanupB()
	_ = peerA

	cA, err := ipc.Dial(context.Background(), sockA)
	assert.NoError(t, err)
	defer cA.Close()
	cB, err := ipc.Dial(context.Background(), sockB)
	assert.NoError(t, err)
	defer cB.Close()

	keyDir := t.TempDir()
	bExportPath := filepath.Join(keyDir, "b.pub.key")
	assert.NoError(t, exportPub(cB, bExportPath))
	aExportPath := filepath.Join(keyDir, "a.pub.key")
	assert.NoError(t, exportPub(cA, aExportPath))

	// A learns about B; B learns about A.
	assert.NoError(t, cA.Call(context.Background(), "friends.add",
		map[string]string{"key_path": bExportPath, "name": "Bob"}, nil))
	assert.NoError(t, cB.Call(context.Background(), "friends.add",
		map[string]string{"key_path": aExportPath, "name": "Alice"}, nil))

	// Brief warm-up: confirm both daemons report listen addresses before we
	// dial. We rely on libp2p's mDNS multicast to populate each daemon's
	// peerstore with the other's address by the time `calls.start` invokes
	// host.NewStream. On loopback this is fast (~50ms).
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var s methods.DaemonStatusResult
		_ = cA.Call(context.Background(), "daemon.status", nil, &s)
		if len(s.ListenAddrs) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A places a call to "Bob".
	sub, err := cA.Subscribe(context.Background(), "calls.start",
		map[string]string{"target": "Bob"})
	assert.NoError(t, err)
	defer sub.Close()

	// First event should be ringing.
	expectState(t, sub, "ringing", 5*time.Second)

	// On B's side, find the inbound call and accept.
	inboundID := waitForInbound(t, cB, peerB, 5*time.Second)
	assert.NoError(t, cB.Call(context.Background(), "calls.action",
		map[string]string{"call_id": inboundID, "action": "accept"}, nil))

	// A should reach connected.
	expectState(t, sub, "connected", 5*time.Second)

	// Hang up from A.
	var listed methods.CallsListResult
	assert.NoError(t, cA.Call(context.Background(), "calls.list", nil, &listed))
	assert.Len(t, listed.Calls, 1)
	assert.NoError(t, cA.Call(context.Background(), "calls.action", map[string]string{
		"call_id": listed.Calls[0].CallID, "action": "hangup",
	}, nil))

	expectState(t, sub, "ended", 5*time.Second)
}

// exportPub writes the daemon's own public identity to dest via identity.get
// + a hand-rolled YAML write through identity.WriteExport.
func exportPub(c *ipc.Client, dest string) error {
	var got methods.IdentityGetResult
	if err := c.Call(context.Background(), "identity.get", nil, &got); err != nil {
		return err
	}
	pub := identity.PublicIdentity{
		Version:   1,
		Name:      got.DisplayName,
		PeerID:    got.PeerID.String(),
		PublicKey: got.PublicKey,
	}
	return identity.WriteExport(dest, pub)
}

// waitForInbound polls B's calls.list until an inbound call appears.
func waitForInbound(t *testing.T, c *ipc.Client, _ string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var res methods.CallsListResult
		if err := c.Call(context.Background(), "calls.list", nil, &res); err == nil {
			for _, ce := range res.Calls {
				if ce.Direction == "inbound" {
					return ce.CallID
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no inbound call appeared within %s", timeout)
	return ""
}

// expectState drains sub.Events until it sees an event whose state matches
// the target. Fails the test if the timeout elapses first.
func expectState(t *testing.T, sub *ipc.Subscription, target string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-sub.Events:
			if !ok {
				t.Fatalf("subscription closed before reaching state %q", target)
			}
			var change struct {
				State string `json:"state"`
			}
			if err := json.Unmarshal(ev.Data, &change); err != nil {
				t.Fatalf("bad event payload: %v: %s", err, ev.Data)
			}
			if change.State == target {
				return
			}
		case <-deadline:
			t.Fatalf("did not reach state %q within %s", target, timeout)
		}
	}
}

func startDHTBootstrap(t *testing.T) (peer.AddrInfo, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	priv, _, err := libp2pcrypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	h, err := p2p.New(ctx, p2p.HostOptions{
		PrivKey:        priv,
		BootstrapPeers: []peer.AddrInfo{}, // explicitly empty — no upstream
		// Force server mode so the routing table populates with daemons
		// that connect to us. Without this the DHT stays in client mode
		// (AutoNAT can't confirm reachability on a tiny loopback network),
		// the DHT protocol is never advertised, and peers fail to add the
		// bootstrap to their routing tables.
		DHTMode: dht.ModeServer,
	})
	assert.NoError(t, err)
	info, err := p2p.HostAddrInfo(h)
	assert.NoError(t, err)
	return info, func() {
		cancel()
		_ = h.Close()
	}
}

// startRealDaemonWithBootstrap is like startRealDaemon but threads a
// caller-controlled bootstrap list and disables mDNS, so the daemon's
// only path to other peers is the DHT.
func startRealDaemonWithBootstrap(t *testing.T, bootstraps []peer.AddrInfo) (sock string, peerID string, cleanup func()) {
	t.Helper()
	root := t.TempDir()
	paths := config.Paths{
		ConfigDir:   root,
		StateDir:    filepath.Join(root, "state"),
		ConfigFile:  filepath.Join(root, "config.yaml"),
		PrivateKey:  filepath.Join(root, "priv.key"),
		FriendsFile: filepath.Join(root, "friends.json"),
		SocketPath:  filepath.Join(root, "opencom.sock"),
		Peerstore:   filepath.Join(root, "state", "peerstore"),
		LogFile:     filepath.Join(root, "state", "daemon.log"),
		PeerCache:   filepath.Join(root, "state", "peer-cache.json"),
	}
	assert.NoError(t, os.MkdirAll(paths.StateDir, 0o700))

	kp, err := identity.Generate()
	assert.NoError(t, err)

	cfg := config.Default()
	cfg.User.Name = "E2EBoot"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = app.Run(ctx, app.Options{
			Paths:          paths,
			Config:         cfg,
			Identity:       kp,
			Log:            zap.NewNop(),
			Version:        "e2e",
			StartedAt:      time.Now().UTC(),
			DisableMDNS:    true,
			HostBootstraps: bootstraps,
			DisableAudio:   true,
		})
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(paths.SocketPath); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	return paths.SocketPath, kp.PeerID.String(), func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("daemon did not exit")
		}
	}
}

func TestE2E_CallAcrossSimulatedNetworks(t *testing.T) {
	bootstrapInfo, cleanupBoot := startDHTBootstrap(t)
	defer cleanupBoot()

	sockA, peerA, cleanupA := startRealDaemonWithBootstrap(t, []peer.AddrInfo{bootstrapInfo})
	defer cleanupA()
	sockB, peerB, cleanupB := startRealDaemonWithBootstrap(t, []peer.AddrInfo{bootstrapInfo})
	defer cleanupB()
	_ = peerA

	cA, err := ipc.Dial(context.Background(), sockA)
	assert.NoError(t, err)
	defer cA.Close()
	cB, err := ipc.Dial(context.Background(), sockB)
	assert.NoError(t, err)
	defer cB.Close()

	keyDir := t.TempDir()
	bExportPath := filepath.Join(keyDir, "b.pub.key")
	assert.NoError(t, exportPub(cB, bExportPath))
	aExportPath := filepath.Join(keyDir, "a.pub.key")
	assert.NoError(t, exportPub(cA, aExportPath))

	assert.NoError(t, cA.Call(context.Background(), "friends.add",
		map[string]string{"key_path": bExportPath, "name": "Bob"}, nil))
	assert.NoError(t, cB.Call(context.Background(), "friends.add",
		map[string]string{"key_path": aExportPath, "name": "Alice"}, nil))

	// Give the publisher time to publish initial records via the DHT.
	time.Sleep(2 * time.Second)

	sub, err := cA.Subscribe(context.Background(), "calls.start",
		map[string]string{"target": "Bob"})
	assert.NoError(t, err)
	defer sub.Close()

	expectState(t, sub, "ringing", 10*time.Second)

	inboundID := waitForInbound(t, cB, peerB, 10*time.Second)
	assert.NoError(t, cB.Call(context.Background(), "calls.action",
		map[string]string{"call_id": inboundID, "action": "accept"}, nil))

	expectState(t, sub, "connected", 10*time.Second)

	var listed methods.CallsListResult
	assert.NoError(t, cA.Call(context.Background(), "calls.list", nil, &listed))
	assert.Len(t, listed.Calls, 1)
	assert.NoError(t, cA.Call(context.Background(), "calls.action", map[string]string{
		"call_id": listed.Calls[0].CallID, "action": "hangup",
	}, nil))
	expectState(t, sub, "ended", 5*time.Second)
}

func TestE2E_InviteFlow(t *testing.T) {
	bootstrapInfo, cleanupBoot := startDHTBootstrap(t)
	defer cleanupBoot()

	sockA, _, cleanupA := startRealDaemonWithBootstrap(t, []peer.AddrInfo{bootstrapInfo})
	defer cleanupA()
	sockB, _, cleanupB := startRealDaemonWithBootstrap(t, []peer.AddrInfo{bootstrapInfo})
	defer cleanupB()

	cA, err := ipc.Dial(context.Background(), sockA)
	assert.NoError(t, err)
	defer cA.Close()
	cB, err := ipc.Dial(context.Background(), sockB)
	assert.NoError(t, err)
	defer cB.Close()

	// Wait briefly for the publishers to settle.
	time.Sleep(2 * time.Second)

	// A creates an invite.
	var createResp methods.InviteCreateResult
	assert.NoError(t, cA.Call(context.Background(), "invite.create",
		methods.InviteCreateParams{}, &createResp))
	assert.NotEmpty(t, createResp.Code)

	// B redeems.
	var redeemResp methods.InviteRedeemResult
	err = cB.Call(context.Background(), "invite.redeem",
		methods.InviteRedeemParams{Code: createResp.Code}, &redeemResp)
	assert.NoError(t, err)

	// Both daemons' friends.list should now show one friend.
	var listA, listB methods.FriendsListResult
	assert.NoError(t, cA.Call(context.Background(), "friends.list", nil, &listA))
	assert.NoError(t, cB.Call(context.Background(), "friends.list", nil, &listB))
	assert.Len(t, listA.Friends, 1)
	assert.Len(t, listB.Friends, 1)

	// A's invite.list should show the code as consumed.
	var inviteList methods.InviteListResult
	assert.NoError(t, cA.Call(context.Background(), "invite.list", nil, &inviteList))
	assert.Len(t, inviteList.Invites, 1)
	assert.True(t, inviteList.Invites[0].Consumed)
}
