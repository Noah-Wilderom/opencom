package methods

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"opencom/internal/friends"
	"opencom/internal/identity"
	"opencom/internal/ipc"
)

// FriendsAddParams configures a friends.add invocation.
type FriendsAddParams struct {
	KeyPath string `json:"key_path"`
	Name    string `json:"name,omitempty"`
}

// FriendsListEntry is one row in the friends.list result.
type FriendsListEntry struct {
	Name     string    `json:"name"`
	PeerID   peer.ID   `json:"peer_id"`
	Online   bool      `json:"online"`
	LastSeen time.Time `json:"last_seen"`
	AddedAt  time.Time `json:"added_at"`
}

// FriendsListResult is the friends.list response shape.
type FriendsListResult struct {
	Friends []FriendsListEntry `json:"friends"`
}

// FriendsRemoveParams names which friend to remove.
type FriendsRemoveParams struct {
	Name string `json:"name"`
}

// FriendsRenameParams names a rename target.
type FriendsRenameParams struct {
	Name    string `json:"name"`
	NewName string `json:"new_name"`
}

// FriendsShowParams names which friend to fetch in detail.
type FriendsShowParams struct {
	Name string `json:"name"`
}

// FriendsShowResult is the full friend record as exposed by friends.show.
type FriendsShowResult struct {
	Name      string    `json:"name"`
	PeerID    peer.ID   `json:"peer_id"`
	PublicKey string    `json:"public_key"`
	AddedAt   time.Time `json:"added_at"`
	Online    bool      `json:"online"`
	LastSeen  time.Time `json:"last_seen"`
}

// FriendsSubscribePresenceResult is the friends.subscribe_presence response.
type FriendsSubscribePresenceResult struct {
	SubscriptionID string `json:"subscription_id"`
}

// FriendsRemoveResult is the friends.remove response.
type FriendsRemoveResult struct {
	Removed string `json:"removed"`
}

// FriendsRenameResult is the friends.rename response.
type FriendsRenameResult struct {
	Name    string `json:"name"`
	NewName string `json:"new_name"`
}

// FriendsAdd reads the YAML pubkey file at params.KeyPath, adds the
// resulting Friend to the store (using params.Name if set, otherwise the
// name embedded in the key file), and returns a FriendsListEntry for the
// newly-added friend.
func FriendsAdd(store *friends.Store) ipc.Handler {
	return func(_ context.Context, raw json.RawMessage) (interface{}, error) {
		var p FriendsAddParams
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "invalid params: "+err.Error())
			}
		}
		if p.KeyPath == "" {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "key_path is required")
		}
		ident, err := identity.ReadExport(p.KeyPath)
		if err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, fmt.Sprintf("reading key file: %v", err))
		}
		pid, err := peer.Decode(ident.PeerID)
		if err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, fmt.Sprintf("decoding peer id: %v", err))
		}
		name := p.Name
		if name == "" {
			name = ident.Name
		}
		if name == "" {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "key file has no name and --name not provided")
		}
		f := friends.Friend{
			Name:           name,
			PeerID:         pid,
			PublicKey:      ident.PublicKey,
			AddedAt:        time.Now().UTC(),
			RendezvousHint: ident.RendezvousHint,
		}
		if err := store.Add(f); err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, err.Error())
		}
		return FriendsListEntry{
			Name:    f.Name,
			PeerID:  f.PeerID,
			AddedAt: f.AddedAt,
		}, nil
	}
}

// FriendsList returns the sorted friends list with online/last-seen state
// from the presence tracker.
func FriendsList(store *friends.Store, presence *friends.Presence) ipc.Handler {
	return func(_ context.Context, _ json.RawMessage) (interface{}, error) {
		all := store.List()
		out := FriendsListResult{Friends: make([]FriendsListEntry, 0, len(all))}
		for _, f := range all {
			out.Friends = append(out.Friends, FriendsListEntry{
				Name:     f.Name,
				PeerID:   f.PeerID,
				Online:   presence.IsOnline(f.PeerID),
				LastSeen: presence.LastSeen(f.PeerID),
				AddedAt:  f.AddedAt,
			})
		}
		return out, nil
	}
}

// FriendsRemove removes the named friend.
func FriendsRemove(store *friends.Store) ipc.Handler {
	return func(_ context.Context, raw json.RawMessage) (interface{}, error) {
		var p FriendsRemoveParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "invalid params: "+err.Error())
		}
		if err := store.Remove(p.Name); err != nil {
			if errors.Is(err, friends.ErrFriendNotFound) {
				return nil, ipc.NewError(ipc.ErrCodeNoSuchFriend, err.Error())
			}
			return nil, ipc.NewError(ipc.ErrCodeInternalError, err.Error())
		}
		return FriendsRemoveResult{Removed: p.Name}, nil
	}
}

// FriendsRename renames a friend.
func FriendsRename(store *friends.Store) ipc.Handler {
	return func(_ context.Context, raw json.RawMessage) (interface{}, error) {
		var p FriendsRenameParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "invalid params: "+err.Error())
		}
		if err := store.Rename(p.Name, p.NewName); err != nil {
			if errors.Is(err, friends.ErrFriendNotFound) {
				return nil, ipc.NewError(ipc.ErrCodeNoSuchFriend, err.Error())
			}
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, err.Error())
		}
		return FriendsRenameResult{Name: p.Name, NewName: p.NewName}, nil
	}
}

// FriendsShow returns full detail for a single friend.
func FriendsShow(store *friends.Store, presence *friends.Presence) ipc.Handler {
	return func(_ context.Context, raw json.RawMessage) (interface{}, error) {
		var p FriendsShowParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "invalid params: "+err.Error())
		}
		f, ok := store.Get(p.Name)
		if !ok {
			return nil, ipc.NewError(ipc.ErrCodeNoSuchFriend, fmt.Sprintf("friend %q not found", p.Name))
		}
		return FriendsShowResult{
			Name:      f.Name,
			PeerID:    f.PeerID,
			PublicKey: f.PublicKey,
			AddedAt:   f.AddedAt,
			Online:    presence.IsOnline(f.PeerID),
			LastSeen:  presence.LastSeen(f.PeerID),
		}, nil
	}
}

// FriendsSubscribePresence registers a long-running subscription that
// emits a "presence_changed" event whenever any peer transitions
// online/offline.
func FriendsSubscribePresence(presence *friends.Presence) ipc.Handler {
	return func(ctx context.Context, _ json.RawMessage) (interface{}, error) {
		conn := ipc.ConnFromContext(ctx)
		if conn == nil {
			return nil, ipc.NewError(ipc.ErrCodeInternalError, "subscribe_presence requires a connection context")
		}
		subID := newSubscriptionID()
		presID, events := presence.Subscribe()

		go func() {
			defer presence.Unsubscribe(presID)
			for {
				select {
				case <-conn.Done():
					return
				case ev, ok := <-events:
					if !ok {
						return
					}
					_ = conn.EmitEvent(subID, "presence_changed", ev)
				}
			}
		}()

		return FriendsSubscribePresenceResult{SubscriptionID: subID}, nil
	}
}

// newSubscriptionID returns a short unique-ish string for the lifetime
// of one daemon. Uses time + counter rather than crypto-random because
// uniqueness is per-connection, not security-sensitive.
var subCounter atomic.Uint64

func newSubscriptionID() string {
	return fmt.Sprintf("s-%d-%d", time.Now().UnixNano(), subCounter.Add(1))
}
