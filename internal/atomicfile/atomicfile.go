// Package atomicfile replaces files so readers see either the old contents or
// the new, never a partial write: write a temp file in the same directory,
// fsync it, then rename it over the target.
package atomicfile

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Write atomically replaces path with data. The parent directory must exist.
func Write(path string, data []byte, perm fs.FileMode) error {
	return WriteFunc(path, perm, func(w io.Writer) error {
		_, err := io.Copy(w, bytes.NewReader(data))
		return err
	})
}

// WriteFunc atomically replaces path with whatever write produces, such as an
// encoder streaming into w. If write or any later step fails, path is left
// as it was and the temp file is removed. The parent directory must exist.
func WriteFunc(path string, perm fs.FileMode, write func(w io.Writer) error) (err error) {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	// The temp file must be in the same directory: rename is only atomic
	// within one filesystem.
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close() // may already be closed; the error is irrelevant
			_ = os.Remove(tmp.Name())
		}
	}()

	if err := write(tmp); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// CreateTemp uses 0600; set the requested mode before the file is
	// visible under its real name.
	if err := tmp.Chmod(perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// Flush to disk before the rename, so a crash can't leave the new name
	// pointing at empty or partial contents.
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// The rename itself is durable only once the directory is synced.
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("write %s: sync directory: %w", path, err)
	}
	return nil
}
