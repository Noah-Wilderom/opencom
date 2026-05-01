package cli_test

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/call"
	"opencom/internal/cli"
	"opencom/internal/friends"
	"opencom/internal/identity"
	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
	"opencom/internal/transport/p2p"
)

// syncBuffer is a thread-safe bytes.Buffer wrapper for tests that read
// the buffer concurrently with the cobra command writing to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startCallsServerForCLI mounts a real call engine + IPC server at the
// XDG_RUNTIME_DIR socket so the CLI can dial it.
func startCallsServerForCLI(
	t *testing.T,
	store *friends.Store,
) (rigA *call.Engine, mgrA *call.Manager, peerB string, cleanup func()) {
	t.Helper()
	skipIfWindowsNoUnixSockets(t)

	root := os.Getenv("XDG_RUNTIME_DIR")
	assert.NoError(t, os.MkdirAll(root, 0o700))
	sock := filepath.Join(root, "opencom.sock")
	ln, err := net.Listen("unix", sock)
	assert.NoError(t, err)
	assert.NoError(t, os.Chmod(sock, 0o600))

	ctx, cancel := context.WithCancel(context.Background())

	kpA, err := identity.Generate()
	assert.NoError(t, err)
	kpB, err := identity.Generate()
	assert.NoError(t, err)
	hA, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpA.Priv})
	assert.NoError(t, err)
	hB, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpB.Priv})
	assert.NoError(t, err)
	bInfo, err := p2p.HostAddrInfo(hB)
	assert.NoError(t, err)
	assert.NoError(t, hA.Connect(ctx, bInfo))

	mA := call.NewManager()
	mB := call.NewManager()
	eA := call.NewEngine(hA, mA, zap.NewNop(), time.Now)
	eB := call.NewEngine(hB, mB, zap.NewNop(), time.Now)
	eA.Start()
	eB.Start()

	// Auto-accept incoming on B so the test can assert end-to-end progress.
	go func() {
		select {
		case in := <-mB.Inbound():
			_ = eB.Accept(in)
		case <-ctx.Done():
			return
		}
	}()

	s := ipc.NewServer(zap.NewNop(), "test")
	s.Register("calls.start", methods.CallsStart(eA, mA, store))
	s.Register("calls.list", methods.CallsList(mA))
	s.Register("calls.action", methods.CallsAction(eA, mA))
	s.Register("calls.attach", methods.CallsAttach(mA))
	go func() { _ = s.Serve(ctx, ln) }()

	return eA, mA, hB.ID().String(), func() {
		cancel()
		_ = ln.Close()
		_ = os.Remove(sock)
		_ = hA.Close()
		_ = hB.Close()
	}
}

func TestCall_PrintsRingingAndConnected(t *testing.T) {
	withTempPaths(t)

	store, err := friends.Open(filepath.Join(t.TempDir(), "friends.json"))
	assert.NoError(t, err)
	_, _, peerB, cleanup := startCallsServerForCLI(t, store)
	defer cleanup()
	bID, err := peer.Decode(peerB)
	assert.NoError(t, err)
	assert.NoError(t, store.Add(friends.Friend{
		Name: "Bob", PeerID: bID, PublicKey: "x", AddedAt: time.Now().UTC(),
	}))

	root := cli.NewRootCmd()
	var out syncBuffer
	root.SetOut(&out)
	root.SetErr(&out)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root.SetContext(ctx)

	root.SetArgs([]string{"call", "Bob"})
	done := make(chan struct{})
	go func() {
		_ = root.Execute()
		close(done)
	}()

	// Wait for both "ringing" and "connected" to appear, then cancel
	// to terminate the foreground call.
	assert.Eventually(t, func() bool {
		body := out.String()
		return bytes.Contains([]byte(body), []byte("ringing")) &&
			bytes.Contains([]byte(body), []byte("connected"))
	}, 3*time.Second, 25*time.Millisecond, "expected both ringing and connected in output")

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not return after cancel")
	}
}

func TestCallList_PrintsEmptyWhenNoCalls(t *testing.T) {
	withTempPaths(t)

	store, err := friends.Open(filepath.Join(t.TempDir(), "friends.json"))
	assert.NoError(t, err)
	_, _, _, cleanup := startCallsServerForCLI(t, store)
	defer cleanup()

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"call", "list"})
	assert.NoError(t, root.Execute())

	assert.Contains(t, out.String(), "no active calls")
}

func TestCallHangup_NoSuchCall(t *testing.T) {
	withTempPaths(t)

	store, err := friends.Open(filepath.Join(t.TempDir(), "friends.json"))
	assert.NoError(t, err)
	_, _, _, cleanup := startCallsServerForCLI(t, store)
	defer cleanup()

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"call", "hangup", "missing"})
	err = root.Execute()
	assert.Error(t, err)
	// Server should have responded with NoSuchCall — the surfaced error
	// must include the requested call id, not a generic "daemon not
	// running" or transport error.
	assert.Contains(t, err.Error(), `"missing"`)
}
