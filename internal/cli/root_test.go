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

// TestRootCmd_HelpSubcommandPrintsHelp confirms `opencom help`
// (Cobra's built-in subcommand) prints the same usage block that
// `opencom --help` does. We rely on Cobra's default registration —
// no explicit SetHelpCommand call needed — but the test pins the
// behaviour so a future "disable cobra default subcommands" change
// can't silently break the user-facing `opencom help` contract.
func TestRootCmd_HelpSubcommandPrintsHelp(t *testing.T) {
	t.Parallel()
	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"help"})
	assert.NoError(t, root.Execute())
	body := out.String()
	assert.Contains(t, body, "Usage:")
	assert.Contains(t, body, "Available Commands:")
}

func TestRootCmd_NoArgsRunsTUI(t *testing.T) {
	// NOT t.Parallel(): writes the package-level cli.RunTUIForTest var.
	// Any other test that wants to inject a fake TUI runner must also
	// run serially with this one.
	called := false
	cli.RunTUIForTest = func() error { called = true; return nil }
	t.Cleanup(func() { cli.RunTUIForTest = nil })

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{}) // no args
	assert.NoError(t, root.Execute())
	assert.True(t, called, "bare `opencom` should call into the TUI")
}
