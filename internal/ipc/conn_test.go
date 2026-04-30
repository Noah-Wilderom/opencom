package ipc_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/ipc"
)

func TestConnFromContext_NilWhenAbsent(t *testing.T) {
	t.Parallel()

	conn := ipc.ConnFromContext(context.Background())
	assert.Nil(t, conn)
}
