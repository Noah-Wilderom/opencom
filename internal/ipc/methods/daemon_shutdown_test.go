package methods_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"opencom/internal/ipc/methods"
)

func TestDaemonShutdown_RespondsAndCancelsContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := methods.DaemonShutdown(cancel)

	out, err := h(context.Background(), nil)
	assert.NoError(t, err)

	raw, _ := json.Marshal(out)
	var got map[string]string
	assert.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "shutting down", got["status"])

	// Cancellation is scheduled ~50ms after the response. Wait up to 500ms.
	select {
	case <-ctx.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ctx was not canceled within 500ms")
	}
}
