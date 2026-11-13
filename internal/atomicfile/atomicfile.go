package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Write atomically replaces path with data. The parent directory must exist.
func Write(path string, data []byte, perm fs.FileMode) error {
	return WriteFunc(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// WriteFunc atomically replaces path with whatever write produces, such as an
// encoder streaming into w. If write or any later step fails, path is left
// as it was and the temp file is removed. The parent directory must exist.
//
// If path is a symlink, the file it points to is replaced and the link is
// kept, so a config file linked from a dotfiles repo stays linked.
//
// When replacing an existing file, the new file's permissions are perm with
// any bits the old file didn't have removed. A write never loosens
// permissions the user tightened, such as on a config holding API keys.
func WriteFunc(path string, perm fs.FileMode, write func(w io.Writer) error) (err error) {
	target, err := resolve(path)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if info, err := os.Stat(target); err == nil {
		perm &= info.Mode().Perm()
	}

	dir, base := filepath.Split(target)
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
	if err := rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// Syncing the directory makes the rename itself durable. It's best
	// effort: the new contents are already in place, and some filesystems
	// (FUSE, network mounts) reject directory syncs. Reporting failure here
	// would make callers think a committed write didn't happen.
	_ = syncDir(dir)
	return nil
}

// resolve follows symlinks in path. A path that doesn't exist yet is
// returned as is.
func resolve(path string) (string, error) {
	target, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, nil
	}
	return target, err
}
