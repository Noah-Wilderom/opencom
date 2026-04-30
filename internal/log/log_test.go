package log_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	openlog "opencom/internal/log"
)

func TestNew_WritesInfoMessageAtInfoLevel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	l := openlog.New("info", &buf)
	l.Info("hello", openlog.String("k", "v"))

	out := buf.String()
	assert.Contains(t, out, `"msg":"hello"`)
	assert.Contains(t, out, `"k":"v"`)
	assert.Contains(t, out, `"level":"info"`)
}

func TestNew_DropsDebugAtInfoLevel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	l := openlog.New("info", &buf)
	l.Debug("should-not-appear")

	assert.Equal(t, "", buf.String())
}

func TestNew_PassesDebugAtDebugLevel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	l := openlog.New("debug", &buf)
	l.Debug("appears")

	assert.Contains(t, buf.String(), "appears")
}

func TestNew_UnknownLevelDefaultsToInfo(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	l := openlog.New("flarp", &buf)
	l.Info("info-line")
	l.Debug("debug-line")

	out := buf.String()
	assert.Contains(t, out, "info-line")
	assert.False(t, strings.Contains(out, "debug-line"))
}

func TestNew_UnknownLevelEmitsWarning(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	openlog.New("flarp", &buf)

	out := buf.String()
	assert.Contains(t, out, `"level":"warn"`)
	assert.Contains(t, out, "unknown log level")
	assert.Contains(t, out, `"requested":"flarp"`)
}

func TestNew_KnownLevelDoesNotEmitWarning(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	openlog.New("info", &buf)

	assert.Equal(t, "", buf.String())
}
