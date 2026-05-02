package methods_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/friends"
	"opencom/internal/identity"
	"opencom/internal/invite"
	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
	"opencom/internal/transport/p2p"
)

// newInviteMgrForTest constructs a minimal *invite.Manager that can
// successfully run Create() and MarkRedeemedForTest() — i.e. a real
// p2p host, a fake DHT, an in-memory friends sink, and a temp-dir
// invite store. The Manager is started so the stream handler is
// registered (cheap, harmless for this test which never opens a
// stream against it).
func newInviteMgrForTest(t *testing.T) *invite.Manager {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	kp, err := identity.Generate()
	assert.NoError(t, err)

	host, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kp.Priv})
	assert.NoError(t, err)
	t.Cleanup(func() { host.Close() })

	store, err := invite.OpenStore(filepath.Join(t.TempDir(), "invites.json"))
	assert.NoError(t, err)

	mgr, err := invite.NewManager(invite.ManagerOptions{
		Host:        host,
		DHT:         noopDHTForInviteSubTest{},
		Friends:     fakeFriendsSinkForInviteSubTest{},
		Store:       store,
		Identity:    kp.Priv,
		IdentityPub: kp.Pub,
		Log:         zap.NewNop(),
		DisplayName: "alice",
	})
	assert.NoError(t, err)
	mgr.Start()
	t.Cleanup(func() { mgr.Stop() })
	return mgr
}

// noopDHTForInviteSubTest satisfies discovery.DHT with no-op behaviour.
// Create's best-effort publish silently succeeds; SubscribeRedemption
// never goes through the DHT path so Get/Put are never exercised.
type noopDHTForInviteSubTest struct{}

func (noopDHTForInviteSubTest) PutValue(_ context.Context, _ string, _ []byte, _ ...routing.Option) error {
	return nil
}
func (noopDHTForInviteSubTest) GetValue(_ context.Context, _ string, _ ...routing.Option) ([]byte, error) {
	return nil, assert.AnError
}

type fakeFriendsSinkForInviteSubTest struct{}

func (fakeFriendsSinkForInviteSubTest) Add(_ friends.Friend) error { return nil }

func TestInviteSubscribeRedemption_FiresOnRedeem(t *testing.T) {
	t.Parallel()

	mgr := newInviteMgrForTest(t)
	sock := startCallsServer(t, context.Background(), func(s *ipc.Server) {
		s.Register("invite.subscribe_redemption", methods.InviteSubscribeRedemption(mgr))
	})

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()

	// Create + immediately mark-redeemed an invite to trigger the event.
	created, err := mgr.Create(context.Background(), time.Minute)
	assert.NoError(t, err)
	sub, err := c.Subscribe(context.Background(), "invite.subscribe_redemption",
		methods.InviteSubscribeRedemptionParams{Code: string(created.Code)})
	assert.NoError(t, err)
	defer sub.Close()

	other, _ := identity.Generate()
	mgr.MarkRedeemedForTest(string(created.Code), other.PeerID)

	select {
	case ev := <-sub.Events:
		var got methods.InviteRedemptionEvent
		assert.NoError(t, json.Unmarshal(ev.Data, &got))
		assert.Equal(t, string(created.Code), got.Code)
		assert.Equal(t, other.PeerID.String(), got.RedeemedBy)
	case <-time.After(2 * time.Second):
		t.Fatal("no redemption event delivered")
	}
}
