//go:build unix

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// spawnDaemon starts a detached daemon child process. The child runs
// `<execPath> daemon start --background` and survives the parent's exit
// (Setsid detaches it from the parent's session/controlling terminal).
//
// stdout/stderr are redirected to logPath so the child can write its
// startup logs even though the parent has detached.
func spawnDaemon(execPath, logPath string) error {
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening daemon log file %s: %w", logPath, err)
	}
	cmd := exec.Command(execPath, "daemon", "start", "--background")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
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
