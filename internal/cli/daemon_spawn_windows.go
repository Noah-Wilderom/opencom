//go:build windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

const (
	// Windows process creation flags; see CreateProcess docs.
	createNoWindow        = 0x08000000
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// spawnDaemon starts a detached, headless daemon child process. The
// child runs `<execPath> daemon start --background` with no console
// window and detached from the parent's process group.
func spawnDaemon(execPath, logPath string) error {
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening daemon log file %s: %w", logPath, err)
	}
	cmd := exec.Command(execPath, "daemon", "start", "--background")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | createNoWindow | createNewProcessGroup,
	}
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("starting daemon: %w", err)
	}
	_ = logFile.Close()
	_ = cmd.Process.Release()
	return nil
}
