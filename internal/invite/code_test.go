package invite_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/invite"
)

func TestGenerate_LengthAndCharset(t *testing.T) {
	t.Parallel()

	c, err := invite.Generate()
	assert.NoError(t, err)
	assert.Equal(t, 8, len(string(c)))
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	for _, r := range string(c) {
		assert.True(t, strings.ContainsRune(alphabet, r), "char %q must be in Crockford base-32", r)
	}
}

func TestGenerate_RandomEnough(t *testing.T) {
	t.Parallel()

	seen := make(map[invite.Code]bool)
	for i := 0; i < 100; i++ {
		c, err := invite.Generate()
		assert.NoError(t, err)
		assert.False(t, seen[c], "duplicate code in 100 generations: %s", c)
		seen[c] = true
	}
}

func TestParse_AcceptsCanonical(t *testing.T) {
	t.Parallel()

	c, err := invite.Parse("OPEN-A7B2-X9K4")
	assert.NoError(t, err)
	assert.Equal(t, invite.Code("A7B2X9K4"), c)
}

func TestParse_AcceptsLowercase(t *testing.T) {
	t.Parallel()

	c, err := invite.Parse("open-a7b2-x9k4")
	assert.NoError(t, err)
	assert.Equal(t, invite.Code("A7B2X9K4"), c)
}

func TestParse_AcceptsBareEightChar(t *testing.T) {
	t.Parallel()

	c, err := invite.Parse("A7B2X9K4")
	assert.NoError(t, err)
	assert.Equal(t, invite.Code("A7B2X9K4"), c)
}

func TestParse_DecodesCrockfordAliases(t *testing.T) {
	t.Parallel()

	// Crockford decode: 'O'/'o' → '0', 'I'/'i'/'L'/'l' → '1'.
	c, err := invite.Parse("OPEN-OOOO-IIII")
	assert.NoError(t, err)
	assert.Equal(t, invite.Code("00001111"), c)
}

func TestParse_RejectsBadLength(t *testing.T) {
	t.Parallel()

	_, err := invite.Parse("OPEN-A7B2")
	assert.Error(t, err)
	_, err = invite.Parse("OPEN-A7B2-X9K4-EXTRA")
	assert.Error(t, err)
	_, err = invite.Parse("")
	assert.Error(t, err)
}

func TestParse_RejectsBadChars(t *testing.T) {
	t.Parallel()

	_, err := invite.Parse("OPEN-A7B2-X9U4")
	assert.Error(t, err)
	_, err = invite.Parse("OPEN-A7B2-X9@4")
	assert.Error(t, err)
}

func TestPretty_RoundTrip(t *testing.T) {
	t.Parallel()

	c, err := invite.Parse("OPEN-A7B2-X9K4")
	assert.NoError(t, err)
	assert.Equal(t, "OPEN-A7B2-X9K4", c.Pretty())
}
