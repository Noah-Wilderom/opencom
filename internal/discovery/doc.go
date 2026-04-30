// Package discovery implements opencom's WAN peer-discovery layer:
// per-friend encrypted DHT records that map peer IDs to current
// addresses, plus a cache + DHT-fallback resolver and a periodic
// publisher. See docs/superpowers/specs/2026-04-30-opencom-m5-wan-discovery.md.
package discovery
