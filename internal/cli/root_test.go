package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/cli"
)

func TestVersion_PrintsVersionLine(t *testing.T) {
	t.Parallel()

	root := cli.NewRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{"version"})

	assert.NoError(t, root.Execute())
	out := stdout.String()
	assert.Contains(t, out, "opencom")
}

func TestRoot_HelpDescribesProject(t *testing.T) {
	t.Parallel()

	root := cli.NewRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{"--help"})

	assert.NoError(t, root.Execute())
	out := strings.ToLower(stdout.String())
	assert.Contains(t, out, "opencom")
	assert.Contains(t, out, "peer-to-peer")
}

func TestUnknownCommand_Errors(t *testing.T) {
	t.Parallel()

	root := cli.NewRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"nope-not-a-command"})

	err := root.Execute()
	assert.Error(t, err)
}
