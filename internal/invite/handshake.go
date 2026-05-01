package invite

import (
	"bufio"
	"encoding/json"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// ProtocolID is the libp2p stream protocol for the invite handshake.
const ProtocolID protocol.ID = "/opencom/invite/1.0.0"

// handshakeReadDeadline bounds reads on each side of the handshake.
const handshakeReadDeadline = 10 * time.Second

// Type values for Hello/Response.Type.
const (
	TypeRedeem = "invite_redeem"
	TypeAccept = "invite_accept"
	TypeReject = "invite_reject"
)

// Hello is the invitee's redeem message.
type Hello struct {
	Type        string   `json:"type"`
	Code        Code     `json:"code"`
	PeerID      string   `json:"peer_id"`
	PublicKey   string   `json:"public_key"`
	Addresses   []string `json:"addresses"`
	DisplayName string   `json:"display_name"`
}

// Response is the inviter's reply.
type Response struct {
	Type        string `json:"type"`
	PeerID      string `json:"peer_id,omitempty"`
	PublicKey   string `json:"public_key,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// SendHello writes h as a single newline-delimited JSON message.
func SendHello(s network.Stream, h Hello) error {
	_ = s.SetWriteDeadline(time.Now().Add(handshakeReadDeadline))
	if h.Type == "" {
		h.Type = TypeRedeem
	}
	enc := json.NewEncoder(s)
	if err := enc.Encode(&h); err != nil {
		return fmt.Errorf("writing hello: %w", err)
	}
	_ = s.SetWriteDeadline(time.Time{})
	return nil
}

// ReadHello reads a single Hello message, bounded by handshakeReadDeadline.
func ReadHello(s network.Stream) (Hello, error) {
	var h Hello
	_ = s.SetReadDeadline(time.Now().Add(handshakeReadDeadline))
	scanner := bufio.NewScanner(s)
	scanner.Buffer(make([]byte, 0, 4*1024), 1<<20)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return h, fmt.Errorf("reading hello: %w", err)
		}
		return h, fmt.Errorf("reading hello: stream closed before hello")
	}
	if err := json.Unmarshal(scanner.Bytes(), &h); err != nil {
		return h, fmt.Errorf("unmarshal hello: %w", err)
	}
	_ = s.SetReadDeadline(time.Time{})
	return h, nil
}

// SendResponse writes r as a single newline-delimited JSON message.
func SendResponse(s network.Stream, r Response) error {
	_ = s.SetWriteDeadline(time.Now().Add(handshakeReadDeadline))
	enc := json.NewEncoder(s)
	if err := enc.Encode(&r); err != nil {
		return fmt.Errorf("writing response: %w", err)
	}
	_ = s.SetWriteDeadline(time.Time{})
	return nil
}

// ReadResponse reads a single Response message, bounded by handshakeReadDeadline.
func ReadResponse(s network.Stream) (Response, error) {
	var r Response
	_ = s.SetReadDeadline(time.Now().Add(handshakeReadDeadline))
	scanner := bufio.NewScanner(s)
	scanner.Buffer(make([]byte, 0, 4*1024), 1<<20)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return r, fmt.Errorf("reading response: %w", err)
		}
		return r, fmt.Errorf("reading response: stream closed before response")
	}
	if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
		return r, fmt.Errorf("unmarshal response: %w", err)
	}
	_ = s.SetReadDeadline(time.Time{})
	return r, nil
}
