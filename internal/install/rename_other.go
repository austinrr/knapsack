//go:build !windows

package install

import "os"

// rename is os.Rename. Unix lets a folder be renamed while files in it are
// open, so there's nothing to retry.
func rename(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}
