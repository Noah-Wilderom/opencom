package ipc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/ipc"
)

func TestNewError_PopulatesCodeAndMessage(t *testing.T) {
	t.Parallel()

	e := ipc.NewError(ipc.ErrCodeNoSuchFriend, "friend Alice not found")
	assert.Equal(t, ipc.ErrCodeNoSuchFriend, e.Code)
	assert.Equal(t, "friend Alice not found", e.Message)
	assert.Nil(t, e.Data)
}

func TestErrorCodes_AreStable(t *testing.T) {
	t.Parallel()

	// Lock in the integer values so a future rename doesn't silently
	// change the wire contract.
	assert.Equal(t, -32600, ipc.ErrCodeInvalidRequest)
	assert.Equal(t, -32601, ipc.ErrCodeMethodNotFound)
	assert.Equal(t, -32602, ipc.ErrCodeInvalidParams)
	assert.Equal(t, -32603, ipc.ErrCodeInternalError)
	assert.Equal(t, -32001, ipc.ErrCodeNotAuthorized)
	assert.Equal(t, -32002, ipc.ErrCodeNoSuchFriend)
	assert.Equal(t, -32003, ipc.ErrCodeNoSuchCall)
	assert.Equal(t, -32004, ipc.ErrCodeDeviceUnavailable)
	assert.Equal(t, -32005, ipc.ErrCodeDaemonNotReady)
}
