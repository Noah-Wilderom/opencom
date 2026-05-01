package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
)

// Paths bundles all file paths opencom uses.
type Paths struct {
	ConfigDir   string
	StateDir    string
	RuntimeDir  string
	ConfigFile  string
	PrivateKey  string
	FriendsFile string
	SocketPath  string
	Peerstore     string
	LogFile       string
	PeerCache     string
	ActiveInvites string
}

const (
	dirName           = "opencom"
	configFileName    = "config.yaml"
	keyFileName       = "priv.key"
	friendsName       = "friends.json"
	peerstoreName     = "peerstore"
	logFileName       = "daemon.log"
	socketFileName    = "opencom.sock"
	peerCacheName     = "peer-cache.json"
	activeInvitesName = "active-invites.json"
)

// DefaultPaths populates Paths using OS conventions. Errors only when the
// user's home directory cannot be determined.
func DefaultPaths() (Paths, error) {
	cfgRoot, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("locating user config dir: %w", err)
	}

	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("locating user cache dir: %w", err)
	}

	stateRoot, err := userStateDir()
	if err != nil {
		return Paths{}, fmt.Errorf("locating user state dir: %w", err)
	}

	configDir := filepath.Join(cfgRoot, dirName)
	stateDir := filepath.Join(stateRoot, dirName)

	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")

	socketPath := socketPathFor(runtimeDir, cacheRoot)

	return Paths{
		ConfigDir:   configDir,
		StateDir:    stateDir,
		RuntimeDir:  runtimeDir,
		ConfigFile:  filepath.Join(configDir, configFileName),
		PrivateKey:  filepath.Join(configDir, keyFileName),
		FriendsFile: filepath.Join(configDir, friendsName),
		SocketPath:  socketPath,
		Peerstore:   filepath.Join(stateDir, peerstoreName),
		LogFile:     filepath.Join(stateDir, logFileName),
		PeerCache:     filepath.Join(stateDir, peerCacheName),
		ActiveInvites: filepath.Join(stateDir, activeInvitesName),
	}, nil
}

// socketPathFor decides where the IPC socket / pipe lives. Windows uses a
// named pipe URL-style path; Unix uses XDG_RUNTIME_DIR (or a cache fallback).
func socketPathFor(runtimeDir, cacheRoot string) string {
	if runtime.GOOS == "windows" {
		username := "user"
		if u, err := user.Current(); err == nil && u.Username != "" {
			username = filepath.Base(u.Username)
		}
		return `\\.\pipe\opencom-` + username
	}
	if runtimeDir != "" {
		return filepath.Join(runtimeDir, socketFileName)
	}
	return filepath.Join(cacheRoot, dirName, socketFileName)
}

// userStateDir returns the user's state dir, mirroring stdlib's
// UserConfigDir/UserCacheDir but for state. The stdlib does not yet expose
// this, so we implement the XDG/macOS/Windows conventions here.
func userStateDir() (string, error) {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return v, nil
	}
	switch runtime.GOOS {
	case "windows":
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return v, nil
		}
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state"), nil
}
