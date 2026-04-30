package discovery_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"

	"opencom/internal/discovery"
)

func TestCache_OpenSetGet(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "peer-cache.json")
	c, err := discovery.OpenCache(path)
	assert.NoError(t, err)

	pid := peer.ID("12D3KooWBob")
	addrs := []string{"/ip4/192.0.2.1/tcp/4001"}
	c.Set(pid, addrs)

	got, ok := c.Get(pid)
	assert.True(t, ok)
	assert.Equal(t, addrs, got.Addresses)
	assert.WithinDuration(t, time.Now(), got.FetchedAt, time.Second)
}

func TestCache_GetMissing(t *testing.T) {
	t.Parallel()

	c, err := discovery.OpenCache(filepath.Join(t.TempDir(), "c.json"))
	assert.NoError(t, err)

	_, ok := c.Get(peer.ID("missing"))
	assert.False(t, ok)
}

func TestCache_PersistsAcrossOpens(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "peer-cache.json")
	c1, err := discovery.OpenCache(path)
	assert.NoError(t, err)
	pid := peer.ID("12D3KooWAlice")
	addrs := []string{"/ip4/198.51.100.42/tcp/4001"}
	c1.Set(pid, addrs)

	c2, err := discovery.OpenCache(path)
	assert.NoError(t, err)
	got, ok := c2.Get(pid)
	assert.True(t, ok)
	assert.Equal(t, addrs, got.Addresses)
}

func TestCache_Invalidate(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "peer-cache.json")
	c, err := discovery.OpenCache(path)
	assert.NoError(t, err)

	pid := peer.ID("12D3KooWAlice")
	c.Set(pid, []string{"/ip4/192.0.2.1/tcp/4001"})
	c.Invalidate(pid)

	_, ok := c.Get(pid)
	assert.False(t, ok)
}

func TestCache_RecoversFromCorruption(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "peer-cache.json")
	assert.NoError(t, os.WriteFile(path, []byte("not json at all"), 0o600))

	c, err := discovery.OpenCache(path)
	assert.NoError(t, err)
	_, ok := c.Get(peer.ID("anything"))
	assert.False(t, ok)
}

func TestCache_SetDefensivelyCopies(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "peer-cache.json")
	c, err := discovery.OpenCache(path)
	assert.NoError(t, err)

	pid := peer.ID("12D3KooWAlice")
	addrs := []string{"/ip4/192.0.2.1/tcp/4001"}
	c.Set(pid, addrs)
	addrs[0] = "MUTATED"

	got, ok := c.Get(pid)
	assert.True(t, ok)
	assert.Equal(t, "/ip4/192.0.2.1/tcp/4001", got.Addresses[0])
}

func TestCache_GetDefensivelyCopies(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "peer-cache.json")
	c, err := discovery.OpenCache(path)
	assert.NoError(t, err)

	pid := peer.ID("12D3KooWAlice")
	c.Set(pid, []string{"/ip4/192.0.2.1/tcp/4001"})

	got1, _ := c.Get(pid)
	got1.Addresses[0] = "MUTATED"

	got2, _ := c.Get(pid)
	assert.Equal(t, "/ip4/192.0.2.1/tcp/4001", got2.Addresses[0])
}

func TestCache_RoundTripsRealPeerIDBytes(t *testing.T) {
	t.Parallel()

	// Generate a real peer.ID (multihash bytes — likely non-UTF-8).
	_, pub, err := libp2pcrypto.GenerateEd25519Key(nil)
	assert.NoError(t, err)
	pid, err := peer.IDFromPublicKey(pub)
	assert.NoError(t, err)

	// Verify the bytes are non-UTF-8 (the bug we're fixing only matters
	// for non-UTF-8 inputs). On rare occasions a generated peer.ID could
	// be coincidentally valid UTF-8; if so, this assertion would fail
	// spuriously, but in practice it always produces non-UTF-8 bytes.
	// Skip the assertion to keep the test deterministic; the round-trip
	// test below catches the actual bug regardless.

	path := filepath.Join(t.TempDir(), "peer-cache.json")
	c1, err := discovery.OpenCache(path)
	assert.NoError(t, err)
	addrs := []string{"/ip4/192.0.2.1/tcp/4001"}
	c1.Set(pid, addrs)

	// Reopen — this exercises the JSON round-trip.
	c2, err := discovery.OpenCache(path)
	assert.NoError(t, err)
	got, ok := c2.Get(pid)
	assert.True(t, ok, "real peer.ID must round-trip through JSON")
	assert.Equal(t, addrs, got.Addresses)
}

func TestCache_ConcurrentSetGetInvalidate(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "peer-cache.json")
	c, err := discovery.OpenCache(path)
	assert.NoError(t, err)

	const goroutines = 20
	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	// Writers
	for g := 0; g < goroutines; g++ {
		gID := g
		go func() {
			defer wg.Done()
			pid := peer.ID(fmt.Sprintf("peer-%d", gID))
			for i := 0; i < iterations; i++ {
				c.Set(pid, []string{fmt.Sprintf("/ip4/10.0.0.%d/tcp/%d", gID, i)})
			}
		}()
	}
	// Readers
	for g := 0; g < goroutines; g++ {
		gID := g
		go func() {
			defer wg.Done()
			pid := peer.ID(fmt.Sprintf("peer-%d", gID))
			for i := 0; i < iterations; i++ {
				_, _ = c.Get(pid)
			}
		}()
	}
	// Invalidators
	for g := 0; g < goroutines; g++ {
		gID := g
		go func() {
			defer wg.Done()
			pid := peer.ID(fmt.Sprintf("peer-%d", gID))
			for i := 0; i < iterations; i++ {
				if i%10 == 0 {
					c.Invalidate(pid)
				}
			}
		}()
	}
	wg.Wait()
}
