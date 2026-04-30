package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

// Conn is the per-connection handle handlers receive via context. Use
// ConnFromContext(ctx) inside a handler to obtain it. EmitEvent writes a
// notification on this connection; Done signals when the client has
// disconnected so long-running emitters can stop work.
type Conn interface {
	EmitEvent(subID, kind string, data interface{}) error
	Done() <-chan struct{}
}

// connKey is an unexported type used as a context.Value key so callers
// outside this package cannot inject their own Conn.
type connKey struct{}

// ConnFromContext returns the Conn for the current handler invocation, or
// nil if the handler is not running within a connection context.
func ConnFromContext(ctx context.Context) Conn {
	c, _ := ctx.Value(connKey{}).(Conn)
	return c
}

// withConn injects c into ctx so handlers can retrieve it via
// ConnFromContext.
func withConn(ctx context.Context, c Conn) context.Context {
	return context.WithValue(ctx, connKey{}, c)
}

// connImpl is the concrete Conn produced by the server for each accepted
// connection. It serializes writes through writeMu (which is also used by
// the dispatch loop's response writes) so notifications and responses do
// not interleave.
type connImpl struct {
	enc     *json.Encoder
	writeMu *sync.Mutex
	done    <-chan struct{}
}

func newConn(enc *json.Encoder, writeMu *sync.Mutex, done <-chan struct{}) *connImpl {
	return &connImpl{enc: enc, writeMu: writeMu, done: done}
}

func (c *connImpl) Done() <-chan struct{} { return c.done }

func (c *connImpl) EmitEvent(subID, kind string, data interface{}) error {
	rawData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	params, err := json.Marshal(struct {
		Sub  string          `json:"sub"`
		Kind string          `json:"kind"`
		Data json.RawMessage `json:"data"`
	}{Sub: subID, Kind: kind, Data: rawData})
	if err != nil {
		return err
	}
	msg := Message{JSONRPC: JSONRPCVersion, Method: "event", Params: params}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return errors.New("connection closed")
	default:
	}
	return c.enc.Encode(&msg)
}
