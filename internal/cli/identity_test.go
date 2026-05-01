package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/cli"
	"opencom/internal/config"
	"opencom/internal/identity"
)

func runInitForTest(t *testing.T, name string) {
	t.Helper()
	startDaemonStub(t)
	root := cli.NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"init", "--name=" + name, "--no-prompt"})
	assert.NoError(t, root.Execute())
}

func TestIdentityShow_PrintsPeerIDAndName(t *testing.T) {
	withTempPaths(t)
	runInitForTest(t, "Alice")

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"identity", "show"})
	assert.NoError(t, root.Execute())

	p, err := config.DefaultPaths()
	assert.NoError(t, err)
	kp, err := identity.Load(p.PrivateKey)
	assert.NoError(t, err)

	body := out.String()
	assert.Contains(t, body, kp.PeerID.String())
	assert.Contains(t, body, "Alice")
}

func TestIdentityShow_ErrorsWhenNotInitialised(t *testing.T) {
	withTempPaths(t)

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"identity", "show"})

	assert.Error(t, root.Execute())
}

func TestIdentityExport_WritesYAMLAtPath(t *testing.T) {
	withTempPaths(t)
	runInitForTest(t, "Alice")

	dest := filepath.Join(t.TempDir(), "alice.pub.key")

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"identity", "export", dest})
	assert.NoError(t, root.Execute())

	_, err := os.Stat(dest)
	assert.NoError(t, err)

	id, err := identity.ReadExport(dest)
	assert.NoError(t, err)
	assert.Equal(t, "Alice", id.Name)
}

func TestIdentityExport_RequiresPathArg(t *testing.T) {
	withTempPaths(t)
	runInitForTest(t, "Alice")

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"identity", "export"})

	assert.Error(t, root.Execute())
}
