package ipc_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/ipc"
)

// skipIfWindowsNoUnixSockets skips a test that hits raw AF_UNIX
// sockets directly. Windows production code uses Microsoft/go-winio
// named pipes (see transport_windows.go), so these tests aren't
// validating Windows production behavior — they exercise the
// Unix-only test fixture.
func skipIfWindowsNoUnixSockets(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses named pipes via go-winio; skipping unix-socket test fixture")
	}
}

// startServer spins up an ipc.Server on a Unix socket inside t.TempDir() and
// returns the socket path. The server is shut down when the test context is
// canceled.
func startServer(t *testing.T, ctx context.Context, register func(s *ipc.Server)) string {
	t.Helper()
	skipIfWindowsNoUnixSockets(t)
	sock := filepath.Join(t.TempDir(), "test.sock")
	ln, err := net.Listen("unix", sock)
	assert.NoError(t, err)

	s := ipc.NewServer(zap.NewNop(), "test-version")
	if register != nil {
		register(s)
	}
	go func() { _ = s.Serve(ctx, ln) }()
	return sock
}

// readMsgLine reads one newline-delimited JSON message from conn.
func readMsgLine(t *testing.T, conn net.Conn) ipc.Message {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	if !scanner.Scan() {
		t.Fatalf("no message: %v", scanner.Err())
	}
	var m ipc.Message
	assert.NoError(t, json.Unmarshal(scanner.Bytes(), &m))
	return m
}

func TestServer_SendsHelloOnConnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := startServer(t, ctx, nil)

	conn, err := net.Dial("unix", sock)
	assert.NoError(t, err)
	defer conn.Close()

	hello := readMsgLine(t, conn)
	assert.Equal(t, "hello", hello.Method)
	assert.True(t, hello.IsNotification())

	var p ipc.HelloParams
	assert.NoError(t, json.Unmarshal(hello.Params, &p))
	assert.Equal(t, ipc.ProtocolVersion, p.Version)
	assert.Equal(t, "test-version", p.DaemonVersion)
}

func TestServer_DispatchesRegisteredHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := startServer(t, ctx, func(s *ipc.Server) {
		s.Register("ping", func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return "pong", nil
		})
	})

	conn, err := net.Dial("unix", sock)
	assert.NoError(t, err)
	defer conn.Close()

	_ = readMsgLine(t, conn) // discard hello

	id := int64(1)
	req := ipc.Message{JSONRPC: ipc.JSONRPCVersion, ID: &id, Method: "ping"}
	enc := json.NewEncoder(conn)
	assert.NoError(t, enc.Encode(&req))

	resp := readMsgLine(t, conn)
	assert.True(t, resp.IsResponse())
	assert.Equal(t, int64(1), *resp.ID)
	assert.Nil(t, resp.Error)

	var result string
	assert.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.Equal(t, "pong", result)
}

func TestServer_UnknownMethodReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := startServer(t, ctx, nil)

	conn, err := net.Dial("unix", sock)
	assert.NoError(t, err)
	defer conn.Close()
	_ = readMsgLine(t, conn) // hello

	id := int64(1)
	enc := json.NewEncoder(conn)
	assert.NoError(t, enc.Encode(&ipc.Message{JSONRPC: ipc.JSONRPCVersion, ID: &id, Method: "nope.not.a.method"}))

	resp := readMsgLine(t, conn)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, ipc.ErrCodeMethodNotFound, resp.Error.Code)
}

func TestServer_HandlerErrorIsForwarded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := startServer(t, ctx, func(s *ipc.Server) {
		s.Register("explode", func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return nil, ipc.NewError(ipc.ErrCodeNoSuchFriend, "no friend named Bob")
		})
	})

	conn, err := net.Dial("unix", sock)
	assert.NoError(t, err)
	defer conn.Close()
	_ = readMsgLine(t, conn) // hello

	id := int64(1)
	enc := json.NewEncoder(conn)
	assert.NoError(t, enc.Encode(&ipc.Message{JSONRPC: ipc.JSONRPCVersion, ID: &id, Method: "explode"}))

	resp := readMsgLine(t, conn)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, ipc.ErrCodeNoSuchFriend, resp.Error.Code)
	assert.Equal(t, "no friend named Bob", resp.Error.Message)
}

func TestServer_PlainErrorBecomesInternalError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := startServer(t, ctx, func(s *ipc.Server) {
		s.Register("explode", func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return nil, assertErr("boom")
		})
	})

	conn, err := net.Dial("unix", sock)
	assert.NoError(t, err)
	defer conn.Close()
	_ = readMsgLine(t, conn) // hello

	id := int64(1)
	enc := json.NewEncoder(conn)
	assert.NoError(t, enc.Encode(&ipc.Message{JSONRPC: ipc.JSONRPCVersion, ID: &id, Method: "explode"}))

	resp := readMsgLine(t, conn)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, ipc.ErrCodeInternalError, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "boom")
}

// assertErr is a tiny error type for tests.
type assertErr string

func (a assertErr) Error() string { return string(a) }

func TestServer_NotificationGetsNoResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := startServer(t, ctx, func(s *ipc.Server) {
		s.Register("noop", func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return "ignored", nil
		})
	})

	conn, err := net.Dial("unix", sock)
	assert.NoError(t, err)
	defer conn.Close()
	_ = readMsgLine(t, conn) // hello

	enc := json.NewEncoder(conn)
	assert.NoError(t, enc.Encode(&ipc.Message{JSONRPC: ipc.JSONRPCVersion, Method: "noop"}))

	// No id => no response. Probe by sending a follow-up request and
	// expecting only its response back.
	id := int64(99)
	assert.NoError(t, enc.Encode(&ipc.Message{JSONRPC: ipc.JSONRPCVersion, ID: &id, Method: "noop"}))

	resp := readMsgLine(t, conn)
	assert.True(t, resp.IsResponse())
	assert.Equal(t, int64(99), *resp.ID)
}

func TestServer_HandlesConcurrentConnections(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := startServer(t, ctx, func(s *ipc.Server) {
		s.Register("ping", func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return "pong", nil
		})
	})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial("unix", sock)
			assert.NoError(t, err)
			defer conn.Close()
			_ = readMsgLine(t, conn) // hello

			id := int64(1)
			enc := json.NewEncoder(conn)
			assert.NoError(t, enc.Encode(&ipc.Message{JSONRPC: ipc.JSONRPCVersion, ID: &id, Method: "ping"}))

			resp := readMsgLine(t, conn)
			assert.NotNil(t, resp.Result)
		}()
	}
	wg.Wait()
}

func TestServer_HandlerCanEmitEventsToSubscriber(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sock := startServer(t, ctx, func(s *ipc.Server) {
		s.Register("watch", func(hctx context.Context, _ json.RawMessage) (interface{}, error) {
			conn := ipc.ConnFromContext(hctx)
			assert.NotNil(t, conn, "handler should see a Conn in context")
			subID := "s-1"
			go func() {
				for i := 0; i < 3; i++ {
					_ = conn.EmitEvent(subID, "tick", map[string]int{"n": i})
				}
			}()
			return map[string]string{"subscription_id": subID}, nil
		})
	})

	conn, err := net.Dial("unix", sock)
	assert.NoError(t, err)
	defer conn.Close()

	// Persistent scanner: bufio reads ahead, so each readMsgLine call would
	// otherwise discard already-buffered messages. Multi-message tests must
	// share a scanner across reads.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	readNext := func() ipc.Message {
		t.Helper()
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if !scanner.Scan() {
			t.Fatalf("no message: %v", scanner.Err())
		}
		var m ipc.Message
		assert.NoError(t, json.Unmarshal(scanner.Bytes(), &m))
		return m
	}

	_ = readNext() // hello

	id := int64(1)
	enc := json.NewEncoder(conn)
	assert.NoError(t, enc.Encode(&ipc.Message{
		JSONRPC: ipc.JSONRPCVersion, ID: &id, Method: "watch",
	}))

	resp := readNext()
	assert.True(t, resp.IsResponse())
	assert.Nil(t, resp.Error)

	for i := 0; i < 3; i++ {
		ev := readNext()
		assert.True(t, ev.IsNotification())
		assert.Equal(t, "event", ev.Method)

		var p struct {
			Sub  string          `json:"sub"`
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		assert.NoError(t, json.Unmarshal(ev.Params, &p))
		assert.Equal(t, "s-1", p.Sub)
		assert.Equal(t, "tick", p.Kind)
	}
}

func TestServer_ConnDoneFiresOnClientDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	doneObserved := make(chan struct{})
	sock := startServer(t, ctx, func(s *ipc.Server) {
		s.Register("attach", func(hctx context.Context, _ json.RawMessage) (interface{}, error) {
			conn := ipc.ConnFromContext(hctx)
			go func() {
				<-conn.Done()
				close(doneObserved)
			}()
			return map[string]string{"ok": "yes"}, nil
		})
	})

	conn, err := net.Dial("unix", sock)
	assert.NoError(t, err)
	_ = readMsgLine(t, conn) // hello
	id := int64(1)
	enc := json.NewEncoder(conn)
	assert.NoError(t, enc.Encode(&ipc.Message{JSONRPC: ipc.JSONRPCVersion, ID: &id, Method: "attach"}))
	_ = readMsgLine(t, conn) // response

	conn.Close()

	select {
	case <-doneObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("Conn.Done() did not fire after client disconnect")
	}
}

func TestServer_ContextCancellationStopsServe(t *testing.T) {
	skipIfWindowsNoUnixSockets(t)
	t.Parallel()

	sock := filepath.Join(t.TempDir(), "test.sock")
	ln, err := net.Listen("unix", sock)
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	s := ipc.NewServer(zap.NewNop(), "test")
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after ctx cancel")
	}
}
