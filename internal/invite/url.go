package invite

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// URLPrefix is the URL scheme + path prefix for opencom invite URLs.
const URLPrefix = "opencom://join"

// URLPayload mirrors what's encoded into / decoded from the URL form.
type URLPayload struct {
	PeerID      string
	Addresses   []string
	DisplayName string
	Code        Code
}

// FormatURL builds the opencom://join?... URL form.
//
// Addresses are joined with '\n' and base64url-encoded so the URL stays
// compact (one short query parameter instead of a list per address).
func FormatURL(p URLPayload) string {
	addrBlob := strings.Join(p.Addresses, "\n")
	addrEnc := base64.RawURLEncoding.EncodeToString([]byte(addrBlob))
	v := url.Values{}
	v.Set("p", p.PeerID)
	v.Set("a", addrEnc)
	v.Set("n", p.DisplayName)
	v.Set("c", string(p.Code))
	return URLPrefix + "?" + v.Encode()
}

// ParseURL parses an opencom://join?... URL into its payload.
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
	q := s[idx+1:]
	values, err := url.ParseQuery(q)
	if err != nil {
		return out, fmt.Errorf("parsing query: %w", err)
	}
	out.PeerID = values.Get("p")
	if out.PeerID == "" {
		return out, errors.New("missing required field 'p' (peer id)")
	}
	addrEnc := values.Get("a")
	if addrEnc == "" {
		return out, errors.New("missing required field 'a' (addresses)")
	}
	addrBlob, err := base64.RawURLEncoding.DecodeString(addrEnc)
	if err != nil {
		return out, fmt.Errorf("decoding addresses: %w", err)
	}
	out.Addresses = strings.Split(string(addrBlob), "\n")
	out.DisplayName = values.Get("n")
	codeStr := values.Get("c")
	c, err := Parse(codeStr)
	if err != nil {
		return out, fmt.Errorf("parsing code: %w", err)
	}
	out.Code = c
	return out, nil
}
