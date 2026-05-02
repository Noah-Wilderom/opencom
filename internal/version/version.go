// Package version implements opencom's "is there a newer release"
// check. The daemon polls GitHub's releases API on a slow cadence and
// caches the result on disk; CLI commands read the cache to render a
// one-line warning when an upgrade is available, without ever hitting
// the network themselves.
package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultRepo is the GitHub owner/repo opencom checks for releases.
// Overridable at build time or via NewChecker for forks/tests.
const DefaultRepo = "Noah-Wilderom/opencom"

// CheckInterval is how often the daemon refreshes the cache. Six hours
// is conservative against GitHub's anonymous rate limits (60/hour) and
// fast enough that newly-published releases are surfaced within a day.
const CheckInterval = 6 * time.Hour

// HTTPTimeout caps a single fetch attempt. GitHub's API is normally
// very fast; if it stalls we don't want the daemon's check goroutine
// stuck holding a half-open connection.
const HTTPTimeout = 10 * time.Second

// CacheFileName is the filename inside the daemon's state directory
// where the latest-release cache is persisted. Persistence lets the
// CLI render a warning even when the daemon is starting fresh — the
// last known result rides over restarts.
const CacheFileName = "latest-release.json"

// Cache is the on-disk shape of the version cache.
type Cache struct {
	// Latest is the most recent tag observed on the releases endpoint
	// (e.g. "v0.1.13"), with the leading "v" stripped.
	Latest string `json:"latest"`
	// FetchedAt is when the cache entry was written. Used by the
	// daemon to decide whether to re-check.
	FetchedAt time.Time `json:"fetched_at"`
	// Repo records which owner/repo the entry came from so
	// switching forks invalidates the cache cleanly.
	Repo string `json:"repo"`
}

// Result is what callers ultimately consume.
type Result struct {
	// Current is the running binary's compile-time version (with any
	// "v" prefix stripped). "dev" for unreleased builds.
	Current string
	// Latest is the latest tag from the cache, or "" when no cache
	// entry exists yet.
	Latest string
	// UpgradeAvailable reports whether Latest is newer than Current.
	// Always false when Current == "dev" or Latest == "".
	UpgradeAvailable bool
	// FetchedAt is when the cache entry was written.
	FetchedAt time.Time
}

// Checker bundles the moving parts the daemon needs: where to read/
// write the cache, which repo to query, and an HTTP client (overridable
// for tests).
type Checker struct {
	CachePath string
	Repo      string
	Client    *http.Client
}

// New returns a Checker writing to <stateDir>/latest-release.json
// and querying DefaultRepo via the default HTTP client.
func New(stateDir string) *Checker {
	return &Checker{
		CachePath: filepath.Join(stateDir, CacheFileName),
		Repo:      DefaultRepo,
		Client:    &http.Client{Timeout: HTTPTimeout},
	}
}

// ReadCache returns the on-disk cache, or (Cache{}, nil) when no cache
// file exists yet (a fresh daemon hasn't refreshed once). Other read
// errors are returned so the caller can decide whether to retry.
func (c *Checker) ReadCache() (Cache, error) {
	b, err := os.ReadFile(c.CachePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Cache{}, nil
		}
		return Cache{}, err
	}
	var entry Cache
	if err := json.Unmarshal(b, &entry); err != nil {
		// Treat a corrupt cache as "no cache" — the next refresh
		// rewrites it cleanly. Better than blocking startup.
		return Cache{}, nil
	}
	return entry, nil
}

// Refresh queries GitHub for the latest release tag and writes it to
// the cache file. Safe to call whenever; the daemon spaces calls via
// CheckInterval.
func (c *Checker) Refresh(ctx context.Context) (Cache, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", c.Repo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return Cache{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.Client.Do(req)
	if err != nil {
		return Cache{}, fmt.Errorf("fetching releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Cache{}, fmt.Errorf("releases API returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Cache{}, fmt.Errorf("decoding release payload: %w", err)
	}
	entry := Cache{
		Latest:    strings.TrimPrefix(payload.TagName, "v"),
		FetchedAt: time.Now().UTC(),
		Repo:      c.Repo,
	}
	if err := writeCacheAtomic(c.CachePath, entry); err != nil {
		return Cache{}, fmt.Errorf("writing cache: %w", err)
	}
	return entry, nil
}

// Compare consumes a Cache plus the running binary's version and
// produces a Result. Pure function — no I/O — so the IPC handler can
// reuse the same comparison logic the CLI uses locally.
func Compare(currentVersion string, cache Cache) Result {
	current := strings.TrimPrefix(currentVersion, "v")
	r := Result{
		Current:   current,
		Latest:    cache.Latest,
		FetchedAt: cache.FetchedAt,
	}
	if current == "" || current == "dev" || cache.Latest == "" {
		return r
	}
	r.UpgradeAvailable = isNewer(cache.Latest, current)
	return r
}

// isNewer reports whether a is strictly greater than b under semver
// ordering ("0.1.10" > "0.1.2"). Falls back to lexical comparison
// for non-numeric segments so unconventional tags don't crash.
func isNewer(a, b string) bool {
	as := splitVersion(a)
	bs := splitVersion(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv int
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av != bv {
			return av > bv
		}
	}
	return false
}

// splitVersion returns the integer components of v, ignoring any
// pre-release suffix ("0.1.10-rc1" → [0,1,10]). Non-numeric components
// become 0 so weird tags compare as equal-low instead of crashing.
func splitVersion(v string) []int {
	v = strings.SplitN(v, "-", 2)[0]
	v = strings.SplitN(v, "+", 2)[0]
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		var n int
		_, _ = fmt.Sscanf(p, "%d", &n)
		out[i] = n
	}
	return out
}

// writeCacheAtomic writes entry to path via a temp file + rename, so
// readers never see a half-written file.
func writeCacheAtomic(path string, entry Cache) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
