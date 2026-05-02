package audio

import "context"

// DatagramConnForTest is the test-facing alias for the unexported
// datagramConn interface. Test packages implement this to feed a
// fake conn into AttachFakeDatagramForTest.
type DatagramConnForTest interface {
	SendDatagram([]byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
}

// AttachFakeDatagramForTest forces the given Transport to attach the
// supplied DatagramConnForTest as its datagram fast-path, bypassing
// the watcher. Used by transport_test to verify that MediaMode flips
// correctly when a datagram conn becomes available — the libp2p-level
// "second connection appears mid-call" scenario is too uncooperative
// to simulate end-to-end in a unit test.
//
// Panics if t isn't a *libp2pTransport (caller's responsibility).
func AttachFakeDatagramForTest(t Transport, dc DatagramConnForTest) {
	lt := t.(*libp2pTransport)
	lt.dconnMu.Lock()
	lt.dconn = dc
	lt.dconnMu.Unlock()
}
