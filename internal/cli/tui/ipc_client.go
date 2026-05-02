package tui

import (
	"context"
	"sync"

	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
)

// Client is the daemon-facing surface the TUI uses. Production code
// uses the *ipc.Client wrapper; tests use FakeClient.
type Client interface {
	// FriendsList returns the friends list as the daemon currently sees
	// it (sorted, with online/last-seen state populated from presence).
	FriendsList(ctx context.Context) ([]methods.FriendsListEntry, error)
	// FriendsAdd adds a friend from a public-key YAML file at keyPath.
	// If name is non-empty it overrides the name embedded in the key file.
	FriendsAdd(ctx context.Context, keyPath, name string) (methods.FriendsListEntry, error)
	// FriendsRename renames the friend currently called name to newName.
	FriendsRename(ctx context.Context, name, newName string) error
	// FriendsRemove removes the named friend.
	FriendsRemove(ctx context.Context, name string) error

	// InviteCreate generates a fresh invite. ttl is parsed by the daemon
	// (Go duration string); pass "" for the daemon default.
	InviteCreate(ctx context.Context, ttl string) (methods.InviteCreateResult, error)
	// InviteRedeem accepts pretty/bare/URL form invite codes.
	InviteRedeem(ctx context.Context, code string) (methods.InviteRedeemResult, error)

	// CallsStartSubscribe places an outbound call to target (friend name
	// or peer ID) and returns a Subscription that emits the call's
	// state-change events. The first event delivered is a synthetic
	// snapshot of the session's current state.
	CallsStartSubscribe(ctx context.Context, target string) (Subscription, error)
	// CallsList returns the daemon's currently-tracked sessions.
	CallsList(ctx context.Context) (methods.CallsListResult, error)
	// CallsAction performs accept/hangup/mute/unmute on an existing call.
	// reason is optional and only meaningful for hangup.
	CallsAction(ctx context.Context, callID, action, reason string) error

	// SubscribePresence registers a subscription that emits a
	// "presence_changed" event whenever any peer transitions
	// online/offline.
	SubscribePresence(ctx context.Context) (Subscription, error)
	// SubscribeCalls registers a subscription that emits "call_state"
	// events for every state transition across all active sessions.
	SubscribeCalls(ctx context.Context) (Subscription, error)
	// SubscribeInviteRedemption registers a subscription that fires
	// "invite_redeemed" when the named code is redeemed by a peer.
	SubscribeInviteRedemption(ctx context.Context, code string) (Subscription, error)

	// DaemonStatus returns the daemon's current status snapshot.
	DaemonStatus(ctx context.Context) (methods.DaemonStatusResult, error)

	// Close shuts down the underlying connection. After Close, no
	// further methods may be called on this Client.
	Close() error
}

// Subscription is the TUI-facing view of a long-running ipc.Subscription.
// Events delivers SubEvents until the subscription is closed; Close is
// idempotent.
type Subscription interface {
	// Events returns the channel events arrive on. Closed when the
	// subscription is closed (locally or by the daemon).
	Events() <-chan SubEvent
	// Close stops local routing for the subscription. Safe to call
	// multiple times.
	Close() error
}

// SubEvent is one event read from the underlying ipc.Subscription.
// Method holds the event name (e.g. "presence_changed", "call_state",
// "invite_redeemed", "call.state_changed"); Data holds the raw JSON
// payload, which the receiver unmarshals into the method-specific
// struct.
type SubEvent struct {
	Method string
	Data   []byte
}

// realClient wraps *ipc.Client to satisfy the Client interface.
type realClient struct{ inner *ipc.Client }

// newRealClient returns a Client backed by the given *ipc.Client.
func newRealClient(c *ipc.Client) Client { return &realClient{inner: c} }

// FriendsList implements Client.FriendsList.
func (c *realClient) FriendsList(ctx context.Context) ([]methods.FriendsListEntry, error) {
	var res methods.FriendsListResult
	if err := c.inner.Call(ctx, "friends.list", nil, &res); err != nil {
		return nil, err
	}
	return res.Friends, nil
}

// FriendsAdd implements Client.FriendsAdd.
func (c *realClient) FriendsAdd(ctx context.Context, keyPath, name string) (methods.FriendsListEntry, error) {
	var entry methods.FriendsListEntry
	err := c.inner.Call(ctx, "friends.add",
		methods.FriendsAddParams{KeyPath: keyPath, Name: name}, &entry)
	return entry, err
}

// FriendsRename implements Client.FriendsRename.
func (c *realClient) FriendsRename(ctx context.Context, name, newName string) error {
	var res methods.FriendsRenameResult
	return c.inner.Call(ctx, "friends.rename",
		methods.FriendsRenameParams{Name: name, NewName: newName}, &res)
}

// FriendsRemove implements Client.FriendsRemove.
func (c *realClient) FriendsRemove(ctx context.Context, name string) error {
	var res methods.FriendsRemoveResult
	return c.inner.Call(ctx, "friends.remove",
		methods.FriendsRemoveParams{Name: name}, &res)
}

// InviteCreate implements Client.InviteCreate.
func (c *realClient) InviteCreate(ctx context.Context, ttl string) (methods.InviteCreateResult, error) {
	var res methods.InviteCreateResult
	err := c.inner.Call(ctx, "invite.create",
		methods.InviteCreateParams{TTL: ttl}, &res)
	return res, err
}

// InviteRedeem implements Client.InviteRedeem.
func (c *realClient) InviteRedeem(ctx context.Context, code string) (methods.InviteRedeemResult, error) {
	var res methods.InviteRedeemResult
	err := c.inner.Call(ctx, "invite.redeem",
		methods.InviteRedeemParams{Code: code}, &res)
	return res, err
}

// CallsStartSubscribe implements Client.CallsStartSubscribe. The
// underlying "calls.start" method both places the call AND registers a
// subscription for its state-change events; we Subscribe rather than
// Call so the *ipc.Subscription is wired up before the daemon emits
// the synthetic initial state.
func (c *realClient) CallsStartSubscribe(ctx context.Context, target string) (Subscription, error) {
	sub, err := c.inner.Subscribe(ctx, "calls.start",
		methods.CallsStartParams{Target: target})
	if err != nil {
		return nil, err
	}
	return wrapSubscription(sub), nil
}

// CallsList implements Client.CallsList.
func (c *realClient) CallsList(ctx context.Context) (methods.CallsListResult, error) {
	var res methods.CallsListResult
	err := c.inner.Call(ctx, "calls.list", nil, &res)
	return res, err
}

// CallsAction implements Client.CallsAction.
func (c *realClient) CallsAction(ctx context.Context, callID, action, reason string) error {
	var res methods.CallsOK
	return c.inner.Call(ctx, "calls.action",
		methods.CallsActionParams{CallID: callID, Action: action, Reason: reason}, &res)
}

// SubscribePresence implements Client.SubscribePresence.
func (c *realClient) SubscribePresence(ctx context.Context) (Subscription, error) {
	sub, err := c.inner.Subscribe(ctx, "friends.subscribe_presence", nil)
	if err != nil {
		return nil, err
	}
	return wrapSubscription(sub), nil
}

// SubscribeCalls implements Client.SubscribeCalls.
func (c *realClient) SubscribeCalls(ctx context.Context) (Subscription, error) {
	sub, err := c.inner.Subscribe(ctx, "calls.subscribe", nil)
	if err != nil {
		return nil, err
	}
	return wrapSubscription(sub), nil
}

// SubscribeInviteRedemption implements Client.SubscribeInviteRedemption.
func (c *realClient) SubscribeInviteRedemption(ctx context.Context, code string) (Subscription, error) {
	sub, err := c.inner.Subscribe(ctx, "invite.subscribe_redemption",
		methods.InviteSubscribeRedemptionParams{Code: code})
	if err != nil {
		return nil, err
	}
	return wrapSubscription(sub), nil
}

// DaemonStatus implements Client.DaemonStatus.
func (c *realClient) DaemonStatus(ctx context.Context) (methods.DaemonStatusResult, error) {
	var res methods.DaemonStatusResult
	err := c.inner.Call(ctx, "daemon.status", nil, &res)
	return res, err
}

// Close implements Client.Close.
func (c *realClient) Close() error { return c.inner.Close() }

// realSubscription adapts *ipc.Subscription to the Subscription
// interface by translating *ipc.Event values into SubEvent values on
// a forwarding channel.
type realSubscription struct {
	inner    *ipc.Subscription
	ch       chan SubEvent
	done     chan struct{}
	closeOnce sync.Once
}

// wrapSubscription wraps an *ipc.Subscription in a realSubscription
// and starts the forwarding goroutine.
func wrapSubscription(inner *ipc.Subscription) *realSubscription {
	rs := &realSubscription{
		inner: inner,
		ch:    make(chan SubEvent, 16),
		done:  make(chan struct{}),
	}
	go rs.forward()
	return rs
}

// forward drains the inner *ipc.Subscription's Events channel,
// translating each *ipc.Event into a SubEvent and pushing it onto
// rs.ch. Returns when the inner channel closes or rs.Close is called.
func (rs *realSubscription) forward() {
	defer close(rs.ch)
	for {
		select {
		case <-rs.done:
			return
		case ev, ok := <-rs.inner.Events:
			if !ok {
				return
			}
			select {
			case rs.ch <- SubEvent{Method: ev.Kind, Data: ev.Data}:
			case <-rs.done:
				return
			}
		}
	}
}

// Events implements Subscription.Events.
func (rs *realSubscription) Events() <-chan SubEvent { return rs.ch }

// Close implements Subscription.Close. Idempotent.
func (rs *realSubscription) Close() error {
	rs.closeOnce.Do(func() {
		close(rs.done)
		rs.inner.Close()
	})
	return nil
}

// FakeClient is a hand-rolled stand-in used by TUI tests. All fields
// are set by the test before use; methods return the canned values
// without contacting any daemon.
type FakeClient struct {
	// Friends is the canned response for FriendsList.
	Friends []methods.FriendsListEntry
	// CallsResp is the canned response for CallsList.
	CallsResp methods.CallsListResult
	// Status is the canned response for DaemonStatus.
	Status methods.DaemonStatusResult
	// InviteResp is the canned response for InviteCreate.
	InviteResp methods.InviteCreateResult
	// RedeemResp is the canned response for InviteRedeem.
	RedeemResp methods.InviteRedeemResult
	// AddResp is the canned response for FriendsAdd.
	AddResp methods.FriendsListEntry

	// StartSub is returned from CallsStartSubscribe.
	StartSub *FakeSubscription
	// PresenceSub is returned from SubscribePresence.
	PresenceSub *FakeSubscription
	// CallsSub is returned from SubscribeCalls.
	CallsSub *FakeSubscription
	// InviteSub is returned from SubscribeInviteRedemption.
	InviteSub *FakeSubscription

	// Actions records every CallsAction invocation in call order.
	Actions []FakeAction

	// Closed is set to true by Close.
	Closed bool
}

// FakeAction is one recorded CallsAction invocation.
type FakeAction struct {
	CallID string
	Action string
	Reason string
}

// FriendsList implements Client.FriendsList.
func (f *FakeClient) FriendsList(_ context.Context) ([]methods.FriendsListEntry, error) {
	return f.Friends, nil
}

// FriendsAdd implements Client.FriendsAdd.
func (f *FakeClient) FriendsAdd(_ context.Context, _, _ string) (methods.FriendsListEntry, error) {
	return f.AddResp, nil
}

// FriendsRename implements Client.FriendsRename.
func (f *FakeClient) FriendsRename(_ context.Context, _, _ string) error { return nil }

// FriendsRemove implements Client.FriendsRemove.
func (f *FakeClient) FriendsRemove(_ context.Context, _ string) error { return nil }

// InviteCreate implements Client.InviteCreate.
func (f *FakeClient) InviteCreate(_ context.Context, _ string) (methods.InviteCreateResult, error) {
	return f.InviteResp, nil
}

// InviteRedeem implements Client.InviteRedeem.
func (f *FakeClient) InviteRedeem(_ context.Context, _ string) (methods.InviteRedeemResult, error) {
	return f.RedeemResp, nil
}

// CallsStartSubscribe implements Client.CallsStartSubscribe.
func (f *FakeClient) CallsStartSubscribe(_ context.Context, _ string) (Subscription, error) {
	if f.StartSub == nil {
		f.StartSub = NewFakeSubscription()
	}
	return f.StartSub, nil
}

// CallsList implements Client.CallsList.
func (f *FakeClient) CallsList(_ context.Context) (methods.CallsListResult, error) {
	return f.CallsResp, nil
}

// CallsAction implements Client.CallsAction.
func (f *FakeClient) CallsAction(_ context.Context, callID, action, reason string) error {
	f.Actions = append(f.Actions, FakeAction{CallID: callID, Action: action, Reason: reason})
	return nil
}

// SubscribePresence implements Client.SubscribePresence.
func (f *FakeClient) SubscribePresence(_ context.Context) (Subscription, error) {
	if f.PresenceSub == nil {
		f.PresenceSub = NewFakeSubscription()
	}
	return f.PresenceSub, nil
}

// SubscribeCalls implements Client.SubscribeCalls.
func (f *FakeClient) SubscribeCalls(_ context.Context) (Subscription, error) {
	if f.CallsSub == nil {
		f.CallsSub = NewFakeSubscription()
	}
	return f.CallsSub, nil
}

// SubscribeInviteRedemption implements Client.SubscribeInviteRedemption.
func (f *FakeClient) SubscribeInviteRedemption(_ context.Context, _ string) (Subscription, error) {
	if f.InviteSub == nil {
		f.InviteSub = NewFakeSubscription()
	}
	return f.InviteSub, nil
}

// DaemonStatus implements Client.DaemonStatus.
func (f *FakeClient) DaemonStatus(_ context.Context) (methods.DaemonStatusResult, error) {
	return f.Status, nil
}

// Close implements Client.Close.
func (f *FakeClient) Close() error {
	f.Closed = true
	return nil
}

// FakeSubscription is a manually-driven Subscription for tests.
// Events pushed via Push are delivered to readers of Events(); Close
// closes the underlying channel and is idempotent.
type FakeSubscription struct {
	ch     chan SubEvent
	closed bool
	mu     sync.Mutex
}

// NewFakeSubscription returns a FakeSubscription with a buffered
// channel of capacity 16.
func NewFakeSubscription() *FakeSubscription {
	return &FakeSubscription{ch: make(chan SubEvent, 16)}
}

// Events implements Subscription.Events.
func (f *FakeSubscription) Events() <-chan SubEvent { return f.ch }

// Push delivers a SubEvent to the channel. Panics if the
// subscription has been closed.
func (f *FakeSubscription) Push(method string, payload []byte) {
	f.ch <- SubEvent{Method: method, Data: payload}
}

// Close implements Subscription.Close. Idempotent.
func (f *FakeSubscription) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.ch)
	}
	return nil
}
