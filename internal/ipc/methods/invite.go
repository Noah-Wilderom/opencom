package methods

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"opencom/internal/friends"
	"opencom/internal/invite"
	"opencom/internal/ipc"
)

// InviteCreateParams configures an invite.create invocation.
type InviteCreateParams struct {
	TTL string `json:"ttl,omitempty"`
}

// InviteCreateResult is the invite.create response shape.
//
// DHTPublishWarning is empty on success and a human-readable message
// on best-effort DHT failure. When non-empty, the short Code may not
// be redeemable until DHT recovers, but URL is always usable.
//
// ReachableAddrs lists the host's currently dialable cross-network
// addresses (publicly-routable addresses + relay-circuit reservations).
// Empty means the URL likely only works on the same LAN until AutoRelay
// negotiates a reservation (typically ~30s after startup).
type InviteCreateResult struct {
	Code              string   `json:"code"`
	URL               string   `json:"url"`
	ExpiresAt         int64    `json:"expires_at"`
	DHTPublishWarning string   `json:"dht_publish_warning,omitempty"`
	ReachableAddrs    []string `json:"reachable_addrs,omitempty"`
}

// InviteListResult is the invite.list response shape.
type InviteListResult struct {
	Invites []InviteListEntry `json:"invites"`
}

// InviteListEntry is one row in the invite.list result.
type InviteListEntry struct {
	Code       string    `json:"code"`
	Pretty     string    `json:"pretty"`
	Active     bool      `json:"active"`
	Consumed   bool      `json:"consumed"`
	ConsumedBy string    `json:"consumed_by,omitempty"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// InviteRevokeParams names the code to revoke.
type InviteRevokeParams struct {
	Code string `json:"code"`
}

// InviteRevokeResult is the invite.revoke response shape.
type InviteRevokeResult struct {
	Removed string `json:"removed"`
}

// InviteRedeemParams accepts pretty, bare, or URL form.
type InviteRedeemParams struct {
	Code string `json:"code"`
}

// InviteRedeemResult reuses the friends list-entry shape so the CLI can
// print "added <name> (<peer-id>)" identically to friends.add.
type InviteRedeemResult struct {
	Friend FriendsListEntry `json:"friend"`
}

// InviteCreate generates a fresh invite, publishes it to the DHT, and
// records it locally. Returns the pretty code, URL form, expiry, and
// the host's currently-reachable cross-network addresses (so the CLI
// can warn if URL invites are LAN-only at the moment).
//
// reachableAddrs is a function so the daemon can return live state at
// invite-creation time rather than the addresses recorded at startup.
func InviteCreate(mgr *invite.Manager, reachableAddrs func() []string) ipc.Handler {
	return func(ctx context.Context, raw json.RawMessage) (interface{}, error) {
		var p InviteCreateParams
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "invalid params: "+err.Error())
			}
		}
		ttl := 30 * time.Minute
		if p.TTL != "" {
			d, err := time.ParseDuration(p.TTL)
			if err != nil {
				return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "invalid ttl: "+err.Error())
			}
			ttl = d
		}
		res, err := mgr.Create(ctx, ttl)
		if err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInternalError, err.Error())
		}
		var reachable []string
		if reachableAddrs != nil {
			reachable = reachableAddrs()
		}
		out := InviteCreateResult{
			Code:           res.Code.Pretty(),
			URL:            res.URL,
			ExpiresAt:      res.ExpiresAt.Unix(),
			ReachableAddrs: reachable,
		}
		if res.DHTPublishErr != nil {
			out.DHTPublishWarning = res.DHTPublishErr.Error()
		}
		return out, nil
	}
}

// InviteList returns every entry (active, consumed, expired) from the
// store. Active means: not consumed and not expired.
func InviteList(store *invite.Store) ipc.Handler {
	return func(_ context.Context, _ json.RawMessage) (interface{}, error) {
		all := store.AllList()
		out := InviteListResult{Invites: make([]InviteListEntry, 0, len(all))}
		now := time.Now()
		for _, e := range all {
			active := !e.Consumed && now.Before(e.ExpiresAt)
			out.Invites = append(out.Invites, InviteListEntry{
				Code:       string(e.Code),
				Pretty:     e.Code.Pretty(),
				Active:     active,
				Consumed:   e.Consumed,
				ConsumedBy: e.ConsumedBy,
				ExpiresAt:  e.ExpiresAt,
			})
		}
		return out, nil
	}
}

// InviteRevoke removes the named code from the local store. The DHT
// record cannot be retracted, but it expires naturally and any redeem
// attempt will fail at the inviter-side store lookup.
func InviteRevoke(mgr *invite.Manager) ipc.Handler {
	return func(_ context.Context, raw json.RawMessage) (interface{}, error) {
		var p InviteRevokeParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "invalid params: "+err.Error())
		}
		c, err := invite.Parse(p.Code)
		if err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, err.Error())
		}
		if !mgr.Revoke(c) {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "invite code not found")
		}
		return InviteRevokeResult{Removed: c.Pretty()}, nil
	}
}

// InviteSubscribeRedemptionParams identifies which invite code to
// subscribe to redemption events for.
type InviteSubscribeRedemptionParams struct {
	Code string `json:"code"`
}

// InviteSubscribeRedemptionResult is the response shape — matches
// FriendsSubscribePresenceResult / CallsSubscribeResult: just a
// subscription ID the caller can correlate with subsequent
// "invite_redeemed" events.
type InviteSubscribeRedemptionResult struct {
	SubscriptionID string `json:"subscription_id"`
}

// InviteRedemptionEvent is the per-event payload emitted on the
// "invite_redeemed" subscription channel.
type InviteRedemptionEvent struct {
	Code       string `json:"code"`
	RedeemedBy string `json:"redeemed_by"`
}

// InviteSubscribeRedemption registers a subscription that fires when
// the named invite code is redeemed by a peer. The TUI's generate-
// invite modal uses this to render "Invite redeemed by Alice" before
// auto-closing.
func InviteSubscribeRedemption(mgr *invite.Manager) ipc.Handler {
	return func(ctx context.Context, raw json.RawMessage) (interface{}, error) {
		var p InviteSubscribeRedemptionParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "invalid params: "+err.Error())
		}
		if p.Code == "" {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "code is required")
		}
		conn := ipc.ConnFromContext(ctx)
		if conn == nil {
			return nil, ipc.NewError(ipc.ErrCodeInternalError, "invite.subscribe_redemption requires a connection context")
		}
		subID := newSubscriptionID()
		events := mgr.SubscribeRedemption(p.Code)
		go func() {
			defer mgr.UnsubscribeRedemption(p.Code, events)
			for {
				select {
				case <-conn.Done():
					return
				case ev, ok := <-events:
					if !ok {
						return
					}
					_ = conn.EmitEvent(subID, "invite_redeemed", InviteRedemptionEvent{
						Code:       ev.Code,
						RedeemedBy: ev.RedeemedBy,
					})
				}
			}
		}()
		return InviteSubscribeRedemptionResult{SubscriptionID: subID}, nil
	}
}

// InviteRedeem accepts both short codes (OPEN-XXXX-XXXX / bare 8-char)
// and long URLs (opencom://join?...). URL form is detected by scanning
// for "://"; otherwise we treat the input as a short code via
// invite.Parse.
func InviteRedeem(mgr *invite.Manager) ipc.Handler {
	return func(ctx context.Context, raw json.RawMessage) (interface{}, error) {
		var p InviteRedeemParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, ipc.NewError(ipc.ErrCodeInvalidParams, "invalid params: "+err.Error())
		}
		var fr friends.Friend
		var redErr error
		if strings.Contains(p.Code, "://") {
			fr, redErr = mgr.RedeemURL(ctx, p.Code)
		} else {
			c, err := invite.Parse(p.Code)
			if err != nil {
				return nil, ipc.NewError(ipc.ErrCodeInvalidParams, err.Error())
			}
			fr, redErr = mgr.Redeem(ctx, c)
		}
		if redErr != nil {
			return nil, ipc.NewError(ipc.ErrCodeInternalError, fmt.Sprintf("redeeming invite: %v", redErr))
		}
		return InviteRedeemResult{Friend: FriendsListEntry{
			Name:    fr.Name,
			PeerID:  fr.PeerID,
			AddedAt: fr.AddedAt,
		}}, nil
	}
}
