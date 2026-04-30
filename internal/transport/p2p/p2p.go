// Package p2p implements the libp2p host that the opencom daemon uses for
// peer discovery, encrypted transport, and (in M4+) media streaming.
//
// The package is structured so that a Host can be constructed with mDNS
// discovery, the IPFS-public Kademlia DHT, persistent peerstore caching,
// and a connection notifier — all toggleable via HostOptions.
package p2p
