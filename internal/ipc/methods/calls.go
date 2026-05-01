package methods

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"opencom/internal/audio"
	"opencom/internal/call"
	"opencom/internal/friends"
	"opencom/internal/ipc"
)

// AudioMuter is the slice of audio.Manager that calls.action depends on
// for mute/unmute actions. Defining it here avoids a hard import cycle
// risk and keeps the IPC method package focused.
type AudioMuter interface {
	SetMuted(callID string, muted bool) bool
}

// AudioStatter exposes audio session stats per call. *audio.Manager
// implements this; the daemon passes a no-op stub when audio is
// disabled (e.g. in tests with DisableAudio: true).
type AudioStatter interface {
	Stats(callID string) (audio.Stats, bool)
}

// CallsStartParams is the calls.start request payload.
type CallsStartParams struct {
	Target string `json:"target"`
}

// CallsStartResult is the calls.start response: the new call's ID and the
// subscription ID for its state-change events.
type CallsStartResult struct {
	CallID         string `json:"call_id"`
	SubscriptionID string `json:"subscription_id"`
}

// CallsListEntry is one row in the calls.list result.
type CallsListEntry struct {
	CallID    string    `json:"call_id"`
	State     string    `json:"state"`
	Direction string    `json:"direction"`
	Remote    peer.ID   `json:"remote"`
	StartedAt time.Time `json:"started_at"`
	// Audio fields — populated only when an audio session exists for the call.
	Muted     bool   `json:"muted,omitempty"`
	PeerMuted bool   `json:"peer_muted,omitempty"`
	AudioOK   string `json:"audio_ok,omitempty"` // "ok" | "no-mic" | "no-output" | "unavailable" | ""
	RxLevelDB int    `json:"rx_level_db,omitempty"`
	TxLevelDB int    `json:"tx_level_db,omitempty"`
}

// CallsListResult is the calls.list response shape.
type CallsListResult struct {
	Calls []CallsListEntry `json:"calls"`
}

// CallsAttachParams identifies the call to attach to.
type CallsAttachParams struct {
	CallID string `json:"call_id"`
}

// CallsAttachResult is the calls.attach response: subscription ID for the
// attached call's state-change events.
type CallsAttachResult struct {
	SubscriptionID string `json:"subscription_id"`
}

// CallsActionParams is the calls.action request payload. Action is one of
// "accept" or "hangup"; Reason is optional and only meaningful for hangup.
type CallsActionParams struct {
	CallID string `json:"call_id"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

// CallsOK is the success-status response shared by calls.action.
type CallsOK struct {
	Status string `json:"status"`
}

var callSubCounter atomic.Uint64

func newCallSubID() string {
	return fmt.Sprintf("cs-%d-%d", time.Now().UnixNano(), callSubCounter.Add(1))
}

// CallsStart resolves params.Target to a peer ID (friend name first, raw
// peer ID as fallback), places an outbound call, and returns a subscription
// that emits "call.state_changed" events for the new session.
func CallsStart(eng *call.Engine, mgr *call.Manager, store *friends.Store) ipc.Handler {
	return func(ctx context.Context, raw json.RawMessage) (interface{}, error) {
		var p CallsStartParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "invalid params: "+err.Error())
		}
		if p.Target == "" {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "target is required")
		}

		pid, err := resolveTarget(p.Target, store)
		if err != nil {
			return nil, err
		}

		conn := ipc.ConnFromContext(ctx)
		if conn == nil {
			return nil, ipc.NewError(ipc.ErrCodeInternalError, "calls.start requires a connection context")
		}

		s, err := eng.Place(ctx, pid)
		if err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInternalError, fmt.Sprintf("placing call: %v", err))
		}

		subID := newCallSubID()
		pipeStateChanges(conn, s, subID)

		return CallsStartResult{
			CallID:         s.ID(),
			SubscriptionID: subID,
		}, nil
	}
}

// CallsList returns all currently-tracked sessions, sorted by ID. When
// audioStatter is non-nil, each entry is enriched with audio stats if an
// audio session exists for that call.
func CallsList(mgr *call.Manager, audioStatter AudioStatter) ipc.Handler {
	return func(_ context.Context, _ json.RawMessage) (interface{}, error) {
		all := mgr.List()
		out := CallsListResult{Calls: make([]CallsListEntry, 0, len(all))}
		for _, s := range all {
			entry := CallsListEntry{
				CallID:    s.ID(),
				State:     s.State().String(),
				Direction: s.Direction().String(),
				Remote:    s.Remote(),
				StartedAt: s.StartedAt(),
			}
			if audioStatter != nil {
				if stats, ok := audioStatter.Stats(s.ID()); ok {
					entry.Muted = stats.Muted
					entry.PeerMuted = stats.PeerMuted
					entry.AudioOK = "ok"
					entry.RxLevelDB = stats.RxPeakDBFS
					entry.TxLevelDB = stats.TxPeakDBFS
				}
			}
			out.Calls = append(out.Calls, entry)
		}
		return out, nil
	}
}

// CallsAttach subscribes the caller's connection to state-change events for
// an existing session.
func CallsAttach(mgr *call.Manager) ipc.Handler {
	return func(ctx context.Context, raw json.RawMessage) (interface{}, error) {
		var p CallsAttachParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "invalid params: "+err.Error())
		}
		s, ok := mgr.Get(p.CallID)
		if !ok {
			return nil, ipc.NewError(ipc.ErrCodeNoSuchCall, fmt.Sprintf("call %q not found", p.CallID))
		}
		conn := ipc.ConnFromContext(ctx)
		if conn == nil {
			return nil, ipc.NewError(ipc.ErrCodeInternalError, "calls.attach requires a connection context")
		}
		subID := newCallSubID()
		pipeStateChanges(conn, s, subID)
		return CallsAttachResult{SubscriptionID: subID}, nil
	}
}

// CallsAction performs accept/hangup/mute/unmute on an existing call.
// audioMuter is optional; when nil, mute/unmute return an error indicating
// that audio is not enabled on this daemon.
func CallsAction(eng *call.Engine, mgr *call.Manager, audioMuter AudioMuter) ipc.Handler {
	return func(_ context.Context, raw json.RawMessage) (interface{}, error) {
		var p CallsActionParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "invalid params: "+err.Error())
		}
		switch p.Action {
		case "mute":
			if audioMuter == nil {
				return nil, ipc.NewError(ipc.ErrCodeNoSuchCall, "audio not enabled on this daemon")
			}
			if !audioMuter.SetMuted(p.CallID, true) {
				return nil, ipc.NewError(ipc.ErrCodeNoSuchCall, "no audio session for call "+p.CallID)
			}
			return map[string]string{"status": "muted"}, nil
		case "unmute":
			if audioMuter == nil {
				return nil, ipc.NewError(ipc.ErrCodeNoSuchCall, "audio not enabled on this daemon")
			}
			if !audioMuter.SetMuted(p.CallID, false) {
				return nil, ipc.NewError(ipc.ErrCodeNoSuchCall, "no audio session for call "+p.CallID)
			}
			return map[string]string{"status": "unmuted"}, nil
		}
		s, ok := mgr.Get(p.CallID)
		if !ok {
			return nil, ipc.NewError(ipc.ErrCodeNoSuchCall, fmt.Sprintf("call %q not found", p.CallID))
		}
		switch p.Action {
		case "accept":
			if err := eng.Accept(s); err != nil {
				return nil, ipc.NewError(ipc.ErrCodeInvalidParams, err.Error())
			}
		case "hangup":
			if err := eng.Hangup(s, p.Reason); err != nil {
				return nil, ipc.NewError(ipc.ErrCodeInternalError, err.Error())
			}
		default:
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams,
				fmt.Sprintf("unknown action %q (expected: accept | hangup | mute | unmute)", p.Action))
		}
		return CallsOK{Status: "ok"}, nil
	}
}

// resolveTarget resolves a friends-name OR raw peer-id string to a peer.ID.
//
// Resolution order:
//  1. If a friend store is provided and target matches a friend name → that peer ID.
//  2. Else attempt peer.Decode(target). On success, return.
//  3. Else if the input "looks like" a peer ID (prefixed 12D3, Qm, or 1) but
//     decode failed, return InvalidParams (caller passed a malformed peer ID).
//  4. Else return NoSuchFriend (caller probably meant a friend that doesn't exist).
func resolveTarget(target string, store *friends.Store) (peer.ID, error) {
	if store != nil {
		if f, ok := store.Get(target); ok {
			return f.PeerID, nil
		}
	}
	pid, err := peer.Decode(target)
	if err == nil {
		return pid, nil
	}
	if looksLikePeerID(target) {
		return "", ipc.NewError(ipc.ErrCodeInvalidParams,
			fmt.Sprintf("invalid peer id %q: %v", target, err))
	}
	return "", ipc.NewError(ipc.ErrCodeNoSuchFriend,
		fmt.Sprintf("no friend named %q", target))
}

// looksLikePeerID returns true if s has the surface shape of a libp2p
// peer ID: long enough, and starts with one of the common multibase
// prefixes. Used only to choose a more accurate error code.
func looksLikePeerID(s string) bool {
	if len(s) < 32 {
		return false
	}
	switch {
	case len(s) >= 4 && s[:4] == "12D3":
		return true
	case len(s) >= 2 && (s[:2] == "Qm" || s[:2] == "1A" || s[:2] == "1B"):
		return true
	}
	return false
}

// pipeStateChanges forwards Session state-change events to the IPC
// connection until the session ends or the connection closes. A synthetic
// state-change reflecting the current state is emitted first so a caller
// who subscribes after a transition has already happened (e.g. the
// Ringing transition that Place performs synchronously) still observes
// the session's current state.
//
// Because Subscribe registers the receiver before we read State, a
// transition firing in that window would be captured both in the snapshot
// and on the events channel. We drop a leading duplicate of the synthetic
// state to keep wire output deduplicated.
func pipeStateChanges(conn ipc.Conn, s *call.Session, subID string) {
	subPID, events := s.Subscribe()
	current := call.StateChange{
		SessionID: s.ID(),
		State:     s.State().String(),
		Direction: s.Direction().String(),
		Remote:    s.Remote(),
		Time:      time.Now(),
	}
	_ = conn.EmitEvent(subID, "call.state_changed", current)
	if current.State == call.StateEnded.String() {
		s.Unsubscribe(subPID)
		return
	}
	go func() {
		defer s.Unsubscribe(subPID)
		first := true
		for {
			select {
			case <-conn.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				if first {
					first = false
					if ev.State == current.State {
						// Same transition we just synthesized — drop.
						continue
					}
				}
				_ = conn.EmitEvent(subID, "call.state_changed", ev)
				if ev.State == call.StateEnded.String() {
					return
				}
			}
		}
	}()
}
