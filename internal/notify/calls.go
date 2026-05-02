package notify

import (
	"context"
	"fmt"

	"github.com/libp2p/go-libp2p/core/peer"
	"go.uber.org/zap"

	"opencom/internal/call"
	"opencom/internal/friends"
)

// FriendNamer resolves a peer.ID to a display name. *friends.Store is
// the production implementation; tests inject a fake.
type FriendNamer interface {
	GetByPeerID(id peer.ID) (friends.Friend, bool)
}

// CallSource is the call-state subscription surface this package
// consumes. *call.Manager is the production implementation; the same
// interface used by audio.Manager.
type CallSource interface {
	SubscribeStateChanges() <-chan call.StateChange
	UnsubscribeStateChanges(ch <-chan call.StateChange)
}

// WatchCalls subscribes to call state changes and fires a desktop
// notification on each transition the user cares about (ringing in
// either direction, connected, ended). Blocks until ctx is cancelled.
//
// Spawn this as a goroutine alongside audio.Manager.Start in daemon
// startup. Notifier may be Disabled to skip every event without
// changing the call-graph.
//
// log is optional — when non-nil, the watcher emits info-level
// breadcrumbs at startup and on every state-change event so users
// debugging missing notifications can see whether the watcher is
// even receiving events vs. whether the Notifier is silently no-oping.
func WatchCalls(ctx context.Context, src CallSource, names FriendNamer, n Notifier, log *zap.Logger) {
	events := src.SubscribeStateChanges()
	defer src.UnsubscribeStateChanges(events)
	if log != nil {
		log.Info("notify: watching call state changes")
		defer log.Info("notify: watcher exiting")
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			title, body := formatNotification(ev, names)
			if log != nil {
				log.Info("notify: received call event",
					zap.String("state", ev.State),
					zap.String("direction", ev.Direction),
					zap.String("session", ev.SessionID),
					zap.String("notify_title", title))
			}
			if title == "" {
				continue
			}
			n.Notify(title, body)
		}
	}
}

// formatNotification builds the (title, body) for a state change.
// Returns ("", "") when the state is one we don't notify on (e.g.
// "connecting", which is just a transient step between ringing and
// connected). Exposed for testing.
func formatNotification(ev call.StateChange, names FriendNamer) (title, body string) {
	who := friendName(names, ev.Remote)
	switch ev.State {
	case "ringing":
		if ev.Direction == "inbound" {
			return "Incoming call", "from " + who
		}
		return "Calling…", who
	case "connected":
		return "Call connected", who
	case "ended":
		body = who
		if ev.Reason != "" {
			body = fmt.Sprintf("%s — %s", who, ev.Reason)
		}
		return "Call ended", body
	}
	return "", ""
}

// friendName returns the friend's display name for id, or a short
// peer-ID fallback when the peer isn't in the friends store (which
// happens for inbound calls from a sender we haven't added back).
func friendName(names FriendNamer, id peer.ID) string {
	if names != nil {
		if f, ok := names.GetByPeerID(id); ok && f.Name != "" {
			return f.Name
		}
	}
	s := id.String()
	if len(s) > 12 {
		return s[:6] + "…" + s[len(s)-4:]
	}
	return s
}
