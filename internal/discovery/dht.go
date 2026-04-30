package discovery

import (
	"context"

	"github.com/libp2p/go-libp2p/core/routing"
)

// DHT is the narrow subset of go-libp2p-kad-dht we depend on. The
// library's *IpfsDHT type satisfies this interface directly; tests use
// an in-memory fake.
//
// The variadic routing.Option args mirror the upstream signature so
// *dht.IpfsDHT satisfies this interface without an adapter. Opencom
// callers always pass zero options.
type DHT interface {
	// PutValue publishes value under key. Caller picks the key shape;
	// implementations may impose record-validator constraints.
	PutValue(ctx context.Context, key string, value []byte, opts ...routing.Option) error

	// GetValue returns the value at key. Returns an error (typically
	// routing.ErrNotFound) if no record exists.
	GetValue(ctx context.Context, key string, opts ...routing.Option) ([]byte, error)
}
