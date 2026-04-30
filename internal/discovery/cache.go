package discovery

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"opencom/internal/iox"
)

// CacheEntry is one row in the peer cache.
type CacheEntry struct {
	Addresses []string  `json:"addresses"`
	FetchedAt time.Time `json:"fetched_at"`
}

// Cache is a disk-backed last-known-good map from peer.ID to addresses.
//
// Reads come from an in-memory mirror; writes flush atomically to disk.
// On corruption (bad JSON), Open returns an empty cache rather than
// failing — losing the cache is preferable to losing the daemon.
type Cache struct {
	path    string
	mu      sync.RWMutex
	entries map[peer.ID]CacheEntry

	flushMu sync.Mutex // serializes flush operations to preserve write ordering on disk
}

// cacheFile is the on-disk shape. Keys are base64-encoded peer.ID
// bytes — JSON requires valid UTF-8 strings, but real peer.IDs are
// raw multihash binary that contain non-UTF-8 sequences. Base64 keeps
// the wire format ASCII-clean and round-trip safe.
type cacheFile struct {
	Version int                   `json:"version"`
	Entries map[string]CacheEntry `json:"entries"`
}

const cacheVersion = 1

// OpenCache loads the cache file at path, creating an empty cache if it
// doesn't exist or is corrupted.
func OpenCache(path string) (*Cache, error) {
	c := &Cache{path: path, entries: make(map[peer.ID]CacheEntry)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading cache: %w", err)
	}
	var f cacheFile
	if err := json.Unmarshal(data, &f); err != nil || f.Version != cacheVersion {
		// Corrupted or unknown version → drop and start fresh.
		return c, nil
	}
	for k, v := range f.Entries {
		raw, derr := base64.StdEncoding.DecodeString(k)
		if derr != nil {
			// Skip malformed entries; don't fail the whole load.
			continue
		}
		c.entries[peer.ID(raw)] = v
	}
	return c, nil
}

// Get returns the cache entry for p, if any. The returned entry's
// Addresses slice is a defensive copy; callers may mutate it freely.
func (c *Cache) Get(p peer.ID) (CacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[p]
	if !ok {
		return CacheEntry{}, false
	}
	out := CacheEntry{
		Addresses: append([]string(nil), e.Addresses...),
		FetchedAt: e.FetchedAt,
	}
	return out, true
}

// Set updates p's entry with addrs and the current time, then flushes.
func (c *Cache) Set(p peer.ID, addrs []string) {
	c.mu.Lock()
	c.entries[p] = CacheEntry{Addresses: append([]string(nil), addrs...), FetchedAt: time.Now().UTC()}
	c.mu.Unlock()
	c.flush()
}

// Invalidate drops p's entry, then flushes.
func (c *Cache) Invalidate(p peer.ID) {
	c.mu.Lock()
	delete(c.entries, p)
	c.mu.Unlock()
	c.flush()
}

func (c *Cache) flush() {
	c.flushMu.Lock()
	defer c.flushMu.Unlock()

	c.mu.RLock()
	snap := make(map[string]CacheEntry, len(c.entries))
	for k, v := range c.entries {
		snap[base64.StdEncoding.EncodeToString([]byte(k))] = v
	}
	c.mu.RUnlock()

	f := cacheFile{Version: cacheVersion, Entries: snap}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')
	_ = iox.AtomicWriteFile(c.path, data, 0o600, 0o700)
}
