package ipc_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"opencom/internal/ipc"
)

func TestClient_DialReceivesHelloAndExposesDaemonVersion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := startServer(t, ctx, nil)

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	c, err := ipc.Dial(dialCtx, sock)
	assert.NoError(t, err)
	defer c.Close()

	assert.Equal(t, "test-version", c.DaemonVersion())
}

func TestClient_CallReturnsResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := startServer(t, ctx, func(s *ipc.Server) {
		s.Register("echo", func(_ context.Context, params json.RawMessage) (interface{}, error) {
			var in map[string]string
			_ = json.Unmarshal(params, &in)
			return map[string]string{"echoed": in["v"]}, nil
		})
	})

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()

	var out map[string]string
	assert.NoError(t, c.Call(context.Background(), "echo", map[string]string{"v": "hello"}, &out))
	assert.Equal(t, "hello", out["echoed"])
}

func TestClient_CallReturnsErrorFromHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := startServer(t, ctx, func(s *ipc.Server) {
		s.Register("nope", func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return nil, ipc.NewError(ipc.ErrCodeNoSuchFriend, "no friend")
		})
	})

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()

	err = c.Call(context.Background(), "nope", nil, nil)
	assert.Error(t, err)
	var rpcErr *ipc.Error
	assert.True(t, errors.As(err, &rpcErr), "should be *ipc.Error, got %T: %v", err, err)
	assert.Equal(t, ipc.ErrCodeNoSuchFriend, rpcErr.Code)
}

func TestClient_DialFailsOnVersionMismatch(t *testing.T) {
	t.Parallel()

	sock := filepath.Join(t.TempDir(), "test.sock")
	ln, err := net.Listen("unix", sock)
	assert.NoError(t, err)
	defer ln.Close()

	// Hand-rolled server that sends a hello with the wrong version.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		params, _ := json.Marshal(ipc.HelloParams{Version: "999", DaemonVersion: "fake"})
		_ = json.NewEncoder(conn).Encode(&ipc.Message{
			JSONRPC: ipc.JSONRPCVersion, Method: "hello", Params: params,
		})
		// keep conn open until test releases it
		time.Sleep(time.Second)
	}()

	dialCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = ipc.Dial(dialCtx, sock)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "protocol mismatch")
}

func TestClient_ConcurrentCallsAreCorrelatedByID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := startServer(t, ctx, func(s *ipc.Server) {
		s.Register("identity", func(_ context.Context, params json.RawMessage) (interface{}, error) {
			return params, nil // echo params back as result
		})
	})

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out int
			callCtx, ccancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer ccancel()
			assert.NoError(t, c.Call(callCtx, "identity", i, &out))
			assert.Equal(t, i, out)
		}()
	}
	wg.Wait()
}

func TestClient_SubscribeReceivesEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := startServer(t, ctx, func(s *ipc.Server) {
		s.Register("watch", func(hctx context.Context, _ json.RawMessage) (interface{}, error) {
			conn := ipc.ConnFromContext(hctx)
			subID := "s-42"
			go func() {
				for i := 0; i < 3; i++ {
					_ = conn.EmitEvent(subID, "tick", map[string]int{"n": i})
				}
			}()
			return map[string]string{"subscription_id": subID}, nil
		})
	})

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()

	sub, err := c.Subscribe(context.Background(), "watch", nil)
	assert.NoError(t, err)
	defer sub.Close()
	assert.Equal(t, "s-42", sub.ID)

	for i := 0; i < 3; i++ {
		select {
		case ev := <-sub.Events:
			assert.NotNil(t, ev)
			assert.Equal(t, "s-42", ev.Sub)
			assert.Equal(t, "tick", ev.Kind)
			var d struct {
				N int `json:"n"`
			}
			assert.NoError(t, json.Unmarshal(ev.Data, &d))
			assert.Equal(t, i, d.N)
		case <-time.After(2 * time.Second):
			t.Fatalf("event %d not received", i)
		}
	}
}

func TestClient_CallRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := startServer(t, ctx, func(s *ipc.Server) {
		s.Register("slow", func(hctx context.Context, _ json.RawMessage) (interface{}, error) {
			<-hctx.Done()
			return nil, hctx.Err()
		})
	})

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()

	callCtx, ccancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer ccancel()
	err = c.Call(callCtx, "slow", nil, nil)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)
}
