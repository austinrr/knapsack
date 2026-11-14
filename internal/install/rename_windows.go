package install

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// errSharingViolation is ERROR_SHARING_VIOLATION, which package syscall
// doesn't name.
const errSharingViolation syscall.Errno = 32

// rename retries os.Rename for about two seconds while the error is one an
// open file causes: antivirus scanning freshly extracted files, or the game
// holding an addon's files (DESIGN.md §6.3). If it still fails that way,
// the error wraps ErrInUse.
func rename(oldpath, newpath string) error {
	for delay := time.Millisecond; ; delay *= 2 {
		err := os.Rename(oldpath, newpath)
		if err == nil || !isTransient(err) {
			return err
		}
		if delay > time.Second {
			return fmt.Errorf("%w: %w", ErrInUse, err)
		}
		time.Sleep(delay)
	}
}

func isTransient(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) &&
		(errno == syscall.ERROR_ACCESS_DENIED || errno == errSharingViolation)
}
