// Package tui — message types dispatched through the Bubble Tea
// program. Defined in one place so the model's Update can pattern-match
// without depending on every Cmd factory.
package tui

import (
	"opencom/internal/call"
	"opencom/internal/ipc/methods"
)

// errMsg is returned from any tea.Cmd that hits an error path. The
// model logs (or surfaces) and continues; transient errors don't
// terminate the TUI.
type errMsg struct {
	Err    error
	Source string // e.g. "friends.list", "calls.subscribe"
}

// friendsLoadedMsg is the response from loadFriendsCmd.
type friendsLoadedMsg struct {
	Friends []methods.FriendsListEntry
}

// presenceChangedMsg is one event from the friends.subscribe_presence stream.
type presenceChangedMsg struct {
	Name   string
	PeerID string
	Online bool
}

// callStateMsg is one event from the calls.subscribe stream.
type callStateMsg struct {
	State call.StateChange
}

// daemonStatusMsg refreshes the relay-reservation/reachability footer.
type daemonStatusMsg struct {
	Status methods.DaemonStatusResult
}

// inviteCreatedMsg is the response from createInviteCmd.
type inviteCreatedMsg struct {
	Result methods.InviteCreateResult
}

// inviteRedeemedMsg is one event from invite.subscribe_redemption.
type inviteRedeemedMsg struct {
	Code       string
	RedeemedBy string
}

// callsListMsg is the periodic refresh of audio stats for the dock.
type callsListMsg struct {
	Result methods.CallsListResult
}

// inviteRedeemDoneMsg is the response from redeemInviteCmd (the
// outbound side: the local user redeemed someone else's invite). The
// stream-side counterpart is inviteRedeemedMsg.
type inviteRedeemDoneMsg struct {
	Result methods.InviteRedeemResult
}

// friendAddedMsg is the response from addFriendCmd.
type friendAddedMsg struct {
	Friend methods.FriendsListEntry
}

// friendRenamedMsg signals a successful friends.rename.
type friendRenamedMsg struct {
	Name    string
	NewName string
}

// friendRemovedMsg signals a successful friends.remove.
type friendRemovedMsg struct {
	Name string
}

// callStartedMsg carries the Subscription opened by calls.start.
type callStartedMsg struct {
	Sub Subscription
}

// callActionDoneMsg signals a successful calls.action.
type callActionDoneMsg struct {
	CallID string
	Action string
}

// presenceSubMsg carries a freshly-registered presence Subscription.
type presenceSubMsg struct {
	Sub Subscription
}

// callsSubMsg carries a freshly-registered calls Subscription.
type callsSubMsg struct {
	Sub Subscription
}

// inviteRedemptionSubMsg carries a freshly-registered
// invite-redemption Subscription, tagged with the code it watches.
type inviteRedemptionSubMsg struct {
	Sub  Subscription
	Code string
}

// subEventMsg carries a single event read from a subscription. Label
// identifies which subscription it came from (e.g. "presence",
// "calls", "invite:OPEN-XXXX-XXXX").
type subEventMsg struct {
	Label string
	Event SubEvent
}

// subClosedMsg signals that the labelled subscription's Events
// channel was closed (locally or by the daemon).
type subClosedMsg struct {
	Label string
}
