// Package methods implements the IPC method handlers exposed by the opencom
// daemon. Each method has a small constructor that captures the daemon
// state it needs and returns an ipc.Handler.
package methods

import (
	"context"
	"encoding/json"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"opencom/internal/identity"
	"opencom/internal/ipc"
)

// DaemonStatusResult is the JSON shape of the daemon.status response.
type DaemonStatusResult struct {
	Version      string    `json:"version"`
	PeerID       peer.ID   `json:"peer_id"`
	StartedAt    time.Time `json:"started_at"`
	ListenAddrs  []string  `json:"listen_addrs"`
	CurrentCalls []string  `json:"current_calls"`
	Reachability string    `json:"reachability"`
}

// DaemonStatus returns an ipc.Handler that reports the daemon's current
// status. listenAddrs and reachability are functions so the daemon can
// expose live state (the libp2p host's dynamic addrs and the AutoNAT
// reachability that may flip mid-session as the network changes).
func DaemonStatus(version string, kp identity.Keypair, startedAt time.Time, listenAddrs func() []string, reachability func() string) ipc.Handler {
	return func(_ context.Context, _ json.RawMessage) (interface{}, error) {
		addrs := []string{}
		if listenAddrs != nil {
			addrs = listenAddrs()
			if addrs == nil {
				addrs = []string{}
			}
		}
		reach := "unknown"
		if reachability != nil {
			reach = reachability()
		}
		return DaemonStatusResult{
			Version:      version,
			PeerID:       kp.PeerID,
			StartedAt:    startedAt,
			ListenAddrs:  addrs,
			CurrentCalls: []string{},
			Reachability: reach,
		}, nil
	}
}

// DaemonShutdown returns a handler that responds with {"status":"shutting down"}
// and then schedules cancel() ~50ms later, giving the response time to flush
// over the connection before the IPC server's Serve loop returns.
func DaemonShutdown(cancel context.CancelFunc) ipc.Handler {
	return func(_ context.Context, _ json.RawMessage) (interface{}, error) {
		time.AfterFunc(50*time.Millisecond, cancel)
		return map[string]string{"status": "shutting down"}, nil
	}
}
