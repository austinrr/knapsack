//go:build !windows

package atomicfile

import "os"

// syncDir flushes a directory's entries, making a rename in it durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}
