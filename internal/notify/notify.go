// Package notify wraps cross-platform desktop notifications. The
// production Notifier delegates to gen2brain/beeep (DBus on Linux,
// terminal-notifier/osascript on macOS, toast on Windows). Tests and
// headless servers can substitute Disabled or a recording fake via the
// Notifier interface.
package notify

import (
	"github.com/gen2brain/beeep"
	"go.uber.org/zap"
)

// Notifier sends a desktop notification. Implementations must be
// best-effort: never return errors that would block the caller. On
// platforms or environments where notifications can't be delivered
// (no display server, no DBus, sandboxed), the Notify call should
// silently no-op.
type Notifier interface {
	Notify(title, body string)
}

// Beeep is the production Notifier. Errors from beeep — common on
// headless Linux without a session bus, and on systems where
// terminal-notifier isn't installed on macOS — are logged at debug
// level and never surfaced.
type Beeep struct {
	Log *zap.Logger
}

// Notify implements Notifier.
func (b Beeep) Notify(title, body string) {
	if err := beeep.Notify(title, body, nil); err != nil && b.Log != nil {
		b.Log.Debug("desktop notification failed",
			zap.String("title", title),
			zap.Error(err))
	}
}

// Disabled is a no-op Notifier. Inject it when ui.notifications=false
// (the relay/server profile written by setup-node.sh) so the daemon
// doesn't bother poking beeep on every call event.
type Disabled struct{}

// Notify implements Notifier.
func (Disabled) Notify(_, _ string) {}
