package app

import (
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"go.uber.org/zap"

	"opencom/internal/config"
	"opencom/internal/identity"
)

// Options configures a daemon Run invocation. Defined here (without build
// tags) so non-unix builds can still reference the type even though Run
// itself is unix-only in M2.
type Options struct {
	Paths     config.Paths
	Config    config.Config
	Identity  identity.Keypair
	Log       *zap.Logger
	Version   string
	StartedAt time.Time

	// DisableMDNS suppresses mDNS LAN discovery. Used by the
	// cross-network E2E test to force pure-DHT discovery.
	DisableMDNS bool

	// HostBootstraps overrides the libp2p bootstrap peer list. Defaults
	// to the public IPFS bootstraps when empty/nil. Used by tests that
	// run a private DHT against a single test bootstrap node.
	HostBootstraps []peer.AddrInfo
}
