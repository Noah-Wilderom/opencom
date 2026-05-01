package invite_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"opencom/internal/invite"
)

func TestStore_OpenAddGet(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.json")
	s, err := invite.OpenStore(path)
	assert.NoError(t, err)

	e := invite.Entry{
		Code:      invite.Code("A7B2X9K4"),
		ExpiresAt: time.Now().Add(30 * time.Minute),
		CreatedAt: time.Now(),
	}
	s.Add(e)
	got, ok := s.Get(e.Code)
	assert.True(t, ok)
	assert.Equal(t, e.Code, got.Code)
}

func TestStore_PersistsAcrossOpens(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.json")
	s1, err := invite.OpenStore(path)
	assert.NoError(t, err)
	c := invite.Code("A7B2X9K4")
	s1.Add(invite.Entry{Code: c, ExpiresAt: time.Now().Add(30 * time.Minute), CreatedAt: time.Now()})

	s2, err := invite.OpenStore(path)
	assert.NoError(t, err)
	_, ok := s2.Get(c)
	assert.True(t, ok)
}

func TestStore_MarkConsumed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.json")
	s, err := invite.OpenStore(path)
	assert.NoError(t, err)
	c := invite.Code("A7B2X9K4")
	s.Add(invite.Entry{Code: c, ExpiresAt: time.Now().Add(30 * time.Minute), CreatedAt: time.Now()})

	ok := s.MarkConsumed(c, "12D3KooWBob")
	assert.True(t, ok, "first MarkConsumed should succeed")
	ok = s.MarkConsumed(c, "12D3KooWEve")
	assert.False(t, ok, "second MarkConsumed should fail")

	got, _ := s.Get(c)
	assert.True(t, got.Consumed)
	assert.Equal(t, "12D3KooWBob", got.ConsumedBy)
}

func TestStore_MarkConsumed_RejectsUnknown(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.json")
	s, err := invite.OpenStore(path)
	assert.NoError(t, err)
	assert.False(t, s.MarkConsumed(invite.Code("NOTHING1"), "12D3KooWX"))
}

func TestStore_MarkConsumed_RejectsExpired(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.json")
	s, err := invite.OpenStore(path)
	assert.NoError(t, err)
	c := invite.Code("A7B2X9K4")
	s.Add(invite.Entry{Code: c, ExpiresAt: time.Now().Add(-1 * time.Minute), CreatedAt: time.Now()})
	assert.False(t, s.MarkConsumed(c, "12D3KooWBob"))
}

func TestStore_ActiveListFiltersConsumedAndExpired(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.json")
	s, err := invite.OpenStore(path)
	assert.NoError(t, err)

	now := time.Now()
	s.Add(invite.Entry{Code: "ACTIVE01", ExpiresAt: now.Add(30 * time.Minute), CreatedAt: now})
	s.Add(invite.Entry{Code: "EXPIRED1", ExpiresAt: now.Add(-1 * time.Minute), CreatedAt: now})
	s.Add(invite.Entry{Code: "CONSUMED", ExpiresAt: now.Add(30 * time.Minute), CreatedAt: now})
	s.MarkConsumed("CONSUMED", "12D3KooWPeer")

	active := s.ActiveList()
	assert.Len(t, active, 1)
	assert.Equal(t, invite.Code("ACTIVE01"), active[0].Code)
}

func TestStore_AllListIncludesConsumed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.json")
	s, err := invite.OpenStore(path)
	assert.NoError(t, err)
	now := time.Now()
	s.Add(invite.Entry{Code: "ACTIVE01", ExpiresAt: now.Add(30 * time.Minute), CreatedAt: now})
	s.Add(invite.Entry{Code: "CONSUMED", ExpiresAt: now.Add(30 * time.Minute), CreatedAt: now})
	s.MarkConsumed("CONSUMED", "12D3KooWPeer")

	all := s.AllList()
	assert.Len(t, all, 2)
}

func TestStore_Remove(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.json")
	s, err := invite.OpenStore(path)
	assert.NoError(t, err)
	c := invite.Code("A7B2X9K4")
	s.Add(invite.Entry{Code: c, ExpiresAt: time.Now().Add(30 * time.Minute), CreatedAt: time.Now()})
	s.Remove(c)
	_, ok := s.Get(c)
	assert.False(t, ok)
}
