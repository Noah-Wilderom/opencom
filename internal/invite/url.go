package invite

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// URLPrefix is the URL scheme + path prefix for opencom invite URLs.
const URLPrefix = "opencom://join"

// urlSigDomain is the domain-separator string mixed into the URL
// signature input. Including a version + protocol tag means a signature
// produced for one URL form can never be misinterpreted as a signature
// for any other opencom signed payload, even if the field set drifts.
const urlSigDomain = "opencom-invite-url-v1"

// Sentinel errors returned by ParseURL / VerifyURL.
var (
	ErrURLSignatureInvalid = errors.New("invite url signature invalid")
	ErrURLPubkeyMismatch   = errors.New("invite url public key does not match peer id")
)

// URLPayload mirrors what's encoded into / decoded from the URL form.
//
// Unlike the DHT-stored encrypted Record (which derives its encryption
// key from the code, so the code itself is the secret), the URL form
// is self-contained: the URL itself is the secret, and authenticity is
// guaranteed by the embedded ed25519 signature over all fields.
type URLPayload struct {
	PeerID      string
	PublicKey   string // base64(StdEncoding) of libp2p MarshalPublicKey output
	Addresses   []string
	DisplayName string
	Code        Code
	ExpiresAt   int64  // Unix seconds; matches Record.ExpiresAt
	Signature   string // base64(StdEncoding) ed25519 signature; empty before SignURL
}

// FormatURL builds the opencom://join?... URL form. Caller is expected
// to have populated Signature via SignURL first; an unsigned URL will
// fail VerifyURL on the redeemer side.
//
// Addresses are joined with '\n' and base64url-encoded so the URL stays
// compact (one short query parameter instead of a list per address).
func FormatURL(p URLPayload) string {
	addrEnc := encodeAddresses(p.Addresses)
	v := url.Values{}
	v.Set("p", p.PeerID)
	v.Set("k", p.PublicKey)
	v.Set("a", addrEnc)
	v.Set("n", p.DisplayName)
	v.Set("c", string(p.Code))
	v.Set("e", strconv.FormatInt(p.ExpiresAt, 10))
	if p.Signature != "" {
		v.Set("s", p.Signature)
	}
	return URLPrefix + "?" + v.Encode()
}

// ParseURL parses an opencom://join?... URL into its payload. The
// Signature field is populated but NOT verified — call VerifyURL to
// authenticate the payload before trusting it.
//
// Backwards-compatibility note: pre-self-contained URLs (no `k`/`e`/`s`
// fields) parse with empty PublicKey, zero ExpiresAt, empty Signature.
// VerifyURL will reject them, which is the intended behavior — they
// can no longer be redeemed.
func ParseURL(s string) (URLPayload, error) {
	var out URLPayload
	low := strings.ToLower(s)
	if !strings.HasPrefix(low, URLPrefix) {
		return out, fmt.Errorf("not an %s URL", URLPrefix)
	}
	idx := strings.IndexByte(s, '?')
	if idx < 0 {
		return out, errors.New("missing query string")
	}
	values, err := url.ParseQuery(s[idx+1:])
	if err != nil {
		return out, fmt.Errorf("parsing query: %w", err)
	}
	out.PeerID = values.Get("p")
	if out.PeerID == "" {
		return out, errors.New("missing required field 'p' (peer id)")
	}
	out.PublicKey = values.Get("k")
	addrEnc := values.Get("a")
	if addrEnc == "" {
		return out, errors.New("missing required field 'a' (addresses)")
	}
	out.Addresses, err = decodeAddresses(addrEnc)
	if err != nil {
		return out, err
	}
	out.DisplayName = values.Get("n")
	c, err := Parse(values.Get("c"))
	if err != nil {
		return out, fmt.Errorf("parsing code: %w", err)
	}
	out.Code = c
	if exp := values.Get("e"); exp != "" {
		out.ExpiresAt, err = strconv.ParseInt(exp, 10, 64)
		if err != nil {
			return out, fmt.Errorf("parsing expires_at: %w", err)
		}
	}
	out.Signature = values.Get("s")
	return out, nil
}

// SignURL fills in the Signature field by signing canonical bytes of
// p (excluding Signature itself) with signer's ed25519 key. Returns the
// updated payload by value.
func SignURL(p URLPayload, signer crypto.PrivKey) (URLPayload, error) {
	sig, err := signer.Sign(urlCanonicalBytes(p))
	if err != nil {
		return p, fmt.Errorf("signing url: %w", err)
	}
	p.Signature = base64.StdEncoding.EncodeToString(sig)
	return p, nil
}

// VerifyURL authenticates p: checks the embedded public key parses,
// checks PeerID is the libp2p peer-ID derived from PublicKey, and
// verifies Signature against the canonical bytes of all other fields.
//
// Returns the parsed libp2p public key on success — callers use it to
// dial the inviter and seed Noise/TLS.
func VerifyURL(p URLPayload) (crypto.PubKey, error) {
	if p.PublicKey == "" {
		return nil, fmt.Errorf("%w: missing public key", ErrURLSignatureInvalid)
	}
	if p.Signature == "" {
		return nil, fmt.Errorf("%w: missing signature", ErrURLSignatureInvalid)
	}
	pubBytes, err := base64.StdEncoding.DecodeString(p.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("decoding public key: %w", err)
	}
	pub, err := crypto.UnmarshalPublicKey(pubBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}
	derivedID, err := peer.IDFromPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("deriving peer id: %w", err)
	}
	if derivedID.String() != p.PeerID {
		return nil, ErrURLPubkeyMismatch
	}
	sig, err := base64.StdEncoding.DecodeString(p.Signature)
	if err != nil {
		return nil, fmt.Errorf("decoding signature: %w", err)
	}
	ok, err := pub.Verify(urlCanonicalBytes(p), sig)
	if err != nil || !ok {
		return nil, ErrURLSignatureInvalid
	}
	return pub, nil
}

// urlCanonicalBytes returns the deterministic byte sequence that the
// URL signature covers. Field order is fixed, length-prefixed (\n) so
// adjacent fields can't be confused, and prefixed with a domain
// separator. Signature itself is NOT included.
func urlCanonicalBytes(p URLPayload) []byte {
	var b strings.Builder
	b.WriteString(urlSigDomain)
	b.WriteByte('\n')
	b.WriteString("p=")
	b.WriteString(p.PeerID)
	b.WriteByte('\n')
	b.WriteString("k=")
	b.WriteString(p.PublicKey)
	b.WriteByte('\n')
	b.WriteString("a=")
	b.WriteString(encodeAddresses(p.Addresses))
	b.WriteByte('\n')
	b.WriteString("n=")
	b.WriteString(p.DisplayName)
	b.WriteByte('\n')
	b.WriteString("c=")
	b.WriteString(string(p.Code))
	b.WriteByte('\n')
	b.WriteString("e=")
	b.WriteString(strconv.FormatInt(p.ExpiresAt, 10))
	return []byte(b.String())
}

func encodeAddresses(addrs []string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(addrs, "\n")))
}

func decodeAddresses(enc string) ([]string, error) {
	blob, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return nil, fmt.Errorf("decoding addresses: %w", err)
	}
	if len(blob) == 0 {
		return nil, nil
	}
	return strings.Split(string(blob), "\n"), nil
}
