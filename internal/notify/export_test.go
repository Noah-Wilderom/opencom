package notify

import "opencom/internal/call"

// FormatNotificationForTest exposes the internal formatNotification so
// tests in the _test package can assert on the title/body it produces
// without going through the goroutine plumbing of WatchCalls.
func FormatNotificationForTest(ev call.StateChange, names FriendNamer) (string, string) {
	return formatNotification(ev, names)
}
