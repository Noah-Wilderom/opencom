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
type InviteCreateResult struct {
	Code      string `json:"code"`
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expires_at"`
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
// records it locally. Returns the pretty code, URL form, and expiry.
func InviteCreate(mgr *invite.Manager) ipc.Handler {
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
		return InviteCreateResult{
			Code:      res.Code.Pretty(),
			URL:       res.URL,
			ExpiresAt: res.ExpiresAt.Unix(),
		}, nil
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
