package ipc_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/ipc"
)

func TestMessage_RequestRoundTrip(t *testing.T) {
	t.Parallel()

	id := int64(7)
	params, err := json.Marshal(map[string]string{"target": "John"})
	assert.NoError(t, err)

	in := ipc.Message{
		JSONRPC: ipc.JSONRPCVersion,
		ID:      &id,
		Method:  "calls.start",
		Params:  params,
	}
	raw, err := json.Marshal(in)
	assert.NoError(t, err)
	assert.Contains(t, string(raw), `"id":7`)
	assert.Contains(t, string(raw), `"method":"calls.start"`)
	assert.NotContains(t, string(raw), `"result"`)
	assert.NotContains(t, string(raw), `"error"`)

	var out ipc.Message
	assert.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, "2.0", out.JSONRPC)
	assert.Equal(t, "calls.start", out.Method)
	assert.True(t, out.IsRequest())
	assert.False(t, out.IsNotification())
	assert.False(t, out.IsResponse())
}

func TestMessage_NotificationHasNoID(t *testing.T) {
	t.Parallel()

	in := ipc.Message{
		JSONRPC: ipc.JSONRPCVersion,
		Method:  "hello",
		Params:  json.RawMessage(`{"version":"1"}`),
	}
	raw, err := json.Marshal(in)
	assert.NoError(t, err)
	assert.NotContains(t, string(raw), `"id"`)

	var out ipc.Message
	assert.NoError(t, json.Unmarshal(raw, &out))
	assert.True(t, out.IsNotification())
	assert.False(t, out.IsRequest())
}

func TestMessage_ResponseRoundTrip(t *testing.T) {
	t.Parallel()

	id := int64(7)
	in := ipc.Message{
		JSONRPC: ipc.JSONRPCVersion,
		ID:      &id,
		Result:  json.RawMessage(`{"call_id":"c-9F2K"}`),
	}
	raw, err := json.Marshal(in)
	assert.NoError(t, err)

	var out ipc.Message
	assert.NoError(t, json.Unmarshal(raw, &out))
	assert.True(t, out.IsResponse())
	assert.Equal(t, int64(7), *out.ID)
	assert.JSONEq(t, `{"call_id":"c-9F2K"}`, string(out.Result))
}

func TestMessage_ErrorResponseRoundTrip(t *testing.T) {
	t.Parallel()

	id := int64(8)
	in := ipc.Message{
		JSONRPC: ipc.JSONRPCVersion,
		ID:      &id,
		Error: &ipc.Error{
			Code:    ipc.ErrCodeMethodNotFound,
			Message: `method "calls.bogus" not found`,
		},
	}
	raw, err := json.Marshal(in)
	assert.NoError(t, err)

	var out ipc.Message
	assert.NoError(t, json.Unmarshal(raw, &out))
	assert.True(t, out.IsResponse())
	assert.NotNil(t, out.Error)
	assert.Equal(t, ipc.ErrCodeMethodNotFound, out.Error.Code)
	assert.Equal(t, `method "calls.bogus" not found`, out.Error.Message)
}

func TestError_ErrorReturnsMessage(t *testing.T) {
	t.Parallel()

	e := &ipc.Error{Code: ipc.ErrCodeInternalError, Message: "boom"}
	assert.Equal(t, "boom", e.Error())
}

func TestHelloParams_RoundTrip(t *testing.T) {
	t.Parallel()

	in := ipc.HelloParams{Version: ipc.ProtocolVersion, DaemonVersion: "0.1.0"}
	raw, err := json.Marshal(in)
	assert.NoError(t, err)

	var out ipc.HelloParams
	assert.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, ipc.ProtocolVersion, out.Version)
	assert.Equal(t, "0.1.0", out.DaemonVersion)
}
