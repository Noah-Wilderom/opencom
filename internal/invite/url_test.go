package invite_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/invite"
)

func TestURL_RoundTrip(t *testing.T) {
	t.Parallel()
	p := invite.URLPayload{
		PeerID:      "12D3KooWAlice",
		Addresses:   []string{"/ip4/192.0.2.1/tcp/4001", "/ip4/192.0.2.1/udp/4001/quic-v1"},
		DisplayName: "Alice Smith",
		Code:        invite.Code("A7B2X9K4"),
	}
	s := invite.FormatURL(p)
	assert.True(t, strings.HasPrefix(s, "opencom://join?"), "got %q", s)

	out, err := invite.ParseURL(s)
	assert.NoError(t, err)
	assert.Equal(t, p, out)
}

func TestURL_AcceptsCaseInsensitiveScheme(t *testing.T) {
	t.Parallel()
	p := invite.URLPayload{
		PeerID: "12D3KooWAlice", Addresses: []string{"/ip4/1.2.3.4/tcp/4001"},
		DisplayName: "A", Code: invite.Code("A7B2X9K4"),
	}
	s := invite.FormatURL(p)
	upper := "OPENCOM://JOIN?" + s[len("opencom://join?"):]
	out, err := invite.ParseURL(upper)
	assert.NoError(t, err)
	assert.Equal(t, p, out)
}

func TestURL_RejectsWrongScheme(t *testing.T) {
	t.Parallel()
	_, err := invite.ParseURL("https://example.com/?p=x")
	assert.Error(t, err)
}

func TestURL_RejectsMissingFields(t *testing.T) {
	t.Parallel()
	_, err := invite.ParseURL("opencom://join?p=12D3&c=A7B2X9K4")
	assert.Error(t, err, "missing addresses (a=) should reject")

	_, err = invite.ParseURL("opencom://join?a=&c=A7B2X9K4")
	assert.Error(t, err, "empty peer id should reject")
}

func TestURL_RejectsBadCode(t *testing.T) {
	t.Parallel()
	_, err := invite.ParseURL("opencom://join?p=12D3&a=&n=A&c=BADCODE")
	assert.Error(t, err)
}
