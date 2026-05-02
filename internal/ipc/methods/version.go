package methods

import (
	"context"
	"encoding/json"
	"time"

	"opencom/internal/ipc"
	"opencom/internal/version"
)

// VersionCheckResult is the version.check response payload.
type VersionCheckResult struct {
	Current          string    `json:"current"`
	Latest           string    `json:"latest,omitempty"`
	UpgradeAvailable bool      `json:"upgrade_available"`
	FetchedAt        time.Time `json:"fetched_at,omitempty"`
}

// VersionChecker is the daemon-side dependency the handler needs.
// *version.Checker satisfies it.
type VersionChecker interface {
	ReadCache() (version.Cache, error)
}

// VersionCheck returns a handler that reports whether the running
// daemon's version trails the latest GitHub release. Reads the
// daemon-managed cache; never hits the network from the handler so
// CLI invocations stay fast and the GitHub API rate limit is
// respected (one fetch per CheckInterval, not per CLI command).
func VersionCheck(currentVersion string, checker VersionChecker) ipc.Handler {
	return func(_ context.Context, _ json.RawMessage) (interface{}, error) {
		var cache version.Cache
		if checker != nil {
			cache, _ = checker.ReadCache()
		}
		r := version.Compare(currentVersion, cache)
		return VersionCheckResult{
			Current:          r.Current,
			Latest:           r.Latest,
			UpgradeAvailable: r.UpgradeAvailable,
			FetchedAt:        r.FetchedAt,
		}, nil
	}
}
