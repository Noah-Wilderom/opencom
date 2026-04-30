// Package ipc implements the JSON-RPC 2.0 protocol used between the opencom
// daemon and CLI/TUI clients over a local Unix domain socket.
package ipc

import "encoding/json"

const (
	// JSONRPCVersion is the protocol version field value used in every message.
	JSONRPCVersion = "2.0"

	// ProtocolVersion is the opencom-specific handshake version sent in the
	// hello frame. Bumped on incompatible wire changes.
	ProtocolVersion = "1"
)

// Message is the on-wire JSON-RPC 2.0 envelope. A request carries Method,
// Params, and ID. A notification carries Method and Params (no ID). A
// response carries either Result or Error and the matching ID.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// IsRequest reports whether m is a request that expects a response.
func (m Message) IsRequest() bool { return m.Method != "" && m.ID != nil }

// IsNotification reports whether m is a notification (no response expected).
func (m Message) IsNotification() bool { return m.Method != "" && m.ID == nil }

// IsResponse reports whether m is a response (carries Result or Error).
func (m Message) IsResponse() bool { return m.Method == "" && m.ID != nil }

// Error is the JSON-RPC error payload.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements the error interface so handlers can return *Error directly.
func (e *Error) Error() string { return e.Message }

// HelloParams is the payload of the "hello" notification the server sends
// immediately after a connection is accepted.
type HelloParams struct {
	Version       string `json:"version"`        // protocol version
	DaemonVersion string `json:"daemon_version"` // daemon build version
}
