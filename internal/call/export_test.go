package call

import (
	"context"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// HostForTest exposes the engine's stream-dialing surface to integration
// tests in package call_test. Test-only because the file ends in _test.go.
func (e *Engine) HostForTest() interface {
	NewStream(ctx context.Context, p peer.ID, pids ...protocol.ID) (network.Stream, error)
} {
	return e.host.HostInternal()
}
