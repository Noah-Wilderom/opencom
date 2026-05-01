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

	// HostBootstraps overrides the opencom-DHT bootstrap peer list.
	// nil = use the cfg.Discovery.Bootstraps from config (default
	// empty); non-nil overrides for tests. Empty (non-nil) means
	// "no DHT bootstraps" — used by tests on isolated networks.
	HostBootstraps []peer.AddrInfo

	// HostRelays overrides the libp2p relay-v2 peer list used by
	// AutoRelay. nil = use cfg.Relay.Peers (default: public libp2p
	// bootstraps); empty (non-nil) disables AutoRelay entirely.
	HostRelays []peer.AddrInfo
}
