// internal/cli/tui/modal.go
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderOverlay places fg centered over bg. Background lines are
// dimmed; foreground replaces the corresponding rectangle of cells.
//
// Both bg and fg are pre-rendered, possibly with ANSI escapes. The
// overlay treats them as plain runes for splicing — colours are
// preserved within each line because lipgloss styles wrap their own
// content; we only blank-pad based on visible width.
func renderOverlay(bg, fg string, width, height int) string {
	bgLines := strings.Split(bg, "\n")
	for len(bgLines) < height {
		bgLines = append(bgLines, "")
	}
	fgLines := strings.Split(fg, "\n")
	mh := len(fgLines)
	mw := lipgloss.Width(fg)
	top := (height - mh) / 2
	if top < 0 {
		top = 0
	}
	left := (width - mw) / 2
	if left < 0 {
		left = 0
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#484f58"))
	out := make([]string, len(bgLines))
	for i, line := range bgLines {
		// Strip ANSI BEFORE measuring/slicing so visible-column
		// indices match the underlying rune positions.
		plain := stripANSI(line)
		plainRunes := []rune(plain)
		// Pad to full width so the splice arithmetic is uniform.
		if len(plainRunes) < width {
			plainRunes = append(plainRunes, []rune(strings.Repeat(" ", width-len(plainRunes)))...)
		}
		// Outside the modal's vertical band: dim the whole row.
		if i < top || i >= top+mh {
			out[i] = dim.Render(string(plainRunes[:width]))
			continue
		}
		// Inside the modal band: dim the leading + trailing portions
		// (whatever the modal panel doesn't cover) and splice in the
		// pre-styled fg row in the middle. The fg row keeps its own
		// ANSI styling intact since we never touch it.
		fl := fgLines[i-top]
		flW := lipgloss.Width(fl)
		end := left + flW
		if end > width {
			end = width
		}
		leading := dim.Render(string(plainRunes[:left]))
		trailing := dim.Render(string(plainRunes[end:width]))
		out[i] = leading + fl + trailing
	}
	return strings.Join(out, "\n")
}

// padRight pads s with spaces on the right to width w (visible
// columns). Returns s unchanged if it already meets or exceeds w.
func padRight(s string, w int) string {
	pad := w - lipgloss.Width(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// stripANSI removes ANSI CSI escape sequences (ESC '[' ... finalByte)
// so renderOverlay can splice without ANSI artefacts crossing
// boundaries. Final bytes are 0x40–0x7e per ECMA-48; the `[`
// immediately after ESC is the introducer, not a terminator.
func stripANSI(s string) string {
	const (
		stateText = iota
		stateEsc
		stateCSI
	)
	var b strings.Builder
	state := stateText
	for _, r := range s {
		switch state {
		case stateText:
			if r == 0x1b {
				state = stateEsc
				continue
			}
			b.WriteRune(r)
		case stateEsc:
			if r == '[' {
				state = stateCSI
				continue
			}
			// Non-CSI escape; treat as a single-byte sequence.
			state = stateText
		case stateCSI:
			if r >= 0x40 && r <= 0x7e {
				state = stateText
			}
		}
	}
	return b.String()
}
