package methods

import (
	"context"
	"encoding/json"
	"time"

	"opencom/internal/call"
	"opencom/internal/friends"
	"opencom/internal/identity"
	"opencom/internal/invite"
	"opencom/internal/ipc"
)

// DaemonStatusSummaryResult is the consolidated daemon-state response
// powering `opencom status`.
type DaemonStatusSummaryResult struct {
	Identity      DaemonStatusResult `json:"identity"`
	Friends       []FriendsListEntry `json:"friends"`
	Calls         []CallsListEntry   `json:"calls"`
	Invites       []InviteListEntry  `json:"invites"`
	ServiceStatus string             `json:"service_status,omitempty"`
}

// DaemonStatusSummary aggregates the existing per-domain handlers into
// a single payload for the CLI's `opencom status` command. ServiceStatus
// is left blank — the CLI fills it client-side via service.Status() since
// the daemon doesn't know its own service installation state.
func DaemonStatusSummary(
	version string,
	kp identity.Keypair,
	started time.Time,
	listenAddrs func() []string,
	reachability func() string,
	friendsStore *friends.Store,
	presence *friends.Presence,
	callMgr *call.Manager,
	inviteStore *invite.Store,
) ipc.Handler {
	return func(_ context.Context, _ json.RawMessage) (interface{}, error) {
		statusH := DaemonStatus(version, kp, started, listenAddrs, reachability)
		statusOut, err := statusH(context.Background(), nil)
		if err != nil {
			return nil, err
		}
		ident := statusOut.(DaemonStatusResult)

		friendsH := FriendsList(friendsStore, presence)
		friendsOut, err := friendsH(context.Background(), nil)
		if err != nil {
			return nil, err
		}
		friendsResult := friendsOut.(FriendsListResult)

		callsH := CallsList(callMgr)
		callsOut, err := callsH(context.Background(), nil)
		if err != nil {
			return nil, err
		}
		callsResult := callsOut.(CallsListResult)

		invitesH := InviteList(inviteStore)
		invitesOut, err := invitesH(context.Background(), nil)
		if err != nil {
			return nil, err
		}
		invitesResult := invitesOut.(InviteListResult)

		return DaemonStatusSummaryResult{
			Identity: ident,
			Friends:  friendsResult.Friends,
			Calls:    callsResult.Calls,
			Invites:  invitesResult.Invites,
		}, nil
	}
}
