package invite

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
)

// codeLength is the number of Crockford base-32 characters in a code,
// excluding the OPEN- prefix and dashes. Eight chars × 5 bits = 40 bits
// of entropy.
const codeLength = 8

// crockfordAlphabet is the canonical Crockford base-32 alphabet:
// no I, L, O, U.
const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Code is a canonicalized invite code: uppercase, no separators, exactly
// codeLength characters from the Crockford base-32 alphabet.
type Code string

// Generate returns a freshly generated random Code.
//
// Five random bytes encode to exactly 8 Crockford base-32 characters
// (5 × 8 / 5 = 8), giving 40 bits of uniformly distributed entropy.
func Generate() (Code, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}
	out := make([]byte, codeLength)
	bits := uint64(b[0])<<32 | uint64(b[1])<<24 | uint64(b[2])<<16 | uint64(b[3])<<8 | uint64(b[4])
	for i := 0; i < codeLength; i++ {
		shift := uint(35 - 5*i)
		idx := (bits >> shift) & 0x1F
		out[i] = crockfordAlphabet[idx]
	}
	return Code(out), nil
}

// Parse normalizes user input into a canonical Code.
//
// Accepted forms (case-insensitive, dashes optional, OPEN- prefix optional):
//   - "OPEN-A7B2-X9K4"
//   - "open-a7b2-x9k4"
//   - "A7B2X9K4"
//
// Crockford decode aliases ('O'/'o' → '0'; 'I'/'i'/'L'/'l' → '1') are
// applied so users typing the most common confused characters still get
// the right code.
func Parse(input string) (Code, error) {
	s := strings.ToUpper(strings.TrimSpace(input))
	s = strings.TrimPrefix(s, "OPEN-")
	s = strings.ReplaceAll(s, "-", "")

	if len(s) != codeLength {
		return "", fmt.Errorf("invite code must be %d characters (after stripping OPEN- and dashes); got %d", codeLength, len(s))
	}

	out := make([]byte, codeLength)
	for i, r := range s {
		switch r {
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		case 'U':
			return "", errors.New("invite code contains 'U' (not in Crockford base-32)")
		}
		if !strings.ContainsRune(crockfordAlphabet, r) {
			return "", fmt.Errorf("invite code character %q is not in Crockford base-32", r)
		}
		out[i] = byte(r)
	}
	return Code(out), nil
}

// Pretty returns the user-facing form of c: "OPEN-XXXX-XXXX".
func (c Code) Pretty() string {
	if len(c) != codeLength {
		return string(c)
	}
	return "OPEN-" + string(c[:4]) + "-" + string(c[4:])
}
