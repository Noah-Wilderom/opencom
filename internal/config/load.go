package config

import (
	"errors"
	"fmt"
	"os"

	"go.yaml.in/yaml/v2"

	"opencom/internal/iox"
)

// Load reads cfg from path. If the file does not exist, returns
// (Default(), os.ErrNotExist) so the caller can bootstrap.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), err
		}
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}

	cfg := Default()
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes cfg to path atomically with mode 0600. Parent directories are
// created with mode 0700 if missing.
func Save(path string, cfg Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	return iox.AtomicWriteFile(path, data, 0o600, 0o700)
}
