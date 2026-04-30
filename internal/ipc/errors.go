package ipc

// JSON-RPC 2.0 standard error codes.
const (
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

// opencom-specific error codes (in the JSON-RPC implementation-defined range
// -32000 to -32099). Stable: these integers are part of the wire contract.
const (
	ErrCodeNotAuthorized     = -32001
	ErrCodeNoSuchFriend      = -32002
	ErrCodeNoSuchCall        = -32003
	ErrCodeDeviceUnavailable = -32004
	ErrCodeDaemonNotReady    = -32005
)

// NewError constructs an *Error with the given code and message.
func NewError(code int, message string) *Error {
	return &Error{Code: code, Message: message}
}
