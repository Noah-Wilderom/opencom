package config

import (
	"os"
	"path"
)

var (
	CONFIG_DIR = ".config/"
)

func ConfigDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		panic(err)
	}

	return path.Join(dir, "opencom")
}
