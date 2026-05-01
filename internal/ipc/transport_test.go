package ipc

import (
	"context"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func testIPCPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `\\.\pipe\opencom-test-` + t.Name() + "-" + time.Now().Format("150405.000000")
	}
	return filepath.Join(t.TempDir(), "test.sock")
}

func TestTransport_RoundTrip(t *testing.T) {
	t.Parallel()

	path := testIPCPath(t)
	ln, err := Listen(path)
	assert.NoError(t, err)
	defer ln.Close()

	wantMsg := []byte("hello via IPC")
	gotMsg := make([]byte, len(wantMsg))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, err := ln.Accept()
		assert.NoError(t, err)
		defer c.Close()
		_, err = c.Write(wantMsg)
		assert.NoError(t, err)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := dialTransport(ctx, path)
	assert.NoError(t, err)
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err = conn.Read(gotMsg)
	assert.NoError(t, err)

	assert.Equal(t, wantMsg, gotMsg)
	wg.Wait()
}

func TestTransport_DialFailsWhenNoListener(t *testing.T) {
	t.Parallel()

	path := testIPCPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := dialTransport(ctx, path)
	assert.Error(t, err)
}
