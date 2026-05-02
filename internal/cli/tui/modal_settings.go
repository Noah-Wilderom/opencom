// internal/cli/tui/modal_settings.go
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"
)

// settingsModal is a pointer-style modal: it shows the path to
// config.yaml and offers to open it in $EDITOR. v1 deliberately
// avoids in-TUI editing — the file is short and changes rarely.
type settingsModal struct {
	path   string
	editor string
	exec   func(name string, args ...string) error // injected for tests
}

func newSettingsModal(path, editorOverride string) *settingsModal {
	ed := editorOverride
	if ed == "" {
		ed = pickEditor()
	}
	return &settingsModal{
		path:   path,
		editor: ed,
		exec: func(name string, args ...string) error {
			c := exec.Command(name, args...)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			return c.Run()
		},
	}
}

// pickEditor walks $EDITOR, $VISUAL, then platform fallbacks.
func pickEditor() string {
	for _, env := range []string{"EDITOR", "VISUAL"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	for _, cand := range []string{"vim", "vi", "nano"} {
		if _, err := exec.LookPath(cand); err == nil {
			return cand
		}
	}
	if runtime.GOOS == "windows" {
		return "notepad"
	}
	return "vi"
}

func (m *settingsModal) Update(msg tea.Msg) (modalView, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "o":
			_ = m.exec(m.editor, m.path)
			return nil, nil
		case "esc":
			return nil, nil
		}
	}
	return m, nil
}

func (m *settingsModal) View(width, height int) string {
	_ = width
	_ = height
	body := fmt.Sprintf(
		"Settings live in %s.\nAfter editing, run `opencom daemon restart` to apply.\n\n[ %s open in $EDITOR ]   [ %s close ]",
		m.path, theme.key.Render("o"), theme.key.Render("esc"),
	)
	return theme.modalBox.Render(body)
}
