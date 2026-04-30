// Package call implements the opencom call control plane: session state
// machines, the libp2p stream protocol, and the active-calls registry.
package call

// MessageType discriminates control messages on the wire.
type MessageType string

const (
	MsgInvite  MessageType = "invite"
	MsgAccept  MessageType = "accept"
	MsgDecline MessageType = "decline"
	MsgHangup  MessageType = "hangup"
)

// Message is the JSON envelope for every control-plane message that flows
// over the /opencom/control/1.0.0 libp2p stream.
type Message struct {
	Type   MessageType `json:"type"`
	CallID string      `json:"call_id"`
	Reason string      `json:"reason,omitempty"` // hangup / decline reason
	Caller string      `json:"caller,omitempty"` // peer ID of the inviter (in INVITE)
}
