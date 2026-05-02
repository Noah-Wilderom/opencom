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

// Beeep is the production Notifier. Successful notifies log at info;
// failures log at warn so users can see why notifications aren't
// appearing without flipping the daemon's log level.
type Beeep struct {
	Log *zap.Logger
}

// Notify implements Notifier.
func (b Beeep) Notify(title, body string) {
	err := beeep.Notify(title, body, nil)
	if b.Log == nil {
		return
	}
	if err != nil {
		b.Log.Warn("desktop notification failed",
			zap.String("title", title),
			zap.String("body", body),
			zap.Error(err))
		return
	}
	b.Log.Info("desktop notification sent",
		zap.String("title", title),
		zap.String("body", body))
}

// Disabled is a no-op Notifier. Inject it when ui.notifications=false
// (the relay/server profile written by setup-node.sh) so the daemon
// doesn't bother poking beeep on every call event.
type Disabled struct{}

// Notify implements Notifier.
func (Disabled) Notify(_, _ string) {}
