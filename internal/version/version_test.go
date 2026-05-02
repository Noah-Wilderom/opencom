package version_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"opencom/internal/version"
)

func TestCompare_NoUpgradeWhenDevBuild(t *testing.T) {
	t.Parallel()
	r := version.Compare("dev", version.Cache{Latest: "1.2.3"})
	assert.False(t, r.UpgradeAvailable)
	assert.Equal(t, "dev", r.Current)
	assert.Equal(t, "1.2.3", r.Latest)
}

func TestCompare_NoUpgradeWhenCacheEmpty(t *testing.T) {
	t.Parallel()
	r := version.Compare("0.1.10", version.Cache{})
	assert.False(t, r.UpgradeAvailable)
	assert.Equal(t, "0.1.10", r.Current)
	assert.Equal(t, "", r.Latest)
}

func TestCompare_DetectsNewerLatest(t *testing.T) {
	t.Parallel()
	r := version.Compare("0.1.10", version.Cache{Latest: "0.1.13"})
	assert.True(t, r.UpgradeAvailable)
}

func TestCompare_NoUpgradeWhenSame(t *testing.T) {
	t.Parallel()
	r := version.Compare("0.1.13", version.Cache{Latest: "0.1.13"})
	assert.False(t, r.UpgradeAvailable)
}

func TestCompare_NumericOrderNotLexical(t *testing.T) {
	t.Parallel()
	// Lexically "0.1.2" > "0.1.10", numerically the opposite.
	r := version.Compare("0.1.2", version.Cache{Latest: "0.1.10"})
	assert.True(t, r.UpgradeAvailable, "numeric ordering: 0.1.10 must beat 0.1.2")
}

func TestCompare_StripsLeadingV(t *testing.T) {
	t.Parallel()
	r := version.Compare("v0.1.10", version.Cache{Latest: "0.1.13"})
	assert.True(t, r.UpgradeAvailable)
	assert.Equal(t, "0.1.10", r.Current, "leading v stripped from current")
}

func TestRefresh_WritesCacheAndCompareTrips(t *testing.T) {
	t.Parallel()
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v9.9.9"})
	}))
	defer stub.Close()

	dir := t.TempDir()
	c := &version.Checker{
		CachePath: filepath.Join(dir, version.CacheFileName),
		Repo:      "owner/repo", // not used because we override URL
		Client:    stub.Client(),
	}
	// Hack: point Refresh at the stub by overriding the URL via Client's
	// Transport. Simpler: bind Repo to a path the stub serves.
	c.Repo = "anything" // unused for our handler
	// Replace the client with one whose RoundTripper rewrites URLs to
	// the stub server. Easier: just override the request URL host via
	// http.RoundTripperFunc.
	c.Client = &http.Client{
		Transport: rewriteHost{target: stub.URL},
		Timeout:   2 * time.Second,
	}

	got, err := c.Refresh(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "9.9.9", got.Latest)
	assert.WithinDuration(t, time.Now(), got.FetchedAt, time.Minute)

	// Round-trip via the cache file.
	read, err := c.ReadCache()
	assert.NoError(t, err)
	assert.Equal(t, got, read)

	r := version.Compare("0.1.0", read)
	assert.True(t, r.UpgradeAvailable)
}

func TestReadCache_MissingFileReturnsEmpty(t *testing.T) {
	t.Parallel()
	c := &version.Checker{CachePath: filepath.Join(t.TempDir(), "nope.json")}
	got, err := c.ReadCache()
	assert.NoError(t, err)
	assert.Equal(t, version.Cache{}, got)
}

func TestReadCache_CorruptFileReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	assert.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))
	c := &version.Checker{CachePath: path}
	got, err := c.ReadCache()
	assert.NoError(t, err)
	assert.Equal(t, version.Cache{}, got, "corrupt cache should read as empty rather than error")
}

// rewriteHost rewrites every outbound request's host to point at the
// stub server. Lets the test exercise the real Refresh code path
// without a third-party mocking library.
type rewriteHost struct{ target string }

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := req.URL.Parse(r.target + req.URL.Path)
	if err != nil {
		return nil, err
	}
	req.URL = u
	req.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}
