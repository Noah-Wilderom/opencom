package tui

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/libp2p/go-libp2p/core/peer"

	"opencom/internal/call"
	"opencom/internal/ipc/methods"
)

// Model is the root Bubble Tea model. Composed of focused
// submodels; messages cascade down via Update.
type Model struct {
	client Client

	// terminal size, cached on tea.WindowSizeMsg
	width, height int

	// children
	friends friendsPane
	focus   focusPane
	dock    callDock
	footer  statusFooterState

	// filter mode: when filtering is true, key events go to the
	// filter input instead of the global navigation handler.
	filtering   bool
	filterInput string

	// active modal (one at a time). nil = no modal.
	modal modalView

	// settings modal needs the config path + an optional editor
	// override. Both come from Options at construction.
	configPath     string
	editorOverride string

	// clipboard surface for the add-friend modal.
	clipboard Clipboard

	// callTargetName is the friend name passed to the most recent
	// startCallCmd — used to populate the dock's Remote field before
	// the daemon emits its first state event for the new call.
	callTargetName string
}

// NewModel constructs the root Model from Options.
func NewModel(client Client, opts Options) Model {
	cb := opts.Clipboard
	if cb == nil {
		cb = DefaultClipboard()
	}
	return Model{
		client:         client,
		footer:         statusFooterState{Status: statusIdle},
		configPath:     opts.ConfigPath,
		editorOverride: opts.Editor,
		clipboard:      cb,
	}
}

// NewModelForTest is the test-facing constructor that mirrors NewModel
// without requiring callers to thread Options.
func NewModelForTest(client Client) Model {
	return NewModel(client, Options{Clipboard: &FakeClipboard{}})
}

// Init is the Bubble Tea entry point. Kicks off:
//   - the initial friends load (so the sidebar populates immediately),
//   - the manager-level calls subscription (so the dock reacts to
//     ringing / connected / ended events as soon as they arrive),
//   - the periodic daemon.status poll (so the footer reflects relay
//     reservation / reachability changes mid-session).
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		loadFriendsCmd(m.client),
		subscribeCallsCmd(m.client),
		// Immediate status check so the footer reflects reality on
		// frame 1 instead of showing the default "relay reserved"
		// for 5s while waiting for the first tick.
		loadDaemonStatusCmd(m.client),
		tickDaemonStatusCmd(m.client),
	)
}

// Update is the root reducer. Pattern-matches on the message type and
// either handles it inline (window resize, top-level keys, footer
// updates) or forwards to the active modal.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.footer.Width = msg.Width
	case tea.KeyMsg:
		if m.modal != nil {
			prev := m.modal
			next, cmd := m.modal.Update(msg)
			m.modal = next
			if next == nil {
				if extra := m.handleModalClosed(prev); extra != nil {
					if cmd == nil {
						cmd = extra
					} else {
						cmd = tea.Batch(cmd, extra)
					}
				}
			}
			return m, cmd
		}
		// Filter mode owns input until esc/enter exits it.
		if m.filtering {
			switch msg.Type {
			case tea.KeyEsc:
				m.filtering = false
				m.filterInput = ""
				m.friends.SetFilter("")
				m.syncFocusFromSelection()
				return m, nil
			case tea.KeyEnter:
				m.filtering = false
				m.syncFocusFromSelection()
				return m, nil
			case tea.KeyBackspace:
				if len(m.filterInput) > 0 {
					m.filterInput = m.filterInput[:len(m.filterInput)-1]
					m.friends.SetFilter(m.filterInput)
					m.syncFocusFromSelection()
				}
				return m, nil
			case tea.KeyRunes:
				m.filterInput += string(msg.Runes)
				m.friends.SetFilter(m.filterInput)
				m.syncFocusFromSelection()
				return m, nil
			}
		}
		// In-call key bindings (only when the dock is active and no
		// modal is open). The Help/Quit modals are wired in Tasks
		// 23/24; this block covers the dock-level controls today.
		if m.dock.Active() {
			switch msg.String() {
			case "m":
				return m, callActionCmd(m.client, m.dock.state.CallID, "mute", "")
			case "h":
				return m, callActionCmd(m.client, m.dock.state.CallID, "hangup", "user requested")
			case "esc":
				// Detach: collapse the dock; the call lives on in the daemon.
				m.dock.SetState(callDockState{})
				return m, nil
			}
		}
		switch msg.String() {
		case "down", "j":
			m.friends.MoveDown()
			m.syncFocusFromSelection()
		case "up", "k":
			m.friends.MoveUp()
			m.syncFocusFromSelection()
		case "enter":
			if sel, ok := m.friends.Selected(); ok {
				m.callTargetName = sel.Name
				return m, startCallCmd(m.client, sel.Name)
			}
		case "q":
			if m.dock.Active() {
				m.modal = newQuitModal(m.dock.state.Remote)
				return m, nil
			}
			return m, tea.Quit
		case "?":
			m.modal = newHelpModal()
			return m, nil
		case "s":
			m.modal = newSettingsModal(m.configPath, m.editorOverride)
			return m, nil
		case "a":
			m.modal = newAddFriendModal(m.clipboard, m.client)
			return m, nil
		case "c":
			m.modal = newInviteModal(m.clipboard)
			return m, createInviteCmd(m.client, "")
		case "/":
			m.filtering = true
			return m, nil
		}
	case friendsLoadedMsg:
		m.friends.SetFriends(msg.Friends)
		m.syncFocusFromSelection()
	case callsSubMsg:
		// Subscription opened; start draining its events.
		return m, waitForSubEventCmd(msg.Sub, "calls")
	case callStartedMsg:
		// calls.start subscription opened (per-call). Surface a
		// "calling" placeholder state immediately so the dock pops
		// even before the daemon emits the first state event — a
		// purely-async pop-on-event flow gives the user no feedback
		// from pressing enter.
		if !m.dock.Active() {
			m.dock.SetState(callDockState{
				State:  "calling",
				Remote: m.callTargetName,
			})
			m.footer.Status = statusRinging
			m.footer.Detail = m.callTargetName
		}
		// Drain the per-call subscription so the immediate
		// ringing → connected transitions are received before the
		// manager-level subscription does.
		return m, waitForSubEventCmd(msg.Sub, "calls.start")
	case inviteCreatedMsg:
		// invite.create response — promote modal from loading to
		// active and subscribe to redemption events for the new code.
		if im, ok := m.modal.(*inviteModal); ok {
			im.SetResult(msg.Result)
			return m, subscribeInviteRedemptionCmd(m.client, msg.Result.Code)
		}
		return m, nil
	case inviteRedemptionSubMsg:
		// Subscription opened; start draining its events.
		return m, waitForSubEventCmd(msg.Sub, "invite:"+msg.Code)
	case subEventMsg:
		switch {
		case msg.Label == "calls" || msg.Label == "calls.start":
			var sc call.StateChange
			if err := json.Unmarshal(msg.Event.Data, &sc); err == nil {
				wasActive := m.dock.Active()
				m.applyCallStateChange(sc)
				// Kick off the per-second audio-level tick the
				// first time the dock becomes active.
				if !wasActive && m.dock.Active() {
					return m, tickCallsListCmd(m.client)
				}
			}
			return m, nil
		case strings.HasPrefix(msg.Label, "invite:"):
			if im, ok := m.modal.(*inviteModal); ok {
				var ev methods.InviteRedemptionEvent
				if err := json.Unmarshal(msg.Event.Data, &ev); err == nil {
					im.SetRedeemed(ev.RedeemedBy)
				}
			}
			return m, nil
		}
	case callsListMsg:
		// Live audio-level refresh while a call is active.
		for _, c := range msg.Result.Calls {
			if c.CallID == m.dock.state.CallID {
				s := m.dock.state
				s.RxDB = c.RxLevelDB
				s.TxDB = c.TxLevelDB
				s.Mode = c.MediaMode
				s.Muted = c.Muted
				m.dock.SetState(s)
			}
		}
		if m.dock.Active() {
			return m, tickCallsListCmd(m.client)
		}
	case daemonStatusMsg:
		// Refresh the footer's relay-state indicator unless we're
		// currently showing connected/ringing (those win for context).
		if m.footer.Status != statusConnected && m.footer.Status != statusRinging {
			if len(msg.Status.Relays) == 0 {
				m.footer.Status = statusNoRelay
			} else {
				m.footer.Status = statusIdle
			}
		}
		// Schedule the next poll. Always re-tick so the indicator
		// keeps tracking the daemon even after recoveries from errors.
		return m, tickDaemonStatusCmd(m.client)
	case errMsg:
		if msg.Source == "daemon.status" {
			m.footer.Status = statusDaemonDown
			m.footer.Detail = ""
			// Keep retrying so the footer recovers when the daemon
			// comes back.
			return m, tickDaemonStatusCmd(m.client)
		}
		m.footer.Status = statusError
		m.footer.Detail = msg.Source + ": " + msg.Err.Error()
	}
	return m, nil
}

// applyCallStateChange folds a call.StateChange event into the dock's
// state. Resolves the remote peer.ID to a friend name when possible
// (else short-form peer ID). Also pops the incoming-call modal on
// the inbound ringing transition.
func (m *Model) applyCallStateChange(sc call.StateChange) {
	if m.dock.state.CallID != "" && m.dock.state.CallID != sc.SessionID {
		// Different call — for v1 we only track one in the dock.
		// Multi-call support is explicitly out of scope.
		return
	}
	// Inbound ringing → modal (unless one is already open).
	if sc.State == "ringing" && sc.Direction == "inbound" && m.modal == nil {
		m.modal = newIncomingModal(incomingModalState{
			CallID: sc.SessionID,
			Caller: m.friendNameForPeer(sc.Remote),
			PeerID: sc.Remote.String(),
		})
	}
	s := callDockState{
		State:  sc.State,
		CallID: sc.SessionID,
		Remote: m.friendNameForPeer(sc.Remote),
		Reason: sc.Reason,
	}
	if sc.State == "connected" {
		s.StartedAt = sc.Time
	}
	// Preserve audio levels / mode if we already have them; the
	// per-second tick will refresh them.
	s.Mode = m.dock.state.Mode
	s.RxDB = m.dock.state.RxDB
	s.TxDB = m.dock.state.TxDB
	s.Muted = m.dock.state.Muted
	m.dock.SetState(s)

	// Sync footer indicator to match dock state.
	switch sc.State {
	case "ringing", "calling", "connecting":
		m.footer.Status = statusRinging
		m.footer.Detail = s.Remote
	case "connected":
		m.footer.Status = statusConnected
		m.footer.Detail = s.Remote
	case "ended":
		m.footer.Status = statusIdle
		m.footer.Detail = ""
	}
}

// handleModalClosed inspects the just-closed modal and returns any
// follow-up Cmd its Action implies (e.g. accept → calls.action accept).
// Returns nil for modals that don't dispatch IPC.
func (m *Model) handleModalClosed(prev modalView) tea.Cmd {
	switch mod := prev.(type) {
	case *incomingModal:
		switch mod.Action {
		case "accept":
			return callActionCmd(m.client, mod.state.CallID, "accept", "")
		case "decline":
			return callActionCmd(m.client, mod.state.CallID, "hangup", "declined")
		}
	case *quitModal:
		switch mod.Action {
		case "detach":
			return tea.Quit
		case "hangup":
			callID := m.dock.state.CallID
			return tea.Sequence(
				callActionCmd(m.client, callID, "hangup", "user requested"),
				tea.Quit,
			)
		}
	case *addFriendModal:
		if mod.Action == "redeem" && mod.CodeText() != "" {
			return redeemInviteCmd(m.client, mod.CodeText())
		}
		// "open" → focus the existing friend; v1 just falls through
		// (the friend is already in the sidebar). "cancel" / "" → no IPC.
	}
	return nil
}

// friendNameForPeer maps a peer.ID to its friend display name, or
// returns a short peer-ID fallback when the peer isn't (yet) a friend.
func (m *Model) friendNameForPeer(id peer.ID) string {
	for _, f := range m.friends.friends {
		if f.PeerID == id {
			return f.Name
		}
	}
	s := id.String()
	if len(s) > 12 {
		return s[:6] + "…" + s[len(s)-4:]
	}
	return s
}

// syncFocusFromSelection updates the focus pane to reflect whatever
// the friends sidebar currently has selected. Called after every
// selection-changing event.
func (m *Model) syncFocusFromSelection() {
	if sel, ok := m.friends.Selected(); ok {
		f := sel
		m.focus.SetFriend(&f)
	} else {
		m.focus.SetFriend(nil)
	}
}

// View renders the root layout: friends sidebar (left) + focused-friend
// pane (right) + status footer. The call dock lands in Task 13.
func (m Model) View() string {
	if m.width == 0 {
		return "" // initial frame before tea.WindowSizeMsg
	}
	// sidebarW / focusW are the CONTENT widths of each pane. lipgloss
	// renders a box.Width(N) at N+2 visible columns (1 char per side
	// for the rounded border), so the two panes together occupy
	// (sidebarW+2) + (focusW+2) = m.width — i.e. focusW = m.width-32.
	// Getting this wrong by even 1 column makes the terminal wrap the
	// overflow, shifting all content down a row and hiding the first
	// friend behind the sidebar's top border.
	sidebarW := 28
	focusW := m.width - sidebarW - 4
	if focusW < 20 {
		focusW = 20
	}
	// Same border-arithmetic as widths: box.Height(N) renders N+2
	// visible rows (top + bottom border). Subtract 3 from terminal
	// height to leave room for upper-pane borders (2) and the footer
	// (1). When the dock is active, also subtract its rendered height
	// — the dock view is plain text, no extra border rows.
	upperH := m.height - 3
	dockH := 0
	if m.dock.Active() {
		dockH = 6
		upperH -= dockH
	}
	if upperH < 5 {
		upperH = 5
	}
	left := m.friends.View(sidebarW, upperH, m.filtering, m.filterInput)
	right := m.focus.View(focusW, upperH)
	upper := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	footer := renderStatusFooter(m.footer)
	var bg string
	if dockH > 0 {
		dock := m.dock.View(m.width, dockH)
		bg = lipgloss.JoinVertical(lipgloss.Left, upper, dock, footer)
	} else {
		bg = lipgloss.JoinVertical(lipgloss.Left, upper, footer)
	}
	if m.modal != nil {
		return renderOverlay(bg, m.modal.View(m.width, m.height), m.width, m.height)
	}
	return bg
}

// modalView is the contract every modal honours. nil means no modal.
//
// View returns the rendered modal panel only — the parent Model
// composes it over the dimmed dashboard background via renderOverlay.
// Implementations should call theme.modalBox.Render(body) to get the
// double-bordered panel; do NOT call renderOverlay yourself.
type modalView interface {
	Update(msg tea.Msg) (modalView, tea.Cmd)
	View(width, height int) string
}
