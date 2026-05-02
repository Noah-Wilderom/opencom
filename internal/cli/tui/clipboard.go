// internal/cli/tui/clipboard.go
package tui

import (
	"errors"
	"sync"

	"golang.design/x/clipboard"
)

// Clipboard is the OS-clipboard surface the TUI uses. Production
// code uses defaultClipboard (golang.design/x/clipboard); tests
// inject FakeClipboard.
type Clipboard interface {
	Read() (string, error)
	Write(s string) error
}

var (
	clipboardInitOnce sync.Once
	clipboardInitErr  error
)

// defaultClipboard delegates to golang.design/x/clipboard. Init is
// lazy because the library can fail on headless Linux without a
// display server; failure surfaces only when the user opens a modal
// that needs clipboard access.
type defaultClipboard struct{}

// DefaultClipboard returns the production OS-clipboard reader/writer.
// Lazily initialises golang.design/x/clipboard on first use.
func DefaultClipboard() Clipboard { return defaultClipboard{} }

func (defaultClipboard) Read() (string, error) {
	clipboardInitOnce.Do(func() { clipboardInitErr = clipboard.Init() })
	if clipboardInitErr != nil {
		return "", clipboardInitErr
	}
	b := clipboard.Read(clipboard.FmtText)
	return string(b), nil
}

func (defaultClipboard) Write(s string) error {
	clipboardInitOnce.Do(func() { clipboardInitErr = clipboard.Init() })
	if clipboardInitErr != nil {
		return clipboardInitErr
	}
	clipboard.Write(clipboard.FmtText, []byte(s))
	return nil
}

// FakeClipboard is a Clipboard for tests.
type FakeClipboard struct {
	Contents string
	Err      error
}

func (f *FakeClipboard) Read() (string, error) {
	if f.Err != nil {
		return "", f.Err
	}
	return f.Contents, nil
}

func (f *FakeClipboard) Write(s string) error {
	if f.Err != nil {
		return f.Err
	}
	f.Contents = s
	return nil
}

// errClipboardUnavailable surfaces when the platform refuses
// clipboard access. Callers (e.g. add-friend modal) fall back to
// manual paste.
var errClipboardUnavailable = errors.New("clipboard unavailable")
