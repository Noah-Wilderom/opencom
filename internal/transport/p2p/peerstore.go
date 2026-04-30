package p2p

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	ma "github.com/multiformats/go-multiaddr"

	"opencom/internal/iox"
)

const peerstoreFileName = "peerstore.json"

type peerstoreEntry struct {
	PeerID peer.ID  `json:"peer_id"`
	Addrs  []string `json:"addrs"`
}

// SavePeerstore writes h's known peers and their addresses to
// dir/peerstore.json. Excludes our own peer ID. Mode 0600.
func SavePeerstore(h *Host, dir string) error {
	ps := h.HostInternal().Peerstore()
	self := h.ID()

	out := make([]peerstoreEntry, 0, 16)
	for _, id := range ps.Peers() {
		if id == self {
			continue
		}
		addrs := ps.Addrs(id)
		if len(addrs) == 0 {
			continue
		}
		strs := make([]string, 0, len(addrs))
		for _, a := range addrs {
			strs = append(strs, a.String())
		}
		out = append(out, peerstoreEntry{PeerID: id, Addrs: strs})
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling peerstore: %w", err)
	}
	data = append(data, '\n')
	return iox.AtomicWriteFile(filepath.Join(dir, peerstoreFileName), data, 0o600, 0o700)
}

// LoadPeerstore reads dir/peerstore.json (if present) and re-adds the
// known peer addresses to h's libp2p peerstore.
//
// The on-disk format has no staleness bound: cached addresses are loaded
// with PermanentAddrTTL regardless of age. Stale entries are harmless —
// failed dials are absorbed by libp2p, and mDNS/DHT republish fresh
// addresses on first contact. M4+ may grow this with a saved-at
// timestamp + max-age check if cache pollution becomes measurable.
func LoadPeerstore(h *Host, dir string) error {
	path := filepath.Join(dir, peerstoreFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", path, err)
	}
	var entries []peerstoreEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	ps := h.HostInternal().Peerstore()
	for _, e := range entries {
		addrs := make([]ma.Multiaddr, 0, len(e.Addrs))
		for _, s := range e.Addrs {
			m, err := ma.NewMultiaddr(s)
			if err != nil {
				continue // skip unparseable; not fatal
			}
			addrs = append(addrs, m)
		}
		if len(addrs) > 0 {
			ps.AddAddrs(e.PeerID, addrs, peerstore.PermanentAddrTTL)
		}
	}
	return nil
}
