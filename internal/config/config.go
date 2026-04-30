package config

import "os"

// CONFIG_DIR is retained for backwards compatibility with code written before
// Paths existed. New code should call DefaultPaths().
//
// Deprecated: use DefaultPaths().ConfigDir.
var CONFIG_DIR = ".config/"

// ConfigDir returns the user's OS config directory (without the opencom
// segment). Use DefaultPaths().ConfigDir for the opencom-specific dir.
//
// Deprecated: use DefaultPaths().ConfigDir.
func ConfigDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		panic(err)
	}
	return dir
}
