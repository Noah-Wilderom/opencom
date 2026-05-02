package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ipcTimeout is the per-call ceiling on synchronous IPC requests
// dispatched as tea.Cmds. Subscriptions are not bounded by this — only
// the round-trip register-subscription call is.
const ipcTimeout = 5 * time.Second

// loadFriendsCmd asks the daemon for the current friends list.
// Returns friendsLoadedMsg or errMsg.
func loadFriendsCmd(c Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ipcTimeout)
		defer cancel()
		fs, err := c.FriendsList(ctx)
		if err != nil {
			return errMsg{Err: err, Source: "friends.list"}
		}
		return friendsLoadedMsg{Friends: fs}
	}
}

// loadCallsCmd asks the daemon for the current calls list.
// Returns callsListMsg or errMsg.
func loadCallsCmd(c Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ipcTimeout)
		defer cancel()
		res, err := c.CallsList(ctx)
		if err != nil {
			return errMsg{Err: err, Source: "calls.list"}
		}
		return callsListMsg{Result: res}
	}
}

// loadDaemonStatusCmd asks the daemon for its current status snapshot.
// Returns daemonStatusMsg or errMsg.
func loadDaemonStatusCmd(c Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ipcTimeout)
		defer cancel()
		st, err := c.DaemonStatus(ctx)
		if err != nil {
			return errMsg{Err: err, Source: "daemon.status"}
		}
		return daemonStatusMsg{Status: st}
	}
}

// addFriendCmd dispatches a friends.add to the daemon.
// Returns friendAddedMsg or errMsg.
func addFriendCmd(c Client, keyPath, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ipcTimeout)
		defer cancel()
		entry, err := c.FriendsAdd(ctx, keyPath, name)
		if err != nil {
			return errMsg{Err: err, Source: "friends.add"}
		}
		return friendAddedMsg{Friend: entry}
	}
}

// renameFriendCmd dispatches a friends.rename.
// Returns friendRenamedMsg or errMsg.
func renameFriendCmd(c Client, name, newName string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ipcTimeout)
		defer cancel()
		if err := c.FriendsRename(ctx, name, newName); err != nil {
			return errMsg{Err: err, Source: "friends.rename"}
		}
		return friendRenamedMsg{Name: name, NewName: newName}
	}
}

// removeFriendCmd dispatches a friends.remove.
// Returns friendRemovedMsg or errMsg.
func removeFriendCmd(c Client, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ipcTimeout)
		defer cancel()
		if err := c.FriendsRemove(ctx, name); err != nil {
			return errMsg{Err: err, Source: "friends.remove"}
		}
		return friendRemovedMsg{Name: name}
	}
}

// createInviteCmd dispatches an invite.create.
// Returns inviteCreatedMsg or errMsg.
func createInviteCmd(c Client, ttl string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ipcTimeout)
		defer cancel()
		res, err := c.InviteCreate(ctx, ttl)
		if err != nil {
			return errMsg{Err: err, Source: "invite.create"}
		}
		return inviteCreatedMsg{Result: res}
	}
}

// redeemInviteCmd dispatches an invite.redeem.
// Returns inviteRedeemDoneMsg or errMsg. (The stream-side
// invite_redeemed event is delivered as inviteRedeemedMsg.)
func redeemInviteCmd(c Client, code string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ipcTimeout)
		defer cancel()
		res, err := c.InviteRedeem(ctx, code)
		if err != nil {
			return errMsg{Err: err, Source: "invite.redeem"}
		}
		return inviteRedeemDoneMsg{Result: res}
	}
}

// startCallCmd places an outbound call and registers a subscription
// for its state-change events. Returns callStartedMsg (carrying the
// Subscription) or errMsg.
func startCallCmd(c Client, target string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ipcTimeout)
		defer cancel()
		sub, err := c.CallsStartSubscribe(ctx, target)
		if err != nil {
			return errMsg{Err: err, Source: "calls.start"}
		}
		return callStartedMsg{Sub: sub}
	}
}

// callActionCmd dispatches a calls.action.
// Returns callActionDoneMsg or errMsg.
func callActionCmd(c Client, callID, action, reason string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ipcTimeout)
		defer cancel()
		if err := c.CallsAction(ctx, callID, action, reason); err != nil {
			return errMsg{Err: err, Source: "calls.action"}
		}
		return callActionDoneMsg{CallID: callID, Action: action}
	}
}

// subscribePresenceCmd registers a presence subscription.
// Returns presenceSubMsg or errMsg.
func subscribePresenceCmd(c Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ipcTimeout)
		defer cancel()
		sub, err := c.SubscribePresence(ctx)
		if err != nil {
			return errMsg{Err: err, Source: "friends.subscribe_presence"}
		}
		return presenceSubMsg{Sub: sub}
	}
}

// subscribeCallsCmd registers a calls subscription.
// Returns callsSubMsg or errMsg.
func subscribeCallsCmd(c Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ipcTimeout)
		defer cancel()
		sub, err := c.SubscribeCalls(ctx)
		if err != nil {
			return errMsg{Err: err, Source: "calls.subscribe"}
		}
		return callsSubMsg{Sub: sub}
	}
}

// subscribeInviteRedemptionCmd registers an invite-redemption
// subscription. Returns inviteRedemptionSubMsg or errMsg.
func subscribeInviteRedemptionCmd(c Client, code string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ipcTimeout)
		defer cancel()
		sub, err := c.SubscribeInviteRedemption(ctx, code)
		if err != nil {
			return errMsg{Err: err, Source: "invite.subscribe_redemption"}
		}
		return inviteRedemptionSubMsg{Sub: sub, Code: code}
	}
}

// tickDaemonStatusCmd polls daemon.status every 5s so the status
// footer reflects relay-reservation / reachability changes that
// happen mid-session (relay drops, AutoRelay re-reserves, etc).
// Returns daemonStatusMsg or errMsg.
func tickDaemonStatusCmd(c Client) tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		s, err := c.DaemonStatus(ctx)
		if err != nil {
			return errMsg{Err: err, Source: "daemon.status"}
		}
		return daemonStatusMsg{Status: s}
	})
}

// tickCallsListCmd polls calls.list every second to refresh the
// dock's audio levels + media-mode display while a call is active.
// Returns callsListMsg or errMsg.
func tickCallsListCmd(c Client) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
		defer cancel()
		res, err := c.CallsList(ctx)
		if err != nil {
			return errMsg{Err: err, Source: "calls.list"}
		}
		return callsListMsg{Result: res}
	})
}

// waitForSubEventCmd reads one event from sub and converts it into a
// subEventMsg. The caller (typically Update) dispatches a follow-up
// waitForSubEventCmd to keep draining the subscription.
//
// label distinguishes events from different subscriptions in the
// model's Update so the same handler can route presence/calls/invite
// events to their respective sub-models.
func waitForSubEventCmd(sub Subscription, label string) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-sub.Events()
		if !ok {
			return subClosedMsg{Label: label}
		}
		return subEventMsg{Label: label, Event: ev}
	}
}
