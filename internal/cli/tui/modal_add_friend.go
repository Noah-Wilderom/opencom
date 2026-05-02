// internal/cli/tui/modal_add_friend.go
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"opencom/internal/invite"
	"opencom/internal/ipc/methods"
)

// addFriendState reflects which of the three branches the modal is in.
type addFriendState int

const (
	addStateEmpty addFriendState = iota
	addStateNewPeer
	addStateDuplicate
)

// addFriendModal implements modalView. On Open, reads the clipboard
// and computes the appropriate state. Action records the user's
// choice; the parent Model's handleModalClosed switch on
// *addFriendModal dispatches the matching IPC.
type addFriendModal struct {
	state    addFriendState
	codeText string                   // raw clipboard or manual-paste content
	parsed   string                   // canonical form for redemption (URL or pretty Code)
	friend   methods.FriendsListEntry // populated only in stateDuplicate
	inviter  string                   // detected inviter display name (URL form) or short-code text
	Action   string                   // "" | "redeem" | "open" | "cancel"

	clipboardErr error
	manualEntry  string // when in stateEmpty, the user-typed paste
}

// newAddFriendModal builds the modal: reads the clipboard once,
// runs detection, and returns the populated modal. Detection calls
// FriendsList synchronously (context.Background) when an invite URL
// is present — this blocks the construction goroutine briefly but
// keeps the View deterministic.
func newAddFriendModal(cb Clipboard, client Client) *addFriendModal {
	m := &addFriendModal{state: addStateEmpty}
	contents, err := cb.Read()
	if err != nil {
		m.clipboardErr = err
		return m
	}
	m.applyDetection(contents, client)
	return m
}

// applyDetection inspects s (clipboard or typed) and sets state /
// parsed / friend / inviter accordingly. Falls back to addStateEmpty
// when no invite is found.
//
// URL-form invites carry an embedded peer ID, so we can pre-check
// duplicates against the friends list. Short-code invites (OPEN-XXXX)
// don't, so we accept them as new-peer and let invite.redeem return
// a duplicate error if the inviter is already a friend.
func (m *addFriendModal) applyDetection(s string, client Client) {
	s = strings.TrimSpace(s)
	if s == "" {
		m.state = addStateEmpty
		return
	}
	// URL form first — has embedded peer ID for duplicate check.
	if strings.HasPrefix(strings.ToLower(s), invite.URLPrefix) {
		p, err := invite.ParseURL(s)
		if err == nil && p.PeerID != "" {
			m.codeText = s
			m.parsed = s
			m.inviter = p.DisplayName
			if m.inviter == "" {
				m.inviter = "(unnamed)"
			}
			// Duplicate check: blocks briefly on FriendsList.
			if friends, lerr := client.FriendsList(context.Background()); lerr == nil {
				for _, f := range friends {
					if f.PeerID.String() == p.PeerID {
						m.state = addStateDuplicate
						m.friend = f
						return
					}
				}
			}
			m.state = addStateNewPeer
			return
		}
	}
	// Short code form — no embedded peer ID, can't pre-check duplicate.
	if c, err := invite.Parse(s); err == nil {
		m.codeText = s
		m.parsed = c.Pretty()
		m.inviter = "(short code)"
		m.state = addStateNewPeer
		return
	}
	// Nothing parseable.
	m.state = addStateEmpty
}

// Update handles key events. Returns nil to close the modal; the
// parent's handleModalClosed reads m.Action to dispatch IPC.
func (m *addFriendModal) Update(msg tea.Msg) (modalView, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			m.Action = "cancel"
			return nil, nil
		case "enter":
			switch m.state {
			case addStateNewPeer:
				m.Action = "redeem"
				return nil, nil
			case addStateDuplicate:
				m.Action = "open"
				return nil, nil
			case addStateEmpty:
				if m.manualEntry != "" {
					// Treat as pasted content. We don't re-run
					// applyDetection here because we don't carry a
					// reference to the Client — the parent's
					// handleModalClosed sees Action=="redeem" with
					// CodeText set and dispatches redeemInviteCmd.
					m.codeText = m.manualEntry
					m.parsed = m.manualEntry
					m.Action = "redeem"
					return nil, nil
				}
			}
		case "backspace":
			if m.state == addStateEmpty && len(m.manualEntry) > 0 {
				m.manualEntry = m.manualEntry[:len(m.manualEntry)-1]
			}
		}
		// Capture rune input only in stateEmpty (manual paste).
		if m.state == addStateEmpty && k.Type == tea.KeyRunes {
			m.manualEntry += string(k.Runes)
		}
	}
	return m, nil
}

// View returns the modal panel only — the parent Model composes the
// overlay via renderOverlay.
func (m *addFriendModal) View(width, height int) string {
	_ = width
	_ = height
	var b strings.Builder
	b.WriteString(theme.head.Render("Add friend"))
	b.WriteString("\n\n")
	switch m.state {
	case addStateNewPeer:
		fmt.Fprintf(&b, "%s found invite in clipboard\n\n", theme.ok.Render("✓"))
		fmt.Fprintf(&b, "Inviter:    %s\n", theme.head.Render(m.inviter))
		fmt.Fprintf(&b, "Code:       %s\n\n", m.parsed)
		fmt.Fprintf(&b, "[ %s add ]   [ %s cancel ]",
			theme.key.Render("enter"), theme.key.Render("esc"))
	case addStateDuplicate:
		fmt.Fprintf(&b, "%s already a friend\n\n", theme.warn.Render("⚠"))
		fmt.Fprintf(&b, "Clipboard contains an invite for %s.\n", theme.head.Render(m.friend.Name))
		fmt.Fprintf(&b, "(added %s)\n\n", m.friend.AddedAt.Format("2006-01-02"))
		fmt.Fprintf(&b, "[ %s open %s ]   [ %s close ]",
			theme.key.Render("enter"), m.friend.Name, theme.key.Render("esc"))
	default:
		// Empty state — manual paste form.
		fmt.Fprintf(&b, "Paste an opencom:// URL or OPEN-XXXX code:\n%s_\n\n",
			theme.dim.Render(m.manualEntry))
		if m.clipboardErr != nil {
			b.WriteString(theme.warn.Render("clipboard unavailable: " + m.clipboardErr.Error()))
		} else {
			b.WriteString(theme.dim.Render("clipboard checked — no invite found"))
		}
		b.WriteString("\n\n")
		fmt.Fprintf(&b, "[ %s add ]   [ %s cancel ]",
			theme.key.Render("enter"), theme.key.Render("esc"))
	}
	return theme.modalBox.Render(b.String())
}

// CodeText returns the raw invite text the user wants to redeem;
// model's handleModalClosed reads this when Action == "redeem".
func (m *addFriendModal) CodeText() string {
	if m.codeText != "" {
		return m.codeText
	}
	return m.manualEntry
}
