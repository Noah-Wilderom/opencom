package call_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/call"
)

func TestMessage_InviteRoundTrip(t *testing.T) {
	t.Parallel()

	in := call.Message{
		Type:   call.MsgInvite,
		CallID: "c-1",
		Caller: "12D3KooWAlice",
	}
	raw, err := json.Marshal(in)
	assert.NoError(t, err)
	assert.Contains(t, string(raw), `"type":"invite"`)

	var out call.Message
	assert.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, in, out)
}

func TestMessage_HangupRoundTripWithReason(t *testing.T) {
	t.Parallel()

	in := call.Message{
		Type:   call.MsgHangup,
		CallID: "c-1",
		Reason: "user requested",
	}
	raw, err := json.Marshal(in)
	assert.NoError(t, err)

	var out call.Message
	assert.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, "user requested", out.Reason)
}

func TestMessage_AcceptOmitsEmptyFields(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(call.Message{Type: call.MsgAccept, CallID: "c-1"})
	assert.NoError(t, err)
	s := string(raw)
	assert.NotContains(t, s, `"reason"`)
	assert.NotContains(t, s, `"caller"`)
}

func TestMessageType_StringValues(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "invite", string(call.MsgInvite))
	assert.Equal(t, "accept", string(call.MsgAccept))
	assert.Equal(t, "decline", string(call.MsgDecline))
	assert.Equal(t, "hangup", string(call.MsgHangup))
}
