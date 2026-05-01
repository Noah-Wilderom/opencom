// Package audio implements opencom's media plane: real-time voice over
// libp2p QUIC datagrams. PCM capture (malgo) → AEC + NS + AGC
// (WebRTC AEC3 via livekit/echo-cancel) → Opus encode → RTP packetize →
// QUIC datagram. Reverse on the receiving side. Mute toggles and codec
// stats travel over a small reliable control stream
// (/opencom/audio-control/1.0.0); media never blocks on retransmits.
//
// Sessions are keyed by call ID; the Manager subscribes to call state
// changes and starts a session on [connected], stops on [ended].
//
// See docs/superpowers/specs/2026-05-01-opencom-m8-audio.md.
package audio
