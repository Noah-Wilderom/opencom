// Package invite implements opencom's onboarding flow: short invite
// codes resolved via the opencom DHT mesh (protocol prefix
// /opencom/kad/1.0.0 — see internal/transport/p2p/host.go), a long-URL
// fallback for embedding peer ID + addresses directly, an on-disk
// active-invites store, and the /opencom/invite/1.0.0 libp2p stream
// protocol that completes a bidirectional friend-handshake.
//
// See docs/superpowers/specs/2026-05-01-opencom-m7-invite-codes.md.
package invite
