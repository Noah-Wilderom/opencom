// Package iox holds tiny filesystem I/O helpers shared across opencom.
package iox

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteFile writes data to path with the given file mode by writing to
// a temp file in the same directory and renaming it into place. Parent
// directories are created with the given dirMode if missing. The temp file
// is chmod'd before content is written; if any step before the rename fails,
// the temp file is removed.
func AtomicWriteFile(path string, data []byte, mode, dirMode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return fmt.Errorf("creating parent dir for %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := os.Chmod(tmpName, mode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming temp file to %s: %w", path, err)
	}
	return nil
}
