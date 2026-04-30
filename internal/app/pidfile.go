//go:build unix

package app

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// AcquirePIDFile creates a PID file at path holding the current PID. The
// returned release function removes the file. Errors if the file already
// exists and refers to a process that is currently running.
func AcquirePIDFile(path string) (func() error, error) {
	if data, err := os.ReadFile(path); err == nil {
		text := strings.TrimSpace(string(data))
		pid, perr := strconv.Atoi(text)
		// pid 0 is the process group on Unix; reject defensively so we
		// never accidentally signal it via processIsRunning.
		if perr == nil && pid > 0 && processIsRunning(pid) {
			return nil, fmt.Errorf("daemon already running with pid %d (pid file: %s)", pid, path)
		}
		// Stale or unparseable: remove and continue.
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("removing stale pid file: %w", err)
		}
	}

	// 0600 is umask-safe (only owner bits); no explicit Chmod needed.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("creating pid file: %w", err)
	}
	pid := os.Getpid()
	if _, err := fmt.Fprintf(f, "%d\n", pid); err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("writing pid file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("closing pid file: %w", err)
	}
	return func() error {
		err := os.Remove(path)
		if err != nil && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}, nil
}

// processIsRunning reports whether a process with the given PID exists.
// Uses signal 0 to test existence. EPERM means the process exists but is
// owned by another user — we treat that as "exists" so we never reclaim a
// PID file that points at a live process we don't own.
func processIsRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return errors.Is(err, syscall.EPERM)
	}
	return true
}
